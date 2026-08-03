using System;

namespace IHomeland.Client.Application.Battle
{
    /// <summary>
    /// 保存由 battle-network-profile-v2 与 battle-model-v1 冻结的客户端运行参数。
    /// </summary>
    /// <remarks>
    /// 该类型不从远端配置接受覆盖。任何数值变化必须先更新 source manifest、profile/model
    /// 与跨端契约，再由 entry validator 同步验证本投影。
    /// </remarks>
    internal sealed class ClientBattlePolicy
    {
        /// <summary>获取唯一 current policy。</summary>
        internal static ClientBattlePolicy Current { get; } = new ClientBattlePolicy(
            inputStepMilliseconds: 25,
            simulationStepMilliseconds: 50,
            inputLeadSimulationTicks: 2,
            maximumInputStepsPerFrame: 4,
            inputBundleDepth: 3,
            inputBundleRedundancy: 2,
            continuousHoldTicks: 4,
            historyTicks: 16,
            correctionPositionMillimeters: 80,
            correctionAngleMillidegrees: 2000,
            interpolationDelayMilliseconds: 100,
            maximumExtrapolationMilliseconds: 150,
            maximumBaselineAgeTicks: 40,
            maximumBaselineFanout: 10,
            maximumQueueItems: 256,
            maximumKcpMessages: 64,
            maximumSnapshotPartitions: 16,
            maximumEntities: 256,
            maximumSamplesPerEntity: 16,
            maximumGameplayCues: 256,
            maximumHorizontalSpeedMillimetersPerSecond: 3000,
            accelerationMillimetersPerSecondSquared: 60000,
            decelerationMillimetersPerSecondSquared: 60000,
            gravityMillimetersPerSecondSquared: 10000,
            jumpSpeedMillimetersPerSecond: 5000);

