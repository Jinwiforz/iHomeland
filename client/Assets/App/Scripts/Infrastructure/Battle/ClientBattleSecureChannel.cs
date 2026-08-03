using System;
using System.Text;

namespace IHomeland.Client.Infrastructure.Battle
{
    /// <summary>
    /// 标识 authenticated secure datagram 的 closed lane。
    /// </summary>
    internal enum ClientBattlePacketKind
    {
        /// <summary>不可靠 input、probe 与 snapshot lane。</summary>
        Raw = 1,

        /// <summary>可靠有序且会过期的 KCP lane。</summary>
        Kcp = 2,

        /// <summary>Authenticated rekey、rebind 与 close lane。</summary>
        Control = 3,
    }

    /// <summary>
    /// 标识 secure datagram 在 application dispatch 前的唯一裁决。
    /// </summary>
    internal enum ClientBattleOpenDisposition
    {
        /// <summary>Header、AEAD 与 replay window 已原子提交。</summary>
        Accepted = 1,

        /// <summary>Wire、identity、epoch 或长度不合法。</summary>
        InvalidHeader = 2,

        /// <summary>AEAD authentication 失败。</summary>
        AuthenticationFailed = 3,

        /// <summary>Sequence 已接受过，静默抑制。</summary>
        Duplicate = 4,

        /// <summary>Sequence 已离开 replay window，静默抑制。</summary>
        TooOld = 5,

        /// <summary>Authenticated sequence 跳过 replay window 并关闭 session。</summary>
        FutureJump = 6,

        /// <summary>Session 已关闭或 monotonic clock 失效。</summary>
        Closed = 7,
    }

    /// <summary>
    /// 保存 authenticated open 的低层 lane 与 caller-owned plaintext。
    /// </summary>
    internal sealed class ClientBattleOpenPacket
    {
        /// <summary>
        /// 创建一次 secure open 裁决。
        /// </summary>
        /// <param name="disposition">唯一处理终局。</param>
        /// <param name="kind">仅 Accepted 时有效的 lane。</param>
        /// <param name="sequence">仅 Accepted 时有效的 packet sequence。</param>
        /// <param name="plaintext">仅 Accepted 时非空的 plaintext。</param>
        internal ClientBattleOpenPacket(
            ClientBattleOpenDisposition disposition,
            ClientBattlePacketKind kind,
            ulong sequence,
            byte[] plaintext)
        {
            Disposition = disposition;
            Kind = kind;
            Sequence = sequence;
            Plaintext = plaintext;
        }

        /// <summary>获取 open 裁决。</summary>
        internal ClientBattleOpenDisposition Disposition { get; }

        /// <summary>获取 authenticated lane。</summary>
        internal ClientBattlePacketKind Kind { get; }

        /// <summary>获取 authenticated sequence。</summary>
        internal ulong Sequence { get; }

        /// <summary>获取 caller-owned plaintext；非 Accepted 时为空。</summary>
        internal byte[] Plaintext { get; }
    }

    /// <summary>
    /// 独立拥有 wire v1 traffic keys、nonce、replay、epoch 与 endpoint generation。
    /// </summary>
    internal sealed class ClientBattleSecureChannel : IDisposable
    {
        /// <summary>保存 canonical AAD header 宽度。</summary>
        internal const int SecureHeaderBytes = 48;

        /// <summary>保存最大 UDP datagram 宽度。</summary>
        internal const int MaximumDatagramBytes = 1200;

        /// <summary>保存 receive replay window 宽度。</summary>
        internal const int ReplayWindowPackets = 256;

        /// <summary>保存 time-based rekey trigger。</summary>
        internal const long RekeyIntervalMilliseconds = 600000;

        /// <summary>保存 packet-based rekey trigger。</summary>
        internal const ulong RekeyPacketLimit = 1048576;

        /// <summary>保存 previous receive epoch overlap。</summary>
        internal const long PreviousEpochOverlapMilliseconds = 3000;

        /// <summary>保存 rekey completion deadline。</summary>
        internal const long RolloverDeadlineMilliseconds = 3000;

        /// <summary>保存 protected payload ceiling。</summary>
        private const int MaximumProtectedPayloadBytes =
            MaximumDatagramBytes -
            SecureHeaderBytes -
            ClientBattleNativeCrypto.TagBytes;

        /// <summary>保存 C2S direction discriminator。</summary>
        private const byte ClientToServerDirection = 1;

        /// <summary>保存 S2C direction discriminator。</summary>
        private const byte ServerToClientDirection = 2;

        /// <summary>保存 traffic key derivation domain。</summary>
        private static readonly byte[] TrafficDomain =
            Encoding.ASCII.GetBytes("ihomeland/battle/traffic/v1");

