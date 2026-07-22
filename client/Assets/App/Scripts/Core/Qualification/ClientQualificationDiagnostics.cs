#if DEVELOPMENT_BUILD || UNITY_EDITOR
using System;
using IHomeland.Client.Application.Control;
using IHomeland.Client.Application.Gameplay;
using IHomeland.Client.Application.World;
using IHomeland.Client.Core.Bootstrap;
using IHomeland.Client.Core.Lifetime;
using IHomeland.Client.Presentation.Navigation;
using IHomeland.Client.Scenes.PersonalWorld;

namespace IHomeland.Client.Core.Qualification
{
    /// <summary>保存Development资格运行可比较的固定低敏资源计数。</summary>
    internal sealed class ClientQualificationDiagnosticSnapshot
    {
        /// <summary>创建一个不含credential、endpoint或业务identity的计数快照。</summary>
        /// <param name="appRootOwners">进程内AppRoot owner数量。</param>
        /// <param name="controlGeneration">Current control health generation。</param>
        /// <param name="controlRunOwners">存活control run owner数量。</param>
        /// <param name="gameplayGeneration">Current gameplay generation。</param>
        /// <param name="gameplaySocketOwners">Current gameplay socket owner数量。</param>
        /// <param name="gameplayHeartbeatOwners">Current heartbeat owner数量。</param>
        /// <param name="recoveryIntentOwners">Current recovery intent owner数量。</param>
        /// <param name="gameplayPendingOperations">Gameplay correlation pending数量。</param>
        /// <param name="dispatcherPendingCallbacks">Dispatcher待执行callback数量。</param>
        /// <param name="subscriptions">资格范围内subscriber总数。</param>
        /// <param name="sceneOwners">已提交内容Scene owner数量。</param>
        /// <exception cref="ArgumentOutOfRangeException">任一计数或generation为负数时抛出。</exception>
        internal ClientQualificationDiagnosticSnapshot(
            int appRootOwners,
            long controlGeneration,
            int controlRunOwners,
            long gameplayGeneration,
            int gameplaySocketOwners,
            int gameplayHeartbeatOwners,
            int recoveryIntentOwners,
            int gameplayPendingOperations,
            int dispatcherPendingCallbacks,
            int subscriptions,
            int sceneOwners)
        {
            if (appRootOwners < 0 || controlGeneration < 0 || controlRunOwners < 0 ||
                gameplayGeneration < 0 || gameplaySocketOwners < 0 ||
                gameplayHeartbeatOwners < 0 || recoveryIntentOwners < 0 ||
                gameplayPendingOperations < 0 || dispatcherPendingCallbacks < 0 ||
                subscriptions < 0 || sceneOwners < 0)
            {
                throw new ArgumentOutOfRangeException(nameof(appRootOwners));
            }

            AppRootOwners = appRootOwners;
            ControlGeneration = controlGeneration;
            ControlRunOwners = controlRunOwners;
            GameplayGeneration = gameplayGeneration;
            GameplaySocketOwners = gameplaySocketOwners;
            GameplayHeartbeatOwners = gameplayHeartbeatOwners;
            RecoveryIntentOwners = recoveryIntentOwners;
            GameplayPendingOperations = gameplayPendingOperations;
            DispatcherPendingCallbacks = dispatcherPendingCallbacks;
            Subscriptions = subscriptions;
            SceneOwners = sceneOwners;
        }

        /// <summary>获取进程内AppRoot唯一性owner数量。</summary>
        internal int AppRootOwners { get; }

        /// <summary>获取current control health generation。</summary>
        internal long ControlGeneration { get; }

        /// <summary>获取仍存活的control run owner数量。</summary>
        internal int ControlRunOwners { get; }

        /// <summary>获取current gameplay connection generation。</summary>
        internal long GameplayGeneration { get; }

        /// <summary>获取current gameplay socket owner数量。</summary>
        internal int GameplaySocketOwners { get; }

