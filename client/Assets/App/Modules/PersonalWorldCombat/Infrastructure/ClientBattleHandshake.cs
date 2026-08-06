using System;
using System.Security.Cryptography;
using System.Text;
using IHomeland.Client.PersonalWorldCombat.Application;

namespace IHomeland.Client.PersonalWorldCombat.Infrastructure
{
    /// <summary>
    /// 保存认证 ServerAccept 后才能交给 secure transport 的单次 session material。
    /// </summary>
    internal sealed class ClientBattleSessionParameters : IDisposable
    {
        /// <summary>
        /// 保存尚未转移给 secure channel 的 session seed。
        /// </summary>
        private byte[] _sessionSeed;

        /// <summary>
        /// 保存不可变 session identity。
        /// </summary>
        private byte[] _sessionID;

        /// <summary>
        /// 保存 authority binding fingerprint。
        /// </summary>
        private byte[] _bindingFingerprint;

        /// <summary>
        /// 创建经过 closed plaintext 校验的 session parameters。
        /// </summary>
        /// <param name="sessionSeed">32-byte traffic seed。</param>
        /// <param name="sessionID">16-byte routing identity。</param>
        /// <param name="battleSessionGeneration">非零 battle session generation。</param>
        /// <param name="keyEpoch">初始 key epoch，必须为一。</param>
        /// <param name="endpointGeneration">初始 endpoint generation，必须为一。</param>
        /// <param name="actorSlot">服务端冻结的 0..7 actor slot。</param>
        /// <param name="role">认证角色。</param>
        /// <param name="bindingFingerprint">32-byte authority binding。</param>
        internal ClientBattleSessionParameters(
            byte[] sessionSeed,
            byte[] sessionID,
            uint battleSessionGeneration,
            uint keyEpoch,
            uint endpointGeneration,
            byte actorSlot,
            ClientBattleRole role,
            byte[] bindingFingerprint)
        {
            if (sessionSeed == null ||
                sessionSeed.Length != ClientBattleNativeCrypto.KeyBytes ||
                sessionID == null ||
                sessionID.Length != 16 ||
                battleSessionGeneration == 0 ||
                keyEpoch != 1 ||
                endpointGeneration != 1 ||
                actorSlot >= 8 ||
                role == ClientBattleRole.None ||
                bindingFingerprint == null ||
                bindingFingerprint.Length != ClientBattleNativeCrypto.KeyBytes)
            {
                throw new ArgumentException(
                    "client battle session parameters are invalid");
            }

            _sessionSeed = sessionSeed;
            _sessionID = sessionID;
            _bindingFingerprint = bindingFingerprint;
            BattleSessionGeneration = battleSessionGeneration;
            KeyEpoch = keyEpoch;
            EndpointGeneration = endpointGeneration;
            ActorSlot = actorSlot;
            Role = role;
        }

        /// <summary>获取 battle session generation。</summary>
        internal uint BattleSessionGeneration { get; }

        /// <summary>获取初始 key epoch。</summary>
        internal uint KeyEpoch { get; }

        /// <summary>获取初始 endpoint generation。</summary>
        internal uint EndpointGeneration { get; }

        /// <summary>获取服务端冻结的 actor slot。</summary>
        internal byte ActorSlot { get; }

        /// <summary>获取服务端认证角色。</summary>
        internal ClientBattleRole Role { get; }

        /// <summary>
        /// 从 SHA-256(session ID) 前八字节派生服务端一致的 nonzero KCP conversation。
        /// </summary>
        /// <returns>低 32-bit 非零时使用低位，否则使用高 32-bit。</returns>
        internal uint DeriveKcpConversation()
        {
            var sessionID = _sessionID;
            if (sessionID == null)
            {
                throw new ObjectDisposedException(
                    nameof(ClientBattleSessionParameters));
            }

            byte[] digest = null;
            try
            {
                using (var sha256 = SHA256.Create())
                {
                    digest = sha256.ComputeHash(sessionID);
                }

                var high = ReadUInt32BigEndian(digest, 0);
                var low = ReadUInt32BigEndian(digest, 4);
                var conversation = low != 0 ? low : high;
                if (conversation == 0)
                {
                    throw new InvalidOperationException(
                        "client battle KCP conversation is zero");
                }

                return conversation;
            }
            finally
            {
                if (digest != null)
                {
                    Array.Clear(digest, 0, digest.Length);
                }
            }
        }

