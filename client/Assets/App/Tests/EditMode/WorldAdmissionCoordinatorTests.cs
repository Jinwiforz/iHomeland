using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using Google.Protobuf;
using IHomeland.Client.Application.Gameplay;
using IHomeland.Client.Application.Session;
using IHomeland.Client.Application.World;
using IHomeland.Client.Application.Configuration;
using IHomeland.Client.Foundation.Lifetime;
using IHomeland.Client.Foundation.Time;
using IHomeland.Client.Application.Contracts;
using IHomeland.Client.Application.Ports;
using IHomeland.Client.Infrastructure.Http;
using IHomeland.Client.Infrastructure.Tcp;
using IHomeland.Client.Infrastructure.Time;
using IHomeland.Protocol.Common.V1;
using IHomeland.Protocol.Session.V1;
using IHomeland.Protocol.Visit.V1;
using IHomeland.Protocol.World.V1;
using NUnit.Framework;

namespace IHomeland.Client.Tests.EditMode
{
    /// <summary>
    /// 使用确定性 HTTP 与内存 duplex stream 验证 world target coordinator 的关键竞态。
    /// </summary>
    public sealed class WorldAdmissionCoordinatorTests
    {
        /// <summary>验证 bootstrap 未完成时第二个 enter intent 被有界拒绝。</summary>
        [Test]
        public async Task ConcurrentEnterOwnWorldKeepsSingleIntent()
        {
            var fixture = await CoordinatorFixture.CreateAsync(blockBootstrap: true);
            var first = fixture.Coordinator.EnterOwnWorldAsync(CancellationToken.None);
            await fixture.Api.WaitForBootstrapCallAsync();

            Assert.That(
                await fixture.Coordinator.EnterOwnWorldAsync(CancellationToken.None),
                Is.False);
            fixture.Api.CompleteBootstrap();
            Assert.That(await first, Is.True);
            Assert.That(fixture.Api.BootstrapCalls, Is.EqualTo(1));
            Assert.That(fixture.Coordinator.Snapshot.State, Is.EqualTo(ClientWorldFlowState.OwnWorld));
            await fixture.StopAsync();
        }

        /// <summary>验证不存在或已过期的 inbox invite 会形成可见拒绝，且不会发送 HTTP accept。</summary>
        [Test]
        public async Task UnavailableInviteRejectsWithoutHttpAccept()
        {
            var fixture = await CoordinatorFixture.CreateAsync(blockBootstrap: false);
            Assert.That(await fixture.Coordinator.EnterOwnWorldAsync(CancellationToken.None), Is.True);

            Assert.That(
                await fixture.Coordinator.JoinVisitAsync(
                    "visit_expired",
                    "invite_expired",
                    CancellationToken.None),
                Is.False);
            Assert.That(
                fixture.Coordinator.Snapshot.State,
                Is.EqualTo(ClientWorldFlowState.OwnWorld));
            Assert.That(
                fixture.Coordinator.Snapshot.Failure,
                Is.EqualTo(ClientWorldFlowFailure.InviteUnavailable));
            Assert.That(fixture.Api.AcceptInviteCalls, Is.Zero);

            await fixture.StopAsync();
        }

        /// <summary>验证服务端明确判定邀请失效时只退役对应 identity，并保持 OwnWorld。</summary>
        [TestCase(2101)]
        [TestCase(2102)]
        [TestCase(2104)]
        [TestCase(2105)]
        public async Task AuthoritativeAcceptRejectionRetiresInvite(int errorCode)
        {
            var fixture = await CoordinatorFixture.CreateAsync(blockBootstrap: false);
            Assert.That(await fixture.Coordinator.EnterOwnWorldAsync(CancellationToken.None), Is.True);
            Assert.That(
                fixture.VisitService.ApplyInvite(CoordinatorFixture.Invite()),
                Is.EqualTo(ClientProjectionApplyResult.Applied));
            fixture.Api.AcceptInviteErrorCode = errorCode;

            Assert.That(
                await fixture.Coordinator.JoinVisitAsync(
                    "visit_one",
                    "invite_one",
                    CancellationToken.None),
                Is.False);

            Assert.That(fixture.Coordinator.Snapshot.State, Is.EqualTo(ClientWorldFlowState.OwnWorld));
            Assert.That(
                fixture.Coordinator.Snapshot.Failure,
                Is.EqualTo(ClientWorldFlowFailure.InviteUnavailable));
            Assert.That(fixture.VisitService.Snapshot.Invites, Is.Empty);
            Assert.That(fixture.Factory.ConnectionCount, Is.EqualTo(1));
            await fixture.StopAsync();
        }

        /// <summary>验证容量拒绝不代表邀请失效，客户端保留 identity 供状态变化后重试。</summary>
        [Test]
        public async Task CapacityRejectionKeepsInvite()
        {
            var fixture = await CoordinatorFixture.CreateAsync(blockBootstrap: false);
            Assert.That(await fixture.Coordinator.EnterOwnWorldAsync(CancellationToken.None), Is.True);
            Assert.That(
                fixture.VisitService.ApplyInvite(CoordinatorFixture.Invite()),
                Is.EqualTo(ClientProjectionApplyResult.Applied));
            fixture.Api.AcceptInviteErrorCode = 2103;

            Assert.That(
                await fixture.Coordinator.JoinVisitAsync(
                    "visit_one",
                    "invite_one",
                    CancellationToken.None),
                Is.False);

            Assert.That(fixture.Coordinator.Snapshot.State, Is.EqualTo(ClientWorldFlowState.OwnWorld));
            Assert.That(fixture.Coordinator.Snapshot.Failure, Is.EqualTo(ClientWorldFlowFailure.Rejected));
            Assert.That(fixture.VisitService.Snapshot.Invites, Has.Count.EqualTo(1));
            await fixture.StopAsync();
        }

        /// <summary>验证返回 own-world 失败后保持 ReturningOwnWorld，显式 retry 才可恢复。</summary>
        [Test]
        public async Task ReturnFailureCannotReviveVisitorTarget()
        {
            var fixture = await CoordinatorFixture.CreateAsync(blockBootstrap: false);
            Assert.That(await fixture.Coordinator.EnterOwnWorldAsync(CancellationToken.None), Is.True);
            Assert.That(
                fixture.VisitService.ApplyInvite(CoordinatorFixture.Invite()),
                Is.EqualTo(ClientProjectionApplyResult.Applied));
            var joined = await fixture.Coordinator.JoinVisitAsync(
                "visit_one",
                "invite_one",
                CancellationToken.None);
            Assert.That(
                joined,
                Is.True,
                $"state={fixture.Coordinator.Snapshot.State} failure={fixture.Coordinator.Snapshot.Failure}");
            Assert.That(fixture.Coordinator.Snapshot.State, Is.EqualTo(ClientWorldFlowState.Visiting));
            Assert.That(fixture.VisitService.Snapshot.Invites, Is.Empty);

            fixture.Api.FailBootstrap = true;
            Assert.That(await fixture.Coordinator.LeaveVisitAsync(CancellationToken.None), Is.False);
            Assert.That(
                fixture.Coordinator.Snapshot.State,
                Is.EqualTo(ClientWorldFlowState.ReturningOwnWorld));
            Assert.That(fixture.Coordinator.Snapshot.VisitSessionID, Is.Null);
            Assert.That(fixture.Gameplay.Snapshot.State, Is.EqualTo(ClientGameplayChannelState.Ready));

            fixture.Api.FailBootstrap = false;
            Assert.That(await fixture.Coordinator.RetryReturnAsync(CancellationToken.None), Is.True);
            Assert.That(fixture.Coordinator.Snapshot.State, Is.EqualTo(ClientWorldFlowState.OwnWorld));
            Assert.That(fixture.Factory.ConnectionCount, Is.EqualTo(3));
            await fixture.StopAsync();
        }

