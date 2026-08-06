using System;

namespace IHomeland.Client.PersonalWorldCombat.Infrastructure
{
    /// <summary>
    /// 以固定 buffer 暴露 native crypto primitive，不保存 ticket、proof 或 traffic key。
    /// </summary>
    internal static class ClientBattleNativeCrypto
    {
        /// <summary>保存 X25519 与 traffic key 宽度。</summary>
        internal const int KeyBytes = 32;

        /// <summary>保存 IETF ChaCha20-Poly1305 nonce 宽度。</summary>
        internal const int NonceBytes = 12;

        /// <summary>保存 detached AEAD tag 宽度。</summary>
        internal const int TagBytes = 16;

        /// <summary>
        /// 使用 CSPRNG 填充 caller-owned buffer。
        /// </summary>
        /// <param name="output">非空输出。</param>
        internal static void RandomFill(byte[] output)
        {
            ValidateNonEmpty(output, nameof(output));
            ThrowIfFailure(
                "random-fill",
                ClientBattleNativeInterop.RandomFill(
                    output,
                    checked((uint)output.Length)));
        }

        /// <summary>
        /// 从 secret scalar 派生 X25519 public key。
        /// </summary>
        /// <param name="secretScalar">32-byte secret scalar。</param>
        /// <param name="publicKey">32-byte public key 输出。</param>
        internal static void X25519Public(byte[] secretScalar, byte[] publicKey)
        {
            ValidateExact(secretScalar, KeyBytes, nameof(secretScalar));
            ValidateExact(publicKey, KeyBytes, nameof(publicKey));
            ThrowIfFailure(
                "x25519-public",
                ClientBattleNativeInterop.X25519Public(secretScalar, publicKey));
        }

        /// <summary>
        /// 计算 X25519 shared secret。
        /// </summary>
        /// <param name="secretScalar">32-byte secret scalar。</param>
        /// <param name="peerPublicKey">32-byte peer public key。</param>
        /// <param name="sharedSecret">32-byte secret 输出。</param>
        internal static void X25519Shared(
            byte[] secretScalar,
            byte[] peerPublicKey,
            byte[] sharedSecret)
        {
            ValidateExact(secretScalar, KeyBytes, nameof(secretScalar));
            ValidateExact(peerPublicKey, KeyBytes, nameof(peerPublicKey));
            ValidateExact(sharedSecret, KeyBytes, nameof(sharedSecret));
            var status = ClientBattleNativeInterop.X25519Shared(
                secretScalar,
                peerPublicKey,
                sharedSecret);
            if (status != ClientBattleNativeStatus.Ok)
            {
                Array.Clear(sharedSecret, 0, sharedSecret.Length);
                throw new ClientBattleNativeException("x25519-shared", status);
            }
        }

        /// <summary>
        /// 计算 HMAC-SHA-256。
        /// </summary>
        /// <param name="key">非空 key。</param>
        /// <param name="message">允许为空的 message。</param>
        /// <param name="tag">32-byte tag 输出。</param>
        internal static void HmacSha256(byte[] key, byte[] message, byte[] tag)
        {
            ValidateNonEmpty(key, nameof(key));
            ValidateOptional(message, nameof(message));
            ValidateExact(tag, KeyBytes, nameof(tag));
            ThrowIfFailure(
                "hmac-sha256",
                ClientBattleNativeInterop.HmacSha256(
                    key,
                    checked((uint)key.Length),
                    message,
                    checked((uint)message.Length),
                    tag));
        }

        /// <summary>
        /// 执行 HKDF-SHA-256 extract/expand。
        /// </summary>
        /// <param name="inputKeyMaterial">非空 IKM。</param>
        /// <param name="salt">允许为空的 salt。</param>
        /// <param name="info">允许为空的 info。</param>
        /// <param name="output">非空 caller-owned 输出。</param>
        internal static void HkdfSha256(
            byte[] inputKeyMaterial,
            byte[] salt,
            byte[] info,
            byte[] output)
        {
            ValidateNonEmpty(inputKeyMaterial, nameof(inputKeyMaterial));
            ValidateOptional(salt, nameof(salt));
            ValidateOptional(info, nameof(info));
            ValidateNonEmpty(output, nameof(output));
            var status = ClientBattleNativeInterop.HkdfSha256(
                inputKeyMaterial,
                checked((uint)inputKeyMaterial.Length),
                salt,
                checked((uint)salt.Length),
                info,
                checked((uint)info.Length),
                output,
                checked((uint)output.Length));
            if (status != ClientBattleNativeStatus.Ok)
            {
                Array.Clear(output, 0, output.Length);
                throw new ClientBattleNativeException("hkdf-sha256", status);
            }
        }