        /// <summary>串行化 nonce、replay 与 transition。</summary>
        private readonly object _gate = new object();

        /// <summary>保存 SHA-256(session ID) 前 8 bytes。</summary>
        private byte[] _sessionDigest;

        /// <summary>保存 battle session incarnation。</summary>
        private readonly uint _battleSessionGeneration;

        /// <summary>保存当前 authenticated endpoint generation。</summary>
        private uint _endpointGeneration;

        /// <summary>保存 binding fingerprint 前 8 bytes。</summary>
        private byte[] _bindingDiscriminator;

        /// <summary>保存 current C2S traffic key。</summary>
        private byte[] _c2sTraffic;

        /// <summary>保存 current S2C traffic key。</summary>
        private byte[] _s2cTraffic;

        /// <summary>保存 current C2S rekey key。</summary>
        private byte[] _c2sRekey;

        /// <summary>保存 current S2C rekey key。</summary>
        private byte[] _s2cRekey;

        /// <summary>保存 overlap 内 previous S2C traffic key。</summary>
        private byte[] _previousS2cTraffic;

        /// <summary>保存 current receive replay window。</summary>
        private ReplayWindow _currentReplay = new ReplayWindow();

        /// <summary>保存 overlap 内 previous receive replay window。</summary>
        private ReplayWindow _previousReplay;

        /// <summary>保存 current traffic epoch。</summary>
        private uint _keyEpoch;

        /// <summary>保存 previous traffic epoch；零表示不存在。</summary>
        private uint _previousEpoch;

        /// <summary>保存下一个唯一 C2S sequence。</summary>
        private ulong _nextSendSequence = 1;

        /// <summary>保存 current epoch 已发送 packet 数。</summary>
        private ulong _sentPacketsCurrentEpoch;

        /// <summary>保存 current epoch 起点。</summary>
        private long _epochStartedAtMilliseconds;

        /// <summary>保存 previous receive key 的删除 deadline。</summary>
        private long _previousDeadlineMilliseconds;

        /// <summary>保存 trigger 后的 rekey completion deadline。</summary>
        private long _rolloverDeadlineMilliseconds;

        /// <summary>保存 sequence 已耗尽标志。</summary>
        private bool _sendExhausted;

        /// <summary>保存不可逆 closed 状态。</summary>
        private bool _closed;

        /// <summary>
        /// 从认证 session parameters 一次性取得 seed 并派生方向隔离 key。
        /// </summary>
        /// <param name="parameters">所有权不转移但 seed 被一次性消费的参数。</param>
        /// <param name="startedAtMilliseconds">Current epoch Unix 毫秒起点。</param>
        internal ClientBattleSecureChannel(
            ClientBattleSessionParameters parameters,
            long startedAtMilliseconds)
        {
            if (parameters == null)
            {
                throw new ArgumentNullException(nameof(parameters));
            }

            if (startedAtMilliseconds <= 0)
            {
                throw new ArgumentOutOfRangeException(
                    nameof(startedAtMilliseconds));
            }

            byte[] sessionSeed = null;
            byte[] c2sSchedule = null;
            byte[] s2cSchedule = null;
            try
            {
                parameters.TakeSecureMaterial(
                    out sessionSeed,
                    out _sessionDigest,
                    out _bindingDiscriminator);
                _battleSessionGeneration =
                    parameters.BattleSessionGeneration;
                _endpointGeneration = parameters.EndpointGeneration;
                _keyEpoch = parameters.KeyEpoch;
                _epochStartedAtMilliseconds = startedAtMilliseconds;
                if (_battleSessionGeneration == 0 ||
                    _endpointGeneration == 0 ||
                    _keyEpoch == 0 ||
                    IsAllZero(sessionSeed) ||
                    IsAllZero(_sessionDigest) ||
                    IsAllZero(_bindingDiscriminator))
                {
                    throw new ArgumentException(
                        "client battle secure identity is invalid");
                }

                c2sSchedule = DeriveDirectionSchedule(
                    sessionSeed,
                    Array.Empty<byte>(),
                    ClientToServerDirection,
                    _keyEpoch);
                s2cSchedule = DeriveDirectionSchedule(
                    sessionSeed,
                    Array.Empty<byte>(),
                    ServerToClientDirection,
                    _keyEpoch);
                _c2sTraffic = Copy(c2sSchedule, 0, 32);
                _c2sRekey = Copy(c2sSchedule, 32, 32);
                _s2cTraffic = Copy(s2cSchedule, 0, 32);
                _s2cRekey = Copy(s2cSchedule, 32, 32);
            }
            catch
            {
                Close();
                throw;
            }
            finally
            {
                if (sessionSeed != null)
                {
                    ClientBattleNativeCrypto.SecureZero(sessionSeed);
                }

                if (c2sSchedule != null)
                {
                    ClientBattleNativeCrypto.SecureZero(c2sSchedule);
                }

                if (s2cSchedule != null)
                {
                    ClientBattleNativeCrypto.SecureZero(s2cSchedule);
                }
            }
        }

