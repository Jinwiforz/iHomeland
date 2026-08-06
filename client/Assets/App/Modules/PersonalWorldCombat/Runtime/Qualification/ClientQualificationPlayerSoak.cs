#if DEVELOPMENT_BUILD || UNITY_EDITOR
using System;
using System.IO;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.PersonalWorldCombat.Application;
using IHomeland.Client.Networking.Application.Control;
using IHomeland.Client.Networking.Application.Gameplay;
using IHomeland.Client.AppShell.Runtime.Composition;
using IHomeland.Client.AppShell.Runtime.Qualification;
using IHomeland.Client.Networking.Infrastructure.WebSocket;
using IHomeland.Client.Session.Application;
using IHomeland.Client.PersonalWorld.Application;
using IHomeland.Client.AppShell.Presentation.Navigation;
using IHomeland.Client.PersonalWorld.Presentation;
using IHomeland.Client.PersonalWorld.Runtime.Scenes;
using IHomeland.Client.PersonalWorldCombat.Runtime.Scenes;
using UnityEngine;

namespace IHomeland.Client.PersonalWorldCombat.Runtime.Qualification
{
    /// <summary>在Development Player中以真实产品graph执行有界五分钟恢复soak。</summary>
    internal static class ClientQualificationPlayerSoak
    {
        /// <summary>资格mode命令行参数。</summary>
        private const string ModeArgument = "-ihomelandQualificationMode";

        /// <summary>资格运行从继承环境读取的一次性账号变量。</summary>
        private const string UsernameVariable = "IHOMELAND_QUALIFICATION_USERNAME";

        /// <summary>资格运行从继承环境读取的一次性密码变量。</summary>
        private const string PasswordVariable = "IHOMELAND_QUALIFICATION_PASSWORD";

        /// <summary>唯一允许的自动Player mode。</summary>
        private const string SoakMode = "soak";

        /// <summary>真实server process replacement的定向Development诊断mode。</summary>
        private const string ServerRestartRecoveryMode = "server-restart-recovery";

        /// <summary>真实双Player产品与故障矩阵的Development-only operator mode。</summary>
        private const string TwoPlayerOperatorMode = "two-player-operator";

        /// <summary>真实双Player battle runtime定向验收mode。</summary>
        private const string ClientBattleRuntimeMode = "client-battle-runtime";

        /// <summary>双Player operator使用的角色参数。</summary>
        private const string OperatorRoleArgument = "-ihomelandQualificationRole";

        /// <summary>双Player operator使用的场景参数。</summary>
        private const string OperatorScenarioArgument = "-ihomelandQualificationScenario";

        /// <summary>双Player operator使用的跨进程协调目录参数。</summary>
        private const string OperatorCoordinationRootArgument =
            "-ihomelandQualificationCoordinationRoot";

        /// <summary>产品与分通道故障场景。</summary>
        private const string ProductFaultScenario = "product-fault";

        /// <summary>服务端停机、恢复与重邀场景。</summary>
        private const string ServerRestartScenario = "server-restart";

        /// <summary>外部诊断owner在新server listeners就绪后提交的文件信号参数。</summary>
        private const string RecoverySignalArgument = "-ihomelandQualificationRecoverySignal";

        /// <summary>唯一允许资格工具请求secure store owner执行精确删除的mode。</summary>
        private const string CleanupMode = "cleanup";

        /// <summary>固定soak总时长，保证跨越多个15秒heartbeat周期。</summary>
        private static readonly TimeSpan SoakDuration = TimeSpan.FromMinutes(5);

        /// <summary>每轮开始的绝对间隔，使三轮覆盖整个soak而不是集中在启动瞬间。</summary>
        private static readonly TimeSpan RoundInterval = TimeSpan.FromSeconds(100);

        /// <summary>单次权威状态转换允许的上限，不修改任何production deadline。</summary>
        private static readonly TimeSpan TransitionObservationDeadline = TimeSpan.FromSeconds(60);

        /// <summary>只用于观察显式snapshot提交的低频调度间隔。</summary>
        private static readonly TimeSpan ObservationInterval = TimeSpan.FromMilliseconds(100);

        /// <summary>Production玩家primary Ability中的最大权威cooldown Tick数。</summary>
        private const ulong MaximumPlayerPrimaryCooldownTicks = 14;

        /// <summary>跨越已观察到的约三分钟battle终结点，验证同一generation持续可玩。</summary>
        private static readonly TimeSpan BattleContinuityDuration = TimeSpan.FromSeconds(210);

        /// <summary>
        /// 本地资格环境的gameplay pre-auth持续预算为每分钟60次；operator主动整形真实admission，
        /// 避免把高密度测试步骤本身误判为异常握手流量。
        /// </summary>
        private static readonly TimeSpan GameplayAdmissionBudgetInterval =
            TimeSpan.FromMilliseconds(1250);

        /// <summary>防止重复Bootstrap callback启动第二个soak owner。</summary>
        private static int _started;

        /// <summary>仅在显式soak mode存在时启动唯一异步资格owner。</summary>
        /// <param name="composition">已经进入Running的完整产品Composition。</param>
        internal static void TryStart(AppCompositionResult composition)
        {
            if (composition == null)
            {
                throw new ArgumentNullException(nameof(composition));
            }

            var mode = ReadSingleArgument(Environment.GetCommandLineArgs(), ModeArgument);
            if (mode == null)
            {
                return;
            }

            if (Interlocked.CompareExchange(ref _started, 1, 0) != 0)
            {
                Debug.LogError("[IHOMELAND_QUALIFICATION] outcome=fail reason=invalid-mode");
                UnityEngine.Application.Quit(2);
                return;
            }

            if (string.Equals(mode, SoakMode, StringComparison.Ordinal))
            {
                _ = ObserveAsync(RunAsync(composition));
                return;
            }

            if (string.Equals(mode, ServerRestartRecoveryMode, StringComparison.Ordinal))
            {
                _ = ObserveServerRestartAsync(RunServerRestartRecoveryAsync(composition));
                return;
            }

            if (string.Equals(mode, TwoPlayerOperatorMode, StringComparison.Ordinal))
            {
                _ = ObserveTwoPlayerOperatorAsync(RunTwoPlayerOperatorAsync(composition));
                return;
            }

            if (string.Equals(mode, ClientBattleRuntimeMode, StringComparison.Ordinal))
            {
                _ = ObserveClientBattleRuntimeAsync(
                    RunClientBattleRuntimeAsync(composition));
                return;
            }

            if (string.Equals(mode, CleanupMode, StringComparison.Ordinal))
            {
                _ = ObserveCleanupAsync(composition.SessionCoordinator.ForgetAsync(CancellationToken.None));
                return;
            }

            Debug.LogError("[IHOMELAND_QUALIFICATION] outcome=fail reason=invalid-mode");
            UnityEngine.Application.Quit(2);
        }

        /// <summary>执行真实双Player operator场景并只通过共享信号交换低敏状态。</summary>
        /// <param name="composition">已经进入Running的完整产品Composition。</param>
        /// <returns>当前角色负责的场景全部通过时完成。</returns>
        private static async Task RunTwoPlayerOperatorAsync(AppCompositionResult composition)
        {
            var arguments = Environment.GetCommandLineArgs();
            var role = ReadSingleArgument(arguments, OperatorRoleArgument);
            var scenario = ReadSingleArgument(arguments, OperatorScenarioArgument);
            var coordinationRoot = ReadSingleArgument(arguments, OperatorCoordinationRootArgument);
            if ((!string.Equals(role, "owner", StringComparison.Ordinal) &&
                 !string.Equals(role, "visitor", StringComparison.Ordinal)) ||
                ( !string.Equals(scenario, ProductFaultScenario, StringComparison.Ordinal) &&
                  !string.Equals(scenario, ServerRestartScenario, StringComparison.Ordinal)) ||
                string.IsNullOrWhiteSpace(coordinationRoot))
            {
                throw new QualificationFailureException("invalid-operator-arguments");
            }

            coordinationRoot = Path.GetFullPath(coordinationRoot);
            if (!Directory.Exists(coordinationRoot))
            {
                throw new QualificationFailureException("missing-coordination-root");
            }

            if (string.Equals(scenario, ProductFaultScenario, StringComparison.Ordinal))
            {
                if (string.Equals(role, "owner", StringComparison.Ordinal))
                {
                    await RunOwnerProductFaultAsync(composition, coordinationRoot);
                }
                else
                {
                    await RunVisitorProductFaultAsync(composition, coordinationRoot);
                }

                return;
            }

            if (string.Equals(role, "owner", StringComparison.Ordinal))
            {
                await RunOwnerServerRestartAsync(composition, coordinationRoot);
            }
            else
            {
                await RunVisitorServerRestartAsync(composition, coordinationRoot);
            }
        }

        /// <summary>执行真实双Player battle clean、recovery、safe-return与teardown场景。</summary>
        /// <param name="composition">已经进入Running的完整产品Composition。</param>
        /// <returns>当前角色负责的battle场景全部通过时完成。</returns>
        private static async Task RunClientBattleRuntimeAsync(
            AppCompositionResult composition)
        {
            var arguments = Environment.GetCommandLineArgs();
            var role = ReadSingleArgument(arguments, OperatorRoleArgument);
            var coordinationRoot = ReadSingleArgument(
                arguments,
                OperatorCoordinationRootArgument);
            if ((!string.Equals(role, "owner", StringComparison.Ordinal) &&
                 !string.Equals(role, "visitor", StringComparison.Ordinal)) ||
                string.IsNullOrWhiteSpace(coordinationRoot))
            {
                throw new QualificationFailureException(
                    "invalid-battle-runtime-arguments");
            }

            coordinationRoot = Path.GetFullPath(coordinationRoot);
            if (!Directory.Exists(coordinationRoot))
            {
                throw new QualificationFailureException(
                    "missing-coordination-root");
            }

            if (string.Equals(role, "owner", StringComparison.Ordinal))
            {
                await RunOwnerClientBattleRuntimeAsync(
                    composition,
                    coordinationRoot);
            }
            else
            {
                await RunVisitorClientBattleRuntimeAsync(
                    composition,
                    coordinationRoot);
            }
        }

        /// <summary>驱动Owner battle clean、successor recovery、Visit关闭和最终资源释放。</summary>
        /// <param name="composition">Owner完整产品graph。</param>
        /// <param name="root">本次battle运行独占协调目录。</param>
        /// <returns>Owner负责的全部真实场景通过时完成。</returns>
        private static async Task RunOwnerClientBattleRuntimeAsync(
            AppCompositionResult composition,
            string root)
        {
            await LoginOrRestoreOwnWorldAsync(
                composition,
                requireCredentials: true);
            var diagnostics = composition.CreateQualificationDiagnostics();
            await WaitForBattleActiveAsync(
                diagnostics,
                ClientBattleTargetKind.OwnWorld,
                ClientBattleRole.Owner);
            WriteSignal(root, "owner-ready", "ready");
            var visitorPlayerID = await ReadRequiredSignalAsync(
                root,
                "visitor-player");
            await RequireActionAsync(
                composition.PersonalWorldExperience.OpenVisitAsync(
                    CancellationToken.None),
                "battle-owner-open-rejected");
            await WaitUntilAsync(
                () => composition.PersonalWorldExperience.ViewState.
                    WorldVisit.IsOwner,
                TransitionObservationDeadline);
            await PublishInviteAsync(
                composition,
                root,
                "battle-invite",
                visitorPlayerID);
            await WaitForOwnerMemberAsync(
                composition,
                root,
                "battle-joined",
                visitorPlayerID);

            var ownerActive = await WaitForBattleActiveAsync(
                diagnostics,
                ClientBattleTargetKind.OwnWorld,
                ClientBattleRole.Owner);
            WriteSignal(
                root,
                "owner-battle-slot",
                ownerActive.Runtime.Runtime.ActorSlot.ToString());
            var visitorSlot = await ReadActorSlotSignalAsync(
                root,
                "visitor-battle-slot");
            if (visitorSlot == ownerActive.Runtime.Runtime.ActorSlot)
            {
                throw new QualificationFailureException(
                    "battle-actor-slot-collision");
            }

            await WaitForSceneActorsAsync(
                diagnostics,
                expectedActors: 6,
                stage: "owner-joined");
            await ReadRequiredSignalAsync(root, "visitor-observer-ready");
            await ExerciseLocalBattleAsync(
                composition,
                diagnostics,
                moveXMilli: 1000,
                moveYMilli: 0,
                aimYawMillidegrees: 45000);
            await ExerciseCombatAsync(
                composition,
                diagnostics,
                expectedEntities: 6);
            WriteSignal(root, "owner-motion-complete", "pass");
            await ReadRequiredSignalAsync(root, "visitor-observed-owner");

            var visitorEntityID = (ulong)(visitorSlot + 1);
            var visitorBefore = await CaptureActorTransformAsync(
                composition,
                visitorEntityID);
            WriteSignal(root, "owner-observer-ready", "ready");
            await ReadRequiredSignalAsync(root, "visitor-motion-complete");
            await WaitForActorTransformChangeAsync(
                composition,
                diagnostics,
                visitorEntityID,
                visitorBefore);
            WriteSignal(root, "owner-observed-visitor", "pass");
            WriteSignal(root, "owner-pre-recovery-ready", "ready");
            await ReadRequiredSignalAsync(
                root,
                "visitor-pre-recovery-ready");

            var beforeRecovery = diagnostics.CaptureBattle();
            if (!diagnostics.InjectBattleTransportDisconnect())
            {
                throw new QualificationFailureException(
                    "battle-owner-disconnect-not-injected");
            }

            var recovered = await WaitForBattleSuccessorAsync(
                diagnostics,
                beforeRecovery.Runtime.Runtime.BattleGeneration,
                ClientBattleTargetKind.OwnWorld,
                ClientBattleRole.Owner);
            if (recovered.Runtime.Runtime.ActorSlot !=
                beforeRecovery.Runtime.Runtime.ActorSlot)
            {
                throw new QualificationFailureException(
                    "battle-successor-actor-slot-drifted");
            }

            await WaitForSceneActorsAsync(
                diagnostics,
                expectedActors: 6,
                stage: "owner-recovered");
            await ExerciseLocalBattleAsync(
                composition,
                diagnostics,
                moveXMilli: 0,
                moveYMilli: 1000,
                aimYawMillidegrees: 90000);
            await ExerciseCombatAsync(
                composition,
                diagnostics,
                expectedEntities: 6);
            WriteSignal(root, "owner-recovered", "pass");
            await RunCooperativeBossDefeatAsync(
                composition,
                diagnostics,
                root,
                localRole: "owner",
                remoteRole: "visitor");
            await AssertBattleContinuityAsync(
                diagnostics,
                recovered.Runtime.Runtime.BattleGeneration,
                ClientBattleTargetKind.OwnWorld,
                ClientBattleRole.Owner);
            WriteSignal(root, "owner-continuity-pass", "pass");
            await ReadRequiredSignalAsync(root, "visitor-continuity-pass");

            await ReadRequiredSignalAsync(root, "visitor-disconnected");
            var ownerBeforeSafeReturn = diagnostics.CaptureBattle();
            await RequireActionAsync(
                composition.PersonalWorldExperience.CloseVisitAsync(
                    CancellationToken.None),
                "battle-owner-close-rejected");
            await WaitUntilAsync(
                () => composition.VisitSessionService.Snapshot.Current == null,
                TransitionObservationDeadline);
            var ownerAfterSafeReturn = await WaitForBattleActiveAsync(
                diagnostics,
                ClientBattleTargetKind.OwnWorld,
                ClientBattleRole.Owner);
            if (ownerAfterSafeReturn.Runtime.Runtime.BattleGeneration !=
                ownerBeforeSafeReturn.Runtime.Runtime.BattleGeneration)
            {
                throw new QualificationFailureException(
                    "battle-owner-safe-return-replaced-generation");
            }

            WriteSignal(root, "safe-return-triggered", "pass");
            await ReadRequiredSignalAsync(root, "visitor-safe-return");

            await LogoutAndAssertBattleReleasedAsync(composition, diagnostics);
            WriteSignal(root, "owner-cleanup", "pass");
            await ReadRequiredSignalAsync(root, "visitor-cleanup");
            WriteSignal(root, "client-battle-runtime-pass", "pass");
        }

