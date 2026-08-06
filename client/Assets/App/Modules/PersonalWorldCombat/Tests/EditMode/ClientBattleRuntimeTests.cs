using System;
using System.Collections.Generic;
using System.Linq;
using System.Net;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using Google.Protobuf;
using IHomeland.Client.PersonalWorldCombat.Application;
using IHomeland.Client.Networking.Application.Contracts;
using IHomeland.Client.Core.Foundation.Lifetime;
using IHomeland.Client.Core.Foundation.Time;
using IHomeland.Client.PersonalWorldCombat.Infrastructure;
using IHomeland.Client.Networking.Infrastructure.Http;
using IHomeland.Protocol.Battle.V1;
using NUnit.Framework;

namespace IHomeland.Client.PersonalWorldCombat.Tests.EditMode
{
    /// <summary>
    /// 验证 client battle policy、movement/jump prediction、replica 与 ticket closed codec。
    /// </summary>
    public sealed class ClientBattleRuntimeTests
    {
        /// <summary>
        /// 确认 runtime policy 精确绑定 current 25/50 ms、history 与 correction 边界。
        /// </summary>
        [Test]
        public void CurrentPolicyMatchesFrozenCadenceAndHistory()
        {
            var policy = ClientBattlePolicy.Current;

            Assert.That(policy.InputStepMilliseconds, Is.EqualTo(25));
            Assert.That(policy.SimulationStepMilliseconds, Is.EqualTo(50));
            Assert.That(policy.InputLeadSimulationTicks, Is.EqualTo(2));
            Assert.That(policy.InputBundleDepth, Is.EqualTo(3));
            Assert.That(policy.InputBundleRedundancy, Is.EqualTo(2));
            Assert.That(policy.ContinuousHoldTicks, Is.EqualTo(4));
            Assert.That(policy.InputGapExpiryTicks, Is.EqualTo(6));
            Assert.That(policy.LastGapExpiredInputTick(7), Is.Zero);
            Assert.That(policy.LastGapExpiredInputTick(10), Is.EqualTo(6));
            Assert.That(policy.HistoryTicks, Is.EqualTo(16));
            Assert.That(policy.CorrectionPositionMillimeters, Is.EqualTo(80));
            Assert.That(policy.CorrectionAngleMillidegrees, Is.EqualTo(2000));
        }

        /// <summary>
        /// 确认 locomotion 只由 immutable Transform 的量化水平速度派生。
        /// </summary>
        [Test]
        public void ActorPresentationDerivesLocomotionFromHorizontalVelocity()
        {
            var verticalOnly = new ClientActorViewState(
                battleGeneration: 1,
                entityID: 1,
                entityGeneration: 1,
                local: true,
                Transform(0, 0, 0, 0, 0, 5000, 0),
                healthMilli: 100000,
                maxHealthMilli: 100000,
                stateFlags: 0,
                archetypeID: ClientBattleContentIdentity.PlayerArchetype,
                equippedWeaponID: ClientBattleContentIdentity.SwordWeapon,
                degraded: false);
            var movingX = new ClientActorViewState(
                battleGeneration: 1,
                entityID: 1,
                entityGeneration: 1,
                local: true,
                Transform(0, 0, 0, 0, 1, 0, 0),
                healthMilli: 100000,
                maxHealthMilli: 100000,
                stateFlags: 0,
                archetypeID: ClientBattleContentIdentity.PlayerArchetype,
                equippedWeaponID: ClientBattleContentIdentity.SwordWeapon,
                degraded: false);
            var movingZ = new ClientActorViewState(
                battleGeneration: 1,
                entityID: 1,
                entityGeneration: 1,
                local: true,
                Transform(0, 0, 0, 0, 0, 0, -1),
                healthMilli: 100000,
                maxHealthMilli: 100000,
                stateFlags: 0,
                archetypeID: ClientBattleContentIdentity.PlayerArchetype,
                equippedWeaponID: ClientBattleContentIdentity.SwordWeapon,
                degraded: false);

            Assert.That(verticalOnly.Moving, Is.False);
            Assert.That(movingX.Moving, Is.True);
            Assert.That(movingZ.Moving, Is.True);
        }

        /// <summary>
        /// 确认 route 3000 只设置 command kind 拥有的 Protobuf optional fields。
        /// </summary>
        [Test]
        public void InputProtocolPreservesClosedOptionalFieldShape()
        {
            var network = new RecordingGameplayPort();
            var prediction = new GameplayPrediction(
                ClientBattlePolicy.Current,
                network);
            prediction.Activate(
                1,
                Transform(0, 0, 0, 0, 0, 0, 0),
                grounded: true,
                latestServerTick: 1);
            Assert.That(
                prediction.TrySetInput(
                    1,
                    new ClientBattleSemanticInput(
                        1000,
                        -1000,
                        -45000,
                        -10000,
                        jumpPressed: true,
                        primaryPressed: true,
                        secondaryPressed: true,
                        interactPressed: true,
                        interactionSlot: 1,
                        switchWeaponPressed: true)),
                Is.True);
            prediction.Tick(0.025f);

            var adapter = new ClientBattleProtocolAdapter();
            var encoded = adapter.EncodeInputBundle(network.Bundles[0]);
            var decoded = BattleInputBundle.Parser.ParseFrom(encoded);
            Assert.That(decoded.Commands, Has.Count.EqualTo(7));
            Assert.That(
                decoded.Commands,
                Has.Exactly(1).Matches<BattleInputCommand>(
                    command => command.Kind ==
                        BattleInputKind.SwitchWeapon));
            foreach (var command in decoded.Commands)
            {
                var hasMove =
                    command.HasMoveXMilli &&
                    command.HasMoveYMilli;
                var hasAim =
                    command.HasAimYawMillidegrees &&
                    command.HasAimPitchMillidegrees;
                switch (command.Kind)
                {
                    case BattleInputKind.Move:
                        Assert.That(hasMove, Is.True);
                        Assert.That(hasAim, Is.False);
                        Assert.That(command.HasInteractionSlot, Is.False);
                        break;
                    case BattleInputKind.Aim:
                        Assert.That(hasMove, Is.False);
                        Assert.That(hasAim, Is.True);
                        Assert.That(
                            command.AimYawMillidegrees,
                            Is.EqualTo(-45000));
                        Assert.That(command.HasInteractionSlot, Is.False);
                        break;
                    case BattleInputKind.Interact:
                        Assert.That(hasMove, Is.False);
                        Assert.That(hasAim, Is.False);
                        Assert.That(command.HasInteractionSlot, Is.True);
                        break;
                    default:
                        Assert.That(hasMove, Is.False);
                        Assert.That(hasAim, Is.False);
                        Assert.That(command.HasInteractionSlot, Is.False);
                        break;
                }
            }
        }

        /// <summary>
        /// 确认 bit 4 是唯一 grounded 事实，并拒绝未知bit、缺失scalar、非法yaw与delta mask漂移。
        /// </summary>
        [Test]
        public void SnapshotProtocolConsumesClosedGroundedRegistry()
        {
            var adapter = new ClientBattleProtocolAdapter();
            var full = new BattleFullSnapshot
            {
                ServerTick = 1,
                SnapshotSequence = 1,
                BaselineId = 1,
                PartitionIndex = 0,
                PartitionCount = 1,
                LastProcessedInputTick = 0,
            };
            full.Entities.Add(new BattleEntityState
            {
                EntityId = 1,
                EntityGeneration = 1,
                Transform = GeneratedTransform(),
                HealthMilli = 100000,
                StateFlags =
                    ClientBattleEntityState.GroundedStateFlag,
                ArchetypeId = ClientBattleContentIdentity.PlayerArchetype,
                EquippedWeaponId = ClientBattleContentIdentity.SwordWeapon,
                MaxHealthMilli = 100000,
            });
            var fullFrame = new ClientBattleRouteFrame(
                ClientBattleRouteCatalog.FullSnapshot,
                applicationSequence: 1,
                partitionIndex: 0,
                partitionCount: 1,
                full.ToByteArray());

            Assert.That(
                adapter.TryDecodeSnapshot(
                    battleGeneration: 1,
                    fullFrame,
                    out var grounded),
                Is.True);
            Assert.That(grounded.Entities[0].Grounded, Is.True);

            var phaseOnly = full.Clone();
            phaseOnly.Entities[0].StateFlags = 1;
            Assert.That(
                adapter.TryDecodeSnapshot(
                    1,
                    new ClientBattleRouteFrame(
                        ClientBattleRouteCatalog.FullSnapshot,
                        2,
                        0,
                        1,
                        phaseOnly.ToByteArray()),
                    out var phase),
                Is.True);
            Assert.That(phase.Entities[0].Grounded, Is.False);

            var unknownFlag = full.Clone();
            unknownFlag.Entities[0].StateFlags = 0x00000020;
            Assert.That(
                adapter.TryDecodeSnapshot(
                    1,
                    new ClientBattleRouteFrame(
                        ClientBattleRouteCatalog.FullSnapshot,
                        3,
                        0,
                        1,
                        unknownFlag.ToByteArray()),
                    out _),
                Is.False);

            var missingScalar = full.Clone();
            missingScalar.Entities[0].Transform.
                ClearVelocityZMmPerSecond();
            Assert.That(
                adapter.TryDecodeSnapshot(
                    1,
                    new ClientBattleRouteFrame(
                        ClientBattleRouteCatalog.FullSnapshot,
                        4,
                        0,
                        1,
                        missingScalar.ToByteArray()),
                    out _),
                Is.False);

            var illegalYaw = full.Clone();
            illegalYaw.Entities[0].Transform.YawMillidegrees =
                180000;
            Assert.That(
                adapter.TryDecodeSnapshot(
                    1,
                    new ClientBattleRouteFrame(
                        ClientBattleRouteCatalog.FullSnapshot,
                        5,
                        0,
                        1,
                        illegalYaw.ToByteArray()),
                    out _),
                Is.False);

            var delta = new BattleDeltaSnapshot
            {
                ServerTick = 2,
                SnapshotSequence = 2,
                BaselineId = 1,
                PartitionIndex = 0,
                PartitionCount = 1,
                LastProcessedInputTick = 0,
            };
            delta.Deltas.Add(new BattleEntityDelta
            {
                EntityId = 1,
                EntityGeneration = 1,
                StateMask =
                    ClientBattleEntityDelta.StateFlagsMask,
                StateFlags =
                    ClientBattleEntityState.GroundedStateFlag,
            });
            Assert.That(
                adapter.TryDecodeSnapshot(
                    1,
                    new ClientBattleRouteFrame(
                        ClientBattleRouteCatalog.DeltaSnapshot,
                        6,
                        0,
                        1,
                        delta.ToByteArray()),
                    out var groundedDelta),
                Is.True);
            Assert.That(
                groundedDelta.Deltas[0].StateFlags,
                Is.EqualTo(
                    ClientBattleEntityState.GroundedStateFlag));

            var nonPlayerWeaponReplacement = delta.Clone();
            nonPlayerWeaponReplacement.Deltas[0].StateMask =
                ClientBattleEntityDelta.EquippedWeaponMask;
            nonPlayerWeaponReplacement.Deltas[0].ClearStateFlags();
            nonPlayerWeaponReplacement.Deltas[0].EquippedWeaponId = 0;
            Assert.That(
                adapter.TryDecodeSnapshot(
                    1,
                    new ClientBattleRouteFrame(
                        ClientBattleRouteCatalog.DeltaSnapshot,
                        7,
                        0,
                        1,
                        nonPlayerWeaponReplacement.ToByteArray()),
                    out var nonPlayerWeaponDelta),
                Is.True);
            Assert.That(
                nonPlayerWeaponDelta.Deltas[0].EquippedWeaponID,
                Is.EqualTo(0));

            var maskDrift = delta.Clone();
            maskDrift.Deltas[0].StateMask =
                ClientBattleEntityDelta.TransformMask;
            Assert.That(
                adapter.TryDecodeSnapshot(
                    1,
                    new ClientBattleRouteFrame(
                        ClientBattleRouteCatalog.DeltaSnapshot,
                        8,
                        0,
                        1,
                        maskDrift.ToByteArray()),
                    out _),
                Is.False);
        }

        /// <summary>
        /// 确认 semantic move/jump 产生有界 bundle 与非权威 local kinematic prediction。
        /// </summary>
        [Test]
        public void PredictionProducesMovementAndGroundedJump()
        {
            var network = new RecordingGameplayPort();
            var prediction = new GameplayPrediction(
                ClientBattlePolicy.Current,
                network);
            prediction.Activate(
                1,
                Transform(0, 0, 0, 0, 0, 0, 0),
                grounded: true,
                latestServerTick: 10);

            Assert.That(
                prediction.TrySetInput(
                    1,
                    new ClientBattleSemanticInput(
                        1000,
                        0,
                        90000,
                        0,
                        jumpPressed: true,
                        primaryPressed: false,
                        secondaryPressed: false,
                        interactPressed: false,
                        interactionSlot: 0)),
                Is.True);
            prediction.Tick(0.025f);

            var snapshot = prediction.Snapshot();
            var firstInputTick =
                ClientBattlePolicy.Current.FirstInputTickAfterBaseline(10);
            Assert.That(network.Bundles.Count, Is.EqualTo(1));
            Assert.That(
                snapshot.LastSentInputTick,
                Is.EqualTo(firstInputTick));
            Assert.That(
                network.Bundles[0].NewestInputTick,
                Is.EqualTo(firstInputTick));
            Assert.That(
                ClientBattlePolicy.Current.MapInputToSimulationTick(
                    firstInputTick),
                Is.EqualTo(
                    10UL +
                    (ulong)ClientBattlePolicy.Current.
                        InputLeadSimulationTicks));
            Assert.That(
                snapshot.State.Transform.PositionXMillimeters,
                Is.GreaterThan(0));
            Assert.That(
                snapshot.State.Transform.PositionYMillimeters,
                Is.GreaterThan(0));
            Assert.That(
                snapshot.State.Transform.VelocityYMillimetersPerSecond,
                Is.EqualTo(ClientBattlePolicy.Current.JumpSpeedMillimetersPerSecond));
            Assert.That(snapshot.State.Grounded, Is.False);
            Assert.That(
                network.Bundles[0].Frames[0].Commands,
                Has.Exactly(1).Matches<ClientBattleInputCommand>(
                    command => command.Kind == ClientBattleInputKind.Jump));
        }

        /// <summary>
        /// 确认10 Hz authority publication之间缺失的SimulationTick按server continuous hold
        /// 重演，prediction-owned表现段以100 ms连续连接horizon且不暴露发送catch-up。
        /// </summary>
        [Test]
        public void PredictionPresentationBridgesTenHertzAuthorityWithHeldTicks()
        {
            var network = new RecordingGameplayPort();
            var prediction = new GameplayPrediction(
                ClientBattlePolicy.Current,
                network);
            prediction.Activate(
                71,
                Transform(0, 0, 0, 0, 0, 0, 0),
                grounded: true,
                latestServerTick: 10);
            Assert.That(
                prediction.TrySetInput(
                    71,
                    new ClientBattleSemanticInput(
                        1000,
                        0,
                        0,
                        0,
                        jumpPressed: false,
                        primaryPressed: false,
                        secondaryPressed: false,
                        interactPressed: false,
                        interactionSlot: 0)),
                Is.True);

            prediction.Tick(0.050f);
            var firstHorizon = prediction.Snapshot();
            Assert.That(
                firstHorizon.State.Transform.PositionXMillimeters,
                Is.EqualTo(150));
            Assert.That(
                firstHorizon.PresentationTransform.PositionXMillimeters,
                Is.Zero,
                "New horizon提交帧不得把send accumulator直接暴露为可见跳跃。");

            prediction.Tick(0.050f);
            var beforePublication = prediction.Snapshot();
            Assert.That(
                beforePublication.PresentationTransform.PositionXMillimeters,
                Is.EqualTo(150));
            Assert.That(network.Bundles, Has.Count.EqualTo(2));

            var firstInputTick =
                ClientBattlePolicy.Current.FirstInputTickAfterBaseline(10);
            prediction.Reconcile(
                71,
                lastProcessedInputTick: firstInputTick + 1,
                latestServerTick: 12,
                Transform(150, 0, 0, 0, 3000, 0, 0),
                grounded: true);
            prediction.Tick(0.0f);
            var secondHorizon = prediction.Snapshot();
            Assert.That(
                network.Bundles,
                Has.Count.EqualTo(3),
                "Authority放行后只能消费一个保留的InputTick credit，禁止同帧burst。");
            Assert.That(
                secondHorizon.State.Transform.PositionXMillimeters,
                Is.EqualTo(450),
                "Sim13必须沿用Sim12的continuous Move，再积分有sample的Sim14。");
            Assert.That(
                secondHorizon.PresentationTransform.PositionXMillimeters,
                Is.EqualTo(150),
                "新horizon必须从current visible pose连续开始。");

            var expectedPresentation = new[] { 225, 300, 375, 450 };
            for (var index = 0; index < expectedPresentation.Length; index++)
            {
                prediction.Tick(0.025f);
                Assert.That(
                    prediction.Snapshot().PresentationTransform.
                        PositionXMillimeters,
                    Is.EqualTo(expectedPresentation[index]),
                    "10 Hz snapshot之间必须保持3 m/s等速render步进。");
            }

            prediction.Reconcile(
                71,
                lastProcessedInputTick:
                    ClientBattlePolicy.Current.
                        FirstInputTickAfterBaseline(12) + 1,
                latestServerTick: 14,
                Transform(450, 0, 0, 0, 3000, 0, 0),
                grounded: true);
            var reconciled = prediction.Snapshot();
            Assert.That(
                reconciled.LastReconciliationPositionDeltaMillimeters,
                Is.Zero,
                "连续hold replay必须消除每个10 Hz publication的一步rebase。");
            Assert.That(
                reconciled.PresentationTransform.PositionXMillimeters,
                Is.EqualTo(450));
        }