        /// <summary>获取 current battle session generation。</summary>
        internal uint BattleSessionGeneration
        {
            get
            {
                return _battleSessionGeneration;
            }
        }

        /// <summary>
        /// 获取 current endpoint generation。
        /// </summary>
        internal uint EndpointGeneration
        {
            get
            {
                lock (_gate)
                {
                    return _endpointGeneration;
                }
            }
        }

        /// <summary>
        /// 获取 current traffic epoch。
        /// </summary>
        internal uint KeyEpoch
        {
            get
            {
                lock (_gate)
                {
                    return _keyEpoch;
                }
            }
        }

        /// <summary>
        /// 使用唯一 C2S nonce 认证保护非空 payload。
        /// </summary>
        /// <param name="kind">Raw、KCP 或 Control lane。</param>
        /// <param name="payload">不超过 datagram ceiling 的 plaintext。</param>
        /// <param name="nowMilliseconds">当前 Unix 毫秒。</param>
        /// <param name="datagram">成功时返回 caller-owned protected datagram。</param>
        /// <returns>Session 可发送且不被 rekey gate 阻止时为 true。</returns>
        internal bool TrySeal(
            ClientBattlePacketKind kind,
            byte[] payload,
            long nowMilliseconds,
            out byte[] datagram)
        {
            datagram = null;
            if (!ValidKind(kind) ||
                payload == null ||
                payload.Length == 0 ||
                payload.Length > MaximumProtectedPayloadBytes)
            {
                throw new ArgumentException(
                    "client battle secure payload is invalid");
            }

            lock (_gate)
            {
                if (_closed)
                {
                    return false;
                }

                var rolloverRequired =
                    RolloverRequiredLocked(nowMilliseconds);
                if (_closed ||
                    (rolloverRequired &&
                     kind != ClientBattlePacketKind.Control))
                {
                    return false;
                }

                if (_sendExhausted || _nextSendSequence == 0)
                {
                    CloseLocked();
                    return false;
                }

                var sequence = _nextSendSequence;
                if (sequence == ulong.MaxValue)
                {
                    _sendExhausted = true;
                }
                else
                {
                    _nextSendSequence++;
                }

                if (_sentPacketsCurrentEpoch != ulong.MaxValue)
                {
                    _sentPacketsCurrentEpoch++;
                }

                var header = EncodeHeader(
                    kind,
                    _keyEpoch,
                    sequence,
                    payload.Length);
                var nonce = PacketNonce(_keyEpoch, sequence);
                var ciphertext = new byte[payload.Length];
                var tag = new byte[ClientBattleNativeCrypto.TagBytes];
                try
                {
                    ClientBattleNativeCrypto.Seal(
                        _c2sTraffic,
                        nonce,
                        header,
                        payload,
                        ciphertext,
                        tag);
                    datagram = new byte[
                        SecureHeaderBytes +
                        ciphertext.Length +
                        tag.Length];
                    Buffer.BlockCopy(
                        header,
                        0,
                        datagram,
                        0,
                        header.Length);
                    Buffer.BlockCopy(
                        ciphertext,
                        0,
                        datagram,
                        header.Length,
                        ciphertext.Length);
                    Buffer.BlockCopy(
                        tag,
                        0,
                        datagram,
                        header.Length + ciphertext.Length,
                        tag.Length);
                    return true;
                }
                finally
                {
                    Array.Clear(header, 0, header.Length);
                    Array.Clear(nonce, 0, nonce.Length);
                    Array.Clear(ciphertext, 0, ciphertext.Length);
                    Array.Clear(tag, 0, tag.Length);
                }
            }
        }

