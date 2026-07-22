using System;
using System.ComponentModel;
using System.Runtime.InteropServices;

namespace IHomeland.Client.Infrastructure.Security
{
    /// <summary>
    /// 表示 OS data protection 的封闭结果，不向上层暴露 native 错误文本。
    /// </summary>
    internal sealed class ClientDataProtectionResult
    {
        /// <summary>创建保护结果。</summary>
        /// <param name="succeeded">操作是否成功。</param>
        /// <param name="data">成功时由调用方拥有的 bytes。</param>
        private ClientDataProtectionResult(bool succeeded, byte[] data)
        {
            Succeeded = succeeded;
            Data = data;
        }

        /// <summary>获取操作是否成功。</summary>
        internal bool Succeeded { get; }

        /// <summary>获取成功 bytes；失败时为空。</summary>
        internal byte[] Data { get; }

        /// <summary>创建成功结果并转移 bytes ownership。</summary>
        /// <param name="data">非空结果 bytes。</param>
        /// <returns>成功结果。</returns>
        internal static ClientDataProtectionResult Success(byte[] data)
        {
            return new ClientDataProtectionResult(
                succeeded: true,
                data ?? throw new ArgumentNullException(nameof(data)));
        }

        /// <summary>创建不携带 native detail 的失败结果。</summary>
        /// <returns>稳定失败结果。</returns>
        internal static ClientDataProtectionResult Failure()
        {
            return new ClientDataProtectionResult(succeeded: false, data: null);
        }
    }

    /// <summary>
    /// 定义 secure session file owner 使用的数据保护窄口。
    /// </summary>
    internal interface IClientDataProtector
    {
        /// <summary>使用当前用户范围保护完整 plaintext。</summary>
        /// <param name="plaintext">由调用方继续拥有并负责归零的 plaintext。</param>
        /// <param name="entropy">稳定 product/environment isolation bytes。</param>
        /// <returns>新分配 ciphertext 或稳定失败。</returns>
        ClientDataProtectionResult Protect(byte[] plaintext, byte[] entropy);

        /// <summary>使用当前用户范围解保护完整 ciphertext。</summary>
        /// <param name="ciphertext">由调用方继续拥有的 ciphertext。</param>
        /// <param name="entropy">与保护时完全相同的 isolation bytes。</param>
        /// <returns>新分配 plaintext 或稳定失败。</returns>
        ClientDataProtectionResult Unprotect(byte[] ciphertext, byte[] entropy);
    }

    /// <summary>
    /// 通过 Windows DPAPI CurrentUser 实现完整 payload 保护。
    /// </summary>
    /// <remarks>
    /// Native output 使用 LocalAlloc，由本类型在复制后通过 LocalFree 释放；输入 CoTaskMem 和
    /// managed plaintext 都由各自 owner 在 finally 中归零。异常消息不得进入上层结果或日志。
    /// </remarks>
    internal sealed class WindowsDpapiDataProtector : IClientDataProtector
    {
        /// <summary>禁止 DPAPI 显示任何交互 UI。</summary>
        private const int CryptProtectUiForbidden = 0x1;

        /// <summary>保护完整 plaintext。</summary>
        /// <inheritdoc />
        public ClientDataProtectionResult Protect(byte[] plaintext, byte[] entropy)
        {
            return Transform(plaintext, entropy, protect: true);
        }

        /// <summary>解保护完整 ciphertext。</summary>
        /// <inheritdoc />
        public ClientDataProtectionResult Unprotect(byte[] ciphertext, byte[] entropy)
        {
            return Transform(ciphertext, entropy, protect: false);
        }

