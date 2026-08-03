using System;
using System.IO;
using System.Text.Json;
using IHomeland.Client.Application.Battle;

namespace IHomeland.Client.Infrastructure.Battle
{
    /// <summary>
    /// 拥有固定 32-byte BattleTicket secret，并在所有释放路径清零。
    /// </summary>
    internal sealed class ClientBattleSecretBuffer : IDisposable
    {
        /// <summary>
        /// 保存尚未释放的 caller-owned bytes。
        /// </summary>
        private byte[] _bytes;

        /// <summary>
        /// 接受 codec 直接解码完成的 exact 32-byte buffer。
        /// </summary>
        /// <param name="bytes">所有权转移给当前实例的 secret。</param>
        internal ClientBattleSecretBuffer(byte[] bytes)
        {
            if (bytes == null || bytes.Length != ClientBattleNativeCrypto.KeyBytes)
            {
                throw new ArgumentException(
                    "BattleTicket secret width mismatched",
                    nameof(bytes));
            }

            _bytes = bytes;
        }

        /// <summary>
        /// 获取 current owner 内部使用的 exact secret buffer。
        /// </summary>
        /// <returns>不可在 owner 外保存的 32-byte buffer。</returns>
        internal byte[] RequireBytes()
        {
            var bytes = _bytes;
            if (bytes == null)
            {
                throw new ObjectDisposedException(nameof(ClientBattleSecretBuffer));
            }

            return bytes;
        }

        /// <summary>
        /// 清零并释放 secret；重复调用安全。
        /// </summary>
        public void Dispose()
        {
            var bytes = System.Threading.Interlocked.Exchange(ref _bytes, null);
            if (bytes != null)
            {
                ClientBattleNativeCrypto.SecureZero(bytes);
            }
        }

        /// <summary>
        /// 返回固定脱敏文本。
        /// </summary>
        /// <returns>不包含 secret bytes 的稳定值。</returns>
        public override string ToString()
        {
            return "ClientBattleSecretBuffer[REDACTED]";
        }
    }

    /// <summary>
    /// 保存 HTTPS BattleTicket 的一次性 credential 与已验证低敏 binding。
    /// </summary>
    internal sealed class ClientBattleTicketMaterial : IDisposable
    {
        /// <summary>
        /// 保存尚未转移的 secret owner。
        /// </summary>
        private ClientBattleSecretBuffer _secret;

        /// <summary>
        /// 创建已通过 closed response 校验的 ticket material。
        /// </summary>
        /// <param name="ticketID">Decoded 16-byte public lookup identity。</param>
        /// <param name="secret">Decoded 32-byte bearer owner。</param>
        /// <param name="host">Trusted UDP host。</param>
        /// <param name="port">Trusted UDP port。</param>
        /// <param name="role">Authoritative role。</param>
        /// <param name="targetKind">Canonical target selector。</param>
        /// <param name="targetRevision">Current SimulationTarget revision。</param>
        /// <param name="expiresAtMilliseconds">Absolute Unix expiry。</param>
        internal ClientBattleTicketMaterial(
            byte[] ticketID,
            ClientBattleSecretBuffer secret,
            string host,
            int port,
            ClientBattleRole role,
            ClientBattleTargetKind targetKind,
            ulong targetRevision,
            long expiresAtMilliseconds)
        {
            if (ticketID == null || ticketID.Length != 16)
            {
                throw new ArgumentException(
                    "BattleTicket identity width mismatched",
                    nameof(ticketID));
            }

            if (string.IsNullOrWhiteSpace(host) ||
                host.Length > 253 ||
                port < 1 ||
                port > ushort.MaxValue ||
                targetRevision == 0 ||
                expiresAtMilliseconds <= 0)
            {
                throw new ArgumentException("BattleTicket binding is invalid");
            }

            TicketID = ticketID;
            _secret = secret ?? throw new ArgumentNullException(nameof(secret));
            Host = host;
            Port = port;
            Role = role;
            TargetKind = targetKind;
            TargetRevision = targetRevision;
            ExpiresAtMilliseconds = expiresAtMilliseconds;
        }