        /// <summary>验证 Owner mutation 逐次使用 current revision，并提交服务端返回的完整 replacement。</summary>
        [Test]
        public async Task OwnerActionsAdvanceOnlyThroughAuthoritativeRevisions()
        {
            var fixture = await CoordinatorFixture.CreateAsync(blockBootstrap: false);
            Assert.That(await fixture.Coordinator.EnterOwnWorldAsync(CancellationToken.None), Is.True);

            Assert.That((await fixture.VisitService.OpenAsync(CancellationToken.None)).IsSuccess, Is.True);
            Assert.That(fixture.VisitService.Snapshot.Current.Revision, Is.EqualTo(2));
            Assert.That(
                (await fixture.VisitService.CreateInviteAsync(
                    "player_visitor",
                    60_000,
                    CancellationToken.None)).IsSuccess,
                Is.True);
            Assert.That(fixture.VisitService.Snapshot.Current.Revision, Is.EqualTo(3));
            Assert.That(fixture.VisitService.Snapshot.Invites, Is.Empty);
            Assert.That(fixture.VisitService.Snapshot.OutgoingInvites, Has.Count.EqualTo(1));
            Assert.That(
                fixture.VisitService.Snapshot.OutgoingInvites[0].InviteID,
                Is.EqualTo("invite_created"));
            Assert.That(
                fixture.VisitService.Snapshot.OutgoingInvites[0].TargetVisitorID,
                Is.EqualTo("player_visitor"));
            Assert.That(
                (await fixture.VisitService.KickAsync(
                    "player_visitor",
                    CancellationToken.None)).IsSuccess,
                Is.True);
            Assert.That(fixture.VisitService.Snapshot.Current.Revision, Is.EqualTo(4));
            Assert.That((await fixture.VisitService.CloseAsync(CancellationToken.None)).IsSuccess, Is.True);
            Assert.That(fixture.VisitService.Snapshot.Current, Is.Null);
            Assert.That(fixture.VisitService.Snapshot.OutgoingInvites, Is.Empty);

            await fixture.StopAsync();
        }

        /// <summary>验证撤销后同一 target 的新 invite 只保留新 identity，旧按钮对象不能再次提交。</summary>
        [Test]
        public async Task OwnerRevokeThenReinviteReplacesOutgoingIdentity()
        {
            var fixture = await CoordinatorFixture.CreateAsync(blockBootstrap: false);
            Assert.That(await fixture.Coordinator.EnterOwnWorldAsync(CancellationToken.None), Is.True);
            Assert.That((await fixture.VisitService.OpenAsync(CancellationToken.None)).IsSuccess, Is.True);
            Assert.That(
                (await fixture.VisitService.CreateInviteAsync(
                    "player_visitor",
                    60_000,
                    CancellationToken.None)).IsSuccess,
                Is.True);
            var revokedInviteID = fixture.VisitService.Snapshot.OutgoingInvites[0].InviteID;

            Assert.That(
                (await fixture.VisitService.RevokeInviteAsync(
                    revokedInviteID,
                    CancellationToken.None)).IsSuccess,
                Is.True);
            Assert.That(fixture.VisitService.Snapshot.OutgoingInvites, Is.Empty);
            Assert.That(
                (await fixture.VisitService.CreateInviteAsync(
                    "player_visitor",
                    60_000,
                    CancellationToken.None)).IsSuccess,
                Is.True);

            Assert.That(fixture.VisitService.Snapshot.OutgoingInvites, Has.Count.EqualTo(1));
            Assert.That(
                fixture.VisitService.Snapshot.OutgoingInvites[0].InviteID,
                Is.Not.EqualTo(revokedInviteID));
            Assert.That(fixture.VisitService.Snapshot.Current.Revision, Is.EqualTo(5));
            await fixture.StopAsync();
        }

        /// <summary>验证WSS断代立即移除stale outgoing invite且不丢弃健康Owner gameplay snapshot。</summary>
        [Test]
        public async Task ControlInvalidationRetiresOutgoingInviteAndKeepsOwnerSnapshot()
        {
            var fixture = await CoordinatorFixture.CreateAsync(blockBootstrap: false);
            Assert.That(await fixture.Coordinator.EnterOwnWorldAsync(CancellationToken.None), Is.True);
            Assert.That((await fixture.VisitService.OpenAsync(CancellationToken.None)).IsSuccess, Is.True);
            Assert.That(
                (await fixture.VisitService.CreateInviteAsync(
                    "player_other",
                    60_000,
                    CancellationToken.None)).IsSuccess,
                Is.True);
            Assert.That(fixture.VisitService.Snapshot.OutgoingInvites, Has.Count.EqualTo(1));
            var current = fixture.VisitService.Snapshot.Current;
            var operations = (IClientConnectionRecoveryOperations)fixture.Coordinator;
            Assert.That(operations.TryCaptureTarget(out var descriptor), Is.True);
            Assert.That(descriptor.VisitSessionID, Is.EqualTo("visit_one"));

            operations.InvalidateControlOnlyState();

            Assert.That(fixture.VisitService.Snapshot.Current, Is.SameAs(current));
            Assert.That(fixture.VisitService.Snapshot.OutgoingInvites, Is.Empty);
            Assert.That(fixture.VisitService.Snapshot.NeedsRefresh, Is.True);
            Assert.That(
                await operations.ReconcileControlAsync(descriptor, CancellationToken.None),
                Is.EqualTo(ClientConnectionRecoveryResultKind.Succeeded));
            Assert.That(fixture.VisitService.Snapshot.NeedsRefresh, Is.False);
            Assert.That(fixture.Factory.ConnectionCount, Is.EqualTo(1));
            await fixture.StopAsync();
        }

        /// <summary>验证 member snapshot 会退役已经被接受的 outgoing invite，旧撤销按钮不再暴露。</summary>
        [Test]
        public async Task OwnerMemberSnapshotRetiresConsumedOutgoingInvite()
        {
            var fixture = await CoordinatorFixture.CreateAsync(blockBootstrap: false);
            Assert.That(await fixture.Coordinator.EnterOwnWorldAsync(CancellationToken.None), Is.True);
            Assert.That((await fixture.VisitService.OpenAsync(CancellationToken.None)).IsSuccess, Is.True);
            Assert.That(
                (await fixture.VisitService.CreateInviteAsync(
                    "player_visitor",
                    60_000,
                    CancellationToken.None)).IsSuccess,
                Is.True);
            Assert.That(fixture.VisitService.Snapshot.OutgoingInvites, Has.Count.EqualTo(1));

            var memberSnapshot = new VisitSessionSnapshot
            {
                VisitSessionId = "visit_one",
                OwnerPlayerId = "player_owner",
                Assignment = new WorldAssignment
                {
                    PersonalWorldId = "world_self",
                    WorldInstanceId = "instance_self",
                    Endpoint = new Endpoint
                    {
                        Channel = TransportChannel.TlsTcp,
                        Host = "127.0.0.1",
                        Port = 4433,
                    },
                    Generation = 1,
                    LeaseExpiresAtMs = 90_000,
                },
                Lifecycle = VisitLifecycle.Open,
                Revision = 4,
                Capacity = 4,
                CreatedAtMs = 1_000,
                ExpiresAtMs = 90_000,
            };
            memberSnapshot.Visitors.Add(new VisitVisitorSummary
            {
                PlayerId = "player_visitor",
                State = VisitMembershipState.Joined,
            });

            Assert.That(
                fixture.VisitService.ApplySnapshot(memberSnapshot, ClientVisitRole.Owner),
                Is.EqualTo(ClientProjectionApplyResult.Applied));
            Assert.That(fixture.VisitService.Snapshot.OutgoingInvites, Is.Empty);
            await fixture.StopAsync();
        }

        /// <summary>验证 Visitor 的 Owner-only action 在写入 channel 前失败，leave 仍使用 current revision。</summary>
        [Test]
        public async Task VisitorCannotSubmitOwnerActionsAndCanLeaveCurrentRevision()
        {
            var fixture = await CoordinatorFixture.CreateAsync(blockBootstrap: false);
            Assert.That(await fixture.Coordinator.EnterOwnWorldAsync(CancellationToken.None), Is.True);
            Assert.That(
                fixture.VisitService.ApplyInvite(CoordinatorFixture.Invite()),
                Is.EqualTo(ClientProjectionApplyResult.Applied));
            Assert.That(
                await fixture.Coordinator.JoinVisitAsync(
                    "visit_one",
                    "invite_one",
                    CancellationToken.None),
                Is.True);
            var writesBeforePolicyChecks = fixture.Factory.OperationCount;

            Assert.That(
                (await fixture.VisitService.CreateInviteAsync(
                    "player_other",
                    60_000,
                    CancellationToken.None)).Failure,
                Is.EqualTo(ClientGameplayFailureKind.Policy));
            Assert.That(
                (await fixture.VisitService.KickAsync(
                    "player_other",
                    CancellationToken.None)).Failure,
                Is.EqualTo(ClientGameplayFailureKind.Policy));
            Assert.That(
                (await fixture.VisitService.CloseAsync(CancellationToken.None)).Failure,
                Is.EqualTo(ClientGameplayFailureKind.Policy));
            Assert.That(fixture.Factory.OperationCount, Is.EqualTo(writesBeforePolicyChecks));

            Assert.That(await fixture.Coordinator.LeaveVisitAsync(CancellationToken.None), Is.True);
            Assert.That(fixture.Coordinator.Snapshot.State, Is.EqualTo(ClientWorldFlowState.OwnWorld));
            await fixture.StopAsync();
        }