        /// <summary>
        /// 确认authority死亡原子丢弃本地预测，并在同generation内永久关闭输入。
        /// </summary>
        [Test]
        public void PredictionAuthorityDeathStopsInputAndDiscardsHistory()
        {
            var network = new RecordingGameplayPort();
            var prediction = new GameplayPrediction(
                ClientBattlePolicy.Current,
                network);
            prediction.Activate(
                1,
                Transform(0, 0, 0, 0, 0, 0, 0),
                grounded: true,
                latestServerTick: 10);
            var input = new ClientBattleSemanticInput(
                1000,
                0,
                0,
                0,
                jumpPressed: false,
                primaryPressed: true,
                secondaryPressed: false,
                interactPressed: false,
                interactionSlot: 0);
            Assert.That(prediction.TrySetInput(1, input), Is.True);
            prediction.Tick(0.025f);
            Assert.That(network.Bundles, Has.Count.EqualTo(1));
            Assert.That(
                prediction.Snapshot().State.Transform.PositionXMillimeters,
                Is.GreaterThan(0));

            var authorityDeath =
                Transform(125, 0, -250, 180000, 0, 0, 0);
            prediction.Reconcile(
                1,
                lastProcessedInputTick: 0,
                latestServerTick: 11,
                authorityDeath,
                grounded: true,
                authorityDead: true);

            var dead = prediction.Snapshot();
            Assert.That(dead.InputEnabled, Is.False);
            Assert.That(dead.BaselineRequired, Is.False);
            Assert.That(dead.InputHistoryItems, Is.Zero);
            Assert.That(dead.PredictedHistoryItems, Is.Zero);
            Assert.That(
                dead.State.Transform.PositionXMillimeters,
                Is.EqualTo(125));
            Assert.That(
                dead.State.Transform.PositionZMillimeters,
                Is.EqualTo(-250));
            Assert.That(prediction.TrySetInput(1, input), Is.False);
            prediction.Tick(0.050f);
            Assert.That(network.Bundles, Has.Count.EqualTo(1));

            prediction.Reconcile(
                1,
                lastProcessedInputTick: 0,
                latestServerTick: 12,
                authorityDeath,
                grounded: true,
                authorityDead: false);
            Assert.That(prediction.Snapshot().InputEnabled, Is.False);
        }

        /// <summary>
        /// 防止25 ms input采样被重复当作两次50 ms authority Movement积分。
        /// </summary>
        [Test]
        public void PredictionFoldsTwoInputTicksIntoOneAuthorityStep()
        {
            var network = new RecordingGameplayPort();
            var prediction = new GameplayPrediction(
                ClientBattlePolicy.Current,
                network);
            prediction.Activate(
                41,
                Transform(0, 0, 0, 0, 0, 0, 0),
                grounded: true,
                latestServerTick: 10);
            Assert.That(
                prediction.TrySetInput(
                    41,
                    new ClientBattleSemanticInput(
                        1000,
                        0,
                        0,
                        0,
                        jumpPressed: false,
                        primaryPressed: false,
                        secondaryPressed: false,
                        interactPressed: false,
                        interactionSlot: 0)),
                Is.True);

            prediction.Tick(0.025f);
            var firstSample = prediction.Snapshot().State;
            prediction.Tick(0.025f);
            var completedGroup = prediction.Snapshot().State;
            Assert.That(
                prediction.Reconcile(
                    41,
                    lastProcessedInputTick: 0,
                    latestServerTick: 11,
                    Transform(0, 0, 0, 0, 0, 0, 0),
                    grounded: true),
                Is.False);
            prediction.Tick(0.025f);
            var nextGroup = prediction.Snapshot().State;

            Assert.That(
                firstSample.Transform.PositionXMillimeters,
                Is.EqualTo(150));
            Assert.That(
                firstSample.Transform.VelocityXMillimetersPerSecond,
                Is.EqualTo(3000));
            Assert.That(
                completedGroup.Transform.PositionXMillimeters,
                Is.EqualTo(150));
            Assert.That(
                completedGroup.Transform.VelocityXMillimetersPerSecond,
                Is.EqualTo(3000));
            Assert.That(
                nextGroup.Transform.PositionXMillimeters,
                Is.EqualTo(300));
        }

        /// <summary>
        /// 保护同一SimulationTick第二个input sample到达的jump edge不会丢失或重复积分。
        /// </summary>
        [Test]
        public void PredictionFoldsLateGroupJumpExactlyOnce()
        {
            var network = new RecordingGameplayPort();
            var prediction = new GameplayPrediction(
                ClientBattlePolicy.Current,
                network);
            prediction.Activate(
                42,
                Transform(0, 0, 0, 0, 0, 0, 0),
                grounded: true,
                latestServerTick: 10);

            prediction.Tick(0.025f);
            Assert.That(prediction.Snapshot().State.Grounded, Is.True);
            Assert.That(
                prediction.TrySetInput(
                    42,
                    new ClientBattleSemanticInput(
                        0,
                        0,
                        0,
                        0,
                        jumpPressed: true,
                        primaryPressed: false,
                        secondaryPressed: false,
                        interactPressed: false,
                        interactionSlot: 0)),
                Is.True);
            prediction.Tick(0.025f);

            var jumpSnapshot = prediction.Snapshot();
            var jumped = jumpSnapshot.State;
            Assert.That(
                jumped.Transform.PositionYMillimeters,
                Is.EqualTo(250));
            Assert.That(
                jumped.Transform.VelocityYMillimetersPerSecond,
                Is.EqualTo(5000));
            Assert.That(jumped.Grounded, Is.False);
            Assert.That(
                jumpSnapshot.LastJumpInputTick,
                Is.EqualTo(
                    ClientBattlePolicy.Current.
                        FirstInputTickAfterBaseline(10) + 1));
            Assert.That(
                jumpSnapshot.LastJumpSimulationTick,
                Is.EqualTo(
                    ClientBattlePolicy.Current.MapInputToSimulationTick(
                        jumpSnapshot.LastJumpInputTick)));
            Assert.That(jumpSnapshot.LastJumpPredicted, Is.True);

            Assert.That(
                prediction.Reconcile(
                    42,
                    lastProcessedInputTick: 0,
                    latestServerTick: 11,
                    Transform(0, 0, 0, 0, 0, 0, 0),
                    grounded: true),
                Is.False);
            prediction.Tick(0.025f);
            var airborne = prediction.Snapshot().State;
            Assert.That(
                airborne.Transform.PositionYMillimeters,
                Is.EqualTo(475));
            Assert.That(
                airborne.Transform.VelocityYMillimetersPerSecond,
                Is.EqualTo(4500));
        }

        /// <summary>
        /// 防止完整authority组确认后重演相同输入时改变local target并造成周期性回拉。
        /// </summary>
        [Test]
        public void PredictionReconcilePreservesExactSimulationStep()
        {
            var network = new RecordingGameplayPort();
            var prediction = new GameplayPrediction(
                ClientBattlePolicy.Current,
                network);
            prediction.Activate(
                43,
                Transform(0, 0, 0, 0, 0, 0, 0),
                grounded: true,
                latestServerTick: 10);
            Assert.That(
                prediction.TrySetInput(
                    43,
                    new ClientBattleSemanticInput(
                        1000,
                        0,
                        0,
                        0,
                        jumpPressed: false,
                        primaryPressed: false,
                        secondaryPressed: false,
                        interactPressed: false,
                        interactionSlot: 0)),
                Is.True);
            prediction.Tick(0.05f);
            Assert.That(
                prediction.Reconcile(
                    43,
                    lastProcessedInputTick: 0,
                    latestServerTick: 11,
                    Transform(0, 0, 0, 0, 0, 0, 0),
                    grounded: true),
                Is.False);
            prediction.Tick(0.05f);

            var before = prediction.Snapshot().State.Transform;
            var firstInputTick =
                ClientBattlePolicy.Current.FirstInputTickAfterBaseline(10);
            Assert.That(
                prediction.Reconcile(
                    43,
                    lastProcessedInputTick: firstInputTick + 1,
                    latestServerTick: 12,
                    Transform(150, 0, 0, 0, 3000, 0, 0),
                    grounded: true),
                Is.False);
            var reconciled = prediction.Snapshot();
            var after = reconciled.State.Transform;

            Assert.That(before.PositionXMillimeters, Is.EqualTo(300));
            Assert.That(reconciled.ReconciliationCount, Is.EqualTo(2));
            Assert.That(
                reconciled.LastReconciliationPositionDeltaMillimeters,
                Is.Zero);
            Assert.That(
                reconciled.MaximumReconciliationPositionDeltaMillimeters,
                Is.Zero);
            Assert.That(
                reconciled.LastReconciliationYawDeltaMillidegrees,
                Is.Zero);
            Assert.That(
                after.PositionXMillimeters,
                Is.EqualTo(before.PositionXMillimeters));
            Assert.That(
                after.PositionYMillimeters,
                Is.EqualTo(before.PositionYMillimeters));
            Assert.That(
                after.PositionZMillimeters,
                Is.EqualTo(before.PositionZMillimeters));
            Assert.That(
                after.VelocityXMillimetersPerSecond,
                Is.EqualTo(before.VelocityXMillimetersPerSecond));
            Assert.That(
                after.VelocityYMillimetersPerSecond,
                Is.EqualTo(before.VelocityYMillimetersPerSecond));
            Assert.That(
                after.VelocityZMillimetersPerSecond,
                Is.EqualTo(before.VelocityZMillimetersPerSecond));
        }

        /// <summary>
        /// 防止连续ack gap保留的旧frame从已包含这些Tick的authority state再次积分。
        /// </summary>
        [Test]
        public void PredictionDoesNotReplayHistoryInsideAuthorityHorizon()
        {
            var network = new RecordingGameplayPort();
            var prediction = new GameplayPrediction(
                ClientBattlePolicy.Current,
                network);
            prediction.Activate(
                44,
                Transform(0, 0, 0, 0, 0, 0, 0),
                grounded: true,
                latestServerTick: 10);
            Assert.That(
                prediction.TrySetInput(
                    44,
                    new ClientBattleSemanticInput(
                        1000,
                        0,
                        0,
                        0,
                        jumpPressed: false,
                        primaryPressed: false,
                        secondaryPressed: false,
                        interactPressed: false,
                        interactionSlot: 0)),
                Is.True);
            prediction.Tick(0.05f);
            Assert.That(
                prediction.Reconcile(
                    44,
                    lastProcessedInputTick: 0,
                    latestServerTick: 11,
                    Transform(0, 0, 0, 0, 0, 0, 0),
                    grounded: true),
                Is.False);
            prediction.Tick(0.05f);
            Assert.That(
                prediction.Reconcile(
                    44,
                    lastProcessedInputTick: 0,
                    latestServerTick: 12,
                    Transform(150, 0, 0, 0, 3000, 0, 0),
                    grounded: true),
                Is.False);
            prediction.Tick(0.05f);

            var before = prediction.Snapshot();
            Assert.That(
                before.State.Transform.PositionXMillimeters,
                Is.EqualTo(450));
            Assert.That(
                prediction.Reconcile(
                    44,
                    lastProcessedInputTick: 0,
                    latestServerTick: 13,
                    Transform(300, 0, 0, 0, 3000, 0, 0),
                    grounded: true),
                Is.False);

            var after = prediction.Snapshot();
            Assert.That(after.InputHistoryItems, Is.EqualTo(6));
            Assert.That(
                after.State.Transform.PositionXMillimeters,
                Is.EqualTo(before.State.Transform.PositionXMillimeters));
            Assert.That(
                after.LastReconciliationPositionDeltaMillimeters,
                Is.Zero);
        }

        /// <summary>
        /// 确认本地InputTick落后authority时只前向对齐，且Jump进入首个future单帧bundle。
        /// </summary>
        [Test]
        public void PredictionRealignsDriftedInputAndPreservesJumpEdge()
        {
            var network = new RecordingGameplayPort();
            var prediction = new GameplayPrediction(
                ClientBattlePolicy.Current,
                network);
            prediction.Activate(
                45,
                Transform(0, 0, 0, 0, 0, 0, 0),
                grounded: true,
                latestServerTick: 10);
            prediction.Tick(0.025f);
            var staleInputTick = prediction.Snapshot().LastSentInputTick;
            Assert.That(
                ClientBattlePolicy.Current.MapInputToSimulationTick(
                    staleInputTick),
                Is.EqualTo(12));
            Assert.That(
                prediction.TrySetInput(
                    45,
                    new ClientBattleSemanticInput(
                        moveXMilli: 1000,
                        moveYMilli: 0,
                        aimYawMillidegrees: 12000,
                        aimPitchMillidegrees: 0,
                        jumpPressed: true,
                        primaryPressed: false,
                        secondaryPressed: false,
                        interactPressed: false,
                        interactionSlot: 0)),
                Is.True);

            Assert.That(
                prediction.Reconcile(
                    45,
                    lastProcessedInputTick: 0,
                    latestServerTick: 20,
                    Transform(0, 0, 0, 0, 0, 0, 0),
                    grounded: true),
                Is.False);
            Assert.That(
                prediction.Reconcile(
                    45,
                    lastProcessedInputTick:
                        ClientBattlePolicy.Current.
                            FirstInputTickAfterBaseline(20) - 1,
                    latestServerTick: 21,
                    Transform(0, 0, 0, 0, 0, 0, 0),
                    grounded: true),
                Is.False);
            prediction.Tick(0.025f);

            var bundle = network.Bundles[network.Bundles.Count - 1];
            var frame = bundle.Frames[bundle.Frames.Count - 1];
            var snapshot = prediction.Snapshot();
            Assert.That(bundle.Frames, Has.Count.EqualTo(1));
            Assert.That(
                frame.InputTick,
                Is.EqualTo(
                    ClientBattlePolicy.Current.
                        FirstInputTickAfterBaseline(21)));
            Assert.That(
                frame.SimulationTick,
                Is.EqualTo(
                    21UL +
                    (ulong)ClientBattlePolicy.Current.
                        InputLeadSimulationTicks));
            Assert.That(
                frame.Commands,
                Has.Some.Matches<ClientBattleInputCommand>(
                    command =>
                        command.Kind == ClientBattleInputKind.Move &&
                        command.MoveXMilli == 1000));
            Assert.That(
                frame.Commands,
                Has.Some.Matches<ClientBattleInputCommand>(
                    command =>
                        command.Kind == ClientBattleInputKind.Aim &&
                        command.AimYawMillidegrees == 12000));
            Assert.That(
                frame.Commands,
                Has.Exactly(1).Matches<ClientBattleInputCommand>(
                    command =>
                        command.Kind == ClientBattleInputKind.Jump));
            Assert.That(snapshot.LastJumpPredicted, Is.True);
            Assert.That(snapshot.State.Grounded, Is.False);
            Assert.That(
                snapshot.State.Transform.VelocityYMillimetersPerSecond,
                Is.EqualTo(
                    ClientBattlePolicy.Current.
                        JumpSpeedMillimetersPerSecond));
        }

