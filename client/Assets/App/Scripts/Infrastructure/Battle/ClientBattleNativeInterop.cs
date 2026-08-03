using System;
using System.Runtime.InteropServices;

namespace IHomeland.Client.Infrastructure.Battle
{
    /// <summary>
    /// 定义 client battle native version 1 的稳定结果码。
    /// </summary>
    internal enum ClientBattleNativeStatus
    {
        /// <summary>操作完成。</summary>
        Ok = 0,

        /// <summary>调用参数违反固定宽度或空值契约。</summary>
        InvalidArgument = 1,

        /// <summary>crypto primitive 执行失败。</summary>
        CryptoFailure = 2,

        /// <summary>AEAD authentication 失败。</summary>
        AuthenticationFailure = 3,

        /// <summary>caller buffer 不足，required length 已返回。</summary>
        BufferTooSmall = 4,

        /// <summary>KCP 有界 queue 已满。</summary>
        QueueFull = 5,

        /// <summary>KCP primitive 拒绝 malformed input 或进入内部失败。</summary>
        KcpFailure = 6,

        /// <summary>generation-scoped context 已关闭。</summary>
        Closed = 7,
    }

    /// <summary>
    /// 把 native 结果映射为不携带 secret 或 payload 的稳定异常。
    /// </summary>
    internal sealed class ClientBattleNativeException : InvalidOperationException
    {
        /// <summary>
        /// 初始化低敏 native failure。
        /// </summary>
        /// <param name="operation">固定 operation 名称。</param>
        /// <param name="status">稳定 native 结果码。</param>
        internal ClientBattleNativeException(
            string operation,
            ClientBattleNativeStatus status)
            : base($"client battle native {operation} failed: {status}")
        {
            Operation = operation;
            Status = status;
        }

        /// <summary>
        /// 获取固定 operation 名称。
        /// </summary>
        internal string Operation { get; }

        /// <summary>
        /// 获取稳定 native 结果码。
        /// </summary>
        internal ClientBattleNativeStatus Status { get; }
    }

    /// <summary>
    /// 声明 Windows x64 generated plugin 的 versioned C ABI。
    /// </summary>
    internal static class ClientBattleNativeInterop
    {
        /// <summary>
        /// 保存不含平台扩展名的 Unity native plugin 名。
        /// </summary>
        private const string LibraryName = "ihomeland_client_battle_native";

        /// <summary>
        /// 读取 native ABI version。
        /// </summary>
        /// <returns>当前 ABI version。</returns>
        [DllImport(
            LibraryName,
            EntryPoint = "ihbr_abi_version",
            CallingConvention = CallingConvention.Cdecl)]
        internal static extern uint AbiVersion();

        /// <summary>
        /// 验证 libsodium 固定宽度。
        /// </summary>
        /// <returns>稳定 native 结果码。</returns>
        [DllImport(
            LibraryName,
            EntryPoint = "ihbr_initialize",
            CallingConvention = CallingConvention.Cdecl)]
        internal static extern ClientBattleNativeStatus Initialize();

        /// <summary>
        /// 使用 CSPRNG 填充 caller buffer。
        /// </summary>
        /// <param name="output">非空输出。</param>
        /// <param name="outputLength">输出宽度。</param>
        /// <returns>稳定 native 结果码。</returns>
        [DllImport(
            LibraryName,
            EntryPoint = "ihbr_random_fill",
            CallingConvention = CallingConvention.Cdecl)]
        internal static extern ClientBattleNativeStatus RandomFill(
            [Out] byte[] output,
            uint outputLength);

        /// <summary>
        /// 派生 X25519 public key。
        /// </summary>
        /// <param name="secretScalar">32-byte secret scalar。</param>
        /// <param name="publicKey">32-byte public key 输出。</param>
        /// <returns>稳定 native 结果码。</returns>
        [DllImport(
            LibraryName,
            EntryPoint = "ihbr_x25519_public",
            CallingConvention = CallingConvention.Cdecl)]
        internal static extern ClientBattleNativeStatus X25519Public(
            [In] byte[] secretScalar,
            [Out] byte[] publicKey);

        /// <summary>
        /// 计算 X25519 shared secret。
        /// </summary>
        /// <param name="secretScalar">32-byte secret scalar。</param>
        /// <param name="peerPublicKey">32-byte peer public key。</param>
        /// <param name="sharedSecret">32-byte secret 输出。</param>
        /// <returns>稳定 native 结果码。</returns>
        [DllImport(
            LibraryName,
            EntryPoint = "ihbr_x25519_shared",
            CallingConvention = CallingConvention.Cdecl)]
        internal static extern ClientBattleNativeStatus X25519Shared(
            [In] byte[] secretScalar,
            [In] byte[] peerPublicKey,
            [Out] byte[] sharedSecret);