        /// <summary>
        /// 一次性转移 session seed，并返回 secure header 需要的低敏 identity。
        /// </summary>
        /// <param name="sessionSeed">转移给 secure channel 的 32-byte seed。</param>
        /// <param name="sessionDigest">SHA-256(session ID) 前 8 bytes。</param>
        /// <param name="bindingDiscriminator">Binding fingerprint 前 8 bytes。</param>
        internal void TakeSecureMaterial(
            out byte[] sessionSeed,
            out byte[] sessionDigest,
            out byte[] bindingDiscriminator)
        {
            sessionSeed = System.Threading.Interlocked.Exchange(
                ref _sessionSeed,
                null);
            var sessionID = _sessionID;
            var binding = _bindingFingerprint;
            if (sessionSeed == null || sessionID == null || binding == null)
            {
                if (sessionSeed != null)
                {
                    ClientBattleNativeCrypto.SecureZero(sessionSeed);
                }

                throw new ObjectDisposedException(
                    nameof(ClientBattleSessionParameters));
            }

            byte[] digest = null;
            try
            {
                using (var sha256 = SHA256.Create())
                {
                    digest = sha256.ComputeHash(sessionID);
                }

                sessionDigest = new byte[8];
                bindingDiscriminator = new byte[8];
                Buffer.BlockCopy(digest, 0, sessionDigest, 0, 8);
                Buffer.BlockCopy(binding, 0, bindingDiscriminator, 0, 8);
            }
            catch
            {
                ClientBattleNativeCrypto.SecureZero(sessionSeed);
                sessionSeed = null;
                throw;
            }
            finally
            {
                if (digest != null)
                {
                    Array.Clear(digest, 0, digest.Length);
                }
            }
        }

        /// <summary>
        /// 清零尚未转移的 seed 与完整 identity；重复调用安全。
        /// </summary>
        public void Dispose()
        {
            var seed = System.Threading.Interlocked.Exchange(
                ref _sessionSeed,
                null);
            var sessionID = System.Threading.Interlocked.Exchange(
                ref _sessionID,
                null);
            var binding = System.Threading.Interlocked.Exchange(
                ref _bindingFingerprint,
                null);
            if (seed != null)
            {
                ClientBattleNativeCrypto.SecureZero(seed);
            }

            if (sessionID != null)
            {
                Array.Clear(sessionID, 0, sessionID.Length);
            }

            if (binding != null)
            {
                ClientBattleNativeCrypto.SecureZero(binding);
            }
        }

        /// <summary>
        /// 返回不包含 session 或 binding bytes 的稳定脱敏文本。
        /// </summary>
        /// <returns>低敏 session generation 与 actor slot。</returns>
        public override string ToString()
        {
            return $"ClientBattleSessionParameters[REDACTED] generation={BattleSessionGeneration} actorSlot={ActorSlot}";
        }

        /// <summary>
        /// 解码 network-order uint32。
        /// </summary>
        /// <param name="input">Source buffer。</param>
        /// <param name="offset">Value 起点。</param>
        /// <returns>Host-order value。</returns>
        private static uint ReadUInt32BigEndian(
            byte[] input,
            int offset)
        {
            return ((uint)input[offset] << 24) |
                   ((uint)input[offset + 1] << 16) |
                   ((uint)input[offset + 2] << 8) |
                   input[offset + 3];
        }
    }