        /// <summary>
        /// 确认本地clock快于authority时不发送TooEarly输入，且等待期间保留Jump edge。
        /// </summary>
        [Test]
        public void PredictionPacesAheadClockAtAuthorityHorizon()
        {
            var network = new RecordingGameplayPort();
            var prediction = new GameplayPrediction(
                ClientBattlePolicy.Current,
                network);
            prediction.Activate(
                47,
                Transform(0, 0, 0, 0, 0, 0, 0),
                grounded: true,
                latestServerTick: 10);
            prediction.Tick(0.05f);
            Assert.That(network.Bundles, Has.Count.EqualTo(2));
            Assert.That(
                network.Bundles[network.Bundles.Count - 1].
                    Frames[
                        network.Bundles[network.Bundles.Count - 1].
                            Frames.Count - 1].
                    SimulationTick,
                Is.EqualTo(12));

            Assert.That(
                prediction.TrySetInput(
                    47,
                    new ClientBattleSemanticInput(
                        moveXMilli: 1000,
                        moveYMilli: 0,
                        aimYawMillidegrees: 15000,
                        aimPitchMillidegrees: 0,
                        jumpPressed: true,
                        primaryPressed: false,
                        secondaryPressed: false,
                        interactPressed: false,
                        interactionSlot: 0)),
                Is.True);
            prediction.Tick(0.2f);
            var waiting = prediction.Snapshot();
            Assert.That(network.Bundles, Has.Count.EqualTo(2));
            Assert.That(waiting.ClockOverrun, Is.False);
            Assert.That(waiting.BaselineRequired, Is.False);
            Assert.That(waiting.InputEnabled, Is.True);

            Assert.That(
                prediction.Reconcile(
                    47,
                    lastProcessedInputTick: 0,
                    latestServerTick: 11,
                    Transform(0, 0, 0, 0, 0, 0, 0),
                    grounded: true),
                Is.False);
            prediction.Tick(0.0f);

            var resumed =
                network.Bundles[network.Bundles.Count - 1];
            Assert.That(
                network.Bundles,
                Has.Count.EqualTo(3),
                "Authority pacing只能保留一个InputTick credit，禁止放行帧catch-up burst。");
            Assert.That(
                resumed.Frames[resumed.Frames.Count - 1].
                    SimulationTick,
                Is.EqualTo(13));
            Assert.That(
                resumed.Frames.SelectMany(frame => frame.Commands),
                Has.Exactly(1).Matches<ClientBattleInputCommand>(
                    command =>
                        command.Kind ==
                        ClientBattleInputKind.Jump));
            Assert.That(
                prediction.Snapshot().LastJumpPredicted,
                Is.True);
        }

        /// <summary>
        /// 确认长帧不会产生 catch-up burst，而是关闭 input 并要求新 baseline。
        /// </summary>
        [Test]
        public void PredictionOverrunFailsClosedWithoutBurst()
        {
            var network = new RecordingGameplayPort();
            var prediction = new GameplayPrediction(
                ClientBattlePolicy.Current,
                network);
            prediction.Activate(
                3,
                Transform(0, 0, 0, 0, 0, 0, 0),
                grounded: true,
                latestServerTick: 20);

            prediction.Tick(0.2f);

            var snapshot = prediction.Snapshot();
            Assert.That(network.Bundles, Is.Empty);
            Assert.That(snapshot.ClockOverrun, Is.True);
            Assert.That(snapshot.BaselineRequired, Is.True);
            Assert.That(snapshot.InputEnabled, Is.False);
        }

        /// <summary>
        /// 确认长帧后的 authority frontier 可重建连续性而不会被误判为 future ack。
        /// </summary>
        [Test]
        public void PredictionOverrunReanchorsFromCurrentAuthority()
        {
            var network = new RecordingGameplayPort();
            var prediction = new GameplayPrediction(
                ClientBattlePolicy.Current,
                network);
            prediction.Activate(
                31,
                Transform(0, 0, 0, 0, 0, 0, 0),
                grounded: true,
                latestServerTick: 20);
            prediction.Tick(0.025f);
            var oldSent = prediction.Snapshot().LastSentInputTick;

            Assert.That(
                prediction.TrySetInput(
                    31,
                    new ClientBattleSemanticInput(
                        moveXMilli: 0,
                        moveYMilli: 0,
                        aimYawMillidegrees: 0,
                        aimPitchMillidegrees: 0,
                        jumpPressed: true,
                        primaryPressed: false,
                        secondaryPressed: false,
                        interactPressed: false,
                        interactionSlot: 0)),
                Is.True);
            prediction.Tick(0.2f);
            var serverFinalized = oldSent + 5;
            Assert.That(
                prediction.Reconcile(
                    31,
                    serverFinalized,
                    latestServerTick: 22,
                    Transform(250, 0, 0, 0, 0, 0, 0),
                    grounded: true),
                Is.True);

            var recovered = prediction.Snapshot();
            Assert.That(recovered.BaselineRequired, Is.False);
            Assert.That(recovered.InputEnabled, Is.True);
            Assert.That(recovered.ClockOverrun, Is.False);
            Assert.That(
                recovered.LastAcknowledgedInputTick,
                Is.EqualTo(serverFinalized));
            Assert.That(
                recovered.ContinuityAcknowledgementAnchor,
                Is.EqualTo(serverFinalized));
            Assert.That(recovered.LastSentInputTick, Is.EqualTo(oldSent));
            Assert.That(recovered.InputHistoryItems, Is.Zero);

            prediction.Tick(0.025f);
            Assert.That(
                prediction.Snapshot().LastSentInputTick,
                Is.EqualTo(oldSent));
            Assert.That(
                prediction.Reconcile(
                    31,
                    serverFinalized,
                    latestServerTick: 23,
                    Transform(250, 0, 0, 0, 0, 0, 0),
                    grounded: true),
                Is.False);
            prediction.Tick(0.0f);
            Assert.That(
                prediction.Snapshot().LastSentInputTick,
                Is.GreaterThan(serverFinalized));
            Assert.That(
                network.Bundles[network.Bundles.Count - 1]
                    .Frames[
                        network.Bundles[network.Bundles.Count - 1]
                            .Frames.Count - 1]
                    .Commands,
                Has.Some.Matches<ClientBattleInputCommand>(
                    command =>
                        command.Kind ==
                        ClientBattleInputKind.Jump));
        }

        /// <summary>
        /// 确认re-anchor authority落后已发送frontier时不会回退或复用旧InputTick。
        /// </summary>
        [Test]
        public void PredictionReanchorNeverRegressesPastSentFrontier()
        {
            var network = new RecordingGameplayPort();
            var prediction = new GameplayPrediction(
                ClientBattlePolicy.Current,
                network);
            prediction.Activate(
                46,
                Transform(0, 0, 0, 0, 0, 0, 0),
                grounded: true,
                latestServerTick: 10);
            prediction.Tick(0.05f);
            Assert.That(
                prediction.Reconcile(
                    46,
                    lastProcessedInputTick: 0,
                    latestServerTick: 11,
                    Transform(0, 0, 0, 0, 0, 0, 0),
                    grounded: true),
                Is.False);
            prediction.Tick(0.05f);
            Assert.That(
                prediction.Reconcile(
                    46,
                    lastProcessedInputTick: 0,
                    latestServerTick: 12,
                    Transform(0, 0, 0, 0, 0, 0, 0),
                    grounded: true),
                Is.False);
            prediction.Tick(0.05f);
            Assert.That(
                prediction.Reconcile(
                    46,
                    lastProcessedInputTick: 0,
                    latestServerTick: 13,
                    Transform(0, 0, 0, 0, 0, 0, 0),
                    grounded: true),
                Is.False);

            var oldSent = prediction.Snapshot().LastSentInputTick;
            prediction.Tick(0.2f);
            Assert.That(
                prediction.Reconcile(
                    46,
                    lastProcessedInputTick: 0,
                    latestServerTick: 13,
                    Transform(0, 0, 0, 0, 0, 0, 0),
                    grounded: true),
                Is.False);
            var reanchored = prediction.Snapshot();
            Assert.That(
                reanchored.AcknowledgementAcceptanceFrontier,
                Is.GreaterThanOrEqualTo(oldSent));

            prediction.Tick(0.025f);
            var successorBundle =
                network.Bundles[network.Bundles.Count - 1];
            var successorFrame = successorBundle.Frames[0];
            Assert.That(
                successorBundle.Frames,
                Has.Count.EqualTo(1));
            Assert.That(
                successorFrame.InputTick,
                Is.EqualTo(oldSent + 1));
        }

        /// <summary>
        /// 确认同actor successor继承ack锚点，并接受严格gap-expiry边界内的后续终结。
        /// </summary>
        [Test]
        public void PredictionSuccessorActivationSeedsAuthorityAcknowledgement()
        {
            var network = new RecordingGameplayPort();
            var prediction = new GameplayPrediction(
                ClientBattlePolicy.Current,
                network);
            prediction.Activate(
                52,
                Transform(1000, 0, 0, 0, 0, 0, 0),
                grounded: true,
                latestServerTick: 100,
                lastProcessedInputTick: 175);

            var activated = prediction.Snapshot();
            Assert.That(
                activated.LastAcknowledgedInputTick,
                Is.EqualTo(175));
            Assert.That(
                activated.ContinuityAcknowledgementAnchor,
                Is.EqualTo(175));
            Assert.That(activated.InputHistoryItems, Is.Zero);
            Assert.That(activated.PredictedHistoryItems, Is.Zero);
            Assert.That(activated.InputEnabled, Is.True);

            prediction.Tick(0.025f);
            Assert.That(network.Bundles, Has.Count.EqualTo(1));
            Assert.That(
                prediction.Reconcile(
                    52,
                    lastProcessedInputTick: 206,
                    latestServerTick: 110,
                    Transform(1000, 0, 0, 0, 0, 0, 0),
                    grounded: true),
                Is.False);
            var expired = prediction.Snapshot();
            Assert.That(expired.InputEnabled, Is.True);
            Assert.That(
                expired.LastAcknowledgedInputTick,
                Is.EqualTo(206));
            Assert.That(expired.InputHistoryItems, Is.Zero);

            prediction.Tick(0.025f);
            Assert.That(network.Bundles, Has.Count.EqualTo(2));
            Assert.That(
                network.Bundles[1].NewestInputTick,
                Is.GreaterThan(206));
        }

        /// <summary>
        /// 确认超过sent、显式skip与严格gap-expiry上限的future ack继续fail closed。
        /// </summary>
        [Test]
        public void PredictionRejectsAcknowledgementBeyondAllLegalFrontiers()
        {
            var prediction = new GameplayPrediction(
                ClientBattlePolicy.Current,
                new RecordingGameplayPort());
            prediction.Activate(
                53,
                Transform(0, 0, 0, 0, 0, 0, 0),
                grounded: true,
                latestServerTick: 100);

            Assert.That(
                () => prediction.Reconcile(
                    53,
                    lastProcessedInputTick: 203,
                    latestServerTick: 101,
                    Transform(0, 0, 0, 0, 0, 0, 0),
                    grounded: true),
                Throws.TypeOf<ClientBattlePredictionProtocolException>());
            Assert.That(prediction.Snapshot().InputEnabled, Is.False);
            Assert.That(prediction.Snapshot().BaselineRequired, Is.True);
        }

        /// <summary>
        /// 确认future frame前的无sample Tick先按server规则完成落地，随后有Move的Tick
        /// 才执行水平积分，并继续复现百万分比crossing与toward-zero整数落点。
        /// </summary>
        [Test]
        public void PredictionLandingStopsAtCurrentFlatGround()
        {
            var network = new RecordingGameplayPort();
            var prediction = new GameplayPrediction(
                ClientBattlePolicy.Current,
                network);
            prediction.Activate(
                32,
                Transform(0, 10, 0, 0, 0, -1000, 0),
                grounded: false,
                latestServerTick: 20);
            Assert.That(
                prediction.TrySetInput(
                    32,
                    new ClientBattleSemanticInput(
                        1000,
                        0,
                        0,
                        0,
                        jumpPressed: false,
                        primaryPressed: false,
                        secondaryPressed: false,
                        interactPressed: false,
                        interactionSlot: 0)),
                Is.True);

            prediction.Tick(0.025f);

            var landed = prediction.Snapshot().State;
            Assert.That(
                landed.Transform.PositionYMillimeters,
                Is.EqualTo(1));
            Assert.That(
                landed.Transform.PositionXMillimeters,
                Is.EqualTo(150));
            Assert.That(
                landed.Transform.VelocityYMillimetersPerSecond,
                Is.Zero);
            Assert.That(
                landed.Transform.VelocityXMillimetersPerSecond,
                Is.EqualTo(3000));
            Assert.That(landed.Grounded, Is.True);
        }

        /// <summary>
        /// 确认连续输入按1/2/3深度发送，ack只裁剪已确认历史且回退会fail closed。
        /// </summary>
        [Test]
        public void PredictionBundlesAreBoundedAndAcknowledgementIsMonotonic()
        {
            var network = new RecordingGameplayPort();
            var prediction = new GameplayPrediction(
                ClientBattlePolicy.Current,
                network);
            prediction.Activate(
                4,
                Transform(0, 0, 0, 0, 0, 0, 0),
                grounded: true,
                latestServerTick: 30);
            Assert.That(
                prediction.TrySetInput(
                    4,
                    new ClientBattleSemanticInput(
                        1000,
                        0,
                        0,
                        0,
                        jumpPressed: false,
                        primaryPressed: false,
                        secondaryPressed: false,
                        interactPressed: false,
                        interactionSlot: 0)),
                Is.True);

            prediction.Tick(0.05f);
            Assert.That(
                prediction.Reconcile(
                    4,
                    lastProcessedInputTick: 0,
                    latestServerTick: 31,
                    Transform(0, 0, 0, 0, 0, 0, 0),
                    grounded: true),
                Is.False);
            prediction.Tick(0.05f);

            Assert.That(
                network.Bundles.ConvertAll(bundle => bundle.Frames.Count),
                Is.EqualTo(new[] { 1, 2, 3, 3 }));
            var firstInputTick =
                ClientBattlePolicy.Current.FirstInputTickAfterBaseline(30);
            Assert.That(
                prediction.Reconcile(
                    4,
                    lastProcessedInputTick: firstInputTick + 1,
                    latestServerTick: 32,
                    Transform(1000, 0, 0, 0, 0, 0, 0),
                    grounded: true),
                Is.True);
            var acknowledged = prediction.Snapshot();
            Assert.That(
                acknowledged.LastSentInputTick,
                Is.EqualTo(firstInputTick + 3));
            Assert.That(
                acknowledged.LastAcknowledgedInputTick,
                Is.EqualTo(firstInputTick + 1));
            Assert.That(acknowledged.InputHistoryItems, Is.EqualTo(2));
            Assert.That(acknowledged.PredictedHistoryItems, Is.EqualTo(2));

            Assert.That(
                () => prediction.Reconcile(
                    4,
                    lastProcessedInputTick: firstInputTick,
                    latestServerTick: 33,
                    Transform(1000, 0, 0, 0, 0, 0, 0),
                    grounded: true),
                Throws.TypeOf<ClientBattlePredictionProtocolException>());
            var rejected = prediction.Snapshot();
            Assert.That(rejected.BaselineRequired, Is.True);
            Assert.That(rejected.InputEnabled, Is.False);
        }

        /// <summary>
        /// 确认16 Tick历史达到硬上限后不会扩容，并由successor generation清空旧历史。
        /// </summary>
        [Test]
        public void PredictionHistoryOverflowFailsClosedAndSuccessorResets()
        {
            var network = new RecordingGameplayPort();
            var prediction = new GameplayPrediction(
                ClientBattlePolicy.Current,
                network);
            prediction.Activate(
                6,
                Transform(0, 0, 0, 0, 0, 0, 0),
                grounded: true,
                latestServerTick: 40);

            var latestServerTick = 40UL;
            for (var index = 0;
                 index < ClientBattlePolicy.Current.HistoryTicks + 1;
                 index++)
            {
                if (index > 0 && index % 2 == 0)
                {
                    latestServerTick++;
                    Assert.That(
                        prediction.Reconcile(
                            6,
                            lastProcessedInputTick: 0,
                            latestServerTick: latestServerTick,
                            Transform(0, 0, 0, 0, 0, 0, 0),
                            grounded: true),
                        Is.False);
                }

                prediction.Tick(0.025f);
            }

            var overflow = prediction.Snapshot();
            Assert.That(
                network.Bundles.Count,
                Is.EqualTo(ClientBattlePolicy.Current.HistoryTicks));
            Assert.That(
                overflow.InputHistoryItems,
                Is.Zero);
            Assert.That(overflow.PredictedHistoryItems, Is.Zero);
            Assert.That(overflow.BaselineRequired, Is.True);
            Assert.That(overflow.InputEnabled, Is.False);

            prediction.Deactivate(6);
            prediction.Activate(
                7,
                Transform(0, 0, 0, 0, 0, 0, 0),
                grounded: false,
                latestServerTick: 50);
            var successor = prediction.Snapshot();
            Assert.That(successor.BattleGeneration, Is.EqualTo(7));
            Assert.That(successor.LastSentInputTick, Is.Zero);
            Assert.That(successor.InputHistoryItems, Is.Zero);
            Assert.That(successor.PredictedHistoryItems, Is.Zero);
            Assert.That(successor.BaselineRequired, Is.False);
        }