        /// <summary>验证接受、离开后同一 Owner 的新 invite 使用新 identity，再次进入仍是 Visitor target。</summary>
        [Test]
        public async Task LeaveThenAcceptReplacementInviteEntersVisitAgain()
        {
            var fixture = await CoordinatorFixture.CreateAsync(blockBootstrap: false);
            Assert.That(await fixture.Coordinator.EnterOwnWorldAsync(CancellationToken.None), Is.True);
            Assert.That(
                fixture.VisitService.ApplyInvite(CoordinatorFixture.Invite("invite_first", 1)),
                Is.EqualTo(ClientProjectionApplyResult.Applied));
            Assert.That(
                await fixture.Coordinator.JoinVisitAsync(
                    "visit_one",
                    "invite_first",
                    CancellationToken.None),
                Is.True);
            Assert.That(await fixture.Coordinator.LeaveVisitAsync(CancellationToken.None), Is.True);
            Assert.That(fixture.Coordinator.Snapshot.State, Is.EqualTo(ClientWorldFlowState.OwnWorld));

            Assert.That(
                fixture.VisitService.ApplyInvite(CoordinatorFixture.Invite("invite_second", 5)),
                Is.EqualTo(ClientProjectionApplyResult.Applied));
            Assert.That(fixture.VisitService.Snapshot.Invites, Has.Count.EqualTo(1));
            Assert.That(fixture.VisitService.Snapshot.Invites[0].InviteID, Is.EqualTo("invite_second"));
            Assert.That(
                await fixture.Coordinator.JoinVisitAsync(
                    "visit_one",
                    "invite_second",
                    CancellationToken.None),
                Is.True);

            Assert.That(fixture.Api.AcceptInviteCalls, Is.EqualTo(2));
            Assert.That(fixture.Factory.ConnectionCount, Is.EqualTo(4));
            Assert.That(fixture.Coordinator.Snapshot.State, Is.EqualTo(ClientWorldFlowState.Visiting));
            Assert.That(fixture.Coordinator.Snapshot.VisitSessionID, Is.EqualTo("visit_one"));
            Assert.That(fixture.VisitService.Snapshot.Current.Role, Is.EqualTo(ClientVisitRole.Visitor));
            Assert.That(fixture.VisitService.Snapshot.Invites, Is.Empty);
            await fixture.StopAsync();
        }

        /// <summary>验证 active target 的远端断开通过主线程事件立即失效，并允许显式重进 own-world。</summary>
        [Test]
        public async Task UnexpectedGameplayDisconnectInvalidatesTargetAndAllowsExplicitRecovery()
        {
            var fixture = await CoordinatorFixture.CreateAsync(blockBootstrap: false);
            Assert.That(await fixture.Coordinator.EnterOwnWorldAsync(CancellationToken.None), Is.True);
            Assert.That(fixture.WorldService.Snapshot.CurrentWorld, Is.Not.Null);

            fixture.Factory.CurrentConnection.Dispose();
            await WaitUntilAsync(() => fixture.Dispatcher.PendingCount > 0);
            Assert.That(fixture.Coordinator.Snapshot.State, Is.EqualTo(ClientWorldFlowState.OwnWorld));

            await DrainUntilAsync(
                fixture.Dispatcher,
                () => fixture.Coordinator.Snapshot.State == ClientWorldFlowState.ConnectionLost);

            Assert.That(fixture.Coordinator.Snapshot.State, Is.EqualTo(ClientWorldFlowState.ConnectionLost));
            Assert.That(fixture.Coordinator.Snapshot.Failure, Is.EqualTo(ClientWorldFlowFailure.Transport));
            Assert.That(fixture.WorldService.Snapshot.CurrentWorld, Is.Null);
            Assert.That(fixture.VisitService.Snapshot.Current, Is.Null);
            Assert.That(await fixture.Coordinator.EnterOwnWorldAsync(CancellationToken.None), Is.True);
            Assert.That(fixture.Coordinator.Snapshot.State, Is.EqualTo(ClientWorldFlowState.OwnWorld));
            Assert.That(fixture.Coordinator.Snapshot.Failure, Is.EqualTo(ClientWorldFlowFailure.None));
            await fixture.StopAsync();
        }

        /// <summary>验证OwnWorld恢复使用冻结descriptor重建新connection并接受同identity replacement。</summary>
        [Test]
        public async Task OwnWorldRecoveryRebuildsAuthoritativeTarget()
        {
            var fixture = await CoordinatorFixture.CreateAsync(blockBootstrap: false);
            Assert.That(await fixture.Coordinator.EnterOwnWorldAsync(CancellationToken.None), Is.True);
            var operations = (IClientConnectionRecoveryOperations)fixture.Coordinator;
            Assert.That(operations.TryCaptureTarget(out var descriptor), Is.True);
            Assert.That(descriptor.Kind, Is.EqualTo(ClientRecoveryTargetKind.OwnWorld));

            fixture.Factory.CurrentConnection.Dispose();
            await DrainUntilAsync(
                fixture.Dispatcher,
                () => fixture.Coordinator.Snapshot.State == ClientWorldFlowState.ConnectionLost);

            var result = await operations.RecoverWorldAsync(descriptor, CancellationToken.None);
            Assert.That(result, Is.EqualTo(ClientConnectionRecoveryResultKind.Succeeded));
            Assert.That(fixture.Coordinator.Snapshot.State, Is.EqualTo(ClientWorldFlowState.OwnWorld));
            Assert.That(fixture.Factory.CurrentConnection.FirstBusinessMessageID, Is.EqualTo(2000));
            Assert.That(operations.TryValidateRecoveredTarget(descriptor, out var targetGeneration), Is.True);
            Assert.That(targetGeneration, Is.GreaterThan(descriptor.TargetGeneration));
            await fixture.StopAsync();
        }

        /// <summary>验证开放访问的Owner恢复时先收敛VisitSession，再允许target validation成功。</summary>
        [Test]
        public async Task OwnWorldRecoveryReconcilesOpenOwnerVisitBeforeValidation()
        {
            var fixture = await CoordinatorFixture.CreateAsync(blockBootstrap: false);
            Assert.That(await fixture.Coordinator.EnterOwnWorldAsync(CancellationToken.None), Is.True);
            Assert.That((await fixture.VisitService.OpenAsync(CancellationToken.None)).IsSuccess, Is.True);
            var operations = (IClientConnectionRecoveryOperations)fixture.Coordinator;
            Assert.That(operations.TryCaptureTarget(out var descriptor), Is.True);
            Assert.That(descriptor.Kind, Is.EqualTo(ClientRecoveryTargetKind.OwnWorld));
            Assert.That(descriptor.VisitSessionID, Is.EqualTo("visit_one"));
            Assert.That(descriptor.VisitRevision, Is.EqualTo(2));

            fixture.Factory.NextVisitRevision = descriptor.VisitRevision;
            fixture.Factory.CurrentConnection.Dispose();
            await DrainUntilAsync(
                fixture.Dispatcher,
                () => fixture.Coordinator.Snapshot.State == ClientWorldFlowState.ConnectionLost);

            var result = await operations.RecoverWorldAsync(descriptor, CancellationToken.None);

            Assert.That(result, Is.EqualTo(ClientConnectionRecoveryResultKind.Succeeded));
            Assert.That(fixture.Coordinator.Snapshot.State, Is.EqualTo(ClientWorldFlowState.OwnWorld));
            Assert.That(fixture.Factory.CurrentConnection.VisitSnapshotRequests, Is.EqualTo(1));
            Assert.That(fixture.VisitService.Snapshot.Current, Is.Not.Null);
            Assert.That(fixture.VisitService.Snapshot.Current.VisitSessionID, Is.EqualTo("visit_one"));
            Assert.That(operations.TryValidateRecoveredTarget(descriptor, out _), Is.True);
            await fixture.StopAsync();
        }