    /// <summary>
    /// 独立实现 wire v1 ClientHello/Retry/ClientAuth/ServerAccept 的 closed 状态机。
    /// </summary>
    internal sealed class ClientBattleHandshake : IDisposable
    {
        /// <summary>保存 fixed ClientHello 宽度。</summary>
        internal const int ClientHelloBytes = 88;

        /// <summary>保存 fixed Retry 宽度。</summary>
        internal const int RetryBytes = 28;

        /// <summary>保存 fixed ClientAuth 宽度。</summary>
        internal const int ClientAuthBytes = 140;

        /// <summary>保存 fixed ServerAccept 宽度。</summary>
        internal const int ServerAcceptBytes = 156;

        /// <summary>保存 hello 到 accept 的总 deadline。</summary>
        internal const long HandshakeTimeoutMilliseconds = 3000;

        /// <summary>保存 ClientAuth proof 起点。</summary>
        private const int ClientAuthProofOffset = 108;

        /// <summary>保存 ServerAccept AAD 宽度。</summary>
        private const int AcceptAadBytes = 76;

        /// <summary>保存 ServerAccept plaintext 宽度。</summary>
        private const int AcceptPlaintextBytes = 64;

        /// <summary>保存 session schedule 宽度。</summary>
        private const int SessionScheduleBytes = 76;

        /// <summary>保存 proof transcript domain。</summary>
        private static readonly byte[] ProofDomain =
            Encoding.ASCII.GetBytes("ihomeland/battle/client-auth/v1");

        /// <summary>保存 ticket proof-key derivation domain。</summary>
        private static readonly byte[] ProofKeyDomain =
            Encoding.ASCII.GetBytes(
                "ihomeland/battle-ticket/proof-key/v2");

        /// <summary>保存 session schedule derivation domain。</summary>
        private static readonly byte[] ScheduleDomain =
            Encoding.ASCII.GetBytes(
                "ihomeland/battle/session-schedule/v1");

        /// <summary>串行化 transcript 状态。</summary>
        private readonly object _gate = new object();

        /// <summary>保存握手开始的 Unix 毫秒。</summary>
        private readonly long _startedAtMilliseconds;

        /// <summary>保存 ticket absolute expiry。</summary>
        private readonly long _expiresAtMilliseconds;

        /// <summary>保存 ticket 冻结的预期角色。</summary>
        private readonly ClientBattleRole _expectedRole;

        /// <summary>保存 client hello transcript。</summary>
        private byte[] _hello;

        /// <summary>保存 client auth transcript。</summary>
        private byte[] _auth;

        /// <summary>保存一次性 X25519 scalar。</summary>
        private byte[] _secretScalar;

        /// <summary>保存派生 proof key。</summary>
        private byte[] _proofKey;

        /// <summary>保存 client nonce。</summary>
        private byte[] _clientNonce;

        /// <summary>保存禁止跳步的 current handshake 状态。</summary>
        private HandshakeState _state;