        /// <summary>
        /// 确认 full snapshot 只在完整集合后发布，并严格使用 actor_slot + 1 local identity。
        /// </summary>
        [Test]
        public void ReplicaPublishesFullBaselineWithBoundLocalActor()
        {
            var network = new RecordingGameplayPort();
            var replica = new GameplayReplica(
                ClientBattlePolicy.Current,
                network);
            replica.Activate(5, actorSlot: 0);
            var entities = new[]
            {
                Entity(1, 1, 10),
                Entity(2, 1, 20),
            };
            var partition = new ClientBattleSnapshotPartition(
                5,
                ClientBattleSnapshotKind.Full,
                serverTick: 100,
                snapshotSequence: 1,
                baselineID: 7,
                partitionIndex: 0,
                partitionCount: 1,
                lastProcessedInputTick: 0,
                entities,
                Array.Empty<ClientBattleEntityDelta>());

            Assert.That(
                replica.AcceptSnapshot(partition),
                Is.EqualTo(ClientBattleSnapshotCommit.Published));
            var snapshot = replica.Snapshot();
            Assert.That(snapshot.LocalEntityID, Is.EqualTo(1));
            Assert.That(snapshot.TryGetLocalEntity(out var local), Is.True);
            Assert.That(local.EntityID, Is.EqualTo(1));
            Assert.That(snapshot.Entities.Count, Is.EqualTo(2));
            Assert.That(snapshot.Entities[0].EntityID, Is.EqualTo(1));
            Assert.That(snapshot.Entities[1].EntityID, Is.EqualTo(2));
        }

        /// <summary>
        /// 确认actor slot七只绑定entity八，缺失该identity的full snapshot不能提交。
        /// </summary>
        [Test]
        public void ReplicaRejectsFullSnapshotWithoutExactSlotIdentity()
        {
            var replica = new GameplayReplica(
                ClientBattlePolicy.Current,
                new RecordingGameplayPort());
            replica.Activate(8, actorSlot: 7);

            Assert.That(
                () => replica.AcceptSnapshot(
                    FullPartition(
                        battleGeneration: 8,
                        serverTick: 60,
                        snapshotSequence: 1,
                        baselineID: 9,
                        partitionIndex: 0,
                        partitionCount: 1,
                        Entity(1, 1, 10),
                        Entity(7, 1, 70))),
                Throws.TypeOf<ClientBattleReplicaProtocolException>());
        }

        /// <summary>
        /// 确认乱序分区只在齐全后发布，重复分区幂等且不会产生中间replica。
        /// </summary>
        [Test]
        public void ReplicaPublishesOutOfOrderPartitionsExactlyOnce()
        {
            var replica = new GameplayReplica(
                ClientBattlePolicy.Current,
                new RecordingGameplayPort());
            replica.Activate(9, actorSlot: 0);
            var second = FullPartition(
                battleGeneration: 9,
                serverTick: 70,
                snapshotSequence: 1,
                baselineID: 10,
                partitionIndex: 1,
                partitionCount: 2,
                Entity(2, 1, 20));
            var first = FullPartition(
                battleGeneration: 9,
                serverTick: 70,
                snapshotSequence: 1,
                baselineID: 10,
                partitionIndex: 0,
                partitionCount: 2,
                Entity(1, 1, 10));

            Assert.That(
                replica.AcceptSnapshot(second),
                Is.EqualTo(ClientBattleSnapshotCommit.Pending));
            Assert.That(replica.Snapshot().Entities, Is.Empty);
            Assert.That(
                replica.AcceptSnapshot(second),
                Is.EqualTo(ClientBattleSnapshotCommit.Duplicate));
            Assert.That(
                replica.AcceptSnapshot(first),
                Is.EqualTo(ClientBattleSnapshotCommit.Published));
            Assert.That(replica.Snapshot().Entities.Count, Is.EqualTo(2));
            Assert.That(
                replica.AcceptSnapshot(first),
                Is.EqualTo(ClientBattleSnapshotCommit.Duplicate));
        }

        /// <summary>
        /// 确认同一partition index的内容冲突会终结集合，不能以latest-wins掩盖authority漂移。
        /// </summary>
        [Test]
        public void ReplicaRejectsConflictingSnapshotPartition()
        {
            var replica = new GameplayReplica(
                ClientBattlePolicy.Current,
                new RecordingGameplayPort());
            replica.Activate(10, actorSlot: 0);
            var first = FullPartition(
                battleGeneration: 10,
                serverTick: 75,
                snapshotSequence: 1,
                baselineID: 11,
                partitionIndex: 0,
                partitionCount: 2,
                Entity(1, 1, 10));
            var conflict = FullPartition(
                battleGeneration: 10,
                serverTick: 75,
                snapshotSequence: 1,
                baselineID: 11,
                partitionIndex: 0,
                partitionCount: 2,
                Entity(1, 1, 11));

            Assert.That(
                replica.AcceptSnapshot(first),
                Is.EqualTo(ClientBattleSnapshotCommit.Pending));
            Assert.That(
                () => replica.AcceptSnapshot(conflict),
                Throws.TypeOf<ClientBattleReplicaProtocolException>());
            Assert.That(replica.Snapshot().Entities, Is.Empty);
        }

        /// <summary>
        /// 确认未知baseline与entity generation gap共享唯一current resync intent。
        /// </summary>
        [Test]
        public void ReplicaRequestsSingleFlightResyncForBaselineAndGenerationGaps()
        {
            var network = new RecordingGameplayPort();
            var replica = new GameplayReplica(
                ClientBattlePolicy.Current,
                network);
            replica.Activate(10, actorSlot: 0);
            var missingBaseline = new ClientBattleSnapshotPartition(
                10,
                ClientBattleSnapshotKind.Delta,
                serverTick: 80,
                snapshotSequence: 1,
                baselineID: 404,
                partitionIndex: 0,
                partitionCount: 1,
                lastProcessedInputTick: 0,
                Array.Empty<ClientBattleEntityState>(),
                new[]
                {
                    new ClientBattleEntityDelta(
                        1,
                        1,
                        ClientBattleEntityDelta.HealthMask,
                        transform: null,
                        healthMilli: 90000,
                        stateFlags: null),
                });

            Assert.That(
                replica.AcceptSnapshot(missingBaseline),
                Is.EqualTo(ClientBattleSnapshotCommit.ResyncRequested));
            Assert.That(network.ResyncRequests.Count, Is.EqualTo(1));
            Assert.That(
                network.ResyncRequests[0].Reason,
                Is.EqualTo(ClientBattleResyncReason.MissingBaseline));

            replica.AcceptLifecycle(
                new ClientBattleEntityLifecycle(
                    10,
                    eventID: 2,
                    serverTick: 81,
                    entityID: 2,
                    entityGeneration: 2,
                    ClientBattleEntityLifecycleKind.Spawn,
                    archetypeID: 1,
                    Entity(2, 2, 20)));
            Assert.That(network.ResyncRequests.Count, Is.EqualTo(1));
            Assert.That(replica.Snapshot().ResyncPending, Is.True);
        }

        /// <summary>
        /// 确认过期baseline、generation gap与rate-limit retry都复用一个有界resync intent。
        /// </summary>
        [Test]
        public void ReplicaExpiresBaselineAndRateLimitsSingleFlightResync()
        {
            var network = new RecordingGameplayPort();
            var replica = new GameplayReplica(
                ClientBattlePolicy.Current,
                network);
            replica.Activate(12, actorSlot: 0);
            Assert.That(
                replica.AcceptSnapshot(
                    FullPartition(
                        battleGeneration: 12,
                        serverTick: 1,
                        snapshotSequence: 1,
                        baselineID: 12,
                        partitionIndex: 0,
                        partitionCount: 1,
                        Entity(1, 1, 10),
                        Entity(2, 1, 20))),
                Is.EqualTo(ClientBattleSnapshotCommit.Published));

            var expired = DeltaPartition(
                battleGeneration: 12,
                serverTick: 42,
                snapshotSequence: 2,
                baselineID: 12,
                new ClientBattleEntityDelta(
                    entityID: 2,
                    generation: 1,
                    ClientBattleEntityDelta.HealthMask,
                    transform: null,
                    healthMilli: 90000,
                    stateFlags: null));
            Assert.That(
                replica.AcceptSnapshot(expired),
                Is.EqualTo(ClientBattleSnapshotCommit.ResyncRequested));
            Assert.That(network.ResyncRequests.Count, Is.EqualTo(1));
            Assert.That(
                network.ResyncRequests[0].Reason,
                Is.EqualTo(ClientBattleResyncReason.BaselineExpired));

            replica.AcceptLifecycle(
                new ClientBattleEntityLifecycle(
                    12,
                    eventID: 1,
                    serverTick: 43,
                    entityID: 3,
                    entityGeneration: 2,
                    ClientBattleEntityLifecycleKind.Spawn,
                    archetypeID: 1,
                    Entity(3, 2, 30)));
            Assert.That(network.ResyncRequests.Count, Is.EqualTo(1));

            var firstRequest = network.ResyncRequests[0];
            replica.AcceptResyncResponse(
                new ClientBattleResyncResponse(
                    12,
                    firstRequest.RequestSequence,
                    serverTick: 44,
                    ClientBattleResyncDisposition.RateLimited,
                    scheduledBaselineID: 0,
                    retryAfterMilliseconds: 25),
                utcNowMilliseconds: 1000);
            Assert.That(replica.TickResync(1024), Is.False);
            Assert.That(replica.TickResync(1025), Is.True);
            Assert.That(network.ResyncRequests.Count, Is.EqualTo(2));
            Assert.That(
                network.ResyncRequests[1].RequestSequence,
                Is.GreaterThan(firstRequest.RequestSequence));
            Assert.That(
                network.ResyncRequests[1].Reason,
                Is.EqualTo(ClientBattleResyncReason.BaselineExpired));
        }

        /// <summary>
        /// 确认delta entity generation漂移不产生部分提交，并只触发current resync。
        /// </summary>
        [Test]
        public void ReplicaRejectsDeltaEntityGenerationDriftAtomically()
        {
            var network = new RecordingGameplayPort();
            var replica = new GameplayReplica(
                ClientBattlePolicy.Current,
                network);
            replica.Activate(13, actorSlot: 0);
            Assert.That(
                replica.AcceptSnapshot(
                    FullPartition(
                        battleGeneration: 13,
                        serverTick: 10,
                        snapshotSequence: 1,
                        baselineID: 13,
                        partitionIndex: 0,
                        partitionCount: 1,
                        Entity(1, 1, 10),
                        Entity(2, 1, 20))),
                Is.EqualTo(ClientBattleSnapshotCommit.Published));

            var drifted = DeltaPartition(
                battleGeneration: 13,
                serverTick: 11,
                snapshotSequence: 2,
                baselineID: 13,
                new ClientBattleEntityDelta(
                    entityID: 2,
                    generation: 2,
                    ClientBattleEntityDelta.HealthMask,
                    transform: null,
                    healthMilli: 70000,
                    stateFlags: null));
            Assert.That(
                replica.AcceptSnapshot(drifted),
                Is.EqualTo(ClientBattleSnapshotCommit.ResyncRequested));
            Assert.That(network.ResyncRequests.Count, Is.EqualTo(1));
            Assert.That(
                network.ResyncRequests[0].Reason,
                Is.EqualTo(ClientBattleResyncReason.EntityGenerationGap));
            Assert.That(replica.Snapshot().Entities[1].HealthMilli, Is.EqualTo(100000));
        }

        /// <summary>
        /// 确认remote actor按100 ms延迟插值，150 ms后停止继续外推并标记stale。
        /// </summary>
        [Test]
        public void InterpolationSeparatesLocalActorAndFreezesAfterMaximumExtrapolation()
        {
            var interpolation = new GameplayInterpolation(
                ClientBattlePolicy.Current);
            interpolation.Activate(11, localEntityID: 1);
            interpolation.AddSample(11, 10, Entity(1, 1, 999));
            interpolation.AddSample(
                11,
                10,
                new ClientBattleEntityState(
                    2,
                    1,
                    Transform(0, 0, 0, 0, 1000, 0, 0),
                    healthMilli: 100000,
                    stateFlags: 0));
            interpolation.AddSample(
                11,
                12,
                new ClientBattleEntityState(
                    2,
                    1,
                    Transform(100, 0, 0, 0, 1000, 0, 0),
                    healthMilli: 100000,
                    stateFlags: 0));

            var interpolated = interpolation.Evaluate(
                11,
                authorityNowMilliseconds: 650);
            Assert.That(interpolated.Count, Is.EqualTo(1));
            Assert.That(
                interpolated[0].Transform.PositionXMillimeters,
                Is.EqualTo(50));
            Assert.That(interpolated[0].Extrapolated, Is.False);
            Assert.That(interpolated[0].Stale, Is.False);

            var stale = interpolation.Evaluate(
                11,
                authorityNowMilliseconds: 1000);
            Assert.That(stale.Count, Is.EqualTo(1));
            Assert.That(stale[0].Extrapolated, Is.True);
            Assert.That(stale[0].Stale, Is.True);
            Assert.That(
                stale[0].Transform.PositionXMillimeters,
                Is.EqualTo(250));
        }

        /// <summary>
        /// 确认 projector 将 local prediction、remote interpolation、HUD 与 Started cue
        /// 映射到同 generation Camera impulse，且不把 authority transform 当作 local 表现。
        /// </summary>
        [Test]
        public void ProjectorSeparatesActorSourcesAndMapsStartedCueToImpulse()
        {
            var localAuthority = new ClientBattleEntityState(
                entityID: 2,
                generation: 1,
                Transform(100, 0, 0, 0, 0, 0, 0),
                healthMilli: 48500,
                stateFlags: 0,
                archetypeID: ClientBattleContentIdentity.PlayerArchetype,
                equippedWeaponID: ClientBattleContentIdentity.FanWeapon,
                maxHealthMilli: 100000);
            var remoteAuthority = new ClientBattleEntityState(
                entityID: 3,
                generation: 1,
                Transform(200, 0, 0, 0, 0, 0, 0),
                healthMilli: 240000,
                stateFlags: 2,
                archetypeID: ClientBattleContentIdentity.BossArchetype,
                equippedWeaponID: 0,
                maxHealthMilli: 300000);
            var replica = new ClientGameplayReplicaSnapshot(
                battleGeneration: 21,
                localEntityID: 2,
                serverTick: 10,
                snapshotSequence: 1,
                baselineID: 1,
                lastProcessedInputTick: 0,
                resyncPending: false,
                new[] { localAuthority, remoteAuthority },
                Array.Empty<ClientBattleAbilityEvent>());
            var prediction = new ClientGameplayPredictionSnapshot(
                battleGeneration: 21,
                inputEnabled: true,
                baselineRequired: false,
                clockOverrun: false,
                lastSentInputTick: 1,
                lastAcknowledgedInputTick: 0,
                continuityAcknowledgementAnchor: 0,
                acknowledgementAcceptanceFrontier: 1,
                inputHistoryItems: 1,
                predictedHistoryItems: 1,
                reconciliationCount: 0,
                lastReconciliationPositionDeltaMillimeters: 0,
                maximumReconciliationPositionDeltaMillimeters: 0,
                lastReconciliationYawDeltaMillidegrees: 0,
                lastJumpInputTick: 0,
                lastJumpSimulationTick: 0,
                lastJumpPredicted: false,
                new ClientPredictedActorState(
                    inputTick: 1,
                    Transform(1000, 0, 0, 90000, 0, 0, 0),
                    grounded: true),
                presentationTransform:
                    Transform(750, 0, 0, 67500, 0, 0, 0));
            var remote = new ClientBattleInterpolatedState(
                battleGeneration: 21,
                entityID: 3,
                entityGeneration: 1,
                Transform(3000, 0, 0, 180000, 0, 0, 0),
                extrapolated: false,
                stale: false);
            var ability = new ClientBattleAbilityEvent(
                battleGeneration: 21,
                eventID: 7,
                serverTick: 10,
                sourceEntityID: 2,
                sourceEntityGeneration: 1,
                abilityID: ClientBattleContentIdentity.SwordAbility,
                ClientBattleAbilityPhase.Started,
                new[] { 3UL });

            var presentation = GameplayPresentationProjector.Project(
                replica,
                prediction,
                new[] { remote },
                new[] { ability },
                ClientBattleAvailability.Active,
                ClientBattleCameraMode.RangedAim,
                correctionVisible: true);

            Assert.That(presentation.Actors.Count, Is.EqualTo(2));
            Assert.That(presentation.Actors[0].Local, Is.True);
            Assert.That(
                presentation.Actors[0].Transform.PositionXMillimeters,
                Is.EqualTo(750));
            Assert.That(presentation.Actors[1].Local, Is.False);
            Assert.That(
                presentation.Actors[1].Transform.PositionXMillimeters,
                Is.EqualTo(3000));
            Assert.That(presentation.Hud.LocalHealthMilli, Is.EqualTo(48500));
            Assert.That(presentation.Hud.LocalMaxHealthMilli, Is.EqualTo(100000));
            Assert.That(
                presentation.Hud.EquippedWeaponID,
                Is.EqualTo(ClientBattleContentIdentity.FanWeapon));
            Assert.That(presentation.Hud.BossHealthMilli, Is.EqualTo(240000));
            Assert.That(presentation.Hud.BossPhase, Is.EqualTo(2));
            Assert.That(presentation.Hud.CorrectionVisible, Is.True);
            Assert.That(presentation.Cues.Count, Is.EqualTo(1));
            Assert.That(presentation.Cues[0].EventID, Is.EqualTo(7));
            Assert.That(
                presentation.Cues[0].AbilityID,
                Is.EqualTo(ClientBattleContentIdentity.SwordAbility));
            Assert.That(presentation.Camera.Mode, Is.EqualTo(ClientBattleCameraMode.RangedAim));
            Assert.That(presentation.Camera.FollowEntityID, Is.EqualTo(2));
            Assert.That(presentation.Camera.ImpulseEventID, Is.EqualTo(7));
        }