        /// <summary>
        /// 计算 HMAC-SHA-256。
        /// </summary>
        /// <param name="key">非空 key。</param>
        /// <param name="keyLength">key 宽度。</param>
        /// <param name="message">允许为空的 message。</param>
        /// <param name="messageLength">message 宽度。</param>
        /// <param name="tag">32-byte tag 输出。</param>
        /// <returns>稳定 native 结果码。</returns>
        [DllImport(
            LibraryName,
            EntryPoint = "ihbr_hmac_sha256",
            CallingConvention = CallingConvention.Cdecl)]
        internal static extern ClientBattleNativeStatus HmacSha256(
            [In] byte[] key,
            uint keyLength,
            [In] byte[] message,
            uint messageLength,
            [Out] byte[] tag);

        /// <summary>
        /// 执行 HKDF-SHA-256 extract/expand。
        /// </summary>
        /// <param name="inputKeyMaterial">非空 IKM。</param>
        /// <param name="inputKeyMaterialLength">IKM 宽度。</param>
        /// <param name="salt">允许为空的 salt。</param>
        /// <param name="saltLength">salt 宽度。</param>
        /// <param name="info">允许为空的 info。</param>
        /// <param name="infoLength">info 宽度。</param>
        /// <param name="output">caller-owned 输出。</param>
        /// <param name="outputLength">输出宽度。</param>
        /// <returns>稳定 native 结果码。</returns>
        [DllImport(
            LibraryName,
            EntryPoint = "ihbr_hkdf_sha256",
            CallingConvention = CallingConvention.Cdecl)]
        internal static extern ClientBattleNativeStatus HkdfSha256(
            [In] byte[] inputKeyMaterial,
            uint inputKeyMaterialLength,
            [In] byte[] salt,
            uint saltLength,
            [In] byte[] info,
            uint infoLength,
            [Out] byte[] output,
            uint outputLength);

        /// <summary>
        /// 执行 detached ChaCha20-Poly1305 seal。
        /// </summary>
        /// <param name="key">32-byte key。</param>
        /// <param name="nonce">12-byte nonce。</param>
        /// <param name="aad">允许为空的 AAD。</param>
        /// <param name="aadLength">AAD 宽度。</param>
        /// <param name="plaintext">非空 plaintext。</param>
        /// <param name="plaintextLength">plaintext 宽度。</param>
        /// <param name="ciphertext">同宽 ciphertext 输出。</param>
        /// <param name="tag">16-byte tag 输出。</param>
        /// <returns>稳定 native 结果码。</returns>
        [DllImport(
            LibraryName,
            EntryPoint = "ihbr_chacha20poly1305_seal",
            CallingConvention = CallingConvention.Cdecl)]
        internal static extern ClientBattleNativeStatus Seal(
            [In] byte[] key,
            [In] byte[] nonce,
            [In] byte[] aad,
            uint aadLength,
            [In] byte[] plaintext,
            uint plaintextLength,
            [Out] byte[] ciphertext,
            [Out] byte[] tag);

        /// <summary>
        /// 验证 detached tag 并打开 ciphertext。
        /// </summary>
        /// <param name="key">32-byte key。</param>
        /// <param name="nonce">12-byte nonce。</param>
        /// <param name="aad">允许为空的 AAD。</param>
        /// <param name="aadLength">AAD 宽度。</param>
        /// <param name="ciphertext">非空 ciphertext。</param>
        /// <param name="ciphertextLength">ciphertext 宽度。</param>
        /// <param name="tag">16-byte tag。</param>
        /// <param name="plaintext">同宽 plaintext 输出。</param>
        /// <returns>稳定 native 结果码。</returns>
        [DllImport(
            LibraryName,
            EntryPoint = "ihbr_chacha20poly1305_open",
            CallingConvention = CallingConvention.Cdecl)]
        internal static extern ClientBattleNativeStatus Open(
            [In] byte[] key,
            [In] byte[] nonce,
            [In] byte[] aad,
            uint aadLength,
            [In] byte[] ciphertext,
            uint ciphertextLength,
            [In] byte[] tag,
            [Out] byte[] plaintext);

        /// <summary>
        /// constant-time 比较相同宽度的值。
        /// </summary>
        /// <param name="first">第一个值。</param>
        /// <param name="second">第二个值。</param>
        /// <param name="length">共同宽度。</param>
        /// <param name="equal">1 表示相等，否则为 0。</param>
        /// <returns>稳定 native 结果码。</returns>
        [DllImport(
            LibraryName,
            EntryPoint = "ihbr_constant_time_equal",
            CallingConvention = CallingConvention.Cdecl)]
        internal static extern ClientBattleNativeStatus ConstantTimeEqual(
            [In] byte[] first,
            [In] byte[] second,
            uint length,
            out byte equal);

        /// <summary>
        /// 清零 caller-owned secret。
        /// </summary>
        /// <param name="material">非空 secret buffer。</param>
        /// <param name="materialLength">buffer 宽度。</param>
        /// <returns>稳定 native 结果码。</returns>
        [DllImport(
            LibraryName,
            EntryPoint = "ihbr_secure_zero",
            CallingConvention = CallingConvention.Cdecl)]
        internal static extern ClientBattleNativeStatus SecureZero(
            [In, Out] byte[] material,
            uint materialLength);

