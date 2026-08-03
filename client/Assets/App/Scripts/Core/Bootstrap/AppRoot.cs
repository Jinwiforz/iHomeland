using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Core.Composition;
using IHomeland.Client.Foundation.Lifetime;
#if DEVELOPMENT_BUILD || UNITY_EDITOR
using IHomeland.Client.Core.Qualification;
#endif
using UnityEngine;

namespace IHomeland.Client.Core.Bootstrap
{
    /// <summary>
    /// 承载唯一 App Scope 的 Unity 生命周期、主线程 drain 与显式 tick。
    /// </summary>
    /// <remarks>
    /// AppRoot 不保存账号、连接、世界或 UI 事实，也不向 feature 暴露全局 Instance。
    /// 静态字段只用于唯一性争用，并在 SubsystemRegistration 时清空以兼容关闭 Domain Reload。
    /// </remarks>
    [DisallowMultipleComponent]
    public sealed class AppRoot : MonoBehaviour
    {
        /// <summary>
        /// 保存当前进程内已经取得唯一性所有权的 AppRoot，不作为服务查询入口。
        /// </summary>
        private static AppRoot _claimedRoot;

        /// <summary>
        /// 保存 AppComposition 转交的不可变对象图；只允许绑定一次。
        /// </summary>
        private AppCompositionResult _composition;

        /// <summary>
        /// 保存启动任务，使测试和显式启动调用观察同一结果。
        /// </summary>
        private Task _startTask;

        /// <summary>
        /// 保存包含 claim 释放的共享停止任务，使显式停止与 Unity callback 观察同一终态。
        /// </summary>
        private Task _stopTask;

        /// <summary>
        /// 保存已捕获异常的停止观察任务，防止 Unity callback 产生未观察异常。
        /// </summary>
        private Task _stopObserverTask;

        /// <summary>
        /// 表示当前实例持有静态唯一性 claim，停止完成时必须对称释放。
        /// </summary>
        private bool _ownsClaim;

        /// <summary>
        /// 表示 Unity 已进入应用或Play生命周期终结，异步尽力清理失败只输出退出摘要。
        /// </summary>
        private bool _applicationQuitting;

        /// <summary>
        /// 获取当前 App Scope 生命周期状态；尚未绑定对象图时返回 Created。
        /// </summary>
        internal AppLifetimeState State => _composition == null
            ? AppLifetimeState.Created
            : _composition.Lifetime.State;

#if DEVELOPMENT_BUILD || UNITY_EDITOR
        /// <summary>获取资格运行可观察的进程内AppRoot唯一性owner数量。</summary>
        internal static int QualificationClaimedOwnerCount => ReferenceEquals(_claimedRoot, null) ? 0 : 1;
#endif

        /// <summary>
        /// 尝试取得进程内唯一 AppRoot 所有权。
        /// </summary>
        /// <returns>当前实例成为唯一 root 或已经持有 claim 时返回 true；存在其他 root 时返回 false。</returns>
        internal bool TryClaim()
        {
            if (_stopTask != null)
            {
                return false;
            }

            if (!ReferenceEquals(_claimedRoot, null) && !ReferenceEquals(_claimedRoot, this))
            {
                return false;
            }

            _claimedRoot = this;
            _ownsClaim = true;
            return true;
        }

        /// <summary>
        /// 绑定唯一 Composition 结果，并把当前 GameObject 提升为跨场景对象。
        /// </summary>
        /// <param name="composition">由当前唯一 AppComposition 创建的对象图。</param>
        /// <exception cref="ArgumentNullException">composition 为空时抛出。</exception>
        /// <exception cref="InvalidOperationException">未持有 claim 或重复绑定时抛出。</exception>
        internal void Attach(AppCompositionResult composition)
        {
            if (composition == null)
            {
                throw new ArgumentNullException(nameof(composition));
            }

            if (!_ownsClaim)
            {
                throw new InvalidOperationException("AppRoot 必须先取得唯一性 claim 才能绑定对象图。");
            }

            if (_composition != null)
            {
                throw new InvalidOperationException("AppRoot 已经绑定 App Scope 对象图。");
            }

            _composition = composition;
            DontDestroyOnLoad(gameObject);
        }

        /// <summary>
        /// 启动已绑定的 App Scope，并让重复调用共享生命周期结果。
        /// </summary>
        /// <returns>App Scope 进入 Running 或完成失败回滚时结束的任务。</returns>
        /// <exception cref="AppStartupException">参与者初始化失败或启动取消且回滚结束时通过返回任务抛出。</exception>
        /// <exception cref="InvalidOperationException">尚未绑定 Composition 时抛出。</exception>
        internal Task StartAsync()
        {
            if (_composition == null)
            {
                throw new InvalidOperationException("AppRoot 尚未绑定 AppComposition 结果。");
            }

            if (_startTask == null)
            {
                _startTask = _composition.Lifetime.StartAsync(CancellationToken.None);
            }

            return _startTask;
        }