        /// <summary>获取current gameplay heartbeat owner数量。</summary>
        internal int GameplayHeartbeatOwners { get; }

        /// <summary>获取current recovery intent owner数量。</summary>
        internal int RecoveryIntentOwners { get; }

        /// <summary>获取gameplay correlation pending数量。</summary>
        internal int GameplayPendingOperations { get; }

        /// <summary>获取主线程dispatcher待执行callback数量。</summary>
        internal int DispatcherPendingCallbacks { get; }

        /// <summary>获取资格范围内现有subscriber总数。</summary>
        internal int Subscriptions { get; }

        /// <summary>获取已提交内容Scene owner数量。</summary>
        internal int SceneOwners { get; }
    }

    /// <summary>从现有App Scope owner原子读取资格计数，不保存或修正任何业务事实。</summary>
    internal sealed class ClientQualificationDiagnostics
    {
        /// <summary>App Scope唯一control owner。</summary>
        private readonly ClientControlChannel _control;

        /// <summary>App Scope唯一gameplay owner。</summary>
        private readonly ClientGameplayChannel _gameplay;

        /// <summary>App Scope唯一恢复intent owner。</summary>
        private readonly ClientConnectionRecoveryCoordinator _recovery;

        /// <summary>App Scope唯一主线程dispatcher。</summary>
        private readonly MainThreadDispatcher _dispatcher;

        /// <summary>App Scope唯一UI route owner。</summary>
        private readonly ClientUiRouter _router;

        /// <summary>App Scope唯一内容Scene owner。</summary>
        private readonly ClientWorldSceneTransitionHost _scene;

        /// <summary>绑定只读计数来源；不登记subscriber或生命周期参与者。</summary>
        /// <param name="control">唯一control owner。</param>
        /// <param name="gameplay">唯一gameplay owner。</param>
        /// <param name="recovery">唯一恢复intent owner。</param>
        /// <param name="dispatcher">唯一主线程dispatcher。</param>
        /// <param name="router">唯一UI route owner。</param>
        /// <param name="scene">唯一内容Scene owner。</param>
        /// <exception cref="ArgumentNullException">任一owner为空时抛出。</exception>
        internal ClientQualificationDiagnostics(
            ClientControlChannel control,
            ClientGameplayChannel gameplay,
            ClientConnectionRecoveryCoordinator recovery,
            MainThreadDispatcher dispatcher,
            ClientUiRouter router,
            ClientWorldSceneTransitionHost scene)
        {
            _control = control ?? throw new ArgumentNullException(nameof(control));
            _gameplay = gameplay ?? throw new ArgumentNullException(nameof(gameplay));
            _recovery = recovery ?? throw new ArgumentNullException(nameof(recovery));
            _dispatcher = dispatcher ?? throw new ArgumentNullException(nameof(dispatcher));
            _router = router ?? throw new ArgumentNullException(nameof(router));
            _scene = scene ?? throw new ArgumentNullException(nameof(scene));
        }

        /// <summary>在各owner自身同步边界内读取一次可比较快照。</summary>
        /// <returns>固定低敏资源计数。</returns>
        internal ClientQualificationDiagnosticSnapshot Capture()
        {
            var control = _control.Snapshot;
            var gameplay = _gameplay.Snapshot;
            return new ClientQualificationDiagnosticSnapshot(
                AppRoot.QualificationClaimedOwnerCount,
                control.Generation,
                _control.QualificationRunOwnerCount,
                gameplay.Generation,
                _gameplay.QualificationSocketOwnerCount,
                _gameplay.QualificationHeartbeatOwnerCount,
                _recovery.QualificationIntentOwnerCount,
                _gameplay.QualificationPendingOperationCount,
                _dispatcher.PendingCount,
                _control.QualificationSubscriptionCount +
                _gameplay.QualificationSubscriptionCount +
                _recovery.QualificationSubscriptionCount +
                _router.QualificationSubscriptionCount,
                _scene.QualificationSceneOwnerCount);
        }
    }
}
#endif