        /// <summary>获取 decoded 16-byte public lookup identity。</summary>
        internal byte[] TicketID { get; }

        /// <summary>获取 trusted UDP host。</summary>
        internal string Host { get; }

        /// <summary>获取 trusted UDP port。</summary>
        internal int Port { get; }

        /// <summary>获取 authoritative role。</summary>
        internal ClientBattleRole Role { get; }

        /// <summary>获取 canonical target selector。</summary>
        internal ClientBattleTargetKind TargetKind { get; }

        /// <summary>获取 current SimulationTarget revision。</summary>
        internal ulong TargetRevision { get; }

        /// <summary>获取 absolute Unix expiry。</summary>
        internal long ExpiresAtMilliseconds { get; }

        /// <summary>
        /// 把 secret ownership 一次性转移给紧邻 connect attempt。
        /// </summary>
        /// <returns>唯一可用的 fixed secret owner。</returns>
        internal ClientBattleSecretBuffer TakeSecret()
        {
            var secret = System.Threading.Interlocked.Exchange(ref _secret, null);
            if (secret == null)
            {
                throw new InvalidOperationException(
                    "BattleTicket secret is unavailable");
            }

            return secret;
        }

        /// <summary>
        /// 清除尚未转移的 secret；重复调用安全。
        /// </summary>
        public void Dispose()
        {
            var secret = System.Threading.Interlocked.Exchange(ref _secret, null);
            secret?.Dispose();
            Array.Clear(TicketID, 0, TicketID.Length);
        }

        /// <summary>
        /// 返回固定脱敏文本。
        /// </summary>
        /// <returns>不包含 identity、endpoint 或 secret 的稳定值。</returns>
        public override string ToString()
        {
            return "ClientBattleTicketMaterial[REDACTED]";
        }
    }

    /// <summary>
    /// 以 Utf8JsonReader 直接解析 closed BattleTicket response，并把 secret 解码到可清零 buffer。
    /// </summary>
    internal sealed class ClientBattleTicketCodec
    {
        /// <summary>
        /// 编码 OWN_WORLD/VISIT_WORLD closed selector。
        /// </summary>
        /// <param name="intent">Current immutable target intent。</param>
        /// <returns>只包含 kind 与可选 visitSessionId 的 UTF-8 JSON。</returns>
        internal byte[] EncodeRequest(ClientBattleTargetIntent intent)
        {
            if (intent == null)
            {
                throw new ArgumentNullException(nameof(intent));
            }

            using (var stream = new MemoryStream(160))
            {
                using (var writer = new Utf8JsonWriter(stream))
                {
                    writer.WriteStartObject();
                    switch (intent.Kind)
                    {
                        case ClientBattleTargetKind.OwnWorld:
                            writer.WriteString("kind", "OWN_WORLD");
                            break;
                        case ClientBattleTargetKind.VisitWorld:
                            writer.WriteString("kind", "VISIT_WORLD");
                            writer.WriteString(
                                "visitSessionId",
                                intent.VisitSessionID);
                            break;
                        default:
                            throw new ArgumentOutOfRangeException(nameof(intent));
                    }

                    writer.WriteEndObject();
                }

                return stream.ToArray();
            }
        }

