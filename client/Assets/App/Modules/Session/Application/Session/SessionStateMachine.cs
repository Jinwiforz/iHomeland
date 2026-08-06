using System;

namespace IHomeland.Client.Session.Application
{
    /// <summary>
    /// 纯计算 Session generation、epoch 与迟到提交决议，不保存 current Session 事实。
    /// </summary>
    internal sealed class SessionStateMachine
    {
        /// <summary>判断 captured generation 是否仍为 current authenticated lineage。</summary>
        internal bool IsCurrent(
            ClientSessionOwnerState state,
            ClientSessionSnapshot snapshot,
            long capturedGeneration)
        {
            return state == ClientSessionOwnerState.Authenticated &&
                   snapshot != null &&
                   snapshot.Generation == capturedGeneration;
        }

        /// <summary>判断 control invalidation 是否能替换 current authority。</summary>
        internal bool CanAcceptInvalidation(
            ClientSessionOwnerState state,
            ClientSessionSnapshot snapshot,
            long sourceGeneration,
            ulong invalidatedEpoch)
        {
            return IsCurrent(state, snapshot, sourceGeneration) &&
                   invalidatedEpoch > checked((ulong)snapshot.Session.SessionEpoch);
        }

        /// <summary>计算 owner 下一代 local generation。</summary>
        internal long NextGeneration(long currentGeneration)
        {
            if (currentGeneration == long.MaxValue)
            {
                throw new InvalidOperationException("Session generation 已耗尽。");
            }

            return currentGeneration + 1;
        }

        /// <summary>判断清理前状态是否需要发布唯一 invalidation event。</summary>
        internal bool ShouldPublishInvalidation(
            ClientSessionOwnerState state,
            ClientSessionSnapshot snapshot)
        {
            return state == ClientSessionOwnerState.Authenticated &&
                   snapshot != null;
        }
    }
}