        /// <summary>
        /// 确认reliable ability duplicate与旧event只投影一次，Started cue不会重复触发impulse。
        /// </summary>
        [Test]
        public void ReplicaDeduplicatesAbilityCueBeforePresentation()
        {
            var replica = new GameplayReplica(
                ClientBattlePolicy.Current,
                new RecordingGameplayPort());
            replica.Activate(22, actorSlot: 0);
            Assert.That(
                replica.AcceptSnapshot(
                    FullPartition(
                        22,
                        serverTick: 9,
                        snapshotSequence: 1,
                        baselineID: 1,
                        partitionIndex: 0,
                        partitionCount: 1,
                        Entity(1, 1, 0),
                        Entity(2, 1, 100))),
                Is.EqualTo(ClientBattleSnapshotCommit.Published));
            var ability = new ClientBattleAbilityEvent(
                battleGeneration: 22,
                eventID: 1,
                serverTick: 10,
                sourceEntityID: 1,
                sourceEntityGeneration: 1,
                abilityID: ClientBattleContentIdentity.SwordAbility,
                ClientBattleAbilityPhase.Started,
                new[] { 2UL });

            replica.AcceptAbilityEvent(ability);
            replica.AcceptAbilityEvent(ability);
            var events = replica.DrainAbilityEvents(22);
            Assert.That(events.Count, Is.EqualTo(1));
            Assert.That(events[0].EventID, Is.EqualTo(1));
            Assert.That(replica.DrainAbilityEvents(22), Is.Empty);
            Assert.That(replica.Snapshot().ObservedAbilityEventCount, Is.EqualTo(1));
            Assert.That(
                replica.Snapshot().ObservedLocalAbilityEventCount,
                Is.EqualTo(1));
            Assert.That(
                replica.Snapshot().LastObservedLocalAbilityID,
                Is.EqualTo(ClientBattleContentIdentity.SwordAbility));
            replica.Activate(23, actorSlot: 0);
            Assert.That(replica.Snapshot().ObservedAbilityEventCount, Is.Zero);
            Assert.That(
                replica.Snapshot().ObservedLocalAbilityEventCount,
                Is.Zero);
            Assert.That(replica.Snapshot().LastObservedLocalAbilityID, Is.Zero);
        }

        /// <summary>
        /// 确认较新的full已移除entity后仍保留generation墓碑，使迟到despawn可推进可靠序列。
        /// </summary>
        [Test]
        public void ReplicaRetainsLifecycleGenerationAcrossFullReplacement()
        {
            var replica = new GameplayReplica(
                ClientBattlePolicy.Current,
                new RecordingGameplayPort());
            replica.Activate(24, actorSlot: 0);
            Assert.That(
                replica.AcceptSnapshot(
                    FullPartition(
                        24,
                        serverTick: 9,
                        snapshotSequence: 1,
                        baselineID: 1,
                        partitionIndex: 0,
                        partitionCount: 1,
                        Entity(1, 1, 0),
                        Entity(2, 1, 100))),
                Is.EqualTo(ClientBattleSnapshotCommit.Published));

            replica.AcceptLifecycle(
                new ClientBattleEntityLifecycle(
                    24,
                    eventID: 1,
                    serverTick: 10,
                    entityID: 30,
                    entityGeneration: 1,
                    ClientBattleEntityLifecycleKind.Spawn,
                    archetypeID: 1,
                    Entity(30, 1, 200)));
            Assert.That(
                replica.AcceptSnapshot(
                    FullPartition(
                        24,
                        serverTick: 12,
                        snapshotSequence: 2,
                        baselineID: 2,
                        partitionIndex: 0,
                        partitionCount: 1,
                        Entity(1, 1, 0),
                        Entity(2, 1, 100))),
                Is.EqualTo(ClientBattleSnapshotCommit.Published));

            replica.AcceptLifecycle(
                new ClientBattleEntityLifecycle(
                    24,
                    eventID: 2,
                    serverTick: 11,
                    entityID: 30,
                    entityGeneration: 1,
                    ClientBattleEntityLifecycleKind.Despawn,
                    archetypeID: 0,
                    initialState: null));
            replica.AcceptAbilityEvent(
                new ClientBattleAbilityEvent(
                    24,
                    eventID: 3,
                    serverTick: 13,
                    sourceEntityID: 1,
                    sourceEntityGeneration: 1,
                    abilityID: ClientBattleContentIdentity.SwordAbility,
                    ClientBattleAbilityPhase.Started,
                    new[] { 2UL }));

            Assert.That(replica.Snapshot().ResyncPending, Is.False);
            Assert.That(replica.Snapshot().ObservedLocalAbilityEventCount, Is.EqualTo(1));
        }

        /// <summary>
        /// 确认full已推进mutable state时，迟到spawn只校验不可变identity并推进可靠序列。
        /// </summary>
        [Test]
        public void ReplicaAcceptsDelayedSpawnAfterFullStateAdvanced()
        {
            var replica = new GameplayReplica(
                ClientBattlePolicy.Current,
                new RecordingGameplayPort());
            replica.Activate(25, actorSlot: 0);
            Assert.That(
                replica.AcceptSnapshot(
                    FullPartition(
                        25,
                        serverTick: 12,
                        snapshotSequence: 1,
                        baselineID: 1,
                        partitionIndex: 0,
                        partitionCount: 1,
                        Entity(1, 1, 0),
                        Entity(2, 1, 100),
                        Entity(30, 1, 300))),
                Is.EqualTo(ClientBattleSnapshotCommit.Published));

            replica.AcceptLifecycle(
                new ClientBattleEntityLifecycle(
                    25,
                    eventID: 1,
                    serverTick: 10,
                    entityID: 30,
                    entityGeneration: 1,
                    ClientBattleEntityLifecycleKind.Spawn,
                    archetypeID: 1,
                    Entity(30, 1, 200)));
            replica.AcceptAbilityEvent(
                new ClientBattleAbilityEvent(
                    25,
                    eventID: 2,
                    serverTick: 13,
                    sourceEntityID: 1,
                    sourceEntityGeneration: 1,
                    abilityID: ClientBattleContentIdentity.SwordAbility,
                    ClientBattleAbilityPhase.Started,
                    new[] { 2UL }));

            Assert.That(replica.Snapshot().ResyncPending, Is.False);
            Assert.That(replica.Snapshot().ObservedLocalAbilityEventCount, Is.EqualTo(1));
        }

        /// <summary>
        /// 确认network callback只在主线程drain时提交，并且256项后只允许替换latest raw snapshot。
        /// </summary>
        [Test]
        public async Task InboundRouterUsesMainThreadAndEnforcesHardCapacity()
        {
            var dispatcher = new MainThreadDispatcher(
                Environment.CurrentManagedThreadId,
                capacity: 8);
            var sink = new RecordingInboundSink();
            var router = new ClientBattleInboundRouter(dispatcher);
            router.Bind(sink);
            await dispatcher.InitializeAsync(CancellationToken.None);
            await router.InitializeAsync(CancellationToken.None);
            try
            {
                Assert.That(
                    router.AcceptSnapshot(
                        FullPartition(
                            12,
                            100,
                            1,
                            1,
                            0,
                            1,
                            Entity(1, 1, 10))),
                    Is.EqualTo(ClientBattleSnapshotCommit.Pending));
                Assert.That(sink.SnapshotCalls, Is.Zero);
                Assert.That(router.PendingItems, Is.EqualTo(1));

                var drained = dispatcher.Drain(maximumCallbacks: 1);
                Assert.That(drained.Errors, Is.Empty);
                Assert.That(sink.SnapshotCalls, Is.EqualTo(1));
                Assert.That(router.PendingItems, Is.Zero);

                for (var index = 0; index < 256; index++)
                {
                    router.AcceptSnapshot(
                        FullPartition(
                            12,
                            serverTick: checked((ulong)(200 + index)),
                            snapshotSequence: checked((ulong)(2 + index)),
                            baselineID: checked((ulong)(2 + index)),
                            partitionIndex: 0,
                            partitionCount: 1,
                            Entity(1, 1, index)));
                }

                router.AcceptSnapshot(
                    FullPartition(
                        12,
                        serverTick: 500,
                        snapshotSequence: 500,
                        baselineID: 500,
                        partitionIndex: 0,
                        partitionCount: 1,
                        Entity(1, 1, 500)));
                Assert.That(router.PendingItems, Is.EqualTo(256));
                Assert.That(
                    () => router.AcceptEntityLifecycle(
                        new ClientBattleEntityLifecycle(
                            12,
                            eventID: 1,
                            serverTick: 500,
                            entityID: 2,
                            entityGeneration: 1,
                            ClientBattleEntityLifecycleKind.Spawn,
                            archetypeID: 1,
                            Entity(2, 1, 20))),
                    Throws.TypeOf<ClientBattleInboundBackpressureException>());
            }
            finally
            {
                await router.StopAsync(CancellationToken.None);
                await dispatcher.StopAsync(CancellationToken.None);
            }
        }

        /// <summary>
        /// 验证activation、battle-only reconnect、target replacement与旧terminal共享generation fence。
        /// </summary>
        [Test]
        public async Task RuntimeFencesReconnectReplacementAndOldTerminal()
        {
            var targets = new MutableBattleTargetSource(
                ClientBattleTargetIntent.OwnWorld(
                    1,
                    "world-runtime",
                    "instance-runtime-a",
                    1));
            var connection = new RuntimeConnectionPort();
            connection.EnqueueConnection(EstablishedConnection(1));
            connection.EnqueueConnection(EstablishedConnection(2));
            connection.EnqueueConnection(EstablishedConnection(3));
            var recovery = new ImmediateBattleRecoveryScheduler();
            var runtime = CreateRuntime(
                targets,
                connection,
                recovery);
            await runtime.InitializeAsync(CancellationToken.None);
            try
            {
                await WaitUntilAsync(
                    () => runtime.Snapshot().BattleGeneration == 1);
                Assert.That(
                    connection.StartedGenerations,
                    Is.EqualTo(new[] { 1L }));

                runtime.PublishTerminal(
                    1,
                    ClientBattleFailure.Transport);
                await WaitUntilAsync(
                    () => runtime.Snapshot().BattleGeneration == 2);
                Assert.That(recovery.RunCalls, Is.EqualTo(1));
                Assert.That(
                    connection.StartedGenerations,
                    Is.EqualTo(new[] { 1L, 2L }));

                runtime.PublishTerminal(
                    1,
                    ClientBattleFailure.Security);
                Assert.That(recovery.RunCalls, Is.EqualTo(1));
                Assert.That(
                    runtime.Snapshot().Failure,
                    Is.EqualTo(ClientBattleFailure.None));

                targets.Publish(
                    ClientBattleTargetIntent.OwnWorld(
                        1,
                        "world-runtime",
                        "instance-runtime-b",
                        2));
                await WaitUntilAsync(
                    () => runtime.Snapshot().BattleGeneration == 3);
                Assert.That(
                    recovery.PreemptCalls,
                    Is.GreaterThanOrEqualTo(1));
                Assert.That(
                    connection.StartedGenerations,
                    Is.EqualTo(new[] { 1L, 2L, 3L }));

                targets.Publish(null);
                await WaitUntilAsync(
                    () => runtime.Snapshot().BattleGeneration == 0);
                Assert.That(
                    runtime.Snapshot().Availability,
                    Is.EqualTo(ClientBattleAvailability.Inactive));
                runtime.PublishTerminal(
                    3,
                    ClientBattleFailure.Transport);
                Assert.That(recovery.RunCalls, Is.EqualTo(1));
            }
            finally
            {
                await runtime.StopAsync(CancellationToken.None);
            }

            Assert.That(targets.SubscriptionCount, Is.Zero);
            Assert.That(connection.DeactivateCalls, Is.GreaterThanOrEqualTo(4));
        }

        /// <summary>
        /// 验证同target successor连接期间保持Retrying，使Scene继续呈现最后可信画面。
        /// </summary>
        [Test]
        public async Task RuntimeKeepsRetryingWhileSuccessorConnects()
        {
            var targets = new MutableBattleTargetSource(
                ClientBattleTargetIntent.OwnWorld(
                    1,
                    "world-runtime-recovery",
                    "instance-runtime-recovery",
                    1));
            var successor =
                new TaskCompletionSource<ClientBattleConnectionSnapshot>();
            var connection = new RuntimeConnectionPort();
            connection.ActivationHandler = (_, cancellationToken) =>
            {
                cancellationToken.ThrowIfCancellationRequested();
                return connection.ActivateCalls == 1
                    ? Task.FromResult(EstablishedConnection(1))
                    : successor.Task;
            };
            var runtime = CreateRuntime(
                targets,
                connection,
                new ImmediateBattleRecoveryScheduler());
            await runtime.InitializeAsync(CancellationToken.None);
            try
            {
                await WaitUntilAsync(
                    () => runtime.Snapshot().BattleGeneration == 1);

                runtime.PublishTerminal(
                    1,
                    ClientBattleFailure.Transport);
                await WaitUntilAsync(
                    () => connection.ActivateCalls == 2);

                var recovering = runtime.Snapshot();
                Assert.That(
                    recovering.Availability,
                    Is.EqualTo(ClientBattleAvailability.Retrying));
                Assert.That(recovering.BattleGeneration, Is.Zero);
                Assert.That(recovering.BaselineReady, Is.False);
                Assert.That(recovering.InputEnabled, Is.False);

                successor.SetResult(EstablishedConnection(2));
                await WaitUntilAsync(
                    () => runtime.Snapshot().BattleGeneration == 2);
                Assert.That(
                    runtime.Snapshot().Availability,
                    Is.EqualTo(ClientBattleAvailability.LoadingBaseline));
            }
            finally
            {
                successor.TrySetCanceled();
                await runtime.StopAsync(CancellationToken.None);
            }
        }

        /// <summary>
        /// 确认 runtime activation 与 reconciliation 逐次消费 full/delta 的authority grounded bit。
        /// </summary>
        [Test]
        public async Task RuntimeUsesAuthorityGroundedForActivationAndReconciliation()
        {
            var targets = new MutableBattleTargetSource(
                ClientBattleTargetIntent.OwnWorld(
                    1,
                    "world-grounded",
                    "instance-grounded",
                    1));
            var connection = new RuntimeConnectionPort();
            connection.EnqueueConnection(EstablishedConnection(1));
            var prediction = new GameplayPrediction(
                ClientBattlePolicy.Current,
                connection);
            var runtime = new ClientBattleRuntimeCoordinator(
                targets,
                connection,
                new GameplayReplica(
                    ClientBattlePolicy.Current,
                    connection),
                prediction,
                new GameplayInterpolation(
                    ClientBattlePolicy.Current),
                new FixedClock(),
                new ImmediateBattleRecoveryScheduler());
            await runtime.InitializeAsync(CancellationToken.None);
            try
            {
                await WaitUntilAsync(
                    () => runtime.Snapshot().BattleGeneration == 1);
                Assert.That(
                    runtime.AcceptSnapshot(
                        FullPartition(
                            battleGeneration: 1,
                            serverTick: 1,
                            snapshotSequence: 1,
                            baselineID: 1,
                            partitionIndex: 0,
                            partitionCount: 1,
                            Entity(
                                1,
                                1,
                                0,
                                ClientBattleEntityState.
                                    GroundedStateFlag))),
                    Is.EqualTo(
                        ClientBattleSnapshotCommit.Published));
                Assert.That(
                    prediction.Snapshot().State.Grounded,
                    Is.True);

                Assert.That(
                    runtime.AcceptSnapshot(
                        DeltaPartition(
                            battleGeneration: 1,
                            serverTick: 2,
                            snapshotSequence: 2,
                            baselineID: 1,
                            new ClientBattleEntityDelta(
                                entityID: 1,
                                generation: 1,
                                ClientBattleEntityDelta.
                                    StateFlagsMask,
                                transform: null,
                                healthMilli: null,
                                stateFlags: 0))),
                    Is.EqualTo(
                        ClientBattleSnapshotCommit.Published));
                Assert.That(
                    prediction.Snapshot().State.Grounded,
                    Is.False);
            }
            finally
            {
                await runtime.StopAsync(CancellationToken.None);
            }
        }