        /// <summary>
        /// 解码单一 closed response，并验证 role/target 与请求 selector 一致。
        /// </summary>
        /// <param name="body">不超过 operation limit 的 raw UTF-8 body。</param>
        /// <param name="intent">签发请求绑定的 immutable target。</param>
        /// <returns>拥有 decoded secret 的 ticket material。</returns>
        internal ClientBattleTicketMaterial Decode(
            ReadOnlySpan<byte> body,
            ClientBattleTargetIntent intent)
        {
            if (body.IsEmpty || body.Length > 16 * 1024)
            {
                throw new ClientBattleTicketCodecException(
                    "BattleTicket response size is invalid");
            }

            if (intent == null)
            {
                throw new ArgumentNullException(nameof(intent));
            }

            byte[] ticketID = null;
            byte[] secretBytes = null;
            try
            {
                var reader = new Utf8JsonReader(
                    body,
                    new JsonReaderOptions
                    {
                        AllowTrailingCommas = false,
                        CommentHandling = JsonCommentHandling.Disallow,
                        MaxDepth = 4,
                    });
                RequireRead(ref reader, JsonTokenType.StartObject);

                var seenTicketID = false;
                var seenSecret = false;
                var seenEndpoint = false;
                var seenWireSuite = false;
                var seenRole = false;
                var seenTargetKind = false;
                var seenTargetRevision = false;
                var seenExpiry = false;
                string host = null;
                var port = 0;
                var role = ClientBattleRole.None;
                var targetKind = default(ClientBattleTargetKind);
                ulong targetRevision = 0;
                long expiresAtMilliseconds = 0;

                while (reader.Read() && reader.TokenType != JsonTokenType.EndObject)
                {
                    RequireToken(reader.TokenType, JsonTokenType.PropertyName);
                    if (reader.ValueTextEquals("ticketId"))
                    {
                        RejectDuplicate(ref seenTicketID);
                        RequireRead(ref reader, JsonTokenType.String);
                        ticketID = DecodePrefixedBase64Url(
                            reader.ValueSpan,
                            "btk1_",
                            16,
                            "ticketId");
                    }
                    else if (reader.ValueTextEquals("ticketSecret"))
                    {
                        RejectDuplicate(ref seenSecret);
                        RequireRead(ref reader, JsonTokenType.String);
                        secretBytes = DecodePrefixedBase64Url(
                            reader.ValueSpan,
                            "bts1_",
                            ClientBattleNativeCrypto.KeyBytes,
                            "ticketSecret");
                    }
                    else if (reader.ValueTextEquals("endpoint"))
                    {
                        RejectDuplicate(ref seenEndpoint);
                        RequireRead(ref reader, JsonTokenType.StartObject);
                        ReadEndpoint(ref reader, out host, out port);
                    }
                    else if (reader.ValueTextEquals("wireSuite"))
                    {
                        RejectDuplicate(ref seenWireSuite);
                        RequireRead(ref reader, JsonTokenType.StartObject);
                        ReadWireSuite(ref reader);
                    }
                    else if (reader.ValueTextEquals("role"))
                    {
                        RejectDuplicate(ref seenRole);
                        RequireRead(ref reader, JsonTokenType.String);
                        role = reader.ValueTextEquals("OWNER")
                            ? ClientBattleRole.Owner
                            : reader.ValueTextEquals("VISITOR")
                                ? ClientBattleRole.Visitor
                                : throw new ClientBattleTicketCodecException(
                                    "BattleTicket role is invalid");
                    }
                    else if (reader.ValueTextEquals("targetKind"))
                    {
                        RejectDuplicate(ref seenTargetKind);
                        RequireRead(ref reader, JsonTokenType.String);
                        targetKind = reader.ValueTextEquals("OWN_WORLD")
                            ? ClientBattleTargetKind.OwnWorld
                            : reader.ValueTextEquals("VISIT_WORLD")
                                ? ClientBattleTargetKind.VisitWorld
                                : throw new ClientBattleTicketCodecException(
                                    "BattleTicket target kind is invalid");
                    }
                    else if (reader.ValueTextEquals("targetRevision"))
                    {
                        RejectDuplicate(ref seenTargetRevision);
                        RequireRead(ref reader, JsonTokenType.Number);
                        if (!reader.TryGetUInt64(out targetRevision) ||
                            targetRevision == 0)
                        {
                            throw new ClientBattleTicketCodecException(
                                "BattleTicket target revision is invalid");
                        }
                    }
                    else if (reader.ValueTextEquals("expiresAtMs"))
                    {
                        RejectDuplicate(ref seenExpiry);
                        RequireRead(ref reader, JsonTokenType.Number);
                        if (!reader.TryGetInt64(out expiresAtMilliseconds) ||
                            expiresAtMilliseconds <= 0)
                        {
                            throw new ClientBattleTicketCodecException(
                                "BattleTicket expiry is invalid");
                        }
                    }
                    else
                    {
                        throw new ClientBattleTicketCodecException(
                            "BattleTicket response contains an unknown field");
                    }
                }

                if (reader.TokenType != JsonTokenType.EndObject ||
                    reader.Read() ||
                    !seenTicketID ||
                    !seenSecret ||
                    !seenEndpoint ||
                    !seenWireSuite ||
                    !seenRole ||
                    !seenTargetKind ||
                    !seenTargetRevision ||
                    !seenExpiry ||
                    ticketID == null ||
                    secretBytes == null ||
                    (targetKind == ClientBattleTargetKind.OwnWorld &&
                     role != ClientBattleRole.Owner) ||
                    (targetKind == ClientBattleTargetKind.VisitWorld &&
                     role != ClientBattleRole.Visitor) ||
                    targetKind != intent.Kind)
                {
                    throw new ClientBattleTicketCodecException(
                        "BattleTicket response binding is incomplete");
                }

                ClientBattleSecretBuffer secret = null;
                try
                {
                    secret = new ClientBattleSecretBuffer(secretBytes);
                    secretBytes = null;
                    var material = new ClientBattleTicketMaterial(
                        ticketID,
                        secret,
                        host,
                        port,
                        role,
                        targetKind,
                        targetRevision,
                        expiresAtMilliseconds);
                    secret = null;
                    ticketID = null;
                    return material;
                }
                finally
                {
                    secret?.Dispose();
                }
            }
            finally
            {
                if (secretBytes != null)
                {
                    Array.Clear(secretBytes, 0, secretBytes.Length);
                }

                if (ticketID != null)
                {
                    Array.Clear(ticketID, 0, ticketID.Length);
                }
            }
        }