        /// <summary>驱动Visitor join、远端插值、本地movement和safe-return旧代拒绝。</summary>
        /// <param name="composition">Visitor完整产品graph。</param>
        /// <param name="root">本次battle运行独占协调目录。</param>
        /// <returns>Visitor负责的全部真实场景通过时完成。</returns>
        private static async Task RunVisitorClientBattleRuntimeAsync(
            AppCompositionResult composition,
            string root)
        {
            await LoginOrRestoreOwnWorldAsync(
                composition,
                requireCredentials: true);
            var diagnostics = composition.CreateQualificationDiagnostics();
            await WaitForBattleActiveAsync(
                diagnostics,
                ClientBattleTargetKind.OwnWorld,
                ClientBattleRole.Owner);
            WriteSignal(
                root,
                "visitor-player",
                composition.PersonalWorldExperience.ViewState.Shell.PlayerID);
            await ReadRequiredSignalAsync(root, "owner-ready");
            await AcceptPublishedInviteAsync(
                composition,
                root,
                "battle-invite",
                "battle-joined");

            var visitorActive = await WaitForBattleActiveAsync(
                diagnostics,
                ClientBattleTargetKind.VisitWorld,
                ClientBattleRole.Visitor);
            WriteSignal(
                root,
                "visitor-battle-slot",
                visitorActive.Runtime.Runtime.ActorSlot.ToString());
            var ownerSlot = await ReadActorSlotSignalAsync(
                root,
                "owner-battle-slot");
            if (ownerSlot == visitorActive.Runtime.Runtime.ActorSlot)
            {
                throw new QualificationFailureException(
                    "battle-actor-slot-collision");
            }

            await WaitForSceneActorsAsync(
                diagnostics,
                expectedActors: 6,
                stage: "visitor-joined");
            var ownerEntityID = (ulong)(ownerSlot + 1);
            var ownerBefore = await CaptureActorTransformAsync(
                composition,
                ownerEntityID);
            WriteSignal(root, "visitor-observer-ready", "ready");
            await ReadRequiredSignalAsync(root, "owner-motion-complete");
            await WaitForActorTransformChangeAsync(
                composition,
                diagnostics,
                ownerEntityID,
                ownerBefore);
            WriteSignal(root, "visitor-observed-owner", "pass");

            await ReadRequiredSignalAsync(root, "owner-observer-ready");
            await ExerciseLocalBattleAsync(
                composition,
                diagnostics,
                moveXMilli: -1000,
                moveYMilli: 0,
                aimYawMillidegrees: -45000);
            await ExerciseCombatAsync(
                composition,
                diagnostics,
                expectedEntities: 6);
            WriteSignal(root, "visitor-motion-complete", "pass");
            await ReadRequiredSignalAsync(root, "owner-observed-visitor");
            WriteSignal(root, "visitor-pre-recovery-ready", "ready");
            await ReadRequiredSignalAsync(root, "owner-pre-recovery-ready");
            await ReadRequiredSignalAsync(root, "owner-recovered");
            await RunCooperativeBossDefeatAsync(
                composition,
                diagnostics,
                root,
                localRole: "visitor",
                remoteRole: "owner");
            await AssertBattleContinuityAsync(
                diagnostics,
                visitorActive.Runtime.Runtime.BattleGeneration,
                ClientBattleTargetKind.VisitWorld,
                ClientBattleRole.Visitor);
            WriteSignal(root, "visitor-continuity-pass", "pass");
            await ReadRequiredSignalAsync(root, "owner-continuity-pass");

            var beforeSafeReturn = diagnostics.CaptureBattle();
            if (!diagnostics.InjectBattleTransportDisconnect())
            {
                throw new QualificationFailureException(
                    "battle-visitor-disconnect-not-injected");
            }

            WriteSignal(root, "visitor-disconnected", "pass");
            await ReadRequiredSignalAsync(root, "safe-return-triggered");
            var successor = await WaitForBattleSuccessorAsync(
                diagnostics,
                beforeSafeReturn.Runtime.Runtime.BattleGeneration,
                ClientBattleTargetKind.OwnWorld,
                ClientBattleRole.Owner);
            await WaitForSceneActorsAsync(
                diagnostics,
                expectedActors: 5,
                stage: "visitor-safe-return");
            await WaitUntilAsync(
                () => IsStableOwnWorld(composition, diagnostics.Capture()),
                TransitionObservationDeadline);
            await Task.Delay(TimeSpan.FromMilliseconds(500));
            var stable = diagnostics.CaptureBattle();
            if (stable.Runtime.Runtime.BattleGeneration !=
                    successor.Runtime.Runtime.BattleGeneration ||
                stable.Runtime.Runtime.TargetKind !=
                    ClientBattleTargetKind.OwnWorld ||
                stable.Runtime.Runtime.Availability !=
                    ClientBattleAvailability.Active)
            {
                throw new QualificationFailureException(
                    "battle-old-callback-overwrote-successor");
            }

            WriteSignal(root, "visitor-safe-return", "pass");
            await LogoutAndAssertBattleReleasedAsync(composition, diagnostics);
            WriteSignal(root, "visitor-cleanup", "pass");
            await ReadRequiredSignalAsync(root, "owner-cleanup");
        }

        /// <summary>执行Owner产品流程、独立通道故障、进程恢复与Session失效检查。</summary>
        /// <param name="composition">Owner完整产品graph。</param>
        /// <param name="root">本次operator运行独占协调目录。</param>
        /// <returns>Owner负责的矩阵全部通过时完成。</returns>
        private static async Task RunOwnerProductFaultAsync(
            AppCompositionResult composition,
            string root)
        {
            var resumed = SignalExists(root, "resume-owner");
            await LoginOrRestoreOwnWorldAsync(composition, requireCredentials: !resumed);
            var diagnostics = composition.CreateQualificationDiagnostics();
            var baseline = diagnostics.Capture();
            AssertStableResources(baseline, baseline);

            if (resumed)
            {
                await RunResumedOwnerProductFaultAsync(composition, diagnostics, baseline, root);
                return;
            }

            WriteSignal(root, "owner-ready", composition.PersonalWorldExperience.ViewState.Shell.PlayerID);
            var visitorPlayerID = await ReadRequiredSignalAsync(root, "visitor-ready");
            await RequireActionAsync(
                composition.PersonalWorldExperience.OpenVisitAsync(CancellationToken.None),
                "owner-open-rejected");
            await WaitUntilAsync(
                () => composition.PersonalWorldExperience.ViewState.WorldVisit.IsOwner,
                TransitionObservationDeadline);

            await PublishInviteAsync(composition, root, "invite-1", visitorPlayerID);
            await WaitForOwnerMemberAsync(composition, root, "joined-1", visitorPlayerID);
            WriteSignal(root, "leave-1", "go");
            await WaitForOwnerMemberRemovalAsync(composition, root, "left-1", visitorPlayerID);

            await PublishInviteAsync(composition, root, "invite-2", visitorPlayerID);
            await WaitForOwnerMemberAsync(composition, root, "joined-2", visitorPlayerID);
            await RequireActionAsync(
                composition.PersonalWorldExperience.KickVisitorAsync(
                    visitorPlayerID,
                    CancellationToken.None),
                "owner-kick-rejected");
            await WaitUntilAsync(
                () => !Contains(
                    composition.PersonalWorldExperience.ViewState.WorldVisit.VisitorPlayerIDs,
                    visitorPlayerID),
                TransitionObservationDeadline);
            WriteSignal(root, "kicked-2", "pass");

            await PublishInviteAsync(composition, root, "invite-3", visitorPlayerID);
            await WaitForOwnerMemberAsync(composition, root, "joined-3", visitorPlayerID);
            await RequireActionAsync(
                composition.PersonalWorldExperience.CloseVisitAsync(CancellationToken.None),
                "owner-close-rejected");
            await WaitUntilAsync(
                () => composition.VisitSessionService.Snapshot.Current == null,
                TransitionObservationDeadline);
            WriteSignal(root, "closed-3", "pass");

            await RequireActionAsync(
                composition.PersonalWorldExperience.OpenVisitAsync(CancellationToken.None),
                "owner-reopen-rejected");
            await PublishInviteAsync(composition, root, "invite-4", visitorPlayerID);
            await WaitForOwnerMemberAsync(composition, root, "joined-4", visitorPlayerID);
            await ReadRequiredSignalAsync(root, "visitor-permission-pass");

            var visitID = composition.PersonalWorldExperience.ViewState.WorldVisit.VisitSessionID;
            var controlGeneration = composition.ControlChannel.Snapshot.Generation;
            var gameplayGeneration = composition.GameplayChannel.Snapshot.Generation;
            if (!composition.ControlChannel.InjectQualificationTransportDisconnect())
            {
                throw new QualificationFailureException("owner-control-fault-not-injected");
            }

            await WaitUntilAsync(
                () => composition.ControlChannel.Snapshot.State == ClientControlChannelState.Connected &&
                      composition.ControlChannel.Snapshot.Generation > controlGeneration &&
                      composition.GameplayChannel.Snapshot.Generation == gameplayGeneration &&
                      HasSameOwnerVisit(composition, visitID, visitorPlayerID),
                TransitionObservationDeadline);
            await WaitForStableResourcesAsync(diagnostics, baseline);
            WriteSignal(root, "owner-control-pass", "pass");

            gameplayGeneration = composition.GameplayChannel.Snapshot.Generation;
            if (!composition.GameplayChannel.InjectQualificationTransportDisconnect())
            {
                throw new QualificationFailureException("owner-gameplay-fault-not-injected");
            }

            await WaitUntilAsync(
                () => composition.GameplayChannel.Snapshot.State == ClientGameplayChannelState.Active &&
                      composition.GameplayChannel.Snapshot.Generation > gameplayGeneration &&
                      HasSameOwnerVisit(composition, visitID, visitorPlayerID) &&
                      IsStableOwnWorld(composition, diagnostics.Capture()),
                TransitionObservationDeadline);
            await WaitForStableResourcesAsync(diagnostics, baseline);
            WriteSignal(root, "owner-gameplay-pass", "pass");

            WriteSignal(root, "run-visitor-gameplay", "go");
            await ReadRequiredSignalAsync(root, "visitor-gameplay-pass");
            await WaitUntilAsync(
                () => HasSameOwnerVisit(composition, visitID, visitorPlayerID),
                TransitionObservationDeadline);
            WriteSignal(root, "ready-owner-restart", "ready");
            await WaitForTerminationAsync();
        }

        /// <summary>在同一安全profile恢复后验证Owner事实并收尾Visitor重启与Session失效。</summary>
        /// <param name="composition">恢复后的Owner完整产品graph。</param>
        /// <param name="diagnostics">当前只读资源诊断。</param>
        /// <param name="baseline">恢复后的稳定OwnWorld资源基线。</param>
        /// <param name="root">本次operator运行独占协调目录。</param>
        /// <returns>恢复、重邀与失效全部提交时完成。</returns>
        private static async Task RunResumedOwnerProductFaultAsync(
            AppCompositionResult composition,
            ClientQualificationDiagnostics diagnostics,
            ClientQualificationDiagnosticSnapshot baseline,
            string root)
        {
            var visitorPlayerID = await ReadRequiredSignalAsync(root, "visitor-ready");
            await WaitUntilAsync(
                () => composition.PersonalWorldExperience.ViewState.WorldVisit.IsOwner &&
                      Contains(
                          composition.PersonalWorldExperience.ViewState.WorldVisit.VisitorPlayerIDs,
                          visitorPlayerID),
                TransitionObservationDeadline);
            await WaitForStableResourcesAsync(diagnostics, baseline);
            WriteSignal(root, "owner-restart-pass", "pass");

            await ReadRequiredSignalAsync(root, "visitor-restart-pass");
            if (Contains(
                    composition.PersonalWorldExperience.ViewState.WorldVisit.VisitorPlayerIDs,
                    visitorPlayerID))
            {
                await RequireActionAsync(
                    composition.PersonalWorldExperience.KickVisitorAsync(
                        visitorPlayerID,
                        CancellationToken.None),
                    "post-restart-kick-rejected");
            }

            await WaitUntilAsync(
                () => !Contains(
                    composition.PersonalWorldExperience.ViewState.WorldVisit.VisitorPlayerIDs,
                    visitorPlayerID),
                TransitionObservationDeadline);
            await PublishInviteAsync(composition, root, "invite-5", visitorPlayerID);
            await WaitForOwnerMemberAsync(composition, root, "joined-5", visitorPlayerID);
            WriteSignal(root, "leave-5", "go");
            await WaitForOwnerMemberRemovalAsync(composition, root, "left-5", visitorPlayerID);
            await RequireActionAsync(
                composition.PersonalWorldExperience.CloseVisitAsync(CancellationToken.None),
                "post-restart-close-rejected");
            await WaitUntilAsync(
                () => composition.VisitSessionService.Snapshot.Current == null,
                TransitionObservationDeadline);

            await RequireActionAsync(
                composition.PersonalWorldExperience.LogoutAsync(CancellationToken.None),
                "session-invalidation-rejected");
            await WaitUntilAsync(
                () => composition.PersonalWorldExperience.ViewState.Phase == ClientPersonalWorldPhase.Login &&
                      !composition.SessionCoordinator.TryGetCurrent(out _),
                TransitionObservationDeadline);
            WriteSignal(root, "session-invalidation-pass", "pass");
            WriteSignal(root, "product-fault-pass", "pass");
        }