        /// <summary>
        /// 执行 detached ChaCha20-Poly1305 seal。
        /// </summary>
        /// <param name="key">32-byte key。</param>
        /// <param name="nonce">12-byte nonce。</param>
        /// <param name="aad">允许为空的 AAD。</param>
        /// <param name="plaintext">非空 plaintext。</param>
        /// <param name="ciphertext">与 plaintext 同宽输出。</param>
        /// <param name="tag">16-byte tag 输出。</param>
        internal static void Seal(
            byte[] key,
            byte[] nonce,
            byte[] aad,
            byte[] plaintext,
            byte[] ciphertext,
            byte[] tag)
        {
            ValidateExact(key, KeyBytes, nameof(key));
            ValidateExact(nonce, NonceBytes, nameof(nonce));
            ValidateOptional(aad, nameof(aad));
            ValidateNonEmpty(plaintext, nameof(plaintext));
            ValidateExact(ciphertext, plaintext.Length, nameof(ciphertext));
            ValidateExact(tag, TagBytes, nameof(tag));
            ThrowIfFailure(
                "chacha20poly1305-seal",
                ClientBattleNativeInterop.Seal(
                    key,
                    nonce,
                    aad,
                    checked((uint)aad.Length),
                    plaintext,
                    checked((uint)plaintext.Length),
                    ciphertext,
                    tag));
        }

        /// <summary>
        /// 验证 tag 后打开 ciphertext；authentication failure 不抛出 secret-bearing 异常。
        /// </summary>
        /// <param name="key">32-byte key。</param>
        /// <param name="nonce">12-byte nonce。</param>
        /// <param name="aad">允许为空的 AAD。</param>
        /// <param name="ciphertext">非空 ciphertext。</param>
        /// <param name="tag">16-byte tag。</param>
        /// <param name="plaintext">与 ciphertext 同宽输出。</param>
        /// <returns>authentication 成功时为 true。</returns>
        internal static bool TryOpen(
            byte[] key,
            byte[] nonce,
            byte[] aad,
            byte[] ciphertext,
            byte[] tag,
            byte[] plaintext)
        {
            ValidateExact(key, KeyBytes, nameof(key));
            ValidateExact(nonce, NonceBytes, nameof(nonce));
            ValidateOptional(aad, nameof(aad));
            ValidateNonEmpty(ciphertext, nameof(ciphertext));
            ValidateExact(tag, TagBytes, nameof(tag));
            ValidateExact(plaintext, ciphertext.Length, nameof(plaintext));
            var status = ClientBattleNativeInterop.Open(
                key,
                nonce,
                aad,
                checked((uint)aad.Length),
                ciphertext,
                checked((uint)ciphertext.Length),
                tag,
                plaintext);
            if (status == ClientBattleNativeStatus.AuthenticationFailure)
            {
                Array.Clear(plaintext, 0, plaintext.Length);
                return false;
            }

            ThrowIfFailure("chacha20poly1305-open", status);
            return true;
        }

        /// <summary>
        /// constant-time 比较相同宽度的非空 buffer。
        /// </summary>
        /// <param name="first">第一个 buffer。</param>
        /// <param name="second">第二个 buffer。</param>
        /// <returns>所有 bytes 相同时为 true。</returns>
        internal static bool ConstantTimeEqual(byte[] first, byte[] second)
        {
            ValidateNonEmpty(first, nameof(first));
            ValidateExact(second, first.Length, nameof(second));
            ThrowIfFailure(
                "constant-time-equal",
                ClientBattleNativeInterop.ConstantTimeEqual(
                    first,
                    second,
                    checked((uint)first.Length),
                    out var equal));
            return equal == 1;
        }

        /// <summary>
        /// 清零非空 caller-owned secret；native 不可用时仍执行 managed fallback clear。
        /// </summary>
        /// <param name="material">待清零 buffer。</param>
        internal static void SecureZero(byte[] material)
        {
            ValidateNonEmpty(material, nameof(material));
            try
            {
                ThrowIfFailure(
                    "secure-zero",
                    ClientBattleNativeInterop.SecureZero(
                        material,
                        checked((uint)material.Length)));
            }
            finally
            {
                Array.Clear(material, 0, material.Length);
            }
        }

        /// <summary>
        /// 验证非空 buffer。
        /// </summary>
        /// <param name="value">待验证 buffer。</param>
        /// <param name="parameterName">异常参数名。</param>
        private static void ValidateNonEmpty(byte[] value, string parameterName)
        {
            if (value == null || value.Length == 0)
            {
                throw new ArgumentException(
                    "client battle native buffer is empty",
                    parameterName);
            }
        }

        /// <summary>
        /// 验证允许空数组但不允许 null 的 buffer。
        /// </summary>
        /// <param name="value">待验证 buffer。</param>
        /// <param name="parameterName">异常参数名。</param>
        private static void ValidateOptional(byte[] value, string parameterName)
        {
            if (value == null)
            {
                throw new ArgumentNullException(parameterName);
            }
        }

        /// <summary>
        /// 验证 fixed-width buffer。
        /// </summary>
        /// <param name="value">待验证 buffer。</param>
        /// <param name="expectedLength">精确宽度。</param>
        /// <param name="parameterName">异常参数名。</param>
        private static void ValidateExact(
            byte[] value,
            int expectedLength,
            string parameterName)
        {
            if (value == null || value.Length != expectedLength)
            {
                throw new ArgumentException(
                    "client battle native buffer width mismatched",
                    parameterName);
            }
        }

        /// <summary>
        /// 把非成功 native status 映射为稳定低敏异常。
        /// </summary>
        /// <param name="operation">固定 operation 名称。</param>
        /// <param name="status">native 结果码。</param>
        private static void ThrowIfFailure(
            string operation,
            ClientBattleNativeStatus status)
        {
            if (status != ClientBattleNativeStatus.Ok)
            {
                throw new ClientBattleNativeException(operation, status);
            }
        }
    }
}
