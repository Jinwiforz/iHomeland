using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;

namespace IHomeland.Client.Application.Battle
{
    /// <summary>
    /// 保存单个 remote entity 的插值或有界外推结果。
    /// </summary>
    internal sealed class ClientBattleInterpolatedState
    {
        /// <summary>
        /// 创建不依赖 Unity Transform 的 immutable remote view state。
        /// </summary>
        internal ClientBattleInterpolatedState(
            long battleGeneration,
            ulong entityID,
            uint entityGeneration,
            ClientBattleTransform transform,
            bool extrapolated,
            bool stale)
        {
            if (battleGeneration <= 0 ||
                entityID == 0 ||
                entityGeneration == 0 ||
                (stale && !extrapolated))
            {
                throw new ArgumentException(
                    "Client battle interpolated state is invalid.");
            }

            BattleGeneration = battleGeneration;
            EntityID = entityID;
            EntityGeneration = entityGeneration;
            Transform = transform;
            Extrapolated = extrapolated;
            Stale = stale;
        }

        /// <summary>获取来源 battle generation。</summary>
        internal long BattleGeneration { get; }

        /// <summary>获取 remote entity identity。</summary>
        internal ulong EntityID { get; }

        /// <summary>获取 remote entity lifecycle generation。</summary>
        internal uint EntityGeneration { get; }

        /// <summary>获取插值、外推或冻结后的量化 transform。</summary>
        internal ClientBattleTransform Transform { get; }

        /// <summary>获取结果是否位于最新 sample 之后。</summary>
        internal bool Extrapolated { get; }

        /// <summary>获取结果是否已超过 150 ms 并冻结。</summary>
        internal bool Stale { get; }
    }

    /// <summary>
    /// 唯一拥有 remote entity sample 与 100 ms render timeline 的纯 C# owner。
    /// </summary>
    internal sealed class GameplayInterpolation
    {
        /// <summary>保护 generation 与所有 entity sample buffer。</summary>
        private readonly object _sync = new object();

        /// <summary>保存冻结 interpolation policy。</summary>
        private readonly ClientBattlePolicy _policy;

        /// <summary>保存每个 remote entity 的有界排序 samples。</summary>
        private readonly SortedDictionary<ulong, EntitySamples> _samples =
            new SortedDictionary<ulong, EntitySamples>();

        /// <summary>保存 current battle generation。</summary>
        private long _battleGeneration;

        /// <summary>保存禁止进入 remote buffer 的 local entity identity。</summary>
        private ulong _localEntityID;

        /// <summary>
        /// 创建 remote interpolation owner。
        /// </summary>
        internal GameplayInterpolation(ClientBattlePolicy policy)
        {
            _policy = policy ?? throw new ArgumentNullException(nameof(policy));
        }

        /// <summary>
        /// 激活 successor generation并清空所有旧 sample。
        /// </summary>
        internal void Activate(long battleGeneration, ulong localEntityID)
        {
            if (battleGeneration <= 0 || localEntityID == 0)
            {
                throw new ArgumentOutOfRangeException(nameof(battleGeneration));
            }

            lock (_sync)
            {
                if (battleGeneration <= _battleGeneration)
                {
                    throw new InvalidOperationException(
                        "GameplayInterpolation requires a successor generation.");
                }

                _samples.Clear();
                _battleGeneration = battleGeneration;
                _localEntityID = localEntityID;
            }
        }

        /// <summary>
        /// 加入一个 authority remote sample，按 server Tick 排序且 duplicate 幂等。
        /// </summary>
        internal void AddSample(
            long battleGeneration,
            ulong serverTick,
            ClientBattleEntityState state)
        {
            if (state == null)
            {
                throw new ArgumentNullException(nameof(state));
            }

            lock (_sync)
            {
                if (battleGeneration != _battleGeneration ||
                    _battleGeneration == 0 ||
                    state.EntityID == _localEntityID)
                {
                    return;
                }

                if (!_samples.TryGetValue(state.EntityID, out var buffer))
                {
                    if (_samples.Count >= _policy.MaximumEntities)
                    {
                        throw new ClientBattleInterpolationException(
                            "Remote interpolation entity capacity reached.");
                    }

                    buffer = new EntitySamples(
                        state.EntityID,
                        state.Generation,
                        _policy.MaximumSamplesPerEntity);
                    _samples.Add(state.EntityID, buffer);
                }

                buffer.Add(serverTick, state);
            }
        }