        /// <summary>
        /// 确认runtime在local dead snapshot同一提交中关闭Scene与资格输入。
        /// </summary>
        [Test]
        public async Task RuntimeAuthorityDeathClosesCurrentGenerationInput()
        {
            var targets = new MutableBattleTargetSource(
                ClientBattleTargetIntent.OwnWorld(
                    1,
                    "world-death",
                    "instance-death",
                    1));
            var connection = new RuntimeConnectionPort();
            connection.EnqueueConnection(EstablishedConnection(1));
            var runtime = CreateRuntime(
                targets,
                connection,
                new ImmediateBattleRecoveryScheduler());
            await runtime.InitializeAsync(CancellationToken.None);
            try
            {
                await WaitUntilAsync(
                    () => runtime.Snapshot().BattleGeneration == 1);
                Assert.That(
                    runtime.AcceptSnapshot(
                        FullPartition(
                            battleGeneration: 1,
                            serverTick: 1,
                            snapshotSequence: 1,
                            baselineID: 1,
                            partitionIndex: 0,
                            partitionCount: 1,
                            Entity(
                                1,
                                1,
                                0,
                                ClientBattleEntityState.
                                    GroundedStateFlag))),
                    Is.EqualTo(ClientBattleSnapshotCommit.Published));
                var input = new ClientBattleSemanticInput(
                    1000,
                    0,
                    0,
                    0,
                    jumpPressed: false,
                    primaryPressed: true,
                    secondaryPressed: false,
                    interactPressed: false,
                    interactionSlot: 0);
                Assert.That(runtime.TrySetInput(1, input), Is.True);

                Assert.That(
                    runtime.AcceptSnapshot(
                        DeltaPartition(
                            battleGeneration: 1,
                            serverTick: 2,
                            snapshotSequence: 2,
                            baselineID: 1,
                            new ClientBattleEntityDelta(
                                entityID: 1,
                                generation: 1,
                                ClientBattleEntityDelta.HealthMask |
                                    ClientBattleEntityDelta.StateFlagsMask,
                                transform: null,
                                healthMilli: 0,
                                stateFlags:
                                    ClientBattleEntityState.DeadStateFlag))),
                    Is.EqualTo(ClientBattleSnapshotCommit.Published));

                Assert.That(runtime.Snapshot().InputEnabled, Is.False);
                Assert.That(runtime.TrySetInput(1, input), Is.False);
                Assert.That(
                    runtime.TryCaptureQualificationSnapshot(out var dead),
                    Is.True);
                Assert.That(dead.LocalHealthMilli, Is.Zero);
                Assert.That(dead.LocalDead, Is.True);
                Assert.That(dead.Runtime.InputEnabled, Is.False);
            }
            finally
            {
                await runtime.StopAsync(CancellationToken.None);
            }
        }

        /// <summary>
        /// 验证Development Player资格快照只读current generation，且input override会屏蔽Scene sample。
        /// </summary>
        [Test]
        public async Task RuntimeQualificationSnapshotAndInputOverrideAreGenerationScoped()
        {
            var targets = new MutableBattleTargetSource(
                ClientBattleTargetIntent.OwnWorld(
                    1,
                    "world-qualification",
                    "instance-qualification",
                    1));
            var connection = new RuntimeConnectionPort();
            connection.EnqueueConnection(EstablishedConnection(1));
            var runtime = CreateRuntime(
                targets,
                connection,
                new ImmediateBattleRecoveryScheduler());
            await runtime.InitializeAsync(CancellationToken.None);
            try
            {
                await WaitUntilAsync(
                    () => runtime.Snapshot().BattleGeneration == 1);
                Assert.That(
                    runtime.AcceptSnapshot(
                        FullPartition(
                            battleGeneration: 1,
                            serverTick: 1,
                            snapshotSequence: 1,
                            baselineID: 1,
                            partitionIndex: 0,
                            partitionCount: 1,
                            Entity(
                                1,
                                1,
                                0,
                                ClientBattleEntityState.
                                    GroundedStateFlag))),
                    Is.EqualTo(ClientBattleSnapshotCommit.Published));

                Assert.That(
                    runtime.TryCaptureQualificationSnapshot(
                        out var active),
                    Is.True);
                Assert.That(active.Runtime.BattleGeneration, Is.EqualTo(1));
                Assert.That(active.EntityCount, Is.EqualTo(1));
                Assert.That(active.LocalEntityID, Is.EqualTo(1));
                Assert.That(active.AuthorityGrounded, Is.True);

                var input = new ClientBattleSemanticInput(
                    1000,
                    0,
                    45000,
                    0,
                    jumpPressed: true,
                    primaryPressed: false,
                    secondaryPressed: false,
                    interactPressed: false,
                    interactionSlot: 0);
                Assert.That(
                    runtime.TrySetQualificationInput(1, input),
                    Is.True);
                Assert.That(runtime.TrySetInput(1, input), Is.False);
                runtime.ClearQualificationInput(1);
                Assert.That(runtime.TrySetInput(1, input), Is.True);

                targets.Publish(null);
                await WaitUntilAsync(
                    () => runtime.Snapshot().BattleGeneration == 0);
                Assert.That(
                    runtime.TryCaptureQualificationSnapshot(
                        out var inactive),
                    Is.True);
                Assert.That(inactive.Runtime.BattleGeneration, Is.Zero);
                Assert.That(inactive.EntityCount, Is.Zero);
                Assert.That(inactive.LocalEntityID, Is.Zero);
            }
            finally
            {
                await runtime.StopAsync(CancellationToken.None);
            }
        }

        /// <summary>验证Stop抢占尚未提交的activation并拒绝迟到successor。</summary>
        [Test]
        public async Task RuntimeStopRejectsLateActivationCompletion()
        {
            var targets = new MutableBattleTargetSource(
                ClientBattleTargetIntent.OwnWorld(
                    1,
                    "world-stop",
                    "instance-stop",
                    1));
            var connection = new RuntimeConnectionPort();
            var pending =
                new TaskCompletionSource<ClientBattleConnectionSnapshot>(
                    TaskCreationOptions.RunContinuationsAsynchronously);
            connection.ActivationHandler = (_, __) => pending.Task;
            var recovery = new ImmediateBattleRecoveryScheduler();
            var runtime = CreateRuntime(
                targets,
                connection,
                recovery);
            await runtime.InitializeAsync(CancellationToken.None);
            await WaitUntilAsync(() => connection.ActivateCalls == 1);

            var stop = runtime.StopAsync(CancellationToken.None);
            pending.SetResult(EstablishedConnection(1));
            await stop;

            var snapshot = runtime.Snapshot();
            Assert.That(snapshot.BattleGeneration, Is.Zero);
            Assert.That(snapshot.InputEnabled, Is.False);
            Assert.That(
                snapshot.Failure,
                Is.EqualTo(ClientBattleFailure.Shutdown));
            Assert.That(connection.StartedGenerations, Is.Empty);
            Assert.That(targets.SubscriptionCount, Is.Zero);
            Assert.That(connection.DeactivateCalls, Is.GreaterThanOrEqualTo(2));
        }

        /// <summary>
        /// 验证rebind只推进endpoint generation并保留traffic epoch与packet sequence。
        /// </summary>
        [Test]
        public void SecureRebindPreservesTrafficContinuity()
        {
            var seed = Filled(32, 0x11);
            var sessionID = Filled(16, 0x22);
            var binding = Filled(32, 0x33);
            using (var parameters = new ClientBattleSessionParameters(
                       seed,
                       sessionID,
                       battleSessionGeneration: 7,
                       keyEpoch: 1,
                       endpointGeneration: 1,
                       actorSlot: 0,
                       ClientBattleRole.Owner,
                       binding))
            using (var secure = new ClientBattleSecureChannel(
                       parameters,
                       startedAtMilliseconds: 1_000))
            {
                Assert.That(
                    secure.TrySeal(
                        ClientBattlePacketKind.Raw,
                        new byte[] { 1 },
                        1_001,
                        out var predecessor),
                    Is.True);
                Assert.That(
                    ReadUInt32BigEndian(predecessor, 20),
                    Is.EqualTo(1));
                Assert.That(
                    ReadUInt64BigEndian(predecessor, 24),
                    Is.EqualTo(1));
                Assert.That(
                    ReadUInt32BigEndian(predecessor, 34),
                    Is.EqualTo(1));

                Assert.That(
                    secure.CommitEndpointGeneration(1, 2),
                    Is.True);
                Assert.That(secure.KeyEpoch, Is.EqualTo(1));
                Assert.That(secure.EndpointGeneration, Is.EqualTo(2));
                Assert.That(
                    secure.TrySeal(
                        ClientBattlePacketKind.Raw,
                        new byte[] { 2 },
                        1_002,
                        out var successor),
                    Is.True);
                Assert.That(
                    ReadUInt32BigEndian(successor, 20),
                    Is.EqualTo(1));
                Assert.That(
                    ReadUInt64BigEndian(successor, 24),
                    Is.EqualTo(2));
                Assert.That(
                    ReadUInt32BigEndian(successor, 34),
                    Is.EqualTo(2));

                var rekeyNonce = Filled(32, 0x44);
                Assert.That(
                    secure.CommitRollover(
                        rekeyNonce,
                        nextEpoch: 2,
                        nowMilliseconds: 1_003),
                    Is.True);
                Assert.That(secure.KeyEpoch, Is.EqualTo(2));
                Assert.That(secure.EndpointGeneration, Is.EqualTo(2));
                Assert.That(
                    secure.TrySeal(
                        ClientBattlePacketKind.Control,
                        new byte[] { 3 },
                        1_004,
                        out var rekeyed),
                    Is.True);
                Assert.That(
                    ReadUInt32BigEndian(rekeyed, 20),
                    Is.EqualTo(2));
                Assert.That(
                    ReadUInt64BigEndian(rekeyed, 24),
                    Is.EqualTo(1));
                Assert.That(
                    ReadUInt32BigEndian(rekeyed, 34),
                    Is.EqualTo(2));

                Array.Clear(predecessor, 0, predecessor.Length);
                Array.Clear(successor, 0, successor.Length);
                Array.Clear(rekeyed, 0, rekeyed.Length);
                Array.Clear(rekeyNonce, 0, rekeyNonce.Length);
            }
        }

        /// <summary>验证transport-control rebind envelope与fixed payload width闭合。</summary>
        [Test]
        public void RebindControlCodecUsesExactCanonicalWidths()
        {
            var address = new byte[16];
            address[10] = 0xff;
            address[11] = 0xff;
            address[12] = 127;
            address[15] = 1;
            var nonce = Filled(16, 0x55);
            var request =
                ClientBattleControlCodec.EncodeRebindRequest(
                    address,
                    30123,
                    nonce);

            Assert.That(request.Length, Is.EqualTo(42));
            Assert.That(request[5], Is.EqualTo(
                (byte)ClientBattleControlKind.RebindRequest));
            Assert.That(request[6], Is.Zero);
            Assert.That(request[7], Is.EqualTo(34));
            Assert.That(request[24], Is.EqualTo((byte)(30123 >> 8)));
            Assert.That(request[25], Is.EqualTo((byte)(30123 & 0xff)));
            Assert.That(
                new ArraySegment<byte>(request, 26, 16),
                Is.EqualTo(nonce));

            var challenge = ServerControl(
                ClientBattleControlKind.RebindChallenge,
                Filled(48, 0x66));
            Assert.That(
                ClientBattleControlCodec.TryDecodeServer(
                    challenge,
                    out var decoded),
                Is.True);
            Assert.That(
                decoded.Kind,
                Is.EqualTo(ClientBattleControlKind.RebindChallenge));
            Assert.That(decoded.Payload.Length, Is.EqualTo(48));

            challenge[7] = 47;
            Assert.That(
                ClientBattleControlCodec.TryDecodeServer(
                    challenge,
                    out _),
                Is.False);
            Array.Clear(request, 0, request.Length);
            Array.Clear(nonce, 0, nonce.Length);
            Array.Clear(decoded.Payload, 0, decoded.Payload.Length);
            Array.Clear(challenge, 0, challenge.Length);
        }

        /// <summary>确认认证后缺失首个 full snapshot 会在有界时间进入可恢复终态。</summary>
        [Test]
        public async Task BattleNetworkBaselineWaitHasBoundedTimeout()
        {
            var dispatcher = new MainThreadDispatcher(
                Environment.CurrentManagedThreadId,
                capacity: 16);
            var router = new ClientBattleInboundRouter(dispatcher);
            router.Bind(new RecordingInboundSink());
            var native = new ClientBattleNativeProvider();
            var socket = new BlockingBattleSocket();
            await dispatcher.InitializeAsync(CancellationToken.None);
            await router.InitializeAsync(CancellationToken.None);
            await native.InitializeAsync(CancellationToken.None);
            var parameters = new ClientBattleSessionParameters(
                Filled(32, 0x61),
                Filled(16, 0x62),
                battleSessionGeneration: 8,
                keyEpoch: 1,
                endpointGeneration: 1,
                actorSlot: 0,
                ClientBattleRole.Owner,
                Filled(32, 0x63));
            var established = new ClientBattleEstablishedConnection(
                socket,
                new ClientBattleSecureChannel(
                    parameters,
                    startedAtMilliseconds:
                    DateTimeOffset.UtcNow.ToUnixTimeMilliseconds()),
                native.CreateKcp(
                    1,
                    parameters.DeriveKcpConversation()),
                ClientBattleRole.Owner,
                actorSlot: 0,
                targetRevision: 1);
            var network = new BattleNetworkClient(
                new EstablishedConnectAttempt(established),
                new ClientBattleProtocolAdapter(),
                router);

            await network.InitializeAsync(CancellationToken.None);
            try
            {
                var activated = await network.ActivateAsync(
                    ClientBattleTargetIntent.OwnWorld(
                        1,
                        "world-baseline-timeout",
                        "instance-baseline-timeout",
                        1),
                    CancellationToken.None);
                Assert.That(
                    activated.State,
                    Is.EqualTo(
                        ClientBattleConnectionState.AwaitingBaseline));
                Assert.That(
                    BattleNetworkClient.
                        InitialBaselineDeadlineMilliseconds,
                    Is.EqualTo(5000));
                Assert.That(
                    network.InjectQualificationBaselineTimeout(),
                    Is.True);
                var terminal = network.Snapshot();
                Assert.That(
                    terminal.State,
                    Is.EqualTo(ClientBattleConnectionState.Closing));
                Assert.That(
                    terminal.Failure,
                    Is.EqualTo(ClientBattleFailure.Timeout));
                Assert.That(
                    network.InjectQualificationBaselineTimeout(),
                    Is.False);
            }
            finally
            {
                await network.StopAsync(CancellationToken.None);
                await router.StopAsync(CancellationToken.None);
                await native.StopAsync(CancellationToken.None);
                await dispatcher.StopAsync(CancellationToken.None);
                parameters.Dispose();
            }
        }

        /// <summary>
        /// 验证fake socket上的authenticated rebind request、stop竞态与native/socket/pump零残留。
        /// </summary>
        [Test]
        public async Task BattleNetworkFakeSocketRebindAndStopReleaseAllResources()
        {
            var dispatcher = new MainThreadDispatcher(
                Environment.CurrentManagedThreadId,
                capacity: 16);
            var router = new ClientBattleInboundRouter(dispatcher);
            router.Bind(new RecordingInboundSink());
            var native = new ClientBattleNativeProvider();
            var socket = new BlockingBattleSocket();
            var parameters = new ClientBattleSessionParameters(
                Filled(32, 0x71),
                Filled(16, 0x72),
                battleSessionGeneration: 9,
                keyEpoch: 1,
                endpointGeneration: 1,
                actorSlot: 0,
                ClientBattleRole.Owner,
                Filled(32, 0x73));
            var conversation = parameters.DeriveKcpConversation();
            var secure = new ClientBattleSecureChannel(
                parameters,
                startedAtMilliseconds:
                DateTimeOffset.UtcNow.ToUnixTimeMilliseconds());

            await dispatcher.InitializeAsync(CancellationToken.None);
            await router.InitializeAsync(CancellationToken.None);
            await native.InitializeAsync(CancellationToken.None);
            var established = new ClientBattleEstablishedConnection(
                socket,
                secure,
                native.CreateKcp(1, conversation),
                ClientBattleRole.Owner,
                actorSlot: 0,
                targetRevision: 1);
            var network = new BattleNetworkClient(
                new EstablishedConnectAttempt(established),
                new ClientBattleProtocolAdapter(),
                router);
            await network.InitializeAsync(CancellationToken.None);
            try
            {
                var activated = await network.ActivateAsync(
                    ClientBattleTargetIntent.OwnWorld(
                        1,
                        "world-network",
                        "instance-network",
                        1),
                    CancellationToken.None);
                Assert.That(
                    activated.State,
                    Is.EqualTo(
                        ClientBattleConnectionState.AwaitingBaseline));
                Assert.That(network.TryStartTraffic(1), Is.True);
                Assert.That(network.QualificationSocketOwnerCount, Is.EqualTo(1));
                Assert.That(network.QualificationPumpOwnerCount, Is.EqualTo(3));
                Assert.That(native.QualificationLeaseCount, Is.EqualTo(1));

                var candidate = new byte[16];
                candidate[10] = 0xff;
                candidate[11] = 0xff;
                candidate[12] = 127;
                candidate[15] = 1;
                Assert.That(
                    network.TryBeginEndpointRebind(
                        1,
                        candidate,
                        30123),
                    Is.True);
                await WaitUntilAsync(() => socket.SentCount == 1);
                Assert.That(
                    network.Snapshot().State,
                    Is.EqualTo(ClientBattleConnectionState.Rebinding));
                Assert.That(network.Snapshot().EndpointGeneration, Is.EqualTo(1));

                await network.DeactivateAsync(
                    ClientBattleFailure.Shutdown,
                    CancellationToken.None);
                Assert.That(network.QualificationSocketOwnerCount, Is.Zero);
                Assert.That(network.QualificationPumpOwnerCount, Is.Zero);
                Assert.That(native.QualificationLeaseCount, Is.Zero);
                Assert.That(socket.DisposeCalls, Is.EqualTo(1));
            }
            finally
            {
                await network.StopAsync(CancellationToken.None);
                await router.StopAsync(CancellationToken.None);
                await native.StopAsync(CancellationToken.None);
                await dispatcher.StopAsync(CancellationToken.None);
                parameters.Dispose();
            }
        }