        /// <summary>验证server replacement退役旧VisitSession或assignment时Owner提交新OwnWorld。</summary>
        /// <param name="errorCode">证明旧访问目标不可恢复的公开错误码。</param>
        [TestCase(2002)]
        [TestCase(2100)]
        public async Task OwnWorldRecoveryRetiresUnavailableOwnerVisitAndCommitsOwnWorld(
            int errorCode)
        {
            var fixture = await CoordinatorFixture.CreateAsync(blockBootstrap: false);
            Assert.That(await fixture.Coordinator.EnterOwnWorldAsync(CancellationToken.None), Is.True);
            Assert.That((await fixture.VisitService.OpenAsync(CancellationToken.None)).IsSuccess, Is.True);
            var operations = (IClientConnectionRecoveryOperations)fixture.Coordinator;
            Assert.That(operations.TryCaptureTarget(out var descriptor), Is.True);
            Assert.That(descriptor.VisitSessionID, Is.EqualTo("visit_one"));

            fixture.Factory.NextVisitSnapshotErrorCode = errorCode;
            fixture.Factory.CurrentConnection.Dispose();
            await DrainUntilAsync(
                fixture.Dispatcher,
                () => fixture.Coordinator.Snapshot.State == ClientWorldFlowState.ConnectionLost);

            var result = await operations.RecoverWorldAsync(descriptor, CancellationToken.None);

            Assert.That(result, Is.EqualTo(ClientConnectionRecoveryResultKind.ReturningOwnWorld));
            Assert.That(fixture.Coordinator.Snapshot.State, Is.EqualTo(ClientWorldFlowState.OwnWorld));
            Assert.That(fixture.Factory.ConnectionCount, Is.EqualTo(3));
            Assert.That(fixture.Factory.CurrentConnection.VisitSnapshotRequests, Is.Zero);
            Assert.That(fixture.VisitService.Snapshot.Current, Is.Null);
            Assert.That(fixture.WorldService.Snapshot.CurrentWorld, Is.Not.Null);
            Assert.That(operations.TryCaptureTarget(out var replacement), Is.True);
            Assert.That(replacement.Kind, Is.EqualTo(ClientRecoveryTargetKind.OwnWorld));
            Assert.That(string.IsNullOrEmpty(replacement.VisitSessionID), Is.True);
            await fixture.StopAsync();
        }

        /// <summary>验证Visitor恢复以RECONNECT作为唯一首帧并恢复同一VisitSession。</summary>
        [Test]
        public async Task VisitorRecoveryUsesReconnectFirstFrame()
        {
            var fixture = await CoordinatorFixture.CreateAsync(blockBootstrap: false);
            Assert.That(await fixture.Coordinator.EnterOwnWorldAsync(CancellationToken.None), Is.True);
            Assert.That(
                fixture.VisitService.ApplyInvite(CoordinatorFixture.Invite()),
                Is.EqualTo(ClientProjectionApplyResult.Applied));
            Assert.That(
                await fixture.Coordinator.JoinVisitAsync(
                    "visit_one",
                    "invite_one",
                    CancellationToken.None),
                Is.True);
            var operations = (IClientConnectionRecoveryOperations)fixture.Coordinator;
            Assert.That(operations.TryCaptureTarget(out var descriptor), Is.True);
            Assert.That(descriptor.Kind, Is.EqualTo(ClientRecoveryTargetKind.Visiting));
            Assert.That(descriptor.VisitRevision, Is.EqualTo(3));

            fixture.Factory.CurrentConnection.Dispose();
            await DrainUntilAsync(
                fixture.Dispatcher,
                () => fixture.Coordinator.Snapshot.State == ClientWorldFlowState.ConnectionLost);
            fixture.Api.UseReconnectAdmission(4);

            var result = await operations.RecoverWorldAsync(descriptor, CancellationToken.None);
            Assert.That(result, Is.EqualTo(ClientConnectionRecoveryResultKind.Succeeded));
            Assert.That(fixture.Factory.CurrentConnection.FirstBusinessMessageID, Is.EqualTo(2115));
            Assert.That(fixture.Factory.CurrentConnection.LastReconnectExpectedRevision, Is.EqualTo(4));
            Assert.That(fixture.Coordinator.Snapshot.State, Is.EqualTo(ClientWorldFlowState.Visiting));
            Assert.That(fixture.Coordinator.Snapshot.VisitSessionID, Is.EqualTo("visit_one"));
            Assert.That(fixture.VisitService.Snapshot.Current.Role, Is.EqualTo(ClientVisitRole.Visitor));
            await fixture.StopAsync();
        }

        /// <summary>验证Visitor断线后的access refresh可安全重绑定冻结descriptor并恢复同一VisitSession。</summary>
        [Test]
        public async Task VisitorRecoveryRebindsDescriptorAfterSessionRefresh()
        {
            var fixture = await CoordinatorFixture.CreateAsync(blockBootstrap: false);
            Assert.That(await fixture.Coordinator.EnterOwnWorldAsync(CancellationToken.None), Is.True);
            Assert.That(
                fixture.VisitService.ApplyInvite(CoordinatorFixture.Invite()),
                Is.EqualTo(ClientProjectionApplyResult.Applied));
            Assert.That(
                await fixture.Coordinator.JoinVisitAsync(
                    "visit_one",
                    "invite_one",
                    CancellationToken.None),
                Is.True);
            var operations = (IClientConnectionRecoveryOperations)fixture.Coordinator;
            Assert.That(operations.TryCaptureTarget(out var frozen), Is.True);

            fixture.Factory.CurrentConnection.Dispose();
            await DrainUntilAsync(
                fixture.Dispatcher,
                () => fixture.Coordinator.Snapshot.State == ClientWorldFlowState.ConnectionLost);
            var refresh = await fixture.Session.RefreshAsync(CancellationToken.None);
            Assert.That(refresh.IsSuccess, Is.True);
            Assert.That(refresh.Value.Generation, Is.GreaterThan(frozen.SessionGeneration));
            Assert.That(operations.TryCaptureTarget(out var rebound), Is.True);
            Assert.That(rebound.SessionGeneration, Is.EqualTo(refresh.Value.Generation));
            Assert.That(rebound.HasSameSessionLineage(frozen), Is.True);

            fixture.Api.UseReconnectAdmission(4);
            var result = await operations.RecoverWorldAsync(frozen, CancellationToken.None);

            Assert.That(result, Is.EqualTo(ClientConnectionRecoveryResultKind.Succeeded));
            Assert.That(fixture.Coordinator.Snapshot.State, Is.EqualTo(ClientWorldFlowState.Visiting));
            Assert.That(fixture.Coordinator.Snapshot.VisitSessionID, Is.EqualTo("visit_one"));
            Assert.That(operations.TryValidateRecoveredTarget(frozen, out _), Is.True);
            await fixture.StopAsync();
        }

        /// <summary>验证Visitor在公开grace绝对边界不再签发RECONNECT，而是权威返回OwnWorld。</summary>
        [Test]
        public async Task VisitorRecoveryAtGraceDeadlineReturnsOwnWorldWithoutReconnectAdmission()
        {
            var fixture = await CoordinatorFixture.CreateAsync(blockBootstrap: false);
            Assert.That(await fixture.Coordinator.EnterOwnWorldAsync(CancellationToken.None), Is.True);
            Assert.That(
                fixture.VisitService.ApplyInvite(CoordinatorFixture.Invite()),
                Is.EqualTo(ClientProjectionApplyResult.Applied));
            Assert.That(
                await fixture.Coordinator.JoinVisitAsync(
                    "visit_one",
                    "invite_one",
                    CancellationToken.None),
                Is.True);
            var operations = (IClientConnectionRecoveryOperations)fixture.Coordinator;
            Assert.That(operations.TryCaptureTarget(out var descriptor), Is.True);

            fixture.Factory.CurrentConnection.Dispose();
            await DrainUntilAsync(
                fixture.Dispatcher,
                () => fixture.Coordinator.Snapshot.State == ClientWorldFlowState.ConnectionLost);
            fixture.SetUtcNowMilliseconds(descriptor.ReconnectExpiresAtMilliseconds);
            var admissionsBeforeRecovery = fixture.Api.AdmissionCalls;

            var result = await operations.RecoverWorldAsync(descriptor, CancellationToken.None);

            Assert.That(
                result,
                Is.EqualTo(ClientConnectionRecoveryResultKind.ReturningOwnWorld),
                $"flow={fixture.Coordinator.Snapshot.State}/{fixture.Coordinator.Snapshot.Failure} " +
                $"bootstrap={fixture.Api.BootstrapCalls} admission={fixture.Api.AdmissionCalls}");
            Assert.That(fixture.Api.AdmissionCalls, Is.EqualTo(admissionsBeforeRecovery + 1));
            Assert.That(fixture.Factory.CurrentConnection.FirstBusinessMessageID, Is.EqualTo(2000));
            Assert.That(fixture.Coordinator.Snapshot.State, Is.EqualTo(ClientWorldFlowState.OwnWorld));
            Assert.That(fixture.VisitService.Snapshot.Current, Is.Null);
            await fixture.StopAsync();
        }

