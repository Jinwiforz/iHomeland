using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;

namespace IHomeland.Client.PersonalWorldCombat.Application
{
    /// <summary>
    /// 标识客户端唯一可提交给 authority 的 semantic input。
    /// </summary>
    internal enum ClientBattleInputKind
    {
        /// <summary>请求更新量化移动轴。</summary>
        Move = 1,

        /// <summary>请求更新量化瞄准方向。</summary>
        Aim = 2,

        /// <summary>请求一次 grounded jump edge。</summary>
        Jump = 3,

        /// <summary>请求 primary ability。</summary>
        PrimaryAbility = 4,

        /// <summary>请求 secondary ability。</summary>
        SecondaryAbility = 5,

        /// <summary>请求受限交互槽位。</summary>
        Interact = 6,

        /// <summary>请求切换当前已授权武器，不携带目标 weapon。</summary>
        SwitchWeapon = 7,
    }

    /// <summary>
    /// 保存由 production wire mapping 冻结并由 build parity 校验的 content ID。
    /// </summary>
    internal static class ClientBattleContentIdentity
    {
        /// <summary>Player actor archetype ID。</summary>
        internal const uint PlayerArchetype = 1;

        /// <summary>Ordinary monster actor archetype ID。</summary>
        internal const uint MonsterArchetype = 2;

        /// <summary>Boss actor archetype ID。</summary>
        internal const uint BossArchetype = 3;

        /// <summary>Fan projectile archetype ID。</summary>
        internal const uint FanProjectileArchetype = 301;

        /// <summary>Sword weapon ID。</summary>
        internal const uint SwordWeapon = 101;

        /// <summary>Fan weapon ID。</summary>
        internal const uint FanWeapon = 102;

        /// <summary>Sword primary ability ID。</summary>
        internal const uint SwordAbility = 201;

        /// <summary>Fan primary ability ID。</summary>
        internal const uint FanAbility = 202;

        /// <summary>Ordinary monster strike ability ID。</summary>
        internal const uint MonsterAbility = 203;

        /// <summary>Boss slam ability ID。</summary>
        internal const uint BossAbility = 204;

        /// <summary>验证 actor archetype 是否属于 current production mapping。</summary>
        internal static bool IsKnownArchetype(uint value)
        {
            return value == PlayerArchetype ||
                   value == MonsterArchetype ||
                   value == BossArchetype ||
                   value == FanProjectileArchetype;
        }

        /// <summary>验证 weapon 是否属于 current production mapping。</summary>
        internal static bool IsKnownWeapon(uint value)
        {
            return value == SwordWeapon || value == FanWeapon;
        }

        /// <summary>验证 ability 是否属于 current production mapping。</summary>
        internal static bool IsKnownAbility(uint value)
        {
            return value == SwordAbility ||
                   value == FanAbility ||
                   value == MonsterAbility ||
                   value == BossAbility;
        }

        /// <summary>验证 player/non-player 的 weapon presence invariant。</summary>
        internal static bool WeaponMatchesArchetype(uint archetypeID, uint weaponID)
        {
            return archetypeID == PlayerArchetype
                ? IsKnownWeapon(weaponID)
                : weaponID == 0;
        }
    }

