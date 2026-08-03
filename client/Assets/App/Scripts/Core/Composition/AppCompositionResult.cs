using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using IHomeland.Client.Application.Bootstrap;
using IHomeland.Client.Application.Battle;
using IHomeland.Client.Application.Control;
using IHomeland.Client.Application.Gameplay;
using IHomeland.Client.Application.Session;
using IHomeland.Client.Application.World;
using IHomeland.Client.Foundation.Lifetime;
using IHomeland.Client.Infrastructure.Battle;
using IHomeland.Client.Infrastructure.Tcp;
using IHomeland.Client.Infrastructure.WebSocket;
#if DEVELOPMENT_BUILD || UNITY_EDITOR
using IHomeland.Client.Core.Qualification;
#endif
using IHomeland.Client.Presentation.Navigation;
using IHomeland.Client.Presentation.PersonalWorld;
using IHomeland.Client.Scenes.PersonalWorld;

namespace IHomeland.Client.Core.Composition
{
    /// <summary>
    /// 保存 AppComposition 创建并转交给唯一 AppRoot 的不可变 App Scope 对象图。
    /// </summary>
    /// <remarks>
    /// 该结果不是 service locator：它只暴露 Composition 明确导出的强类型入口，不提供按类型查询。
    /// 后续 feature 应在 Composition 内显式注入窄依赖，不能经由 AppRoot 取得任意服务。
    /// </remarks>
    internal sealed class AppCompositionResult
    {
        /// <summary>
        /// 保存 Composition 完成时冻结的 tickable 快照。
        /// </summary>
        private readonly ReadOnlyCollection<IAppTickable> _tickables;

        /// <summary>
        /// 保存显式启动 version/config 的强类型用例；isolated host fixture 可以不提供。
        /// </summary>
        private readonly ClientBootstrapService _bootstrapService;

        /// <summary>
        /// 保存账号、token lineage 与 ticket 的唯一 owner；isolated host fixture 可以不提供。
        /// </summary>
        private readonly SessionCoordinator _sessionCoordinator;

        /// <summary>
        /// 保存只接收 WSS control PUSH 的唯一 owner；isolated host fixture 可以不提供。
        /// </summary>
        private readonly ClientControlChannel _controlChannel;

        /// <summary>
        /// 保存可靠 TLS/TCP gameplay 的唯一 owner；isolated host fixture 可以不提供。
        /// </summary>
        private readonly ClientGameplayChannel _gameplayChannel;

        /// <summary>
        /// 保存 PersonalWorld/assignment 的唯一不可变投影 owner；isolated host fixture 可以不提供。
        /// </summary>
        private readonly PersonalWorldService _personalWorldService;

        /// <summary>
        /// 保存 VisitSession/invite 的唯一不可变投影 owner；isolated host fixture 可以不提供。
        /// </summary>
        private readonly VisitSessionService _visitSessionService;

        /// <summary>
        /// 保存 current world target 转换的唯一 coordinator；isolated host fixture 可以不提供。
        /// </summary>
        private readonly WorldAdmissionCoordinator _worldAdmissionCoordinator;

        /// <summary>保存唯一connection recovery intent owner；isolated host fixture可以不提供。</summary>
        private readonly ClientConnectionRecoveryCoordinator _connectionRecoveryCoordinator;

        /// <summary>保存唯一 battle runtime facade；isolated host fixture 可以不提供。</summary>
        private readonly ClientBattleRuntimeCoordinator _battleRuntimeCoordinator;

        /// <summary>保存唯一battle network owner；isolated host fixture可以不提供。</summary>
        private readonly BattleNetworkClient _battleNetworkClient;

        /// <summary>保存唯一battle native lease owner；isolated host fixture可以不提供。</summary>
        private readonly ClientBattleNativeProvider _battleNativeProvider;

        /// <summary>
        /// 保存当前 App Scope 唯一 UI route owner；isolated host fixture 可以不提供。
        /// </summary>
        private readonly ClientUiRouter _uiRouter;

        /// <summary>保存个人世界产品表现协调器；isolated host fixture 可以不提供。</summary>
        private readonly ClientPersonalWorldExperience _personalWorldExperience;

