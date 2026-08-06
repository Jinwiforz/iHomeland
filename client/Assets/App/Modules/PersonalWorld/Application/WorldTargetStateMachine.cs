using System;
using IHomeland.Client.Networking.Application.Contracts;
using IHomeland.Client.Networking.Application.Gameplay;

namespace IHomeland.Client.PersonalWorld.Application
{
    /// <summary>保存一次 world target intent 的不可变 generation lease。</summary>
    internal sealed class WorldTargetIntentLease
    {
        /// <summary>创建绑定 target 与 Session generation 的 lease。</summary>
        internal WorldTargetIntentLease(long targetGeneration, long sessionGeneration)
        {
            if (targetGeneration <= 0 || sessionGeneration <= 0)
            {
                throw new ArgumentOutOfRangeException(
                    targetGeneration <= 0
                        ? nameof(targetGeneration)
                        : nameof(sessionGeneration));
            }

            TargetGeneration = targetGeneration;
            SessionGeneration = sessionGeneration;
        }

        /// <summary>获取本次 target generation。</summary>
        internal long TargetGeneration { get; }

        /// <summary>获取发起时 Session generation。</summary>
        internal long SessionGeneration { get; private set; }

        /// <summary>只允许同一服务端 lineage 的本地 generation 前进。</summary>
        internal void AdvanceSessionGeneration(long generation)
        {
            if (generation < SessionGeneration)
            {
                throw new ArgumentOutOfRangeException(nameof(generation));
            }

            SessionGeneration = generation;
        }
    }

    /// <summary>纯计算 world target 合法迁移、generation 与迟到提交决议。</summary>
    internal sealed class WorldTargetStateMachine
    {
        /// <summary>尝试从指定 source 创建唯一下一代 intent lease。</summary>
        internal bool TryBegin(
            ClientWorldFlowSnapshot current,
            bool intentActive,
            ClientWorldFlowState required,
            ClientWorldFlowState next,
            long sessionGeneration,
            out WorldTargetIntentLease lease)
        {
            if (current == null)
            {
                throw new ArgumentNullException(nameof(current));
            }

            if (current.State != required ||
                intentActive ||
                sessionGeneration <= 0 ||
                !IsLegalTransition(required, next) ||
                current.TargetGeneration == long.MaxValue)
            {
                lease = null;
                return false;
            }

            lease = new WorldTargetIntentLease(
                current.TargetGeneration + 1,
                sessionGeneration);
            return true;
        }

        /// <summary>判断异步完成仍可提交 current target。</summary>
        internal bool CanCommit(
            ClientWorldFlowSnapshot current,
            bool intentActive,
            WorldTargetIntentLease lease,
            long currentSessionGeneration)
        {
            return current != null &&
                   lease != null &&
                   current.State != ClientWorldFlowState.Stopped &&
                   intentActive &&
                   current.TargetGeneration == lease.TargetGeneration &&
                   currentSessionGeneration == lease.SessionGeneration;
        }

        /// <summary>判断 source 到 flow-in-progress/terminal target 的封闭迁移。</summary>
        internal bool IsLegalTransition(
            ClientWorldFlowState source,
            ClientWorldFlowState target)
        {
            switch (source)
            {
                case ClientWorldFlowState.Inactive:
                    return target == ClientWorldFlowState.ResolvingOwnWorld;
                case ClientWorldFlowState.OwnWorld:
                    return target == ClientWorldFlowState.JoiningVisit ||
                           target == ClientWorldFlowState.ConnectionLost;
                case ClientWorldFlowState.Visiting:
                    return target == ClientWorldFlowState.ReturningOwnWorld ||
                           target == ClientWorldFlowState.ConnectionLost;
                case ClientWorldFlowState.ReturningOwnWorld:
                    return target == ClientWorldFlowState.ReturningOwnWorld;
                case ClientWorldFlowState.ConnectionLost:
                    return target == ClientWorldFlowState.ResolvingOwnWorld ||
                           target == ClientWorldFlowState.RecoveringTarget;
                default:
                    return false;
            }
        }
    }

    /// <summary>把 HTTP、Gameplay 与恢复失败统一映射为封闭低敏分类。</summary>
    internal sealed class WorldAdmissionFailureMapper
    {
        /// <summary>把 accept 的不确定 transport completion 映射为 commit-unknown。</summary>
        internal ClientWorldFlowFailure MapAccept(
            ClientGatewayResult<ClientVisitReservation> result)
        {
            if (result?.Failure != null &&
                (result.Failure.Kind == ClientGatewayFailureKind.CallerCancelled ||
                 result.Failure.Kind == ClientGatewayFailureKind.Timeout ||
                 result.Failure.Kind == ClientGatewayFailureKind.Transport))
            {
                return ClientWorldFlowFailure.CommitUnknown;
            }

            return MapGateway(result);
        }

