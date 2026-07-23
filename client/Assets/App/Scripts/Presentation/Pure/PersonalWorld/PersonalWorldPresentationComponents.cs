using System;
using System.Collections.Generic;
using IHomeland.Client.Application.Contracts;
using IHomeland.Client.Application.Gameplay;
using IHomeland.Client.Application.Session;
using IHomeland.Client.Application.World;
using IHomeland.Client.Presentation.Navigation;

namespace IHomeland.Client.Presentation.PersonalWorld
{
    /// <summary>
    /// 唯一拥有 presentation generation、active intent、failure scope 与 View State commit。
    /// </summary>
    internal sealed class PersonalWorldPresentationState
    {
        /// <summary>获取或设置最近不可变 View State。</summary>
        internal ClientPersonalWorldViewState ViewState { get; set; } =
            ClientPersonalWorldViewState.Inactive;

        /// <summary>获取或设置 current single-flight intent。</summary>
        internal ClientPersonalWorldIntent ActiveIntent { get; set; }

        /// <summary>获取或设置最近低敏失败。</summary>
        internal ClientPersonalWorldFailure Failure { get; set; }

        /// <summary>获取或设置失败所属 intent。</summary>
        internal ClientPersonalWorldIntent FailureIntent { get; set; }

        /// <summary>获取或设置单调 presentation generation。</summary>
        internal long PresentationGeneration { get; set; }

        /// <summary>获取或设置 lifecycle running 标志。</summary>
        internal bool Running { get; set; }

        /// <summary>获取或设置稳定 connection-lost 标志。</summary>
        internal bool ConnectionLost { get; set; }

        /// <summary>获取或设置 startup restore 标志。</summary>
        internal bool RestoringSession { get; set; }

        /// <summary>获取或设置待消费 Session invalidation generation。</summary>
        internal long PendingSessionInvalidationGeneration { get; set; }

        /// <summary>获取或设置 Login 收敛 transaction 所有权。</summary>
        internal bool ReturningToLogin { get; set; }

        /// <summary>尝试取得当前 presentation generation 的唯一 action intent。</summary>
        internal bool TryBeginIntent(
            ClientPersonalWorldIntent intent,
            out long generation)
        {
            if (!Running || ActiveIntent != ClientPersonalWorldIntent.None)
            {
                generation = 0;
                return false;
            }

            ActiveIntent = intent;
            Failure = ClientPersonalWorldFailure.None;
            FailureIntent = ClientPersonalWorldIntent.None;
            generation = PresentationGeneration;
            return true;
        }

        /// <summary>判断异步 completion 仍属于 current generation。</summary>
        internal bool IsCurrent(long generation)
        {
            return Running && PresentationGeneration == generation;
        }
    }

    /// <summary>保存纯 View State projector 的完整不可变输入。</summary>
    internal sealed class PersonalWorldProjectionInput
    {
        /// <summary>创建一次 owner snapshots 的冻结输入。</summary>
        internal PersonalWorldProjectionInput(
            long presentationGeneration,
            ClientPersonalWorldPhase phase,
            ClientPersonalWorldIntent activeIntent,
            ClientPersonalWorldFailure failure,
            bool connectionLost,
            ClientSessionSnapshot session,
            ClientWorldFlowSnapshot worldFlow,
            ClientPersonalWorldServiceSnapshot world,
            ClientVisitSessionServiceSnapshot visit,
            ClientConnectionRecoverySnapshot recovery,
            ClientWorldSceneSnapshot scene)
        {
            PresentationGeneration = presentationGeneration;
            Phase = phase;
            ActiveIntent = activeIntent;
            Failure = failure;
            ConnectionLost = connectionLost;
            Session = session;
            WorldFlow = worldFlow ?? throw new ArgumentNullException(nameof(worldFlow));
            World = world ?? throw new ArgumentNullException(nameof(world));
            Visit = visit ?? throw new ArgumentNullException(nameof(visit));
            Recovery = recovery ?? throw new ArgumentNullException(nameof(recovery));
            Scene = scene ?? throw new ArgumentNullException(nameof(scene));
        }

        /// <summary>获取 presentation generation。</summary>
        internal long PresentationGeneration { get; }

        /// <summary>获取已决议产品 phase。</summary>
        internal ClientPersonalWorldPhase Phase { get; }

        /// <summary>获取 active intent。</summary>
        internal ClientPersonalWorldIntent ActiveIntent { get; }

        /// <summary>获取 presentation failure。</summary>
        internal ClientPersonalWorldFailure Failure { get; }

        /// <summary>获取 connection-lost 标志。</summary>
        internal bool ConnectionLost { get; }

        /// <summary>获取可选 current Session。</summary>
        internal ClientSessionSnapshot Session { get; }