        /// <summary>
        /// 以完整 authority entity set 替换 remote sample owners，清除已不在 full baseline 的实体。
        /// </summary>
        /// <param name="battleGeneration">必须匹配 current generation。</param>
        /// <param name="serverTick">完整 baseline 的 authority Tick。</param>
        /// <param name="states">按 identity 排序且包含 local actor 的完整 entity set。</param>
        internal void ReplaceSamples(
            long battleGeneration,
            ulong serverTick,
            IReadOnlyList<ClientBattleEntityState> states)
        {
            if (serverTick == 0 || states == null)
            {
                throw new ArgumentException(
                    "Full interpolation sample set is invalid.");
            }

            lock (_sync)
            {
                if (battleGeneration != _battleGeneration ||
                    _battleGeneration == 0)
                {
                    return;
                }

                _samples.Clear();
                for (var index = 0; index < states.Count; index++)
                {
                    var state = states[index] ??
                        throw new ArgumentNullException(nameof(states));
                    if (state.EntityID == _localEntityID)
                    {
                        continue;
                    }

                    if (_samples.Count >= _policy.MaximumEntities)
                    {
                        throw new ClientBattleInterpolationException(
                            "Full interpolation entity capacity reached.");
                    }

                    var buffer = new EntitySamples(
                        state.EntityID,
                        state.Generation,
                        _policy.MaximumSamplesPerEntity);
                    buffer.Add(serverTick, state);
                    _samples.Add(state.EntityID, buffer);
                }
            }
        }

        /// <summary>
        /// 删除已 despawn 的 exact entity generation。
        /// </summary>
        internal void Remove(
            long battleGeneration,
            ulong entityID,
            uint entityGeneration)
        {
            lock (_sync)
            {
                if (battleGeneration != _battleGeneration ||
                    !_samples.TryGetValue(entityID, out var buffer) ||
                    buffer.EntityGeneration != entityGeneration)
                {
                    return;
                }

                _samples.Remove(entityID);
            }
        }

        /// <summary>
        /// 在 authority timeline 上落后 100 ms 计算全部 remote state。
        /// </summary>
        /// <param name="battleGeneration">必须匹配 current generation。</param>
        /// <param name="authorityNowMilliseconds">由 latest server Tick 加本地 elapsed 推导的 timeline。</param>
        /// <returns>按 entity identity 排序的 immutable view states。</returns>
        internal IReadOnlyList<ClientBattleInterpolatedState> Evaluate(
            long battleGeneration,
            long authorityNowMilliseconds)
        {
            lock (_sync)
            {
                if (battleGeneration != _battleGeneration ||
                    _battleGeneration == 0)
                {
                    return Array.Empty<ClientBattleInterpolatedState>();
                }

                var target = authorityNowMilliseconds -
                    _policy.InterpolationDelayMilliseconds;
                var output = new ClientBattleInterpolatedState[_samples.Count];
                var index = 0;
                foreach (var pair in _samples)
                {
                    output[index++] = pair.Value.Evaluate(
                        _battleGeneration,
                        target,
                        _policy.SimulationStepMilliseconds,
                        _policy.MaximumExtrapolationMilliseconds);
                }

                return new ReadOnlyCollection<ClientBattleInterpolatedState>(output);
            }
        }

        /// <summary>
        /// 结束 current generation并释放全部 sample buffer。
        /// </summary>
        internal void Deactivate(long battleGeneration)
        {
            lock (_sync)
            {
                if (battleGeneration != _battleGeneration)
                {
                    return;
                }

                _samples.Clear();
                _battleGeneration = 0;
                _localEntityID = 0;
            }
        }

        /// <summary>
        /// 保存单个 entity generation 的固定容量 samples。
        /// </summary>
        private sealed class EntitySamples
        {
            /// <summary>保存按 server Tick 递增的 samples。</summary>
            private readonly List<Sample> _items = new List<Sample>();

            /// <summary>保存每 entity hard limit。</summary>
            private readonly int _capacity;

            /// <summary>
            /// 创建固定 generation 的 sample buffer。
            /// </summary>
            internal EntitySamples(
                ulong entityID,
                uint entityGeneration,
                int capacity)
            {
                EntityID = entityID;
                EntityGeneration = entityGeneration;
                _capacity = capacity;
            }

            /// <summary>获取 entity identity。</summary>
            internal ulong EntityID { get; }

            /// <summary>获取固定 lifecycle generation。</summary>
            internal uint EntityGeneration { get; }