        /// <summary>执行Visitor产品动作、权限派生、gameplay grace恢复与进程恢复。</summary>
        /// <param name="composition">Visitor完整产品graph。</param>
        /// <param name="root">本次operator运行独占协调目录。</param>
        /// <returns>Visitor负责的矩阵全部通过时完成。</returns>
        private static async Task RunVisitorProductFaultAsync(
            AppCompositionResult composition,
            string root)
        {
            var resumed = SignalExists(root, "resume-visitor");
            await LoginOrRestoreOwnWorldAsync(composition, requireCredentials: !resumed);
            var diagnostics = composition.CreateQualificationDiagnostics();
            var baseline = diagnostics.Capture();
            AssertStableResources(baseline, baseline);
            WriteSignal(root, "visitor-ready", composition.PersonalWorldExperience.ViewState.Shell.PlayerID);

            if (resumed)
            {
                WriteSignal(root, "visitor-restart-pass", "pass");
                await AcceptPublishedInviteAsync(composition, root, "invite-5", "joined-5");
                await ReadRequiredSignalAsync(root, "leave-5");
                await RequireActionAsync(
                    composition.PersonalWorldExperience.LeaveVisitAsync(CancellationToken.None),
                    "post-restart-leave-rejected");
                await WaitUntilAsync(
                    () => IsStableOwnWorld(composition, diagnostics.Capture()),
                    TransitionObservationDeadline);
                WriteSignal(root, "left-5", "pass");
                return;
            }

            await ReadRequiredSignalAsync(root, "owner-ready");
            await AcceptPublishedInviteAsync(composition, root, "invite-1", "joined-1");
            await ReadRequiredSignalAsync(root, "leave-1");
            await RequireActionAsync(
                composition.PersonalWorldExperience.LeaveVisitAsync(CancellationToken.None),
                "visitor-leave-rejected");
            await WaitUntilAsync(
                () => IsStableOwnWorld(composition, diagnostics.Capture()),
                TransitionObservationDeadline);
            WriteSignal(root, "left-1", "pass");

            await AcceptPublishedInviteAsync(composition, root, "invite-2", "joined-2");
            await ReadRequiredSignalAsync(root, "kicked-2");
            await WaitUntilAsync(
                () => IsStableOwnWorld(composition, diagnostics.Capture()),
                TransitionObservationDeadline);

            await AcceptPublishedInviteAsync(composition, root, "invite-3", "joined-3");
            await ReadRequiredSignalAsync(root, "closed-3");
            await WaitUntilAsync(
                () => IsStableOwnWorld(composition, diagnostics.Capture()),
                TransitionObservationDeadline);

            await AcceptPublishedInviteAsync(composition, root, "invite-4", "joined-4");
            var actions = composition.PersonalWorldExperience.ViewState.WorldVisit.Actions;
            if (actions.CanOpenVisit || actions.CanCreateInvite || actions.CanRevokeInvite ||
                actions.CanKickVisitor || actions.CanCloseVisit || actions.CanAcceptInvite ||
                !actions.CanLeaveVisit)
            {
                throw new QualificationFailureException("visitor-permission-drift");
            }

            WriteSignal(root, "visitor-permission-pass", "pass");
            await ReadRequiredSignalAsync(root, "run-visitor-gameplay");
            var visitID = composition.PersonalWorldExperience.ViewState.WorldVisit.VisitSessionID;
            var gameplayGeneration = composition.GameplayChannel.Snapshot.Generation;
            if (!composition.GameplayChannel.InjectQualificationTransportDisconnect())
            {
                throw new QualificationFailureException("visitor-gameplay-fault-not-injected");
            }

            await WaitUntilAsync(
                () => composition.GameplayChannel.Snapshot.State == ClientGameplayChannelState.Active &&
                      composition.GameplayChannel.Snapshot.Generation > gameplayGeneration &&
                      composition.PersonalWorldExperience.ViewState.Phase == ClientPersonalWorldPhase.Visiting &&
                      string.Equals(
                          composition.PersonalWorldExperience.ViewState.WorldVisit.VisitSessionID,
                          visitID,
                          StringComparison.Ordinal),
                TransitionObservationDeadline);
            await WaitForStableResourcesAsync(diagnostics, baseline);
            WriteSignal(root, "visitor-gameplay-pass", "pass");
            WriteSignal(root, "ready-visitor-restart", "ready");
            await WaitForTerminationAsync();
        }

        /// <summary>执行Owner在真实服务端停机、恢复后的权威OwnWorld收敛与重邀。</summary>
        /// <param name="composition">Owner完整产品graph。</param>
        /// <param name="root">本次operator运行独占协调目录。</param>
        /// <returns>停机、恢复和重邀全部通过时完成。</returns>
        private static async Task RunOwnerServerRestartAsync(
            AppCompositionResult composition,
            string root)
        {
            await LoginOrRestoreOwnWorldAsync(composition, requireCredentials: true);
            WriteSignal(root, "restart-owner-ready", composition.PersonalWorldExperience.ViewState.Shell.PlayerID);
            var visitorPlayerID = await ReadRequiredSignalAsync(root, "restart-visitor-ready");
            await RequireActionAsync(
                composition.PersonalWorldExperience.OpenVisitAsync(CancellationToken.None),
                "restart-open-rejected");
            await PublishInviteAsync(composition, root, "restart-invite-1", visitorPlayerID);
            await WaitForOwnerMemberAsync(composition, root, "restart-joined-1", visitorPlayerID);
            WriteSignal(root, "ready-server-stop", "ready");

            await ReadRequiredSignalAsync(root, "server-offline");
            await AssertOfflineRetryIsBoundedAsync(composition);
            WriteSignal(root, "owner-offline-pass", "pass");
            await ReadRequiredSignalAsync(root, "server-online");
            await RecoverToOwnWorldAsync(composition);
            WriteSignal(root, "owner-recovered", "pass");
            await ReadRequiredSignalAsync(root, "visitor-recovered");

            await RequireActionAsync(
                composition.PersonalWorldExperience.OpenVisitAsync(CancellationToken.None),
                "restart-reopen-rejected");
            await PublishInviteAsync(composition, root, "restart-invite-2", visitorPlayerID);
            await WaitForOwnerMemberAsync(composition, root, "restart-joined-2", visitorPlayerID);
            await RequireActionAsync(
                composition.PersonalWorldExperience.CloseVisitAsync(CancellationToken.None),
                "restart-final-close-rejected");
            WriteSignal(root, "restart-closed-2", "pass");
            WriteSignal(root, "server-restart-pass", "pass");
        }

        /// <summary>执行Visitor在真实服务端停机、恢复后的OwnWorld收敛与重邀。</summary>
        /// <param name="composition">Visitor完整产品graph。</param>
        /// <param name="root">本次operator运行独占协调目录。</param>
        /// <returns>停机、恢复和重邀全部通过时完成。</returns>
        private static async Task RunVisitorServerRestartAsync(
            AppCompositionResult composition,
            string root)
        {
            await LoginOrRestoreOwnWorldAsync(composition, requireCredentials: true);
            WriteSignal(root, "restart-visitor-ready", composition.PersonalWorldExperience.ViewState.Shell.PlayerID);
            await ReadRequiredSignalAsync(root, "restart-owner-ready");
            await AcceptPublishedInviteAsync(composition, root, "restart-invite-1", "restart-joined-1");
            await ReadRequiredSignalAsync(root, "server-offline");
            await AssertOfflineRetryIsBoundedAsync(composition);
            WriteSignal(root, "visitor-offline-pass", "pass");
            await ReadRequiredSignalAsync(root, "server-online");
            await RecoverToOwnWorldAsync(composition);
            WriteSignal(root, "visitor-recovered", "pass");

            await AcceptPublishedInviteAsync(composition, root, "restart-invite-2", "restart-joined-2");
            await ReadRequiredSignalAsync(root, "restart-closed-2");
            var diagnostics = composition.CreateQualificationDiagnostics();
            await WaitUntilAsync(
                () => IsStableOwnWorld(composition, diagnostics.Capture()),
                TransitionObservationDeadline);
        }

        /// <summary>等待真实battle baseline、input gate、active actor与唯一资源owner同时就绪。</summary>
        /// <param name="diagnostics">只读battle诊断owner。</param>
        /// <param name="targetKind">预期current target类别。</param>
        /// <param name="role">预期authenticated角色。</param>
        /// <returns>全部条件来自同一current generation的快照。</returns>
        private static async Task<ClientBattleQualificationDiagnosticSnapshot>
            WaitForBattleActiveAsync(
                ClientQualificationDiagnostics diagnostics,
                ClientBattleTargetKind targetKind,
                ClientBattleRole role)
        {
            await WaitUntilAsync(
                () => TryCaptureBattle(diagnostics, out var current) &&
                      IsActiveBattle(current, targetKind, role),
                TransitionObservationDeadline);
            if (!TryCaptureBattle(diagnostics, out var snapshot) ||
                !IsActiveBattle(snapshot, targetKind, role))
            {
                throw new QualificationFailureException(
                    "battle-active-capture-drifted");
            }

            return snapshot;
        }

        /// <summary>等待旧battle generation退役且current target建立更高完整generation。</summary>
        /// <param name="diagnostics">只读battle诊断owner。</param>
        /// <param name="previousGeneration">必须退役的正generation。</param>
        /// <param name="targetKind">successor预期target。</param>
        /// <param name="role">successor预期角色。</param>
        /// <returns>更高且已Active的successor快照。</returns>
        private static async Task<ClientBattleQualificationDiagnosticSnapshot>
            WaitForBattleSuccessorAsync(
                ClientQualificationDiagnostics diagnostics,
                long previousGeneration,
                ClientBattleTargetKind targetKind,
                ClientBattleRole role)
        {
            await WaitUntilAsync(
                () => TryCaptureBattle(diagnostics, out var current) &&
                      current.Runtime.Runtime.BattleGeneration >
                          previousGeneration &&
                      IsActiveBattle(current, targetKind, role),
                TransitionObservationDeadline);
            var snapshot = await WaitForBattleActiveAsync(
                diagnostics,
                targetKind,
                role);
            if (snapshot.Runtime.Runtime.BattleGeneration <= previousGeneration)
            {
                throw new QualificationFailureException(
                    "battle-successor-generation-not-advanced");
            }

            return snapshot;
        }

        /// <summary>持续验证同一battle generation保持Active、Tick推进且资源owner唯一。</summary>
        /// <param name="diagnostics">只读battle诊断owner。</param>
        /// <param name="generation">不得被静默替换或清空的current generation。</param>
        /// <param name="targetKind">预期target。</param>
        /// <param name="role">预期角色。</param>
        /// <returns>跨越已知故障时段后完成。</returns>
        private static async Task AssertBattleContinuityAsync(
            ClientQualificationDiagnostics diagnostics,
            long generation,
            ClientBattleTargetKind targetKind,
            ClientBattleRole role)
        {
            var deadline = DateTime.UtcNow.Add(BattleContinuityDuration);
            var advanceDeadline = DateTime.UtcNow.AddSeconds(5);
            DateTime? inputUnavailableSince = null;
            ulong observedServerTick = 0;
            while (DateTime.UtcNow < deadline)
            {
                if (!TryCaptureBattle(diagnostics, out var current) ||
                    current.Runtime.Runtime.Availability !=
                        ClientBattleAvailability.Active ||
                    current.Runtime.Runtime.Failure != ClientBattleFailure.None ||
                    current.Runtime.Runtime.BattleGeneration != generation ||
                    current.Runtime.Runtime.TargetKind != targetKind ||
                    current.Runtime.Runtime.Role != role ||
                    !current.Runtime.Runtime.BaselineReady ||
                    current.Runtime.EntityCount == 0 ||
                    current.Runtime.LocalEntityID !=
                        (ulong)(current.Runtime.Runtime.ActorSlot + 1) ||
                    current.SocketOwners != 1 ||
                    current.PumpOwners != 3 ||
                    current.NativeLeases != 1)
                {
#if DEVELOPMENT_BUILD || UNITY_EDITOR
                    if (current != null)
                    {
                        Debug.LogError(
                            "[IHOMELAND_BATTLE_DIAGNOSTIC] " +
                            "stage=long-continuity-drift " +
                            $"generation={current.Runtime.Runtime.BattleGeneration} " +
                            $"availability={current.Runtime.Runtime.Availability} " +
                            $"failure={current.Runtime.Runtime.Failure} " +
                            $"baseline_ready={current.Runtime.Runtime.BaselineReady} " +
                            $"input_enabled={current.Runtime.Runtime.InputEnabled} " +
                            $"entities={current.Runtime.EntityCount} " +
                            $"resync_pending={current.Runtime.ResyncPending} " +
                            $"socket_owners={current.SocketOwners} " +
                            $"pump_owners={current.PumpOwners} " +
                            $"native_leases={current.NativeLeases}");
                    }
#endif
                    throw new QualificationFailureException(
                        "battle-long-continuity-drifted");
                }

                if (!current.Runtime.Runtime.InputEnabled)
                {
                    inputUnavailableSince ??= DateTime.UtcNow;
                    if (DateTime.UtcNow - inputUnavailableSince.Value >
                        TimeSpan.FromSeconds(2))
                    {
                        throw new QualificationFailureException(
                            "battle-long-continuity-input-stalled");
                    }
                }
                else
                {
                    inputUnavailableSince = null;
                }

                if (current.Runtime.ServerTick < observedServerTick)
                {
                    throw new QualificationFailureException(
                        "battle-long-continuity-tick-regressed");
                }

                if (current.Runtime.ServerTick > observedServerTick)
                {
                    observedServerTick = current.Runtime.ServerTick;
                    advanceDeadline = DateTime.UtcNow.AddSeconds(5);
                }
                else if (DateTime.UtcNow >= advanceDeadline)
                {
                    throw new QualificationFailureException(
                        "battle-long-continuity-tick-stalled");
                }

                await Task.Delay(ObservationInterval);
            }
        }