        /// <summary>
        /// 接管 BattleTicket material，派生 proof key 并创建唯一 ClientHello。
        /// </summary>
        /// <param name="ticket">已验证且所有权转移的 ticket material。</param>
        /// <param name="target">签发 attempt 冻结的 target intent。</param>
        /// <param name="startedAtMilliseconds">单调调用方采样的 Unix 毫秒。</param>
        internal ClientBattleHandshake(
            ClientBattleTicketMaterial ticket,
            ClientBattleTargetIntent target,
            long startedAtMilliseconds)
        {
            if (ticket == null)
            {
                throw new ArgumentNullException(nameof(ticket));
            }

            if (target == null)
            {
                ticket.Dispose();
                throw new ArgumentNullException(nameof(target));
            }

            _startedAtMilliseconds = startedAtMilliseconds;
            _expiresAtMilliseconds = ticket.ExpiresAtMilliseconds;
            _expectedRole = ticket.Role;
            byte[] ticketID = null;
            ClientBattleSecretBuffer secret = null;
            try
            {
                if (startedAtMilliseconds <= 0 ||
                    _expiresAtMilliseconds <= startedAtMilliseconds ||
                    ticket.TargetKind != target.Kind ||
                    ticket.TargetRevision == 0 ||
                    (target.Kind == ClientBattleTargetKind.OwnWorld &&
                     ticket.Role != ClientBattleRole.Owner) ||
                    (target.Kind == ClientBattleTargetKind.VisitWorld &&
                     ticket.Role != ClientBattleRole.Visitor))
                {
                    throw new ArgumentException(
                        "client battle handshake binding is invalid");
                }

                ticketID = (byte[])ticket.TicketID.Clone();
                secret = ticket.TakeSecret();
                _proofKey = new byte[ClientBattleNativeCrypto.KeyBytes];
                ClientBattleNativeCrypto.HkdfSha256(
                    secret.RequireBytes(),
                    ticketID,
                    ProofKeyDomain,
                    _proofKey);

                _secretScalar = new byte[ClientBattleNativeCrypto.KeyBytes];
                var publicKey = new byte[ClientBattleNativeCrypto.KeyBytes];
                _clientNonce = new byte[ClientBattleNativeCrypto.KeyBytes];
                try
                {
                    ClientBattleNativeCrypto.RandomFill(_secretScalar);
                    ClientBattleNativeCrypto.X25519Public(
                        _secretScalar,
                        publicKey);
                    ClientBattleNativeCrypto.RandomFill(_clientNonce);
                    if (IsAllZero(ticketID) ||
                        IsAllZero(_proofKey) ||
                        IsAllZero(_secretScalar) ||
                        IsAllZero(publicKey) ||
                        IsAllZero(_clientNonce))
                    {
                        throw new InvalidOperationException(
                            "client battle handshake material is invalid");
                    }

                    _hello = new byte[ClientHelloBytes];
                    WriteMagic(_hello, "IHBH", 1);
                    Buffer.BlockCopy(ticketID, 0, _hello, 8, 16);
                    Buffer.BlockCopy(_clientNonce, 0, _hello, 24, 32);
                    Buffer.BlockCopy(publicKey, 0, _hello, 56, 32);
                    _auth = new byte[ClientAuthBytes];
                    _state = HandshakeState.HelloReady;
                }
                finally
                {
                    Array.Clear(publicKey, 0, publicKey.Length);
                }
            }
            catch
            {
                Dispose();
                throw;
            }
            finally
            {
                secret?.Dispose();
                ticket.Dispose();
                if (ticketID != null)
                {
                    Array.Clear(ticketID, 0, ticketID.Length);
                }
            }
        }

        /// <summary>
        /// 返回当前 transcript 的 caller-owned ClientHello。
        /// </summary>
        /// <returns>88-byte fixed handshake datagram。</returns>
        internal byte[] CreateClientHello()
        {
            lock (_gate)
            {
                if (_state != HandshakeState.HelloReady || _hello == null)
                {
                    throw new InvalidOperationException(
                        "client battle hello is unavailable");
                }

                return (byte[])_hello.Clone();
            }
        }

        /// <summary>
        /// 判断 UDP datagram 是否是 wire v1 fixed Retry envelope。
        /// </summary>
        /// <param name="response">待分类 datagram。</param>
        /// <returns>只在 header、epoch 与 cookie 全部合法时为 true。</returns>
        internal static bool MatchesRetryEnvelope(byte[] response)
        {
            return response != null &&
                   response.Length == RetryBytes &&
                   MatchesMagic(response, "IHBR", 2) &&
                   ReadUInt32BigEndian(response, 8) != 0 &&
                   !IsAllZero(response, 12, 16);
        }