            /// <summary>
            /// 插入有序 sample，拒绝 generation drift 与 conflicting duplicate。
            /// </summary>
            internal void Add(ulong serverTick, ClientBattleEntityState state)
            {
                if (serverTick == 0 ||
                    state.EntityID != EntityID ||
                    state.Generation != EntityGeneration)
                {
                    throw new ClientBattleInterpolationException(
                        "Remote interpolation sample binding drifted.");
                }

                var insertion = _items.BinarySearch(
                    new Sample(serverTick, state.Transform),
                    SampleComparer.Instance);
                if (insertion >= 0)
                {
                    if (!TransformEqual(
                            _items[insertion].Transform,
                            state.Transform))
                    {
                        throw new ClientBattleInterpolationException(
                            "Remote interpolation duplicate Tick conflicts.");
                    }

                    return;
                }

                _items.Insert(~insertion, new Sample(serverTick, state.Transform));
                if (_items.Count > _capacity)
                {
                    _items.RemoveAt(0);
                }
            }

            /// <summary>
            /// 对单个 entity 执行插值、有界外推或 stale freeze。
            /// </summary>
            internal ClientBattleInterpolatedState Evaluate(
                long battleGeneration,
                long targetMilliseconds,
                int simulationStepMilliseconds,
                int maximumExtrapolationMilliseconds)
            {
                if (_items.Count == 0)
                {
                    throw new ClientBattleInterpolationException(
                        "Remote interpolation buffer is empty.");
                }

                Sample? before = null;
                Sample? after = null;
                for (var index = 0; index < _items.Count; index++)
                {
                    var sample = _items[index];
                    var sampleMilliseconds = checked(
                        (long)sample.ServerTick * simulationStepMilliseconds);
                    if (sampleMilliseconds <= targetMilliseconds)
                    {
                        before = sample;
                    }

                    if (sampleMilliseconds >= targetMilliseconds)
                    {
                        after = sample;
                        break;
                    }
                }

                if (!before.HasValue)
                {
                    var earliest = _items[0];
                    return new ClientBattleInterpolatedState(
                        battleGeneration,
                        EntityID,
                        EntityGeneration,
                        earliest.Transform,
                        extrapolated: false,
                        stale: false);
                }

                if (after.HasValue &&
                    after.Value.ServerTick != before.Value.ServerTick)
                {
                    var beforeMilliseconds = checked(
                        (long)before.Value.ServerTick * simulationStepMilliseconds);
                    var afterMilliseconds = checked(
                        (long)after.Value.ServerTick * simulationStepMilliseconds);
                    var numerator = targetMilliseconds - beforeMilliseconds;
                    var denominator = afterMilliseconds - beforeMilliseconds;
                    return new ClientBattleInterpolatedState(
                        battleGeneration,
                        EntityID,
                        EntityGeneration,
                        Interpolate(
                            before.Value.Transform,
                            after.Value.Transform,
                            numerator,
                            denominator),
                        extrapolated: false,
                        stale: false);
                }

                var latest = _items[_items.Count - 1];
                var latestMilliseconds = checked(
                    (long)latest.ServerTick * simulationStepMilliseconds);
                if (targetMilliseconds == latestMilliseconds)
                {
                    return new ClientBattleInterpolatedState(
                        battleGeneration,
                        EntityID,
                        EntityGeneration,
                        latest.Transform,
                        extrapolated: false,
                        stale: false);
                }

                var elapsed = Math.Max(0, targetMilliseconds - latestMilliseconds);
                var capped = Math.Min(elapsed, maximumExtrapolationMilliseconds);
                var extrapolated = Extrapolate(latest.Transform, capped);
                return new ClientBattleInterpolatedState(
                    battleGeneration,
                    EntityID,
                    EntityGeneration,
                    extrapolated,
                    extrapolated: true,
                    stale: elapsed > maximumExtrapolationMilliseconds);
            }

            /// <summary>
            /// 线性插值量化位置、速度与最短 yaw。
            /// </summary>
            private static ClientBattleTransform Interpolate(
                ClientBattleTransform first,
                ClientBattleTransform second,
                long numerator,
                long denominator)
            {
                return new ClientBattleTransform(
                    Lerp(
                        first.PositionXMillimeters,
                        second.PositionXMillimeters,
                        numerator,
                        denominator),
                    Lerp(
                        first.PositionYMillimeters,
                        second.PositionYMillimeters,
                        numerator,
                        denominator),
                    Lerp(
                        first.PositionZMillimeters,
                        second.PositionZMillimeters,
                        numerator,
                        denominator),
                    LerpYaw(
                        first.YawMillidegrees,
                        second.YawMillidegrees,
                        numerator,
                        denominator),
                    Lerp(
                        first.VelocityXMillimetersPerSecond,
                        second.VelocityXMillimetersPerSecond,
                        numerator,
                        denominator),
                    Lerp(
                        first.VelocityYMillimetersPerSecond,
                        second.VelocityYMillimetersPerSecond,
                        numerator,
                        denominator),
                    Lerp(
                        first.VelocityZMillimetersPerSecond,
                        second.VelocityZMillimetersPerSecond,
                        numerator,
                        denominator));
            }