        /// <summary>用封闭semantic input验证真实C++ move、jump、aim、ack与本地收敛。</summary>
        /// <param name="composition">当前完整产品graph。</param>
        /// <param name="diagnostics">只读battle诊断owner。</param>
        /// <param name="moveXMilli">量化水平移动输入。</param>
        /// <param name="moveYMilli">量化纵向移动输入。</param>
        /// <param name="aimYawMillidegrees">预期server规范yaw。</param>
        /// <returns>落地、ack推进且预测回到容差内时完成。</returns>
        private static async Task ExerciseLocalBattleAsync(
            AppCompositionResult composition,
            ClientQualificationDiagnostics diagnostics,
            int moveXMilli,
            int moveYMilli,
            int aimYawMillidegrees)
        {
            if (!TryCaptureBattle(diagnostics, out var start) ||
                start.Runtime.Runtime.Availability !=
                    ClientBattleAvailability.Active)
            {
                throw new QualificationFailureException(
                    "battle-local-exercise-not-active");
            }

            var generation = start.Runtime.Runtime.BattleGeneration;
            var startTransform = start.Runtime.AuthorityTransform;
            var jumpObserved = false;
            var acceptedSamples = 0;
            var inputDeadline = DateTime.UtcNow.Add(
                TransitionObservationDeadline);
            var settledInput = new ClientBattleSemanticInput(
                0,
                0,
                aimYawMillidegrees,
                0,
                jumpPressed: false,
                primaryPressed: false,
                secondaryPressed: false,
                interactPressed: false,
                interactionSlot: 0);
            var expectedYaw = settledInput.AimYawMillidegrees;
            try
            {
                while (acceptedSamples < 44)
                {
                    var input = new ClientBattleSemanticInput(
                        moveXMilli,
                        moveYMilli,
                        aimYawMillidegrees,
                        0,
                        jumpPressed: !jumpObserved,
                        primaryPressed: false,
                        secondaryPressed: false,
                        interactPressed: false,
                        interactionSlot: 0);
                    if (!composition.BattleRuntimeCoordinator.
                            TrySetQualificationInput(generation, input))
                    {
                        if (!TryCaptureBattle(
                                diagnostics,
                                out var rejected) ||
                            rejected.Runtime.Runtime.BattleGeneration !=
                                generation ||
                            rejected.Runtime.Runtime.Availability !=
                                ClientBattleAvailability.Active)
                        {
                            throw new QualificationFailureException(
                                "battle-qualification-input-drifted");
                        }

                        if (DateTime.UtcNow >= inputDeadline)
                        {
                            throw new QualificationFailureException(
                                "battle-qualification-input-rejected");
                        }

                        await Task.Delay(ObservationInterval);
                        continue;
                    }

                    acceptedSamples++;
                    await Task.Delay(TimeSpan.FromMilliseconds(25));
                    if (TryCaptureBattle(diagnostics, out var current) &&
                        current.Runtime.Runtime.BattleGeneration == generation &&
                        (!current.Runtime.AuthorityGrounded ||
                         current.Runtime.AuthorityTransform.
                             PositionYMillimeters >
                         startTransform.PositionYMillimeters))
                    {
                        jumpObserved = true;
                    }
                }

                var settleDeadline = DateTime.UtcNow.Add(
                    TransitionObservationDeadline);
                while (!composition.BattleRuntimeCoordinator.
                           TrySetQualificationInput(generation, settledInput))
                {
                    if (!TryCaptureBattle(
                            diagnostics,
                            out var settling) ||
                        settling.Runtime.Runtime.BattleGeneration !=
                            generation ||
                        settling.Runtime.Runtime.Availability !=
                            ClientBattleAvailability.Active)
                    {
                        throw new QualificationFailureException(
                            "battle-qualification-settle-input-drifted");
                    }

                    if (DateTime.UtcNow >= settleDeadline)
                    {
                        throw new QualificationFailureException(
                            "battle-qualification-settle-input-rejected");
                    }

                    await Task.Delay(ObservationInterval);
                }

                var jumpDeadline = DateTime.UtcNow.Add(
                    TransitionObservationDeadline);
                while (!jumpObserved)
                {
                    composition.BattleRuntimeCoordinator.
                        TrySetQualificationInput(generation, settledInput);
                    if (!TryCaptureBattle(
                            diagnostics,
                            out var current) ||
                        current.Runtime.Runtime.BattleGeneration !=
                            generation ||
                        current.Runtime.Runtime.Availability !=
                            ClientBattleAvailability.Active)
                    {
                        throw new QualificationFailureException(
                            "battle-local-exercise-drifted");
                    }

                    jumpObserved =
                        !current.Runtime.AuthorityGrounded ||
                        current.Runtime.AuthorityTransform.
                            PositionYMillimeters >
                        startTransform.PositionYMillimeters;
                    if (jumpObserved)
                    {
                        break;
                    }

                    if (DateTime.UtcNow >= jumpDeadline)
                    {
                        Debug.Log(
                            "[IHOMELAND_BATTLE_DIAGNOSTIC] " +
                            "stage=jump-not-observed " +
                            $"generation={current.Runtime.Runtime.BattleGeneration} " +
                            $"server_tick={current.Runtime.ServerTick} " +
                            $"sent_input_tick={current.Runtime.LastSentInputTick} " +
                            $"ack_input_tick={current.Runtime.LastAcknowledgedInputTick} " +
                            $"authority_x_mm={current.Runtime.AuthorityTransform.PositionXMillimeters} " +
                            $"authority_y_mm={current.Runtime.AuthorityTransform.PositionYMillimeters} " +
                            $"authority_z_mm={current.Runtime.AuthorityTransform.PositionZMillimeters} " +
                            $"authority_yaw_mdeg={current.Runtime.AuthorityTransform.YawMillidegrees} " +
                            $"predicted_x_mm={current.Runtime.PredictedTransform.PositionXMillimeters} " +
                            $"predicted_y_mm={current.Runtime.PredictedTransform.PositionYMillimeters} " +
                            $"predicted_z_mm={current.Runtime.PredictedTransform.PositionZMillimeters} " +
                            $"predicted_yaw_mdeg={current.Runtime.PredictedTransform.YawMillidegrees} " +
                            $"authority_grounded={current.Runtime.AuthorityGrounded} " +
                            $"input_enabled={current.Runtime.Runtime.InputEnabled} " +
                            $"resync_pending={current.Runtime.ResyncPending}");
                        throw new QualificationFailureException(
                            "battle-authority-jump-not-observed");
                    }

                    await Task.Delay(ObservationInterval);
                }

                var convergenceDeadline = DateTime.UtcNow.Add(
                    TransitionObservationDeadline);
                while (true)
                {
                    composition.BattleRuntimeCoordinator.
                        TrySetQualificationInput(generation, settledInput);
                    if (TryCaptureBattle(diagnostics, out var current) &&
                        current.Runtime.Runtime.BattleGeneration == generation &&
                        current.Runtime.Runtime.Availability ==
                            ClientBattleAvailability.Active)
                    {
                        var authority = current.Runtime.AuthorityTransform;
                        var horizontalMoved =
                            authority.PositionXMillimeters !=
                                startTransform.PositionXMillimeters ||
                            authority.PositionZMillimeters !=
                                startTransform.PositionZMillimeters;
                        var withinCorrection =
                            authority.PositionDistanceSquared(
                                current.Runtime.PredictedTransform) <=
                            (long)ClientBattlePolicy.Current.
                                CorrectionPositionMillimeters *
                            ClientBattlePolicy.Current.
                                CorrectionPositionMillimeters;
                        if (horizontalMoved &&
                            authority.YawMillidegrees == expectedYaw &&
                            current.Runtime.AuthorityGrounded &&
                            current.Runtime.ServerTick >
                                start.Runtime.ServerTick &&
                            current.Runtime.LastAcknowledgedInputTick >
                                start.Runtime.LastAcknowledgedInputTick &&
                            current.Runtime.LastAcknowledgedInputTick <=
                                Math.Max(
                                    current.Runtime.LastSentInputTick,
                                    current.Runtime.
                                        ContinuityAcknowledgementAnchor) &&
                            withinCorrection)
                        {
                            return;
                        }

                        if (DateTime.UtcNow >= convergenceDeadline)
                        {
                            Debug.Log(
                                "[IHOMELAND_BATTLE_DIAGNOSTIC] " +
                                "stage=local-convergence-timeout " +
                                $"generation={current.Runtime.Runtime.BattleGeneration} " +
                                $"server_tick={current.Runtime.ServerTick} " +
                                $"start_server_tick={start.Runtime.ServerTick} " +
                                $"sent_input_tick={current.Runtime.LastSentInputTick} " +
                                $"ack_input_tick={current.Runtime.LastAcknowledgedInputTick} " +
                                $"ack_anchor={current.Runtime.ContinuityAcknowledgementAnchor} " +
                                $"start_ack_input_tick={start.Runtime.LastAcknowledgedInputTick} " +
                                $"authority_x_mm={authority.PositionXMillimeters} " +
                                $"authority_y_mm={authority.PositionYMillimeters} " +
                                $"authority_z_mm={authority.PositionZMillimeters} " +
                                $"authority_yaw_mdeg={authority.YawMillidegrees} " +
                                $"expected_yaw_mdeg={expectedYaw} " +
                                $"predicted_x_mm={current.Runtime.PredictedTransform.PositionXMillimeters} " +
                                $"predicted_y_mm={current.Runtime.PredictedTransform.PositionYMillimeters} " +
                                $"predicted_z_mm={current.Runtime.PredictedTransform.PositionZMillimeters} " +
                                $"predicted_yaw_mdeg={current.Runtime.PredictedTransform.YawMillidegrees} " +
                                $"authority_grounded={current.Runtime.AuthorityGrounded} " +
                                $"within_correction={withinCorrection}");
                            throw new QualificationFailureException(
                                "battle-local-convergence-timeout");
                        }
                    }
                    else if (DateTime.UtcNow >= convergenceDeadline)
                    {
                        throw new QualificationFailureException(
                            "battle-local-exercise-drifted");
                    }

                    await Task.Delay(ObservationInterval);
                }
            }
            finally
            {
                composition.BattleRuntimeCoordinator.
                    ClearQualificationInput(generation);
            }
        }

        /// <summary>验证current武器primary、切换武器及另一primary均由真实authority提交。</summary>
        /// <param name="composition">当前完整产品graph。</param>
        /// <param name="diagnostics">只读battle诊断owner。</param>
        /// <param name="expectedEntities">不含暂态projectile的当前actor数量。</param>
        /// <returns>两种武器的ability event与weapon projection均被观察到时完成。</returns>
        private static async Task ExerciseCombatAsync(
            AppCompositionResult composition,
            ClientQualificationDiagnostics diagnostics,
            int expectedEntities)
        {
            if (!TryCaptureBattle(diagnostics, out var start) ||
                start.Runtime.Runtime.Availability !=
                    ClientBattleAvailability.Active ||
                start.Runtime.EntityCount != expectedEntities ||
                start.Runtime.BossMaxHealthMilli != 300000 ||
                start.Runtime.BossHealthMilli == 0 ||
                start.Runtime.BossHealthMilli >
                    start.Runtime.BossMaxHealthMilli ||
                start.Runtime.BossDead ||
                start.Runtime.LocalMaxHealthMilli != 100000 ||
                start.Runtime.LocalHealthMilli == 0 ||
                start.Runtime.LocalDead ||
                !ClientBattleContentIdentity.IsKnownWeapon(
                    start.Runtime.LocalEquippedWeaponID))
            {
                throw new QualificationFailureException(
                    "battle-combat-baseline-invalid");
            }

            var generation = start.Runtime.Runtime.BattleGeneration;
            var firstWeapon = start.Runtime.LocalEquippedWeaponID;
            var firstAbility = firstWeapon ==
                ClientBattleContentIdentity.SwordWeapon
                    ? ClientBattleContentIdentity.SwordAbility
                    : ClientBattleContentIdentity.FanAbility;
            var firstEventCount =
                start.Runtime.ObservedLocalAbilityEventCount;
            var neutral = new ClientBattleSemanticInput(
                0,
                0,
                0,
                0,
                jumpPressed: false,
                primaryPressed: false,
                secondaryPressed: false,
                interactPressed: false,
                interactionSlot: 0);
            try
            {
                await SubmitQualificationInputAsync(
                    composition,
                    diagnostics,
                    generation,
                    new ClientBattleSemanticInput(
                        0,
                        0,
                        0,
                        0,
                        jumpPressed: false,
                        primaryPressed: true,
                        secondaryPressed: false,
                        interactPressed: false,
                        interactionSlot: 0));
                composition.BattleRuntimeCoordinator.
                    TrySetQualificationInput(generation, neutral);
                await WaitForLocalAbilityAsync(
                    diagnostics,
                    generation,
                    firstEventCount,
                    firstAbility,
                    "battle-first-primary-not-observed");

                await SubmitQualificationInputAsync(
                    composition,
                    diagnostics,
                    generation,
                    new ClientBattleSemanticInput(
                        0,
                        0,
                        0,
                        0,
                        jumpPressed: false,
                        primaryPressed: false,
                        secondaryPressed: false,
                        interactPressed: false,
                        interactionSlot: 0,
                        switchWeaponPressed: true));
                composition.BattleRuntimeCoordinator.
                    TrySetQualificationInput(generation, neutral);
                await WaitUntilAsync(
                    () => TryCaptureBattle(diagnostics, out var current) &&
                          current.Runtime.Runtime.BattleGeneration == generation &&
                          current.Runtime.LocalEquippedWeaponID != firstWeapon &&
                          ClientBattleContentIdentity.IsKnownWeapon(
                              current.Runtime.LocalEquippedWeaponID),
                    TransitionObservationDeadline,
                    "battle-weapon-switch-not-observed");

                var switched = diagnostics.CaptureBattle();
                var switchedEventCount =
                    switched.Runtime.ObservedLocalAbilityEventCount;
                var switchedAbility = switched.Runtime.LocalEquippedWeaponID ==
                    ClientBattleContentIdentity.SwordWeapon
                        ? ClientBattleContentIdentity.SwordAbility
                        : ClientBattleContentIdentity.FanAbility;
                var cooldownBoundary = checked(
                    switched.Runtime.ServerTick +
                    MaximumPlayerPrimaryCooldownTicks);
                await WaitUntilAsync(
                    () => TryCaptureBattle(diagnostics, out var current) &&
                          current.Runtime.Runtime.BattleGeneration == generation &&
                          current.Runtime.ServerTick >= cooldownBoundary,
                    TransitionObservationDeadline,
                    "battle-switched-primary-cooldown-not-observed");
                await SubmitQualificationInputAsync(
                    composition,
                    diagnostics,
                    generation,
                    new ClientBattleSemanticInput(
                        0,
                        0,
                        0,
                        0,
                        jumpPressed: false,
                        primaryPressed: true,
                        secondaryPressed: false,
                        interactPressed: false,
                        interactionSlot: 0));
                composition.BattleRuntimeCoordinator.
                    TrySetQualificationInput(generation, neutral);
                await WaitForLocalAbilityAsync(
                    diagnostics,
                    generation,
                    switchedEventCount,
                    switchedAbility,
                    "battle-second-primary-not-observed");
            }
            finally
            {
                composition.BattleRuntimeCoordinator.
                    ClearQualificationInput(generation);
            }
        }