        /// <summary>获取 world target owner snapshot。</summary>
        internal ClientWorldFlowSnapshot WorldFlow { get; }

        /// <summary>获取 PersonalWorld projection snapshot。</summary>
        internal ClientPersonalWorldServiceSnapshot World { get; }

        /// <summary>获取 VisitSession projection snapshot。</summary>
        internal ClientVisitSessionServiceSnapshot Visit { get; }

        /// <summary>获取 recovery owner snapshot。</summary>
        internal ClientConnectionRecoverySnapshot Recovery { get; }

        /// <summary>获取 Scene transaction snapshot。</summary>
        internal ClientWorldSceneSnapshot Scene { get; }
    }

    /// <summary>集中把 Application 与 route 的封闭失败映射为低敏页面失败。</summary>
    internal static class PersonalWorldFailureMapper
    {
        /// <summary>将启动 restore 终态映射为页面失败。</summary>
        /// <param name="outcome">可为空的 production restore 终态。</param>
        /// <returns>冷启动无 record 不显示错误，其余失败使用封闭类别。</returns>
        internal static ClientPersonalWorldFailure FromRestore(
            ClientSessionRestoreOutcome? outcome)
        {
            switch (outcome)
            {
                case null:
                case ClientSessionRestoreOutcome.NotAvailable:
                case ClientSessionRestoreOutcome.Restored:
                    return ClientPersonalWorldFailure.None;
                case ClientSessionRestoreOutcome.Rejected:
                    return ClientPersonalWorldFailure.Unauthenticated;
                case ClientSessionRestoreOutcome.Unresolved:
                    return ClientPersonalWorldFailure.Transport;
                case ClientSessionRestoreOutcome.StorageFailure:
                    return ClientPersonalWorldFailure.SecureStorage;
                case ClientSessionRestoreOutcome.ProfileInUse:
                    return ClientPersonalWorldFailure.ProfileInUse;
                case ClientSessionRestoreOutcome.Stopped:
                    return ClientPersonalWorldFailure.Stopped;
                default:
                    return ClientPersonalWorldFailure.Internal;
            }
        }

        /// <summary>将恢复 owner 终态映射为页面失败。</summary>
        /// <param name="result">Generation-bound 恢复结果。</param>
        /// <returns>封闭页面失败。</returns>
        internal static ClientPersonalWorldFailure FromRecovery(
            ClientConnectionRecoveryResultKind result)
        {
            switch (result)
            {
                case ClientConnectionRecoveryResultKind.Succeeded:
                case ClientConnectionRecoveryResultKind.ReturningOwnWorld:
                    return ClientPersonalWorldFailure.None;
                case ClientConnectionRecoveryResultKind.Transport:
                case ClientConnectionRecoveryResultKind.Deadline:
                case ClientConnectionRecoveryResultKind.None:
                    return ClientPersonalWorldFailure.Transport;
                case ClientConnectionRecoveryResultKind.Protocol:
                    return ClientPersonalWorldFailure.ProtocolIncompatible;
                case ClientConnectionRecoveryResultKind.Authentication:
                    return ClientPersonalWorldFailure.Unauthenticated;
                case ClientConnectionRecoveryResultKind.Policy:
                    return ClientPersonalWorldFailure.Permission;
                case ClientConnectionRecoveryResultKind.Stopped:
                    return ClientPersonalWorldFailure.Stopped;
                default:
                    return ClientPersonalWorldFailure.Internal;
            }
        }

        /// <summary>将 world-flow failure 映射为页面失败。</summary>
        /// <param name="failure">Coordinator 的封闭失败。</param>
        /// <returns>封闭页面失败。</returns>
        internal static ClientPersonalWorldFailure FromWorld(
            ClientWorldFlowFailure failure)
        {
            switch (failure)
            {
                case ClientWorldFlowFailure.None:
                    return ClientPersonalWorldFailure.None;
                case ClientWorldFlowFailure.Policy:
                    return ClientPersonalWorldFailure.Permission;
                case ClientWorldFlowFailure.CallerCancelled:
                    return ClientPersonalWorldFailure.CallerCancelled;
                case ClientWorldFlowFailure.Transport:
                    return ClientPersonalWorldFailure.Transport;
                case ClientWorldFlowFailure.Rejected:
                    return ClientPersonalWorldFailure.Validation;
                case ClientWorldFlowFailure.Protocol:
                    return ClientPersonalWorldFailure.ProtocolIncompatible;
                case ClientWorldFlowFailure.CommitUnknown:
                    return ClientPersonalWorldFailure.CommitUnknown;
                case ClientWorldFlowFailure.Stopped:
                    return ClientPersonalWorldFailure.Stopped;
                case ClientWorldFlowFailure.InviteUnavailable:
                    return ClientPersonalWorldFailure.InviteUnavailable;
                default:
                    return ClientPersonalWorldFailure.Internal;
            }
        }