        /// <summary>
        /// 创建完整且自洽的 immutable policy。
        /// </summary>
        internal ClientBattlePolicy(
            int inputStepMilliseconds,
            int simulationStepMilliseconds,
            int inputLeadSimulationTicks,
            int maximumInputStepsPerFrame,
            int inputBundleDepth,
            int inputBundleRedundancy,
            int continuousHoldTicks,
            int historyTicks,
            int correctionPositionMillimeters,
            int correctionAngleMillidegrees,
            int interpolationDelayMilliseconds,
            int maximumExtrapolationMilliseconds,
            int maximumBaselineAgeTicks,
            int maximumBaselineFanout,
            int maximumQueueItems,
            int maximumKcpMessages,
            int maximumSnapshotPartitions,
            int maximumEntities,
            int maximumSamplesPerEntity,
            int maximumGameplayCues,
            int maximumHorizontalSpeedMillimetersPerSecond,
            int accelerationMillimetersPerSecondSquared,
            int decelerationMillimetersPerSecondSquared,
            int gravityMillimetersPerSecondSquared,
            int jumpSpeedMillimetersPerSecond)
        {
            if (inputStepMilliseconds <= 0 ||
                simulationStepMilliseconds <= 0 ||
                simulationStepMilliseconds % inputStepMilliseconds != 0 ||
                inputLeadSimulationTicks != 2 ||
                maximumInputStepsPerFrame <= 0 ||
                maximumInputStepsPerFrame > 8 ||
                inputBundleDepth != 3 ||
                inputBundleRedundancy != 2 ||
                continuousHoldTicks != 4 ||
                historyTicks != 16 ||
                correctionPositionMillimeters != 80 ||
                correctionAngleMillidegrees != 2000 ||
                interpolationDelayMilliseconds != 100 ||
                maximumExtrapolationMilliseconds != 150 ||
                maximumBaselineAgeTicks != 40 ||
                maximumBaselineFanout != 10 ||
                maximumQueueItems != 256 ||
                maximumKcpMessages != 64 ||
                maximumSnapshotPartitions <= 0 ||
                maximumSnapshotPartitions > 32 ||
                maximumEntities <= 0 ||
                maximumEntities > maximumQueueItems ||
                maximumSamplesPerEntity <= 1 ||
                maximumSamplesPerEntity > historyTicks ||
                maximumGameplayCues <= 0 ||
                maximumGameplayCues > maximumQueueItems ||
                maximumHorizontalSpeedMillimetersPerSecond <= 0 ||
                accelerationMillimetersPerSecondSquared <= 0 ||
                decelerationMillimetersPerSecondSquared <= 0 ||
                gravityMillimetersPerSecondSquared <= 0 ||
                jumpSpeedMillimetersPerSecond <= 0)
            {
                throw new ArgumentException(
                    "Client battle policy violates the frozen profile/model contract.");
            }

            InputStepMilliseconds = inputStepMilliseconds;
            SimulationStepMilliseconds = simulationStepMilliseconds;
            InputLeadSimulationTicks = inputLeadSimulationTicks;
            MaximumInputStepsPerFrame = maximumInputStepsPerFrame;
            InputBundleDepth = inputBundleDepth;
            InputBundleRedundancy = inputBundleRedundancy;
            ContinuousHoldTicks = continuousHoldTicks;
            HistoryTicks = historyTicks;
            CorrectionPositionMillimeters = correctionPositionMillimeters;
            CorrectionAngleMillidegrees = correctionAngleMillidegrees;
            InterpolationDelayMilliseconds = interpolationDelayMilliseconds;
            MaximumExtrapolationMilliseconds = maximumExtrapolationMilliseconds;
            MaximumBaselineAgeTicks = maximumBaselineAgeTicks;
            MaximumBaselineFanout = maximumBaselineFanout;
            MaximumQueueItems = maximumQueueItems;
            MaximumKcpMessages = maximumKcpMessages;
            MaximumSnapshotPartitions = maximumSnapshotPartitions;
            MaximumEntities = maximumEntities;
            MaximumSamplesPerEntity = maximumSamplesPerEntity;
            MaximumGameplayCues = maximumGameplayCues;
            MaximumHorizontalSpeedMillimetersPerSecond =
                maximumHorizontalSpeedMillimetersPerSecond;
            AccelerationMillimetersPerSecondSquared =
                accelerationMillimetersPerSecondSquared;
            DecelerationMillimetersPerSecondSquared =
                decelerationMillimetersPerSecondSquared;
            GravityMillimetersPerSecondSquared =
                gravityMillimetersPerSecondSquared;
            JumpSpeedMillimetersPerSecond = jumpSpeedMillimetersPerSecond;
        }

        /// <summary>获取 semantic input cadence，单位毫秒。</summary>
        internal int InputStepMilliseconds { get; }

        /// <summary>获取 authority simulation cadence，单位毫秒。</summary>
        internal int SimulationStepMilliseconds { get; }

        /// <summary>获取首个 InputTick 相对已观察 ServerTick 的冻结提前窗口。</summary>
        internal int InputLeadSimulationTicks { get; }

        /// <summary>获取一帧允许生成的 input step 硬上限。</summary>
        internal int MaximumInputStepsPerFrame { get; }

        /// <summary>获取单个 bundle 覆盖的相邻 InputTick 数量。</summary>
        internal int InputBundleDepth { get; }

        /// <summary>获取冻结的相邻 bundle 冗余份数。</summary>
        internal int InputBundleRedundancy { get; }

        /// <summary>获取服务端连续输入 hold 上限。</summary>
        internal int ContinuousHoldTicks { get; }

        /// <summary>获取 input/prediction history 硬上限。</summary>
        internal int HistoryTicks { get; }

        /// <summary>获取位置 hard correction 阈值，单位毫米。</summary>
        internal int CorrectionPositionMillimeters { get; }

        /// <summary>获取角度 hard correction 阈值，单位 millidegree。</summary>
        internal int CorrectionAngleMillidegrees { get; }

        /// <summary>获取远端表现延迟，单位毫秒。</summary>
        internal int InterpolationDelayMilliseconds { get; }