        /// <summary>
        /// 判断 UDP datagram 是否是 wire v1 fixed ServerAccept envelope。
        /// </summary>
        /// <param name="response">待分类 datagram。</param>
        /// <returns>只在公开 envelope 完整合法时为 true。</returns>
        internal static bool MatchesServerAcceptEnvelope(byte[] response)
        {
            return response != null &&
                   response.Length == ServerAcceptBytes &&
                   MatchesMagic(response, "IHBS", 4) &&
                   response[72] == 0 &&
                   response[73] == AcceptPlaintextBytes &&
                   response[74] == 0 &&
                   response[75] == 0 &&
                   !IsAllZero(response, 40, 32);
        }

        /// <summary>
        /// 接受唯一 Retry 并生成包含 repeated hello、cookie 与 proof 的 ClientAuth。
        /// </summary>
        /// <param name="response">28-byte Retry datagram。</param>
        /// <param name="nowMilliseconds">当前 Unix 毫秒。</param>
        /// <param name="clientAuth">成功时返回 caller-owned ClientAuth。</param>
        /// <returns>状态、deadline 与 envelope 全部合法时为 true。</returns>
        internal bool TryAcceptRetry(
            byte[] response,
            long nowMilliseconds,
            out byte[] clientAuth)
        {
            clientAuth = null;
            lock (_gate)
            {
                if (_state != HandshakeState.HelloReady ||
                    !Alive(nowMilliseconds) ||
                    !MatchesRetryEnvelope(response))
                {
                    CloseLocked();
                    return false;
                }

                WriteMagic(_auth, "IHBA", 3);
                Buffer.BlockCopy(_hello, 8, _auth, 8, 80);
                Buffer.BlockCopy(response, 8, _auth, 88, 20);
                var proofMaterial = new byte[
                    ProofDomain.Length + ClientAuthProofOffset];
                var proof = new byte[ClientBattleNativeCrypto.KeyBytes];
                try
                {
                    Buffer.BlockCopy(
                        ProofDomain,
                        0,
                        proofMaterial,
                        0,
                        ProofDomain.Length);
                    Buffer.BlockCopy(
                        _auth,
                        0,
                        proofMaterial,
                        ProofDomain.Length,
                        ClientAuthProofOffset);
                    ClientBattleNativeCrypto.HmacSha256(
                        _proofKey,
                        proofMaterial,
                        proof);
                    Buffer.BlockCopy(
                        proof,
                        0,
                        _auth,
                        ClientAuthProofOffset,
                        proof.Length);
                    _state = HandshakeState.AuthReady;
                    clientAuth = (byte[])_auth.Clone();
                    return true;
                }
                catch
                {
                    CloseLocked();
                    throw;
                }
                finally
                {
                    Array.Clear(proofMaterial, 0, proofMaterial.Length);
                    ClientBattleNativeCrypto.SecureZero(proof);
                }
            }
        }