        /// <summary>将 gateway 结果映射为页面失败。</summary>
        /// <typeparam name="T">Gateway 成功投影类型。</typeparam>
        /// <param name="result">强类型 gateway 结果。</param>
        /// <returns>封闭页面失败。</returns>
        internal static ClientPersonalWorldFailure FromGateway<T>(
            ClientGatewayResult<T> result)
        {
            if (result.ServerError != null)
            {
                switch (result.ServerError.Category)
                {
                    case ClientServerErrorCategory.Protocol:
                        return ClientPersonalWorldFailure.ProtocolIncompatible;
                    case ClientServerErrorCategory.Authentication:
                        return ClientPersonalWorldFailure.Unauthenticated;
                    case ClientServerErrorCategory.Validation:
                        return ClientPersonalWorldFailure.Validation;
                    case ClientServerErrorCategory.Conflict:
                        return ClientPersonalWorldFailure.RevisionConflict;
                    case ClientServerErrorCategory.NotFound:
                        return ClientPersonalWorldFailure.NotFound;
                    case ClientServerErrorCategory.RateLimit:
                        return ClientPersonalWorldFailure.RateLimited;
                    case ClientServerErrorCategory.Dependency:
                        return ClientPersonalWorldFailure.DependencyUnavailable;
                    default:
                        return ClientPersonalWorldFailure.Internal;
                }
            }

            if (result.Failure == null)
            {
                return ClientPersonalWorldFailure.None;
            }

            switch (result.Failure.Kind)
            {
                case ClientGatewayFailureKind.CallerCancelled:
                    return ClientPersonalWorldFailure.CallerCancelled;
                case ClientGatewayFailureKind.Timeout:
                case ClientGatewayFailureKind.Transport:
                    return ClientPersonalWorldFailure.Transport;
                case ClientGatewayFailureKind.Stopped:
                    return ClientPersonalWorldFailure.Stopped;
                case ClientGatewayFailureKind.MalformedResponse:
                case ClientGatewayFailureKind.ResponseTooLarge:
                    return ClientPersonalWorldFailure.ProtocolIncompatible;
                case ClientGatewayFailureKind.SecureStorage:
                    return ClientPersonalWorldFailure.SecureStorage;
                case ClientGatewayFailureKind.SecureStorageProfileInUse:
                    return ClientPersonalWorldFailure.ProfileInUse;
                default:
                    return ClientPersonalWorldFailure.Internal;
            }
        }

        /// <summary>将 gameplay 本地失败映射为页面失败。</summary>
        /// <param name="failure">可选 gameplay 失败；为空表示服务端拒绝。</param>
        /// <returns>封闭页面失败。</returns>
        internal static ClientPersonalWorldFailure FromGameplay(
            ClientGameplayFailureKind? failure)
        {
            if (!failure.HasValue)
            {
                return ClientPersonalWorldFailure.Validation;
            }

            switch (failure.Value)
            {
                case ClientGameplayFailureKind.Policy:
                    return ClientPersonalWorldFailure.Permission;
                case ClientGameplayFailureKind.CallerCancelled:
                    return ClientPersonalWorldFailure.CallerCancelled;
                case ClientGameplayFailureKind.Timeout:
                case ClientGameplayFailureKind.Disconnected:
                case ClientGameplayFailureKind.Backpressure:
                    return ClientPersonalWorldFailure.Transport;
                case ClientGameplayFailureKind.Protocol:
                    return ClientPersonalWorldFailure.ProtocolIncompatible;
                default:
                    return ClientPersonalWorldFailure.Internal;
            }
        }

        /// <summary>将 route transition 失败映射为页面失败。</summary>
        /// <param name="result">Router 返回的稳定结果。</param>
        /// <returns>封闭页面失败。</returns>
        internal static ClientPersonalWorldFailure FromUi(
            ClientUiTransitionResult result)
        {
            switch (result.Code)
            {
                case ClientUiTransitionCode.Cancelled:
                    return ClientPersonalWorldFailure.CallerCancelled;
                case ClientUiTransitionCode.Stopped:
                    return ClientPersonalWorldFailure.Stopped;
                case ClientUiTransitionCode.Overloaded:
                case ClientUiTransitionCode.PolicyRejected:
                case ClientUiTransitionCode.InvalidSceneGeneration:
                    return ClientPersonalWorldFailure.Permission;
                default:
                    return ClientPersonalWorldFailure.Internal;
            }
        }
    }

