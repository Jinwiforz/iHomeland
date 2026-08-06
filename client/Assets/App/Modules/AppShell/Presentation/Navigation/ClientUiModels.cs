using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using System.Threading;

namespace IHomeland.Client.AppShell.Presentation.Navigation
{
    /// <summary>
    /// 标识已冻结的纯逻辑 UI route；存在 identity 不代表当前 change 已交付对应页面内容。
    /// </summary>
    internal enum ClientUiRouteId
    {
        /// <summary>表示未登记 route，不能用于 definition。</summary>
        None = 0,

        /// <summary>产品登录 screen identity。</summary>
        Login = 1,

        /// <summary>产品持久 shell screen identity。</summary>
        Shell = 2,

        /// <summary>产品个人世界访问 overlay identity。</summary>
        WorldVisit = 3,

        /// <summary>预留设置 overlay identity。</summary>
        Settings = 4,

        /// <summary>产品连接丢失 modal identity。</summary>
        ConnectionLost = 5,

        /// <summary>个人世界内容场景中的 uGUI HUD identity。</summary>
        WorldHud = 6,
    }

    /// <summary>
    /// 标识某个 route 唯一允许使用的 Unity UI framework。
    /// </summary>
    internal enum ClientUiFrameworkOwner
    {
        /// <summary>表示未登记 framework。</summary>
        None = 0,

        /// <summary>表示 route 由 UI Toolkit Host 承载。</summary>
        UiToolkit = 1,

        /// <summary>表示 route 由 uGUI Host 承载。</summary>
        Ugui = 2,
    }

    /// <summary>
    /// 标识 framework-independent UI layer；数值顺序同时定义稳定渲染优先级。
    /// </summary>
    internal enum ClientUiLayer
    {
        /// <summary>表示未登记 layer。</summary>
        None = 0,

        /// <summary>表示随 gameplay 场景显示的 HUD。</summary>
        Hud = 100,

        /// <summary>表示互斥的主 screen。</summary>
        Screen = 200,

        /// <summary>表示可叠加在 screen 上的 overlay。</summary>
        Overlay = 300,

        /// <summary>表示独占输入的 modal 栈。</summary>
        Modal = 400,

        /// <summary>表示高于 modal 的 system notice。</summary>
        System = 500,
    }

    /// <summary>
    /// 标识 route 激活时唯一输入协调器应提交的模式。
    /// </summary>
    internal enum ClientUiInputMode
    {
        /// <summary>表示未登记输入模式。</summary>
        None = 0,

        /// <summary>表示只开放 Player action map 并锁定 cursor。</summary>
        Gameplay = 1,

        /// <summary>表示开放 UI action map 与 cursor。</summary>
        Ui = 2,

        /// <summary>表示开放 UI 文本输入并阻断 gameplay。</summary>
        Text = 3,

        /// <summary>表示 modal 独占 UI 输入并阻断全部底层交互。</summary>
        Modal = 4,
    }

    /// <summary>
    /// 标识 route 关闭后 Host 资源与 binding 的保留策略。
    /// </summary>
    internal enum ClientUiLifecycle
    {
        /// <summary>表示未登记 lifecycle。</summary>
        None = 0,

        /// <summary>表示保留已初始化 Host，但关闭时必须解除 binding。</summary>
        Cached = 1,

        /// <summary>表示关闭时完整 dispose，下一次打开取得新 Host generation。</summary>
        Recreate = 2,

        /// <summary>表示 route 只在匹配 Scene Scope generation 时有效。</summary>
        SceneBound = 3,
    }

