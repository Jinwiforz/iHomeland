using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Presentation.PersonalWorld;
using IHomeland.Client.Scenes.Contexts;
using UnityEngine;
using UnityEngine.SceneManagement;

namespace IHomeland.Client.Scenes.PersonalWorld
{
    /// <summary>
    /// 在 App Scope 内唯一执行登记内容场景的 additive load、Context 注入与 unload。
    /// </summary>
    /// <remarks>
    /// Host 不决定 world target、不接受任意路径，也不使用全局 Find；Context 只从刚加载 Scene 的
    /// root objects 中发现。候选失败会先失效 generation，再有界卸载候选 Scene。
    /// </remarks>
    [DisallowMultipleComponent]
    public sealed class ClientWorldSceneTransitionHost : MonoBehaviour, IClientWorldSceneTransition
    {
        /// <summary>串行化 load/unload，避免两个候选同时提交 current handle。</summary>
        private readonly SemaphoreSlim _gate = new SemaphoreSlim(1, 1);

        /// <summary>保护生命周期与不可变 snapshot。</summary>
        private readonly object _sync = new object();

        /// <summary>由 Composition 显式注入的 Scene generation owner。</summary>
        private SceneLifetimeOwner _lifetimeOwner;

        /// <summary>保存已提交内容场景的 Unity handle。</summary>
        private Scene _currentScene;

        /// <summary>保存已提交内容场景的唯一 Context。</summary>
        private PersonalWorldSceneContext _currentContext;

        /// <summary>保存已提交内容场景的 SceneLifetime。</summary>
        private SceneLifetime _currentLifetime;

        /// <summary>保存最近一次原子提交的低敏状态。</summary>
        private ClientWorldSceneSnapshot _snapshot = ClientWorldSceneSnapshot.Empty;

        /// <summary>每次 load、unload 或 stop 递增，使排队中的旧候选失去提交资格。</summary>
        private long _transitionRequestGeneration;

        /// <summary>取消 App Scope 内全部候选转换。</summary>
        private CancellationTokenSource _lifetimeCancellation;

        /// <summary>标识 Host 是否已初始化并允许转换。</summary>
        private bool _running;

        /// <summary>标识 Host 是否已经永久停止。</summary>
        private bool _stopped;

        /// <summary>获取最近一次原子提交的不可变场景状态。</summary>
        ClientWorldSceneSnapshot IClientWorldSceneTransition.Snapshot => Snapshot;

        /// <summary>获取最近一次原子提交的不可变场景状态，供同程序集协调器与测试观察。</summary>
        internal ClientWorldSceneSnapshot Snapshot
        {
            get
            {
                lock (_sync)
                {
                    return _snapshot;
                }
            }
        }

#if DEVELOPMENT_BUILD || UNITY_EDITOR
        /// <summary>获取资格运行可观察的已提交内容Scene owner数量。</summary>
        internal int QualificationSceneOwnerCount
        {
            get
            {
                lock (_sync)
                {
                    return _currentContext != null && _currentScene.IsValid() ? 1 : 0;
                }
            }
        }
#endif

        /// <summary>
        /// 由 Composition 在 AppLifetime 启动前注入唯一 SceneLifetimeOwner。
        /// </summary>
        /// <param name="lifetimeOwner">App Scope 唯一 Scene generation owner。</param>
        /// <exception cref="ArgumentNullException">Owner 为空时抛出。</exception>
        /// <exception cref="InvalidOperationException">Host 已启动或重复配置时抛出。</exception>
        internal void Configure(SceneLifetimeOwner lifetimeOwner)
        {
            if (_running || _stopped || _lifetimeOwner != null)
            {
                throw new InvalidOperationException("ClientWorldSceneTransitionHost 只能在启动前配置一次。");
            }

            _lifetimeOwner = lifetimeOwner ?? throw new ArgumentNullException(nameof(lifetimeOwner));
        }

