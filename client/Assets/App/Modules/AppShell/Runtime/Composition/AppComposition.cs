using System;
using System.Collections.Generic;
using IHomeland.Client.AppShell.Application.Bootstrap;
using IHomeland.Client.Networking.Application.Control;
using IHomeland.Client.Networking.Application.Gameplay;
using IHomeland.Client.Session.Application;
using IHomeland.Client.PersonalWorld.Application;
using IHomeland.Client.AppShell.Application.Configuration;
using IHomeland.Client.Core.Foundation.Lifetime;
using IHomeland.Client.Core.Foundation.Time;
using IHomeland.Client.Core.Runtime.Composition;
using IHomeland.Client.Networking.Application.Contracts;
using IHomeland.Client.Networking.Infrastructure.Http;
using IHomeland.Client.Session.Infrastructure.Security;
using IHomeland.Client.Networking.Infrastructure.Tcp;
using IHomeland.Client.Core.Infrastructure.Time;
using IHomeland.Client.Networking.Infrastructure.WebSocket;
using IHomeland.Client.Networking.Runtime.Composition;
using IHomeland.Client.AppShell.Runtime.Presentation.Hosts;
using IHomeland.Client.AppShell.Presentation.Navigation;
using IHomeland.Client.PersonalWorld.Presentation;
using IHomeland.Client.Core.Runtime.Scenes;
using IHomeland.Client.PersonalWorld.Runtime.Scenes;
using IHomeland.Client.PersonalWorld.Runtime.Composition;
using IHomeland.Client.PersonalWorldCombat.Runtime.Composition;
using IHomeland.Client.Session.Runtime.Composition;

namespace IHomeland.Client.AppShell.Runtime.Composition
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
        /// 限制等待 UI transition 的请求数，过载时由 router 立即返回稳定拒绝。
        /// </summary>
        private const int UiTransitionQueueCapacity = 16;

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
        /// <param name="uiHostRoot">已在 bootstrap 前验证的唯一 UI/Input Host root。</param>
        /// <returns>只供唯一 AppRoot 持有和驱动的不可变 composition 结果。</returns>
        /// <exception cref="ArgumentNullException">环境快照或 UI Host root 为空时抛出。</exception>
        /// <exception cref="InvalidOperationException">同一 AppComposition 实例重复 Build 时抛出。</exception>
        internal AppCompositionResult Build(
            ClientEnvironment environment,
            ClientUiHostRoot uiHostRoot)
        {
            return BuildCore(environment, uiHostRoot, sceneTransitionHost: null, productExperience: false);
        }

        /// <summary>
        /// 在当前 Unity 主线程创建包含个人世界产品竖切的完整 App Scope 对象图。
        /// </summary>
        /// <param name="environment">已在任何网络副作用前验证的环境与构建身份。</param>
        /// <param name="uiHostRoot">已接线全部 production routes 的唯一 UI/Input Host root。</param>
        /// <param name="sceneTransitionHost">BootstrapScene 直接引用的唯一内容 Scene 转换 Host。</param>
        /// <returns>只供唯一 AppRoot 持有和驱动的不可变 composition 结果。</returns>
        /// <exception cref="ArgumentNullException">任一必需引用为空时抛出。</exception>
        /// <exception cref="InvalidOperationException">同一 AppComposition 实例重复 Build 时抛出。</exception>
        internal AppCompositionResult Build(
            ClientEnvironment environment,
            ClientUiHostRoot uiHostRoot,
            ClientWorldSceneTransitionHost sceneTransitionHost)
        {
            if (sceneTransitionHost == null)
            {
                throw new ArgumentNullException(nameof(sceneTransitionHost));
            }

            return BuildCore(environment, uiHostRoot, sceneTransitionHost, productExperience: true);
        }

        /// <summary>构造 isolated fixture 或 production 产品对象图的共享实现。</summary>
        /// <param name="environment">已验证环境与构建身份。</param>
        /// <param name="uiHostRoot">唯一 UI/Input Host root。</param>
        /// <param name="sceneTransitionHost">Production 内容 Scene Host；isolated fixture 为空。</param>
        /// <param name="productExperience">是否连接 production routes 与 Experience。</param>
        /// <returns>冻结后的 App Scope 对象图。</returns>
        private AppCompositionResult BuildCore(
            ClientEnvironment environment,
            ClientUiHostRoot uiHostRoot,
            ClientWorldSceneTransitionHost sceneTransitionHost,
            bool productExperience)
        {
            if (environment == null)
            {
                throw new ArgumentNullException(nameof(environment));
            }

            if (uiHostRoot == null)
            {
                throw new ArgumentNullException(nameof(uiHostRoot));
            }

            uiHostRoot.ValidateConfiguration();

            if (_built)
            {
                throw new InvalidOperationException("同一 AppComposition 不能重复构造 App Scope。");
            }

            _built = true;
            var foundation = FoundationComposition.Create(MainThreadQueueCapacity);
            var infrastructure = InfrastructureComposition.Create(environment);
            var session = SessionComposition.Create(
                environment,
                foundation,
                infrastructure);
            var channels = ChannelComposition.Create(
                environment,
                foundation,
                infrastructure,
                session,
                ControlRetryDelays);
#if DEVELOPMENT_BUILD || UNITY_EDITOR
            channels.GameplayChannel.DiagnosticRecorded += RecordGameplayDiagnostic;
#endif
            var world = WorldComposition.Create(foundation, session, channels);
            var battle = BattleComposition.Create(
                foundation,
                infrastructure,
                session,
                world);
            var presentation = PresentationComposition.Create(
                uiHostRoot,
                sceneTransitionHost,
                productExperience,
                UiTransitionQueueCapacity,
                RollbackTimeout,
                foundation,
                session,
                channels,
                world,
                battle);
            return RuntimeQualificationComposition.Create(
                uiHostRoot,
                foundation,
                infrastructure,
                session,
                channels,
                world,
                battle,
                presentation,
                RollbackTimeout,
                ShutdownTimeout,
                MaximumDispatchesPerFrame);
        }

#if DEVELOPMENT_BUILD || UNITY_EDITOR
        /// <summary>把 gameplay channel 的低敏结构化诊断写入 Development Player.log。</summary>
        /// <param name="diagnostic">不含 endpoint、credential、payload、异常文本或业务 identity 的事件。</param>
        private static void RecordGameplayDiagnostic(ClientGameplayDiagnostic diagnostic)
        {
            UnityEngine.Debug.LogWarning(
                $"[IHOMELAND_NETWORK] component=ClientGameplayChannel generation={diagnostic.Generation} " +
                $"stage={diagnostic.Stage} close_reason={diagnostic.CloseReason} " +
                $"exception_type={diagnostic.ExceptionType}");
        }
#endif
    }
}