    /// <summary>
    /// 保存 Scene Input host 在主线程采样的 semantic input，不包含 authority 字段。
    /// </summary>
    internal readonly struct ClientBattleSemanticInput
    {
        /// <summary>
        /// 创建已量化且不含 actor/transform/hit/damage 的 semantic sample。
        /// </summary>
        internal ClientBattleSemanticInput(
            int moveXMilli,
            int moveYMilli,
            int aimYawMillidegrees,
            int aimPitchMillidegrees,
            bool jumpPressed,
            bool primaryPressed,
            bool secondaryPressed,
            bool interactPressed,
            uint interactionSlot,
            bool switchWeaponPressed = false)
        {
            if (moveXMilli < -1000 ||
                moveXMilli > 1000 ||
                moveYMilli < -1000 ||
                moveYMilli > 1000 ||
                aimPitchMillidegrees < -90000 ||
                aimPitchMillidegrees > 90000 ||
                interactionSlot > 16 ||
                (interactPressed && interactionSlot == 0))
            {
                throw new ArgumentOutOfRangeException(
                    nameof(moveXMilli),
                    "Semantic input violates frozen quantization bounds.");
            }

            MoveXMilli = moveXMilli;
            MoveYMilli = moveYMilli;
            AimYawMillidegrees = NormalizeYaw(aimYawMillidegrees);
            AimPitchMillidegrees = aimPitchMillidegrees;
            JumpPressed = jumpPressed;
            PrimaryPressed = primaryPressed;
            SecondaryPressed = secondaryPressed;
            InteractPressed = interactPressed;
            InteractionSlot = interactionSlot;
            SwitchWeaponPressed = switchWeaponPressed;
        }

        /// <summary>获取量化水平移动轴。</summary>
        internal int MoveXMilli { get; }

        /// <summary>获取量化纵向移动轴。</summary>
        internal int MoveYMilli { get; }

        /// <summary>获取规范化 yaw，范围为 [-180000, 180000)。</summary>
        internal int AimYawMillidegrees { get; }

        /// <summary>获取量化 pitch，范围为 [-90000, 90000]。</summary>
        internal int AimPitchMillidegrees { get; }

        /// <summary>获取本次 sample 是否包含 jump edge。</summary>
        internal bool JumpPressed { get; }

        /// <summary>获取本次 sample 是否包含 primary edge。</summary>
        internal bool PrimaryPressed { get; }

        /// <summary>获取本次 sample 是否包含 secondary edge。</summary>
        internal bool SecondaryPressed { get; }

        /// <summary>获取本次 sample 是否包含 interact edge。</summary>
        internal bool InteractPressed { get; }

        /// <summary>获取受限交互槽位，而不是 entity identity。</summary>
        internal uint InteractionSlot { get; }

        /// <summary>获取本次 sample 是否包含 switch-weapon edge。</summary>
        internal bool SwitchWeaponPressed { get; }

        /// <summary>
        /// 清除离散 edge，保留可连续采样的 move/aim。
        /// </summary>
        /// <returns>下一 InputTick 可复用的 continuous sample。</returns>
        internal ClientBattleSemanticInput WithoutEdges()
        {
            return new ClientBattleSemanticInput(
                MoveXMilli,
                MoveYMilli,
                AimYawMillidegrees,
                AimPitchMillidegrees,
                jumpPressed: false,
                primaryPressed: false,
                secondaryPressed: false,
                interactPressed: false,
                InteractionSlot,
                switchWeaponPressed: false);
        }

        /// <summary>
        /// 把任意 yaw 规范化为单一 closed 表示。
        /// </summary>
        private static int NormalizeYaw(int value)
        {
            var normalized = value % 360000;
            if (normalized >= 180000)
            {
                normalized -= 360000;
            }
            else if (normalized < -180000)
            {
                normalized += 360000;
            }

            return normalized;
        }
    }

    /// <summary>
    /// 保存一个 InputTick 内的单条封闭 command。
    /// </summary>
    internal sealed class ClientBattleInputCommand
    {
        /// <summary>
        /// 创建已验证且不可变的 semantic command。
        /// </summary>
        internal ClientBattleInputCommand(
            ulong inputTick,
            ulong simulationTick,
            ulong commandSequence,
            ClientBattleInputKind kind,
            int moveXMilli,
            int moveYMilli,
            int aimYawMillidegrees,
            int aimPitchMillidegrees,
            uint interactionSlot)
        {
            if (inputTick == 0 ||
                simulationTick == 0 ||
                commandSequence == 0 ||
                !Enum.IsDefined(typeof(ClientBattleInputKind), kind) ||
                moveXMilli < -1000 ||
                moveXMilli > 1000 ||
                moveYMilli < -1000 ||
                moveYMilli > 1000 ||
                aimYawMillidegrees < -180000 ||
                aimYawMillidegrees >= 180000 ||
                aimPitchMillidegrees < -90000 ||
                aimPitchMillidegrees > 90000 ||
                interactionSlot > 16 ||
                !ParametersMatchKind(
                    kind,
                    moveXMilli,
                    moveYMilli,
                    aimYawMillidegrees,
                    aimPitchMillidegrees,
                    interactionSlot))
            {
                throw new ArgumentException(
                    "Client battle input command violates the closed semantic contract.");
            }

            InputTick = inputTick;
            SimulationTick = simulationTick;
            CommandSequence = commandSequence;
            Kind = kind;
            MoveXMilli = moveXMilli;
            MoveYMilli = moveYMilli;
            AimYawMillidegrees = aimYawMillidegrees;
            AimPitchMillidegrees = aimPitchMillidegrees;
            InteractionSlot = interactionSlot;
        }

        /// <summary>获取 one-based InputTick。</summary>
        internal ulong InputTick { get; }

        /// <summary>获取由 policy 映射的 one-based SimulationTick。</summary>
        internal ulong SimulationTick { get; }

        /// <summary>获取 battle generation 内单调 command sequence。</summary>
        internal ulong CommandSequence { get; }

        /// <summary>获取封闭 semantic command kind。</summary>
        internal ClientBattleInputKind Kind { get; }

        /// <summary>获取 Move command 的 X 轴。</summary>
        internal int MoveXMilli { get; }

        /// <summary>获取 Move command 的 Y 轴。</summary>
        internal int MoveYMilli { get; }

        /// <summary>获取 Aim command 的 yaw。</summary>
        internal int AimYawMillidegrees { get; }

        /// <summary>获取 Aim command 的 pitch。</summary>
        internal int AimPitchMillidegrees { get; }

        /// <summary>获取 Interact command 的受限槽位。</summary>
        internal uint InteractionSlot { get; }