        /// <summary>
        /// 认证 S2C datagram 后才分类并提交 replay window。
        /// </summary>
        /// <param name="datagram">完整 UDP datagram。</param>
        /// <param name="nowMilliseconds">当前 Unix 毫秒。</param>
        /// <returns>Closed disposition 与可选 caller-owned plaintext。</returns>
        internal ClientBattleOpenPacket Open(
            byte[] datagram,
            long nowMilliseconds)
        {
            if (datagram == null ||
                datagram.Length <=
                SecureHeaderBytes + ClientBattleNativeCrypto.TagBytes ||
                datagram.Length > MaximumDatagramBytes ||
                !MatchesHeaderIdentity(datagram))
            {
                return Rejected(
                    ClientBattleOpenDisposition.InvalidHeader);
            }

            var kind = (ClientBattlePacketKind)datagram[5];
            var payloadLength = ReadUInt16BigEndian(datagram, 32);
            var epoch = ReadUInt32BigEndian(datagram, 20);
            var sequence = ReadUInt64BigEndian(datagram, 24);
            if (!ValidKind(kind) ||
                sequence == 0 ||
                payloadLength == 0 ||
                payloadLength +
                    SecureHeaderBytes +
                    ClientBattleNativeCrypto.TagBytes !=
                datagram.Length)
            {
                return Rejected(
                    ClientBattleOpenDisposition.InvalidHeader);
            }

            var header = Copy(datagram, 0, SecureHeaderBytes);
            var ciphertext = Copy(
                datagram,
                SecureHeaderBytes,
                payloadLength);
            var tag = Copy(
                datagram,
                SecureHeaderBytes + payloadLength,
                ClientBattleNativeCrypto.TagBytes);
            var nonce = PacketNonce(epoch, sequence);
            var plaintext = new byte[payloadLength];
            lock (_gate)
            {
                try
                {
                    if (_closed ||
                        nowMilliseconds < _epochStartedAtMilliseconds)
                    {
                        return Rejected(
                            ClientBattleOpenDisposition.Closed);
                    }

                    ExpirePreviousLocked(nowMilliseconds);
                    byte[] receiveKey;
                    ReplayWindow replay;
                    if (epoch == _keyEpoch)
                    {
                        receiveKey = _s2cTraffic;
                        replay = _currentReplay;
                    }
                    else if (epoch == _previousEpoch &&
                             _previousReplay != null)
                    {
                        receiveKey = _previousS2cTraffic;
                        replay = _previousReplay;
                    }
                    else
                    {
                        return Rejected(
                            ClientBattleOpenDisposition.InvalidHeader);
                    }

                    if (!ClientBattleNativeCrypto.TryOpen(
                            receiveKey,
                            nonce,
                            header,
                            ciphertext,
                            tag,
                            plaintext))
                    {
                        return Rejected(
                            ClientBattleOpenDisposition.AuthenticationFailed);
                    }

                    var disposition = replay.Classify(sequence);
                    if (disposition !=
                        ClientBattleOpenDisposition.Accepted)
                    {
                        Array.Clear(plaintext, 0, plaintext.Length);
                        if (disposition ==
                            ClientBattleOpenDisposition.FutureJump)
                        {
                            CloseLocked();
                        }

                        return Rejected(disposition);
                    }

                    replay.Commit(sequence);
                    var accepted = plaintext;
                    plaintext = null;
                    return new ClientBattleOpenPacket(
                        ClientBattleOpenDisposition.Accepted,
                        kind,
                        sequence,
                        accepted);
                }
                finally
                {
                    Array.Clear(header, 0, header.Length);
                    Array.Clear(ciphertext, 0, ciphertext.Length);
                    Array.Clear(tag, 0, tag.Length);
                    Array.Clear(nonce, 0, nonce.Length);
                    if (plaintext != null)
                    {
                        Array.Clear(plaintext, 0, plaintext.Length);
                    }
                }
            }
        }

        /// <summary>
        /// 判断 current epoch 是否必须开始 rekey，并维护 deadline/overlap。
        /// </summary>
        /// <param name="nowMilliseconds">当前 Unix 毫秒。</param>
        /// <returns>达到任一 trigger 或已关闭时为 true。</returns>
        internal bool RolloverRequired(long nowMilliseconds)
        {
            lock (_gate)
            {
                return RolloverRequiredLocked(nowMilliseconds);
            }
        }

