using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;

namespace IHomeland.Client.AppShell.Presentation.Navigation
{
    /// <summary>保存一次 transition queue admission。</summary>
    internal readonly struct ClientUiTransitionAdmission
    {
        /// <summary>创建 admission。</summary>
        private ClientUiTransitionAdmission(
            bool entered,
            ClientUiTransitionResult result)
        {
            Entered = entered;
            Result = result;
        }

        /// <summary>获取是否取得唯一 transition gate。</summary>
        internal bool Entered { get; }

        /// <summary>获取未取得 gate 时的稳定结果。</summary>
        internal ClientUiTransitionResult Result { get; }

        /// <summary>获取成功 admission。</summary>
        internal static ClientUiTransitionAdmission Accepted { get; } =
            new ClientUiTransitionAdmission(true, null);

        /// <summary>创建副作用前拒绝。</summary>
        internal static ClientUiTransitionAdmission Rejected(
            ClientUiTransitionCode code)
        {
            return new ClientUiTransitionAdmission(
                false,
                ClientUiTransitionResult.Rejected(code));
        }
    }

    /// <summary>拥有有界等待计数与唯一串行 transition gate。</summary>
    internal sealed class ClientUiTransitionQueue : IDisposable
    {
        /// <summary>唯一串行 gate。</summary>
        private readonly SemaphoreSlim _gate = new SemaphoreSlim(1, 1);

        /// <summary>等待调用硬上限。</summary>
        private readonly int _capacity;

        /// <summary>current 等待数。</summary>
        private int _queued;

        /// <summary>创建有界 transition queue。</summary>
        internal ClientUiTransitionQueue(int capacity)
        {
            if (capacity <= 0)
            {
                throw new ArgumentOutOfRangeException(nameof(capacity));
            }

            _capacity = capacity;
        }

        /// <summary>有界登记并等待 gate。</summary>
        internal async Task<ClientUiTransitionAdmission> EnterAsync(
            CancellationToken callerToken,
            CancellationToken lifetimeToken)
        {
            var queued = Interlocked.Increment(ref _queued);
            if (queued > _capacity)
            {
                Interlocked.Decrement(ref _queued);
                return ClientUiTransitionAdmission.Rejected(
                    ClientUiTransitionCode.Overloaded);
            }

            try
            {
                using (var linked = CancellationTokenSource.CreateLinkedTokenSource(
                           callerToken,
                           lifetimeToken))
                {
                    await _gate.WaitAsync(linked.Token);
                    return ClientUiTransitionAdmission.Accepted;
                }
            }
            catch (OperationCanceledException)
                when (callerToken.IsCancellationRequested ||
                      lifetimeToken.IsCancellationRequested)
            {
                return ClientUiTransitionAdmission.Rejected(
                    lifetimeToken.IsCancellationRequested
                        ? ClientUiTransitionCode.Stopped
                        : ClientUiTransitionCode.Cancelled);
            }
            finally
            {
                Interlocked.Decrement(ref _queued);
            }
        }

        /// <summary>释放 current transition owner。</summary>
        internal void Exit()
        {
            _gate.Release();
        }

        /// <summary>停止流程绕过普通容量，等待当前 transition 退出。</summary>
        internal Task EnterForStopAsync(CancellationToken cancellationToken)
        {
            return _gate.WaitAsync(cancellationToken);
        }

        /// <summary>释放 gate 资源。</summary>
        public void Dispose()
        {
            _gate.Dispose();
        }
    }

    /// <summary>保存已提交 active route 的 Host、binding 与页面 lease。</summary>
    internal sealed class ClientUiActiveRoute
    {
        /// <summary>创建 active route owner。</summary>
        internal ClientUiActiveRoute(
            ClientUiRouteDefinition definition,
            IClientUiViewHost host,
            ClientUiRouteBinding binding,
            CancellationTokenSource pageCancellation)
        {
            Definition = definition;
            Host = host;
            Binding = binding;
            PageCancellation = pageCancellation;
        }

        /// <summary>获取冻结 definition。</summary>
        internal ClientUiRouteDefinition Definition { get; }

        /// <summary>获取唯一 Host。</summary>
        internal IClientUiViewHost Host { get; }

        /// <summary>获取 current binding。</summary>
        internal ClientUiRouteBinding Binding { get; }