        /// <summary>验证停止使 current generation 失效并拒绝后续 flow。</summary>
        [Test]
        public async Task ShutdownRejectsLateAndNewFlow()
        {
            var fixture = await CoordinatorFixture.CreateAsync(blockBootstrap: true);
            var pending = fixture.Coordinator.EnterOwnWorldAsync(CancellationToken.None);
            await fixture.Api.WaitForBootstrapCallAsync();
            await fixture.Coordinator.StopAsync(CancellationToken.None);
            fixture.Api.CompleteBootstrap();

            Assert.That(await pending, Is.False);
            Assert.That(fixture.Coordinator.Snapshot.State, Is.EqualTo(ClientWorldFlowState.Stopped));
            Assert.That(await fixture.Coordinator.EnterOwnWorldAsync(CancellationToken.None), Is.False);
            await fixture.StopRemainingAsync();
        }

        /// <summary>在短期有界轮询内等待后台 connection pump 产生可观察结果。</summary>
        /// <param name="condition">无副作用完成条件。</param>
        /// <returns>条件成立时完成。</returns>
        private static async Task WaitUntilAsync(Func<bool> condition)
        {
            var deadline = DateTime.UtcNow.AddSeconds(2);
            while (!condition())
            {
                if (DateTime.UtcNow >= deadline)
                {
                    Assert.Fail("World flow 异步条件未在测试 deadline 内成立。");
                }

                await Task.Delay(5);
            }
        }

        /// <summary>持续执行有限主线程批次，直到 gameplay terminal callback 提交 world flow。</summary>
        /// <param name="dispatcher">被测有界主线程 dispatcher。</param>
        /// <param name="condition">由 terminal callback 提交的无副作用完成条件。</param>
        /// <returns>条件成立时完成。</returns>
        private static async Task DrainUntilAsync(
            MainThreadDispatcher dispatcher,
            Func<bool> condition)
        {
            var deadline = DateTime.UtcNow.AddSeconds(2);
            while (!condition())
            {
                dispatcher.Drain(maximumCallbacks: 8);
                if (condition())
                {
                    return;
                }

                if (DateTime.UtcNow >= deadline)
                {
                    Assert.Fail("World flow terminal callback 未在测试 deadline 内提交。");
                }

                await Task.Delay(5);
            }
        }

        /// <summary>组合不依赖真实 listener、Unity Services 或 Scene 的完整 coordinator 图。</summary>
        private sealed class CoordinatorFixture
        {
            /// <summary>保存统一 endpoint fixture。</summary>
            private static readonly ClientEndpoint Endpoint =
                new ClientEndpoint(ClientEndpointChannel.TlsTcp, "127.0.0.1", 4433);

            /// <summary>记录 coordinator 是否已单独停止。</summary>
            private bool _coordinatorStopped;

            /// <summary>创建完整 fixture。</summary>
            private CoordinatorFixture(
                ClientConfigurationStore configuration,
                SessionCoordinator session,
                MainThreadDispatcher dispatcher,
                ClientGameplayChannel gameplay,
                ClientGameplayChannelPortAdapter gameplayPort,
                PersonalWorldService worldService,
                VisitSessionService visitService,
                WorldAdmissionCoordinator coordinator,
                ScriptedHttpApi api,
                ScriptedConnectionFactory factory,
                FixedClock clock)
            {
                Configuration = configuration;
                Session = session;
                Dispatcher = dispatcher;
                Gameplay = gameplay;
                GameplayPort = gameplayPort;
                WorldService = worldService;
                VisitService = visitService;
                Coordinator = coordinator;
                Api = api;
                Factory = factory;
                Clock = clock;
            }

            /// <summary>获取配置 owner。</summary>
            internal ClientConfigurationStore Configuration { get; }

            /// <summary>获取 Session owner。</summary>
            internal SessionCoordinator Session { get; }

            /// <summary>获取主线程 dispatcher。</summary>
            internal MainThreadDispatcher Dispatcher { get; }

            /// <summary>获取 gameplay owner。</summary>
            internal ClientGameplayChannel Gameplay { get; }

            /// <summary>获取 Application 使用的 typed gameplay port。</summary>
            internal ClientGameplayChannelPortAdapter GameplayPort { get; }

            /// <summary>获取 PersonalWorld Service。</summary>
            internal PersonalWorldService WorldService { get; }

            /// <summary>获取 VisitSession Service。</summary>
            internal VisitSessionService VisitService { get; }

            /// <summary>获取被测 coordinator。</summary>
            internal WorldAdmissionCoordinator Coordinator { get; }

            /// <summary>获取可控 HTTP fake。</summary>
            internal ScriptedHttpApi Api { get; }

            /// <summary>获取可控 connection factory。</summary>
            internal ScriptedConnectionFactory Factory { get; }

            /// <summary>保存fixture唯一可控Unix毫秒时钟。</summary>
            private FixedClock Clock { get; }

            /// <summary>把fixture时钟推进到指定绝对Unix毫秒。</summary>
            /// <param name="value">不得回退的Unix毫秒值。</param>
            internal void SetUtcNowMilliseconds(long value)
            {
                if (value < Clock.UtcNowMilliseconds)
                {
                    throw new ArgumentOutOfRangeException(nameof(value));
                }

                Clock.UtcNowMilliseconds = value;
            }

            /// <summary>按 production 初始化顺序创建并认证完整对象图。</summary>
            /// <param name="blockBootstrap">是否阻塞第一次 world bootstrap。</param>
            /// <returns>已初始化且 authenticated fixture。</returns>
            internal static async Task<CoordinatorFixture> CreateAsync(bool blockBootstrap)
            {
                var configuration = new ClientConfigurationStore();
                await configuration.InitializeAsync(CancellationToken.None);
                configuration.Publish(new ClientConfigurationSnapshot(
                    new ClientVersionInfo(1, "0.1.0", "0.1.0"),
                    new ClientBootstrapConfiguration(
                        new[] { Endpoint },
                        new ClientPublicLimits(4096, 65536))));
                var api = new ScriptedHttpApi(Endpoint, blockBootstrap);
                var clock = new FixedClock();
                var session = new SessionCoordinator(
                    configuration,
                    api,
                    clock,
                    new FakeClientSecureSessionStore(),
                    FakeClientSecureSessionStore.EnvironmentBinding);
                await session.InitializeAsync(CancellationToken.None);
                Assert.That(
                    (await session.LoginAsync("fixture-user", "password", CancellationToken.None)).IsSuccess,
                    Is.True);
                var dispatcher = new MainThreadDispatcher(Environment.CurrentManagedThreadId, 64);
                await dispatcher.InitializeAsync(CancellationToken.None);
                var factory = new ScriptedConnectionFactory();
                var gameplay = new ClientGameplayChannel(
                    configuration,
                    session,
                    factory,
                    new ClientGameplayCodec(),
                    dispatcher,
                    new SystemClientDelay());
                var gameplayPort = new ClientGameplayChannelPortAdapter(
                    gameplay,
                    new ClientGameplayProtocolAdapter());
                await gameplayPort.InitializeAsync(CancellationToken.None);
                var world = new PersonalWorldService(null, gameplayPort);
                var visit = new VisitSessionService(null, gameplayPort, clock);
                var coordinator = new WorldAdmissionCoordinator(
                    session,
                    clock,
                    gameplayPort,
                    world,
                    visit);
                await world.InitializeAsync(CancellationToken.None);
                await visit.InitializeAsync(CancellationToken.None);
                await coordinator.InitializeAsync(CancellationToken.None);
                return new CoordinatorFixture(
                    configuration,
                    session,
                    dispatcher,
                    gameplay,
                    gameplayPort,
                    world,
                    visit,
                    coordinator,
                    api,
                    factory,
                    clock);
            }

            /// <summary>创建 current target Visitor 的定向 invite。</summary>
            /// <returns>有效 invite PUSH。</returns>
            internal static VisitInvitePush Invite(
                string inviteID = "invite_one",
                ulong createdRevision = 1)
            {
                return new VisitInvitePush
                {
                    OwnerPlayerId = "player_owner",
                    Invite = new VisitInviteSummary
                    {
                        InviteId = inviteID,
                        VisitSessionId = "visit_one",
                        TargetVisitorId = "player_visitor",
                        State = VisitInviteState.Pending,
                        CreatedRevision = createdRevision,
                        ExpiresAtMs = 90_000,
                    },
                };
            }