        /// <summary>
        /// 使用 authenticated control nonce 原子推进 exact next epoch。
        /// </summary>
        /// <param name="rekeyNonce">32-byte nonzero server nonce。</param>
        /// <param name="nextEpoch">必须等于 current+1。</param>
        /// <param name="nowMilliseconds">当前 Unix 毫秒。</param>
        /// <returns>全部 transition 前置条件满足时为 true。</returns>
        internal bool CommitRollover(
            byte[] rekeyNonce,
            uint nextEpoch,
            long nowMilliseconds)
        {
            if (rekeyNonce == null ||
                rekeyNonce.Length != ClientBattleNativeCrypto.KeyBytes)
            {
                throw new ArgumentException(
                    "client battle rekey nonce is invalid",
                    nameof(rekeyNonce));
            }

            lock (_gate)
            {
                if (_closed ||
                    IsAllZero(rekeyNonce) ||
                    nowMilliseconds < _epochStartedAtMilliseconds ||
                    (_rolloverDeadlineMilliseconds != 0 &&
                     nowMilliseconds >= _rolloverDeadlineMilliseconds) ||
                    _keyEpoch == uint.MaxValue ||
                    nextEpoch != _keyEpoch + 1 ||
                    nowMilliseconds >
                    long.MaxValue -
                    PreviousEpochOverlapMilliseconds)
                {
                    CloseLocked();
                    return false;
                }

                byte[] nextC2s = null;
                byte[] nextS2c = null;
                try
                {
                    nextC2s = DeriveDirectionSchedule(
                        _c2sRekey,
                        rekeyNonce,
                        ClientToServerDirection,
                        nextEpoch);
                    nextS2c = DeriveDirectionSchedule(
                        _s2cRekey,
                        rekeyNonce,
                        ServerToClientDirection,
                        nextEpoch);
                    Clear(ref _previousS2cTraffic);
                    _previousS2cTraffic = _s2cTraffic;
                    _s2cTraffic = Copy(nextS2c, 0, 32);
                    Replace(ref _c2sTraffic, Copy(nextC2s, 0, 32));
                    Replace(ref _c2sRekey, Copy(nextC2s, 32, 32));
                    Replace(ref _s2cRekey, Copy(nextS2c, 32, 32));
                    _previousEpoch = _keyEpoch;
                    _previousDeadlineMilliseconds =
                        nowMilliseconds +
                        PreviousEpochOverlapMilliseconds;
                    _previousReplay = _currentReplay;
                    _currentReplay = new ReplayWindow();
                    _keyEpoch = nextEpoch;
                    _nextSendSequence = 1;
                    _sentPacketsCurrentEpoch = 0;
                    _epochStartedAtMilliseconds = nowMilliseconds;
                    _rolloverDeadlineMilliseconds = 0;
                    _sendExhausted = false;
                    return true;
                }
                catch
                {
                    CloseLocked();
                    return false;
                }
                finally
                {
                    if (nextC2s != null)
                    {
                        ClientBattleNativeCrypto.SecureZero(nextC2s);
                    }

                    if (nextS2c != null)
                    {
                        ClientBattleNativeCrypto.SecureZero(nextS2c);
                    }
                }
            }
        }

        /// <summary>
        /// 只允许 current endpoint generation 原子推进到 current+1。
        /// </summary>
        /// <param name="expectedCurrent">调用方冻结的 current generation。</param>
        /// <param name="nextGeneration">必须等于 expected+1。</param>
        /// <returns>Current identity 精确匹配时为 true。</returns>
        internal bool CommitEndpointGeneration(
            uint expectedCurrent,
            uint nextGeneration)
        {
            lock (_gate)
            {
                if (_closed ||
                    expectedCurrent == 0 ||
                    _endpointGeneration != expectedCurrent ||
                    expectedCurrent == uint.MaxValue ||
                    nextGeneration != expectedCurrent + 1)
                {
                    return false;
                }

                _endpointGeneration = nextGeneration;
                return true;
            }
        }

        /// <summary>
        /// 清零所有 current/previous traffic 与 rekey material。
        /// </summary>
        public void Dispose()
        {
            Close();
        }

        /// <summary>
        /// 进入不可逆 closed 状态；重复调用安全。
        /// </summary>
        internal void Close()
        {
            lock (_gate)
            {
                CloseLocked();
            }
        }

        /// <summary>
        /// 在持锁状态判断 trigger 并维护 rekey deadline。
        /// </summary>
        /// <param name="nowMilliseconds">当前 Unix 毫秒。</param>
        /// <returns>已关闭或需要 rollover 时为 true。</returns>
        private bool RolloverRequiredLocked(long nowMilliseconds)
        {
            if (_closed)
            {
                return true;
            }

            if (nowMilliseconds < _epochStartedAtMilliseconds)
            {
                CloseLocked();
                return true;
            }

            ExpirePreviousLocked(nowMilliseconds);
            var required =
                nowMilliseconds - _epochStartedAtMilliseconds >=
                RekeyIntervalMilliseconds ||
                _sentPacketsCurrentEpoch >= RekeyPacketLimit;
            if (!required)
            {
                return false;
            }

            if (_rolloverDeadlineMilliseconds == 0)
            {
                if (nowMilliseconds >
                    long.MaxValue - RolloverDeadlineMilliseconds)
                {
                    CloseLocked();
                    return true;
                }

                _rolloverDeadlineMilliseconds =
                    nowMilliseconds +
                    RolloverDeadlineMilliseconds;
            }

            if (nowMilliseconds >= _rolloverDeadlineMilliseconds)
            {
                CloseLocked();
            }

            return true;
        }

        /// <summary>
        /// 到期时清除 previous receive key 与 replay window。
        /// </summary>
        /// <param name="nowMilliseconds">当前 Unix 毫秒。</param>
        private void ExpirePreviousLocked(long nowMilliseconds)
        {
            if (_previousEpoch == 0 ||
                nowMilliseconds < _previousDeadlineMilliseconds)
            {
                return;
            }

            Clear(ref _previousS2cTraffic);
            _previousEpoch = 0;
            _previousDeadlineMilliseconds = 0;
            _previousReplay = null;
        }