        /// <summary>等待本地权威ability事件，并在失败时输出不含身份的封闭诊断。</summary>
        /// <param name="diagnostics">只读battle诊断owner。</param>
        /// <param name="generation">必须保持current的battle generation。</param>
        /// <param name="previousCount">提交输入前的本地ability事件计数。</param>
        /// <param name="expectedAbility">预期production ability identity。</param>
        /// <param name="failureReason">超时使用的稳定失败原因。</param>
        /// <returns>预期本地ability事件被权威提交时完成。</returns>
        private static async Task WaitForLocalAbilityAsync(
            ClientQualificationDiagnostics diagnostics,
            long generation,
            ulong previousCount,
            uint expectedAbility,
            string failureReason)
        {
            try
            {
                await WaitUntilAsync(
                    () => TryCaptureBattle(diagnostics, out var current) &&
                          current.Runtime.Runtime.BattleGeneration == generation &&
                          current.Runtime.ObservedLocalAbilityEventCount >
                              previousCount &&
                          current.Runtime.LastObservedLocalAbilityID ==
                              expectedAbility,
                    TransitionObservationDeadline,
                    failureReason);
            }
            catch (QualificationFailureException)
            {
                if (TryCaptureBattle(diagnostics, out var current))
                {
                    Debug.Log(
                        "[IHOMELAND_BATTLE_DIAGNOSTIC] " +
                        $"stage={failureReason} " +
                        $"generation={current.Runtime.Runtime.BattleGeneration} " +
                        $"availability={current.Runtime.Runtime.Availability} " +
                        $"server_tick={current.Runtime.ServerTick} " +
                        $"weapon_id={current.Runtime.LocalEquippedWeaponID} " +
                        $"ability_events={current.Runtime.ObservedAbilityEventCount} " +
                        $"local_ability_events={current.Runtime.ObservedLocalAbilityEventCount} " +
                        $"previous_local_ability_events={previousCount} " +
                        $"last_local_ability_id={current.Runtime.LastObservedLocalAbilityID} " +
                        $"expected_ability_id={expectedAbility} " +
                        $"local_health_milli={current.Runtime.LocalHealthMilli} " +
                        $"local_dead={current.Runtime.LocalDead} " +
                        $"sent_input_tick={current.Runtime.LastSentInputTick} " +
                        $"ack_input_tick={current.Runtime.LastAcknowledgedInputTick}");
                }

                throw;
            }
        }

        /// <summary>驱动两个真实Player以fan权威projectile协作终结Boss并核对phase/death收敛。</summary>
        /// <param name="composition">当前完整产品graph。</param>
        /// <param name="diagnostics">只读battle诊断owner。</param>
        /// <param name="root">本次双Player运行独占协调目录。</param>
        /// <param name="localRole">用于低敏文件信号的本地角色token。</param>
        /// <param name="remoteRole">用于低敏文件信号的对端角色token。</param>
        /// <returns>双方都观察到同一Boss权威death且各自fan event已提交时完成。</returns>
        private static async Task RunCooperativeBossDefeatAsync(
            AppCompositionResult composition,
            ClientQualificationDiagnostics diagnostics,
            string root,
            string localRole,
            string remoteRole)
        {
            if (localRole != "owner" && localRole != "visitor" ||
                remoteRole != "owner" && remoteRole != "visitor" ||
                localRole == remoteRole ||
                !TryCaptureBattle(diagnostics, out var start) ||
                start.Runtime.Runtime.Availability !=
                    ClientBattleAvailability.Active ||
                start.Runtime.EntityCount != 6 ||
                start.Runtime.LocalMaxHealthMilli != 100000 ||
                start.Runtime.LocalHealthMilli == 0 ||
                start.Runtime.LocalDead ||
                start.Runtime.BossMaxHealthMilli != 300000 ||
                start.Runtime.BossHealthMilli == 0 ||
                start.Runtime.BossDead)
            {
                throw new QualificationFailureException(
                    "battle-coop-baseline-invalid");
            }

            var generation = start.Runtime.Runtime.BattleGeneration;
            if (start.Runtime.LocalEquippedWeaponID !=
                ClientBattleContentIdentity.FanWeapon)
            {
                await SubmitQualificationInputAsync(
                    composition,
                    diagnostics,
                    generation,
                    new ClientBattleSemanticInput(
                        -1000,
                        -1000,
                        -135000,
                        0,
                        jumpPressed: false,
                        primaryPressed: false,
                        secondaryPressed: false,
                        interactPressed: false,
                        interactionSlot: 0,
                        switchWeaponPressed: true));
                composition.BattleRuntimeCoordinator.
                    TrySetQualificationInput(
                        generation,
                        CreateCooperativeCombatInput(
                            primaryPressed: false,
                            movementTicks: null));
                await WaitUntilAsync(
                    () => TryCaptureBattle(diagnostics, out var current) &&
                          current.Runtime.Runtime.BattleGeneration == generation &&
                          current.Runtime.LocalEquippedWeaponID ==
                              ClientBattleContentIdentity.FanWeapon,
                    TransitionObservationDeadline);
            }

            start = diagnostics.CaptureBattle();
            if (start.Runtime.Runtime.BattleGeneration != generation ||
                start.Runtime.LocalEquippedWeaponID !=
                    ClientBattleContentIdentity.FanWeapon ||
                start.Runtime.LocalMaxHealthMilli != 100000 ||
                start.Runtime.LocalHealthMilli == 0 ||
                start.Runtime.LocalDead ||
                start.Runtime.BossMaxHealthMilli != 300000 ||
                start.Runtime.BossHealthMilli == 0 ||
                start.Runtime.BossDead)
            {
                throw new QualificationFailureException(
                    "battle-coop-ready-state-invalid");
            }

            WriteSignal(
                root,
                $"{localRole}-boss-baseline",
                start.Runtime.BossHealthMilli.ToString());
            var remoteBaseline = await ReadRequiredSignalAsync(
                root,
                $"{remoteRole}-boss-baseline");
            if (!uint.TryParse(remoteBaseline, out var remoteBossHealth) ||
                remoteBossHealth != start.Runtime.BossHealthMilli)
            {
                throw new QualificationFailureException(
                    "battle-coop-baseline-drifted");
            }

            var initialLocalEventCount =
                start.Runtime.ObservedLocalAbilityEventCount;
            var lowestBossHealth = start.Runtime.BossHealthMilli;
            uint highestBossPhase = start.Runtime.BossPhase;
            var deadline = DateTime.UtcNow.Add(
                TransitionObservationDeadline);
            try
            {
                while (true)
                {
                    if (!TryCaptureBattle(diagnostics, out var current) ||
                        current.Runtime.Runtime.BattleGeneration != generation ||
                        current.Runtime.Runtime.Availability !=
                            ClientBattleAvailability.Active ||
                        current.Runtime.LocalMaxHealthMilli != 100000 ||
                        current.Runtime.LocalHealthMilli == 0 ||
                        current.Runtime.LocalDead ||
                        current.Runtime.BossMaxHealthMilli != 300000 ||
                        current.Runtime.BossHealthMilli > lowestBossHealth)
                    {
                        if (TryCaptureBattle(diagnostics, out var drifted))
                        {
                            Debug.Log(
                                "[IHOMELAND_BATTLE_DIAGNOSTIC] " +
                                "stage=battle-coop-authority-drifted " +
                                $"server_tick={drifted.Runtime.ServerTick} " +
                                $"boss_health_milli={drifted.Runtime.BossHealthMilli} " +
                                $"lowest_boss_health_milli={lowestBossHealth} " +
                                $"local_health_milli={drifted.Runtime.LocalHealthMilli} " +
                                $"local_dead={drifted.Runtime.LocalDead} " +
                                $"local_ability_events={drifted.Runtime.ObservedLocalAbilityEventCount} " +
                                $"last_local_ability_id={drifted.Runtime.LastObservedLocalAbilityID}");
                        }

                        throw new QualificationFailureException(
                            "battle-coop-authority-drifted");
                    }

                    lowestBossHealth = current.Runtime.BossHealthMilli;
                    highestBossPhase = Math.Max(
                        highestBossPhase,
                        current.Runtime.BossPhase);
                    if (current.Runtime.BossDead)
                    {
                        if (current.Runtime.BossHealthMilli != 0 ||
                            lowestBossHealth >= start.Runtime.BossHealthMilli ||
                            highestBossPhase < 2 ||
                            current.Runtime.ObservedLocalAbilityEventCount <=
                                initialLocalEventCount ||
                            current.Runtime.LastObservedLocalAbilityID !=
                                ClientBattleContentIdentity.FanAbility)
                        {
                            throw new QualificationFailureException(
                                "battle-coop-defeat-evidence-invalid");
                        }

                        WriteSignal(
                            root,
                            $"{localRole}-boss-defeated",
                            current.Runtime.ServerTick.ToString());
                        var remoteDefeatTick = await ReadRequiredSignalAsync(
                            root,
                            $"{remoteRole}-boss-defeated");
                        if (!ulong.TryParse(
                                remoteDefeatTick,
                                out var parsedRemoteTick) ||
                            parsedRemoteTick == 0)
                        {
                            throw new QualificationFailureException(
                                "battle-coop-remote-defeat-invalid");
                        }

                        await WaitForCombatEntityCleanupAsync(
                            composition,
                            diagnostics,
                            root,
                            generation,
                            localRole,
                            remoteRole,
                            deadline);
                        return;
                    }

                    if (current.Runtime.BossHealthMilli == 0 ||
                        DateTime.UtcNow >= deadline)
                    {
                        Debug.Log(
                            "[IHOMELAND_BATTLE_DIAGNOSTIC] " +
                            "stage=battle-coop-defeat-timeout " +
                            $"server_tick={current.Runtime.ServerTick} " +
                            $"initial_boss_health_milli={start.Runtime.BossHealthMilli} " +
                            $"lowest_boss_health_milli={lowestBossHealth} " +
                            $"local_health_milli={current.Runtime.LocalHealthMilli} " +
                            $"local_ability_events={current.Runtime.ObservedLocalAbilityEventCount} " +
                            $"last_local_ability_id={current.Runtime.LastObservedLocalAbilityID}");
                        throw new QualificationFailureException(
                            "battle-coop-defeat-timeout");
                    }

                    await SubmitQualificationInputAsync(
                        composition,
                        diagnostics,
                        generation,
                        CreateCooperativeCombatInput(
                            primaryPressed: true,
                            movementTicks: current.Runtime.ServerTick -
                                start.Runtime.ServerTick));
                    composition.BattleRuntimeCoordinator.
                        TrySetQualificationInput(
                            generation,
                            CreateCooperativeCombatInput(
                                primaryPressed: false,
                                movementTicks: current.Runtime.ServerTick -
                                    start.Runtime.ServerTick));
                    await Task.Delay(TimeSpan.FromMilliseconds(750));
                }
            }
            finally
            {
                composition.BattleRuntimeCoordinator.
                    ClearQualificationInput(generation);
            }
        }

        /// <summary>清除Boss战后仍存活的AI及全部corpse/projectile，避免长continuity被残余AI污染。</summary>
        /// <param name="composition">当前完整产品graph。</param>
        /// <param name="diagnostics">只读battle诊断owner。</param>
        /// <param name="root">本次双Player运行独占协调目录。</param>
        /// <param name="generation">必须保持current的battle generation。</param>
        /// <param name="localRole">用于低敏文件信号的本地角色token。</param>
        /// <param name="remoteRole">用于低敏文件信号的对端角色token。</param>
        /// <param name="deadline">协作战斗共享绝对deadline。</param>
        /// <returns>authority entity set只剩两个player且双方均观察到时完成。</returns>
        private static async Task WaitForCombatEntityCleanupAsync(
            AppCompositionResult composition,
            ClientQualificationDiagnostics diagnostics,
            string root,
            long generation,
            string localRole,
            string remoteRole,
            DateTime deadline)
        {
            while (DateTime.UtcNow < deadline)
            {
                composition.BattleRuntimeCoordinator.
                    TrySetQualificationInput(
                        generation,
                        CreateCooperativeCombatInput(
                            primaryPressed: false,
                            movementTicks: null));
                await Task.Delay(TimeSpan.FromSeconds(5));
                if (!TryCaptureBattle(diagnostics, out var current) ||
                    current.Runtime.Runtime.BattleGeneration != generation ||
                    current.Runtime.Runtime.Availability !=
                        ClientBattleAvailability.Active ||
                    current.Runtime.LocalMaxHealthMilli != 100000 ||
                    current.Runtime.LocalHealthMilli == 0 ||
                    current.Runtime.LocalDead ||
                    current.Runtime.EntityCount < 2)
                {
                    throw new QualificationFailureException(
                        "battle-coop-cleanup-drifted");
                }

                if (current.Runtime.EntityCount == 2)
                {
                    WriteSignal(
                        root,
                        $"{localRole}-combat-clean",
                        current.Runtime.ServerTick.ToString());
                    var remoteCleanTick = await ReadRequiredSignalAsync(
                        root,
                        $"{remoteRole}-combat-clean");
                    if (!ulong.TryParse(
                            remoteCleanTick,
                            out var parsedRemoteTick) ||
                        parsedRemoteTick == 0)
                    {
                        throw new QualificationFailureException(
                            "battle-coop-remote-cleanup-invalid");
                    }

                    return;
                }

                for (var attempt = 0;
                     attempt < 8 && DateTime.UtcNow < deadline;
                     attempt++)
                {
                    await SubmitQualificationInputAsync(
                        composition,
                        diagnostics,
                        generation,
                        CreateCooperativeCombatInput(
                            primaryPressed: true,
                            movementTicks: null));
                    composition.BattleRuntimeCoordinator.
                        TrySetQualificationInput(
                            generation,
                            CreateCooperativeCombatInput(
                                primaryPressed: false,
                                movementTicks: null));
                    await Task.Delay(TimeSpan.FromMilliseconds(750));
                }
            }

            throw new QualificationFailureException(
                "battle-coop-cleanup-timeout");
        }

        /// <summary>创建只含移动、aim与primary intent的协作战斗输入。</summary>
        /// <param name="primaryPressed">本sample是否产生primary离散edge。</param>
        /// <param name="movementTicks">非空时按相对Tick沿arena外圈方形走位；空值保持静止。</param>
        /// <returns>不含target、damage或其他authority事实的semantic input。</returns>
        private static ClientBattleSemanticInput CreateCooperativeCombatInput(
            bool primaryPressed,
            ulong? movementTicks)
        {
            var moveX = 0;
            var moveZ = 0;
            if (movementTicks.HasValue)
            {
                switch ((movementTicks.Value / 160UL) % 4UL)
                {
                    case 0:
                        moveX = 1000;
                        break;
                    case 1:
                        moveZ = 1000;
                        break;
                    case 2:
                        moveX = -1000;
                        break;
                    default:
                        moveZ = -1000;
                        break;
                }
            }

            return new ClientBattleSemanticInput(
                moveX,
                moveZ,
                45000,
                0,
                jumpPressed: false,
                primaryPressed: primaryPressed,
                secondaryPressed: false,
                interactPressed: false,
                interactionSlot: 0);
        }

        /// <summary>在current generation输入门开启后提交一个资格semantic sample。</summary>
        /// <param name="composition">当前完整产品graph。</param>
        /// <param name="diagnostics">只读battle诊断owner。</param>
        /// <param name="generation">必须保持current的battle generation。</param>
        /// <param name="input">不含任何authority结果字段的semantic input。</param>
        /// <returns>input owner接受sample时完成。</returns>
        private static async Task SubmitQualificationInputAsync(
            AppCompositionResult composition,
            ClientQualificationDiagnostics diagnostics,
            long generation,
            ClientBattleSemanticInput input)
        {
            var deadline = DateTime.UtcNow.Add(
                TransitionObservationDeadline);
            while (!composition.BattleRuntimeCoordinator.
                       TrySetQualificationInput(generation, input))
            {
                if (!TryCaptureBattle(diagnostics, out var current) ||
                    current.Runtime.Runtime.BattleGeneration != generation ||
                    current.Runtime.Runtime.Availability !=
                        ClientBattleAvailability.Active ||
                    DateTime.UtcNow >= deadline)
                {
                    throw new QualificationFailureException(
                        "battle-combat-input-rejected");
                }

                await Task.Delay(ObservationInterval);
            }

            await Task.Delay(TimeSpan.FromMilliseconds(75));
        }