        /// <summary>
        /// 读取 closed UDP endpoint object。
        /// </summary>
        /// <param name="reader">已位于 StartObject 的 reader。</param>
        /// <param name="host">验证后的 trusted host。</param>
        /// <param name="port">验证后的 UDP port。</param>
        private static void ReadEndpoint(
            ref Utf8JsonReader reader,
            out string host,
            out int port)
        {
            var seenTransport = false;
            var seenHost = false;
            var seenPort = false;
            host = null;
            port = 0;
            while (reader.Read() && reader.TokenType != JsonTokenType.EndObject)
            {
                RequireToken(reader.TokenType, JsonTokenType.PropertyName);
                if (reader.ValueTextEquals("transport"))
                {
                    RejectDuplicate(ref seenTransport);
                    RequireRead(ref reader, JsonTokenType.String);
                    if (!reader.ValueTextEquals("UDP"))
                    {
                        throw new ClientBattleTicketCodecException(
                            "BattleTicket endpoint transport is invalid");
                    }
                }
                else if (reader.ValueTextEquals("host"))
                {
                    RejectDuplicate(ref seenHost);
                    RequireRead(ref reader, JsonTokenType.String);
                    host = reader.GetString();
                    if (!IsSafeHost(host))
                    {
                        throw new ClientBattleTicketCodecException(
                            "BattleTicket endpoint host is invalid");
                    }
                }
                else if (reader.ValueTextEquals("port"))
                {
                    RejectDuplicate(ref seenPort);
                    RequireRead(ref reader, JsonTokenType.Number);
                    if (!reader.TryGetInt32(out port) ||
                        port < 1 ||
                        port > ushort.MaxValue)
                    {
                        throw new ClientBattleTicketCodecException(
                            "BattleTicket endpoint port is invalid");
                    }
                }
                else
                {
                    throw new ClientBattleTicketCodecException(
                        "BattleTicket endpoint contains an unknown field");
                }
            }

            if (!seenTransport || !seenHost || !seenPort ||
                reader.TokenType != JsonTokenType.EndObject)
            {
                throw new ClientBattleTicketCodecException(
                    "BattleTicket endpoint is incomplete");
            }
        }