        /// <summary>启用显式场景转换，不自动加载任何内容 Scene。</summary>
        /// <param name="cancellationToken">AppLifetime 启动取消信号。</param>
        /// <returns>Host 可接收显式请求时完成。</returns>
        /// <exception cref="InvalidOperationException">缺少 owner、重复初始化或停止后重启时抛出。</exception>
        public Task InitializeAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            lock (_sync)
            {
                if (_lifetimeOwner == null || _running || _stopped)
                {
                    throw new InvalidOperationException("ClientWorldSceneTransitionHost 缺少 owner 或生命周期非法。");
                }

                _lifetimeCancellation = new CancellationTokenSource();
                _running = true;
                _snapshot = ClientWorldSceneSnapshot.Empty;
            }

            return Task.CompletedTask;
        }

        /// <summary>加载登记场景并只在 target generation 仍 current 时提交。</summary>
        /// <param name="sceneId">封闭 build scene identity。</param>
        /// <param name="targetGeneration">权威 world target generation。</param>
        /// <param name="viewState">只供 SceneContext 表现的无 credential View State。</param>
        /// <param name="cancellationToken">目标切换或调用方取消等待的信号。</param>
        /// <returns>稳定低敏转换结果。</returns>
        Task<ClientWorldSceneTransitionCode> IClientWorldSceneTransition.LoadAsync(
            ClientWorldSceneId sceneId,
            long targetGeneration,
            ClientPersonalWorldViewState viewState,
            CancellationToken cancellationToken)
        {
            return LoadAsync(sceneId, targetGeneration, viewState, cancellationToken);
        }

        /// <summary>加载登记场景并只在 target generation 仍 current 时提交。</summary>
        /// <param name="sceneId">封闭 build scene identity。</param>
        /// <param name="targetGeneration">权威 world target generation。</param>
        /// <param name="viewState">只供 SceneContext 表现的无 credential View State。</param>
        /// <param name="cancellationToken">目标切换或调用方取消等待的信号。</param>
        /// <returns>稳定低敏转换结果。</returns>
        internal async Task<ClientWorldSceneTransitionCode> LoadAsync(
            ClientWorldSceneId sceneId,
            long targetGeneration,
            ClientPersonalWorldViewState viewState,
            CancellationToken cancellationToken)
        {
            if (!ClientWorldSceneCatalog.TryGetSceneName(sceneId, out var sceneName))
            {
                return ClientWorldSceneTransitionCode.NotRegistered;
            }

            if (targetGeneration <= 0 || viewState == null || viewState.TargetGeneration != targetGeneration)
            {
                return ClientWorldSceneTransitionCode.Superseded;
            }

            if (!TryBeginRequest(targetGeneration, out var requestGeneration))
            {
                return ClientWorldSceneTransitionCode.Stopped;
            }

            var entered = false;
            try
            {
                await _gate.WaitAsync(cancellationToken);
                entered = true;
                CancellationToken lifetimeToken;
                lock (_sync)
                {
                    if (!_running || _stopped)
                    {
                        return ClientWorldSceneTransitionCode.Stopped;
                    }

                    if (requestGeneration != _transitionRequestGeneration)
                    {
                        return ClientWorldSceneTransitionCode.Superseded;
                    }

                    lifetimeToken = _lifetimeCancellation.Token;
                    _snapshot = new ClientWorldSceneSnapshot(
                        _snapshot.SceneId,
                        _snapshot.SceneGeneration,
                        targetGeneration,
                        true,
                        _snapshot.LastResult);
                }

                using (var linked = CancellationTokenSource.CreateLinkedTokenSource(
                           lifetimeToken,
                           cancellationToken))
                {
                    await UnloadCurrentCoreAsync(linked.Token);
                    var candidateLifetime = _lifetimeOwner.BeginScene();
                    Scene candidate = default;
                    PersonalWorldSceneContext context = null;
                    try
                    {
                        var operation = SceneManager.LoadSceneAsync(sceneName, LoadSceneMode.Additive);
                        if (operation == null)
                        {
                            _lifetimeOwner.Release(candidateLifetime);
                            return SetResult(ClientWorldSceneTransitionCode.LoadFailed, requestGeneration);
                        }

                        await AwaitOperationAsync(operation, linked.Token);
                        candidate = SceneManager.GetSceneByName(sceneName);
                        if (!candidate.IsValid() || !candidate.isLoaded ||
                            !candidateLifetime.CanCommit || linked.IsCancellationRequested ||
                            !IsRequestCurrent(requestGeneration))
                        {
                            await RollbackCandidateAsync(candidate, context, candidateLifetime);
                            return SetResult(ClientWorldSceneTransitionCode.Superseded, requestGeneration);
                        }

                        context = FindSingleContext(candidate);
                        context.ValidateConfiguration();
                        context.Bind(candidateLifetime, viewState.WorldHud);
                        lock (_sync)
                        {
                            if (!_running || _stopped || !candidateLifetime.CanCommit ||
                                requestGeneration != _transitionRequestGeneration)
                            {
                                // 锁内只判定所有权；Unity 清理由锁外 rollback 完成。
                            }
                            else
                            {
                                _currentScene = candidate;
                                _currentContext = context;
                                _currentLifetime = candidateLifetime;
                                _snapshot = new ClientWorldSceneSnapshot(
                                    sceneId,
                                    candidateLifetime.Generation,
                                    targetGeneration,
                                    false,
                                    ClientWorldSceneTransitionCode.Succeeded);
                                return ClientWorldSceneTransitionCode.Succeeded;
                            }
                        }

                        await RollbackCandidateAsync(candidate, context, candidateLifetime);
                        return SetResult(ClientWorldSceneTransitionCode.Superseded, requestGeneration);
                    }
                    catch (OperationCanceledException)
                    {
                        await RollbackCandidateAsync(candidate, context, candidateLifetime);
                        return SetResult(ClientWorldSceneTransitionCode.Cancelled, requestGeneration);
                    }
                    catch (InvalidOperationException)
                    {
                        await RollbackCandidateAsync(candidate, context, candidateLifetime);
                        return SetResult(ClientWorldSceneTransitionCode.InvalidContext, requestGeneration);
                    }
                    catch (Exception)
                    {
                        await RollbackCandidateAsync(candidate, context, candidateLifetime);
                        return SetResult(ClientWorldSceneTransitionCode.LoadFailed, requestGeneration);
                    }
                }
            }
            catch (OperationCanceledException)
            {
                return SetResult(ClientWorldSceneTransitionCode.Cancelled, requestGeneration);
            }
            finally
            {
                if (entered)
                {
                    _gate.Release();
                }
            }
        }