        /// <summary>
        /// 清零全部 key/identity/window 并进入不可逆终态。
        /// </summary>
        private void CloseLocked()
        {
            if (_closed)
            {
                return;
            }

            _closed = true;
            _sendExhausted = true;
            Clear(ref _c2sTraffic);
            Clear(ref _s2cTraffic);
            Clear(ref _c2sRekey);
            Clear(ref _s2cRekey);
            Clear(ref _previousS2cTraffic);
            Clear(ref _sessionDigest);
            Clear(ref _bindingDiscriminator);
            _currentReplay = new ReplayWindow();
            _previousReplay = null;
            _previousEpoch = 0;
            _previousDeadlineMilliseconds = 0;
            _rolloverDeadlineMilliseconds = 0;
        }

        /// <summary>
        /// 派生指定方向与 epoch 的 32-byte traffic + 32-byte rekey schedule。
        /// </summary>
        /// <param name="inputKeyMaterial">Session seed 或 current rekey key。</param>
        /// <param name="salt">初始为空，rollover 时为 authenticated nonce。</param>
        /// <param name="direction">C2S 或 S2C discriminator。</param>
        /// <param name="epoch">非零 target epoch。</param>
        /// <returns>64-byte caller-owned schedule。</returns>
        private byte[] DeriveDirectionSchedule(
            byte[] inputKeyMaterial,
            byte[] salt,
            byte direction,
            uint epoch)
        {
            var info = DirectionInfo(direction, epoch);
            var output = new byte[64];
            try
            {
                ClientBattleNativeCrypto.HkdfSha256(
                    inputKeyMaterial,
                    salt,
                    info,
                    output);
                return output;
            }
            catch
            {
                ClientBattleNativeCrypto.SecureZero(output);
                throw;
            }
            finally
            {
                Array.Clear(info, 0, info.Length);
            }
        }

        /// <summary>
        /// 构造绑定方向、epoch、session、battle 与 endpoint 的 HKDF info。
        /// </summary>
        /// <param name="direction">C2S 或 S2C discriminator。</param>
        /// <param name="epoch">非零 traffic epoch。</param>
        /// <returns>Canonical direction info。</returns>
        private byte[] DirectionInfo(byte direction, uint epoch)
        {
            var output = new byte[
                TrafficDomain.Length + 1 + 4 + 8 + 4 + 4 + 8];
            var offset = 0;
            Buffer.BlockCopy(
                TrafficDomain,
                0,
                output,
                offset,
                TrafficDomain.Length);
            offset += TrafficDomain.Length;
            output[offset++] = direction;
            WriteUInt32BigEndian(output, offset, epoch);
            offset += 4;
            Buffer.BlockCopy(_sessionDigest, 0, output, offset, 8);
            offset += 8;
            WriteUInt32BigEndian(
                output,
                offset,
                _battleSessionGeneration);
            offset += 4;
            WriteUInt32BigEndian(
                output,
                offset,
                _endpointGeneration);
            offset += 4;
            Buffer.BlockCopy(
                _bindingDiscriminator,
                0,
                output,
                offset,
                8);
            return output;
        }

        /// <summary>
        /// 构造 canonical 48-byte secure header AAD。
        /// </summary>
        /// <param name="kind">Closed lane kind。</param>
        /// <param name="epoch">Current traffic epoch。</param>
        /// <param name="sequence">Nonzero packet sequence。</param>
        /// <param name="payloadLength">Protected payload bytes。</param>
        /// <returns>Caller-owned header。</returns>
        private byte[] EncodeHeader(
            ClientBattlePacketKind kind,
            uint epoch,
            ulong sequence,
            int payloadLength)
        {
            var header = new byte[SecureHeaderBytes];
            header[0] = (byte)'I';
            header[1] = (byte)'H';
            header[2] = (byte)'B';
            header[3] = (byte)'T';
            header[4] = 1;
            header[5] = (byte)kind;
            Buffer.BlockCopy(_sessionDigest, 0, header, 8, 8);
            WriteUInt32BigEndian(
                header,
                16,
                _battleSessionGeneration);
            WriteUInt32BigEndian(header, 20, epoch);
            WriteUInt64BigEndian(header, 24, sequence);
            WriteUInt16BigEndian(
                header,
                32,
                checked((ushort)payloadLength));
            WriteUInt32BigEndian(
                header,
                34,
                _endpointGeneration);
            Buffer.BlockCopy(
                _bindingDiscriminator,
                0,
                header,
                38,
                8);
            return header;
        }