        /// <summary>
        /// 验证每种 command 未携带不属于自身的 authority-free 参数。
        /// </summary>
        private static bool ParametersMatchKind(
            ClientBattleInputKind kind,
            int moveX,
            int moveY,
            int aimYaw,
            int aimPitch,
            uint interactionSlot)
        {
            switch (kind)
            {
                case ClientBattleInputKind.Move:
                    return aimYaw == 0 && aimPitch == 0 && interactionSlot == 0;
                case ClientBattleInputKind.Aim:
                    return moveX == 0 && moveY == 0 && interactionSlot == 0;
                case ClientBattleInputKind.Interact:
                    return moveX == 0 &&
                           moveY == 0 &&
                           aimYaw == 0 &&
                           aimPitch == 0 &&
                           interactionSlot > 0;
                case ClientBattleInputKind.Jump:
                case ClientBattleInputKind.PrimaryAbility:
                case ClientBattleInputKind.SecondaryAbility:
                case ClientBattleInputKind.SwitchWeapon:
                    return moveX == 0 &&
                           moveY == 0 &&
                           aimYaw == 0 &&
                           aimPitch == 0 &&
                           interactionSlot == 0;
                default:
                    return false;
            }
        }
    }

    /// <summary>
    /// 保存单个 InputTick 的不可变 command 集合。
    /// </summary>
    internal sealed class ClientBattleInputFrame
    {
        /// <summary>
        /// 创建按 command sequence 排序的有界 frame。
        /// </summary>
        internal ClientBattleInputFrame(
            ulong inputTick,
            ulong simulationTick,
            IReadOnlyList<ClientBattleInputCommand> commands)
        {
            if (inputTick == 0 || simulationTick == 0 || commands == null)
            {
                throw new ArgumentException("Client battle input frame is invalid.");
            }

            var copy = new ClientBattleInputCommand[commands.Count];
            if (copy.Length == 0 || copy.Length > 7)
            {
                throw new ArgumentException(
                    "Client battle input frame command count is out of bounds.");
            }

            ulong previous = 0;
            for (var index = 0; index < copy.Length; index++)
            {
                var command = commands[index] ??
                    throw new ArgumentNullException(nameof(commands));
                if (command.InputTick != inputTick ||
                    command.SimulationTick != simulationTick ||
                    command.CommandSequence <= previous)
                {
                    throw new ArgumentException(
                        "Client battle input frame ordering or tick binding is invalid.");
                }

                previous = command.CommandSequence;
                copy[index] = command;
            }

            InputTick = inputTick;
            SimulationTick = simulationTick;
            Commands = new ReadOnlyCollection<ClientBattleInputCommand>(copy);
        }

        /// <summary>获取本 frame 的 InputTick。</summary>
        internal ulong InputTick { get; }

        /// <summary>获取本 frame 对应 SimulationTick。</summary>
        internal ulong SimulationTick { get; }

        /// <summary>获取有界、已排序 command 集合。</summary>
        internal IReadOnlyList<ClientBattleInputCommand> Commands { get; }
    }

    /// <summary>
    /// 保存 raw route 3000 要编码的有限冗余 input bundle。
    /// </summary>
    internal sealed class ClientBattleInputBundle
    {
        /// <summary>
        /// 创建按 InputTick 升序排列且深度不超过三的 bundle。
        /// </summary>
        internal ClientBattleInputBundle(
            ulong newestInputTick,
            ulong latestObservedServerTick,
            IReadOnlyList<ClientBattleInputFrame> frames)
        {
            if (newestInputTick == 0 || frames == null)
            {
                throw new ArgumentException("Client battle input bundle is invalid.");
            }

            var copy = new ClientBattleInputFrame[frames.Count];
            if (copy.Length == 0 ||
                copy.Length > ClientBattlePolicy.Current.InputBundleDepth)
            {
                throw new ArgumentException(
                    "Client battle input bundle depth is out of bounds.");
            }

            ulong previous = 0;
            var commandCount = 0;
            for (var index = 0; index < copy.Length; index++)
            {
                var frame = frames[index] ??
                    throw new ArgumentNullException(nameof(frames));
                if (frame.InputTick <= previous ||
                    (index > 0 && frame.InputTick != previous + 1))
                {
                    throw new ArgumentException(
                        "Client battle input bundle ticks are not contiguous.");
                }

                previous = frame.InputTick;
                commandCount = checked(commandCount + frame.Commands.Count);
                copy[index] = frame;
            }

            if (copy[copy.Length - 1].InputTick != newestInputTick ||
                commandCount > 8)
            {
                throw new ArgumentException(
                    "Client battle input bundle frontier is inconsistent.");
            }

            NewestInputTick = newestInputTick;
            LatestObservedServerTick = latestObservedServerTick;
            Frames = new ReadOnlyCollection<ClientBattleInputFrame>(copy);
        }

        /// <summary>获取 bundle 最新 InputTick。</summary>
        internal ulong NewestInputTick { get; }

        /// <summary>获取客户端已原子应用的最新 server Tick。</summary>
        internal ulong LatestObservedServerTick { get; }

        /// <summary>获取按 tick 升序排列的有限冗余 frame。</summary>
        internal IReadOnlyList<ClientBattleInputFrame> Frames { get; }
    }