        /// <summary>使 current Scene Scope 失效并卸载已提交场景。</summary>
        /// <param name="cancellationToken">App 或目标切换清理 deadline。</param>
        /// <returns>稳定低敏转换结果。</returns>
        Task<ClientWorldSceneTransitionCode> IClientWorldSceneTransition.UnloadAsync(
            CancellationToken cancellationToken)
        {
            return UnloadAsync(cancellationToken);
        }

        /// <summary>使 current Scene Scope 失效并卸载已提交场景。</summary>
        /// <param name="cancellationToken">App 或目标切换清理 deadline。</param>
        /// <returns>稳定低敏转换结果。</returns>
        internal async Task<ClientWorldSceneTransitionCode> UnloadAsync(CancellationToken cancellationToken)
        {
            if (!TryBeginRequest(targetGeneration: 0, out var requestGeneration))
            {
                return ClientWorldSceneTransitionCode.Stopped;
            }

            var entered = false;
            try
            {
                await _gate.WaitAsync(cancellationToken);
                entered = true;
                await UnloadCurrentCoreAsync(cancellationToken);
                return SetResult(ClientWorldSceneTransitionCode.Succeeded, requestGeneration);
            }
            catch (OperationCanceledException)
            {
                return SetResult(ClientWorldSceneTransitionCode.Cancelled, requestGeneration);
            }
            catch (Exception)
            {
                return SetResult(ClientWorldSceneTransitionCode.LoadFailed, requestGeneration);
            }
            finally
            {
                if (entered)
                {
                    _gate.Release();
                }
            }
        }