        /// <summary>执行共享 DPAPI 封送与确定性释放。</summary>
        /// <param name="input">待保护或解保护 bytes。</param>
        /// <param name="entropy">额外环境隔离 bytes。</param>
        /// <param name="protect">True 调用 CryptProtectData；false 调用 CryptUnprotectData。</param>
        /// <returns>新分配输出或低敏失败。</returns>
        private static ClientDataProtectionResult Transform(
            byte[] input,
            byte[] entropy,
            bool protect)
        {
            if (input == null)
            {
                throw new ArgumentNullException(nameof(input));
            }

            if (entropy == null)
            {
                throw new ArgumentNullException(nameof(entropy));
            }

            if (input.Length == 0 || entropy.Length == 0)
            {
                return ClientDataProtectionResult.Failure();
            }

            var inputBlob = default(DataBlob);
            var entropyBlob = default(DataBlob);
            var outputBlob = default(DataBlob);
            try
            {
                inputBlob = AllocateBlob(input);
                entropyBlob = AllocateBlob(entropy);
                var succeeded = protect
                    ? CryptProtectData(
                        ref inputBlob,
                        string.Empty,
                        ref entropyBlob,
                        IntPtr.Zero,
                        IntPtr.Zero,
                        CryptProtectUiForbidden,
                        out outputBlob)
                    : CryptUnprotectData(
                        ref inputBlob,
                        IntPtr.Zero,
                        ref entropyBlob,
                        IntPtr.Zero,
                        IntPtr.Zero,
                        CryptProtectUiForbidden,
                        out outputBlob);
                if (!succeeded || outputBlob.ByteCount <= 0 || outputBlob.Data == IntPtr.Zero)
                {
                    return ClientDataProtectionResult.Failure();
                }

                var output = new byte[outputBlob.ByteCount];
                Marshal.Copy(outputBlob.Data, output, 0, output.Length);
                return ClientDataProtectionResult.Success(output);
            }
            catch (DllNotFoundException)
            {
                return ClientDataProtectionResult.Failure();
            }
            catch (EntryPointNotFoundException)
            {
                return ClientDataProtectionResult.Failure();
            }
            catch (Win32Exception)
            {
                return ClientDataProtectionResult.Failure();
            }
            catch (ExternalException)
            {
                return ClientDataProtectionResult.Failure();
            }
            finally
            {
                FreeInputBlob(ref inputBlob);
                FreeInputBlob(ref entropyBlob);
                FreeOutputBlob(ref outputBlob);
            }
        }

        /// <summary>复制 managed bytes 到需要显式归零的 CoTaskMem。</summary>
        /// <param name="source">待复制 bytes。</param>
        /// <returns>拥有 CoTaskMem 的 DATA_BLOB。</returns>
        private static DataBlob AllocateBlob(byte[] source)
        {
            var pointer = Marshal.AllocCoTaskMem(source.Length);
            Marshal.Copy(source, 0, pointer, source.Length);
            return new DataBlob
            {
                ByteCount = source.Length,
                Data = pointer,
            };
        }

        /// <summary>归零并释放调用方分配的 CoTaskMem。</summary>
        /// <param name="blob">待释放输入 blob。</param>
        private static void FreeInputBlob(ref DataBlob blob)
        {
            if (blob.Data == IntPtr.Zero)
            {
                return;
            }

            var zeros = new byte[Math.Max(0, blob.ByteCount)];
            try
            {
                if (zeros.Length > 0)
                {
                    Marshal.Copy(zeros, 0, blob.Data, zeros.Length);
                }
            }
            finally
            {
                Array.Clear(zeros, 0, zeros.Length);
                Marshal.FreeCoTaskMem(blob.Data);
                blob = default;
            }
        }

        /// <summary>归零并释放 DPAPI 返回的 LocalAlloc buffer。</summary>
        /// <param name="blob">待释放输出 blob。</param>
        private static void FreeOutputBlob(ref DataBlob blob)
        {
            if (blob.Data == IntPtr.Zero)
            {
                return;
            }

            var zeros = new byte[Math.Max(0, blob.ByteCount)];
            try
            {
                if (zeros.Length > 0)
                {
                    Marshal.Copy(zeros, 0, blob.Data, zeros.Length);
                }
            }
            finally
            {
                Array.Clear(zeros, 0, zeros.Length);
                LocalFree(blob.Data);
                blob = default;
            }
        }

        /// <summary>表示 Win32 DATA_BLOB 的精确布局。</summary>
        [StructLayout(LayoutKind.Sequential)]
        private struct DataBlob
        {
            /// <summary>Buffer 字节数。</summary>
            internal int ByteCount;

            /// <summary>Buffer 起始地址。</summary>
            internal IntPtr Data;
        }

        /// <summary>使用当前 Windows 用户 scope 保护 bytes。</summary>
        [DllImport("crypt32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        [return: MarshalAs(UnmanagedType.Bool)]
        private static extern bool CryptProtectData(
            ref DataBlob input,
            string description,
            ref DataBlob optionalEntropy,
            IntPtr reserved,
            IntPtr prompt,
            int flags,
            out DataBlob output);

        /// <summary>使用当前 Windows 用户 scope 解保护 bytes。</summary>
        [DllImport("crypt32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        [return: MarshalAs(UnmanagedType.Bool)]
        private static extern bool CryptUnprotectData(
            ref DataBlob input,
            IntPtr description,
            ref DataBlob optionalEntropy,
            IntPtr reserved,
            IntPtr prompt,
            int flags,
            out DataBlob output);

        /// <summary>释放 DPAPI 返回的 LocalAlloc memory。</summary>
        [DllImport("kernel32.dll", SetLastError = false)]
        private static extern IntPtr LocalFree(IntPtr memory);
    }
}