            /// <summary>
            /// 使用 latest velocity 外推量化位置，不改变 authority velocity/yaw。
            /// </summary>
            private static ClientBattleTransform Extrapolate(
                ClientBattleTransform source,
                long elapsedMilliseconds)
            {
                return new ClientBattleTransform(
                    checked(
                        source.PositionXMillimeters +
                        ScaleVelocity(
                            source.VelocityXMillimetersPerSecond,
                            elapsedMilliseconds)),
                    checked(
                        source.PositionYMillimeters +
                        ScaleVelocity(
                            source.VelocityYMillimetersPerSecond,
                            elapsedMilliseconds)),
                    checked(
                        source.PositionZMillimeters +
                        ScaleVelocity(
                            source.VelocityZMillimetersPerSecond,
                            elapsedMilliseconds)),
                    source.YawMillidegrees,
                    source.VelocityXMillimetersPerSecond,
                    source.VelocityYMillimetersPerSecond,
                    source.VelocityZMillimetersPerSecond);
            }

            /// <summary>
            /// 对 signed value 执行有界整数 lerp。
            /// </summary>
            private static int Lerp(
                int first,
                int second,
                long numerator,
                long denominator)
            {
                return checked(
                    first +
                    (int)(((long)second - first) * numerator / denominator));
            }

            /// <summary>
            /// 沿最短圆周路径插值 yaw。
            /// </summary>
            private static int LerpYaw(
                int first,
                int second,
                long numerator,
                long denominator)
            {
                var difference = second - first;
                if (difference > 180000)
                {
                    difference -= 360000;
                }
                else if (difference < -180000)
                {
                    difference += 360000;
                }

                return checked(first + (int)((long)difference * numerator / denominator));
            }

            /// <summary>
            /// 把 velocity 按 elapsed 毫秒转换为 position delta。
            /// </summary>
            private static int ScaleVelocity(int velocity, long elapsedMilliseconds)
            {
                return checked((int)((long)velocity * elapsedMilliseconds / 1000));
            }

            /// <summary>
            /// 比较 transform 全部量化字段。
            /// </summary>
            private static bool TransformEqual(
                ClientBattleTransform first,
                ClientBattleTransform second)
            {
                return first.PositionXMillimeters == second.PositionXMillimeters &&
                       first.PositionYMillimeters == second.PositionYMillimeters &&
                       first.PositionZMillimeters == second.PositionZMillimeters &&
                       first.YawMillidegrees == second.YawMillidegrees &&
                       first.VelocityXMillimetersPerSecond ==
                           second.VelocityXMillimetersPerSecond &&
                       first.VelocityYMillimetersPerSecond ==
                           second.VelocityYMillimetersPerSecond &&
                       first.VelocityZMillimetersPerSecond ==
                           second.VelocityZMillimetersPerSecond;
            }
        }

        /// <summary>
        /// 保存单个 authority sample。
        /// </summary>
        private readonly struct Sample
        {
            /// <summary>
            /// 创建 server Tick 与 transform pair。
            /// </summary>
            internal Sample(ulong serverTick, ClientBattleTransform transform)
            {
                ServerTick = serverTick;
                Transform = transform;
            }

            /// <summary>获取 authority server Tick。</summary>
            internal ulong ServerTick { get; }

            /// <summary>获取 authority transform。</summary>
            internal ClientBattleTransform Transform { get; }
        }

        /// <summary>
        /// 按 server Tick 比较 samples。
        /// </summary>
        private sealed class SampleComparer : IComparer<Sample>
        {
            /// <summary>获取无状态 comparer 单例。</summary>
            internal static SampleComparer Instance { get; } = new SampleComparer();

            /// <inheritdoc />
            public int Compare(Sample first, Sample second)
            {
                return first.ServerTick.CompareTo(second.ServerTick);
            }
        }
    }

    /// <summary>
    /// 表示 remote interpolation sample 或 timeline 违反冻结边界。
    /// </summary>
    internal sealed class ClientBattleInterpolationException : Exception
    {
        /// <summary>
        /// 创建低敏 interpolation failure。
        /// </summary>
        internal ClientBattleInterpolationException(string message)
            : base(message)
        {
        }
    }
}