        /// <summary>
        /// 确认 ticket codec 接受 exact suite，同时拒绝 response unknown field。
        /// </summary>
        [Test]
        public void TicketCodecIsClosedAndTargetBound()
        {
            var codec = new ClientBattleTicketCodec();
            var target = ClientBattleTargetIntent.OwnWorld(
                1,
                "world-test",
                "instance-test",
                1);
            var request = Encoding.UTF8.GetString(codec.EncodeRequest(target));
            Assert.That(request, Is.EqualTo("{\"kind\":\"OWN_WORLD\"}"));

            var response = CreateTicketResponse(includeUnknown: false);
            using (var material = codec.Decode(response, target))
            {
                Assert.That(material.Role, Is.EqualTo(ClientBattleRole.Owner));
                Assert.That(
                    material.TargetKind,
                    Is.EqualTo(ClientBattleTargetKind.OwnWorld));
                Assert.That(material.TargetRevision, Is.EqualTo(9));
            }

            Assert.That(
                () => codec.Decode(
                    CreateTicketResponse(includeUnknown: true),
                    target),
                Throws.TypeOf<ClientBattleTicketCodecException>());
        }

        /// <summary>
        /// 确认 own/visit selector 精确编码，且错误 suite、endpoint、role、kind、revision 与 expiry 全部 fail closed。
        /// </summary>
        /// <param name="current">合法 response 中待替换的片段。</param>
        /// <param name="replacement">制造 closed contract 漂移的片段。</param>
        [TestCase(
            "\"wireVersion\":1",
            "\"wireVersion\":2")]
        [TestCase(
            "\"transport\":\"UDP\"",
            "\"transport\":\"TCP\"")]
        [TestCase(
            "\"role\":\"OWNER\"",
            "\"role\":\"VISITOR\"")]
        [TestCase(
            "\"targetKind\":\"OWN_WORLD\"",
            "\"targetKind\":\"VISIT_WORLD\"")]
        [TestCase(
            "\"targetRevision\":9",
            "\"targetRevision\":0")]
        [TestCase(
            "\"expiresAtMs\":9999999999999",
            "\"expiresAtMs\":0")]
        public void TicketCodecRejectsClosedBindingDrift(
            string current,
            string replacement)
        {
            var codec = new ClientBattleTicketCodec();
            var own = ClientBattleTargetIntent.OwnWorld(
                1,
                "world-test",
                "instance-test",
                1);
            var visit = ClientBattleTargetIntent.VisitWorld(
                1,
                "world-test",
                "instance-test",
                1,
                "visit-test",
                7);
            Assert.That(
                Encoding.UTF8.GetString(codec.EncodeRequest(visit)),
                Is.EqualTo(
                    "{\"kind\":\"VISIT_WORLD\",\"visitSessionId\":\"visit-test\"}"));

            var valid = Encoding.UTF8.GetString(
                CreateTicketResponse(includeUnknown: false));
            var drifted = Encoding.UTF8.GetBytes(
                valid.Replace(current, replacement));
            Assert.That(
                () => codec.Decode(drifted, own),
                Throws.TypeOf<ClientBattleTicketCodecException>());
        }

        /// <summary>
        /// 确认 ticket 绝对 expiry 在任何 native handshake primitive 前终止 attempt。
        /// </summary>
        [Test]
        public void ExpiredTicketCannotStartHandshake()
        {
            var codec = new ClientBattleTicketCodec();
            var target = ClientBattleTargetIntent.OwnWorld(
                1,
                "world-test",
                "instance-test",
                1);
            var responseText = Encoding.UTF8.GetString(
                CreateTicketResponse(includeUnknown: false));
            var expired = Encoding.UTF8.GetBytes(
                responseText.Replace(
                    "\"expiresAtMs\":9999999999999",
                    "\"expiresAtMs\":1"));
            var material = codec.Decode(expired, target);

            Assert.That(
                () => new ClientBattleHandshake(
                    material,
                    target,
                    startedAtMilliseconds: 2),
                Throws.ArgumentException);
        }

        /// <summary>
        /// 确认 decoded secret 与 ticket material 的诊断文本脱敏，释放后原 byte buffer 被原位清零。
        /// </summary>
        [Test]
        public void TicketSecretIsRedactedAndZeroedInPlace()
        {
            var codec = new ClientBattleTicketCodec();
            var target = ClientBattleTargetIntent.OwnWorld(
                1,
                "world-test",
                "instance-test",
                1);
            var response = CreateTicketResponse(includeUnknown: false);
            var material = codec.Decode(response, target);
            var secret = material.TakeSecret();
            var secretBytes = secret.RequireBytes();

            Assert.That(secret.ToString(), Does.Not.Contain(Base64Url(secretBytes)));
            Assert.That(material.ToString(), Does.Not.Contain("127.0.0.1"));
            secret.Dispose();
            Assert.That(secretBytes, Is.All.EqualTo(0));
            material.Dispose();
        }

        /// <summary>
        /// 确认专用 HTTP API 无论成功投影为何都在返回前原位清零 raw credential body。
        /// </summary>
        /// <returns>等待 raw response 解码与 finally 清理完成。</returns>
        [Test]
        public async Task TicketHttpApiClearsRawCredentialBody()
        {
            var target = ClientBattleTargetIntent.OwnWorld(
                1,
                "world-test",
                "instance-test",
                1);
            var rawBody = CreateTicketResponse(includeUnknown: false);
            var transport = new FixedTicketTransport(rawBody);
            var api = new ClientBattleTicketHttpApi(
                transport,
                new ClientBattleTicketCodec(),
                new ClientHttpContractMapper());

            var result = await api.IssueAsync(
                new ClientBattleTicketGatewayRequest(
                    new ClientCredentialLease(
                        "access-token",
                        ClientCredentialPurpose.HttpAuthorization),
                    target,
                    "battle-idempotency-test-0001"),
                CancellationToken.None);

            Assert.That(result.IsSuccess, Is.True);
            Assert.That(rawBody, Is.All.EqualTo(0));
            result.Value.Dispose();
        }

        /// <summary>
        /// 确认 response loss 只以同一 idempotency identity 重试一次，仍未知时返回 CommitUnknown。
        /// </summary>
        /// <returns>等待两次确定性 gateway failure 收敛。</returns>
        [Test]
        public async Task ConnectAttemptBoundsResponseLossWithSameIdentity()
        {
            var authorization = new RecordingAuthorizationSource();
            var gateway = new FailingTicketGateway(
                ClientGatewayFailureKind.Transport,
                ClientGatewayFailureKind.Timeout);
            var sockets = new RejectingSocketFactory();
            var attempt = new ClientBattleConnectAttempt(
                authorization,
                gateway,
                sockets,
                new ClientBattleNativeProvider());

            var result = await attempt.ExecuteAsync(
                1,
                ClientBattleTargetIntent.OwnWorld(
                    1,
                    "world-test",
                    "instance-test",
                    1),
                CancellationToken.None);

            Assert.That(result.IsSuccess, Is.False);
            Assert.That(
                result.Failure,
                Is.EqualTo(ClientBattleFailure.CommitUnknown));
            Assert.That(authorization.Calls, Is.EqualTo(2));
            Assert.That(gateway.IdempotencyKeys, Has.Count.EqualTo(2));
            Assert.That(
                gateway.IdempotencyKeys[1],
                Is.EqualTo(gateway.IdempotencyKeys[0]));
            Assert.That(
                gateway.IdempotencyKeys[0].Length,
                Is.GreaterThanOrEqualTo(32));
            Assert.That(sockets.Calls, Is.Zero);
        }

        /// <summary>
        /// 确认 caller cancellation 在签发/UDP 副作用前结束，不被误分类为 commit-unknown。
        /// </summary>
        /// <returns>等待 pre-cancelled attempt 收敛。</returns>
        [Test]
        public async Task ConnectAttemptPreservesCallerCancellation()
        {
            var authorization = new RecordingAuthorizationSource();
            var gateway = new FailingTicketGateway(
                ClientGatewayFailureKind.Transport);
            var sockets = new RejectingSocketFactory();
            var attempt = new ClientBattleConnectAttempt(
                authorization,
                gateway,
                sockets,
                new ClientBattleNativeProvider());
            using (var cancellation = new CancellationTokenSource())
            {
                cancellation.Cancel();
                var result = await attempt.ExecuteAsync(
                    1,
                    ClientBattleTargetIntent.OwnWorld(
                        1,
                        "world-test",
                        "instance-test",
                        1),
                    cancellation.Token);

                Assert.That(
                    result.Failure,
                    Is.EqualTo(ClientBattleFailure.CallerCancelled));
                Assert.That(gateway.IdempotencyKeys, Is.Empty);
                Assert.That(sockets.Calls, Is.Zero);
            }
        }

        /// <summary>
        /// 确认UDP endpoint快速复用时，结构匹配但不属于current transcript的旧响应被清零并
        /// 忽略，current响应仍可在同一绝对deadline内完成认证。
        /// </summary>
        /// <returns>等待两个候选按顺序被消费。</returns>
        [Test]
        public async Task ConnectAttemptIgnoresUnrelatedHandshakeCandidate()
        {
            var unrelated = new byte[] { 1, 9 };
            var current = new byte[] { 1, 7 };
            var socket = new SequencedBattleSocket(
                unrelated,
                current);
            var clock = new ClientBattleAttemptClock();

            var accepted = await ClientBattleConnectAttempt.
                ReceiveAuthenticatedCandidateAsync(
                    socket,
                    clock.NowMilliseconds + 1000,
                    candidate => candidate[0] == 1,
                    candidate => candidate[1] == 7
                        ? "accepted"
                        : null,
                    clock,
                    CancellationToken.None);

            Assert.That(accepted, Is.EqualTo("accepted"));
            Assert.That(socket.ReceiveCalls, Is.EqualTo(2));
            Assert.That(unrelated, Is.All.EqualTo(0));
            Assert.That(current, Is.All.EqualTo(0));
        }

        /// <summary>创建使用纯C# gameplay owners的runtime fixture。</summary>
        private static ClientBattleRuntimeCoordinator CreateRuntime(
            MutableBattleTargetSource targets,
            RuntimeConnectionPort connection,
            IClientBattleRecoveryScheduler recovery)
        {
            return new ClientBattleRuntimeCoordinator(
                targets,
                connection,
                new GameplayReplica(
                    ClientBattlePolicy.Current,
                    connection),
                new GameplayPrediction(
                    ClientBattlePolicy.Current,
                    connection),
                new GameplayInterpolation(
                    ClientBattlePolicy.Current),
                new FixedClock(),
                recovery);
        }

        /// <summary>创建已认证并等待full baseline的低敏connection snapshot。</summary>
        private static ClientBattleConnectionSnapshot EstablishedConnection(
            long generation)
        {
            return new ClientBattleConnectionSnapshot(
                ClientBattleConnectionState.AwaitingBaseline,
                ClientBattleFailure.None,
                generation,
                ClientBattleTargetKind.OwnWorld,
                ClientBattleRole.Owner,
                actorSlot: 0,
                trafficEpoch: 1,
                endpointGeneration: 1,
                latestServerTick: 0,
                sendQueueItems: 0,
                receiveQueueItems: 0);
        }

        /// <summary>在有限yield内等待异步runtime transition提交。</summary>
        private static async Task WaitUntilAsync(Func<bool> predicate)
        {
            for (var attempt = 0; attempt < 128; attempt++)
            {
                if (predicate())
                {
                    return;
                }

                await Task.Yield();
            }

            Assert.Fail("Battle runtime transition未在受控continuation内提交。");
        }

        /// <summary>创建fixed-width nonzero byte fixture。</summary>
        private static byte[] Filled(int length, byte value)
        {
            var output = new byte[length];
            for (var index = 0; index < output.Length; index++)
            {
                output[index] = value;
            }

            return output;
        }

        /// <summary>编码测试用server transport-control envelope。</summary>
        private static byte[] ServerControl(
            ClientBattleControlKind kind,
            byte[] payload)
        {
            var output = new byte[
                ClientBattleControlCodec.EnvelopeBytes +
                payload.Length];
            output[0] = (byte)'I';
            output[1] = (byte)'H';
            output[2] = (byte)'B';
            output[3] = (byte)'C';
            output[4] = 1;
            output[5] = (byte)kind;
            output[6] = (byte)(payload.Length >> 8);
            output[7] = (byte)payload.Length;
            Buffer.BlockCopy(
                payload,
                0,
                output,
                ClientBattleControlCodec.EnvelopeBytes,
                payload.Length);
            Array.Clear(payload, 0, payload.Length);
            return output;
        }

        /// <summary>读取测试datagram中的canonical big-endian UInt32。</summary>
        private static uint ReadUInt32BigEndian(
            byte[] buffer,
            int offset)
        {
            return
                ((uint)buffer[offset] << 24) |
                ((uint)buffer[offset + 1] << 16) |
                ((uint)buffer[offset + 2] << 8) |
                buffer[offset + 3];
        }

        /// <summary>读取测试datagram中的canonical big-endian UInt64。</summary>
        private static ulong ReadUInt64BigEndian(
            byte[] buffer,
            int offset)
        {
            return
                ((ulong)ReadUInt32BigEndian(buffer, offset) << 32) |
                ReadUInt32BigEndian(buffer, offset + 4);
        }

        /// <summary>创建测试用 authority transform。</summary>
        private static ClientBattleTransform Transform(
            int x,
            int y,
            int z,
            int yaw,
            int velocityX,
            int velocityY,
            int velocityZ)
        {
            return new ClientBattleTransform(
                x,
                y,
                z,
                yaw,
                velocityX,
                velocityY,
                velocityZ);
        }

        /// <summary>创建带 current producer closed state flags 的测试 entity。</summary>
        private static ClientBattleEntityState Entity(
            ulong entityID,
            uint generation,
            int x,
            uint stateFlags = 0)
        {
            return new ClientBattleEntityState(
                entityID,
                generation,
                Transform(x, 0, 0, 0, 0, 0, 0),
                healthMilli: 100000,
                stateFlags);
        }

        /// <summary>创建七个scalar均显式存在的generated transform。</summary>
        private static QuantizedTransform GeneratedTransform()
        {
            return new QuantizedTransform
            {
                PositionXMm = 100,
                PositionYMm = 0,
                PositionZMm = -200,
                YawMillidegrees = 90000,
                VelocityXMmPerSecond = 3000,
                VelocityYMmPerSecond = 0,
                VelocityZMmPerSecond = -1000,
            };
        }

        /// <summary>创建只包含full entity state的测试partition。</summary>
        private static ClientBattleSnapshotPartition FullPartition(
            long battleGeneration,
            ulong serverTick,
            ulong snapshotSequence,
            ulong baselineID,
            int partitionIndex,
            int partitionCount,
            params ClientBattleEntityState[] entities)
        {
            return new ClientBattleSnapshotPartition(
                battleGeneration,
                ClientBattleSnapshotKind.Full,
                serverTick,
                snapshotSequence,
                baselineID,
                partitionIndex,
                partitionCount,
                lastProcessedInputTick: 0,
                entities,
                Array.Empty<ClientBattleEntityDelta>());
        }

        /// <summary>创建单partition delta snapshot。</summary>
        private static ClientBattleSnapshotPartition DeltaPartition(
            long battleGeneration,
            ulong serverTick,
            ulong snapshotSequence,
            ulong baselineID,
            params ClientBattleEntityDelta[] deltas)
        {
            return new ClientBattleSnapshotPartition(
                battleGeneration,
                ClientBattleSnapshotKind.Delta,
                serverTick,
                snapshotSequence,
                baselineID,
                partitionIndex: 0,
                partitionCount: 1,
                lastProcessedInputTick: 0,
                Array.Empty<ClientBattleEntityState>(),
                deltas);
        }

