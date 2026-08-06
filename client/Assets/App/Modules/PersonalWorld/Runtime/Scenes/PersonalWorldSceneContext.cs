using System;
using IHomeland.Client.PersonalWorldCombat.Application;
using IHomeland.Client.AppShell.Runtime.Presentation.Hosts;
using IHomeland.Client.PersonalWorld.Presentation;
using IHomeland.Client.Core.Runtime.Scenes;
using IHomeland.Client.PersonalWorldCombat.Runtime.Input;
using IHomeland.Client.PersonalWorldCombat.Runtime.Scenes;
using UnityEngine;

using UnityEngine.Scripting.APIUpdating;

namespace IHomeland.Client.PersonalWorld.Runtime.Scenes
{
    /// <summary>
    /// 持有 PersonalWorldScene 的 camera、lighting、scene root 与表现引用。
    /// </summary>
    /// <remarks>
    /// Context 不保存 Session、socket、world/visit snapshot 或 target 状态机；所有写入必须在
    /// 当前 SceneLifetime 可提交时发生，卸载会先取消 generation 再释放 Unity 引用。
    /// </remarks>
    [DisallowMultipleComponent]
    [MovedFrom(true, "IHomeland.Client.Scenes.PersonalWorld", "IHomeland.Client.Runtime", null)]
    public sealed class PersonalWorldSceneContext : MonoBehaviour
    {
        /// <summary>保存内容场景直接引用的主 Camera。</summary>
        [SerializeField]
        [Tooltip("PersonalWorldScene 直接拥有的主 Camera。")]
        private Camera _sceneCamera;

        /// <summary>保存内容场景直接引用的主 Light。</summary>
        [SerializeField]
        [Tooltip("PersonalWorldScene 直接拥有的主 Light。")]
        private Light _sceneLight;

        /// <summary>保存首期内容表现对象的唯一根节点。</summary>
        [SerializeField]
        [Tooltip("只承载场景表现对象，不放置 App Scope 或业务最终事实。")]
        private Transform _sceneRoot;

        /// <summary>保存 Scene Scope battle Input/Actor/HUD/Camera 聚合 Host。</summary>
        [SerializeField]
        [Tooltip("只持有 Scene Scope 表现与 App Scope 窄端口，不保存 socket 或业务最终事实。")]
        private ClientBattleSceneHost _battleHost;

        /// <summary>保存当前注入的 Scene Scope 生命周期。</summary>
        private SceneLifetime _lifetime;

        /// <summary>保存最近一次无 credential 页面投影，仅用于当前场景表现。</summary>
        private ClientWorldHudViewState _viewState;

        /// <summary>获取当前 Context 是否持有可提交 Scene generation。</summary>
        internal bool CanCommit => _lifetime != null && _lifetime.CanCommit;

        /// <summary>获取当前 Scene generation；未绑定时为 0。</summary>
        internal long SceneGeneration => _lifetime?.Generation ?? 0;

        /// <summary>获取最近一次注入的无 credential HUD 投影。</summary>
        internal ClientWorldHudViewState ViewState => _viewState;

        /// <summary>
        /// 在候选场景提交前验证全部直接表现引用。
        /// </summary>
        /// <exception cref="InvalidOperationException">Camera、Light、SceneRoot 缺失或跨 Scene 时抛出。</exception>
        internal void ValidateConfiguration()
        {
            if (_sceneCamera == null || _sceneLight == null || _sceneRoot == null)
            {
                throw new InvalidOperationException("PersonalWorldSceneContext 缺少 Camera、Light 或 SceneRoot 直接引用。");
            }

            if (_sceneCamera.gameObject.scene != gameObject.scene ||
                _sceneLight.gameObject.scene != gameObject.scene ||
                _sceneRoot.gameObject.scene != gameObject.scene)
            {
                throw new InvalidOperationException("PersonalWorldSceneContext 表现引用必须属于同一内容 Scene。");
            }
        }

        /// <summary>验证 production PersonalWorldScene 已接线唯一 battle Scene Host。</summary>
        /// <exception cref="InvalidOperationException">Battle Host 缺失或跨 Scene 时抛出。</exception>
        internal void ValidateBattleConfiguration()
        {
            ValidateConfiguration();
            if (_battleHost == null ||
                _battleHost.gameObject.scene != gameObject.scene)
            {
                throw new InvalidOperationException(
                    "PersonalWorldSceneContext 缺少同 Scene 的 ClientBattleSceneHost。");
            }

            _battleHost.ValidateConfiguration();
        }