        /// <summary>
        /// 验证 header 中不依赖 AEAD 的 exact session/endpoint identity。
        /// </summary>
        /// <param name="datagram">至少包含 secure header 的 datagram。</param>
        /// <returns>Magic、reserved 与 frozen identity 全部一致时为 true。</returns>
        private bool MatchesHeaderIdentity(byte[] datagram)
        {
            lock (_gate)
            {
                return !_closed &&
                       datagram[0] == (byte)'I' &&
                       datagram[1] == (byte)'H' &&
                       datagram[2] == (byte)'B' &&
                       datagram[3] == (byte)'T' &&
                       datagram[4] == 1 &&
                       datagram[6] == 0 &&
                       datagram[7] == 0 &&
                       SliceEqual(datagram, 8, _sessionDigest) &&
                       ReadUInt32BigEndian(datagram, 16) ==
                       _battleSessionGeneration &&
                       ReadUInt32BigEndian(datagram, 34) ==
                       _endpointGeneration &&
                       SliceEqual(
                           datagram,
                           38,
                           _bindingDiscriminator) &&
                       datagram[46] == 0 &&
                       datagram[47] == 0;
            }
        }

        /// <summary>
        /// 构造 epoch+sequence 的 canonical 12-byte AEAD nonce。
        /// </summary>
        /// <param name="epoch">Nonzero epoch。</param>
        /// <param name="sequence">Nonzero sequence。</param>
        /// <returns>Caller-owned nonce。</returns>
        private static byte[] PacketNonce(uint epoch, ulong sequence)
        {
            if (epoch == 0 || sequence == 0)
            {
                throw new ArgumentException(
                    "client battle packet nonce is invalid");
            }

            var nonce = new byte[ClientBattleNativeCrypto.NonceBytes];
            WriteUInt32BigEndian(nonce, 0, epoch);
            WriteUInt64BigEndian(nonce, 4, sequence);
            return nonce;
        }

        /// <summary>
        /// 构造不包含 plaintext 的 rejected result。
        /// </summary>
        /// <param name="disposition">非 Accepted 裁决。</param>
        /// <returns>Closed result。</returns>
        private static ClientBattleOpenPacket Rejected(
            ClientBattleOpenDisposition disposition)
        {
            return new ClientBattleOpenPacket(
                disposition,
                ClientBattlePacketKind.Raw,
                0,
                null);
        }

        /// <summary>
        /// 判断 packet kind 是否属于 wire v1 closed set。
        /// </summary>
        /// <param name="kind">待验证值。</param>
        /// <returns>Raw、KCP 或 Control 时为 true。</returns>
        private static bool ValidKind(ClientBattlePacketKind kind)
        {
            return kind == ClientBattlePacketKind.Raw ||
                   kind == ClientBattlePacketKind.Kcp ||
                   kind == ClientBattlePacketKind.Control;
        }

        /// <summary>
        /// 清零并替换 current key。
        /// </summary>
        /// <param name="target">Current owned key field。</param>
        /// <param name="replacement">新 caller-owned key。</param>
        private static void Replace(
            ref byte[] target,
            byte[] replacement)
        {
            Clear(ref target);
            target = replacement;
        }

        /// <summary>
        /// 清零并置空 owned secret。
        /// </summary>
        /// <param name="value">待释放字段。</param>
        private static void Clear(ref byte[] value)
        {
            var current = value;
            value = null;
            if (current != null)
            {
                ClientBattleNativeCrypto.SecureZero(current);
            }
        }

        /// <summary>
        /// 判断 buffer 是否全部为零。
        /// </summary>
        /// <param name="input">待验证 fixed buffer。</param>
        /// <returns>所有 bytes 为零时为 true。</returns>
        private static bool IsAllZero(byte[] input)
        {
            var aggregate = 0;
            for (var index = 0; index < input.Length; index++)
            {
                aggregate |= input[index];
            }

            return aggregate == 0;
        }

        /// <summary>
        /// 验证 datagram slice 与 fixed identity 一致。
        /// </summary>
        /// <param name="input">Datagram。</param>
        /// <param name="offset">Slice 起点。</param>
        /// <param name="expected">Expected fixed bytes。</param>
        /// <returns>逐字节一致时为 true。</returns>
        private static bool SliceEqual(
            byte[] input,
            int offset,
            byte[] expected)
        {
            var aggregate = 0;
            for (var index = 0; index < expected.Length; index++)
            {
                aggregate |= input[offset + index] ^ expected[index];
            }

            return aggregate == 0;
        }

        /// <summary>
        /// 复制 fixed slice 到 caller-owned buffer。
        /// </summary>
        /// <param name="input">Source buffer。</param>
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
        /// 编码 network-order uint16。
        /// </summary>
        /// <param name="output">Destination buffer。</param>
        /// <param name="offset">Value 起点。</param>
        /// <param name="value">Host-order value。</param>
        private static void WriteUInt16BigEndian(
            byte[] output,
            int offset,
            ushort value)
        {
            output[offset] = (byte)(value >> 8);
            output[offset + 1] = (byte)value;
        }