        /// <summary>
        /// 读取并精确验证 wire version 与三个 algorithm。
        /// </summary>
        /// <param name="reader">已位于 StartObject 的 reader。</param>
        private static void ReadWireSuite(ref Utf8JsonReader reader)
        {
            var seenVersion = false;
            var seenKeyAgreement = false;
            var seenKdf = false;
            var seenAead = false;
            while (reader.Read() && reader.TokenType != JsonTokenType.EndObject)
            {
                RequireToken(reader.TokenType, JsonTokenType.PropertyName);
                if (reader.ValueTextEquals("wireVersion"))
                {
                    RejectDuplicate(ref seenVersion);
                    RequireRead(ref reader, JsonTokenType.Number);
                    if (!reader.TryGetByte(out var version) || version != 1)
                    {
                        throw new ClientBattleTicketCodecException(
                            "BattleTicket wire version is invalid");
                    }
                }
                else if (reader.ValueTextEquals("keyAgreement"))
                {
                    RejectDuplicate(ref seenKeyAgreement);
                    RequireRead(ref reader, JsonTokenType.String);
                    if (!reader.ValueTextEquals("X25519"))
                    {
                        throw new ClientBattleTicketCodecException(
                            "BattleTicket key agreement is invalid");
                    }
                }
                else if (reader.ValueTextEquals("kdf"))
                {
                    RejectDuplicate(ref seenKdf);
                    RequireRead(ref reader, JsonTokenType.String);
                    if (!reader.ValueTextEquals("HKDF-SHA-256"))
                    {
                        throw new ClientBattleTicketCodecException(
                            "BattleTicket KDF is invalid");
                    }
                }
                else if (reader.ValueTextEquals("aead"))
                {
                    RejectDuplicate(ref seenAead);
                    RequireRead(ref reader, JsonTokenType.String);
                    if (!reader.ValueTextEquals("ChaCha20-Poly1305"))
                    {
                        throw new ClientBattleTicketCodecException(
                            "BattleTicket AEAD is invalid");
                    }
                }
                else
                {
                    throw new ClientBattleTicketCodecException(
                        "BattleTicket wire suite contains an unknown field");
                }
            }

            if (!seenVersion ||
                !seenKeyAgreement ||
                !seenKdf ||
                !seenAead ||
                reader.TokenType != JsonTokenType.EndObject)
            {
                throw new ClientBattleTicketCodecException(
                    "BattleTicket wire suite is incomplete");
            }
        }

        /// <summary>
        /// 把 canonical prefixed base64url token 直接解码到 exact buffer。
        /// </summary>
        /// <param name="encoded">Utf8JsonReader 的未转 string token bytes。</param>
        /// <param name="prefix">固定非秘密 ASCII prefix。</param>
        /// <param name="outputLength">精确 decoded 宽度。</param>
        /// <param name="field">低敏字段名。</param>
        /// <returns>Caller 必须清零的 decoded bytes。</returns>
        private static byte[] DecodePrefixedBase64Url(
            ReadOnlySpan<byte> encoded,
            string prefix,
            int outputLength,
            string field)
        {
            if (encoded.IndexOf((byte)'\\') >= 0 ||
                encoded.Length <= prefix.Length)
            {
                throw new ClientBattleTicketCodecException(
                    $"BattleTicket {field} encoding is invalid");
            }

            for (var index = 0; index < prefix.Length; index++)
            {
                if (encoded[index] != (byte)prefix[index])
                {
                    throw new ClientBattleTicketCodecException(
                        $"BattleTicket {field} prefix is invalid");
                }
            }

            var payload = encoded.Slice(prefix.Length);
            var expectedEncodedLength = (outputLength * 8 + 5) / 6;
            if (payload.Length != expectedEncodedLength)
            {
                throw new ClientBattleTicketCodecException(
                    $"BattleTicket {field} width is invalid");
            }

            var output = new byte[outputLength];
            try
            {
                uint accumulator = 0;
                var bits = 0;
                var outputIndex = 0;
                foreach (var encodedByte in payload)
                {
                    var value = Base64UrlValue(encodedByte);
                    if (value < 0)
                    {
                        throw new ClientBattleTicketCodecException(
                            $"BattleTicket {field} alphabet is invalid");
                    }

                    accumulator = (accumulator << 6) | (uint)value;
                    bits += 6;
                    if (bits >= 8)
                    {
                        bits -= 8;
                        if (outputIndex >= output.Length)
                        {
                            throw new ClientBattleTicketCodecException(
                                $"BattleTicket {field} overflowed");
                        }

                        output[outputIndex++] =
                            (byte)(accumulator >> bits);
                        accumulator &= bits == 0
                            ? 0U
                            : (1U << bits) - 1U;
                    }
                }

                if (outputIndex != output.Length || accumulator != 0)
                {
                    throw new ClientBattleTicketCodecException(
                        $"BattleTicket {field} is not canonical");
                }

                return output;
            }
            catch
            {
                Array.Clear(output, 0, output.Length);
                throw;
            }
        }