        /// <summary>取得指定actor当前presentation transform并验证active actor/local唯一边界。</summary>
        /// <param name="composition">当前完整产品graph。</param>
        /// <param name="entityID">必须存在的actor identity。</param>
        /// <returns>local prediction或remote interpolation输出的transform。</returns>
        private static async Task<ClientBattleTransform>
            CaptureActorTransformAsync(
                AppCompositionResult composition,
                ulong entityID)
        {
            ClientBattleTransform transform = default;
            await WaitUntilAsync(
                () => TryCaptureActorTransform(
                    composition,
                    entityID,
                    out transform),
                TransitionObservationDeadline);
            return transform;
        }

        /// <summary>等待指定remote actor的插值presentation发生真实位置变化。</summary>
        /// <param name="composition">当前完整产品graph。</param>
        /// <param name="diagnostics">只读battle runtime诊断owner。</param>
        /// <param name="entityID">对端actor identity。</param>
        /// <param name="before">对端movement前的presentation transform。</param>
        /// <returns>位置变化被Scene read path观察到时完成。</returns>
        private static async Task WaitForActorTransformChangeAsync(
            AppCompositionResult composition,
            ClientQualificationDiagnostics diagnostics,
            ulong entityID,
            ClientBattleTransform before)
        {
            var deadline = DateTime.UtcNow.Add(
                TransitionObservationDeadline);
            var current = before;
            while (!TryCaptureActorTransform(
                       composition,
                       entityID,
                       out current) ||
                   current.PositionDistanceSquared(before) == 0)
            {
                if (DateTime.UtcNow >= deadline)
                {
                    if (TryCaptureBattle(diagnostics, out var battle))
                    {
                        Debug.Log(
                            "[IHOMELAND_BATTLE_DIAGNOSTIC] " +
                            "stage=remote-transform-timeout " +
                            $"generation={battle.Runtime.Runtime.BattleGeneration} " +
                            $"server_tick={battle.Runtime.ServerTick} " +
                            $"entity_id={entityID} " +
                            $"before_x_mm={before.PositionXMillimeters} " +
                            $"before_y_mm={before.PositionYMillimeters} " +
                            $"before_z_mm={before.PositionZMillimeters} " +
                            $"current_x_mm={current.PositionXMillimeters} " +
                            $"current_y_mm={current.PositionYMillimeters} " +
                            $"current_z_mm={current.PositionZMillimeters}");
                    }

                    throw new QualificationFailureException(
                        "battle-remote-transform-timeout");
                }

                await Task.Delay(ObservationInterval);
            }
        }

        /// <summary>读取一次current presentation并拒绝空/超量actor或多个local actor。</summary>
        /// <param name="composition">当前完整产品graph。</param>
        /// <param name="entityID">待查找actor identity。</param>
        /// <param name="transform">成功时返回指定actor transform。</param>
        /// <returns>current presentation已包含指定actor时为true。</returns>
        private static bool TryCaptureActorTransform(
            AppCompositionResult composition,
            ulong entityID,
            out ClientBattleTransform transform)
        {
            transform = default;
            if (!composition.BattleRuntimeCoordinator.
                    TryConsumePresentation(out var presentation))
            {
                return false;
            }

            var localActors = 0;
            var found = false;
            for (var index = 0; index < presentation.Actors.Count; index++)
            {
                var actor = presentation.Actors[index];
                if (actor.Local)
                {
                    localActors++;
                }

                if (actor.EntityID == entityID)
                {
                    transform = actor.Transform;
                    found = true;
                }
            }

            if (presentation.Actors.Count == 0 ||
                presentation.Actors.Count >
                    ClientBattlePolicy.Current.MaximumEntities ||
                localActors != 1)
            {
                throw new QualificationFailureException(
                    "battle-presentation-actor-boundary-drifted");
            }

            return found;
        }

        /// <summary>等待Scene registry真正实例化预期Actor，并保持唯一local view。</summary>
        /// <param name="diagnostics">只读battle runtime诊断owner。</param>
        /// <param name="expectedActors">Current公开actor集合大小。</param>
        /// <param name="stage">不含identity的闭合资格阶段。</param>
        /// <returns>可见Scene实例与presentation集合一致时完成。</returns>
        private static async Task WaitForSceneActorsAsync(
            ClientQualificationDiagnostics diagnostics,
            int expectedActors,
            string stage)
        {
            var deadline = DateTime.UtcNow.Add(
                TransitionObservationDeadline);
            while (true)
            {
                var registry =
                    UnityEngine.Object.
                        FindAnyObjectByType<ClientActorViewRegistry>();
                if (registry != null &&
                    registry.QualificationActorCount == expectedActors &&
                    registry.QualificationLocalActorCount == 1)
                {
                    return;
                }

                if (DateTime.UtcNow >= deadline)
                {
                    var sceneActors = registry == null
                        ? -1
                        : registry.QualificationActorCount;
                    var sceneLocalActors = registry == null
                        ? -1
                        : registry.QualificationLocalActorCount;
                    var runtimeEntities = -1;
                    var generation = 0L;
                    var availability =
                        ClientBattleAvailability.Inactive;
                    if (TryCaptureBattle(diagnostics, out var battle))
                    {
                        runtimeEntities = battle.Runtime.EntityCount;
                        generation =
                            battle.Runtime.Runtime.BattleGeneration;
                        availability =
                            battle.Runtime.Runtime.Availability;
                    }

                    Debug.Log(
                        "[IHOMELAND_BATTLE_DIAGNOSTIC] " +
                        "stage=scene-actors-timeout " +
                        $"checkpoint={stage} " +
                        $"expected_actors={expectedActors} " +
                        $"scene_actors={sceneActors} " +
                        $"scene_local_actors={sceneLocalActors} " +
                        $"runtime_entities={runtimeEntities} " +
                        $"generation={generation} " +
                        $"availability={availability}");
                    throw new QualificationFailureException(
                        $"battle-scene-actors-{stage}-timeout");
                }

                await Task.Delay(ObservationInterval);
            }
        }

        /// <summary>解析另一Player发布的0-7 actor slot。</summary>
        /// <param name="root">本次battle运行独占协调目录。</param>
        /// <param name="name">slot信号名。</param>
        /// <returns>合法actor slot。</returns>
        private static async Task<int> ReadActorSlotSignalAsync(
            string root,
            string name)
        {
            var value = await ReadRequiredSignalAsync(root, name);
            if (!int.TryParse(value, out var slot) || slot < 0 || slot > 7)
            {
                throw new QualificationFailureException(
                    "battle-actor-slot-signal-invalid");
            }

            return slot;
        }

        /// <summary>退出Session并验证Scene、battle socket、pump与native lease恰好释放。</summary>
        /// <param name="composition">当前完整产品graph。</param>
        /// <param name="diagnostics">只读资格诊断owner。</param>
        /// <returns>全部App/Scene battle派生资源退役时完成。</returns>
        private static async Task LogoutAndAssertBattleReleasedAsync(
            AppCompositionResult composition,
            ClientQualificationDiagnostics diagnostics)
        {
            await RequireActionAsync(
                composition.PersonalWorldExperience.LogoutAsync(
                    CancellationToken.None),
                "battle-logout-rejected");
            await WaitUntilAsync(
                () =>
                {
                    if (!TryCaptureBattle(diagnostics, out var battle))
                    {
                        return false;
                    }

                    var app = diagnostics.Capture();
                    return composition.PersonalWorldExperience.ViewState.Phase ==
                               ClientPersonalWorldPhase.Login &&
                           !composition.SessionCoordinator.TryGetCurrent(out _) &&
                           app.SceneOwners == 0 &&
                           battle.Runtime.Runtime.BattleGeneration == 0 &&
                           battle.Runtime.Runtime.Availability ==
                               ClientBattleAvailability.Inactive &&
                           battle.Runtime.EntityCount == 0 &&
                           battle.SocketOwners == 0 &&
                           battle.PumpOwners == 0 &&
                           battle.NativeLeases == 0;
                },
                TransitionObservationDeadline);
        }

        /// <summary>在generation替换窗口内安全尝试取得battle诊断。</summary>
        /// <param name="diagnostics">只读battle诊断owner。</param>
        /// <param name="snapshot">成功时返回current稳定快照。</param>
        /// <returns>捕获未跨generation时为true。</returns>
        private static bool TryCaptureBattle(
            ClientQualificationDiagnostics diagnostics,
            out ClientBattleQualificationDiagnosticSnapshot snapshot)
        {
            try
            {
                snapshot = diagnostics.CaptureBattle();
                return true;
            }
            catch (InvalidOperationException)
            {
                snapshot = null;
                return false;
            }
        }

        /// <summary>检查current battle graph已完整Active且资源owner符合固定边界。</summary>
        /// <param name="snapshot">同一捕获窗口的battle诊断。</param>
        /// <param name="targetKind">预期target。</param>
        /// <param name="role">预期角色。</param>
        /// <returns>全部runtime、identity与资源条件成立时为true。</returns>
        private static bool IsActiveBattle(
            ClientBattleQualificationDiagnosticSnapshot snapshot,
            ClientBattleTargetKind targetKind,
            ClientBattleRole role)
        {
            var runtime = snapshot.Runtime;
            return runtime.Runtime.Availability ==
                       ClientBattleAvailability.Active &&
                   runtime.Runtime.Failure == ClientBattleFailure.None &&
                   runtime.Runtime.BattleGeneration > 0 &&
                   runtime.Runtime.TargetKind == targetKind &&
                   runtime.Runtime.Role == role &&
                   runtime.Runtime.ActorSlot >= 0 &&
                   runtime.Runtime.ActorSlot <= 7 &&
                   runtime.Runtime.BaselineReady &&
                   runtime.Runtime.InputEnabled &&
                   runtime.ServerTick > 0 &&
                   runtime.EntityCount > 0 &&
                   runtime.EntityCount <=
                       ClientBattlePolicy.Current.MaximumEntities &&
                   runtime.LocalEntityID ==
                       (ulong)(runtime.Runtime.ActorSlot + 1) &&
                   runtime.LastAcknowledgedInputTick <=
                       Math.Max(
                           runtime.LastSentInputTick,
                           runtime.ContinuityAcknowledgementAnchor) &&
                   !runtime.ResyncPending &&
                   snapshot.SocketOwners == 1 &&
                   snapshot.PumpOwners == 3 &&
                   snapshot.NativeLeases == 1;
        }

        /// <summary>登录新profile或接纳同一安全profile已完成的Session恢复。</summary>
        /// <param name="composition">当前完整产品graph。</param>
        /// <param name="requireCredentials">true表示本次必须显式登录。</param>
        /// <returns>稳定OwnWorld提交时完成。</returns>
        private static async Task LoginOrRestoreOwnWorldAsync(
            AppCompositionResult composition,
            bool requireCredentials)
        {
            var diagnostics = composition.CreateQualificationDiagnostics();
            if (IsStableOwnWorld(composition, diagnostics.Capture()))
            {
                if (requireCredentials)
                {
                    Environment.SetEnvironmentVariable(UsernameVariable, null);
                    Environment.SetEnvironmentVariable(PasswordVariable, null);
                }

                return;
            }

            if (!requireCredentials)
            {
                await WaitUntilAsync(
                    () => IsStableOwnWorld(composition, diagnostics.Capture()),
                    TransitionObservationDeadline);
                return;
            }

            var username = Environment.GetEnvironmentVariable(UsernameVariable);
            var password = Environment.GetEnvironmentVariable(PasswordVariable);
            Environment.SetEnvironmentVariable(UsernameVariable, null);
            Environment.SetEnvironmentVariable(PasswordVariable, null);
            if (string.IsNullOrWhiteSpace(username) || string.IsNullOrEmpty(password))
            {
                throw new QualificationFailureException("missing-credentials");
            }

            var login = await composition.PersonalWorldExperience.LoginAsync(
                username,
                password,
                CancellationToken.None);
            username = null;
            password = null;
            if (!login.Succeeded)
            {
                throw new QualificationFailureException("login-rejected");
            }

            await WaitUntilAsync(
                () => IsStableOwnWorld(composition, diagnostics.Capture()),
                TransitionObservationDeadline);
        }

        /// <summary>创建邀请并把服务端返回的稳定identity发布给另一Player。</summary>
        /// <param name="composition">Owner完整产品graph。</param>
        /// <param name="root">本次operator运行独占协调目录。</param>
        /// <param name="signalName">本轮邀请信号名。</param>
        /// <param name="visitorPlayerID">定向Visitor玩家标识。</param>
        /// <returns>邀请投影提交并发布时完成。</returns>
        private static async Task PublishInviteAsync(
            AppCompositionResult composition,
            string root,
            string signalName,
            string visitorPlayerID)
        {
            await RequireActionAsync(
                composition.PersonalWorldExperience.CreateInviteAsync(
                    visitorPlayerID,
                    CancellationToken.None),
                "create-invite-rejected");
            await WaitUntilAsync(
                () => composition.PersonalWorldExperience.ViewState.WorldVisit.Invites.Count > 0,
                TransitionObservationDeadline);
            var invites = composition.PersonalWorldExperience.ViewState.WorldVisit.Invites;
            var invite = invites[invites.Count - 1];
            WriteSignal(
                root,
                signalName,
                invite.VisitSessionID + "|" + invite.InviteID);
        }

        /// <summary>等待权威inbox后由Visitor使用同一产品入口接受指定邀请。</summary>
        /// <param name="composition">Visitor完整产品graph。</param>
        /// <param name="root">本次operator运行独占协调目录。</param>
        /// <param name="inviteSignal">Owner发布的邀请信号名。</param>
        /// <param name="joinedSignal">Visitor成功进入后发布的信号名。</param>
        /// <returns>同一VisitSession进入Visiting时完成。</returns>
        private static async Task AcceptPublishedInviteAsync(
            AppCompositionResult composition,
            string root,
            string inviteSignal,
            string joinedSignal)
        {
            var serialized = await ReadRequiredSignalAsync(root, inviteSignal);
            var separator = serialized.IndexOf('|');
            if (separator <= 0 || separator >= serialized.Length - 1)
            {
                throw new QualificationFailureException("invalid-invite-signal");
            }

            var visitID = serialized.Substring(0, separator);
            var inviteID = serialized.Substring(separator + 1);
            await WaitUntilAsync(
                () => ContainsInvite(
                    composition.PersonalWorldExperience.ViewState.WorldVisit.Invites,
                    visitID,
                    inviteID),
                TransitionObservationDeadline);
            await Task.Delay(GameplayAdmissionBudgetInterval);
            await RequireActionAsync(
                composition.PersonalWorldExperience.AcceptInviteAsync(
                    visitID,
                    inviteID,
                    CancellationToken.None),
                "accept-invite-rejected");
            await WaitUntilAsync(
                () => composition.PersonalWorldExperience.ViewState.Phase == ClientPersonalWorldPhase.Visiting &&
                      string.Equals(
                          composition.PersonalWorldExperience.ViewState.WorldVisit.VisitSessionID,
                          visitID,
                          StringComparison.Ordinal),
                TransitionObservationDeadline);
            WriteSignal(root, joinedSignal, "pass");
        }

