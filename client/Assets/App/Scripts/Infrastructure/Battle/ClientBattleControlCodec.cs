using System;

namespace IHomeland.Client.Infrastructure.Battle
{
    /// <summary>
    /// 标识 wire v1 authenticated transport-control kind。
    /// </summary>
    internal enum ClientBattleControlKind
    {
        /// <summary>Current endpoint 请求 candidate challenge。</summary>
        RebindRequest = 1,

        /// <summary>Server 向 candidate endpoint 返回 challenge。</summary>
        RebindChallenge = 2,

        /// <summary>Candidate endpoint 回传 exact challenge。</summary>
        RebindConfirm = 3,

        /// <summary>Server 确认 endpoint generation 已推进。</summary>
        RebindCommitted = 4,

        /// <summary>Client 提议 next traffic epoch nonce。</summary>
        RekeyProposal = 5,

        /// <summary>Server 在 old epoch 下确认同一 nonce。</summary>
        RekeyCommitted = 6,

        /// <summary>Client 请求正常关闭 session。</summary>
        CloseRequest = 7,

        /// <summary>Server 确认正常关闭。</summary>
        CloseAcknowledged = 8,
    }

    /// <summary>
    /// 保存已通过 IHBC envelope 与 fixed payload size 校验的 control message。
    /// </summary>
    internal sealed class ClientBattleControlMessage
    {
        /// <summary>
        /// 创建 caller-owned control payload。
        /// </summary>
        /// <param name="kind">Closed control kind。</param>
        /// <param name="payload">Exact-width payload。</param>
        internal ClientBattleControlMessage(
            ClientBattleControlKind kind,
            byte[] payload)
        {
            Kind = kind;
            Payload = payload ?? throw new ArgumentNullException(nameof(payload));
        }

        /// <summary>获取 closed control kind。</summary>
        internal ClientBattleControlKind Kind { get; }

        /// <summary>获取 caller-owned payload。</summary>
        internal byte[] Payload { get; }
    }

    /// <summary>
    /// 编解码 canonical 8-byte IHBC envelope，并冻结每个 kind 的 payload width。
    /// </summary>
    internal static class ClientBattleControlCodec
    {
        /// <summary>保存 IHBC envelope 宽度。</summary>
        internal const int EnvelopeBytes = 8;

        /// <summary>保存 client-requested close code。</summary>
        internal const byte ClientRequestedClose = 1;

        /// <summary>
        /// 编码 32-byte nonzero rekey proposal。
        /// </summary>
        /// <param name="nonce">Caller-owned 32-byte CSPRNG nonce。</param>
        /// <returns>Canonical IHBC plaintext。</returns>
        internal static byte[] EncodeRekeyProposal(byte[] nonce)
        {
            if (nonce == null ||
                nonce.Length != ClientBattleNativeCrypto.KeyBytes ||
                IsAllZero(nonce))
            {
                throw new ArgumentException(
                    "client battle rekey nonce is invalid",
                    nameof(nonce));
            }

            return Encode(ClientBattleControlKind.RekeyProposal, nonce);
        }

        /// <summary>
        /// 编码 client-requested normal close。
        /// </summary>
        /// <returns>Canonical one-byte close request。</returns>
        internal static byte[] EncodeCloseRequest()
        {
            return Encode(
                ClientBattleControlKind.CloseRequest,
                new[] { ClientRequestedClose });
        }

        /// <summary>
        /// 编码 endpoint rebind request。
        /// </summary>
        /// <param name="canonicalAddress">16-byte IPv4-mapped 或 IPv6 address。</param>
        /// <param name="port">Nonzero candidate UDP port。</param>
        /// <param name="nonce">16-byte nonzero challenge nonce。</param>
        /// <returns>Canonical 34-byte request payload envelope。</returns>
        internal static byte[] EncodeRebindRequest(
            byte[] canonicalAddress,
            ushort port,
            byte[] nonce)
        {
            if (canonicalAddress == null ||
                canonicalAddress.Length != 16 ||
                IsAllZero(canonicalAddress) ||
                port == 0 ||
                nonce == null ||
                nonce.Length != 16 ||
                IsAllZero(nonce))
            {
                throw new ArgumentException(
                    "client battle rebind request is invalid");
            }

            var payload = new byte[34];
            Buffer.BlockCopy(canonicalAddress, 0, payload, 0, 16);
            payload[16] = (byte)(port >> 8);
            payload[17] = (byte)port;
            Buffer.BlockCopy(nonce, 0, payload, 18, 16);
            return Encode(
                ClientBattleControlKind.RebindRequest,
                payload);
        }

