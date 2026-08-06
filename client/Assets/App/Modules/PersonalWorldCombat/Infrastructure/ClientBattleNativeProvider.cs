using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Core.Foundation.Lifetime;

namespace IHomeland.Client.PersonalWorldCombat.Infrastructure
{
    /// <summary>
    /// 拥有 native primitive 的 App Scope 初始化与 generation-scoped KCP lease。
    /// </summary>
    internal sealed class ClientBattleNativeProvider : IAppLifetimeParticipant
    {
        /// <summary>
        /// 保存 expected native ABI version。
        /// </summary>
        private const uint ExpectedAbiVersion = 1;

        /// <summary>
        /// 串行化生命周期与 lease registry。
        /// </summary>
        private readonly object _gate = new object();

        /// <summary>
        /// 保存尚未释放的 generation lease。
        /// </summary>
        private readonly HashSet<ClientBattleKcpLease> _leases =
            new HashSet<ClientBattleKcpLease>();

        /// <summary>
        /// 表示 native provider 已通过 ABI 与 primitive 初始化。
        /// </summary>
        private bool _initialized;

        /// <summary>
        /// 表示 AppLifetime 已永久停止当前 provider。
        /// </summary>
        private bool _stopped;

#if DEVELOPMENT_BUILD || UNITY_EDITOR
        /// <summary>获取资格测试可观察的current native KCP lease数量。</summary>
        internal int QualificationLeaseCount
        {
            get
            {
                lock (_gate)
                {
                    return _leases.Count;
                }
            }
        }
#endif

        /// <summary>
        /// 验证 exact ABI 与 libsodium primitive；失败时不保留 partial resource。
        /// </summary>
        /// <param name="cancellationToken">AppLifetime 初始化取消信号。</param>
        /// <returns>初始化同步完成的任务。</returns>
        public Task InitializeAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            lock (_gate)
            {
                if (_stopped)
                {
                    throw new InvalidOperationException(
                        "client battle native provider is stopped");
                }

                if (_initialized)
                {
                    return Task.CompletedTask;
                }

                if (ClientBattleNativeInterop.AbiVersion() != ExpectedAbiVersion)
                {
                    throw new InvalidOperationException(
                        "client battle native ABI version mismatched");
                }

                var status = ClientBattleNativeInterop.Initialize();
                if (status != ClientBattleNativeStatus.Ok)
                {
                    throw new ClientBattleNativeException("initialize", status);
                }

                _initialized = true;
                return Task.CompletedTask;
            }
        }

        /// <summary>
        /// 为 current battle generation 创建一个唯一 KCP lease。
        /// </summary>
        /// <param name="battleGeneration">非零 battle generation。</param>
        /// <param name="conversation">session-derived 非零 KCP conversation。</param>
        /// <returns>由 caller 及时释放的 generation lease。</returns>
        internal ClientBattleKcpLease CreateKcp(
            ulong battleGeneration,
            uint conversation)
        {
            if (battleGeneration == 0)
            {
                throw new ArgumentOutOfRangeException(nameof(battleGeneration));
            }

            lock (_gate)
            {
                if (!_initialized || _stopped)
                {
                    throw new InvalidOperationException(
                        "client battle native provider is unavailable");
                }

                var handle = ClientBattleKcpSafeHandle.Create(conversation);
                var lease = new ClientBattleKcpLease(
                    this,
                    battleGeneration,
                    handle);
                if (!_leases.Add(lease))
                {
                    handle.Dispose();
                    throw new InvalidOperationException(
                        "client battle KCP lease registry rejected duplicate");
                }

                return lease;
            }
        }

        /// <summary>
        /// 逆序释放所有 generation context，并永久停止 provider。
        /// </summary>
        /// <param name="cancellationToken">AppLifetime 共享停止 deadline。</param>
        /// <returns>清理同步完成的任务。</returns>
        public Task StopAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            ClientBattleKcpLease[] leases;
            lock (_gate)
            {
                if (_stopped)
                {
                    return Task.CompletedTask;
                }

                _stopped = true;
                _initialized = false;
                leases = new ClientBattleKcpLease[_leases.Count];
                _leases.CopyTo(leases);
                _leases.Clear();
            }

            for (var index = leases.Length - 1; index >= 0; index--)
            {
                leases[index].ReleaseFromOwner();
            }