        /// <summary>获取页面 cancellation owner。</summary>
        internal CancellationTokenSource PageCancellation { get; }

        /// <summary>获取或设置唯一交互资格。</summary>
        internal bool Interactive { get; set; }

        /// <summary>获取或设置暂停前 focus token。</summary>
        internal ClientUiFocusToken SuspendedFocus { get; set; }
    }

    /// <summary>保存关闭后保留初始化资源的 Cached Host。</summary>
    internal sealed class ClientUiCachedRoute
    {
        /// <summary>创建 cached route。</summary>
        internal ClientUiCachedRoute(
            ClientUiRouteDefinition definition,
            IClientUiViewHost host)
        {
            Definition = definition;
            Host = host;
        }

        /// <summary>获取冻结 definition。</summary>
        internal ClientUiRouteDefinition Definition { get; }

        /// <summary>获取保持初始化的 Host。</summary>
        internal IClientUiViewHost Host { get; }
    }

    /// <summary>唯一拥有 active/cached route 集合、snapshot 与 navigation generation。</summary>
    internal sealed class ClientUiRouteState
    {
        /// <summary>获取按提交顺序排列的 active routes。</summary>
        internal List<ClientUiActiveRoute> ActiveRoutes { get; } =
            new List<ClientUiActiveRoute>();

        /// <summary>获取 cached route owners。</summary>
        internal Dictionary<ClientUiRouteId, ClientUiCachedRoute> CachedRoutes { get; } =
            new Dictionary<ClientUiRouteId, ClientUiCachedRoute>();

        /// <summary>获取或设置最近原子提交 snapshot。</summary>
        internal ClientUiRouteSnapshot Snapshot { get; set; } =
            ClientUiRouteSnapshot.Empty;

        /// <summary>获取最后分配 generation。</summary>
        internal long NavigationGeneration { get; private set; }

        /// <summary>分配下一单调 generation；失败 transition 也不复用。</summary>
        internal long NextGeneration()
        {
            if (NavigationGeneration == long.MaxValue)
            {
                throw new InvalidOperationException("UI navigation generation 已耗尽。");
            }

            NavigationGeneration++;
            return NavigationGeneration;
        }
    }

    /// <summary>纯计算 layer replacement 与 SceneBound policy。</summary>
    internal sealed class ClientUiTransitionPlanner
    {
        /// <summary>查找会被 Screen/System route 替换的唯一 owner。</summary>
        internal ClientUiActiveRoute FindConflictingOwner(
            IReadOnlyList<ClientUiActiveRoute> routes,
            ClientUiRouteDefinition definition)
        {
            if (definition.Layer != ClientUiLayer.Screen &&
                definition.Layer != ClientUiLayer.System)
            {
                return null;
            }

            return routes.LastOrDefault(
                route => route.Definition.Layer == definition.Layer);
        }

        /// <summary>验证 SceneBound generation 组合。</summary>
        internal bool IsSceneBindingValid(
            ClientUiRouteDefinition definition,
            long sceneGeneration)
        {
            return definition.Lifecycle == ClientUiLifecycle.SceneBound
                ? sceneGeneration > 0
                : sceneGeneration == 0;
        }
    }

    /// <summary>纯计算最高交互 route、input state 与 layer top owner。</summary>
    internal sealed class ClientUiInteractionResolver
    {
        /// <summary>选择最高 layer 中最后提交的交互 owner。</summary>
        internal ClientUiActiveRoute FindInteractive(
            IReadOnlyList<ClientUiActiveRoute> routes)
        {
            ClientUiActiveRoute result = null;
            for (var index = 0; index < routes.Count; index++)
            {
                var candidate = routes[index];
                if (result == null ||
                    (int)candidate.Definition.Layer >=
                    (int)result.Definition.Layer)
                {
                    result = candidate;
                }
            }

            return result;
        }

        /// <summary>派生无 Unity input plan。</summary>
        internal ClientUiInputState CreateInputState(
            ClientUiRouteDefinition definition)
        {
            return definition.InputMode == ClientUiInputMode.Gameplay
                ? ClientUiInputState.Gameplay
                : new ClientUiInputState(
                    definition.InputMode,
                    definition.RouteId);
        }