        /// <summary>
        /// 认证 ServerAccept，验证全部 closed plaintext 并转移 session material。
        /// </summary>
        /// <param name="response">156-byte ServerAccept datagram。</param>
        /// <param name="nowMilliseconds">当前 Unix 毫秒。</param>
        /// <param name="parameters">成功时返回唯一 session parameters owner。</param>
        /// <returns>AEAD、identity、generation、slot 与 role 全部合法时为 true。</returns>
        internal bool TryAcceptServer(
            byte[] response,
            long nowMilliseconds,
            out ClientBattleSessionParameters parameters)
        {
            parameters = null;
            lock (_gate)
            {
                if (_state != HandshakeState.AuthReady ||
                    !Alive(nowMilliseconds) ||
                    !MatchesServerAcceptEnvelope(response))
                {
                    CloseLocked();
                    return false;
                }

                var serverPublic = Copy(response, 40, 32);
                var shared = new byte[ClientBattleNativeCrypto.KeyBytes];
                byte[] requestDigest = null;
                byte[] transcriptHash = null;
                var scheduleMaterial = new byte[
                    ScheduleDomain.Length + 32 + 64];
                var hkdfInfo = new byte[ScheduleDomain.Length + 32];
                var schedule = new byte[SessionScheduleBytes];
                var acceptKey = new byte[ClientBattleNativeCrypto.KeyBytes];
                var acceptNonce = new byte[ClientBattleNativeCrypto.NonceBytes];
                var aad = Copy(response, 0, AcceptAadBytes);
                var ciphertext = Copy(
                    response,
                    AcceptAadBytes,
                    AcceptPlaintextBytes);
                var tag = Copy(
                    response,
                    ServerAcceptBytes - ClientBattleNativeCrypto.TagBytes,
                    ClientBattleNativeCrypto.TagBytes);
                var plaintext = new byte[AcceptPlaintextBytes];
                try
                {
                    ClientBattleNativeCrypto.X25519Shared(
                        _secretScalar,
                        serverPublic,
                        shared);
                    requestDigest = Sha256(_auth);
                    Buffer.BlockCopy(
                        ScheduleDomain,
                        0,
                        scheduleMaterial,
                        0,
                        ScheduleDomain.Length);
                    Buffer.BlockCopy(
                        requestDigest,
                        0,
                        scheduleMaterial,
                        ScheduleDomain.Length,
                        requestDigest.Length);
                    Buffer.BlockCopy(
                        response,
                        8,
                        scheduleMaterial,
                        ScheduleDomain.Length + requestDigest.Length,
                        64);
                    transcriptHash = Sha256(scheduleMaterial);
                    Buffer.BlockCopy(
                        ScheduleDomain,
                        0,
                        hkdfInfo,
                        0,
                        ScheduleDomain.Length);
                    Buffer.BlockCopy(
                        transcriptHash,
                        0,
                        hkdfInfo,
                        ScheduleDomain.Length,
                        transcriptHash.Length);
                    ClientBattleNativeCrypto.HkdfSha256(
                        shared,
                        _proofKey,
                        hkdfInfo,
                        schedule);
                    Buffer.BlockCopy(schedule, 0, acceptKey, 0, 32);
                    Buffer.BlockCopy(schedule, 32, acceptNonce, 0, 12);
                    if (!ClientBattleNativeCrypto.TryOpen(
                            acceptKey,
                            acceptNonce,
                            aad,
                            ciphertext,
                            tag,
                            plaintext))
                    {
                        CloseLocked();
                        return false;
                    }

                    var sessionID = Copy(plaintext, 0, 16);
                    var sessionSeed = Copy(schedule, 44, 32);
                    var binding = Copy(plaintext, 32, 32);
                    var generation = ReadUInt32BigEndian(plaintext, 16);
                    var keyEpoch = ReadUInt32BigEndian(plaintext, 20);
                    var endpointGeneration = ReadUInt32BigEndian(plaintext, 24);
                    var actorSlot = plaintext[28];
                    var role = plaintext[29] == 1
                        ? ClientBattleRole.Owner
                        : plaintext[29] == 2
                            ? ClientBattleRole.Visitor
                            : ClientBattleRole.None;
                    if (IsAllZero(sessionID) ||
                        IsAllZero(sessionSeed) ||
                        IsAllZero(binding) ||
                        generation == 0 ||
                        keyEpoch != 1 ||
                        endpointGeneration != 1 ||
                        actorSlot >= 8 ||
                        role == ClientBattleRole.None ||
                        role != _expectedRole ||
                        plaintext[30] != 0 ||
                        plaintext[31] != 0)
                    {
                        ClientBattleNativeCrypto.SecureZero(sessionSeed);
                        Array.Clear(sessionID, 0, sessionID.Length);
                        ClientBattleNativeCrypto.SecureZero(binding);
                        CloseLocked();
                        return false;
                    }

                    try
                    {
                        parameters = new ClientBattleSessionParameters(
                            sessionSeed,
                            sessionID,
                            generation,
                            keyEpoch,
                            endpointGeneration,
                            actorSlot,
                            role,
                            binding);
                    }
                    catch
                    {
                        ClientBattleNativeCrypto.SecureZero(sessionSeed);
                        Array.Clear(sessionID, 0, sessionID.Length);
                        ClientBattleNativeCrypto.SecureZero(binding);
                        throw;
                    }

                    _state = HandshakeState.Established;
                    ReleaseHandshakeSecretsLocked();
                    return true;
                }
                catch
                {
                    parameters?.Dispose();
                    parameters = null;
                    CloseLocked();
                    return false;
                }
                finally
                {
                    Array.Clear(serverPublic, 0, serverPublic.Length);
                    ClientBattleNativeCrypto.SecureZero(shared);
                    if (requestDigest != null)
                    {
                        Array.Clear(
                            requestDigest,
                            0,
                            requestDigest.Length);
                    }

                    if (transcriptHash != null)
                    {
                        Array.Clear(
                            transcriptHash,
                            0,
                            transcriptHash.Length);
                    }

                    Array.Clear(
                        scheduleMaterial,
                        0,
                        scheduleMaterial.Length);
                    Array.Clear(hkdfInfo, 0, hkdfInfo.Length);
                    ClientBattleNativeCrypto.SecureZero(schedule);
                    ClientBattleNativeCrypto.SecureZero(acceptKey);
                    ClientBattleNativeCrypto.SecureZero(acceptNonce);
                    Array.Clear(aad, 0, aad.Length);
                    Array.Clear(ciphertext, 0, ciphertext.Length);
                    Array.Clear(tag, 0, tag.Length);
                    ClientBattleNativeCrypto.SecureZero(plaintext);
                }
            }
        }

