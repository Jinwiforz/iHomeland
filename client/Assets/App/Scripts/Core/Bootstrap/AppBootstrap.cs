using System;
using System.Threading.Tasks;
using IHomeland.Client.Core.Composition;
using IHomeland.Client.Core.Configuration;
using IHomeland.Client.Presentation.Hosts;
using UnityEngine;

namespace IHomeland.Client.Core.Bootstrap
{
    /// <summary>
    /// 作为 BootstrapScene 的唯一入口，争用 AppRoot 并调用唯一 AppComposition。
    /// </summary>
    /// <remarks>
    /// 该 Host 不实现业务状态。重复场景实例会在创建对象图前被拒绝；启动异常在 Awake
    /// 内观察并触发已有生命周期回滚，不依赖 MonoBehaviour 的隐式 Start 顺序。
    /// </remarks>
    [DisallowMultipleComponent]
    [RequireComponent(typeof(AppRoot))]
    public sealed class AppBootstrap : MonoBehaviour
    {
        /// <summary>
        /// 保存 BootstrapScene 直接序列化引用的同一 GameObject AppRoot。
        /// </summary>
        [SerializeField]
        [Tooltip("同一 Bootstrap GameObject 上承载唯一 App Scope 的 AppRoot。")]
        private AppRoot _appRoot;

        /// <summary>
        /// 保存 BootstrapScene 直接引用的非敏感部署环境资产。
        /// </summary>
        [SerializeField]
        [Tooltip("不含 secret 的客户端环境配置；启动前复制为不可变运行快照。")]
        private ClientEnvironmentProfile _environmentProfile = null;

        /// <summary>
        /// 保存 BootstrapScene 直接序列化引用的同一持久 GameObject UI/Input Host root。
        /// </summary>
        [SerializeField]
        [Tooltip("同一 Bootstrap GameObject 上唯一拥有 Input System clone 与 UI Host 列表的 root。")]
        private ClientUiHostRoot _uiHostRoot;

        /// <summary>
        /// 保存程序化 PlayMode fixture 在激活前注入的不可变环境；正式场景保持为空。
        /// </summary>
        private ClientEnvironment _configuredEnvironment;

        /// <summary>
        /// 在 Unity 主线程争用唯一 root、构造对象图并观察完整启动结果。
        /// </summary>
        /// <remarks>
        /// Unity callback 使用 async void，但所有异常均在本方法内捕获。失败时销毁当前 root，
        /// OnDestroy 会共享同一停止结果，不会重复清理参与者。
        /// </remarks>
        private async void Awake()
        {
            if (_appRoot == null)
            {
                Debug.LogError("AppBootstrap 缺少 AppRoot 直接序列化引用。", this);
                enabled = false;
                return;
            }

            if (_configuredEnvironment == null && _environmentProfile == null)
            {
                Debug.LogError("AppBootstrap 缺少 ClientEnvironmentProfile 直接序列化引用。", this);
                enabled = false;
                return;
            }

            if (_uiHostRoot == null)
            {
                Debug.LogError("AppBootstrap 缺少 ClientUiHostRoot 直接序列化引用。", this);
                enabled = false;
                return;
            }

            if (!ReferenceEquals(_uiHostRoot.gameObject, gameObject))
            {
                Debug.LogError("ClientUiHostRoot 必须与 AppBootstrap 位于同一持久 GameObject。", this);
                enabled = false;
                return;
            }

            try
            {
                _uiHostRoot.ValidateConfiguration();
            }
            catch (Exception configurationError)
            {
                Debug.LogException(configurationError, this);
                enabled = false;
                return;
            }

            if (!_appRoot.TryClaim())
            {
                Destroy(gameObject);
                return;
            }

            try
            {
                var environment = _configuredEnvironment ?? _environmentProfile.Build(
                    UnityEngine.Application.version,
                    ClientContractBaseline.ProtocolVersion);
                var composition = new AppComposition().Build(environment, _uiHostRoot);
                _appRoot.Attach(composition);
                await _appRoot.StartAsync();
            }
            catch (Exception startupError)
            {
                Debug.LogException(startupError, this);
                await StopAfterStartupFailureAsync();
                Destroy(gameObject);
            }
        }

        /// <summary>
        /// 在对象首次添加或重置时填充同一 GameObject 的直接 AppRoot 引用。
        /// </summary>
        /// <remarks>该 Editor callback 只设置序列化引用，不创建对象图或执行业务逻辑。</remarks>
        private void Reset()
        {
            _appRoot = GetComponent<AppRoot>();
            _uiHostRoot = GetComponent<ClientUiHostRoot>();
        }

        /// <summary>
        /// 为程序化 PlayMode fixture 在激活前提供与 Inspector 等价的直接引用。
        /// </summary>
        /// <param name="appRoot">位于同一 GameObject 且尚未启动的 AppRoot。</param>
        /// <param name="uiHostRoot">位于同一 GameObject 且尚未初始化的 UI/Input Host root。</param>
        /// <param name="environment">已验证且不访问 Unity 资产的测试环境快照。</param>
        /// <exception cref="ArgumentException">AppRoot 或 UI Host root 不属于同一 GameObject 时抛出。</exception>
        /// <exception cref="ArgumentNullException">任一必需引用为空时抛出。</exception>
        /// <exception cref="InvalidOperationException">组件已经激活时抛出。</exception>
        internal void ConfigureBeforeActivation(
            AppRoot appRoot,
            ClientUiHostRoot uiHostRoot,
            ClientEnvironment environment)
        {
            if (appRoot == null)
            {
                throw new ArgumentNullException(nameof(appRoot));
            }

            if (isActiveAndEnabled)
            {
                throw new InvalidOperationException("AppBootstrap 只能在激活前配置直接引用。");
            }

            if (environment == null)
            {
                throw new ArgumentNullException(nameof(environment));
            }

            if (uiHostRoot == null)
            {
                throw new ArgumentNullException(nameof(uiHostRoot));
            }

            if (!ReferenceEquals(appRoot.gameObject, gameObject))
            {
                throw new ArgumentException("AppRoot 必须与 AppBootstrap 位于同一 GameObject。", nameof(appRoot));
            }

            if (!ReferenceEquals(uiHostRoot.gameObject, gameObject))
            {
                throw new ArgumentException(
                    "ClientUiHostRoot 必须与 AppBootstrap 位于同一 GameObject。",
                    nameof(uiHostRoot));
            }

            uiHostRoot.ValidateConfiguration();

            _appRoot = appRoot;
            _uiHostRoot = uiHostRoot;
            _configuredEnvironment = environment;
        }

        /// <summary>
        /// 观察启动失败后的共享停止结果，避免清理异常从 Awake 逃逸。
        /// </summary>
        /// <returns>停止结果已经完成并被记录时结束的任务。</returns>
        private async Task StopAfterStartupFailureAsync()
        {
            try
            {
                await _appRoot.StopAsync();
            }
            catch (Exception stopError)
            {
                Debug.LogException(stopError, this);
            }
        }
    }
}
