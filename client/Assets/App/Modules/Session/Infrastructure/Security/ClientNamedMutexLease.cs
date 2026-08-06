using System;
using System.Threading;

namespace IHomeland.Client.Session.Infrastructure.Security
{
    /// <summary>
    /// 在专用线程上持有 Windows named mutex，避免 async 生命周期跨线程释放 mutex。
    /// </summary>
    /// <remarks>
    /// Windows mutex ownership 属于获取它的线程而不是 Task 或进程。该 lease 让同一后台线程
    /// 完成 WaitOne 与 ReleaseMutex；进程异常退出时 kernel 仍会把 lease 标记为 abandoned。
    /// </remarks>
    internal sealed class ClientNamedMutexLease : IDisposable
    {
        /// <summary>限制初始化握手，避免线程启动故障导致调用方无限等待。</summary>
        private const int StartupTimeoutMilliseconds = 5000;

        /// <summary>限制正常释放握手。</summary>
        private const int ShutdownTimeoutMilliseconds = 5000;

        /// <summary>通知调用方 acquire 已得出结论。</summary>
        private readonly ManualResetEventSlim _started = new ManualResetEventSlim(false);

        /// <summary>通知 owner thread 释放 mutex。</summary>
        private readonly ManualResetEventSlim _release = new ManualResetEventSlim(false);

        /// <summary>唯一执行 WaitOne 与 ReleaseMutex 的后台线程。</summary>
        private readonly Thread _ownerThread;

        /// <summary>低敏 named mutex 标识。</summary>
        private readonly string _name;

        /// <summary>Owner thread 是否成功取得 mutex。</summary>
        private bool _acquired;

        /// <summary>Owner thread 是否发生不可恢复故障。</summary>
        private bool _ownerFailed;

        /// <summary>Lease 是否已经释放。</summary>
        private bool _disposed;

        /// <summary>创建但尚未启动 named mutex owner。</summary>
        /// <param name="name">只含 product scope 与路径 digest 的 mutex name。</param>
        private ClientNamedMutexLease(string name)
        {
            _name = name ?? throw new ArgumentNullException(nameof(name));
            _ownerThread = new Thread(OwnMutex)
            {
                IsBackground = true,
                Name = "iHomeland secure session mutex owner",
            };
        }

        /// <summary>尝试取得 named mutex 的进程生命周期 lease。</summary>
        /// <param name="name">低敏、稳定的 Windows named mutex name。</param>
        /// <returns>成功 lease；另一个 owner 已持有时返回 null。</returns>
        internal static ClientNamedMutexLease TryAcquire(string name)
        {
            if (string.IsNullOrWhiteSpace(name))
            {
                throw new ArgumentException("Named mutex name 不能为空。", nameof(name));
            }

            var lease = new ClientNamedMutexLease(name);
            lease._ownerThread.Start();
            if (!lease._started.Wait(StartupTimeoutMilliseconds))
            {
                lease._release.Set();
                lease.JoinOwnerThread();
                lease.DisposeEvents();
                throw new InvalidOperationException("Secure session mutex owner 启动超时。");
            }

            if (lease._ownerFailed)
            {
                lease.JoinOwnerThread();
                lease.DisposeEvents();
                throw new InvalidOperationException("Secure session mutex owner 启动失败。");
            }

            if (!lease._acquired)
            {
                lease.JoinOwnerThread();
                lease.DisposeEvents();
                return null;
            }

            return lease;
        }

        /// <summary>通知 owner thread 释放 mutex 并等待确定完成。</summary>
        public void Dispose()
        {
            if (_disposed)
            {
                return;
            }

            _disposed = true;
            _release.Set();
            JoinOwnerThread();
            DisposeEvents();
            if (_ownerFailed)
            {
                throw new InvalidOperationException("Secure session mutex owner 停止失败。");
            }
        }

        /// <summary>在唯一 owner thread 上取得、持有并释放 mutex。</summary>
        private void OwnMutex()
        {
            try
            {
                using (var mutex = new Mutex(initiallyOwned: false, _name))
                {
                    try
                    {
                        _acquired = mutex.WaitOne(0, exitContext: false);
                    }
                    catch (AbandonedMutexException)
                    {
                        _acquired = true;
                    }

                    _started.Set();
                    if (!_acquired)
                    {
                        return;
                    }

                    _release.Wait();
                    mutex.ReleaseMutex();
                }
            }
            catch
            {
                _ownerFailed = true;
                _started.Set();
            }
        }

        /// <summary>等待 owner thread 在固定预算内退出。</summary>
        private void JoinOwnerThread()
        {
            if (!_ownerThread.Join(ShutdownTimeoutMilliseconds))
            {
                throw new InvalidOperationException("Secure session mutex owner 停止超时。");
            }
        }

        /// <summary>释放仅属于本 lease 的同步原语。</summary>
        private void DisposeEvents()
        {
            _started.Dispose();
            _release.Dispose();
        }
    }
}