        /// <summary>
        /// 显式请求 App Scope 幂等逆序停止，并在停止终态对称释放唯一性 claim。
        /// </summary>
        /// <remarks>
        /// Claim 的生命周期与 App Scope 可运行性一致，而不是与 Unity 延迟 Destroy 的帧时序一致。
        /// 即使清理失败，旧对象图也已经进入不可重启终态，因此仍必须允许下一套 root 争用。
        /// </remarks>
        /// <returns>全部可执行清理均已尝试且唯一性 claim 已释放后的共享停止任务。</returns>
        /// <exception cref="AppShutdownException">一个或多个参与者停止失败或超时时通过返回任务抛出。</exception>
        internal Task StopAsync()
        {
            if (_stopTask == null)
            {
                _stopTask = StopAndReleaseClaimAsync();
            }

            return _stopTask;
        }

#if DEVELOPMENT_BUILD || UNITY_EDITOR
        /// <summary>创建当前完整产品graph的Development资格只读诊断。</summary>
        /// <returns>不拥有生命周期或业务事实的诊断聚合器。</returns>
        /// <exception cref="InvalidOperationException">尚未绑定完整产品Composition时抛出。</exception>
        internal ClientQualificationDiagnostics CreateQualificationDiagnostics()
        {
            if (_composition == null)
            {
                throw new InvalidOperationException("AppRoot尚未绑定AppComposition结果。");
            }

            return _composition.CreateQualificationDiagnostics();
        }
#endif

        /// <summary>
        /// 每帧有界排空主线程 callback，并驱动 Composition 冻结的 tickable 快照。
        /// </summary>
        /// <remarks>
        /// 只有 Running 状态执行工作。callback 或单个 tick 异常会记录到 Unity Console，
        /// 但不会阻止同一帧其余已登记工作执行。
        /// </remarks>
        private void Update()
        {
            if (_composition == null || _composition.Lifetime.State != AppLifetimeState.Running)
            {
                return;
            }

            var drainResult = _composition.Dispatcher.Drain(_composition.MaximumDispatchesPerFrame);
            foreach (var callbackError in drainResult.Errors)
            {
                Debug.LogException(callbackError, this);
            }

            foreach (var tickable in _composition.Tickables)
            {
                try
                {
                    tickable.Tick(Time.unscaledDeltaTime);
                }
                catch (Exception tickError)
                {
                    Debug.LogException(tickError, this);
                }
            }
        }

        /// <summary>
        /// 在 Unity 退出流程开始时触发有 deadline 的停止，但不阻塞 Unity callback。
        /// </summary>
        private void OnApplicationQuit()
        {
            _applicationQuitting = true;
            BeginObservedStop();
        }

        /// <summary>
        /// 在 GameObject 销毁时释放唯一性 claim，并确保停止任务已被观察。
        /// </summary>
        private void OnDestroy()
        {
            BeginObservedStop();
        }

        /// <summary>
        /// 清空关闭 Domain Reload 时可能残留的唯一性 guard。
        /// </summary>
        [RuntimeInitializeOnLoadMethod(RuntimeInitializeLoadType.SubsystemRegistration)]
        private static void ResetStaticState()
        {
            _claimedRoot = null;
        }

        /// <summary>
        /// 只创建一次捕获共享停止异常的观察任务，避免 Unity callback 使用 async void。
        /// </summary>
        private void BeginObservedStop()
        {
            if (_stopObserverTask == null)
            {
                _stopObserverTask = StopAndLogAsync();
            }
        }

        /// <summary>
        /// 等待对象图停止，并在任意终态释放当前实例持有的唯一性 claim。
        /// </summary>
        /// <returns>清理和 claim 释放均结束时完成的共享任务。</returns>
        private async Task StopAndReleaseClaimAsync()
        {
            try
            {
                if (_composition != null)
                {
                    await _composition.Lifetime.StopAsync();
                }
            }
            finally
            {
                ReleaseClaim();
            }
        }

        /// <summary>
        /// 等待共享停止任务，并把无法向 Unity callback 返回的异常记录到 Console。
        /// </summary>
        /// <returns>停止结果已经完成并被观察时结束的任务；异常在方法内汇聚，不向 callback 泄漏。</returns>
        private async Task StopAndLogAsync()
        {
            try
            {
                await StopAsync();
            }
            catch (AppShutdownException stopError) when (_applicationQuitting)
            {
                Debug.LogWarning(
                    FormatApplicationQuitShutdownWarning(stopError),
                    this);
            }
            catch (Exception stopError)
            {
                Debug.LogException(stopError, this);
            }
        }

        /// <summary>
        /// 构造不含credential、endpoint或业务identity的应用退出清理摘要。
        /// </summary>
        /// <param name="error">App Scope尽力停止聚合结果。</param>
        /// <returns>包含错误数量和首个稳定异常类型的warning文本。</returns>
        internal static string FormatApplicationQuitShutdownWarning(
            AppShutdownException error)
        {
            if (error == null)
            {
                throw new ArgumentNullException(nameof(error));
            }

            return
                "[IHOMELAND_APP_SHUTDOWN] outcome=best_effort " +
                $"cleanup_errors={error.Errors.Count} " +
                $"first_error={error.Errors[0].GetType().Name}";
        }

        /// <summary>
        /// 对称释放当前实例持有的静态唯一性 claim。
        /// </summary>
        private void ReleaseClaim()
        {
            if (_ownsClaim && ReferenceEquals(_claimedRoot, this))
            {
                _claimedRoot = null;
            }

            _ownsClaim = false;
        }
    }
}