    /// <summary>
    /// 定义一个 route 的不可变 framework、层级、输入和生命周期契约。
    /// </summary>
    internal sealed class ClientUiRouteDefinition
    {
        /// <summary>
        /// 创建并立即验证不可变 route definition。
        /// </summary>
        /// <param name="routeId">封闭逻辑 route identity。</param>
        /// <param name="frameworkOwner">唯一 framework owner。</param>
        /// <param name="layer">稳定 framework-independent layer。</param>
        /// <param name="inputMode">route 成为最高交互 owner 时使用的输入模式。</param>
        /// <param name="lifecycle">关闭 route 时采用的资源策略。</param>
        /// <exception cref="ArgumentOutOfRangeException">任一 enum 未登记时抛出。</exception>
        internal ClientUiRouteDefinition(
            ClientUiRouteId routeId,
            ClientUiFrameworkOwner frameworkOwner,
            ClientUiLayer layer,
            ClientUiInputMode inputMode,
            ClientUiLifecycle lifecycle)
        {
            RouteId = ValidateRouteId(routeId);
            FrameworkOwner = ValidateFrameworkOwner(frameworkOwner);
            Layer = ValidateLayer(layer);
            InputMode = ValidateInputMode(inputMode);
            Lifecycle = ValidateLifecycle(lifecycle);
        }

        /// <summary>获取封闭逻辑 route identity。</summary>
        internal ClientUiRouteId RouteId { get; }

        /// <summary>获取唯一 framework owner。</summary>
        internal ClientUiFrameworkOwner FrameworkOwner { get; }

        /// <summary>获取稳定 framework-independent layer。</summary>
        internal ClientUiLayer Layer { get; }

        /// <summary>获取 route 的输入模式。</summary>
        internal ClientUiInputMode InputMode { get; }

        /// <summary>获取 route 的资源生命周期策略。</summary>
        internal ClientUiLifecycle Lifecycle { get; }

        /// <summary>
        /// 验证 route identity 是 enum 中登记的非空值。
        /// </summary>
        /// <param name="value">待验证 identity。</param>
        /// <returns>原 identity。</returns>
        private static ClientUiRouteId ValidateRouteId(ClientUiRouteId value)
        {
            if (value == ClientUiRouteId.None || !Enum.IsDefined(typeof(ClientUiRouteId), value))
            {
                throw new ArgumentOutOfRangeException(nameof(value), value, "UI route identity 未登记。");
            }

            return value;
        }

        /// <summary>
        /// 验证 framework owner 是登记值。
        /// </summary>
        /// <param name="value">待验证 owner。</param>
        /// <returns>原 owner。</returns>
        private static ClientUiFrameworkOwner ValidateFrameworkOwner(ClientUiFrameworkOwner value)
        {
            if (value == ClientUiFrameworkOwner.None || !Enum.IsDefined(typeof(ClientUiFrameworkOwner), value))
            {
                throw new ArgumentOutOfRangeException(nameof(value), value, "UI framework owner 未登记。");
            }

            return value;
        }

        /// <summary>
        /// 验证 layer 是登记值。
        /// </summary>
        /// <param name="value">待验证 layer。</param>
        /// <returns>原 layer。</returns>
        private static ClientUiLayer ValidateLayer(ClientUiLayer value)
        {
            if (value == ClientUiLayer.None || !Enum.IsDefined(typeof(ClientUiLayer), value))
            {
                throw new ArgumentOutOfRangeException(nameof(value), value, "UI layer 未登记。");
            }

            return value;
        }

        /// <summary>
        /// 验证 input mode 是登记值。
        /// </summary>
        /// <param name="value">待验证 input mode。</param>
        /// <returns>原 input mode。</returns>
        private static ClientUiInputMode ValidateInputMode(ClientUiInputMode value)
        {
            if (value == ClientUiInputMode.None || !Enum.IsDefined(typeof(ClientUiInputMode), value))
            {
                throw new ArgumentOutOfRangeException(nameof(value), value, "UI input mode 未登记。");
            }

            return value;
        }

        /// <summary>
        /// 验证 lifecycle 是登记值。
        /// </summary>
        /// <param name="value">待验证 lifecycle。</param>
        /// <returns>原 lifecycle。</returns>
        private static ClientUiLifecycle ValidateLifecycle(ClientUiLifecycle value)
        {
            if (value == ClientUiLifecycle.None || !Enum.IsDefined(typeof(ClientUiLifecycle), value))
            {
                throw new ArgumentOutOfRangeException(nameof(value), value, "UI lifecycle 未登记。");
            }

            return value;
        }
    }

