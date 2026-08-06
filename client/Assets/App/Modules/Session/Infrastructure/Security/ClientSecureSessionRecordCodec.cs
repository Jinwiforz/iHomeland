using System;
using System.IO;
using System.Text;
using IHomeland.Client.Session.Application;
using IHomeland.Client.Networking.Application.Contracts;
using IHomeland.Client.Networking.Infrastructure.Http;

namespace IHomeland.Client.Session.Infrastructure.Security
{
    /// <summary>
    /// 对 secure session plaintext 执行封闭、版本化 binary 编解码。
    /// </summary>
    internal sealed class ClientSecureSessionRecordCodec
    {
        /// <summary>限制解保护后的完整 record bytes。</summary>
        internal const int MaximumPlaintextBytes = 16384;

        /// <summary>拒绝 UTF-8 replacement fallback，防止损坏 bytes 被静默修复。</summary>
        private static readonly Encoding StrictUtf8 = new UTF8Encoding(
            encoderShouldEmitUTF8Identifier: false,
            throwOnInvalidBytes: true);

        /// <summary>编码 application-owned record。</summary>
        /// <param name="record">完整有效 record。</param>
        /// <returns>由调用方负责归零的 plaintext bytes。</returns>
        internal byte[] Encode(ClientSecureSessionRecord record)
        {
            if (record == null)
            {
                throw new ArgumentNullException(nameof(record));
            }

            using (var stream = new MemoryStream())
            using (var writer = new BinaryWriter(stream, StrictUtf8, leaveOpen: true))
            {
                writer.Write(record.SchemaVersion);
                WriteString(writer, record.EnvironmentBinding, 64);
                WriteString(writer, record.Account.AccountID, 128);
                WriteString(writer, record.Account.DisplayName, 128);
                writer.Write(record.Account.CreatedAtMilliseconds);
                WriteString(writer, record.Session.SessionID, 128);
                writer.Write(record.Session.SessionEpoch);
                writer.Write(record.Session.ExpiresAtMilliseconds);
                WriteString(writer, record.RefreshToken, ClientSecureSessionRecord.MaximumRefreshTokenLength);
                writer.Write(record.RefreshExpiresAtMilliseconds);
                writer.Flush();
                if (stream.Length <= 0 || stream.Length > MaximumPlaintextBytes)
                {
                    throw new InvalidDataException("Secure session plaintext size 无效。");
                }

                return stream.ToArray();
            }
        }

        /// <summary>严格解码完整 plaintext。</summary>
        /// <param name="plaintext">由调用方继续拥有并负责归零的 bytes。</param>
        /// <param name="record">成功时返回 application-owned record。</param>
        /// <returns>Schema、字段、结尾与record contract均有效时返回 true。</returns>
        internal bool TryDecode(byte[] plaintext, out ClientSecureSessionRecord record)
        {
            record = null;
            if (plaintext == null || plaintext.Length <= 0 || plaintext.Length > MaximumPlaintextBytes)
            {
                return false;
            }

            try
            {
                using (var stream = new MemoryStream(plaintext, writable: false))
                using (var reader = new BinaryReader(stream, StrictUtf8, leaveOpen: true))
                {
                    var schemaVersion = reader.ReadInt32();
                    var binding = ReadString(reader, 64, exactLength: 64);
                    var accountID = ReadString(reader, 128);
                    var displayName = ReadString(reader, 128);
                    var createdAt = reader.ReadInt64();
                    var sessionID = ReadString(reader, 128);
                    var sessionEpoch = reader.ReadInt64();
                    var sessionExpiresAt = reader.ReadInt64();
                    var refreshToken = ReadString(
                        reader,
                        ClientSecureSessionRecord.MaximumRefreshTokenLength);
                    var refreshExpiresAt = reader.ReadInt64();
                    if (stream.Position != stream.Length)
                    {
                        return false;
                    }

                    record = new ClientSecureSessionRecord(
                        schemaVersion,
                        binding,
                        new ClientAccountSummary(accountID, displayName, createdAt),
                        new ClientSessionSummary(sessionID, sessionEpoch, sessionExpiresAt),
                        refreshToken,
                        refreshExpiresAt);
                    return true;
                }
            }
            catch (EndOfStreamException)
            {
                return false;
            }
            catch (IOException)
            {
                return false;
            }
            catch (DecoderFallbackException)
            {
                return false;
            }
            catch (ArgumentException)
            {
                return false;
            }
        }

        /// <summary>写入显式 byte length 的严格 UTF-8 字段。</summary>
        /// <param name="writer">当前 binary writer。</param>
        /// <param name="value">待编码文本。</param>
        /// <param name="maximumCharacters">字段最大字符数。</param>
        private static void WriteString(
            BinaryWriter writer,
            string value,
            int maximumCharacters)
        {
            if (value == null || value.Length <= 0 || value.Length > maximumCharacters)
            {
                throw new InvalidDataException("Secure session string 长度无效。");
            }

            var bytes = StrictUtf8.GetBytes(value);
            try
            {
                if (bytes.Length <= 0 || bytes.Length > maximumCharacters * 4)
                {
                    throw new InvalidDataException("Secure session UTF-8 字段过大。");
                }

                writer.Write(bytes.Length);
                writer.Write(bytes);
            }
            finally
            {
                Array.Clear(bytes, 0, bytes.Length);
            }
        }

        /// <summary>读取并严格限制 UTF-8 字段。</summary>
        /// <param name="reader">当前 binary reader。</param>
        /// <param name="maximumCharacters">字段最大字符数。</param>
        /// <param name="exactLength">非零时要求解码字符数精确相等。</param>
        /// <returns>完整解码文本。</returns>
        private static string ReadString(
            BinaryReader reader,
            int maximumCharacters,
            int exactLength = 0)
        {
            var length = reader.ReadInt32();
            if (length <= 0 || length > maximumCharacters * 4)
            {
                throw new InvalidDataException("Secure session UTF-8 length 无效。");
            }

            var bytes = reader.ReadBytes(length);
            try
            {
                if (bytes.Length != length)
                {
                    throw new EndOfStreamException();
                }

                var value = StrictUtf8.GetString(bytes);
                if (value.Length <= 0 || value.Length > maximumCharacters ||
                    (exactLength > 0 && value.Length != exactLength))
                {
                    throw new InvalidDataException("Secure session string contract 无效。");
                }

                return value;
            }
            finally
            {
                Array.Clear(bytes, 0, bytes.Length);
            }
        }
    }
}