    /// <summary>
    /// 保存与平台无关的量化 transform。
    /// </summary>
    internal readonly struct ClientBattleTransform
    {
        /// <summary>
        /// 创建权威或预测的量化 transform。
        /// </summary>
        internal ClientBattleTransform(
            int positionXMillimeters,
            int positionYMillimeters,
            int positionZMillimeters,
            int yawMillidegrees,
            int velocityXMillimetersPerSecond,
            int velocityYMillimetersPerSecond,
            int velocityZMillimetersPerSecond)
        {
            PositionXMillimeters = positionXMillimeters;
            PositionYMillimeters = positionYMillimeters;
            PositionZMillimeters = positionZMillimeters;
            YawMillidegrees = NormalizeYaw(yawMillidegrees);
            VelocityXMillimetersPerSecond = velocityXMillimetersPerSecond;
            VelocityYMillimetersPerSecond = velocityYMillimetersPerSecond;
            VelocityZMillimetersPerSecond = velocityZMillimetersPerSecond;
        }

        /// <summary>获取 world X 位置，单位毫米。</summary>
        internal int PositionXMillimeters { get; }

        /// <summary>获取 world Y 位置，单位毫米。</summary>
        internal int PositionYMillimeters { get; }

        /// <summary>获取 world Z 位置，单位毫米。</summary>
        internal int PositionZMillimeters { get; }

        /// <summary>获取规范化 yaw，单位 millidegree。</summary>
        internal int YawMillidegrees { get; }

        /// <summary>获取 world X 速度，单位 mm/s。</summary>
        internal int VelocityXMillimetersPerSecond { get; }

        /// <summary>获取 world Y 速度，单位 mm/s。</summary>
        internal int VelocityYMillimetersPerSecond { get; }

        /// <summary>获取 world Z 速度，单位 mm/s。</summary>
        internal int VelocityZMillimetersPerSecond { get; }

        /// <summary>
        /// 计算位置平方距离，使用 64-bit 避免常规世界范围溢出。
        /// </summary>
        internal long PositionDistanceSquared(ClientBattleTransform other)
        {
            var x = (long)PositionXMillimeters - other.PositionXMillimeters;
            var y = (long)PositionYMillimeters - other.PositionYMillimeters;
            var z = (long)PositionZMillimeters - other.PositionZMillimeters;
            return checked((x * x) + (y * y) + (z * z));
        }

        /// <summary>
        /// 计算两次 yaw 的最短绝对差。
        /// </summary>
        internal int YawDistance(ClientBattleTransform other)
        {
            var difference = Math.Abs(YawMillidegrees - other.YawMillidegrees);
            return difference > 180000 ? 360000 - difference : difference;
        }

        /// <summary>
        /// 把 yaw 规范化为唯一范围。
        /// </summary>
        private static int NormalizeYaw(int value)
        {
            var normalized = value % 360000;
            if (normalized >= 180000)
            {
                normalized -= 360000;
            }
            else if (normalized < -180000)
            {
                normalized += 360000;
            }

            return normalized;
        }
    }

    /// <summary>
    /// 保存一个 entity generation 的完整权威公开状态。
    /// </summary>
    internal sealed class ClientBattleEntityState
    {
        /// <summary>
        /// 创建完整且不含 Unity object 的 entity state。
        /// </summary>
        internal ClientBattleEntityState(
            ulong entityID,
            uint generation,
            ClientBattleTransform transform,
            uint healthMilli,
            uint stateFlags)
            : this(
                entityID,
                generation,
                transform,
                healthMilli,
                stateFlags,
                ClientBattleContentIdentity.PlayerArchetype,
                ClientBattleContentIdentity.SwordWeapon,
                Math.Max(healthMilli, 1U))
        {
        }

        /// <summary>
        /// 创建包含 production content identity 与 health invariant 的完整 state。
        /// </summary>
        internal ClientBattleEntityState(
            ulong entityID,
            uint generation,
            ClientBattleTransform transform,
            uint healthMilli,
            uint stateFlags,
            uint archetypeID,
            uint equippedWeaponID,
            uint maxHealthMilli)
        {
            if (entityID == 0 ||
                generation == 0 ||
                (stateFlags & ~KnownStateFlags) != 0 ||
                !ClientBattleContentIdentity.IsKnownArchetype(archetypeID) ||
                !ClientBattleContentIdentity.WeaponMatchesArchetype(
                    archetypeID,
                    equippedWeaponID) ||
                maxHealthMilli == 0 ||
                healthMilli > maxHealthMilli)
            {
                throw new ArgumentException("Client battle entity state is invalid.");
            }

            EntityID = entityID;
            Generation = generation;
            Transform = transform;
            HealthMilli = healthMilli;
            StateFlags = stateFlags;
            ArchetypeID = archetypeID;
            EquippedWeaponID = equippedWeaponID;
            MaxHealthMilli = maxHealthMilli;
        }