        /// <summary>创建 exact BattleTicket response bytes。</summary>
        private static byte[] CreateTicketResponse(bool includeUnknown)
        {
            var ticketID = Base64Url(new byte[16]);
            var secret = Base64Url(new byte[32]);
            var unknown = includeUnknown ? ",\"unexpected\":true" : string.Empty;
            var json =
                "{\"ticketId\":\"btk1_" + ticketID +
                "\",\"ticketSecret\":\"bts1_" + secret +
                "\",\"endpoint\":{\"transport\":\"UDP\",\"host\":\"127.0.0.1\",\"port\":30000}" +
                ",\"wireSuite\":{\"wireVersion\":1,\"keyAgreement\":\"X25519\",\"kdf\":\"HKDF-SHA-256\",\"aead\":\"ChaCha20-Poly1305\"}" +
                ",\"role\":\"OWNER\",\"targetKind\":\"OWN_WORLD\",\"targetRevision\":9,\"expiresAtMs\":9999999999999" +
                unknown +
                "}";
            return Encoding.UTF8.GetBytes(json);
        }

        /// <summary>编码无 padding canonical base64url。</summary>
        private static string Base64Url(byte[] value)
        {
            return Convert.ToBase64String(value)
                .TrimEnd('=')
                .Replace('+', '-')
                .Replace('/', '_');
        }

        /// <summary>返回固定 raw BattleTicket response 并把原 body 所有权交给 API。</summary>
        private sealed class FixedTicketTransport :
            IClientBattleTicketTransport
        {
            /// <summary>保存待转移且由测试观察清零结果的 body。</summary>
            private readonly byte[] _body;

            /// <summary>
            /// 创建只返回一次 caller-owned body 的 transport。
            /// </summary>
            /// <param name="body">合法 BattleTicket response bytes。</param>
            internal FixedTicketTransport(byte[] body)
            {
                _body = body ?? throw new ArgumentNullException(nameof(body));
            }

            /// <inheritdoc />
            public Task<ClientHttpRawResult> SendAsync(
                ClientHttpOperation operation,
                byte[] requestBody,
                string bearerToken,
                CancellationToken cancellationToken,
                string idempotencyKey)
            {
                cancellationToken.ThrowIfCancellationRequested();
                return Task.FromResult(
                    ClientHttpRawResult.Received(
                        new ClientHttpRawResponse(
                            HttpStatusCode.Created,
                            "application/json",
                            _body,
                            null)));
            }
        }

        /// <summary>为每次 attempt retry 生成 fresh 单次 authorization lease。</summary>
        private sealed class RecordingAuthorizationSource :
            IClientBattleAuthorizationSource
        {
            /// <summary>获取 authorization 请求次数。</summary>
            internal int Calls { get; private set; }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientCredentialLease>>
                AcquireBattleAuthorizationAsync(
                    long expectedSessionGeneration,
                    CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                Calls++;
                return Task.FromResult(
                    ClientGatewayResult<ClientCredentialLease>.Success(
                        new ClientCredentialLease(
                            "access-token",
                            ClientCredentialPurpose.HttpAuthorization)));
            }
        }

        /// <summary>按顺序返回 response-loss failure，并记录 attempt-stable idempotency identity。</summary>
        private sealed class FailingTicketGateway :
            IClientBattleTicketGateway
        {
            /// <summary>保存每次签发应返回的 transport failure。</summary>
            private readonly ClientGatewayFailureKind[] _failures;

            /// <summary>
            /// 创建按顺序消费 failure 的 gateway。
            /// </summary>
            /// <param name="failures">至少一个确定性 failure。</param>
            internal FailingTicketGateway(
                params ClientGatewayFailureKind[] failures)
            {
                _failures = failures ??
                    throw new ArgumentNullException(nameof(failures));
                if (_failures.Length == 0)
                {
                    throw new ArgumentException(
                        "ticket failure script cannot be empty",
                        nameof(failures));
                }
            }

            /// <summary>获取按调用顺序记录的 idempotency identity。</summary>
            internal List<string> IdempotencyKeys { get; } =
                new List<string>();

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientBattleTicketMaterial>>
                IssueAsync(
                    ClientBattleTicketGatewayRequest request,
                    CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                IdempotencyKeys.Add(request.IdempotencyKey);
                var index = Math.Min(
                    IdempotencyKeys.Count - 1,
                    _failures.Length - 1);
                return Task.FromResult(
                    ClientGatewayResult<ClientBattleTicketMaterial>.Failed(
                        new ClientGatewayFailure(
                            _failures[index],
                            ClientHttpOperationCatalog
                                .IssueBattleTicket
                                .OperationID)));
            }
        }

        /// <summary>证明 response-loss/cancel 测试不会提前创建 UDP socket。</summary>
        private sealed class RejectingSocketFactory :
            IClientBattleDatagramSocketFactory
        {
            /// <summary>获取意外 socket 创建次数。</summary>
            internal int Calls { get; private set; }

            /// <inheritdoc />
            public IClientBattleDatagramSocket Create()
            {
                Calls++;
                throw new InvalidOperationException(
                    "ticket failure must not create a UDP socket");
            }
        }

        /// <summary>只返回一次已建立resource bundle的deterministic connect attempt。</summary>
        private sealed class EstablishedConnectAttempt :
            IClientBattleConnectAttempt
        {
            /// <summary>保存尚未转移的成功connection。</summary>
            private ClientBattleEstablishedConnection _connection;

            /// <summary>创建单次成功attempt。</summary>
            internal EstablishedConnectAttempt(
                ClientBattleEstablishedConnection connection)
            {
                _connection = connection ??
                    throw new ArgumentNullException(nameof(connection));
            }

            /// <inheritdoc />
            public Task<ClientBattleConnectResult> ExecuteAsync(
                long battleGeneration,
                ClientBattleTargetIntent target,
                CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                var current = Interlocked.Exchange(
                    ref _connection,
                    null);
                return Task.FromResult(
                    current == null
                        ? ClientBattleConnectResult.Failed(
                            ClientBattleFailure.Policy)
                        : ClientBattleConnectResult.Success(current));
            }
        }

        /// <summary>阻塞receive并记录serialized send/dispose的fake connected UDP socket。</summary>
        private sealed class BlockingBattleSocket :
            IClientBattleDatagramSocket
        {
            /// <summary>保护send与dispose计数。</summary>
            private readonly object _gate = new object();

            /// <summary>保存已发送datagram数量。</summary>
            private int _sentCount;

            /// <summary>保存dispose调用数量。</summary>
            private int _disposeCalls;

            /// <summary>获取已完整发送的datagram数量。</summary>
            internal int SentCount
            {
                get
                {
                    lock (_gate)
                    {
                        return _sentCount;
                    }
                }
            }

            /// <summary>获取socket dispose调用数量。</summary>
            internal int DisposeCalls
            {
                get
                {
                    lock (_gate)
                    {
                        return _disposeCalls;
                    }
                }
            }

            /// <inheritdoc />
            public Task ConnectAsync(
                string host,
                int port,
                CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                return Task.CompletedTask;
            }

            /// <inheritdoc />
            public Task<int> SendAsync(
                byte[] datagram,
                CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                lock (_gate)
                {
                    _sentCount++;
                }

                return Task.FromResult(datagram.Length);
            }

            /// <inheritdoc />
            public async Task<byte[]> ReceiveAsync(
                int maximumBytes,
                CancellationToken cancellationToken)
            {
                var completion =
                    new TaskCompletionSource<byte[]>(
                        TaskCreationOptions.RunContinuationsAsynchronously);
                using (cancellationToken.Register(
                           () => completion.TrySetCanceled()))
                {
                    return await completion.Task;
                }
            }

            /// <inheritdoc />
            public void Dispose()
            {
                lock (_gate)
                {
                    _disposeCalls++;
                }
            }
        }

        /// <summary>按冻结顺序返回UDP candidate并保留原buffer以验证清零。</summary>
        private sealed class SequencedBattleSocket :
            IClientBattleDatagramSocket
        {
            /// <summary>保存尚未消费的caller-owned candidate。</summary>
            private readonly Queue<byte[]> _datagrams;

            /// <summary>创建至少包含一个candidate的socket。</summary>
            /// <param name="datagrams">按接收顺序返回的buffer。</param>
            internal SequencedBattleSocket(params byte[][] datagrams)
            {
                _datagrams = new Queue<byte[]>(
                    datagrams ?? throw new ArgumentNullException(
                        nameof(datagrams)));
                if (_datagrams.Count == 0)
                {
                    throw new ArgumentException(
                        "sequenced battle socket requires a datagram",
                        nameof(datagrams));
                }
            }

            /// <summary>获取已执行的receive次数。</summary>
            internal int ReceiveCalls { get; private set; }

            /// <inheritdoc />
            public Task ConnectAsync(
                string host,
                int port,
                CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                return Task.CompletedTask;
            }

            /// <inheritdoc />
            public Task<int> SendAsync(
                byte[] datagram,
                CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                return Task.FromResult(datagram.Length);
            }

            /// <inheritdoc />
            public Task<byte[]> ReceiveAsync(
                int maximumBytes,
                CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                ReceiveCalls++;
                if (_datagrams.Count == 0)
                {
                    throw new InvalidOperationException(
                        "sequenced battle socket was exhausted");
                }

                return Task.FromResult(_datagrams.Dequeue());
            }

            /// <inheritdoc />
            public void Dispose()
            {
            }
        }

        /// <summary>提供可替换current target并观察唯一subscriber的测试source。</summary>
        private sealed class MutableBattleTargetSource :
            IClientBattleTargetSource
        {
            /// <summary>保存current target；空值表示authority尚未提交。</summary>
            private ClientBattleTargetIntent _current;

            /// <summary>创建具有可选初始target的source。</summary>
            internal MutableBattleTargetSource(
                ClientBattleTargetIntent current)
            {
                _current = current;
            }

            /// <inheritdoc />
            public event Action Changed;

            /// <summary>获取current subscriber数量。</summary>
            internal int SubscriptionCount =>
                Changed?.GetInvocationList().Length ?? 0;

            /// <inheritdoc />
            public bool TryCapture(out ClientBattleTargetIntent intent)
            {
                intent = _current;
                return intent != null;
            }

            /// <summary>原子替换测试target并同步发布authority变化。</summary>
            internal void Publish(ClientBattleTargetIntent intent)
            {
                _current = intent;
                Changed?.Invoke();
            }
        }

        /// <summary>记录runtime connection lifecycle并提供确定性successor脚本。</summary>
        private sealed class RuntimeConnectionPort :
            IClientBattleConnectionPort,
            IClientBattleGameplayPort
        {
            /// <summary>保存按activation顺序消费的connection结果。</summary>
            private readonly Queue<ClientBattleConnectionSnapshot>
                _connections =
                    new Queue<ClientBattleConnectionSnapshot>();

            /// <summary>获取可覆盖的activation实现。</summary>
            internal Func<ClientBattleTargetIntent, CancellationToken,
                Task<ClientBattleConnectionSnapshot>>
                ActivationHandler { get; set; }

            /// <summary>获取activation调用次数。</summary>
            internal int ActivateCalls { get; private set; }

            /// <summary>获取Stop调用次数。</summary>
            internal int StopCalls { get; private set; }

            /// <summary>获取connection deactivation调用次数。</summary>
            internal int DeactivateCalls { get; private set; }

            /// <summary>获取成功启动traffic的generation序列。</summary>
            internal List<long> StartedGenerations { get; } =
                new List<long>();

            /// <summary>追加下一次activation应返回的结果。</summary>
            internal void EnqueueConnection(
                ClientBattleConnectionSnapshot connection)
            {
                _connections.Enqueue(connection);
            }

            /// <inheritdoc />
            public Task InitializeAsync(
                CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                return Task.CompletedTask;
            }

            /// <inheritdoc />
            public Task<ClientBattleConnectionSnapshot> ActivateAsync(
                ClientBattleTargetIntent intent,
                CancellationToken cancellationToken)
            {
                ActivateCalls++;
                if (ActivationHandler != null)
                {
                    return ActivationHandler(
                        intent,
                        cancellationToken);
                }

                return Task.FromResult(_connections.Dequeue());
            }

            /// <inheritdoc />
            public bool TryStartTraffic(long battleGeneration)
            {
                if (battleGeneration <= 0)
                {
                    return false;
                }

                StartedGenerations.Add(battleGeneration);
                return true;
            }

            /// <inheritdoc />
            public Task DeactivateAsync(
                ClientBattleFailure failure,
                CancellationToken cancellationToken)
            {
                DeactivateCalls++;
                return Task.CompletedTask;
            }

            /// <inheritdoc />
            public ClientBattleConnectionSnapshot Snapshot()
            {
                return _connections.Count == 0
                    ? new ClientBattleConnectionSnapshot(
                        ClientBattleConnectionState.Ready,
                        ClientBattleFailure.None,
                        0,
                        null,
                        ClientBattleRole.None,
                        -1,
                        0,
                        0,
                        0,
                        0,
                        0)
                    : _connections.Peek();
            }

            /// <inheritdoc />
            public bool TrySendInput(
                long battleGeneration,
                ClientBattleInputBundle bundle)
            {
                return battleGeneration > 0 && bundle != null;
            }

            /// <inheritdoc />
            public bool TryRequestResync(
                ClientBattleResyncRequest request)
            {
                return request != null;
            }

            /// <inheritdoc />
            public Task StopAsync(
                CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                StopCalls++;
                return Task.CompletedTask;
            }
        }

        /// <summary>立即执行battle operation并记录抢占次数的唯一scheduler fake。</summary>
        private sealed class ImmediateBattleRecoveryScheduler :
            IClientBattleRecoveryScheduler
        {
            /// <summary>保存current operation cancellation owner。</summary>
            private CancellationTokenSource _current;

            /// <summary>获取recovery运行次数。</summary>
            internal int RunCalls { get; private set; }

            /// <summary>获取显式抢占次数。</summary>
            internal int PreemptCalls { get; private set; }

            /// <inheritdoc />
            public Task<bool> RunBattleRecoveryAsync(
                long sourceBattleGeneration,
                Func<CancellationToken, Task<bool>> operation)
            {
                RunCalls++;
                _current = new CancellationTokenSource();
                return operation(_current.Token);
            }

            /// <inheritdoc />
            public void PreemptBattleRecovery()
            {
                PreemptCalls++;
                _current?.Cancel();
                _current?.Dispose();
                _current = null;
            }
        }

        /// <summary>提供稳定Unix毫秒值的runtime测试clock。</summary>
        private sealed class FixedClock : IClientClock
        {
            /// <inheritdoc />
            public long UtcNowMilliseconds => 10_000;
        }

        /// <summary>记录 prediction 产生的有界 payload commands。</summary>
        private sealed class RecordingGameplayPort : IClientBattleGameplayPort
        {
            /// <summary>获取按发送顺序保存的 immutable bundles。</summary>
            internal List<ClientBattleInputBundle> Bundles { get; } =
                new List<ClientBattleInputBundle>();

            /// <summary>获取按发送顺序保存的single-flight resync requests。</summary>
            internal List<ClientBattleResyncRequest> ResyncRequests { get; } =
                new List<ClientBattleResyncRequest>();

            /// <inheritdoc />
            public bool TrySendInput(
                long battleGeneration,
                ClientBattleInputBundle bundle)
            {
                if (battleGeneration <= 0 || bundle == null)
                {
                    return false;
                }

                Bundles.Add(bundle);
                return true;
            }

            /// <inheritdoc />
            public bool TryRequestResync(ClientBattleResyncRequest request)
            {
                if (request == null)
                {
                    return false;
                }

                ResyncRequests.Add(request);
                return true;
            }
        }

        /// <summary>记录main-thread router实际提交次数，不保存payload或业务identity。</summary>
        private sealed class RecordingInboundSink : IClientBattleInboundSink
        {
            /// <summary>获取已在主线程提交的snapshot次数。</summary>
            internal int SnapshotCalls { get; private set; }

            /// <inheritdoc />
            public ClientBattleSnapshotCommit AcceptSnapshot(
                ClientBattleSnapshotPartition partition)
            {
                SnapshotCalls++;
                return ClientBattleSnapshotCommit.Published;
            }

            /// <inheritdoc />
            public bool AcceptEntityLifecycle(
                ClientBattleEntityLifecycle lifecycle)
            {
                return lifecycle != null;
            }

            /// <inheritdoc />
            public bool AcceptAbilityEvent(
                ClientBattleAbilityEvent abilityEvent)
            {
                return abilityEvent != null;
            }

            /// <inheritdoc />
            public bool AcceptResyncResponse(
                ClientBattleResyncResponse response,
                long nowMilliseconds)
            {
                return response != null && nowMilliseconds >= 0;
            }

            /// <inheritdoc />
            public void PublishTerminal(
                long battleGeneration,
                ClientBattleFailure failure)
            {
            }
        }
    }
}