        /// <summary>
        /// 显式接收 current SceneLifetime 与无 credential View State。
        /// </summary>
        /// <param name="lifetime">已由 App Scope owner 分配的 current Scene generation。</param>
        /// <param name="viewState">当前 HUD 低敏页面切片。</param>
        /// <exception cref="ArgumentNullException">任一参数为空时抛出。</exception>
        /// <exception cref="InvalidOperationException">重复绑定或 generation 已失效时抛出。</exception>
        internal void Bind(SceneLifetime lifetime, ClientWorldHudViewState viewState)
        {
            ValidateConfiguration();
            if (_lifetime != null)
            {
                throw new InvalidOperationException("PersonalWorldSceneContext 不能重复绑定 SceneLifetime。");
            }

            _lifetime = lifetime ?? throw new ArgumentNullException(nameof(lifetime));
            _viewState = viewState ?? throw new ArgumentNullException(nameof(viewState));
            if (!_lifetime.CanCommit)
            {
                _lifetime = null;
                _viewState = null;
                throw new InvalidOperationException("PersonalWorldSceneContext 不能绑定已失效 generation。");
            }
        }

        /// <summary>
        /// 绑定 production battle Scene Host；App Scope runtime 与 Input owner 只以窄端口注入。
        /// </summary>
        /// <param name="lifetime">已由 App Scope owner 分配的 current Scene generation。</param>
        /// <param name="viewState">当前 HUD 低敏页面切片。</param>
        /// <param name="battleRuntime">App Scope 唯一 battle runtime facade。</param>
        /// <param name="battleInput">App Scope 唯一 Input System owner 的窄端口。</param>
        internal void Bind(
            SceneLifetime lifetime,
            ClientWorldHudViewState viewState,
            ClientBattleRuntimeCoordinator battleRuntime,
            IClientBattleInputSource battleInput)
        {
            ValidateBattleConfiguration();
            Bind(lifetime, viewState);
            try
            {
                _battleHost.Bind(lifetime, battleRuntime, battleInput);
            }
            catch
            {
                Unbind();
                throw;
            }
        }

        /// <summary>
        /// 在 current generation 上更新无 credential 场景表现投影。
        /// </summary>
        /// <param name="viewState">最新 HUD 低敏页面切片。</param>
        /// <returns>Generation 仍 current 且已经提交更新时返回 true。</returns>
        internal bool TryApply(ClientWorldHudViewState viewState)
        {
            if (viewState == null)
            {
                throw new ArgumentNullException(nameof(viewState));
            }

            if (!CanCommit)
            {
                return false;
            }

            _viewState = viewState;
            return true;
        }

        /// <summary>解除 View State 与 SceneLifetime 引用，不自行处置 App Scope owner。</summary>
        internal void Unbind()
        {
            _battleHost?.Unbind();
            _viewState = null;
            _lifetime = null;
        }

        /// <summary>在 Unity 销毁阶段拒绝任何迟到表现写入。</summary>
        private void OnDestroy()
        {
            Unbind();
        }

        /// <summary>
        /// 为程序化 PlayMode fixture 在激活前配置与 Inspector 等价的直接引用。
        /// </summary>
        /// <param name="sceneCamera">同一 Scene 的主 Camera。</param>
        /// <param name="sceneLight">同一 Scene 的主 Light。</param>
        /// <param name="sceneRoot">同一 Scene 的表现根节点。</param>
        /// <exception cref="InvalidOperationException">组件已经激活时抛出。</exception>
        internal void ConfigureBeforeActivation(Camera sceneCamera, Light sceneLight, Transform sceneRoot)
        {
            if (isActiveAndEnabled)
            {
                throw new InvalidOperationException("PersonalWorldSceneContext 只能在激活前配置。");
            }

            _sceneCamera = sceneCamera;
            _sceneLight = sceneLight;
            _sceneRoot = sceneRoot;
        }

        /// <summary>
        /// 为程序化 PlayMode fixture 在激活前配置 production battle Scene Host。
        /// </summary>
        /// <param name="battleHost">同一 Scene 的唯一 battle Scene Host。</param>
        /// <exception cref="InvalidOperationException">组件已经激活时抛出。</exception>
        internal void ConfigureBattleBeforeActivation(
            ClientBattleSceneHost battleHost)
        {
            if (isActiveAndEnabled)
            {
                throw new InvalidOperationException(
                    "PersonalWorldSceneContext 只能在激活前配置 battle Host。");
            }

            _battleHost = battleHost;
        }
    }
}