        /// <summary>等待Visitor确认进入并核对Owner权威member投影。</summary>
        /// <param name="composition">Owner完整产品graph。</param>
        /// <param name="root">本次operator运行独占协调目录。</param>
        /// <param name="signal">Visitor joined信号。</param>
        /// <param name="visitorPlayerID">预期唯一Visitor。</param>
        /// <returns>Owner投影包含且只包含目标成员时完成。</returns>
        private static async Task WaitForOwnerMemberAsync(
            AppCompositionResult composition,
            string root,
            string signal,
            string visitorPlayerID)
        {
            await ReadRequiredSignalAsync(root, signal);
            await WaitUntilAsync(
                () => composition.PersonalWorldExperience.ViewState.WorldVisit.VisitorPlayerIDs.Count == 1 &&
                      Contains(
                          composition.PersonalWorldExperience.ViewState.WorldVisit.VisitorPlayerIDs,
                          visitorPlayerID),
                TransitionObservationDeadline);
        }

        /// <summary>等待Visitor离开信号并核对Owner成员权威退役。</summary>
        /// <param name="composition">Owner完整产品graph。</param>
        /// <param name="root">本次operator运行独占协调目录。</param>
        /// <param name="signal">Visitor left信号。</param>
        /// <param name="visitorPlayerID">必须退役的Visitor。</param>
        /// <returns>Owner投影不再包含目标成员时完成。</returns>
        private static async Task WaitForOwnerMemberRemovalAsync(
            AppCompositionResult composition,
            string root,
            string signal,
            string visitorPlayerID)
        {
            await ReadRequiredSignalAsync(root, signal);
            await WaitUntilAsync(
                () => !Contains(
                    composition.PersonalWorldExperience.ViewState.WorldVisit.VisitorPlayerIDs,
                    visitorPlayerID),
                TransitionObservationDeadline);
        }

        /// <summary>等待客户端完成自动terminal，再证明并发手工重试不会绕过single-flight。</summary>
        /// <param name="composition">当前完整产品graph。</param>
        /// <returns>两个调用方都在总预算内失败并稳定回到ConnectionLost时完成。</returns>
        private static async Task AssertOfflineRetryIsBoundedAsync(AppCompositionResult composition)
        {
            await WaitUntilAsync(
                () => composition.PersonalWorldExperience.ViewState.Phase ==
                      ClientPersonalWorldPhase.ConnectionLost,
                TransitionObservationDeadline);
            var first = composition.PersonalWorldExperience.RetryConnectionAsync(CancellationToken.None);
            var second = composition.PersonalWorldExperience.RetryConnectionAsync(CancellationToken.None);
            var results = await Task.WhenAll(first, second);
            if (results[0].Succeeded || results[1].Succeeded)
            {
                throw new QualificationFailureException("offline-retry-unexpected-success");
            }

            await WaitUntilAsync(
                () => composition.PersonalWorldExperience.ViewState.Phase ==
                      ClientPersonalWorldPhase.ConnectionLost,
                TransitionObservationDeadline);
        }

        /// <summary>服务端新generation就绪后由唯一手工入口恢复OwnWorld并清除旧Visit投影。</summary>
        /// <param name="composition">当前完整产品graph。</param>
        /// <returns>Session、通道、target、Scene与HUD全部提交时完成。</returns>
        private static async Task RecoverToOwnWorldAsync(AppCompositionResult composition)
        {
            await RequireActionAsync(
                composition.PersonalWorldExperience.RetryConnectionAsync(CancellationToken.None),
                "online-retry-rejected");
            var diagnostics = composition.CreateQualificationDiagnostics();
            await WaitUntilAsync(
                () => IsStableOwnWorld(composition, diagnostics.Capture()) &&
                      composition.VisitSessionService.Snapshot.Current == null,
                TransitionObservationDeadline);
        }

        /// <summary>等待稳定动作成功并把封闭失败映射为资格reason。</summary>
        /// <param name="action">真实产品入口返回的动作task。</param>
        /// <param name="reason">失败时允许输出的封闭reason。</param>
        /// <returns>动作成功时完成。</returns>
        private static async Task RequireActionAsync(
            Task<ClientPersonalWorldActionResult> action,
            string reason)
        {
            var result = await action;
            if (!result.Succeeded)
            {
                throw new QualificationFailureException(reason);
            }
        }

        /// <summary>检查Owner仍持有同一VisitSession和唯一目标成员。</summary>
        /// <param name="composition">Owner完整产品graph。</param>
        /// <param name="visitID">恢复前VisitSession标识。</param>
        /// <param name="visitorPlayerID">预期唯一Visitor。</param>
        /// <returns>身份、角色与成员均一致时返回true。</returns>
        private static bool HasSameOwnerVisit(
            AppCompositionResult composition,
            string visitID,
            string visitorPlayerID)
        {
            var view = composition.PersonalWorldExperience.ViewState.WorldVisit;
            return view.IsOwner &&
                   string.Equals(view.VisitSessionID, visitID, StringComparison.Ordinal) &&
                   view.VisitorPlayerIDs.Count == 1 &&
                   Contains(view.VisitorPlayerIDs, visitorPlayerID);
        }

        /// <summary>按ordinal语义检查只读字符串集合。</summary>
        /// <param name="values">不可变页面集合。</param>
        /// <param name="expected">待查找值。</param>
        /// <returns>集合包含目标时返回true。</returns>
        private static bool Contains(System.Collections.Generic.IReadOnlyList<string> values, string expected)
        {
            for (var index = 0; index < values.Count; index++)
            {
                if (string.Equals(values[index], expected, StringComparison.Ordinal))
                {
                    return true;
                }
            }

            return false;
        }

        /// <summary>检查inbox是否已经提交指定邀请identity。</summary>
        /// <param name="invites">当前邀请投影。</param>
        /// <param name="visitID">VisitSession标识。</param>
        /// <param name="inviteID">Invite标识。</param>
        /// <returns>两个identity都匹配时返回true。</returns>
        private static bool ContainsInvite(
            System.Collections.Generic.IReadOnlyList<ClientVisitInviteViewState> invites,
            string visitID,
            string inviteID)
        {
            for (var index = 0; index < invites.Count; index++)
            {
                if (string.Equals(invites[index].VisitSessionID, visitID, StringComparison.Ordinal) &&
                    string.Equals(invites[index].InviteID, inviteID, StringComparison.Ordinal))
                {
                    return true;
                }
            }

            return false;
        }

        /// <summary>原子发布单写者operator信号，避免另一进程读取部分内容。</summary>
        /// <param name="root">本次operator运行独占协调目录。</param>
        /// <param name="name">闭合信号名。</param>
        /// <param name="value">不含credential的稳定值。</param>
        private static void WriteSignal(string root, string name, string value)
        {
            var path = SignalPath(root, name);
            var temporary = path + "." + Guid.NewGuid().ToString("N") + ".tmp";
            File.WriteAllText(temporary, value ?? string.Empty);
            if (File.Exists(path))
            {
                File.Delete(temporary);
                return;
            }

            File.Move(temporary, path);
        }

        /// <summary>等待指定operator信号并读取完整低敏内容。</summary>
        /// <param name="root">本次operator运行独占协调目录。</param>
        /// <param name="name">闭合信号名。</param>
        /// <returns>信号原子发布的完整内容。</returns>
        private static async Task<string> ReadRequiredSignalAsync(string root, string name)
        {
            var path = SignalPath(root, name);
            await WaitUntilAsync(() => File.Exists(path), TransitionObservationDeadline);
            return File.ReadAllText(path).Trim();
        }

        /// <summary>检查恢复阶段信号是否已由外部精确进程owner提交。</summary>
        /// <param name="root">本次operator运行独占协调目录。</param>
        /// <param name="name">闭合信号名。</param>
        /// <returns>信号文件存在时返回true。</returns>
        private static bool SignalExists(string root, string name)
        {
            return File.Exists(SignalPath(root, name));
        }

        /// <summary>构造不允许目录逃逸的闭合operator信号路径。</summary>
        /// <param name="root">本次operator运行独占协调目录。</param>
        /// <param name="name">只允许字母、数字与连字符的信号名。</param>
        /// <returns>规范化完整路径。</returns>
        private static string SignalPath(string root, string name)
        {
            for (var index = 0; index < name.Length; index++)
            {
                var character = name[index];
                if (!char.IsLetterOrDigit(character) && character != '-')
                {
                    throw new QualificationFailureException("invalid-signal-name");
                }
            }

            return Path.Combine(root, name + ".signal");
        }

        /// <summary>保持首次Player存活，直到外部owner按精确PID执行进程替换。</summary>
        /// <returns>正常执行不会完成。</returns>
        private static async Task WaitForTerminationAsync()
        {
            while (true)
            {
                await Task.Delay(TimeSpan.FromSeconds(1));
            }
        }

        /// <summary>以真实产品graph验证开放VisitSession的Owner跨server replacement返回OwnWorld。</summary>
        /// <param name="composition">已经进入Running的完整产品Composition。</param>
        /// <returns>新server generation、Scene与route全部提交时完成。</returns>
        private static async Task RunServerRestartRecoveryAsync(AppCompositionResult composition)
        {
            var signalPath = ReadSingleArgument(
                Environment.GetCommandLineArgs(),
                RecoverySignalArgument);
            if (string.IsNullOrWhiteSpace(signalPath))
            {
                throw new QualificationFailureException("missing-recovery-signal");
            }

            var username = Environment.GetEnvironmentVariable(UsernameVariable);
            var password = Environment.GetEnvironmentVariable(PasswordVariable);
            Environment.SetEnvironmentVariable(UsernameVariable, null);
            Environment.SetEnvironmentVariable(PasswordVariable, null);
            if (string.IsNullOrWhiteSpace(username) || string.IsNullOrEmpty(password))
            {
                throw new QualificationFailureException("missing-credentials");
            }

            var login = await composition.PersonalWorldExperience.LoginAsync(
                username,
                password,
                CancellationToken.None);
            username = null;
            password = null;
            if (!login.Succeeded)
            {
                throw new QualificationFailureException("login-rejected");
            }

            var diagnostics = composition.CreateQualificationDiagnostics();
            await WaitUntilAsync(
                () => IsStableOwnWorld(composition, diagnostics.Capture()),
                TransitionObservationDeadline);
            var baseline = diagnostics.Capture();
            var opened = await composition.PersonalWorldExperience.OpenVisitAsync(
                CancellationToken.None);
            if (!opened.Succeeded)
            {
                throw new QualificationFailureException("visit-open-rejected");
            }

            await WaitUntilAsync(
                () => composition.VisitSessionService.Snapshot.Current != null,
                TransitionObservationDeadline);
            Debug.Log("[IHOMELAND_QUALIFICATION] state=ready-for-server-stop scenario=server-restart-recovery");

            await WaitUntilAsync(
                () => composition.PersonalWorldExperience.ViewState.Phase ==
                      ClientPersonalWorldPhase.ConnectionLost,
                TransitionObservationDeadline);
            Debug.Log("[IHOMELAND_QUALIFICATION] state=connection-lost scenario=server-restart-recovery");

            await WaitUntilAsync(
                () => System.IO.File.Exists(signalPath),
                TransitionObservationDeadline);
            var recovered = await composition.PersonalWorldExperience.RetryConnectionAsync(
                CancellationToken.None);
            if (!recovered.Succeeded)
            {
                throw new QualificationFailureException(
                    $"manual-recovery-rejected-{recovered.Failure}");
            }

            await WaitUntilAsync(
                () => IsStableOwnWorld(composition, diagnostics.Capture()) &&
                      composition.VisitSessionService.Snapshot.Current == null &&
                      !HasRoute(
                          composition.UiRouter.CurrentSnapshot,
                          ClientUiRouteId.ConnectionLost),
                TransitionObservationDeadline);
            var visitPresentation = await composition.PersonalWorldExperience.ShowWorldVisitAsync(
                CancellationToken.None);
            if (!visitPresentation.Succeeded ||
                composition.PersonalWorldExperience.ViewState.Shell.Failure !=
                ClientPersonalWorldFailure.None)
            {
                throw new QualificationFailureException("stale-presentation-failure");
            }

            await WaitForStableResourcesAsync(diagnostics, baseline);
        }

        /// <summary>执行登录、三轮双通道恢复、route与Scene替换，并保持五分钟健康窗口。</summary>
        /// <param name="composition">唯一完整产品graph。</param>
        /// <returns>全部低敏资源断言通过后完成。</returns>
        private static async Task RunAsync(AppCompositionResult composition)
        {
            var username = Environment.GetEnvironmentVariable(UsernameVariable);
            var password = Environment.GetEnvironmentVariable(PasswordVariable);
            Environment.SetEnvironmentVariable(UsernameVariable, null);
            Environment.SetEnvironmentVariable(PasswordVariable, null);
            if (string.IsNullOrWhiteSpace(username) || string.IsNullOrEmpty(password))
            {
                throw new QualificationFailureException("missing-credentials");
            }

            var login = await composition.PersonalWorldExperience.LoginAsync(
                username,
                password,
                CancellationToken.None);
            username = null;
            password = null;
            if (!login.Succeeded)
            {
                throw new QualificationFailureException("login-rejected");
            }

            var diagnostics = composition.CreateQualificationDiagnostics();
            await WaitUntilAsync(
                () => IsStableOwnWorld(composition, diagnostics.Capture()),
                TransitionObservationDeadline);
            var baseline = diagnostics.Capture();
            AssertStableResources(baseline, baseline);
            var soakStarted = DateTime.UtcNow;

            for (var round = 0; round < 3; round++)
            {
                await WaitUntilAbsoluteAsync(
                    soakStarted.AddTicks(RoundInterval.Ticks * round));
                await RunRecoveryRoundAsync(composition, diagnostics, baseline);
            }

            while (DateTime.UtcNow - soakStarted < SoakDuration)
            {
                var snapshot = diagnostics.Capture();
                if (!IsStableOwnWorld(composition, snapshot))
                {
                    throw new QualificationFailureException("idle-health-drift");
                }

                await Task.Delay(TimeSpan.FromSeconds(1));
            }

            await WaitForStableResourcesAsync(diagnostics, baseline);
        }