        /// <summary>获取远端最大外推时长，单位毫秒。</summary>
        internal int MaximumExtrapolationMilliseconds { get; }

        /// <summary>获取 baseline 最大 age，单位 server Tick。</summary>
        internal int MaximumBaselineAgeTicks { get; }

        /// <summary>获取单个 baseline 最大 delta fanout。</summary>
        internal int MaximumBaselineFanout { get; }

        /// <summary>获取 managed send/receive/dispatcher hard limit。</summary>
        internal int MaximumQueueItems { get; }

        /// <summary>获取 KCP message queue hard limit。</summary>
        internal int MaximumKcpMessages { get; }

        /// <summary>获取单个 snapshot set 的 partition hard limit。</summary>
        internal int MaximumSnapshotPartitions { get; }

        /// <summary>获取客户端 replica entity hard limit。</summary>
        internal int MaximumEntities { get; }

        /// <summary>获取每个 remote entity 的 sample hard limit。</summary>
        internal int MaximumSamplesPerEntity { get; }

        /// <summary>获取未消费 gameplay cue hard limit。</summary>
        internal int MaximumGameplayCues { get; }

        /// <summary>获取最小本地预测水平速度上限，单位 mm/s。</summary>
        internal int MaximumHorizontalSpeedMillimetersPerSecond { get; }

        /// <summary>获取本地预测水平加速度，单位 mm/s²。</summary>
        internal int AccelerationMillimetersPerSecondSquared { get; }

        /// <summary>获取本地预测水平减速度，单位 mm/s²。</summary>
        internal int DecelerationMillimetersPerSecondSquared { get; }

        /// <summary>获取本地预测重力，单位 mm/s²。</summary>
        internal int GravityMillimetersPerSecondSquared { get; }

        /// <summary>获取本地预测跳跃初速度，单位 mm/s。</summary>
        internal int JumpSpeedMillimetersPerSecond { get; }

        /// <summary>
        /// 把 InputTick 映射到 one-based SimulationTick。
        /// </summary>
        /// <param name="inputTick">One-based InputTick。</param>
        /// <returns>冻结 2:1 mapping 下的 one-based SimulationTick。</returns>
        internal ulong MapInputToSimulationTick(ulong inputTick)
        {
            if (inputTick == 0)
            {
                throw new ArgumentOutOfRangeException(nameof(inputTick));
            }

            var ratio = (ulong)(SimulationStepMilliseconds / InputStepMilliseconds);
            return ((inputTick - 1) / ratio) + 1;
        }

        /// <summary>
        /// 以已应用 full baseline 为锚点计算 current generation 的首个 InputTick。
        /// </summary>
        /// <param name="latestServerTick">已原子应用的非零 ServerTick。</param>
        /// <returns>映射到 profile 提前窗口末端的首个 one-based InputTick。</returns>
        internal ulong FirstInputTickAfterBaseline(ulong latestServerTick)
        {
            if (latestServerTick == 0)
            {
                throw new ArgumentOutOfRangeException(
                    nameof(latestServerTick));
            }

            var ratio =
                (ulong)(SimulationStepMilliseconds / InputStepMilliseconds);
            var targetSimulationTick = checked(
                latestServerTick + (ulong)InputLeadSimulationTicks);
            return checked(
                1UL + ((targetSimulationTick - 1UL) * ratio));
        }

        /// <summary>
        /// 计算当前已观察authority提前窗口内允许生成的最后一个InputTick。
        /// </summary>
        /// <param name="latestServerTick">已原子应用的非零ServerTick。</param>
        /// <returns>映射到冻结early window末端的最后一个InputTick。</returns>
        internal ulong LastInputTickAtObservedHorizon(ulong latestServerTick)
        {
            var first =
                FirstInputTickAfterBaseline(latestServerTick);
            var ratio =
                (ulong)(SimulationStepMilliseconds / InputStepMilliseconds);
            return checked(first + ratio - 1UL);
        }
    }
}
