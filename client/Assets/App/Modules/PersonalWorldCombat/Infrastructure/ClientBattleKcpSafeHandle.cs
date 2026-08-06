using System;
using Microsoft.Win32.SafeHandles;

namespace IHomeland.Client.PersonalWorldCombat.Infrastructure
{
    /// <summary>
    /// 以 SafeHandle 约束单个 battle generation 的 native KCP context。
    /// </summary>
    internal sealed class ClientBattleKcpSafeHandle : SafeHandleZeroOrMinusOneIsInvalid
    {
        /// <summary>
        /// 为 marshaller 创建空 handle。
        /// </summary>
        private ClientBattleKcpSafeHandle()
            : base(true)
        {
        }

        /// <summary>
        /// 包装已创建的 native handle。
        /// </summary>
        /// <param name="handle">非零 native context。</param>
        private ClientBattleKcpSafeHandle(IntPtr handle)
            : base(true)
        {
            SetHandle(handle);
        }

        /// <summary>
        /// 创建 exact KCP profile context，并在失败时释放 partial handle。
        /// </summary>
        /// <param name="conversation">current generation 的非零 conversation。</param>
        /// <returns>拥有 native context 的 SafeHandle。</returns>
        internal static ClientBattleKcpSafeHandle Create(uint conversation)
        {
            if (conversation == 0)
            {
                throw new ArgumentOutOfRangeException(nameof(conversation));
            }

            IntPtr context = IntPtr.Zero;
            var status = ClientBattleNativeInterop.KcpCreate(conversation, out context);
            if (status != ClientBattleNativeStatus.Ok || context == IntPtr.Zero)
            {
                if (context != IntPtr.Zero)
                {
                    ClientBattleNativeInterop.KcpRelease(ref context);
                }

                throw new ClientBattleNativeException("kcp-create", status);
            }

            return new ClientBattleKcpSafeHandle(context);
        }

        /// <summary>
        /// 幂等释放 context；SafeHandle finalizer 也只走同一 C ABI。
        /// </summary>
        /// <returns>无论 native 已释放与否都返回 true，防止重复 finalization。</returns>
        protected override bool ReleaseHandle()
        {
            var current = handle;
            var status = ClientBattleNativeInterop.KcpRelease(ref current);
            SetHandle(IntPtr.Zero);
            return status == ClientBattleNativeStatus.Ok;
        }
    }
}