            /// <summary>按 production 逆序停止完整 fixture。</summary>
            /// <returns>全部 owner 已停止时完成。</returns>
            internal async Task StopAsync()
            {
                await Coordinator.StopAsync(CancellationToken.None);
                _coordinatorStopped = true;
                await StopRemainingAsync();
            }

            /// <summary>停止 coordinator 之外的其余 owner。</summary>
            /// <returns>全部剩余 owner 已停止时完成。</returns>
            internal async Task StopRemainingAsync()
            {
                if (!_coordinatorStopped && Coordinator.Snapshot.State != ClientWorldFlowState.Stopped)
                {
                    await Coordinator.StopAsync(CancellationToken.None);
                }

                _coordinatorStopped = true;
                await VisitService.StopAsync(CancellationToken.None);
                await WorldService.StopAsync(CancellationToken.None);
                await GameplayPort.StopAsync(CancellationToken.None);
                await Session.StopAsync(CancellationToken.None);
                await Configuration.StopAsync(CancellationToken.None);
                await Dispatcher.StopAsync(CancellationToken.None);
            }

            /// <summary>提供 bootstrap/accept/admission/ticket 的确定性 HTTP fake。</summary>
            internal sealed class ScriptedHttpApi : IClientBootstrapGateway, IClientSessionGateway
            {
                /// <summary>保存统一 TLS/TCP endpoint。</summary>
                private readonly ClientEndpoint _endpoint;

                /// <summary>通知测试第一次 bootstrap 已进入 fake。</summary>
                private readonly TaskCompletionSource<bool> _bootstrapCalled =
                    new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);

                /// <summary>可选阻塞 bootstrap completion。</summary>
                private readonly TaskCompletionSource<ClientGatewayResult<ClientWorldBootstrap>> _bootstrapGate;

                /// <summary>保存最近一次 accept 或显式恢复推进后的权威 VisitSession revision。</summary>
                private long _visitAdmissionRevision;

                /// <summary>保存下一次 Visitor admission 的服务端权威用途。</summary>
                private ClientWorldAdmissionPurpose _visitAdmissionPurpose = ClientWorldAdmissionPurpose.Join;

                /// <summary>创建 scripted HTTP fake。</summary>
                internal ScriptedHttpApi(ClientEndpoint endpoint, bool blockBootstrap)
                {
                    _endpoint = endpoint;
                    if (blockBootstrap)
                    {
                        _bootstrapGate = new TaskCompletionSource<ClientGatewayResult<ClientWorldBootstrap>>(
                            TaskCreationOptions.RunContinuationsAsynchronously);
                    }
                }

                /// <summary>获取 bootstrap 调用次数。</summary>
                internal int BootstrapCalls { get; private set; }

                /// <summary>获取接受邀请 HTTP 调用次数。</summary>
                internal int AcceptInviteCalls { get; private set; }

                /// <summary>获取world admission签发调用次数。</summary>
                internal int AdmissionCalls { get; private set; }

                /// <summary>控制后续 bootstrap 返回 transport failure。</summary>
                internal bool FailBootstrap { get; set; }

                /// <summary>控制 accept 返回指定 registry 错误；0 表示成功。</summary>
                internal int AcceptInviteErrorCode { get; set; }

                /// <summary>把既有 Visitor membership 推进到服务端恢复 revision，并令后续 admission 用于 RECONNECT。</summary>
                /// <param name="authoritativeRevision">服务端处理断线事实后的当前 VisitSession revision。</param>
                internal void UseReconnectAdmission(ulong authoritativeRevision)
                {
                    if (authoritativeRevision == 0)
                    {
                        throw new ArgumentOutOfRangeException(nameof(authoritativeRevision));
                    }

                    _visitAdmissionRevision = checked((long)authoritativeRevision);
                    _visitAdmissionPurpose = ClientWorldAdmissionPurpose.Reconnect;
                }

                /// <summary>等待第一次 bootstrap 调用到达。</summary>
                /// <returns>调用已到达时完成。</returns>
                internal Task WaitForBootstrapCallAsync() => _bootstrapCalled.Task;

                /// <summary>释放被阻塞的 bootstrap 成功结果。</summary>
                internal void CompleteBootstrap()
                {
                    _bootstrapGate?.TrySetResult(ClientGatewayResult<ClientWorldBootstrap>.Success(Bootstrap()));
                }

                /// <inheritdoc />
                public Task<ClientGatewayResult<ClientVersionInfo>> GetVersionAsync(CancellationToken cancellationToken) =>
                    throw new NotSupportedException();

                /// <inheritdoc />
                public Task<ClientGatewayResult<ClientBootstrapConfiguration>> GetBootstrapConfigurationAsync(CancellationToken cancellationToken) =>
                    throw new NotSupportedException();

                /// <inheritdoc />
                public Task<ClientGatewayResult<ClientAuthentication>> RegisterAsync(ClientRegisterGatewayRequest request, CancellationToken cancellationToken) =>
                    throw new NotSupportedException();

                /// <inheritdoc />
                public Task<ClientGatewayResult<ClientAuthentication>> LoginAsync(ClientLoginGatewayRequest request, CancellationToken cancellationToken)
                {
                    return Task.FromResult(ClientGatewayResult<ClientAuthentication>.Success(
                        new ClientAuthentication(
                            new ClientAccountSummary("account_fixture", "Fixture", 1),
                            new ClientSessionSummary("session_fixture", 1, 100_000),
                            new ClientTokenPair("access_fixture", "refresh_fixture", 90_000, 100_000),
                            new[] { _endpoint })));
                }

                /// <inheritdoc />
                public Task<ClientGatewayResult<ClientTokenPair>> RefreshAsync(ClientCredentialGatewayRequest request, CancellationToken cancellationToken)
                {
                    return Task.FromResult(ClientGatewayResult<ClientTokenPair>.Success(
                        new ClientTokenPair(
                            "access_refreshed",
                            "refresh_rotated",
                            95_000,
                            100_000)));
                }

                /// <inheritdoc />
                public Task<ClientGatewayResult<ClientGatewayEmpty>> LogoutAsync(ClientCredentialGatewayRequest request, CancellationToken cancellationToken) =>
                    throw new NotSupportedException();

                /// <inheritdoc />
                public Task<ClientGatewayResult<ClientConnectionTicket>> IssueConnectionTicketAsync(ClientConnectionTicketGatewayRequest request, CancellationToken cancellationToken)
                {
                    return Task.FromResult(ClientGatewayResult<ClientConnectionTicket>.Success(
                        new ClientConnectionTicket(
                            "0102030405060708090a0b0c0d0e0f10",
                            _endpoint,
                            new[] { ClientConnectionScope.Gameplay },
                            120_000)));
                }

                /// <inheritdoc />
                public Task<ClientGatewayResult<ClientWorldBootstrap>> GetWorldBootstrapAsync(ClientCredentialGatewayRequest request, CancellationToken cancellationToken)
                {
                    BootstrapCalls++;
                    _bootstrapCalled.TrySetResult(true);
                    if (_bootstrapGate != null && BootstrapCalls == 1)
                    {
                        return _bootstrapGate.Task;
                    }

                    if (FailBootstrap)
                    {
                        return Task.FromResult(ClientGatewayResult<ClientWorldBootstrap>.Failed(
                            new ClientGatewayFailure(ClientGatewayFailureKind.Transport, "getWorldBootstrap")));
                    }

                    return Task.FromResult(ClientGatewayResult<ClientWorldBootstrap>.Success(Bootstrap()));
                }

                /// <inheritdoc />
                public Task<ClientGatewayResult<ClientVisitReservation>> AcceptVisitInviteAsync(ClientAcceptVisitInviteGatewayRequest request, CancellationToken cancellationToken)
                {
                    AcceptInviteCalls++;
                    if (AcceptInviteErrorCode != 0)
                    {
                        Assert.That(
                            ClientErrorRegistry.TryGet(AcceptInviteErrorCode, out var knownError),
                            Is.True);
                        return Task.FromResult(ClientGatewayResult<ClientVisitReservation>.Rejected(
                            new ClientServerError(
                                knownError.Code,
                                knownError.Category,
                                knownError.MessageKey,
                                "request_fixture",
                                knownError.Retryable,
                                null,
                                Array.Empty<ClientErrorDetail>())));
                    }

                    _visitAdmissionRevision = checked(request.Invite.ExpectedRevision + 1);
                    _visitAdmissionPurpose = ClientWorldAdmissionPurpose.Join;
                    return Task.FromResult(ClientGatewayResult<ClientVisitReservation>.Success(
                        new ClientVisitReservation(
                            request.Invite.VisitSessionID,
                            _visitAdmissionRevision,
                            80_000)));
                }