        /// <summary>执行一轮control恢复、gameplay/Scene恢复和route open/close。</summary>
        /// <param name="composition">唯一完整产品graph。</param>
        /// <param name="diagnostics">只读资格计数聚合器。</param>
        /// <param name="baseline">首次稳定OwnWorld资源基线。</param>
        /// <returns>本轮恢复及资源比较完成时结束。</returns>
        private static async Task RunRecoveryRoundAsync(
            AppCompositionResult composition,
            ClientQualificationDiagnostics diagnostics,
            ClientQualificationDiagnosticSnapshot baseline)
        {
            var beforeControl = composition.ControlChannel.Snapshot.Generation;
            var gameplayBeforeControl = composition.GameplayChannel.Snapshot.Generation;
            if (!composition.ControlChannel.InjectQualificationTransportDisconnect())
            {
                throw new QualificationFailureException("control-fault-not-injected");
            }

            await WaitUntilAsync(
                () => composition.ControlChannel.Snapshot.State == ClientControlChannelState.Connected &&
                      composition.ControlChannel.Snapshot.Generation > beforeControl &&
                      composition.GameplayChannel.Snapshot.Generation == gameplayBeforeControl &&
                      IsStableOwnWorld(composition, diagnostics.Capture()),
                TransitionObservationDeadline);

            var beforeGameplay = composition.GameplayChannel.Snapshot.Generation;
            if (!composition.GameplayChannel.InjectQualificationTransportDisconnect())
            {
                throw new QualificationFailureException("gameplay-fault-not-injected");
            }

            await WaitUntilAsync(
                () => composition.GameplayChannel.Snapshot.State == ClientGameplayChannelState.Active &&
                      composition.GameplayChannel.Snapshot.Generation > beforeGameplay &&
                      IsStableOwnWorld(composition, diagnostics.Capture()),
                TransitionObservationDeadline);

            composition.PersonalWorldExperience.RequestWorldVisitFromGameplayMenu();
            await WaitUntilAsync(
                () => HasRoute(composition.UiRouter.CurrentSnapshot, ClientUiRouteId.WorldVisit),
                TransitionObservationDeadline);
            composition.PersonalWorldExperience.RequestUiCancel(ClientUiRouteId.WorldVisit);
            await WaitUntilAsync(
                () => !HasRoute(composition.UiRouter.CurrentSnapshot, ClientUiRouteId.WorldVisit),
                TransitionObservationDeadline);
            await WaitUntilAsync(
                () => diagnostics.Capture().DispatcherPendingCallbacks == 0 &&
                      diagnostics.Capture().GameplayPendingOperations == 0,
                TransitionObservationDeadline);
            await WaitForStableResourcesAsync(diagnostics, baseline);
        }

        /// <summary>判断产品与各资源owner是否已经提交稳定OwnWorld。</summary>
        /// <param name="composition">唯一完整产品graph。</param>
        /// <param name="snapshot">当前低敏资源计数。</param>
        /// <returns>产品、通道、恢复和Scene均稳定时返回true。</returns>
        private static bool IsStableOwnWorld(
            AppCompositionResult composition,
            ClientQualificationDiagnosticSnapshot snapshot)
        {
            return composition.PersonalWorldExperience.ViewState.Phase == ClientPersonalWorldPhase.OwnWorld &&
                   composition.PersonalWorldExperience.ViewState.ActiveIntent ==
                   ClientPersonalWorldIntent.None &&
                   composition.PersonalWorldExperience.ViewState.Shell.Failure ==
                   ClientPersonalWorldFailure.None &&
                   composition.WorldAdmissionCoordinator.Snapshot.Failure ==
                   ClientWorldFlowFailure.None &&
                   composition.ControlChannel.Snapshot.State == ClientControlChannelState.Connected &&
                   composition.GameplayChannel.Snapshot.State == ClientGameplayChannelState.Active &&
                   snapshot.AppRootOwners == 1 &&
                   snapshot.ControlRunOwners == 1 &&
                   snapshot.GameplaySocketOwners == 1 &&
                   snapshot.GameplayHeartbeatOwners == 1 &&
                   snapshot.RecoveryIntentOwners == 0 &&
                   snapshot.DispatcherPendingCallbacks == 0 &&
                   snapshot.SceneOwners == 1;
        }

        /// <summary>比较不应随恢复轮次增长的资源计数。</summary>
        /// <param name="baseline">首次稳定OwnWorld资源基线。</param>
        /// <param name="current">当前轮次结束后的资源快照。</param>
        /// <exception cref="QualificationFailureException">任一owner、pending或subscriber偏离基线时抛出。</exception>
        private static void AssertStableResources(
            ClientQualificationDiagnosticSnapshot baseline,
            ClientQualificationDiagnosticSnapshot current)
        {
            if (!HasStableResources(baseline, current))
            {
                throw new QualificationFailureException("resource-baseline-drift");
            }
        }

        /// <summary>在同一场景预算内等待异步清理尾声精确收敛到固定资源基线。</summary>
        /// <param name="diagnostics">只读资格计数来源。</param>
        /// <param name="baseline">本进程首次稳定OwnWorld资源基线。</param>
        /// <returns>全部计数精确收敛后完成。</returns>
        private static async Task WaitForStableResourcesAsync(
            ClientQualificationDiagnostics diagnostics,
            ClientQualificationDiagnosticSnapshot baseline)
        {
            await WaitUntilAsync(
                () => HasStableResources(baseline, diagnostics.Capture()),
                TransitionObservationDeadline);
            AssertStableResources(baseline, diagnostics.Capture());
        }

        /// <summary>检查所有权、pending、subscriber与Scene计数是否精确等于稳定基线。</summary>
        /// <param name="baseline">首次稳定资源基线。</param>
        /// <param name="current">当前只读资源快照。</param>
        /// <returns>没有重复owner、悬挂operation或订阅漂移时返回true。</returns>
        private static bool HasStableResources(
            ClientQualificationDiagnosticSnapshot baseline,
            ClientQualificationDiagnosticSnapshot current)
        {
            return current.AppRootOwners == 1 &&
                   current.ControlRunOwners == baseline.ControlRunOwners &&
                   current.GameplaySocketOwners == baseline.GameplaySocketOwners &&
                   current.GameplayHeartbeatOwners == baseline.GameplayHeartbeatOwners &&
                   current.RecoveryIntentOwners == 0 &&
                   current.GameplayPendingOperations == 0 &&
                   current.DispatcherPendingCallbacks == 0 &&
                   current.Subscriptions == baseline.Subscriptions &&
                   current.SceneOwners == baseline.SceneOwners;
        }

        /// <summary>等待明确状态predicate提交，不参与production状态推断或修正。</summary>
        /// <param name="predicate">只读检查current snapshot的条件。</param>
        /// <param name="timeout">本次观察的绝对上限。</param>
        /// <returns>条件成立时结束。</returns>
        /// <exception cref="QualificationFailureException">观察deadline内条件未成立时抛出。</exception>
        private static async Task WaitUntilAsync(Func<bool> predicate, TimeSpan timeout)
        {
            var deadline = DateTime.UtcNow.Add(timeout);
            while (!predicate())
            {
                if (DateTime.UtcNow >= deadline)
                {
                    throw new QualificationFailureException("transition-timeout");
                }

                await Task.Delay(ObservationInterval);
            }
        }

        /// <summary>等待明确状态predicate提交，并在超时后返回调用场景的稳定失败原因。</summary>
        /// <param name="predicate">只读检查current snapshot的条件。</param>
        /// <param name="timeout">本次观察的绝对上限。</param>
        /// <param name="failureReason">不含identity或payload的封闭失败原因。</param>
        /// <returns>条件成立时结束。</returns>
        /// <exception cref="QualificationFailureException">观察deadline内条件未成立时抛出。</exception>
        private static async Task WaitUntilAsync(
            Func<bool> predicate,
            TimeSpan timeout,
            string failureReason)
        {
            var deadline = DateTime.UtcNow.Add(timeout);
            while (!predicate())
            {
                if (DateTime.UtcNow >= deadline)
                {
                    throw new QualificationFailureException(failureReason);
                }

                await Task.Delay(ObservationInterval);
            }
        }

        /// <summary>等待soak轮次的绝对计划时间，不以时间推断业务成功。</summary>
        /// <param name="scheduledAt">当前轮次允许开始的UTC时刻。</param>
        /// <returns>绝对计划时刻到达时结束。</returns>
        private static async Task WaitUntilAbsoluteAsync(DateTime scheduledAt)
        {
            while (DateTime.UtcNow < scheduledAt)
            {
                var remaining = scheduledAt - DateTime.UtcNow;
                await Task.Delay(remaining < TimeSpan.FromSeconds(1)
                    ? remaining
                    : TimeSpan.FromSeconds(1));
            }
        }

        /// <summary>检查route snapshot是否包含指定唯一route。</summary>
        /// <param name="snapshot">Current route不可变快照。</param>
        /// <param name="routeId">待查找的登记route。</param>
        /// <returns>快照包含该route时返回true。</returns>
        private static bool HasRoute(ClientUiRouteSnapshot snapshot, ClientUiRouteId routeId)
        {
            foreach (var item in snapshot.Items)
            {
                if (item.Definition.RouteId == routeId)
                {
                    return true;
                }
            }

            return false;
        }

        /// <summary>读取唯一命令行参数；重复或缺值作为稳定资格失败。</summary>
        /// <param name="arguments">进程命令行参数快照。</param>
        /// <param name="name">包含前导连字符的参数名。</param>
        /// <returns>不存在时为null，合法时为参数值，重复或缺值时为空字符串。</returns>
        private static string ReadSingleArgument(string[] arguments, string name)
        {
            string value = null;
            for (var index = 0; index < arguments.Length; index++)
            {
                if (!string.Equals(arguments[index], name, StringComparison.Ordinal))
                {
                    continue;
                }

                if (value != null || index + 1 >= arguments.Length)
                {
                    return string.Empty;
                }

                value = arguments[++index];
            }

            return value;
        }

        /// <summary>观察唯一soak task并只输出稳定低敏结论。</summary>
        /// <param name="run">唯一soak owner task。</param>
        /// <returns>结论已写入日志并请求Player退出时结束。</returns>
        private static async Task ObserveAsync(Task run)
        {
            try
            {
                await run;
                Debug.Log("[IHOMELAND_QUALIFICATION] outcome=pass scenario=five-minute-recovery-soak");
                UnityEngine.Application.Quit(0);
            }
            catch (QualificationFailureException failure)
            {
                Debug.LogError($"[IHOMELAND_QUALIFICATION] outcome=fail reason={failure.Reason}");
                UnityEngine.Application.Quit(3);
            }
            catch
            {
                Debug.LogError("[IHOMELAND_QUALIFICATION] outcome=fail reason=internal");
                UnityEngine.Application.Quit(4);
            }
        }

        /// <summary>观察server replacement诊断并输出可供外部owner判定的唯一低敏结论。</summary>
        /// <param name="run">唯一诊断owner task。</param>
        /// <returns>结论写入Player日志并退出时结束。</returns>
        private static async Task ObserveServerRestartAsync(Task run)
        {
            try
            {
                await run;
                Debug.Log("[IHOMELAND_QUALIFICATION] outcome=pass scenario=server-restart-recovery");
                UnityEngine.Application.Quit(0);
            }
            catch (QualificationFailureException failure)
            {
                Debug.LogError($"[IHOMELAND_QUALIFICATION] outcome=fail reason={failure.Reason}");
                UnityEngine.Application.Quit(7);
            }
            catch
            {
                Debug.LogError("[IHOMELAND_QUALIFICATION] outcome=fail reason=internal");
                UnityEngine.Application.Quit(8);
            }
        }

        /// <summary>观察真实双Player operator并输出当前角色的唯一低敏结论。</summary>
        /// <param name="run">当前Player拥有的唯一operator task。</param>
        /// <returns>结论写入Player日志并退出时结束。</returns>
        private static async Task ObserveTwoPlayerOperatorAsync(Task run)
        {
            try
            {
                await run;
                Debug.Log("[IHOMELAND_QUALIFICATION] outcome=pass scenario=two-player-operator");
                UnityEngine.Application.Quit(0);
            }
            catch (QualificationFailureException failure)
            {
                Debug.LogError($"[IHOMELAND_QUALIFICATION] outcome=fail reason={failure.Reason}");
                UnityEngine.Application.Quit(9);
            }
            catch
            {
                Debug.LogError("[IHOMELAND_QUALIFICATION] outcome=fail reason=internal");
                UnityEngine.Application.Quit(10);
            }
        }

        /// <summary>观察真实battle runtime双Player场景并输出当前角色的唯一低敏结论。</summary>
        /// <param name="run">当前Player拥有的唯一battle runtime资格task。</param>
        /// <returns>结论写入Player日志并退出时结束。</returns>
        private static async Task ObserveClientBattleRuntimeAsync(Task run)
        {
            try
            {
                await run;
                Debug.Log(
                    "[IHOMELAND_QUALIFICATION] outcome=pass scenario=client-battle-runtime");
                UnityEngine.Application.Quit(0);
            }
            catch (QualificationFailureException failure)
            {
                Debug.LogError(
                    $"[IHOMELAND_QUALIFICATION] outcome=fail reason={failure.Reason}");
                UnityEngine.Application.Quit(11);
            }
            catch
            {
                Debug.LogError(
                    "[IHOMELAND_QUALIFICATION] outcome=fail reason=internal");
                UnityEngine.Application.Quit(12);
            }
        }

        /// <summary>观察secure store owner的精确删除结果并自然退出Player。</summary>
        /// <param name="cleanup">Session owner返回的精确store outcome。</param>
        /// <returns>结论已写入日志并请求Player退出时结束。</returns>
        private static async Task ObserveCleanupAsync(Task<ClientSecureSessionStoreOutcome> cleanup)
        {
            try
            {
                var outcome = await cleanup;
                if (outcome != ClientSecureSessionStoreOutcome.Succeeded &&
                    outcome != ClientSecureSessionStoreOutcome.NotFound)
                {
                    throw new QualificationFailureException("storage-cleanup-failed");
                }

                Debug.Log("[IHOMELAND_QUALIFICATION] outcome=pass scenario=storage-cleanup");
                UnityEngine.Application.Quit(0);
            }
            catch (QualificationFailureException failure)
            {
                Debug.LogError($"[IHOMELAND_QUALIFICATION] outcome=fail reason={failure.Reason}");
                UnityEngine.Application.Quit(5);
            }
            catch
            {
                Debug.LogError("[IHOMELAND_QUALIFICATION] outcome=fail reason=internal");
                UnityEngine.Application.Quit(6);
            }
        }

        /// <summary>只携带封闭低敏reason的资格失败。</summary>
        private sealed class QualificationFailureException : Exception
        {
            /// <summary>创建稳定资格失败。</summary>
            /// <param name="reason">允许写入Development日志的封闭reason。</param>
            internal QualificationFailureException(string reason)
                : base("Client qualification failed.")
            {
                Reason = reason;
            }

            /// <summary>获取允许写入Development日志的封闭reason。</summary>
            internal string Reason { get; }
        }
    }
}
#endif
