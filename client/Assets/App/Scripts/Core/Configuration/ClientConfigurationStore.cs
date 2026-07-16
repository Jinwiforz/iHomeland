using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Core.Lifetime;
using IHomeland.Client.Infrastructure.Http;

namespace IHomeland.Client.Core.Configuration
{
    /// <summary>
    /// 标识客户端是否已经取得可用于认证的完整启动配置。
    /// </summary>
    internal enum ClientConfigurationState
    {
        /// <summary>
        /// 尚未完成 version/config bootstrap。
        /// </summary>
        Empty = 0,

        /// <summary>
        /// 已原子发布兼容版本与公开配置。
        /// </summary>
        Ready = 1,

        /// <summary>
        /// 服务端协议或 minimum client version 不兼容。
        /// </summary>
        Incompatible = 2,

        /// <summary>
        /// App Scope 已停止且不得再发布配置。
        /// </summary>
        Stopped = 3,
    }

    /// <summary>
    /// 保存 version 与 config 同一次成功 bootstrap 的不可变原子快照。
    /// </summary>
    internal sealed class ClientConfigurationSnapshot
    {
        /// <summary>
        /// 创建完整启动快照。
        /// </summary>
        /// <param name="version">已通过客户端兼容性检查的服务端版本。</param>
        /// <param name="configuration">已完整验证的公开 endpoint 与 limits。</param>
        internal ClientConfigurationSnapshot(
            ClientVersionInfo version,
            ClientBootstrapConfiguration configuration)
        {
            Version = version ?? throw new ArgumentNullException(nameof(version));
            Configuration = configuration ?? throw new ArgumentNullException(nameof(configuration));
        }

        /// <summary>
        /// 获取与当前配置一同提交的服务端版本投影。
        /// </summary>
        internal ClientVersionInfo Version { get; }

        /// <summary>
        /// 获取当前公开 endpoint 与 limits 快照。
        /// </summary>
        internal ClientBootstrapConfiguration Configuration { get; }
    }

    /// <summary>
    /// 作为 App Scope 唯一 Configuration owner 原子发布兼容启动投影。
    /// </summary>
    internal sealed class ClientConfigurationStore : IAppLifetimeParticipant
    {
        /// <summary>
        /// 保护 state 与 snapshot 的同一提交边界。
        /// </summary>
        private readonly object _sync = new object();

        /// <summary>
        /// 保存当前不可逆停止状态和最近 bootstrap 结论。
        /// </summary>
        private ClientConfigurationState _state = ClientConfigurationState.Empty;

        /// <summary>
        /// 保存最近完整成功投影；未 Ready 时为空。
        /// </summary>
        private ClientConfigurationSnapshot _snapshot;

        /// <summary>
        /// 表示该 owner 已由 AppLifetime 初始化，停止后不能重启。
        /// </summary>
        private bool _initialized;

        /// <summary>
        /// 获取当前配置状态快照。
        /// </summary>
        internal ClientConfigurationState State
        {
            get
            {
                lock (_sync)
                {
                    return _state;
                }
            }
        }

        /// <summary>
        /// 在不访问网络的情况下启用配置发布。
        /// </summary>
        /// <param name="cancellationToken">启动前检查的 AppLifetime 取消信号。</param>
        /// <returns>Owner 已允许显式 bootstrap 时完成的任务。</returns>
        /// <exception cref="InvalidOperationException">重复初始化或停止后重启时抛出。</exception>
        public Task InitializeAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            lock (_sync)
            {
                if (_initialized || _state == ClientConfigurationState.Stopped)
                {
                    throw new InvalidOperationException("ClientConfigurationStore 不能重复初始化或停止后重启。");
                }

                _initialized = true;
            }

            return Task.CompletedTask;
        }

        /// <summary>
        /// 原子发布完整 version/config 投影，替换旧成功快照。
        /// </summary>
        /// <param name="snapshot">已经通过全部 codec 与兼容性检查的新快照。</param>
        /// <exception cref="ArgumentNullException">Snapshot 为空时抛出。</exception>
        /// <exception cref="InvalidOperationException">Owner 尚未初始化或已经停止时抛出。</exception>
        internal void Publish(ClientConfigurationSnapshot snapshot)
        {
            if (snapshot == null)
            {
                throw new ArgumentNullException(nameof(snapshot));
            }

            lock (_sync)
            {
                EnsureWritable();
                _snapshot = snapshot;
                _state = ClientConfigurationState.Ready;
            }
        }

        /// <summary>
        /// 标记版本不兼容并撤销旧配置的认证资格。
        /// </summary>
        /// <exception cref="InvalidOperationException">Owner 尚未初始化或已经停止时抛出。</exception>
        internal void MarkIncompatible()
        {
            lock (_sync)
            {
                EnsureWritable();
                _snapshot = null;
                _state = ClientConfigurationState.Incompatible;
            }
        }

        /// <summary>
        /// 尝试取得当前完整配置，不返回可变内部引用。
        /// </summary>
        /// <param name="snapshot">Ready 时返回不可变快照。</param>
        /// <returns>当前状态为 Ready 时返回 true。</returns>
        internal bool TryGetCurrent(out ClientConfigurationSnapshot snapshot)
        {
            lock (_sync)
            {
                snapshot = _snapshot;
                return _state == ClientConfigurationState.Ready && snapshot != null;
            }
        }

        /// <summary>
        /// 停止后撤销配置与认证资格，防止旧 App Scope 被迟到 callback 使用。
        /// </summary>
        /// <param name="cancellationToken">同步清理不等待该信号。</param>
        /// <returns>状态已清空时完成的任务。</returns>
        public Task StopAsync(CancellationToken cancellationToken)
        {
            lock (_sync)
            {
                _snapshot = null;
                _state = ClientConfigurationState.Stopped;
            }

            return Task.CompletedTask;
        }

        /// <summary>
        /// 验证当前 owner 已初始化且仍允许写入。
        /// </summary>
        /// <exception cref="InvalidOperationException">生命周期不允许写入时抛出。</exception>
        private void EnsureWritable()
        {
            if (!_initialized || _state == ClientConfigurationState.Stopped)
            {
                throw new InvalidOperationException("ClientConfigurationStore 当前生命周期不允许发布配置。");
            }
        }
    }
}