            return Task.CompletedTask;
        }

        /// <summary>
        /// 从 registry 释放 caller 主动结束的 lease。
        /// </summary>
        /// <param name="lease">待释放的 exact lease。</param>
        internal void Release(ClientBattleKcpLease lease)
        {
            if (lease == null)
            {
                return;
            }

            lock (_gate)
            {
                _leases.Remove(lease);
            }

            lease.ReleaseFromOwner();
        }
    }

    /// <summary>
    /// 暴露单 generation 的 fixed-buffer KCP primitive，并拒绝释放后的复用。
    /// </summary>
    internal sealed class ClientBattleKcpLease : IDisposable
    {
        /// <summary>
        /// 保存 native provider owner。
        /// </summary>
        private ClientBattleNativeProvider _owner;

        /// <summary>
        /// 保存 SafeHandle；释放时原子置空。
        /// </summary>
        private ClientBattleKcpSafeHandle _handle;

        /// <summary>
        /// 初始化 generation-scoped lease。
        /// </summary>
        /// <param name="owner">唯一 provider owner。</param>
        /// <param name="battleGeneration">非零 battle generation。</param>
        /// <param name="handle">已创建的 exact context。</param>
        internal ClientBattleKcpLease(
            ClientBattleNativeProvider owner,
            ulong battleGeneration,
            ClientBattleKcpSafeHandle handle)
        {
            _owner = owner ?? throw new ArgumentNullException(nameof(owner));
            _handle = handle ?? throw new ArgumentNullException(nameof(handle));
            BattleGeneration = battleGeneration;
        }

        /// <summary>
        /// 获取该 handle 唯一接受的 battle generation。
        /// </summary>
        internal ulong BattleGeneration { get; }

        /// <summary>
        /// 向 native KCP 写入一个不超过 1000 bytes 的 message。
        /// </summary>
        /// <param name="message">非空 application bytes。</param>
        internal void Send(byte[] message)
        {
            ValidateBoundedBuffer(message, 1000, nameof(message));
            ThrowIfFailure(
                "kcp-send",
                ClientBattleNativeInterop.KcpSend(
                    RequireHandle(),
                    message,
                    checked((uint)message.Length)));
        }

        /// <summary>
        /// 输入一个不超过 1024 bytes 的 KCP segment。
        /// </summary>
        /// <param name="segment">已由 managed secure lane 认证的 bytes。</param>
        internal void Input(byte[] segment)
        {
            ValidateBoundedBuffer(segment, 1024, nameof(segment));
            ThrowIfFailure(
                "kcp-input",
                ClientBattleNativeInterop.KcpInput(
                    RequireHandle(),
                    segment,
                    checked((uint)segment.Length)));
        }

        /// <summary>
        /// 驱动一次 fixed-profile KCP update。
        /// </summary>
        /// <param name="monotonicMilliseconds">current monotonic KCP clock。</param>
        internal void Update(uint monotonicMilliseconds)
        {
            ThrowIfFailure(
                "kcp-update",
                ClientBattleNativeInterop.KcpUpdate(
                    RequireHandle(),
                    monotonicMilliseconds));
        }

        /// <summary>
        /// 尝试读取一个待发送 KCP segment。
        /// </summary>
        /// <param name="buffer">至少 1024 bytes 的复用 buffer。</param>
        /// <param name="length">实际 bytes；没有输出时为 0。</param>
        /// <returns>存在一个完整 segment 时为 true。</returns>
        internal bool TryReadOutput(byte[] buffer, out int length)
        {
            ValidateExactMinimum(buffer, 1024, nameof(buffer));
            var status = ClientBattleNativeInterop.KcpNextOutput(
                RequireHandle(),
                buffer,
                checked((uint)buffer.Length),
                out var nativeLength);
            ThrowIfFailure("kcp-next-output", status);
            length = checked((int)nativeLength);
            return length != 0;
        }

        /// <summary>
        /// 尝试读取一个重组完成的 KCP message。
        /// </summary>
        /// <param name="buffer">至少 1000 bytes 的复用 buffer。</param>
        /// <param name="length">实际 bytes；没有 message 时为 0。</param>
        /// <returns>存在一个完整 message 时为 true。</returns>
        internal bool TryReceive(byte[] buffer, out int length)
        {
            ValidateExactMinimum(buffer, 1000, nameof(buffer));
            var status = ClientBattleNativeInterop.KcpReceive(
                RequireHandle(),
                buffer,
                checked((uint)buffer.Length),
                out var nativeLength);
            ThrowIfFailure("kcp-receive", status);
            length = checked((int)nativeLength);
            return length != 0;
        }

        /// <summary>
        /// 读取 current native send queue/buffer segment 数。
        /// </summary>
        /// <returns>有界 waiting segment 数。</returns>
        internal uint WaitingSegments()
        {
            ThrowIfFailure(
                "kcp-waiting",
                ClientBattleNativeInterop.KcpWaiting(
                    RequireHandle(),
                    out var waiting));
            return waiting;
        }

        /// <summary>
        /// 主动结束 generation lease；重复调用安全。
        /// </summary>
        public void Dispose()
        {
            var owner = Interlocked.Exchange(ref _owner, null);
            if (owner != null)
            {
                owner.Release(this);
            }
            else
            {
                ReleaseFromOwner();
            }
        }

        /// <summary>
        /// 由 provider stop 或 caller dispose 幂等释放 SafeHandle。
        /// </summary>
        internal void ReleaseFromOwner()
        {
            Interlocked.Exchange(ref _owner, null);
            var handle = Interlocked.Exchange(ref _handle, null);
            handle?.Dispose();
        }

        /// <summary>
        /// 获取 current handle，并在 generation 已结束时拒绝复用。
        /// </summary>
        /// <returns>尚未释放的 SafeHandle。</returns>
        private ClientBattleKcpSafeHandle RequireHandle()
        {
            var handle = _handle;
            if (handle == null || handle.IsClosed || handle.IsInvalid)
            {
                throw new ObjectDisposedException(nameof(ClientBattleKcpLease));
            }

            return handle;
        }

        /// <summary>
        /// 验证非空且不超过 hard limit 的 caller buffer。
        /// </summary>
        /// <param name="buffer">待验证 buffer。</param>
        /// <param name="maximum">允许的最大宽度。</param>
        /// <param name="parameterName">异常参数名。</param>
        private static void ValidateBoundedBuffer(
            byte[] buffer,
            int maximum,
            string parameterName)
        {
            if (buffer == null || buffer.Length == 0 || buffer.Length > maximum)
            {
                throw new ArgumentException(
                    "client battle native buffer violates hard limit",
                    parameterName);
            }
        }

        /// <summary>
        /// 验证 reusable output buffer 达到 fixed minimum。
        /// </summary>
        /// <param name="buffer">待验证 buffer。</param>
        /// <param name="minimum">要求的最小宽度。</param>
        /// <param name="parameterName">异常参数名。</param>
        private static void ValidateExactMinimum(
            byte[] buffer,
            int minimum,
            string parameterName)
        {
            if (buffer == null || buffer.Length < minimum)
            {
                throw new ArgumentException(
                    "client battle native output buffer is too small",
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