                /// <inheritdoc />
                public Task<ClientGatewayResult<ClientWorldAdmission>> IssueWorldAdmissionAsync(ClientWorldAdmissionGatewayRequest request, CancellationToken cancellationToken)
                {
                    AdmissionCalls++;
                    var visit = request.Target.Kind == ClientWorldAdmissionTargetKind.VisitWorld;
                    return Task.FromResult(ClientGatewayResult<ClientWorldAdmission>.Success(
                        new ClientWorldAdmission(
                            "wad1_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
                            _endpoint,
                            visit ? ClientWorldRole.Visitor : ClientWorldRole.Owner,
                            visit ? _visitAdmissionPurpose : ClientWorldAdmissionPurpose.OwnWorld,
                            visit ? checked((ulong)_visitAdmissionRevision) : 0UL,
                            120_000)));
                }

                /// <summary>创建 current actor 的 HTTP bootstrap。</summary>
                /// <returns>带 active assignment 的 bootstrap。</returns>
                private static ClientWorldBootstrap Bootstrap()
                {
                    return new ClientWorldBootstrap(
                        new ClientPersonalWorldSummary(
                            "world_self",
                            "player_visitor",
                            ClientPersonalWorldLifecycle.Active,
                            1,
                            1_000),
                        new ClientWorldAssignment(
                            "world_self",
                            "instance_self",
                            Endpoint,
                            1,
                            90_000));
                }
            }

            /// <summary>按连接序号创建 own/visit/return scripted duplex stream。</summary>
            internal sealed class ScriptedConnectionFactory : IClientGameplayConnectionFactory
            {
                /// <summary>获取 current connection，供远端断开测试控制生命周期。</summary>
                internal ScriptedConnection CurrentConnection { get; private set; }

                /// <summary>获取已创建 connection 数量。</summary>
                internal int ConnectionCount { get; private set; }

                /// <summary>获取或设置下一条connection返回的初始权威VisitSession revision。</summary>
                internal ulong NextVisitRevision { get; set; } = 1;

                /// <summary>获取或设置下一条connection对VisitSnapshot返回的公开错误码；0表示成功。</summary>
                internal int NextVisitSnapshotErrorCode { get; set; }

                /// <summary>获取已写入 gameplay channel 的 request/command 数量。</summary>
                internal int OperationCount { get; private set; }

                /// <inheritdoc />
                public Task<IClientGameplayConnection> ConnectAsync(ClientEndpoint endpoint, CancellationToken cancellationToken)
                {
                    cancellationToken.ThrowIfCancellationRequested();
                    ConnectionCount++;
                    CurrentConnection = new ScriptedConnection(
                        () => OperationCount++,
                        NextVisitRevision,
                        NextVisitSnapshotErrorCode);
                    return Task.FromResult<IClientGameplayConnection>(CurrentConnection);
                }
            }

            /// <summary>把客户端 request/command 同步映射为确定性 response 的内存 stream。</summary>
            internal sealed class ScriptedConnection : IClientGameplayConnection
            {
                /// <summary>保护读取队列与 dispose。</summary>
                private readonly object _sync = new object();

                /// <summary>保存 peer 待返回字节。</summary>
                private readonly Queue<byte> _reads = new Queue<byte>();

                /// <summary>通知 reader 有数据或连接已关闭。</summary>
                private readonly SemaphoreSlim _signal = new SemaphoreSlim(0);

                /// <summary>记录每个已解析 operation，供 policy 测试确认没有写入。</summary>
                private readonly Action _recordOperation;

                /// <summary>保存当前 connection 的 S2C sequence。</summary>
                private ulong _sequence;

                /// <summary>保存 fixture 当前 VisitSession revision，使连续 mutation 遵循真实服务端单调事实。</summary>
                private ulong _visitRevision;

                /// <summary>保存当前 connection 已创建 invite 数量，用于产生不可复用 identity。</summary>
                private int _inviteSequence;

                /// <summary>保存VisitSnapshot需要返回的公开错误码；0表示正常快照。</summary>
                private readonly int _visitSnapshotErrorCode;

                /// <summary>区分首个 IHTP preface 与后续 framed envelope。</summary>
                private bool _prefaceWritten;

                /// <summary>标识当前connection已经由JOIN或RECONNECT绑定Visitor target。</summary>
                private bool _visitorTarget;

                /// <summary>保存 dispose 状态。</summary>
                private bool _disposed;

                /// <summary>获取preface后的首个typed业务message ID。</summary>
                internal uint FirstBusinessMessageID { get; private set; }

                /// <summary>获取最近一条 RECONNECT 首帧携带的权威 expected revision。</summary>
                internal ulong LastReconnectExpectedRevision { get; private set; }

                /// <summary>获取当前connection收到的VisitSession完整快照请求数。</summary>
                internal int VisitSnapshotRequests { get; private set; }

                /// <summary>创建指定用途的 scripted connection。</summary>
                /// <param name="recordOperation">记录已解析业务 operation 的 callback。</param>
                /// <param name="visitRevision">该connection可读取的初始权威VisitSession revision。</param>
                /// <param name="visitSnapshotErrorCode">VisitSnapshot公开错误码；0表示正常快照。</param>
                internal ScriptedConnection(
                    Action recordOperation,
                    ulong visitRevision,
                    int visitSnapshotErrorCode)
                {
                    _recordOperation = recordOperation;
                    _visitRevision = visitRevision;
                    _visitSnapshotErrorCode = visitSnapshotErrorCode;
                }

                /// <inheritdoc />
                public async Task<int> ReadAsync(byte[] buffer, int offset, int count, CancellationToken cancellationToken)
                {
                    while (true)
                    {
                        lock (_sync)
                        {
                            if (_reads.Count > 0)
                            {
                                var copied = Math.Min(count, _reads.Count);
                                for (var index = 0; index < copied; index++)
                                {
                                    buffer[offset + index] = _reads.Dequeue();
                                }

                                return copied;
                            }

                            if (_disposed)
                            {
                                return 0;
                            }
                        }

                        await _signal.WaitAsync(cancellationToken);
                    }
                }