        /// <summary>
        /// 编码 exact 48-byte rebind challenge confirmation。
        /// </summary>
        /// <param name="challengePayload">已认证且校验 generation/deadline 的 payload。</param>
        /// <returns>Canonical rebind confirmation。</returns>
        internal static byte[] EncodeRebindConfirm(
            byte[] challengePayload)
        {
            if (challengePayload == null ||
                challengePayload.Length != 48)
            {
                throw new ArgumentException(
                    "client battle rebind challenge is invalid",
                    nameof(challengePayload));
            }

            return Encode(
                ClientBattleControlKind.RebindConfirm,
                challengePayload);
        }

        /// <summary>
        /// 解码服务端允许发送的 challenge/commit/rekey/close response。
        /// </summary>
        /// <param name="plaintext">Authenticated control plaintext。</param>
        /// <param name="message">成功时返回 caller-owned payload。</param>
        /// <returns>Magic、version、kind、length 与 fixed width 全部合法时为 true。</returns>
        internal static bool TryDecodeServer(
            byte[] plaintext,
            out ClientBattleControlMessage message)
        {
            message = null;
            if (plaintext == null ||
                plaintext.Length <= EnvelopeBytes ||
                plaintext[0] != (byte)'I' ||
                plaintext[1] != (byte)'H' ||
                plaintext[2] != (byte)'B' ||
                plaintext[3] != (byte)'C' ||
                plaintext[4] != 1)
            {
                return false;
            }

            var kind = (ClientBattleControlKind)plaintext[5];
            var payloadLength =
                (plaintext[6] << 8) | plaintext[7];
            var expected = ExpectedServerPayloadBytes(kind);
            if (expected == 0 ||
                payloadLength != expected ||
                payloadLength + EnvelopeBytes != plaintext.Length)
            {
                return false;
            }

            var payload = new byte[payloadLength];
            Buffer.BlockCopy(
                plaintext,
                EnvelopeBytes,
                payload,
                0,
                payload.Length);
            message = new ClientBattleControlMessage(kind, payload);
            return true;
        }

        /// <summary>
        /// 编码通用 canonical control envelope。
        /// </summary>
        /// <param name="kind">Closed client-sendable kind。</param>
        /// <param name="payload">Exact-width payload。</param>
        /// <returns>Caller-owned plaintext。</returns>
        private static byte[] Encode(
            ClientBattleControlKind kind,
            byte[] payload)
        {
            if (payload == null ||
                payload.Length == 0 ||
                payload.Length > ushort.MaxValue)
            {
                throw new ArgumentException(
                    "client battle control payload is invalid",
                    nameof(payload));
            }

            var output = new byte[EnvelopeBytes + payload.Length];
            output[0] = (byte)'I';
            output[1] = (byte)'H';
            output[2] = (byte)'B';
            output[3] = (byte)'C';
            output[4] = 1;
            output[5] = (byte)kind;
            output[6] = (byte)(payload.Length >> 8);
            output[7] = (byte)payload.Length;
            Buffer.BlockCopy(
                payload,
                0,
                output,
                EnvelopeBytes,
                payload.Length);
            return output;
        }

        /// <summary>
        /// 返回服务端可发送 kind 的 fixed payload width。
        /// </summary>
        /// <param name="kind">Decoded kind。</param>
        /// <returns>未知或 client-only kind 为零。</returns>
        private static int ExpectedServerPayloadBytes(
            ClientBattleControlKind kind)
        {
            switch (kind)
            {
                case ClientBattleControlKind.RebindChallenge:
                    return 48;
                case ClientBattleControlKind.RebindCommitted:
                    return 20;
                case ClientBattleControlKind.RekeyCommitted:
                    return 32;
                case ClientBattleControlKind.CloseAcknowledged:
                    return 1;
                default:
                    return 0;
            }
        }

        /// <summary>
        /// 判断 fixed identity 是否全部为零。
        /// </summary>
        /// <param name="value">待验证 bytes。</param>
        /// <returns>全部为零时为 true。</returns>
        private static bool IsAllZero(byte[] value)
        {
            var aggregate = 0;
            foreach (var item in value)
            {
                aggregate |= item;
            }

            return aggregate == 0;
        }
    }
}