        /// <summary>保存唯一内容 Scene 转换 Host；isolated host fixture 可以不提供。</summary>
        private readonly ClientWorldSceneTransitionHost _sceneTransitionHost;

        /// <summary>
        /// 创建只包含宿主运行边界的对象图结果，供不接入 HTTP capability 的 isolated fixture 使用。
        /// </summary>
        /// <param name="lifetime">统一拥有 App Scope 初始化和逆序停止的生命周期。</param>
        /// <param name="dispatcher">由 AppRoot Update 有界排空的主线程队列。</param>
        /// <param name="tickables">Composition 明确登记并冻结的逐帧对象。</param>
        /// <param name="maximumDispatchesPerFrame">单帧最多执行的主线程 callback 数量。</param>
        /// <exception cref="ArgumentException">单帧 callback 上限非正数时抛出。</exception>
        /// <exception cref="ArgumentNullException">任一对象、集合引用或 tickable 元素为 null 时抛出。</exception>
        internal AppCompositionResult(
            AppLifetime lifetime,
            MainThreadDispatcher dispatcher,
            IReadOnlyList<IAppTickable> tickables,
            int maximumDispatchesPerFrame)
            : this(
                lifetime,
                dispatcher,
                tickables,
                maximumDispatchesPerFrame,
                bootstrapService: null,
                sessionCoordinator: null,
                controlChannel: null,
                gameplayChannel: null,
                personalWorldService: null,
                visitSessionService: null,
                worldAdmissionCoordinator: null,
                connectionRecoveryCoordinator: null,
                battleRuntimeCoordinator: null,
                battleNetworkClient: null,
                battleNativeProvider: null,
                uiRouter: null,
                personalWorldExperience: null,
                sceneTransitionHost: null)
        {
        }

        /// <summary>
        /// 创建包含 HTTP bootstrap 与唯一 Session owner 的完整 App Scope 结果。
        /// </summary>
        /// <param name="lifetime">统一拥有 App Scope 初始化和逆序停止的生命周期。</param>
        /// <param name="dispatcher">由 AppRoot Update 有界排空的主线程队列。</param>
        /// <param name="tickables">Composition 明确登记并冻结的逐帧对象。</param>
        /// <param name="maximumDispatchesPerFrame">单帧最多执行的主线程 callback 数量。</param>
        /// <param name="bootstrapService">显式 version/config 启动用例。</param>
        /// <param name="sessionCoordinator">App Scope 唯一 Session owner。</param>
        /// <param name="controlChannel">App Scope 唯一 WSS control owner。</param>
        /// <param name="gameplayChannel">App Scope 唯一 TLS/TCP gameplay owner。</param>
        /// <param name="personalWorldService">PersonalWorld/assignment 投影 owner。</param>
        /// <param name="visitSessionService">VisitSession/invite 投影 owner。</param>
        /// <param name="worldAdmissionCoordinator">World target flow owner。</param>
        /// <param name="connectionRecoveryCoordinator">唯一connection recovery intent owner。</param>
        /// <param name="battleRuntimeCoordinator">唯一 client battle runtime owner。</param>
        /// <param name="battleNetworkClient">唯一client battle network owner。</param>
        /// <param name="battleNativeProvider">唯一client battle native lease owner。</param>
        /// <param name="uiRouter">App Scope 唯一 UI route owner。</param>
        /// <param name="personalWorldExperience">个人世界产品表现协调器。</param>
        /// <param name="sceneTransitionHost">唯一内容 Scene 转换 Host。</param>
        /// <exception cref="ArgumentException">单帧 callback 上限非正数时抛出。</exception>
        /// <exception cref="ArgumentNullException">任一必需对象、集合引用或 tickable 元素为 null 时抛出。</exception>
        internal AppCompositionResult(
            AppLifetime lifetime,
            MainThreadDispatcher dispatcher,
            IReadOnlyList<IAppTickable> tickables,
            int maximumDispatchesPerFrame,
            ClientBootstrapService bootstrapService,
            SessionCoordinator sessionCoordinator,
            ClientControlChannel controlChannel,
            ClientGameplayChannel gameplayChannel,
            PersonalWorldService personalWorldService,
            VisitSessionService visitSessionService,
            WorldAdmissionCoordinator worldAdmissionCoordinator,
            ClientConnectionRecoveryCoordinator connectionRecoveryCoordinator,
            ClientBattleRuntimeCoordinator battleRuntimeCoordinator,
            BattleNetworkClient battleNetworkClient,
            ClientBattleNativeProvider battleNativeProvider,
            ClientUiRouter uiRouter,
            ClientPersonalWorldExperience personalWorldExperience,
            ClientWorldSceneTransitionHost sceneTransitionHost)
        {
            Lifetime = lifetime ?? throw new ArgumentNullException(nameof(lifetime));
            Dispatcher = dispatcher ?? throw new ArgumentNullException(nameof(dispatcher));
            if (tickables == null)
            {
                throw new ArgumentNullException(nameof(tickables));
            }

            if (maximumDispatchesPerFrame <= 0)
            {
                throw new ArgumentException("单帧 callback 上限必须为正数。", nameof(maximumDispatchesPerFrame));
            }

            var copy = new IAppTickable[tickables.Count];
            for (var index = 0; index < tickables.Count; index++)
            {
                copy[index] = tickables[index] ??
                    throw new ArgumentNullException(nameof(tickables), $"tickable 索引 {index} 不能为空。");
            }

            _tickables = new ReadOnlyCollection<IAppTickable>(copy);
            MaximumDispatchesPerFrame = maximumDispatchesPerFrame;
            _bootstrapService = bootstrapService;
            _sessionCoordinator = sessionCoordinator;
            _controlChannel = controlChannel;
            _gameplayChannel = gameplayChannel;
            _personalWorldService = personalWorldService;
            _visitSessionService = visitSessionService;
            _worldAdmissionCoordinator = worldAdmissionCoordinator;
            _connectionRecoveryCoordinator = connectionRecoveryCoordinator;
            _battleRuntimeCoordinator = battleRuntimeCoordinator;
            _battleNetworkClient = battleNetworkClient;
            _battleNativeProvider = battleNativeProvider;
            _uiRouter = uiRouter;
            _personalWorldExperience = personalWorldExperience;
            _sceneTransitionHost = sceneTransitionHost;
        }

