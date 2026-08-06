using System;
using System.Threading;
using System.Threading.Tasks;

namespace IHomeland.Client.PersonalWorld.Application
{
    /// <summary>标识根据 channel health 与冻结 target 计算的恢复计划。</summary>
    internal enum ConnectionRecoveryPlanKind
    {
        /// <summary>当前无需恢复。</summary>
        None = 0,
        /// <summary>只恢复 Control 并重取 control-only projection。</summary>
        ControlOnly = 1,
        /// <summary>先恢复 Control，再恢复冻结世界 target。</summary>
        ControlThenGameplay = 2,
        /// <summary>只恢复冻结 Gameplay target。</summary>
        Gameplay = 3,
    }

    /// <summary>保存不含 credential/endpoint 的纯恢复计划。</summary>
    internal sealed class ConnectionRecoveryPlan
    {
        /// <summary>创建冻结计划。</summary>
        internal ConnectionRecoveryPlan(
            ConnectionRecoveryPlanKind kind,
            ClientRecoveryTargetDescriptor target)
        {
            Kind = kind;
            Target = target;
        }

        /// <summary>获取执行类别。</summary>
        internal ConnectionRecoveryPlanKind Kind { get; }

        /// <summary>获取可选冻结 target descriptor。</summary>
        internal ClientRecoveryTargetDescriptor Target { get; }
    }

    /// <summary>纯计算 channel health 到最小恢复计划。</summary>
    internal sealed class ConnectionRecoveryPlanBuilder
    {
        /// <summary>根据当前健康状态生成不含副作用的计划。</summary>
        internal ConnectionRecoveryPlan Build(
            bool controlConnected,
            bool gameplayConnected,
            ClientRecoveryTargetDescriptor target)
        {
            if (controlConnected && gameplayConnected)
            {
                return new ConnectionRecoveryPlan(
                    ConnectionRecoveryPlanKind.None,
                    target);
            }

            if (!controlConnected)
            {
                return new ConnectionRecoveryPlan(
                    target == null || gameplayConnected
                        ? ConnectionRecoveryPlanKind.ControlOnly
                        : ConnectionRecoveryPlanKind.ControlThenGameplay,
                    target);
            }

            return new ConnectionRecoveryPlan(
                target == null
                    ? ConnectionRecoveryPlanKind.None
                    : ConnectionRecoveryPlanKind.Gameplay,
                target);
        }
    }

    /// <summary>纯计算 recovery phase 的 single-flight 与迟到提交规则。</summary>
    internal sealed class ConnectionRecoveryStateMachine
    {
        /// <summary>判断 control intent 是否可从 current phase 开始。</summary>
        internal bool CanBeginControl(
            ClientConnectionRecoverySnapshot snapshot,
            bool manual)
        {
            if (snapshot == null ||
                snapshot.Phase == ClientConnectionRecoveryPhase.Stopped ||
                snapshot.Phase == ClientConnectionRecoveryPhase.RecoveringWorld ||
                snapshot.Phase == ClientConnectionRecoveryPhase.AwaitingSceneCommit)
            {
                return false;
            }

            return !manual ||
                   snapshot.Phase == ClientConnectionRecoveryPhase.ConnectionLost;
        }

        /// <summary>判断 Gameplay intent 是否可从 current phase 开始。</summary>
        internal bool CanBeginGameplay(
            ClientConnectionRecoverySnapshot snapshot,
            bool manual)
        {
            if (snapshot == null ||
                snapshot.Phase == ClientConnectionRecoveryPhase.Stopped ||
                snapshot.Phase == ClientConnectionRecoveryPhase.AwaitingSceneCommit)
            {
                return false;
            }

            return manual
                ? snapshot.Phase == ClientConnectionRecoveryPhase.ConnectionLost
                : snapshot.Phase != ClientConnectionRecoveryPhase.RecoveringWorld;
        }

        /// <summary>判断异步完成仍属于 current intent 与预期 phase。</summary>
        internal bool CanCommit(
            ClientConnectionRecoverySnapshot snapshot,
            long intentGeneration,
            ClientConnectionRecoveryPhase expectedPhase)
        {
            return snapshot != null &&
                   snapshot.IntentGeneration == intentGeneration &&
                   snapshot.Phase == expectedPhase;
        }
    }

    /// <summary>把 flow 异常与取消封闭成 recovery terminal。</summary>
    internal sealed class ConnectionRecoveryFailureMapper
    {
        /// <summary>映射 operation completion；未登记异常不能逃出恢复 owner。</summary>
        internal ClientConnectionRecoveryResultKind Map(
            Exception exception,
            bool stopped,
            bool deadline)
        {
            if (exception == null)
            {
                return ClientConnectionRecoveryResultKind.Succeeded;
            }

            if (exception is OperationCanceledException)
            {
                return stopped
                    ? ClientConnectionRecoveryResultKind.Stopped
                    : deadline
                        ? ClientConnectionRecoveryResultKind.Deadline
                        : ClientConnectionRecoveryResultKind.Internal;
            }

            return ClientConnectionRecoveryResultKind.Internal;
        }
    }

    /// <summary>编排 Control reconciliation，不保存 recovery 最终事实。</summary>
    internal sealed class RecoverControlFlow
    {
        /// <summary>执行唯一登记 Control recovery operation。</summary>
        internal Task<ClientConnectionRecoveryResultKind> ExecuteAsync(
            IClientConnectionRecoveryOperations operations,
            ClientRecoveryTargetDescriptor target,
            CancellationToken cancellationToken)
        {
            if (operations == null)
            {
                throw new ArgumentNullException(nameof(operations));
            }

            if (target == null)
            {
                return Task.FromResult(ClientConnectionRecoveryResultKind.Policy);
            }

            return operations.ReconcileControlAsync(target, cancellationToken);
        }
    }

    /// <summary>编排冻结 OwnWorld/Visitor target 的 Gameplay recovery。</summary>
    internal sealed class RecoverGameplayFlow
    {
        /// <summary>执行唯一登记 Gameplay recovery operation。</summary>
        internal Task<ClientConnectionRecoveryResultKind> ExecuteAsync(
            IClientConnectionRecoveryOperations operations,
            ClientRecoveryTargetDescriptor target,
            CancellationToken cancellationToken)
        {
            if (operations == null)
            {
                throw new ArgumentNullException(nameof(operations));
            }

            if (target == null)
            {
                return Task.FromResult(ClientConnectionRecoveryResultKind.Policy);
            }

            return operations.RecoverWorldAsync(target, cancellationToken);
        }
    }
}