    /// <summary>
    /// 向 Host 交付当前页面 generation 与取消边界，不携带业务事实或资源定位。
    /// </summary>
    internal sealed class ClientUiRouteBinding
    {
        /// <summary>
        /// 创建不可变 route binding。
        /// </summary>
        /// <param name="routeId">当前 route identity。</param>
        /// <param name="navigationGeneration">当前单调 navigation generation。</param>
        /// <param name="sceneGeneration">SceneBound route 的 Scene Scope generation；非 SceneBound 为零。</param>
        /// <param name="cancellationToken">route hide、replace 或停止时取消的页面 token。</param>
        internal ClientUiRouteBinding(
            ClientUiRouteId routeId,
            long navigationGeneration,
            long sceneGeneration,
            CancellationToken cancellationToken)
        {
            if (routeId == ClientUiRouteId.None || !Enum.IsDefined(typeof(ClientUiRouteId), routeId))
            {
                throw new ArgumentOutOfRangeException(nameof(routeId));
            }

            if (navigationGeneration <= 0)
            {
                throw new ArgumentOutOfRangeException(nameof(navigationGeneration));
            }

            if (sceneGeneration < 0)
            {
                throw new ArgumentOutOfRangeException(nameof(sceneGeneration));
            }

            RouteId = routeId;
            NavigationGeneration = navigationGeneration;
            SceneGeneration = sceneGeneration;
            CancellationToken = cancellationToken;
        }

        /// <summary>获取当前 route identity。</summary>
        internal ClientUiRouteId RouteId { get; }

        /// <summary>获取当前 navigation generation。</summary>
        internal long NavigationGeneration { get; }

        /// <summary>获取 Scene Scope generation；零表示不绑定场景。</summary>
        internal long SceneGeneration { get; }

        /// <summary>获取当前页面生命周期取消 token。</summary>
        internal CancellationToken CancellationToken { get; }
    }

    /// <summary>
    /// 保存 framework Host 创建的不透明 focus 引用和其来源 generation。
    /// </summary>
    internal sealed class ClientUiFocusToken
    {
        /// <summary>
        /// 创建不向 router 暴露具体 Unity 对象语义的 focus token。
        /// </summary>
        /// <param name="ownerRouteId">创建 token 的 Host route。</param>
        /// <param name="hostGeneration">创建 token 时的 Host generation。</param>
        /// <param name="value">Host 私有解释的 focus 对象；可以为空。</param>
        internal ClientUiFocusToken(ClientUiRouteId ownerRouteId, long hostGeneration, object value)
        {
            if (ownerRouteId == ClientUiRouteId.None ||
                !Enum.IsDefined(typeof(ClientUiRouteId), ownerRouteId))
            {
                throw new ArgumentOutOfRangeException(nameof(ownerRouteId));
            }

            if (hostGeneration <= 0)
            {
                throw new ArgumentOutOfRangeException(nameof(hostGeneration));
            }

            OwnerRouteId = ownerRouteId;
            HostGeneration = hostGeneration;
            Value = value;
        }

        /// <summary>获取创建 token 的 Host route。</summary>
        internal ClientUiRouteId OwnerRouteId { get; }

        /// <summary>获取创建 token 时的 Host generation。</summary>
        internal long HostGeneration { get; }

        /// <summary>获取只允许原 Host 解释的不透明 focus 对象。</summary>
        internal object Value { get; }
    }

    /// <summary>
    /// 表示 router 原子提交给唯一 Input 协调器的 framework-independent 状态。
    /// </summary>
    internal sealed class ClientUiInputState
    {
        /// <summary>
        /// 创建不可变输入状态。
        /// </summary>
        /// <param name="mode">当前最高交互 route 的输入模式。</param>
        /// <param name="interactiveRouteId">当前唯一最高交互 route；Gameplay 模式为空。</param>
        internal ClientUiInputState(ClientUiInputMode mode, ClientUiRouteId interactiveRouteId)
        {
            if (!Enum.IsDefined(typeof(ClientUiInputMode), mode) || mode == ClientUiInputMode.None)
            {
                throw new ArgumentOutOfRangeException(nameof(mode));
            }

            if (mode != ClientUiInputMode.Gameplay && interactiveRouteId == ClientUiRouteId.None)
            {
                throw new ArgumentException("UI 输入模式必须绑定当前交互 route。", nameof(interactiveRouteId));
            }

            if (mode == ClientUiInputMode.Gameplay && interactiveRouteId != ClientUiRouteId.None)
            {
                throw new ArgumentException("Gameplay 输入模式不能绑定 UI route。", nameof(interactiveRouteId));
            }

            if (interactiveRouteId != ClientUiRouteId.None &&
                !Enum.IsDefined(typeof(ClientUiRouteId), interactiveRouteId))
            {
                throw new ArgumentOutOfRangeException(nameof(interactiveRouteId));
            }

            Mode = mode;
            InteractiveRouteId = interactiveRouteId;
        }