        /// <summary>
        /// 清零 transcript、ephemeral、proof 与 nonce；重复调用安全。
        /// </summary>
        public void Dispose()
        {
            lock (_gate)
            {
                CloseLocked();
            }
        }

        /// <summary>
        /// 验证握手总 deadline 与 ticket expiry。
        /// </summary>
        /// <param name="nowMilliseconds">当前 Unix 毫秒。</param>
        /// <returns>时间未回退且两个 deadline 均未到期时为 true。</returns>
        private bool Alive(long nowMilliseconds)
        {
            return nowMilliseconds >= _startedAtMilliseconds &&
                   nowMilliseconds - _startedAtMilliseconds <
                   HandshakeTimeoutMilliseconds &&
                   nowMilliseconds < _expiresAtMilliseconds;
        }

        /// <summary>
        /// 进入不可逆 closed 状态并清零全部 caller-owned material。
        /// </summary>
        private void CloseLocked()
        {
            _state = HandshakeState.Closed;
            ReleaseHandshakeSecretsLocked();
            Clear(ref _hello, false);
            Clear(ref _auth, false);
            Clear(ref _clientNonce, true);
        }

        /// <summary>
        /// 在 established 或 closed 时清除 proof 与 ephemeral scalar。
        /// </summary>
        private void ReleaseHandshakeSecretsLocked()
        {
            Clear(ref _secretScalar, true);
            Clear(ref _proofKey, true);
        }

        /// <summary>
        /// 清零并置空 owned buffer。
        /// </summary>
        /// <param name="value">待释放字段。</param>
        /// <param name="secure">是否通过 native secure-zero primitive 清零。</param>
        private static void Clear(ref byte[] value, bool secure)
        {
            var current = value;
            value = null;
            if (current == null)
            {
                return;
            }

            if (secure && current.Length > 0)
            {
                ClientBattleNativeCrypto.SecureZero(current);
            }
            else
            {
                Array.Clear(current, 0, current.Length);
            }
        }