        /// <summary>尝试把最新无 credential View State 提交给 current SceneContext。</summary>
        /// <param name="viewState">Experience 最新不可变页面状态。</param>
        /// <returns>Scene generation 与 target generation 均匹配且已提交时返回 true。</returns>
        bool IClientWorldSceneTransition.TryApply(ClientPersonalWorldViewState viewState)
        {
            return TryApply(viewState);
        }

        /// <summary>尝试把最新无 credential View State 提交给 current SceneContext。</summary>
        /// <param name="viewState">Experience 最新不可变页面状态。</param>
        /// <returns>Scene generation 与 target generation 均匹配且已提交时返回 true。</returns>
        internal bool TryApply(ClientPersonalWorldViewState viewState)
        {
            if (viewState == null)
            {
                throw new ArgumentNullException(nameof(viewState));
            }

            PersonalWorldSceneContext context;
            ClientWorldSceneSnapshot snapshot;
            lock (_sync)
            {
                context = _currentContext;
                snapshot = _snapshot;
            }

            return context != null && snapshot.SceneGeneration == viewState.SceneGeneration &&
                   snapshot.TargetGeneration == viewState.TargetGeneration &&
                   context.TryApply(viewState.WorldHud);
        }

        /// <summary>永久拒绝新转换，取消候选并卸载 current 内容 Scene。</summary>
        /// <param name="cancellationToken">AppLifetime 共享停止 deadline。</param>
        /// <returns>本 Host 持有的 Scene/Context 已释放时完成。</returns>
        public async Task StopAsync(CancellationToken cancellationToken)
        {
            CancellationTokenSource lifetime;
            lock (_sync)
            {
                if (_stopped)
                {
                    return;
                }

                _running = false;
                _stopped = true;
                _transitionRequestGeneration++;
                lifetime = _lifetimeCancellation;
            }

            lifetime?.Cancel();
            var entered = false;
            try
            {
                await _gate.WaitAsync(cancellationToken);
                entered = true;
                await UnloadCurrentCoreAsync(cancellationToken);
            }
            finally
            {
                if (entered)
                {
                    _gate.Release();
                }

                lifetime?.Dispose();
                lock (_sync)
                {
                    _snapshot = new ClientWorldSceneSnapshot(
                        ClientWorldSceneId.None,
                        0,
                        0,
                        false,
                        ClientWorldSceneTransitionCode.Stopped);
                }
            }
        }

        /// <summary>先失效 SceneLifetime 和 Context，再卸载 current Unity Scene。</summary>
        /// <param name="cancellationToken">卸载等待取消信号。</param>
        /// <returns>Current scene 已清除时完成。</returns>
        private async Task UnloadCurrentCoreAsync(CancellationToken cancellationToken)
        {
            Scene scene;
            PersonalWorldSceneContext context;
            SceneLifetime lifetime;
            lock (_sync)
            {
                scene = _currentScene;
                context = _currentContext;
                lifetime = _currentLifetime;
                _currentScene = default;
                _currentContext = null;
                _currentLifetime = null;
                _snapshot = new ClientWorldSceneSnapshot(
                    ClientWorldSceneId.None,
                    0,
                    0,
                    true,
                    _snapshot.LastResult);
            }

            context?.Unbind();
            if (lifetime != null)
            {
                _lifetimeOwner.Release(lifetime);
            }

            if (scene.IsValid() && scene.isLoaded)
            {
                var unload = SceneManager.UnloadSceneAsync(scene);
                if (unload == null)
                {
                    throw new InvalidOperationException("SceneManager 未接受登记场景卸载。");
                }

                await AwaitOperationAsync(unload, cancellationToken);
            }
        }

