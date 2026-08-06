using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Core.Foundation.Lifetime;

namespace IHomeland.Client.PersonalWorld.Presentation
{
    /// <summary>
    /// 标识允许由首期场景转换边界加载的封闭 build scene。
    /// </summary>
    internal enum ClientWorldSceneId
    {
        /// <summary>表示未登记场景，不能用于转换。</summary>
        None = 0,

        /// <summary>同时承载 Owner 与 Visitor 表现的个人世界内容场景。</summary>
        PersonalWorld = 1,
    }

    /// <summary>
    /// 标识场景转换的稳定低敏结果。
    /// </summary>
    internal enum ClientWorldSceneTransitionCode
    {
        /// <summary>场景已经提交或幂等满足。</summary>
        Succeeded = 0,

        /// <summary>调用方取消候选加载或卸载等待。</summary>
        Cancelled = 1,

        /// <summary>请求的场景 identity 未登记。</summary>
        NotRegistered = 2,

        /// <summary>SceneManager 未能加载或卸载登记场景。</summary>
        LoadFailed = 3,

        /// <summary>场景缺少或包含重复 PersonalWorldSceneContext。</summary>
        InvalidContext = 4,

        /// <summary>候选完成时目标或 Scene generation 已失效。</summary>
        Superseded = 5,

        /// <summary>App Scope 已停止并拒绝新转换。</summary>
        Stopped = 6,
    }

    /// <summary>
    /// 保存当前场景转换边界的不可变状态，不暴露 Unity Scene handle。
    /// </summary>
    internal sealed class ClientWorldSceneSnapshot
    {
        /// <summary>创建不可变场景状态。</summary>
        /// <param name="sceneId">已提交场景 identity；没有 current 时为 None。</param>
        /// <param name="sceneGeneration">已提交 Scene Scope generation；没有 current 时为 0。</param>
        /// <param name="targetGeneration">该场景绑定的 world target generation。</param>
        /// <param name="transitioning">是否存在候选加载或卸载。</param>
        /// <param name="lastResult">最近稳定转换结果。</param>
        internal ClientWorldSceneSnapshot(
            ClientWorldSceneId sceneId,
            long sceneGeneration,
            long targetGeneration,
            bool transitioning,
            ClientWorldSceneTransitionCode lastResult)
        {
            SceneId = sceneId;
            SceneGeneration = sceneGeneration;
            TargetGeneration = targetGeneration;
            Transitioning = transitioning;
            LastResult = lastResult;
        }

        /// <summary>获取已提交场景 identity。</summary>
        internal ClientWorldSceneId SceneId { get; }

        /// <summary>获取已提交 Scene Scope generation。</summary>
        internal long SceneGeneration { get; }

        /// <summary>获取该场景绑定的 world target generation。</summary>
        internal long TargetGeneration { get; }

        /// <summary>获取是否存在候选加载或卸载。</summary>
        internal bool Transitioning { get; }

        /// <summary>获取最近稳定转换结果。</summary>
        internal ClientWorldSceneTransitionCode LastResult { get; }

        /// <summary>获取尚无 current scene 的基线状态。</summary>
        internal static ClientWorldSceneSnapshot Empty { get; } = new ClientWorldSceneSnapshot(
            ClientWorldSceneId.None,
            0,
            0,
            false,
            ClientWorldSceneTransitionCode.Succeeded);
    }

    /// <summary>
    /// 定义 Experience 允许使用的纯窄场景转换边界。
    /// </summary>
    internal interface IClientWorldSceneTransition : IAppLifetimeParticipant
    {
        /// <summary>获取最近一次原子提交的不可变场景状态。</summary>
        ClientWorldSceneSnapshot Snapshot { get; }

        /// <summary>加载登记场景并只在 target generation 仍 current 时提交。</summary>
        /// <param name="sceneId">封闭 build scene identity。</param>
        /// <param name="targetGeneration">权威 world target generation。</param>
        /// <param name="viewState">只供 SceneContext 表现的无 credential View State。</param>
        /// <param name="cancellationToken">目标切换或调用方取消等待的信号。</param>
        /// <returns>稳定低敏转换结果。</returns>
        Task<ClientWorldSceneTransitionCode> LoadAsync(
            ClientWorldSceneId sceneId,
            long targetGeneration,
            ClientPersonalWorldViewState viewState,
            CancellationToken cancellationToken);

        /// <summary>使 current Scene Scope 失效并卸载已提交场景。</summary>
        /// <param name="cancellationToken">App 或目标切换清理 deadline。</param>
        /// <returns>稳定低敏转换结果。</returns>
        Task<ClientWorldSceneTransitionCode> UnloadAsync(CancellationToken cancellationToken);

        /// <summary>尝试把最新无 credential View State 提交给 current SceneContext。</summary>
        /// <param name="viewState">Experience 最新不可变页面状态。</param>
        /// <returns>Scene generation 与 target generation 均匹配且已提交时返回 true。</returns>
        bool TryApply(ClientPersonalWorldViewState viewState);
    }

    /// <summary>
    /// 把封闭场景 identity 映射为已登记 build scene 名称。
    /// </summary>
    internal static class ClientWorldSceneCatalog
    {
        /// <summary>获取 PersonalWorldScene 的稳定 Unity build scene 名称。</summary>
        internal const string PersonalWorldSceneName = "PersonalWorldScene";

        /// <summary>尝试解析登记 identity。</summary>
        /// <param name="sceneId">封闭场景 identity。</param>
        /// <param name="sceneName">成功时返回 build scene 名称。</param>
        /// <returns>Identity 已登记时返回 true。</returns>
        internal static bool TryGetSceneName(ClientWorldSceneId sceneId, out string sceneName)
        {
            if (sceneId == ClientWorldSceneId.PersonalWorld)
            {
                sceneName = PersonalWorldSceneName;
                return true;
            }

            sceneName = string.Empty;
            return false;
        }
    }
}