        /// <summary>获取当前输入模式。</summary>
        internal ClientUiInputMode Mode { get; }

        /// <summary>获取当前最高交互 route；Gameplay 模式返回 None。</summary>
        internal ClientUiRouteId InteractiveRouteId { get; }

        /// <summary>创建没有 UI owner 时的 gameplay 输入状态。</summary>
        internal static ClientUiInputState Gameplay { get; } =
            new ClientUiInputState(ClientUiInputMode.Gameplay, ClientUiRouteId.None);
    }

    /// <summary>
    /// 标识 UI navigation 调用的稳定、可测试结果类别。
    /// </summary>
    internal enum ClientUiTransitionCode
    {
        /// <summary>表示转换已经提交或幂等满足。</summary>
        Succeeded = 0,

        /// <summary>表示 route 未登记。</summary>
        NotRegistered = 1,

        /// <summary>表示 SceneBound route 缺少或不匹配 scene generation。</summary>
        InvalidSceneGeneration = 2,

        /// <summary>表示 transition 等待队列已经达到硬上限。</summary>
        Overloaded = 3,

        /// <summary>表示调用方在提交前取消。</summary>
        Cancelled = 4,

        /// <summary>表示 router 已停止并永久拒绝新导航。</summary>
        Stopped = 5,

        /// <summary>表示调用违反 layer 或 modal 栈策略。</summary>
        PolicyRejected = 6,

        /// <summary>表示 Host/input/focus 阶段失败且 candidate 未提交。</summary>
        HostFailure = 7,

        /// <summary>表示 route 已提交，但提交后清理或 subscriber 通知出现可观察失败。</summary>
        CommittedWithPostCommitFailure = 8,
    }

    /// <summary>
    /// 返回 navigation 是否提交以及稳定失败分类，不向 UI 泄漏内部异常文本。
    /// </summary>
    internal sealed class ClientUiTransitionResult
    {
        /// <summary>
        /// 创建不可变 transition result。
        /// </summary>
        /// <param name="code">稳定结果分类。</param>
        /// <param name="committed">route snapshot 是否已提交。</param>
        private ClientUiTransitionResult(ClientUiTransitionCode code, bool committed)
        {
            Code = code;
            Committed = committed;
        }

        /// <summary>获取稳定结果分类。</summary>
        internal ClientUiTransitionCode Code { get; }

        /// <summary>获取 route snapshot 是否已经提交。</summary>
        internal bool Committed { get; }

        /// <summary>获取调用是否成功或幂等满足。</summary>
        internal bool IsSuccess => Code == ClientUiTransitionCode.Succeeded;

        /// <summary>获取发生 snapshot 提交的普通成功结果。</summary>
        internal static ClientUiTransitionResult CommittedSuccess { get; } =
            new ClientUiTransitionResult(ClientUiTransitionCode.Succeeded, committed: true);

        /// <summary>获取无需改变 snapshot 即已幂等满足的成功结果。</summary>
        internal static ClientUiTransitionResult UnchangedSuccess { get; } =
            new ClientUiTransitionResult(ClientUiTransitionCode.Succeeded, committed: false);

        /// <summary>获取 snapshot 已提交但提交后副作用失败的结果。</summary>
        internal static ClientUiTransitionResult PostCommitFailure { get; } =
            new ClientUiTransitionResult(
                ClientUiTransitionCode.CommittedWithPostCommitFailure,
                committed: true);