        /// <summary>映射任意 Session gateway result。</summary>
        internal ClientWorldFlowFailure MapGateway<T>(ClientGatewayResult<T> result)
        {
            if (result == null || result.IsSuccess)
            {
                return ClientWorldFlowFailure.Protocol;
            }

            if (result.ServerError != null)
            {
                return ClientWorldFlowFailure.Rejected;
            }

            return result.Failure.Kind == ClientGatewayFailureKind.CallerCancelled
                ? ClientWorldFlowFailure.CallerCancelled
                : result.Failure.Kind == ClientGatewayFailureKind.MalformedResponse ||
                  result.Failure.Kind == ClientGatewayFailureKind.ResponseTooLarge
                    ? ClientWorldFlowFailure.Protocol
                    : result.Failure.Kind == ClientGatewayFailureKind.LocalPolicy ||
                      result.Failure.Kind == ClientGatewayFailureKind.SecureStorage ||
                      result.Failure.Kind == ClientGatewayFailureKind.Stopped
                        ? ClientWorldFlowFailure.Policy
                        : ClientWorldFlowFailure.Transport;
        }

        /// <summary>映射任意 typed Gameplay result。</summary>
        internal ClientWorldFlowFailure MapGameplay<T>(ClientGameplayResult<T> result)
            where T : class
        {
            if (result == null || result.IsSuccess)
            {
                return ClientWorldFlowFailure.Protocol;
            }

            if (result.ServerError != null)
            {
                return ClientWorldFlowFailure.Rejected;
            }

            return result.Failure == ClientGameplayFailureKind.CallerCancelled
                ? ClientWorldFlowFailure.CallerCancelled
                : result.Failure == ClientGameplayFailureKind.Protocol
                    ? ClientWorldFlowFailure.Protocol
                    : result.Failure == ClientGameplayFailureKind.Policy
                        ? ClientWorldFlowFailure.Policy
                        : ClientWorldFlowFailure.Transport;
        }

        /// <summary>把 gateway failure 映射为 recovery terminal。</summary>
        internal ClientConnectionRecoveryResultKind MapRecoveryGateway<T>(
            ClientGatewayResult<T> result)
        {
            if (result?.ServerError != null)
            {
                return result.ServerError.Category ==
                       ClientServerErrorCategory.Authentication
                    ? ClientConnectionRecoveryResultKind.Authentication
                    : ClientConnectionRecoveryResultKind.Policy;
            }

            switch (result?.Failure?.Kind)
            {
                case ClientGatewayFailureKind.Timeout:
                case ClientGatewayFailureKind.Transport:
                    return ClientConnectionRecoveryResultKind.Transport;
                case ClientGatewayFailureKind.MalformedResponse:
                case ClientGatewayFailureKind.ResponseTooLarge:
                    return ClientConnectionRecoveryResultKind.Protocol;
                case ClientGatewayFailureKind.Stopped:
                    return ClientConnectionRecoveryResultKind.Stopped;
                default:
                    return ClientConnectionRecoveryResultKind.Policy;
            }
        }

        /// <summary>把 Gameplay failure 映射为 recovery terminal。</summary>
        internal ClientConnectionRecoveryResultKind MapRecoveryGameplay<T>(
            ClientGameplayResult<T> result)
            where T : class
        {
            if (result?.ServerError != null)
            {
                return result.ServerError.Code >= 100 &&
                       result.ServerError.Code <= 103
                    ? ClientConnectionRecoveryResultKind.Authentication
                    : ClientConnectionRecoveryResultKind.Policy;
            }

            switch (result?.Failure)
            {
                case ClientGameplayFailureKind.Timeout:
                case ClientGameplayFailureKind.Disconnected:
                    return ClientConnectionRecoveryResultKind.Transport;
                case ClientGameplayFailureKind.Protocol:
                    return ClientConnectionRecoveryResultKind.Protocol;
                default:
                    return ClientConnectionRecoveryResultKind.Policy;
            }
        }

        /// <summary>把 world flow failure 映射为 recovery terminal。</summary>
        internal ClientConnectionRecoveryResultKind MapRecoveryFlow(
            ClientWorldFlowFailure failure)
        {
            switch (failure)
            {
                case ClientWorldFlowFailure.Transport:
                case ClientWorldFlowFailure.CommitUnknown:
                    return ClientConnectionRecoveryResultKind.Transport;
                case ClientWorldFlowFailure.Protocol:
                    return ClientConnectionRecoveryResultKind.Protocol;
                case ClientWorldFlowFailure.Stopped:
                    return ClientConnectionRecoveryResultKind.Stopped;
                default:
                    return ClientConnectionRecoveryResultKind.Policy;
            }
        }
    }
}
