using System;
using System.Buffers.Binary;
using System.IO;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Application.Contracts;
using IHomeland.Client.Infrastructure.Http;

namespace IHomeland.Client.Infrastructure.Tcp
{
    /// <summary>
    /// 表示 peer 或本地 codec 违反冻结 gameplay wire contract。
    /// </summary>
    internal sealed class ClientGameplayProtocolException : Exception
    {
        /// <summary>创建不包含 credential、payload 或 endpoint 的协议异常。</summary>
        /// <param name="message">稳定的本地约束说明。</param>
        internal ClientGameplayProtocolException(string message)
            : base(message)
        {
        }

        /// <summary>包装底层解析异常且不复制其不可信文本。</summary>
        /// <param name="message">稳定的本地约束说明。</param>
        /// <param name="innerException">只用于本地异常链的底层错误。</param>
        internal ClientGameplayProtocolException(string message, Exception innerException)
            : base(message, innerException)
        {
        }
    }

    /// <summary>
    /// 编码服务端冻结的 `IHTP` v1 双凭据 authentication preface。
    /// </summary>
    internal static class ClientGameplayPreface
    {
        /// <summary>Preface payload 的固定字节数，不含 4-byte frame prefix。</summary>
        internal const int PayloadBytes = 91;

        /// <summary>编码带 4-byte big-endian 长度的完整 preface frame。</summary>
        /// <param name="ticket">32 字节小写十六进制 ticket 文本。</param>
        /// <param name="admission">48 字节规范 `wad1_` credential。</param>
        /// <param name="purpose">服务端权威连接用途。</param>
        /// <returns>可由 serialized writer 一次完整写入的 frame。</returns>
        /// <exception cref="ClientGameplayProtocolException">任一 credential 或 purpose 不符合 contract 时抛出。</exception>
        internal static byte[] Encode(
            string ticket,
            string admission,
            ClientWorldAdmissionPurpose purpose)
        {
            if (!IsLowerHex(ticket, 32) || !IsAdmission(admission))
            {
                throw new ClientGameplayProtocolException("Gameplay preface credential grammar 无效。");
            }

            var purposeByte = purpose switch
            {
                ClientWorldAdmissionPurpose.OwnWorld => (byte)1,
                ClientWorldAdmissionPurpose.Join => (byte)2,
                ClientWorldAdmissionPurpose.Reconnect => (byte)3,
                _ => throw new ClientGameplayProtocolException("Gameplay preface purpose 无效。"),
            };
            var frame = new byte[4 + PayloadBytes];
            BinaryPrimitives.WriteUInt32BigEndian(frame.AsSpan(0, 4), (uint)PayloadBytes);
            Encoding.ASCII.GetBytes("IHTP", 0, 4, frame, 4);
            BinaryPrimitives.WriteUInt16BigEndian(frame.AsSpan(8, 2), 1);
            BinaryPrimitives.WriteUInt16BigEndian(frame.AsSpan(10, 2), 32);
            BinaryPrimitives.WriteUInt16BigEndian(frame.AsSpan(12, 2), 48);
            frame[14] = purposeByte;
            Encoding.ASCII.GetBytes(ticket, 0, ticket.Length, frame, 15);
            Encoding.ASCII.GetBytes(admission, 0, admission.Length, frame, 47);
            return frame;
        }

        /// <summary>验证固定长度小写十六进制文本。</summary>
        /// <param name="value">待验证文本。</param>
        /// <param name="length">要求的 ASCII 字节数。</param>
        /// <returns>完全符合时返回 true。</returns>
        private static bool IsLowerHex(string value, int length)
        {
            if (value == null || value.Length != length)
            {
                return false;
            }

            foreach (var character in value)
            {
                if (!(character >= '0' && character <= '9') &&
                    !(character >= 'a' && character <= 'f'))
                {
                    return false;
                }
            }

            return true;
        }