        /// <summary>查找指定 layer 当前 top route。</summary>
        internal ClientUiActiveRoute FindTop(
            IReadOnlyList<ClientUiActiveRoute> routes,
            ClientUiLayer layer)
        {
            for (var index = routes.Count - 1; index >= 0; index--)
            {
                if (routes[index].Definition.Layer == layer)
                {
                    return routes[index];
                }
            }

            return null;
        }

        /// <summary>原地标记唯一交互 owner。</summary>
        internal void ApplyOwnership(IReadOnlyList<ClientUiActiveRoute> routes)
        {
            var interactive = FindInteractive(routes);
            for (var index = 0; index < routes.Count; index++)
            {
                routes[index].Interactive =
                    ReferenceEquals(routes[index], interactive);
            }
        }
    }

    /// <summary>保存 Host transaction 在提交前已完成的可回滚副作用。</summary>
    internal sealed class ClientUiTransitionProgress
    {
        /// <summary>获取或设置是否由本次 transaction 初始化 Host。</summary>
        internal bool InitializedByTransaction { get; set; }

        /// <summary>获取或设置 binding 是否完成。</summary>
        internal bool Bound { get; set; }

        /// <summary>获取或设置 show 是否完成。</summary>
        internal bool Shown { get; set; }

        /// <summary>获取或设置旧 owner 是否已暂停交互。</summary>
        internal bool OldInteractionSuspended { get; set; }

        /// <summary>获取或设置默认 focus 是否不可用。</summary>
        internal bool FocusUnavailable { get; set; }

        /// <summary>获取或设置提交前捕获的旧 focus。</summary>
        internal ClientUiFocusToken PreviousFocus { get; set; }
    }

    /// <summary>
    /// 执行 candidate initialize/bind/show、旧 owner 暂停与 input/focus 副作用。
    /// </summary>
    internal sealed class ClientUiTransitionTransaction
    {
        /// <summary>唯一 input/cursor/gameplay gate owner。</summary>
        private readonly IClientUiInputCoordinator _input;

        /// <summary>纯 interaction plan resolver。</summary>
        private readonly ClientUiInteractionResolver _resolver;

        /// <summary>创建 Host transition transaction。</summary>
        internal ClientUiTransitionTransaction(
            IClientUiInputCoordinator input,
            ClientUiInteractionResolver resolver)
        {
            _input = input ?? throw new ArgumentNullException(nameof(input));
            _resolver = resolver ?? throw new ArgumentNullException(nameof(resolver));
        }

        /// <summary>执行 candidate commit 前副作用，并持续记录可回滚进度。</summary>
        internal async Task PrepareOpenAsync(
            ClientUiActiveRoute candidate,
            bool cached,
            ClientUiActiveRoute previousInteractive,
            ClientUiTransitionProgress progress,
            CancellationToken cancellationToken)
        {
            if (candidate == null || progress == null)
            {
                throw new ArgumentNullException(
                    candidate == null ? nameof(candidate) : nameof(progress));
            }

            if (!cached)
            {
                await candidate.Host.InitializeAsync(cancellationToken);
                progress.InitializedByTransaction = true;
            }

            await candidate.Host.BindAsync(candidate.Binding, cancellationToken);
            progress.Bound = true;
            if (candidate.Interactive && previousInteractive != null)
            {
                progress.PreviousFocus = previousInteractive.Host.CaptureFocus();
                previousInteractive.SuspendedFocus = progress.PreviousFocus;
                await previousInteractive.Host.SetInteractiveAsync(
                    false,
                    cancellationToken);
                progress.OldInteractionSuspended = true;
            }

            if (candidate.Interactive)
            {
                await _input.ApplyAsync(
                    _resolver.CreateInputState(candidate.Definition),
                    cancellationToken);
            }

            await candidate.Host.ShowAsync(
                candidate.Definition.Layer,
                cancellationToken);
            progress.Shown = true;
            await candidate.Host.SetInteractiveAsync(
                candidate.Interactive,
                cancellationToken);
            if (candidate.Interactive &&
                candidate.Definition.InputMode != ClientUiInputMode.Gameplay)
            {
                progress.FocusUnavailable =
                    !await candidate.Host.FocusDefaultAsync(cancellationToken);
            }
        }
    }
}
