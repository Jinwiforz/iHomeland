using System;
using IHomeland.Client.Application.Bootstrap;
using IHomeland.Client.Application.Control;
using IHomeland.Client.Application.Gameplay;
using IHomeland.Client.Application.Session;
using IHomeland.Client.Application.World;
using IHomeland.Client.Core.Configuration;
using IHomeland.Client.Core.Lifetime;
using IHomeland.Client.Infrastructure.Http;
using IHomeland.Client.Infrastructure.Tcp;
using IHomeland.Client.Infrastructure.WebSocket;
using IHomeland.Client.Scenes.Contexts;

namespace IHomeland.Client.Core.Composition
{
    /// <summary>
    /// 在唯一入口显式创建并连接当前 App Scope 对象图。
    /// </summary>
    /// <remarks>
    /// 该 Composition Root 是唯一了解 concrete types 的位置。它不缓存对象图、不提供按类型查询，
    /// 每个实例最多构造一次结果，重复 bootstrap 由 AppRoot 唯一性争用在调用前拒绝。
    /// </remarks>
    internal sealed class AppComposition
    {
        /// <summary>
        /// 限制跨线程 callback 积压，避免未来生产者无界占用 App Scope 内存。
        /// </summary>
        private const int MainThreadQueueCapacity = 1024;

        /// <summary>
        /// 限制单帧 callback 数量，避免一次积压独占 Unity 主线程。
        /// </summary>
        private const int MaximumDispatchesPerFrame = 128;

        /// <summary>
        /// 限制初始化失败后的逆序回滚总等待时间。
        /// </summary>
        private static readonly TimeSpan RollbackTimeout = TimeSpan.FromSeconds(5);

        /// <summary>
        /// 限制正常退出时的逆序停止总等待时间。
        /// </summary>
        private static readonly TimeSpan ShutdownTimeout = TimeSpan.FromSeconds(5);

        /// <summary>
        /// 限制一次显式 control run 在瞬时故障后的额外尝试次数与等待时间。
        /// </summary>
        private static readonly TimeSpan[] ControlRetryDelays =
        {
            TimeSpan.FromMilliseconds(250),
            TimeSpan.FromSeconds(1),
            TimeSpan.FromSeconds(3),
        };

        /// <summary>
        /// 表示当前 Composition 已构造对象图，阻止同一实例重复 Build。
        /// </summary>
        private bool _built;

        /// <summary>
        /// 在当前 Unity 主线程创建 App Scope 对象图和冻结的执行顺序。
        /// </summary>
        /// <param name="environment">已在任何网络副作用前验证的环境与构建身份。</param>
        /// <returns>只供唯一 AppRoot 持有和驱动的不可变 composition 结果。</returns>
        /// <exception cref="ArgumentNullException">环境快照为空时抛出。</exception>
        /// <exception cref="InvalidOperationException">同一 AppComposition 实例重复 Build 时抛出。</exception>
        internal AppCompositionResult Build(ClientEnvironment environment)
        {
            if (environment == null)
            {
                throw new ArgumentNullException(nameof(environment));
            }

            if (_built)
            {
                throw new InvalidOperationException("同一 AppComposition 不能重复构造 App Scope。");
            }

            _built = true;
            var dispatcher = new MainThreadDispatcher(
                Environment.CurrentManagedThreadId,
                MainThreadQueueCapacity);
            var configurationStore = new ClientConfigurationStore();
            var transport = new ClientHttpTransport(environment);
            var codec = new ClientHttpCodec();
            var httpApi = new ClientHttpApi(transport, codec);
            var bootstrapService = new ClientBootstrapService(environment, httpApi, configurationStore);
            var clock = new SystemClientClock();
            var sessionCoordinator = new SessionCoordinator(
                configurationStore,
                httpApi,
                clock);
            var controlCatalog = new ClientControlCatalog();
            var controlCodec = new ClientControlCodec(controlCatalog);
            var gameplayChannel = new ClientGameplayChannel(
                configurationStore,
                sessionCoordinator,
                new SystemClientGameplayConnectionFactory(environment),
                new ClientGameplayCodec(),
                dispatcher);
            var controlChannel = new ClientControlChannel(
                environment,
                configurationStore,
                sessionCoordinator,
                new SystemClientWebSocketFactory(),
                controlCodec,
                dispatcher,
                new SystemClientControlDelay(),
                ControlRetryDelays,
                gameplayChannel.InvalidateSession);
            var personalWorldService = new PersonalWorldService(controlChannel, gameplayChannel);
            var visitSessionService = new VisitSessionService(controlChannel, gameplayChannel, clock);
            var worldAdmissionCoordinator = new WorldAdmissionCoordinator(
                sessionCoordinator,
                gameplayChannel,
                personalWorldService,
                visitSessionService);
            var sceneLifetimeOwner = new SceneLifetimeOwner();

            // 逆序停止依次撤销 Scene、world flow/subscriber、WSS、TCP、Session、HTTP、Configuration，最后拒绝主线程回写。
            IAppLifetimeParticipant[] participants =
            {
                dispatcher,
                configurationStore,
                transport,
                sessionCoordinator,
                gameplayChannel,
                controlChannel,
                personalWorldService,
                visitSessionService,
                worldAdmissionCoordinator,
                sceneLifetimeOwner,
            };

            var lifetime = new AppLifetime(participants, RollbackTimeout, ShutdownTimeout);
            return new AppCompositionResult(
                lifetime,
                dispatcher,
                Array.Empty<IAppTickable>(),
                MaximumDispatchesPerFrame,
                bootstrapService,
                sessionCoordinator,
                controlChannel,
                gameplayChannel,
                personalWorldService,
                visitSessionService,
                worldAdmissionCoordinator);
        }
    }
}