        /// <summary>
        /// 获取 App Scope 的唯一生命周期 owner。
        /// </summary>
        internal AppLifetime Lifetime { get; }

        /// <summary>
        /// 获取由 AppRoot 在 Unity 主线程排空的有界 Dispatcher。
        /// </summary>
        internal MainThreadDispatcher Dispatcher { get; }

        /// <summary>
        /// 获取 Composition 完成时冻结的逐帧对象快照。
        /// </summary>
        internal IReadOnlyList<IAppTickable> Tickables => _tickables;

        /// <summary>
        /// 获取单帧主线程 callback 执行硬上限。
        /// </summary>
        internal int MaximumDispatchesPerFrame { get; }

        /// <summary>
        /// 获取完整 Composition 显式连接的 HTTP bootstrap 用例。
        /// </summary>
        /// <exception cref="InvalidOperationException">Isolated host fixture 未连接 HTTP graph 时抛出。</exception>
        internal ClientBootstrapService BootstrapService => _bootstrapService ??
            throw new InvalidOperationException("当前 isolated host composition 不包含 HTTP bootstrap graph。");

        /// <summary>
        /// 获取完整 Composition 显式连接的唯一 Session owner。
        /// </summary>
        /// <exception cref="InvalidOperationException">Isolated host fixture 未连接 HTTP graph 时抛出。</exception>
        internal SessionCoordinator SessionCoordinator => _sessionCoordinator ??
            throw new InvalidOperationException("当前 isolated host composition 不包含 Session owner。");

        /// <summary>
        /// 获取完整 Composition 显式连接的唯一 WSS control owner。
        /// </summary>
        /// <exception cref="InvalidOperationException">Isolated host fixture 未连接 control graph 时抛出。</exception>
        internal ClientControlChannel ControlChannel => _controlChannel ??
            throw new InvalidOperationException("当前 isolated host composition 不包含 WSS control owner。");

