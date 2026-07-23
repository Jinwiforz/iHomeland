using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Application.World;
using IHomeland.Client.Presentation.Navigation;

namespace IHomeland.Client.Presentation.PersonalWorld
{
    /// <summary>供 Scene transaction 读取 owner generation 并提交 View State 的窄边界。</summary>
    internal interface IPersonalWorldSceneTransactionHost
    {
        /// <summary>判断 presentation generation 仍 current。</summary>
        bool IsPresentationCurrent(long generation);

        /// <summary>获取 current world target snapshot。</summary>
        ClientWorldFlowSnapshot CurrentWorldFlow { get; }

        /// <summary>获取 current Session 是否存在。</summary>
        bool HasCurrentSession { get; }

        /// <summary>重建并返回 current View State。</summary>
        ClientPersonalWorldViewState RebuildPresentation();

        /// <summary>发布 current View State。</summary>
        void PublishPresentation();
    }

    /// <summary>
    /// 唯一串行执行旧 Scene invalidation、unload/load 与 Shell/HUD route commit。
    /// </summary>
    internal sealed class PersonalWorldSceneTransaction
    {
        /// <summary>串行化完整 Scene/route transaction。</summary>
        private readonly SemaphoreSlim _gate = new SemaphoreSlim(1, 1);

        /// <summary>唯一 route owner。</summary>
        private readonly ClientUiRouter _router;

        /// <summary>唯一 Scene transition port。</summary>
        private readonly IClientWorldSceneTransition _scene;

        /// <summary>创建 Scene transaction。</summary>
        internal PersonalWorldSceneTransaction(
            ClientUiRouter router,
            IClientWorldSceneTransition scene)
        {
            _router = router ?? throw new ArgumentNullException(nameof(router));
            _scene = scene ?? throw new ArgumentNullException(nameof(scene));
        }

        /// <summary>把 current target 收敛到对应 Scene、Shell 与 HUD。</summary>
        internal async Task<bool> SynchronizeAsync(
            IPersonalWorldSceneTransactionHost host,
            long presentationGeneration,
            CancellationToken cancellationToken)
        {
            if (host == null)
            {
                throw new ArgumentNullException(nameof(host));
            }

            var entered = false;
            try
            {
                await _gate.WaitAsync(cancellationToken);
                entered = true;
                if (!host.IsPresentationCurrent(presentationGeneration))
                {
                    return false;
                }

                var flow = host.CurrentWorldFlow;
                var needsScene =
                    flow.State == ClientWorldFlowState.OwnWorld ||
                    flow.State == ClientWorldFlowState.Visiting;
                ClientUiDiagnostics.Trace(
                    nameof(PersonalWorldSceneTransaction),
                    "scene_synchronization_evaluated",
                    $"flow={flow.State} needs_scene={needsScene} target_generation={flow.TargetGeneration} " +
                    $"scene={_scene.Snapshot.SceneId} scene_generation={_scene.Snapshot.SceneGeneration}");
                if (!needsScene)
                {
                    await CloseRouteAsync(
                        ClientUiRouteId.WorldVisit,
                        cancellationToken);
                    var oldSceneGeneration = _scene.Snapshot.SceneGeneration;
                    if (oldSceneGeneration > 0)
                    {
                        await _router.InvalidateSceneAsync(
                            oldSceneGeneration,
                            cancellationToken);
                    }

                    await _scene.UnloadAsync(cancellationToken);
                    if (host.HasCurrentSession &&
                        host.IsPresentationCurrent(presentationGeneration))
                    {
                        await _router.OpenAsync(
                            ClientUiRouteId.Shell,
                            0,
                            cancellationToken);
                    }

                    host.RebuildPresentation();
                    host.PublishPresentation();
                    return true;
                }

                var currentScene = _scene.Snapshot;
                if (currentScene.SceneId == ClientWorldSceneId.PersonalWorld &&
                    currentScene.TargetGeneration == flow.TargetGeneration &&
                    currentScene.SceneGeneration > 0)
                {
                    var currentHud = await _router.OpenAsync(
                        ClientUiRouteId.WorldHud,
                        currentScene.SceneGeneration,
                        cancellationToken);
                    return currentHud.IsSuccess;
                }

                await CloseRouteAsync(
                    ClientUiRouteId.WorldVisit,
                    cancellationToken);
                if (currentScene.SceneGeneration > 0)
                {
                    await _router.InvalidateSceneAsync(
                        currentScene.SceneGeneration,
                        cancellationToken);
                }

                await _scene.UnloadAsync(cancellationToken);
                if (!host.IsPresentationCurrent(presentationGeneration) ||
                    flow.TargetGeneration != host.CurrentWorldFlow.TargetGeneration)
                {
                    return false;
                }

                var state = host.RebuildPresentation();
                var loaded = await _scene.LoadAsync(
                    ClientWorldSceneId.PersonalWorld,
                    flow.TargetGeneration,
                    state,
                    cancellationToken);
                if (loaded != ClientWorldSceneTransitionCode.Succeeded ||
                    !host.IsPresentationCurrent(presentationGeneration))
                {
                    return false;
                }

                var sceneGeneration = _scene.Snapshot.SceneGeneration;
                host.RebuildPresentation();
                var hud = await _router.OpenAsync(
                    ClientUiRouteId.WorldHud,
                    sceneGeneration,
                    cancellationToken);
                if (!hud.IsSuccess)
                {
                    await _scene.UnloadAsync(cancellationToken);
                    return false;
                }

                await CloseRouteAsync(ClientUiRouteId.Shell, cancellationToken);
                host.PublishPresentation();
                return true;
            }
            finally
            {
                if (entered)
                {
                    _gate.Release();
                }
            }
        }

        /// <summary>以高于 recovery 的 authority 清理 Scene/routes 并只提交 Login。</summary>
        internal async Task ConvergeToLoginAsync(
            IPersonalWorldSceneTransactionHost host,
            long presentationGeneration,
            CancellationToken cancellationToken)
        {
            if (host == null)
            {
                throw new ArgumentNullException(nameof(host));
            }

            await _gate.WaitAsync(cancellationToken);
            try
            {
                await CloseRouteAsync(
                    ClientUiRouteId.ConnectionLost,
                    cancellationToken);
                await CloseRouteAsync(
                    ClientUiRouteId.WorldVisit,
                    cancellationToken);
                await CloseRouteAsync(
                    ClientUiRouteId.WorldHud,
                    cancellationToken);
                var sceneGeneration = _scene.Snapshot.SceneGeneration;
                if (sceneGeneration > 0)
                {
                    await _router.InvalidateSceneAsync(
                        sceneGeneration,
                        cancellationToken);
                }

                await _scene.UnloadAsync(cancellationToken);
                await CloseRouteAsync(ClientUiRouteId.Shell, cancellationToken);
                if (host.IsPresentationCurrent(presentationGeneration) &&
                    !host.HasCurrentSession)
                {
                    await _router.OpenAsync(
                        ClientUiRouteId.Login,
                        0,
                        cancellationToken);
                }
            }
            finally
            {
                _gate.Release();
            }
        }

        /// <summary>幂等关闭 route。</summary>
        private async Task CloseRouteAsync(
            ClientUiRouteId routeId,
            CancellationToken cancellationToken)
        {
            var result = await _router.CloseAsync(routeId, cancellationToken);
            if (!result.IsSuccess &&
                result.Code == ClientUiTransitionCode.NotRegistered)
            {
                return;
            }
        }
    }
}