                /// <inheritdoc />
                public Task WriteAsync(byte[] buffer, CancellationToken cancellationToken)
                {
                    cancellationToken.ThrowIfCancellationRequested();
                    if (!_prefaceWritten)
                    {
                        _prefaceWritten = true;
                        return Task.CompletedTask;
                    }

                    var envelope = ParseFrame(buffer);
                    if (FirstBusinessMessageID == 0)
                    {
                        FirstBusinessMessageID = envelope.MessageId;
                    }

                    _recordOperation();
                    IMessage response;
                    uint responseID;
                    ErrorPayload publicError = null;
                    switch (envelope.MessageId)
                    {
                        case 2000:
                            responseID = 2001;
                            response = new WorldSnapshotResponse
                            {
                                Snapshot = _visitorTarget
                                    ? World("world_owner", "player_owner", "instance_owner")
                                    : World("world_self", "player_visitor", "instance_self"),
                            };
                            break;
                        case 2103:
                            _visitRevision = 2;
                            responseID = 2104;
                            response = new VisitOpenResponse
                            {
                                Snapshot = Visit(_visitRevision, VisitLifecycle.Open),
                            };
                            break;
                        case 2105:
                            var createInvite = VisitCreateInviteCommand.Parser.ParseFrom(envelope.Payload);
                            RequireRevision(createInvite.ExpectedRevision, _visitRevision, "VisitCreateInvite");
                            _visitRevision++;
                            _inviteSequence++;
                            responseID = 2106;
                            response = new VisitCreateInviteResponse
                            {
                                Result = new VisitMutationResult
                                {
                                    Snapshot = Visit(_visitRevision, VisitLifecycle.Open),
                                },
                                Invite = new VisitInviteSummary
                                {
                                    InviteId = _inviteSequence == 1
                                        ? "invite_created"
                                        : $"invite_created_{_inviteSequence}",
                                    VisitSessionId = "visit_one",
                                    TargetVisitorId = createInvite.TargetVisitorId,
                                    State = VisitInviteState.Pending,
                                    CreatedRevision = _visitRevision,
                                    ExpiresAtMs = 90_000,
                                },
                            };
                            break;
                        case 2107:
                            var revoke = VisitRevokeInviteCommand.Parser.ParseFrom(envelope.Payload);
                            RequireRevision(revoke.ExpectedRevision, _visitRevision, "VisitRevokeInvite");
                            _visitRevision++;
                            responseID = 2108;
                            response = new VisitRevokeInviteResponse
                            {
                                Result = new VisitMutationResult
                                {
                                    Snapshot = Visit(_visitRevision, VisitLifecycle.Open),
                                },
                            };
                            break;
                        case 2109:
                            var join = VisitJoinCommand.Parser.ParseFrom(envelope.Payload);
                            _visitorTarget = true;
                            _visitRevision = join.ExpectedRevision + 1;
                            responseID = 2110;
                            response = new VisitJoinResponse
                            {
                                Result = new VisitMutationResult
                                {
                                    Snapshot = Visit(_visitRevision, VisitLifecycle.Open),
                                },
                            };
                            break;
                        case 2115:
                            var reconnect = VisitReconnectCommand.Parser.ParseFrom(envelope.Payload);
                            LastReconnectExpectedRevision = reconnect.ExpectedRevision;
                            _visitorTarget = true;
                            _visitRevision = reconnect.ExpectedRevision + 1;
                            responseID = 2116;
                            response = new VisitReconnectResponse
                            {
                                Result = new VisitMutationResult
                                {
                                    Snapshot = Visit(_visitRevision, VisitLifecycle.Open),
                                },
                            };
                            break;
                        case 2119:
                            VisitSnapshotRequests++;
                            responseID = 2120;
                            if (_visitSnapshotErrorCode != 0)
                            {
                                Assert.That(
                                    ClientErrorRegistry.TryGet(_visitSnapshotErrorCode, out var knownError),
                                    Is.True);
                                publicError = new ErrorPayload
                                {
                                    Code = (uint)knownError.Code,
                                    MessageKey = knownError.MessageKey,
                                    Retryable = knownError.Retryable,
                                    RequestId = envelope.RequestId,
                                };
                                response = null;
                            }
                            else
                            {
                                response = new VisitSnapshotResponse
                                {
                                    Snapshot = Visit(_visitRevision, VisitLifecycle.Open),
                                };
                            }
                            break;
                        case 2111:
                            var leave = VisitLeaveCommand.Parser.ParseFrom(envelope.Payload);
                            RequireRevision(leave.ExpectedRevision, _visitRevision, "VisitLeave");
                            _visitRevision++;
                            responseID = 2112;
                            response = new VisitLeaveResponse
                            {
                                Result = new VisitMutationResult
                                {
                                    Snapshot = Visit(_visitRevision, VisitLifecycle.Open),
                                },
                            };
                            break;
                        case 2113:
                            var kick = VisitKickCommand.Parser.ParseFrom(envelope.Payload);
                            RequireRevision(kick.ExpectedRevision, _visitRevision, "VisitKick");
                            _visitRevision++;
                            responseID = 2114;
                            response = new VisitKickResponse
                            {
                                Result = new VisitMutationResult
                                {
                                    Snapshot = Visit(_visitRevision, VisitLifecycle.Open),
                                },
                            };
                            break;
                        case 2117:
                            var close = VisitCloseCommand.Parser.ParseFrom(envelope.Payload);
                            RequireRevision(close.ExpectedRevision, _visitRevision, "VisitClose");
                            _visitRevision++;
                            responseID = 2118;
                            response = new VisitCloseResponse
                            {
                                Result = new VisitMutationResult
                                {
                                    Snapshot = Visit(_visitRevision, VisitLifecycle.Closed),
                                },
                            };
                            break;
                        default:
                            throw new InvalidOperationException("Fixture 收到未登记 gameplay operation。");
                    }

                    var outgoing = new ReliableEnvelope
                    {
                        ProtocolVersion = 1,
                        MessageId = responseID,
                        Kind = publicError == null ? MessageKind.Response : MessageKind.Error,
                        Sequence = ++_sequence,
                        TimestampMs = 1,
                        Payload = publicError == null
                            ? response.ToByteString()
                            : publicError.ToByteString(),
                    };
                    if (envelope.Kind == MessageKind.Command)
                    {
                        outgoing.CommandId = envelope.CommandId;
                    }
                    else
                    {
                        outgoing.RequestId = envelope.RequestId;
                    }

                    Enqueue(ClientGameplayFramer.Frame(outgoing.ToByteArray()));
                    return Task.CompletedTask;
                }

                /// <inheritdoc />
                public void Dispose()
                {
                    lock (_sync)
                    {
                        _disposed = true;
                    }

                    _signal.Release();
                }

                /// <summary>解析客户端完整 framed envelope。</summary>
                /// <param name="frame">4-byte prefix 与 envelope body。</param>
                /// <returns>Generated reliable envelope。</returns>
                private static ReliableEnvelope ParseFrame(byte[] frame)
                {
                    var body = new byte[frame.Length - 4];
                    Buffer.BlockCopy(frame, 4, body, 0, body.Length);
                    return ReliableEnvelope.Parser.ParseFrom(body);
                }

                /// <summary>拒绝 command 携带陈旧或跳跃 revision。</summary>
                /// <param name="actual">实际 expected revision。</param>
                /// <param name="expected">当前权威 revision。</param>
                /// <param name="operation">用于定位 fixture 失败的 operation 名称。</param>
                private static void RequireRevision(ulong actual, ulong expected, string operation)
                {
                    if (actual != expected)
                    {
                        throw new InvalidOperationException(
                            $"{operation} expected revision 应为 {expected}，实际为 {actual}。");
                    }
                }

                /// <summary>把完整 peer frame 加入读取队列。</summary>
                /// <param name="frame">Framed response。</param>
                private void Enqueue(byte[] frame)
                {
                    lock (_sync)
                    {
                        foreach (var value in frame)
                        {
                            _reads.Enqueue(value);
                        }
                    }

                    _signal.Release();
                }

                /// <summary>创建 world/assignment 完整 replacement。</summary>
                /// <param name="worldID">PersonalWorldID。</param>
                /// <param name="ownerID">Owner PlayerID。</param>
                /// <param name="instanceID">WorldInstanceID。</param>
                /// <returns>Generated world snapshot。</returns>
                private static WorldSnapshot World(string worldID, string ownerID, string instanceID)
                {
                    return new WorldSnapshot
                    {
                        World = new PersonalWorldSnapshot
                        {
                            PersonalWorldId = worldID,
                            OwnerPlayerId = ownerID,
                            Lifecycle = PersonalWorldLifecycle.Active,
                            Revision = 1,
                            CreatedAtMs = 1_000,
                        },
                        Assignment = Assignment(worldID, instanceID),
                    };
                }

                /// <summary>创建 VisitSession 完整 replacement。</summary>
                /// <param name="revision">Aggregate revision。</param>
                /// <param name="lifecycle">Visit lifecycle。</param>
                /// <returns>Generated VisitSession snapshot。</returns>
                private VisitSessionSnapshot Visit(ulong revision, VisitLifecycle lifecycle)
                {
                    var worldID = _visitorTarget ? "world_owner" : "world_self";
                    var instanceID = _visitorTarget ? "instance_owner" : "instance_self";
                    return new VisitSessionSnapshot
                    {
                        VisitSessionId = "visit_one",
                        OwnerPlayerId = "player_owner",
                        Assignment = Assignment(worldID, instanceID),
                        Lifecycle = lifecycle,
                        Revision = revision,
                        Capacity = 4,
                        CreatedAtMs = 1_000,
                        ExpiresAtMs = 90_000,
                    };
                }

                /// <summary>创建统一 TLS/TCP assignment。</summary>
                /// <param name="worldID">PersonalWorldID。</param>
                /// <param name="instanceID">WorldInstanceID。</param>
                /// <returns>Generated assignment。</returns>
                private static WorldAssignment Assignment(string worldID, string instanceID)
                {
                    return new WorldAssignment
                    {
                        PersonalWorldId = worldID,
                        WorldInstanceId = instanceID,
                        Endpoint = new Endpoint
                        {
                            Channel = TransportChannel.TlsTcp,
                            Host = "127.0.0.1",
                            Port = 4433,
                        },
                        Generation = 1,
                        LeaseExpiresAtMs = 90_000,
                    };
                }
            }

            /// <summary>提供未过期固定 Unix 时间。</summary>
            private sealed class FixedClock : IClientClock
            {
                /// <summary>获取固定 Unix 时间，单位为毫秒。</summary>
                public long UtcNowMilliseconds { get; set; } = 1_000;
            }
        }
    }
}