        /// <summary>
        /// 编码 network-order uint32。
        /// </summary>
        /// <param name="output">Destination buffer。</param>
        /// <param name="offset">Value 起点。</param>
        /// <param name="value">Host-order value。</param>
        private static void WriteUInt32BigEndian(
            byte[] output,
            int offset,
            uint value)
        {
            output[offset] = (byte)(value >> 24);
            output[offset + 1] = (byte)(value >> 16);
            output[offset + 2] = (byte)(value >> 8);
            output[offset + 3] = (byte)value;
        }

        /// <summary>
        /// 编码 network-order uint64。
        /// </summary>
        /// <param name="output">Destination buffer。</param>
        /// <param name="offset">Value 起点。</param>
        /// <param name="value">Host-order value。</param>
        private static void WriteUInt64BigEndian(
            byte[] output,
            int offset,
            ulong value)
        {
            for (var index = 0; index < 8; index++)
            {
                output[offset + index] = (byte)(
                    value >> ((7 - index) * 8));
            }
        }

        /// <summary>
        /// 解码 network-order uint16。
        /// </summary>
        /// <param name="input">Source buffer。</param>
        /// <param name="offset">Value 起点。</param>
        /// <returns>Host-order value。</returns>
        private static ushort ReadUInt16BigEndian(
            byte[] input,
            int offset)
        {
            return (ushort)(
                (input[offset] << 8) |
                input[offset + 1]);
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

        /// <summary>
        /// 解码 network-order uint64。
        /// </summary>
        /// <param name="input">Source buffer。</param>
        /// <param name="offset">Value 起点。</param>
        /// <returns>Host-order value。</returns>
        private static ulong ReadUInt64BigEndian(
            byte[] input,
            int offset)
        {
            ulong output = 0;
            for (var index = 0; index < 8; index++)
            {
                output = (output << 8) | input[offset + index];
            }

            return output;
        }

        /// <summary>
        /// 保存一个 highest-relative 256-bit authenticated replay window。
        /// </summary>
        private sealed class ReplayWindow
        {
            /// <summary>保存已认证最高 sequence。</summary>
            private ulong _highest;

            /// <summary>Bit N 表示 highest-N 已接受。</summary>
            private ulong[] _bitmap = new ulong[4];

            /// <summary>
            /// 在 AEAD 成功后分类 sequence，但不改变 window。
            /// </summary>
            /// <param name="sequence">Authenticated nonzero sequence。</param>
            /// <returns>Accepted、duplicate、too-old 或 future-jump。</returns>
            internal ClientBattleOpenDisposition Classify(ulong sequence)
            {
                if (_highest == 0)
                {
                    return sequence > ReplayWindowPackets
                        ? ClientBattleOpenDisposition.FutureJump
                        : ClientBattleOpenDisposition.Accepted;
                }

                if (sequence > _highest)
                {
                    return sequence - _highest > ReplayWindowPackets
                        ? ClientBattleOpenDisposition.FutureJump
                        : ClientBattleOpenDisposition.Accepted;
                }

                var offset = _highest - sequence;
                if (offset >= ReplayWindowPackets)
                {
                    return ClientBattleOpenDisposition.TooOld;
                }

                var word = checked((int)(offset / 64));
                var bit = checked((int)(offset % 64));
                return (_bitmap[word] & (1UL << bit)) != 0
                    ? ClientBattleOpenDisposition.Duplicate
                    : ClientBattleOpenDisposition.Accepted;
            }

            /// <summary>
            /// 提交已经由 Classify 接受的 sequence。
            /// </summary>
            /// <param name="sequence">Authenticated accepted sequence。</param>
            internal void Commit(ulong sequence)
            {
                if (_highest == 0)
                {
                    _highest = sequence;
                    _bitmap[0] = 1;
                    return;
                }

                if (sequence > _highest)
                {
                    var shift = sequence - _highest;
                    var shifted = new ulong[4];
                    for (var offset = 0;
                         (ulong)offset + shift < ReplayWindowPackets;
                         offset++)
                    {
                        if ((_bitmap[offset / 64] &
                             (1UL << (offset % 64))) == 0)
                        {
                            continue;
                        }

                        var target = offset + checked((int)shift);
                        shifted[target / 64] |=
                            1UL << (target % 64);
                    }

                    _bitmap = shifted;
                    _highest = sequence;
                    _bitmap[0] |= 1;
                    return;
                }

                var relative = _highest - sequence;
                _bitmap[checked((int)(relative / 64))] |=
                    1UL << checked((int)(relative % 64));
            }
        }
    }
}