        /// <summary>
        /// 创建未提交 snapshot 的稳定拒绝结果。
        /// </summary>
        /// <param name="code">除成功与提交后失败外的拒绝分类。</param>
        /// <returns>未提交的稳定结果。</returns>
        /// <exception cref="ArgumentOutOfRangeException">分类不是登记的拒绝值时抛出。</exception>
        internal static ClientUiTransitionResult Rejected(ClientUiTransitionCode code)
        {
            if (!Enum.IsDefined(typeof(ClientUiTransitionCode), code) ||
                code == ClientUiTransitionCode.Succeeded ||
                code == ClientUiTransitionCode.CommittedWithPostCommitFailure)
            {
                throw new ArgumentOutOfRangeException(nameof(code));
            }

            return new ClientUiTransitionResult(code, committed: false);
        }
    }

    /// <summary>
    /// 描述 route snapshot 中一个已提交 active owner，不包含 Host 或 Unity 对象引用。
    /// </summary>
    internal sealed class ClientUiRouteSnapshotItem
    {
        /// <summary>
        /// 创建不可变 route snapshot item。
        /// </summary>
        /// <param name="definition">已复制和验证的 route definition。</param>
        /// <param name="navigationGeneration">提交该 route 的 navigation generation。</param>
        /// <param name="sceneGeneration">绑定的 Scene Scope generation。</param>
        /// <param name="interactive">该 route 当前是否拥有交互资格。</param>
        internal ClientUiRouteSnapshotItem(
            ClientUiRouteDefinition definition,
            long navigationGeneration,
            long sceneGeneration,
            bool interactive)
        {
            Definition = definition ?? throw new ArgumentNullException(nameof(definition));
            if (navigationGeneration <= 0)
            {
                throw new ArgumentOutOfRangeException(nameof(navigationGeneration));
            }

            if (sceneGeneration < 0)
            {
                throw new ArgumentOutOfRangeException(nameof(sceneGeneration));
            }

            NavigationGeneration = navigationGeneration;
            SceneGeneration = sceneGeneration;
            Interactive = interactive;
        }

        /// <summary>获取 route definition。</summary>
        internal ClientUiRouteDefinition Definition { get; }

        /// <summary>获取提交该 route 的 navigation generation。</summary>
        internal long NavigationGeneration { get; }

        /// <summary>获取绑定的 Scene Scope generation。</summary>
        internal long SceneGeneration { get; }

        /// <summary>获取该 route 是否拥有当前交互资格。</summary>
        internal bool Interactive { get; }
    }

    /// <summary>
    /// 保存按稳定层级和打开顺序排列的不可变 active route 快照。
    /// </summary>
    internal sealed class ClientUiRouteSnapshot
    {
        /// <summary>
        /// 创建并防御性复制 route items。
        /// </summary>
        /// <param name="generation">最近一次已提交 navigation generation。</param>
        /// <param name="items">按层级和打开顺序排列的 active items。</param>
        internal ClientUiRouteSnapshot(long generation, IReadOnlyList<ClientUiRouteSnapshotItem> items)
        {
            if (generation < 0)
            {
                throw new ArgumentOutOfRangeException(nameof(generation));
            }

            if (items == null)
            {
                throw new ArgumentNullException(nameof(items));
            }

            var copy = new ClientUiRouteSnapshotItem[items.Count];
            for (var index = 0; index < items.Count; index++)
            {
                copy[index] = items[index] ??
                    throw new ArgumentNullException(nameof(items), $"route snapshot 索引 {index} 不能为空。");
            }

            Generation = generation;
            Items = new ReadOnlyCollection<ClientUiRouteSnapshotItem>(copy);
        }

        /// <summary>获取最近一次已提交 navigation generation。</summary>
        internal long Generation { get; }

        /// <summary>获取按稳定顺序冻结的 active routes。</summary>
        internal IReadOnlyList<ClientUiRouteSnapshotItem> Items { get; }

        /// <summary>获取尚未打开任何 route 的初始快照。</summary>
        internal static ClientUiRouteSnapshot Empty { get; } =
            new ClientUiRouteSnapshot(0, Array.Empty<ClientUiRouteSnapshotItem>());
    }
}