        /// <summary>
        /// 获取完整 Composition 显式连接的唯一 TLS/TCP gameplay owner。
        /// </summary>
        /// <exception cref="InvalidOperationException">Isolated host fixture 未连接 gameplay graph 时抛出。</exception>
        internal ClientGameplayChannel GameplayChannel => _gameplayChannel ??
            throw new InvalidOperationException("当前 isolated host composition 不包含 TLS/TCP gameplay owner。");

        /// <summary>获取完整 Composition 显式连接的 PersonalWorld projection owner。</summary>
        /// <exception cref="InvalidOperationException">Isolated host fixture 未连接 world graph 时抛出。</exception>
        internal PersonalWorldService PersonalWorldService => _personalWorldService ??
            throw new InvalidOperationException("当前 isolated host composition 不包含 PersonalWorld Service。");

        /// <summary>获取完整 Composition 显式连接的 VisitSession projection owner。</summary>
        /// <exception cref="InvalidOperationException">Isolated host fixture 未连接 world graph 时抛出。</exception>
        internal VisitSessionService VisitSessionService => _visitSessionService ??
            throw new InvalidOperationException("当前 isolated host composition 不包含 VisitSession Service。");

        /// <summary>获取完整 Composition 显式连接的 world target flow owner。</summary>
        /// <exception cref="InvalidOperationException">Isolated host fixture 未连接 world graph 时抛出。</exception>
        internal WorldAdmissionCoordinator WorldAdmissionCoordinator => _worldAdmissionCoordinator ??
            throw new InvalidOperationException("当前 isolated host composition 不包含 WorldAdmissionCoordinator。");

        /// <summary>获取完整 Composition 显式连接的唯一 battle runtime owner。</summary>
        /// <exception cref="InvalidOperationException">Isolated host fixture 未连接 battle graph 时抛出。</exception>
        internal ClientBattleRuntimeCoordinator BattleRuntimeCoordinator =>
            _battleRuntimeCoordinator ??
            throw new InvalidOperationException(
                "当前 isolated host composition 不包含 battle runtime owner。");

        /// <summary>获取完整 Composition 显式连接的唯一 UI route owner。</summary>
        /// <exception cref="InvalidOperationException">Isolated host fixture 未连接 UI graph 时抛出。</exception>
        internal ClientUiRouter UiRouter => _uiRouter ??
            throw new InvalidOperationException("当前 isolated host composition 不包含 ClientUiRouter。");

        /// <summary>获取完整 Composition 显式连接的个人世界产品 Experience。</summary>
        /// <exception cref="InvalidOperationException">Isolated host fixture 未连接产品 graph 时抛出。</exception>
        internal ClientPersonalWorldExperience PersonalWorldExperience => _personalWorldExperience ??
            throw new InvalidOperationException("当前 isolated host composition 不包含个人世界产品 Experience。");

        /// <summary>获取完整 Composition 显式连接的唯一内容 Scene 转换 Host。</summary>
        /// <exception cref="InvalidOperationException">Isolated host fixture 未连接产品 graph 时抛出。</exception>
        internal ClientWorldSceneTransitionHost SceneTransitionHost => _sceneTransitionHost ??
            throw new InvalidOperationException("当前 isolated host composition 不包含内容 Scene Host。");

#if DEVELOPMENT_BUILD || UNITY_EDITOR
        /// <summary>创建只读取现有owner的Development资格诊断聚合器。</summary>
        /// <returns>不登记生命周期或subscriber的低敏只读诊断。</returns>
        /// <exception cref="InvalidOperationException">Isolated host fixture未连接完整产品graph时抛出。</exception>
        internal ClientQualificationDiagnostics CreateQualificationDiagnostics()
        {
            return new ClientQualificationDiagnostics(
                ControlChannel,
                GameplayChannel,
                _connectionRecoveryCoordinator == null
                    ? throw new InvalidOperationException("当前 isolated host composition 不包含恢复产品 graph。")
                    : _connectionRecoveryCoordinator,
                Dispatcher,
                UiRouter,
                SceneTransitionHost,
                BattleRuntimeCoordinator,
                _battleNetworkClient ??
                    throw new InvalidOperationException(
                        "当前 isolated host composition 不包含 battle network owner。"),
                _battleNativeProvider ??
                    throw new InvalidOperationException(
                        "当前 isolated host composition 不包含 battle native owner。"));
        }
#endif
    }
}