    /// <summary>纯派生 Login/Shell/WorldVisit/HUD View State，不执行 Unity 或 transport 副作用。</summary>
    internal sealed class PersonalWorldViewStateProjector
    {
        /// <summary>从冻结 owner snapshots 创建完整不可变 View State。</summary>
        internal ClientPersonalWorldViewState Project(
            PersonalWorldProjectionInput input)
        {
            if (input == null)
            {
                throw new ArgumentNullException(nameof(input));
            }

            var hasSession = input.Session != null;
            var currentWorld = input.World.CurrentWorld;
            var currentVisit = input.Visit.Current;
            var isOwner = input.WorldFlow.State == ClientWorldFlowState.OwnWorld;
            var isVisitor =
                input.WorldFlow.State == ClientWorldFlowState.Visiting &&
                currentVisit?.Role == ClientVisitRole.Visitor &&
                string.Equals(
                    currentVisit.VisitSessionID,
                    input.WorldFlow.VisitSessionID,
                    StringComparison.Ordinal);
            var hasOpenOwnerVisit =
                isOwner &&
                currentVisit?.Role == ClientVisitRole.Owner &&
                currentVisit.Lifecycle == ClientVisitLifecycle.Open;

            var visitors = new List<string>();
            if (hasOpenOwnerVisit)
            {
                foreach (var visitor in currentVisit.Visitors)
                {
                    visitors.Add(visitor.PlayerID);
                }
            }

            var relevantInvites = hasOpenOwnerVisit
                ? input.Visit.OutgoingInvites
                : input.Visit.Invites;
            var invites = new List<ClientVisitInviteViewState>();
            foreach (var invite in relevantInvites)
            {
                if (invite.State == ClientVisitInviteState.Pending)
                {
                    invites.Add(new ClientVisitInviteViewState(
                        invite.InviteID,
                        invite.VisitSessionID,
                        invite.OwnerPlayerID,
                        invite.TargetVisitorID,
                        invite.ExpiresAtMilliseconds));
                }
            }

            var hudVisible =
                currentWorld?.Assignment != null &&
                (input.Phase == ClientPersonalWorldPhase.OwnWorld ||
                 input.Phase == ClientPersonalWorldPhase.Visiting ||
                 input.Phase == ClientPersonalWorldPhase.RecoveringControl);
            var failure = input.Failure != ClientPersonalWorldFailure.None
                ? input.Failure
                : PersonalWorldFailureMapper.FromWorld(input.WorldFlow.Failure);
            var idle =
                input.ActiveIntent == ClientPersonalWorldIntent.None &&
                !input.ConnectionLost &&
                input.Recovery.Phase == ClientConnectionRecoveryPhase.Idle;
            var actions = new ClientWorldVisitActionState(
                idle && isOwner && currentVisit == null,
                idle && hasOpenOwnerVisit,
                idle && hasOpenOwnerVisit && invites.Count > 0,
                idle && hasOpenOwnerVisit && visitors.Count > 0,
                idle && hasOpenOwnerVisit,
                idle && isOwner && currentVisit == null && invites.Count > 0,
                idle && isVisitor);
            var playerID = hasSession
                ? input.World.PrimaryWorld?.OwnerPlayerID ?? string.Empty
                : string.Empty;

            return new ClientPersonalWorldViewState(
                input.PresentationGeneration,
                input.WorldFlow.TargetGeneration,
                input.Scene.SceneGeneration,
                input.Phase,
                input.ActiveIntent,
                new ClientLoginViewState(
                    !hasSession,
                    input.ActiveIntent == ClientPersonalWorldIntent.Login ||
                    input.ActiveIntent == ClientPersonalWorldIntent.Register,
                    failure),
                new ClientShellViewState(
                    hasSession && !hudVisible,
                    playerID,
                    hasSession
                        ? input.Session.Account.DisplayName
                        : string.Empty,
                    input.Phase,
                    failure),
                new ClientWorldVisitViewState(
                    currentVisit != null || input.Visit.Invites.Count > 0,
                    isOwner,
                    isVisitor,
                    hasOpenOwnerVisit || isVisitor
                        ? currentVisit.VisitSessionID
                        : string.Empty,
                    hasOpenOwnerVisit || isVisitor ? currentVisit.Revision : 0,
                    hasOpenOwnerVisit || isVisitor
                        ? currentVisit.ExpiresAtMilliseconds
                        : 0,
                    hasOpenOwnerVisit || isVisitor
                        ? currentVisit.OwnerGraceExpiresAtMilliseconds
                        : 0,
                    visitors,
                    invites,
                    actions),
                new ClientWorldHudViewState(
                    hudVisible,
                    isOwner,
                    isVisitor,
                    currentWorld?.PersonalWorldID,
                    currentWorld?.Assignment?.WorldInstanceID,
                    currentVisit?.VisitSessionID));
        }

    }
}