        /// <summary>
        /// 写入 handshake magic、version 与 kind，其余 reserved bytes 保持为零。
        /// </summary>
        /// <param name="output">已清零的 fixed envelope。</param>
        /// <param name="magic">四字节 ASCII magic。</param>
        /// <param name="kind">Wire v1 kind。</param>
        private static void WriteMagic(byte[] output, string magic, byte kind)
        {
            output[0] = (byte)magic[0];
            output[1] = (byte)magic[1];
            output[2] = (byte)magic[2];
            output[3] = (byte)magic[3];
            output[4] = 1;
            output[5] = kind;
        }

        /// <summary>
        /// 验证 envelope magic、version、kind 与 reserved bytes。
        /// </summary>
        /// <param name="input">Fixed response。</param>
        /// <param name="magic">预期四字节 ASCII magic。</param>
        /// <param name="kind">预期 wire kind。</param>
        /// <returns>公开 header 精确一致时为 true。</returns>
        private static bool MatchesMagic(
            byte[] input,
            string magic,
            byte kind)
        {
            return input[0] == (byte)magic[0] &&
                   input[1] == (byte)magic[1] &&
                   input[2] == (byte)magic[2] &&
                   input[3] == (byte)magic[3] &&
                   input[4] == 1 &&
                   input[5] == kind &&
                   input[6] == 0 &&
                   input[7] == 0;
        }

        /// <summary>
        /// 解码 network-order uint32。
        /// </summary>
        /// <param name="input">包含四字节值的 buffer。</param>
        /// <param name="offset">值起点。</param>
        /// <returns>Host-order uint32。</returns>
        private static uint ReadUInt32BigEndian(byte[] input, int offset)
        {
            return ((uint)input[offset] << 24) |
                   ((uint)input[offset + 1] << 16) |
                   ((uint)input[offset + 2] << 8) |
                   input[offset + 3];
        }

        /// <summary>
        /// 判断完整 buffer 是否全部为零。
        /// </summary>
        /// <param name="input">待验证 buffer。</param>
        /// <returns>所有 bytes 为零时为 true。</returns>
        private static bool IsAllZero(byte[] input)
        {
            return IsAllZero(input, 0, input.Length);
        }

        /// <summary>
        /// 判断指定 slice 是否全部为零。
        /// </summary>
        /// <param name="input">待验证 buffer。</param>
        /// <param name="offset">Slice 起点。</param>
        /// <param name="length">Slice 宽度。</param>
        /// <returns>所有 bytes 为零时为 true。</returns>
        private static bool IsAllZero(
            byte[] input,
            int offset,
            int length)
        {
            var aggregate = 0;
            for (var index = 0; index < length; index++)
            {
                aggregate |= input[offset + index];
            }

            return aggregate == 0;
        }

        /// <summary>
        /// 复制 fixed slice 到独立 caller-owned buffer。
        /// </summary>
        /// <param name="input">来源 buffer。</param>
        /// <param name="offset">Slice 起点。</param>
        /// <param name="length">Slice 宽度。</param>
        /// <returns>新 buffer。</returns>
        private static byte[] Copy(byte[] input, int offset, int length)
        {
            var output = new byte[length];
            Buffer.BlockCopy(input, offset, output, 0, length);
            return output;
        }

        /// <summary>
        /// 计算 caller-owned SHA-256 digest。
        /// </summary>
        /// <param name="input">公开 transcript bytes。</param>
        /// <returns>32-byte digest。</returns>
        private static byte[] Sha256(byte[] input)
        {
            using (var sha256 = SHA256.Create())
            {
                return sha256.ComputeHash(input);
            }
        }

        /// <summary>
        /// 标识 closed handshake 的唯一合法阶段。
        /// </summary>
        private enum HandshakeState
        {
            /// <summary>构造完成，只接受 Retry。</summary>
            HelloReady = 1,

            /// <summary>Retry 已提交，只接受 ServerAccept。</summary>
            AuthReady = 2,

            /// <summary>Session material 已转移。</summary>
            Established = 3,

            /// <summary>失败、超时或释放后的不可逆终态。</summary>
            Closed = 4,
        }
    }
}