        /// <summary>获取 current producer 用于 ability/AI/Boss phase token 的低位范围。</summary>
        internal const uint PhaseStateMask = 0x0000000f;

        /// <summary>获取 authority Movement/Physics producer 使用的接地标识。</summary>
        internal const uint GroundedStateFlag = 0x00000010;

        /// <summary>获取 current producer 用于已死亡 entity 的高位标识。</summary>
        internal const uint DeadStateFlag = 0x80000000;

        /// <summary>获取 wire v1 current producer 实际允许的 state flag mask。</summary>
        internal const uint KnownStateFlags =
            PhaseStateMask | GroundedStateFlag | DeadStateFlag;

        /// <summary>获取 instance 内非零 entity identity。</summary>
        internal ulong EntityID { get; }

        /// <summary>获取防止 identity 复活的 entity generation。</summary>
        internal uint Generation { get; }

        /// <summary>获取权威量化 transform。</summary>
        internal ClientBattleTransform Transform { get; }

        /// <summary>获取权威量化生命值。</summary>
        internal uint HealthMilli { get; }

        /// <summary>获取已通过 allowlist 的状态 bit set。</summary>
        internal uint StateFlags { get; }

        /// <summary>获取 current generation 的 production actor archetype。</summary>
        internal uint ArchetypeID { get; }

        /// <summary>获取 player 当前 weapon；非 player 为零。</summary>
        internal uint EquippedWeaponID { get; }

        /// <summary>获取 current generation 的不可变正数生命上限。</summary>
        internal uint MaxHealthMilli { get; }

        /// <summary>获取 snapshot 明确携带的 authority 接地事实。</summary>
        internal bool Grounded =>
            (StateFlags & GroundedStateFlag) != 0;

        /// <summary>
        /// 返回替换指定字段后的新 immutable state。
        /// </summary>
        internal ClientBattleEntityState With(
            ClientBattleTransform? transform,
            uint? healthMilli,
            uint? stateFlags,
            uint? equippedWeaponID = null)
        {
            return new ClientBattleEntityState(
                EntityID,
                Generation,
                transform ?? Transform,
                healthMilli ?? HealthMilli,
                stateFlags ?? StateFlags,
                ArchetypeID,
                equippedWeaponID ?? EquippedWeaponID,
                MaxHealthMilli);
        }
    }

    /// <summary>
    /// 保存相对已确认 baseline 的单个 entity delta。
    /// </summary>
    internal sealed class ClientBattleEntityDelta
    {
        /// <summary>声明 transform 显式存在。</summary>
        internal const uint TransformMask = 1;

        /// <summary>声明 health 显式存在。</summary>
        internal const uint HealthMask = 2;

        /// <summary>声明 state flags 显式存在。</summary>
        internal const uint StateFlagsMask = 4;

        /// <summary>声明 equipped weapon 显式存在。</summary>
        internal const uint EquippedWeaponMask = 8;

        /// <summary>
        /// 创建已验证 presence/mask 一致的 delta。
        /// </summary>
        internal ClientBattleEntityDelta(
            ulong entityID,
            uint generation,
            uint stateMask,
            ClientBattleTransform? transform,
            uint? healthMilli,
            uint? stateFlags)
            : this(
                entityID,
                generation,
                stateMask,
                transform,
                healthMilli,
                stateFlags,
                null)
        {
        }

        /// <summary>
        /// 创建包含 weapon presence 的已验证 delta。
        /// </summary>
        internal ClientBattleEntityDelta(
            ulong entityID,
            uint generation,
            uint stateMask,
            ClientBattleTransform? transform,
            uint? healthMilli,
            uint? stateFlags,
            uint? equippedWeaponID)
        {
            if (entityID == 0 ||
                generation == 0 ||
                stateMask == 0 ||
                (stateMask & ~(
                    TransformMask |
                    HealthMask |
                    StateFlagsMask |
                    EquippedWeaponMask)) != 0 ||
                ((stateMask & TransformMask) != 0) != transform.HasValue ||
                ((stateMask & HealthMask) != 0) != healthMilli.HasValue ||
                ((stateMask & StateFlagsMask) != 0) != stateFlags.HasValue ||
                ((stateMask & EquippedWeaponMask) != 0) != equippedWeaponID.HasValue ||
                (stateFlags.HasValue &&
                 (stateFlags.Value & ~ClientBattleEntityState.KnownStateFlags) != 0) ||
                (equippedWeaponID.HasValue &&
                 equippedWeaponID.Value != 0 &&
                 !ClientBattleContentIdentity.IsKnownWeapon(equippedWeaponID.Value)))
            {
                throw new ArgumentException(
                    "Client battle entity delta presence is invalid.");
            }

            EntityID = entityID;
            Generation = generation;
            StateMask = stateMask;
            Transform = transform;
            HealthMilli = healthMilli;
            StateFlags = stateFlags;
            EquippedWeaponID = equippedWeaponID;
        }

        /// <summary>获取 entity identity。</summary>
        internal ulong EntityID { get; }

        /// <summary>获取必须精确匹配的 entity generation。</summary>
        internal uint Generation { get; }