        /// <summary>验证当前 `wad1_` credential 的固定安全 ASCII 形式。</summary>
        /// <param name="value">待验证 admission。</param>
        /// <returns>长度、前缀与字符集均有效时返回 true。</returns>
        private static bool IsAdmission(string value)
        {
            if (value == null || value.Length != 48 || !value.StartsWith("wad1_", StringComparison.Ordinal))
            {
                return false;
            }

            foreach (var character in value)
            {
                if (!(character >= 'A' && character <= 'Z') &&
                    !(character >= 'a' && character <= 'z') &&
                    !(character >= '0' && character <= '9') && character != '_' && character != '-')
                {
                    return false;
                }
            }

            return true;
        }
    }

    /// <summary>
    /// 对 gameplay stream 执行有界 4-byte big-endian framing 与 exact I/O。
    /// </summary>
    internal static class ClientGameplayFramer
    {
        /// <summary>冻结公开协议允许的最大完整 envelope 字节数。</summary>
        internal const int MaximumFrameBytes = 1024 * 1024;

        /// <summary>为完整 payload 增加长度前缀。</summary>
        /// <param name="payload">非空且不超过 hard cap 的 envelope bytes。</param>
        /// <returns>长度前缀与 payload 的独立副本。</returns>
        internal static byte[] Frame(byte[] payload)
        {
            if (payload == null || payload.Length == 0 || payload.Length > MaximumFrameBytes)
            {
                throw new ClientGameplayProtocolException("Gameplay frame 长度无效。");
            }

            var frame = new byte[4 + payload.Length];
            BinaryPrimitives.WriteUInt32BigEndian(frame.AsSpan(0, 4), (uint)payload.Length);
            Buffer.BlockCopy(payload, 0, frame, 4, payload.Length);
            return frame;
        }

        /// <summary>精确读取一个有界 frame body。</summary>
        /// <param name="connection">当前 connection 的唯一 reader。</param>
        /// <param name="maximumBytes">配置与全局上限中的较小值。</param>
        /// <param name="cancellationToken">连接生命周期取消信号。</param>
        /// <returns>不含 prefix 的完整 frame body。</returns>
        internal static async Task<byte[]> ReadFrameAsync(
            IClientGameplayConnection connection,
            int maximumBytes,
            CancellationToken cancellationToken)
        {
            if (connection == null)
            {
                throw new ArgumentNullException(nameof(connection));
            }

            if (maximumBytes <= 0 || maximumBytes > MaximumFrameBytes)
            {
                throw new ArgumentOutOfRangeException(nameof(maximumBytes));
            }

            var prefix = new byte[4];
            await ReadExactlyAsync(connection, prefix, cancellationToken);
            var length = BinaryPrimitives.ReadUInt32BigEndian(prefix);
            if (length == 0 || length > (uint)maximumBytes)
            {
                throw new ClientGameplayProtocolException("Gameplay frame 超出预算。");
            }

            var body = new byte[(int)length];
            await ReadExactlyAsync(connection, body, cancellationToken);
            return body;
        }

        /// <summary>循环读取直到 buffer 填满或 peer 关闭。</summary>
        /// <param name="connection">当前 connection 的唯一 reader。</param>
        /// <param name="buffer">长度已经通过预算验证的目标 buffer。</param>
        /// <param name="cancellationToken">连接生命周期取消信号。</param>
        /// <returns>Buffer 填满时完成。</returns>
        private static async Task ReadExactlyAsync(
            IClientGameplayConnection connection,
            byte[] buffer,
            CancellationToken cancellationToken)
        {
            var offset = 0;
            while (offset < buffer.Length)
            {
                var count = await connection.ReadAsync(
                    buffer,
                    offset,
                    buffer.Length - offset,
                    cancellationToken);
                if (count <= 0)
                {
                    throw new EndOfStreamException("Gameplay peer 已关闭连接。");
                }

                offset += count;
            }
        }
    }
}