        /// <summary>
        /// 解码单个 base64url alphabet byte。
        /// </summary>
        /// <param name="value">ASCII byte。</param>
        /// <returns>0-63，非法时为 -1。</returns>
        private static int Base64UrlValue(byte value)
        {
            if (value >= (byte)'A' && value <= (byte)'Z')
            {
                return value - (byte)'A';
            }

            if (value >= (byte)'a' && value <= (byte)'z')
            {
                return value - (byte)'a' + 26;
            }

            if (value >= (byte)'0' && value <= (byte)'9')
            {
                return value - (byte)'0' + 52;
            }

            return value == (byte)'-'
                ? 62
                : value == (byte)'_'
                    ? 63
                    : -1;
        }

        /// <summary>
        /// 要求 reader 读到 exact token。
        /// </summary>
        /// <param name="reader">Current streaming reader。</param>
        /// <param name="expected">Expected token kind。</param>
        private static void RequireRead(
            ref Utf8JsonReader reader,
            JsonTokenType expected)
        {
            if (!reader.Read())
            {
                throw new ClientBattleTicketCodecException(
                    "BattleTicket response ended early");
            }

            RequireToken(reader.TokenType, expected);
        }

        /// <summary>
        /// 要求 current token kind 精确匹配。
        /// </summary>
        /// <param name="actual">Actual token kind。</param>
        /// <param name="expected">Expected token kind。</param>
        private static void RequireToken(
            JsonTokenType actual,
            JsonTokenType expected)
        {
            if (actual != expected)
            {
                throw new ClientBattleTicketCodecException(
                    "BattleTicket response token is invalid");
            }
        }

        /// <summary>
        /// 拒绝同一 closed object 的 duplicate property。
        /// </summary>
        /// <param name="seen">Property presence slot。</param>
        private static void RejectDuplicate(ref bool seen)
        {
            if (seen)
            {
                throw new ClientBattleTicketCodecException(
                    "BattleTicket response contains a duplicate field");
            }

            seen = true;
        }

        /// <summary>
        /// 验证 host 不含 URI delimiter、控制字符或非 ASCII。
        /// </summary>
        /// <param name="host">待验证 host。</param>
        /// <returns>符合 trusted endpoint grammar 时为 true。</returns>
        private static bool IsSafeHost(string host)
        {
            if (string.IsNullOrWhiteSpace(host) || host.Length > 253)
            {
                return false;
            }

            foreach (var character in host)
            {
                if (character < 0x21 ||
                    character > 0x7e ||
                    character == '/' ||
                    character == '\\' ||
                    character == '?' ||
                    character == '#')
                {
                    return false;
                }
            }

            return true;
        }
    }

    /// <summary>
    /// 表示 BattleTicket response 违反 closed codec，不包含 raw body 或 secret。
    /// </summary>
    internal sealed class ClientBattleTicketCodecException : Exception
    {
        /// <summary>
        /// 创建固定低敏 codec failure。
        /// </summary>
        /// <param name="message">不得包含原始字段值的诊断。</param>
        internal ClientBattleTicketCodecException(string message)
            : base(message)
        {
        }
    }
}