        /// <summary>获取显式字段 mask。</summary>
        internal uint StateMask { get; }

        /// <summary>获取可选完整 transform。</summary>
        internal ClientBattleTransform? Transform { get; }

        /// <summary>获取可选 health。</summary>
        internal uint? HealthMilli { get; }

        /// <summary>获取可选 state flags。</summary>
        internal uint? StateFlags { get; }

        /// <summary>获取可选 player weapon 完整替换值。</summary>
        internal uint? EquippedWeaponID { get; }
    }

    /// <summary>
    /// 标识 snapshot 是 full baseline 或 delta。
    /// </summary>
    internal enum ClientBattleSnapshotKind
    {
        /// <summary>建立完整 baseline。</summary>
        Full = 1,

        /// <summary>引用已确认 baseline。</summary>
        Delta = 2,
    }

    /// <summary>
    /// 保存 codec 已验证的单个 snapshot partition。
    /// </summary>
    internal sealed class ClientBattleSnapshotPartition
    {
        /// <summary>
        /// 创建与 route metadata 和 ack 绑定的 immutable partition。
        /// </summary>
        internal ClientBattleSnapshotPartition(
            long battleGeneration,
            ClientBattleSnapshotKind kind,
            ulong serverTick,
            ulong snapshotSequence,
            ulong baselineID,
            int partitionIndex,
            int partitionCount,
            ulong lastProcessedInputTick,
            IReadOnlyList<ClientBattleEntityState> entities,
            IReadOnlyList<ClientBattleEntityDelta> deltas)
        {
            if (battleGeneration <= 0 ||
                !Enum.IsDefined(typeof(ClientBattleSnapshotKind), kind) ||
                serverTick == 0 ||
                snapshotSequence == 0 ||
                baselineID == 0 ||
                partitionIndex < 0 ||
                partitionCount <= 0 ||
                partitionIndex >= partitionCount ||
                partitionCount > ClientBattlePolicy.Current.MaximumSnapshotPartitions ||
                entities == null ||
                deltas == null ||
                (kind == ClientBattleSnapshotKind.Full &&
                 (entities.Count == 0 || deltas.Count != 0)) ||
                (kind == ClientBattleSnapshotKind.Delta &&
                 (entities.Count != 0 || deltas.Count == 0)))
            {
                throw new ArgumentException("Client battle snapshot partition is invalid.");
            }

            BattleGeneration = battleGeneration;
            Kind = kind;
            ServerTick = serverTick;
            SnapshotSequence = snapshotSequence;
            BaselineID = baselineID;
            PartitionIndex = partitionIndex;
            PartitionCount = partitionCount;
            LastProcessedInputTick = lastProcessedInputTick;
            Entities = CopySortedEntities(entities);
            Deltas = CopySortedDeltas(deltas);
        }

        /// <summary>获取来源 battle generation。</summary>
        internal long BattleGeneration { get; }

        /// <summary>获取 full/delta kind。</summary>
        internal ClientBattleSnapshotKind Kind { get; }

        /// <summary>获取一致 authority server Tick。</summary>
        internal ulong ServerTick { get; }

        /// <summary>获取 snapshot sequence。</summary>
        internal ulong SnapshotSequence { get; }

        /// <summary>获取建立或引用的 baseline identity。</summary>
        internal ulong BaselineID { get; }

        /// <summary>获取零基 partition index。</summary>
        internal int PartitionIndex { get; }

        /// <summary>获取完整 partition count。</summary>
        internal int PartitionCount { get; }

        /// <summary>获取 field 7 显式解码的 input acknowledgement。</summary>
        internal ulong LastProcessedInputTick { get; }

        /// <summary>获取 full partition 内已排序 entity state。</summary>
        internal IReadOnlyList<ClientBattleEntityState> Entities { get; }

        /// <summary>获取 delta partition 内已排序 entity delta。</summary>
        internal IReadOnlyList<ClientBattleEntityDelta> Deltas { get; }

        /// <summary>
        /// 复制并验证完整 entity 排序与唯一性。
        /// </summary>
        private static IReadOnlyList<ClientBattleEntityState> CopySortedEntities(
            IReadOnlyList<ClientBattleEntityState> source)
        {
            var copy = new ClientBattleEntityState[source.Count];
            ulong previousID = 0;
            uint previousGeneration = 0;
            for (var index = 0; index < copy.Length; index++)
            {
                var entity = source[index] ??
                    throw new ArgumentNullException(nameof(source));
                if (index > 0 &&
                    (entity.EntityID < previousID ||
                     (entity.EntityID == previousID &&
                      entity.Generation <= previousGeneration)))
                {
                    throw new ArgumentException(
                        "Client battle entity partition is not strictly sorted.");
                }

                previousID = entity.EntityID;
                previousGeneration = entity.Generation;
                copy[index] = entity;
            }

            return new ReadOnlyCollection<ClientBattleEntityState>(copy);
        }