        /// <summary>在候选失败时撤销 Context、generation 与 Scene。</summary>
        /// <param name="scene">可能尚未有效的候选 Scene。</param>
        /// <param name="context">可能尚未绑定的候选 Context。</param>
        /// <param name="lifetime">候选 SceneLifetime。</param>
        /// <returns>可执行清理均已尝试时完成。</returns>
        private async Task RollbackCandidateAsync(
            Scene scene,
            PersonalWorldSceneContext context,
            SceneLifetime lifetime)
        {
            context?.Unbind();
            if (lifetime != null)
            {
                _lifetimeOwner.Release(lifetime);
            }

            if (scene.IsValid() && scene.isLoaded)
            {
                var unload = SceneManager.UnloadSceneAsync(scene);
                if (unload != null)
                {
                    await AwaitOperationAsync(unload, CancellationToken.None);
                }
            }
        }

        /// <summary>只在指定 Scene root objects 中查找唯一 Context。</summary>
        /// <param name="scene">已完成 additive load 的候选 Scene。</param>
        /// <returns>恰好一个 PersonalWorldSceneContext。</returns>
        /// <exception cref="InvalidOperationException">缺少或重复 Context 时抛出。</exception>
        internal static PersonalWorldSceneContext FindSingleContext(Scene scene)
        {
            var contexts = new List<PersonalWorldSceneContext>();
            foreach (var root in scene.GetRootGameObjects())
            {
                contexts.AddRange(root.GetComponentsInChildren<PersonalWorldSceneContext>(includeInactive: true));
            }

            if (contexts.Count != 1)
            {
                throw new InvalidOperationException($"PersonalWorldScene 必须恰好包含一个 Context，实际为 {contexts.Count}。");
            }

            return contexts[0];
        }

        /// <summary>以可取消轮询等待 Unity AsyncOperation；只在 Unity 主线程继续。</summary>
        /// <param name="operation">SceneManager 返回的非空 operation。</param>
        /// <param name="cancellationToken">目标或 App 取消信号。</param>
        /// <returns>Operation 完成时结束。</returns>
        private static async Task AwaitOperationAsync(
            AsyncOperation operation,
            CancellationToken cancellationToken)
        {
            while (!operation.isDone)
            {
                cancellationToken.ThrowIfCancellationRequested();
                await Task.Yield();
            }

            cancellationToken.ThrowIfCancellationRequested();
        }

        /// <summary>提交最近稳定转换结果。</summary>
        /// <param name="result">低敏转换结果。</param>
        /// <param name="requestGeneration">产生结果的转换请求代际。</param>
        /// <returns>原结果。</returns>
        private ClientWorldSceneTransitionCode SetResult(
            ClientWorldSceneTransitionCode result,
            long requestGeneration)
        {
            lock (_sync)
            {
                if (requestGeneration != _transitionRequestGeneration)
                {
                    return result;
                }

                _snapshot = new ClientWorldSceneSnapshot(
                    _snapshot.SceneId,
                    _snapshot.SceneGeneration,
                    _snapshot.TargetGeneration,
                    false,
                    result);
            }

            return result;
        }

        /// <summary>在 Host 仍运行时登记最新转换请求，并立即使所有旧候选失去提交资格。</summary>
        /// <param name="targetGeneration">新 load 的 target generation；unload 使用 0。</param>
        /// <param name="requestGeneration">成功时返回供候选提交比对的转换请求代际。</param>
        /// <returns>Host 仍接受转换请求时返回 true。</returns>
        private bool TryBeginRequest(long targetGeneration, out long requestGeneration)
        {
            lock (_sync)
            {
                if (!_running || _stopped)
                {
                    requestGeneration = 0;
                    return false;
                }

                _transitionRequestGeneration++;
                requestGeneration = _transitionRequestGeneration;
                _snapshot = new ClientWorldSceneSnapshot(
                    _snapshot.SceneId,
                    _snapshot.SceneGeneration,
                    targetGeneration,
                    true,
                    _snapshot.LastResult);
                return true;
            }
        }

        /// <summary>检查候选是否仍是最新转换请求。</summary>
        /// <param name="requestGeneration">候选捕获的转换请求代际。</param>
        /// <returns>Host 运行且请求仍 current 时返回 true。</returns>
        private bool IsRequestCurrent(long requestGeneration)
        {
            lock (_sync)
            {
                return _running && !_stopped && requestGeneration == _transitionRequestGeneration;
            }
        }
    }
}
