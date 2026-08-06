using System;
using IHomeland.Client.Networking.Application.Contracts;
using IHomeland.Client.PersonalWorld.Application;
using NUnit.Framework;

namespace IHomeland.Client.PersonalWorld.Tests.EditMode
{
    /// <summary>验证拆分后的 WorldAdmission 与 Recovery 纯决策组件。</summary>
    internal sealed class WorldStateModuleTests
    {
        /// <summary>World target state machine 只签发合法下一代 lease。</summary>
        [Test]
        public void WorldTargetStateMachineOwnsLegalTransitionAndGeneration()
        {
            var machine = new WorldTargetStateMachine();
            var current = Snapshot(ClientWorldFlowState.OwnWorld, 9);

            Assert.That(
                machine.TryBegin(
                    current,
                    intentActive: false,
                    ClientWorldFlowState.OwnWorld,
                    ClientWorldFlowState.JoiningVisit,
                    sessionGeneration: 4,
                    out var lease),
                Is.True);
            Assert.That(lease.TargetGeneration, Is.EqualTo(10));
            Assert.That(
                machine.CanCommit(
                    Snapshot(ClientWorldFlowState.JoiningVisit, 10),
                    intentActive: true,
                    lease,
                    currentSessionGeneration: 4),
                Is.True);
            Assert.That(
                machine.CanCommit(
                    Snapshot(ClientWorldFlowState.JoiningVisit, 10),
                    intentActive: true,
                    lease,
                    currentSessionGeneration: 5),
                Is.False);
        }

        /// <summary>非法跨 target 迁移和并发 intent 均被拒绝。</summary>
        [Test]
        public void WorldTargetStateMachineRejectsIllegalOrConcurrentTransition()
        {
            var machine = new WorldTargetStateMachine();
            var current = Snapshot(ClientWorldFlowState.OwnWorld, 2);

            Assert.That(
                machine.TryBegin(
                    current,
                    intentActive: true,
                    ClientWorldFlowState.OwnWorld,
                    ClientWorldFlowState.JoiningVisit,
                    1,
                    out _),
                Is.False);
            Assert.That(
                machine.TryBegin(
                    current,
                    intentActive: false,
                    ClientWorldFlowState.OwnWorld,
                    ClientWorldFlowState.RecoveringTarget,
                    1,
                    out _),
                Is.False);
        }

        /// <summary>Accept transport completion 保持 commit-unknown，普通 timeout 保持 transport。</summary>
        [Test]
        public void WorldAdmissionFailureMapperPreservesCommitUnknown()
        {
            var mapper = new WorldAdmissionFailureMapper();
            var timeout = ClientGatewayResult<ClientVisitReservation>.Failed(
                new ClientGatewayFailure(
                    ClientGatewayFailureKind.Timeout,
                    "accept-visit"));

            Assert.That(
                mapper.MapAccept(timeout),
                Is.EqualTo(ClientWorldFlowFailure.CommitUnknown));
            Assert.That(
                mapper.MapGateway(timeout),
                Is.EqualTo(ClientWorldFlowFailure.Transport));
        }

        /// <summary>Recovery state machine 拒绝并发和错误 manual phase。</summary>
        [Test]
        public void RecoveryStateMachineEnforcesSingleFlightAndPhase()
        {
            var machine = new ConnectionRecoveryStateMachine();
            var recovering = RecoverySnapshot(
                ClientConnectionRecoveryPhase.RecoveringWorld,
                intent: 3);
            var lost = RecoverySnapshot(
                ClientConnectionRecoveryPhase.ConnectionLost,
                intent: 4);

            Assert.That(machine.CanBeginControl(recovering, manual: false), Is.False);
            Assert.That(machine.CanBeginGameplay(recovering, manual: false), Is.False);
            Assert.That(machine.CanBeginControl(lost, manual: true), Is.True);
            Assert.That(
                machine.CanCommit(
                    lost,
                    intentGeneration: 4,
                    ClientConnectionRecoveryPhase.ConnectionLost),
                Is.True);
        }

        /// <summary>Plan builder 只选择恢复缺失的 channel，并保持冻结 target。</summary>
        [Test]
        public void RecoveryPlanBuilderChoosesMinimumPlan()
        {
            var builder = new ConnectionRecoveryPlanBuilder();

            Assert.That(
                builder.Build(
                    controlConnected: true,
                    gameplayConnected: true,
                    target: null).Kind,
                Is.EqualTo(ConnectionRecoveryPlanKind.None));
            Assert.That(
                builder.Build(
                    controlConnected: false,
                    gameplayConnected: true,
                    target: null).Kind,
                Is.EqualTo(ConnectionRecoveryPlanKind.ControlOnly));
        }

        /// <summary>创建无业务 identity 的 flow snapshot。</summary>
        private static ClientWorldFlowSnapshot Snapshot(
            ClientWorldFlowState state,
            long generation)
        {
            return new ClientWorldFlowSnapshot(
                state,
                generation,
                ClientWorldFlowFailure.None,
                null,
                null);
        }

        /// <summary>创建低敏 recovery snapshot。</summary>
        private static ClientConnectionRecoverySnapshot RecoverySnapshot(
            ClientConnectionRecoveryPhase phase,
            long intent)
        {
            return new ClientConnectionRecoverySnapshot(
                phase,
                intent,
                sourceChannelGeneration: 1,
                targetGeneration: 1,
                ClientConnectionRecoveryResultKind.None,
                manual: false);
        }
    }
}