        /// <summary>
        /// 复制并验证 delta 排序与唯一性。
        /// </summary>
        private static IReadOnlyList<ClientBattleEntityDelta> CopySortedDeltas(
            IReadOnlyList<ClientBattleEntityDelta> source)
        {
            var copy = new ClientBattleEntityDelta[source.Count];
            ulong previousID = 0;
            uint previousGeneration = 0;
            for (var index = 0; index < copy.Length; index++)
            {
                var delta = source[index] ??
                    throw new ArgumentNullException(nameof(source));
                if (index > 0 &&
                    (delta.EntityID < previousID ||
                     (delta.EntityID == previousID &&
                      delta.Generation <= previousGeneration)))
                {
                    throw new ArgumentException(
                        "Client battle delta partition is not strictly sorted.");
                }

                previousID = delta.EntityID;
                previousGeneration = delta.Generation;
                copy[index] = delta;
            }

            return new ReadOnlyCollection<ClientBattleEntityDelta>(copy);
        }
    }

    /// <summary>
    /// 标识可靠 entity lifecycle 事件。
    /// </summary>
    internal enum ClientBattleEntityLifecycleKind
    {
        /// <summary>建立新 entity generation。</summary>
        Spawn = 1,

        /// <summary>终止 current entity generation。</summary>
        Despawn = 2,
    }

    /// <summary>
    /// 保存 route 3005 的可靠 entity lifecycle projection。
    /// </summary>
    internal sealed class ClientBattleEntityLifecycle
    {
        /// <summary>
        /// 创建已验证的 spawn/despawn event。
        /// </summary>
        internal ClientBattleEntityLifecycle(
            long battleGeneration,
            ulong eventID,
            ulong serverTick,
            ulong entityID,
            uint entityGeneration,
            ClientBattleEntityLifecycleKind kind,
            uint archetypeID,
            ClientBattleEntityState initialState)
        {
            if (battleGeneration <= 0 ||
                eventID == 0 ||
                serverTick == 0 ||
                entityID == 0 ||
                entityGeneration == 0 ||
                !Enum.IsDefined(typeof(ClientBattleEntityLifecycleKind), kind) ||
                (kind == ClientBattleEntityLifecycleKind.Spawn &&
                 (archetypeID == 0 ||
                  initialState == null ||
                  initialState.EntityID != entityID ||
                  initialState.Generation != entityGeneration ||
                  initialState.ArchetypeID != archetypeID)) ||
                (kind == ClientBattleEntityLifecycleKind.Despawn &&
                 (archetypeID != 0 || initialState != null)))
            {
                throw new ArgumentException(
                    "Client battle entity lifecycle is invalid.");
            }

            BattleGeneration = battleGeneration;
            EventID = eventID;
            ServerTick = serverTick;
            EntityID = entityID;
            EntityGeneration = entityGeneration;
            Kind = kind;
            ArchetypeID = archetypeID;
            InitialState = initialState;
        }

        /// <summary>获取来源 battle generation。</summary>
        internal long BattleGeneration { get; }

        /// <summary>获取 generation 内单调 event identity。</summary>
        internal ulong EventID { get; }

        /// <summary>获取 authority server Tick。</summary>
        internal ulong ServerTick { get; }

        /// <summary>获取 entity identity。</summary>
        internal ulong EntityID { get; }

        /// <summary>获取 lifecycle generation。</summary>
        internal uint EntityGeneration { get; }

        /// <summary>获取 spawn/despawn kind。</summary>
        internal ClientBattleEntityLifecycleKind Kind { get; }

        /// <summary>获取 spawn archetype identity。</summary>
        internal uint ArchetypeID { get; }

        /// <summary>获取仅 spawn 存在的 initial state。</summary>
        internal ClientBattleEntityState InitialState { get; }
    }

    /// <summary>
    /// 标识 ability 可靠事件阶段。
    /// </summary>
    internal enum ClientBattleAbilityPhase
    {
        /// <summary>Ability 已被 authority 接受。</summary>
        Started = 1,

        /// <summary>Ability 已跨过不可取消点。</summary>
        Committed = 2,

        /// <summary>Ability 已完成。</summary>
        Completed = 3,

        /// <summary>Ability 已取消。</summary>
        Cancelled = 4,
    }