        /// <summary>
        /// 创建 exact profile KCP handle。
        /// </summary>
        /// <param name="conversation">非零 conversation。</param>
        /// <param name="context">opaque native context。</param>
        /// <returns>稳定 native 结果码。</returns>
        [DllImport(
            LibraryName,
            EntryPoint = "ihbr_kcp_create",
            CallingConvention = CallingConvention.Cdecl)]
        internal static extern ClientBattleNativeStatus KcpCreate(
            uint conversation,
            out IntPtr context);

        /// <summary>
        /// 幂等释放 opaque KCP handle。
        /// </summary>
        /// <param name="context">释放后会被置空的 handle。</param>
        /// <returns>稳定 native 结果码。</returns>
        [DllImport(
            LibraryName,
            EntryPoint = "ihbr_kcp_release",
            CallingConvention = CallingConvention.Cdecl)]
        internal static extern ClientBattleNativeStatus KcpRelease(
            ref IntPtr context);

        /// <summary>
        /// 向 KCP 写入一个有界 message。
        /// </summary>
        /// <param name="context">current generation handle。</param>
        /// <param name="message">非空 message。</param>
        /// <param name="messageLength">message 宽度。</param>
        /// <returns>稳定 native 结果码。</returns>
        [DllImport(
            LibraryName,
            EntryPoint = "ihbr_kcp_send",
            CallingConvention = CallingConvention.Cdecl)]
        internal static extern ClientBattleNativeStatus KcpSend(
            ClientBattleKcpSafeHandle context,
            [In] byte[] message,
            uint messageLength);

        /// <summary>
        /// 输入一个有界 KCP segment。
        /// </summary>
        /// <param name="context">current generation handle。</param>
        /// <param name="segment">非空 segment。</param>
        /// <param name="segmentLength">segment 宽度。</param>
        /// <returns>稳定 native 结果码。</returns>
        [DllImport(
            LibraryName,
            EntryPoint = "ihbr_kcp_input",
            CallingConvention = CallingConvention.Cdecl)]
        internal static extern ClientBattleNativeStatus KcpInput(
            ClientBattleKcpSafeHandle context,
            [In] byte[] segment,
            uint segmentLength);

        /// <summary>
        /// 以 monotonic millisecond 驱动 KCP。
        /// </summary>
        /// <param name="context">current generation handle。</param>
        /// <param name="monotonicMilliseconds">32-bit wrap-compatible KCP clock。</param>
        /// <returns>稳定 native 结果码。</returns>
        [DllImport(
            LibraryName,
            EntryPoint = "ihbr_kcp_update",
            CallingConvention = CallingConvention.Cdecl)]
        internal static extern ClientBattleNativeStatus KcpUpdate(
            ClientBattleKcpSafeHandle context,
            uint monotonicMilliseconds);

        /// <summary>
        /// 取出一个 KCP output segment。
        /// </summary>
        /// <param name="context">current generation handle。</param>
        /// <param name="output">caller-owned buffer。</param>
        /// <param name="outputCapacity">buffer 宽度。</param>
        /// <param name="outputLength">实际或 required 宽度。</param>
        /// <returns>稳定 native 结果码。</returns>
        [DllImport(
            LibraryName,
            EntryPoint = "ihbr_kcp_next_output",
            CallingConvention = CallingConvention.Cdecl)]
        internal static extern ClientBattleNativeStatus KcpNextOutput(
            ClientBattleKcpSafeHandle context,
            [Out] byte[] output,
            uint outputCapacity,
            out uint outputLength);

        /// <summary>
        /// 取出一个重组完成的 application message。
        /// </summary>
        /// <param name="context">current generation handle。</param>
        /// <param name="output">caller-owned buffer。</param>
        /// <param name="outputCapacity">buffer 宽度。</param>
        /// <param name="outputLength">实际或 required 宽度。</param>
        /// <returns>稳定 native 结果码。</returns>
        [DllImport(
            LibraryName,
            EntryPoint = "ihbr_kcp_receive",
            CallingConvention = CallingConvention.Cdecl)]
        internal static extern ClientBattleNativeStatus KcpReceive(
            ClientBattleKcpSafeHandle context,
            [Out] byte[] output,
            uint outputCapacity,
            out uint outputLength);

        /// <summary>
        /// 读取 third-party send queue/buffer 数。
        /// </summary>
        /// <param name="context">current generation handle。</param>
        /// <param name="waitingSegments">有界 waiting 数。</param>
        /// <returns>稳定 native 结果码。</returns>
        [DllImport(
            LibraryName,
            EntryPoint = "ihbr_kcp_waiting",
            CallingConvention = CallingConvention.Cdecl)]
        internal static extern ClientBattleNativeStatus KcpWaiting(
            ClientBattleKcpSafeHandle context,
            out uint waitingSegments);
    }
}
