using System;
using System.Threading.Tasks;
using IHomeland.Client.Core.Composition;
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

            if (!_appRoot.TryClaim())
            {
                Destroy(gameObject);
                return;
            }

            try
            {
                var composition = new AppComposition().Build();
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
        }

        /// <summary>
        /// 为程序化 PlayMode fixture 在激活前提供与 Inspector 等价的直接引用。
        /// </summary>
        /// <param name="appRoot">位于同一 GameObject 且尚未启动的 AppRoot。</param>
        /// <exception cref="ArgumentException">AppRoot 不属于同一 GameObject 时抛出。</exception>
        /// <exception cref="ArgumentNullException">AppRoot 为空时抛出。</exception>
        /// <exception cref="InvalidOperationException">组件已经激活时抛出。</exception>
        internal void ConfigureBeforeActivation(AppRoot appRoot)
        {
            if (appRoot == null)
            {
                throw new ArgumentNullException(nameof(appRoot));
            }

            if (isActiveAndEnabled)
            {
                throw new InvalidOperationException("AppBootstrap 只能在激活前配置直接引用。");
            }

            if (!ReferenceEquals(appRoot.gameObject, gameObject))
            {
                throw new ArgumentException("AppRoot 必须与 AppBootstrap 位于同一 GameObject。", nameof(appRoot));
            }

            _appRoot = appRoot;
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