    /// <summary>
    /// 保存 route 3004 的可靠 ability event。
    /// </summary>
    internal sealed class ClientBattleAbilityEvent
    {
        /// <summary>
        /// 创建已验证、已排序的 ability event。
        /// </summary>
        internal ClientBattleAbilityEvent(
            long battleGeneration,
            ulong eventID,
            ulong serverTick,
            ulong sourceEntityID,
            uint sourceEntityGeneration,
            uint abilityID,
            ClientBattleAbilityPhase phase,
            IReadOnlyList<ulong> targetEntityIDs)
        {
            if (battleGeneration <= 0 ||
                eventID == 0 ||
                serverTick == 0 ||
                sourceEntityID == 0 ||
                sourceEntityGeneration == 0 ||
                !ClientBattleContentIdentity.IsKnownAbility(abilityID) ||
                !Enum.IsDefined(typeof(ClientBattleAbilityPhase), phase) ||
                targetEntityIDs == null ||
                targetEntityIDs.Count > 16)
            {
                throw new ArgumentException("Client battle ability event is invalid.");
            }

            var copy = new ulong[targetEntityIDs.Count];
            ulong previous = 0;
            for (var index = 0; index < copy.Length; index++)
            {
                var current = targetEntityIDs[index];
                if (current == 0 || (index > 0 && current <= previous))
                {
                    throw new ArgumentException(
                        "Client battle ability targets are not strictly sorted.");
                }

                previous = current;
                copy[index] = current;
            }

            BattleGeneration = battleGeneration;
            EventID = eventID;
            ServerTick = serverTick;
            SourceEntityID = sourceEntityID;
            SourceEntityGeneration = sourceEntityGeneration;
            AbilityID = abilityID;
            Phase = phase;
            TargetEntityIDs = new ReadOnlyCollection<ulong>(copy);
        }

        /// <summary>获取来源 battle generation。</summary>
        internal long BattleGeneration { get; }

        /// <summary>获取 generation 内唯一 event identity。</summary>
        internal ulong EventID { get; }

        /// <summary>获取 authority server Tick。</summary>
        internal ulong ServerTick { get; }

        /// <summary>获取 ability source entity。</summary>
        internal ulong SourceEntityID { get; }

        /// <summary>获取 source entity generation。</summary>
        internal uint SourceEntityGeneration { get; }

        /// <summary>获取版本化 ability identity。</summary>
        internal uint AbilityID { get; }

        /// <summary>获取权威 ability phase。</summary>
        internal ClientBattleAbilityPhase Phase { get; }

        /// <summary>获取排序后的公开 target identities。</summary>
        internal IReadOnlyList<ulong> TargetEntityIDs { get; }
    }

    /// <summary>
    /// 标识客户端请求 baseline 恢复的封闭原因。
    /// </summary>
    internal enum ClientBattleResyncReason
    {
        /// <summary>从未收到引用的 baseline。</summary>
        MissingBaseline = 1,

        /// <summary>Current baseline 超过 40 Tick。</summary>
        BaselineExpired = 2,

        /// <summary>Snapshot sequence 出现不可恢复缺口。</summary>
        DeltaGap = 3,

        /// <summary>Entity lifecycle generation 不连续。</summary>
        EntityGenerationGap = 4,
    }

    /// <summary>
    /// 保存 single-flight route 3006 request 的纯 Application 投影。
    /// </summary>
    internal sealed class ClientBattleResyncRequest
    {
        /// <summary>
        /// 创建与 current generation/baseline 绑定的恢复请求。
        /// </summary>
        internal ClientBattleResyncRequest(
            long battleGeneration,
            ulong requestSequence,
            ulong latestServerTick,
            ulong missingBaselineID,
            ulong latestSnapshotSequence,
            ClientBattleResyncReason reason)
        {
            if (battleGeneration <= 0 ||
                requestSequence == 0 ||
                !Enum.IsDefined(typeof(ClientBattleResyncReason), reason))
            {
                throw new ArgumentException("Client battle resync request is invalid.");
            }

            BattleGeneration = battleGeneration;
            RequestSequence = requestSequence;
            LatestServerTick = latestServerTick;
            MissingBaselineID = missingBaselineID;
            LatestSnapshotSequence = latestSnapshotSequence;
            Reason = reason;
        }

        /// <summary>获取来源 battle generation。</summary>
        internal long BattleGeneration { get; }

        /// <summary>获取 generation 内单调 request sequence。</summary>
        internal ulong RequestSequence { get; }

        /// <summary>获取已应用的最新 server Tick。</summary>
        internal ulong LatestServerTick { get; }

        /// <summary>获取缺失 baseline identity；未知时为零。</summary>
        internal ulong MissingBaselineID { get; }

        /// <summary>获取已应用的最新 snapshot sequence。</summary>
        internal ulong LatestSnapshotSequence { get; }

        /// <summary>获取封闭恢复原因。</summary>
        internal ClientBattleResyncReason Reason { get; }
    }

    /// <summary>
    /// 定义 gameplay owner 使用的窄 battle payload command port。
    /// </summary>
    internal interface IClientBattleGameplayPort
    {
        /// <summary>
        /// 尝试把 current generation input bundle 加入 raw send queue。
        /// </summary>
        /// <param name="battleGeneration">必须匹配 current generation。</param>
        /// <param name="bundle">不含 authority 字段的 immutable bundle。</param>
        /// <returns>成功取得 send ownership 时为 true。</returns>
        bool TrySendInput(long battleGeneration, ClientBattleInputBundle bundle);

        /// <summary>
        /// 尝试把 current single-flight resync 请求加入 KCP queue。
        /// </summary>
        /// <param name="request">Current generation 的 immutable request。</param>
        /// <returns>成功取得 queue ownership 时为 true。</returns>
        bool TryRequestResync(ClientBattleResyncRequest request);
    }
}
