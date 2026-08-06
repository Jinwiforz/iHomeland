using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;

namespace IHomeland.Client.PersonalWorldCombat.Application
{
    /// <summary>
    /// 标识 battle 产品表现的低敏可用性。
    /// </summary>
    internal enum ClientBattleAvailability
    {
        /// <summary>尚未 activation。</summary>
        Inactive = 0,

        /// <summary>正在签发 ticket 或握手。</summary>
        Connecting = 1,

        /// <summary>已认证但等待完整 baseline。</summary>
        LoadingBaseline = 2,

        /// <summary>Current generation 可输入且 state 完整。</summary>
        Active = 3,

        /// <summary>World membership 仍在，但 battle transport 正恢复。</summary>
        Retrying = 4,

        /// <summary>Battle 暂不可用且未伪造 world leave。</summary>
        Unavailable = 5,
    }

    /// <summary>
    /// 标识 Cinemachine host 可消费的纯 presentation camera mode。
    /// </summary>
    internal enum ClientBattleCameraMode
    {
        /// <summary>默认探索跟随。</summary>
        Exploration = 1,

        /// <summary>近战构图候选。</summary>
        MeleeCombat = 2,

        /// <summary>远程瞄准构图候选。</summary>
        RangedAim = 3,

        /// <summary>短时只读 cinematic 构图候选。</summary>
        Cinematic = 4,
    }

    /// <summary>
    /// 保存一个 Scene actor view 的 immutable presentation state。
    /// </summary>
    internal sealed class ClientActorViewState
    {
        /// <summary>
        /// 创建 local prediction 或 remote interpolation 的只读投影。
        /// </summary>
        internal ClientActorViewState(
            long battleGeneration,
            ulong entityID,
            uint entityGeneration,
            bool local,
            ClientBattleTransform transform,
            uint healthMilli,
            uint stateFlags,
            bool degraded)
            : this(
                battleGeneration,
                entityID,
                entityGeneration,
                local,
                transform,
                healthMilli,
                Math.Max(healthMilli, 1U),
                stateFlags,
                ClientBattleContentIdentity.PlayerArchetype,
                ClientBattleContentIdentity.SwordWeapon,
                degraded)
        {
        }

        /// <summary>
        /// 创建包含 production content mapping 的完整只读表现投影。
        /// </summary>
        internal ClientActorViewState(
            long battleGeneration,
            ulong entityID,
            uint entityGeneration,
            bool local,
            ClientBattleTransform transform,
            uint healthMilli,
            uint maxHealthMilli,
            uint stateFlags,
            uint archetypeID,
            uint equippedWeaponID,
            bool degraded)
        {
            if (battleGeneration <= 0 ||
                entityID == 0 ||
                entityGeneration == 0 ||
                (stateFlags & ~ClientBattleEntityState.KnownStateFlags) != 0 ||
                !ClientBattleContentIdentity.IsKnownArchetype(archetypeID) ||
                !ClientBattleContentIdentity.WeaponMatchesArchetype(
                    archetypeID,
                    equippedWeaponID) ||
                maxHealthMilli == 0 ||
                healthMilli > maxHealthMilli)
            {
                throw new ArgumentException("Client actor view state is invalid.");
            }

            BattleGeneration = battleGeneration;
            EntityID = entityID;
            EntityGeneration = entityGeneration;
            Local = local;
            Transform = transform;
            HealthMilli = healthMilli;
            MaxHealthMilli = maxHealthMilli;
            StateFlags = stateFlags;
            ArchetypeID = archetypeID;
            EquippedWeaponID = equippedWeaponID;
            Degraded = degraded;
        }

        /// <summary>获取来源 battle generation。</summary>
        internal long BattleGeneration { get; }

        /// <summary>获取 entity identity。</summary>
        internal ulong EntityID { get; }

        /// <summary>获取 lifecycle generation。</summary>
        internal uint EntityGeneration { get; }

        /// <summary>获取是否为 actor_slot + 1 local entity。</summary>
        internal bool Local { get; }

        /// <summary>获取 local prediction 或 remote interpolation transform。</summary>
        internal ClientBattleTransform Transform { get; }

        /// <summary>获取始终来自 authority replica 的 health。</summary>
        internal uint HealthMilli { get; }

        /// <summary>获取 current generation 的权威生命上限。</summary>
        internal uint MaxHealthMilli { get; }

        /// <summary>获取始终来自 authority replica 的 state flags。</summary>
        internal uint StateFlags { get; }

        /// <summary>获取 production actor/projectile archetype mapping。</summary>
        internal uint ArchetypeID { get; }

        /// <summary>获取 player 当前 weapon mapping；非 player 为零。</summary>
        internal uint EquippedWeaponID { get; }

        /// <summary>获取量化水平速度是否表达实际 locomotion。</summary>
        /// <remarks>
        /// 该语义只来自 prediction 或 remote interpolation 的同一 immutable Transform，
        /// 不读取 Raw Input，也不以 Unity Transform 位移反推 gameplay 状态。
        /// </remarks>
        internal bool Moving =>
            Transform.VelocityXMillimetersPerSecond != 0 ||
            Transform.VelocityZMillimetersPerSecond != 0;

        /// <summary>获取权威 Ability/AI/Boss phase token。</summary>
        internal uint Phase =>
            StateFlags & ClientBattleEntityState.PhaseStateMask;

        /// <summary>获取 authority Death stage 是否已提交。</summary>
        internal bool Dead =>
            (StateFlags & ClientBattleEntityState.DeadStateFlag) != 0;

        /// <summary>获取 remote stale 或 battle unavailable 降级状态。</summary>
        internal bool Degraded { get; }
    }

    /// <summary>
    /// 保存 uGUI battle HUD 可读取的 immutable state。
    /// </summary>
    internal sealed class ClientBattleHudViewState
    {
        /// <summary>
        /// 创建不包含 endpoint、ticket、payload 或玩家 identity 的 HUD state。
        /// </summary>
        internal ClientBattleHudViewState(
            long battleGeneration,
            ClientBattleAvailability availability,
            uint localHealthMilli,
            bool correctionVisible,
            bool inputEnabled,
            bool degraded)
            : this(
                battleGeneration,
                availability,
                localHealthMilli,
                Math.Max(localHealthMilli, 1U),
                ClientBattleContentIdentity.SwordWeapon,
                0,
                0,
                0,
                bossDead: false,
                correctionVisible,
                inputEnabled,
                degraded)
        {
        }

        /// <summary>
        /// 创建包含 player weapon/health 与 Boss health/phase 的 HUD state。
        /// </summary>
        internal ClientBattleHudViewState(
            long battleGeneration,
            ClientBattleAvailability availability,
            uint localHealthMilli,
            uint localMaxHealthMilli,
            uint equippedWeaponID,
            uint bossHealthMilli,
            uint bossMaxHealthMilli,
            uint bossPhase,
            bool bossDead,
            bool correctionVisible,
            bool inputEnabled,
            bool degraded)
        {
            if (battleGeneration < 0 ||
                !Enum.IsDefined(typeof(ClientBattleAvailability), availability) ||
                localHealthMilli > localMaxHealthMilli ||
                (battleGeneration > 0 &&
                 (localMaxHealthMilli == 0 ||
                  !ClientBattleContentIdentity.IsKnownWeapon(equippedWeaponID))) ||
                bossHealthMilli > bossMaxHealthMilli ||
                bossPhase > ClientBattleEntityState.PhaseStateMask)
            {
                throw new ArgumentException("Client battle HUD state is invalid.");
            }

            BattleGeneration = battleGeneration;
            Availability = availability;
            LocalHealthMilli = localHealthMilli;
            LocalMaxHealthMilli = localMaxHealthMilli;
            EquippedWeaponID = equippedWeaponID;
            BossHealthMilli = bossHealthMilli;
            BossMaxHealthMilli = bossMaxHealthMilli;
            BossPhase = bossPhase;
            BossDead = bossDead;
            CorrectionVisible = correctionVisible;
            InputEnabled = inputEnabled;
            Degraded = degraded;
        }

        /// <summary>获取 current battle generation。</summary>
        internal long BattleGeneration { get; }

        /// <summary>获取当前低敏 availability。</summary>
        internal ClientBattleAvailability Availability { get; }

        /// <summary>获取 local actor authority health。</summary>
        internal uint LocalHealthMilli { get; }

        /// <summary>获取 local actor authority max health。</summary>
        internal uint LocalMaxHealthMilli { get; }

        /// <summary>获取 local actor authority equipped weapon。</summary>
        internal uint EquippedWeaponID { get; }

        /// <summary>获取 Boss authority health；Boss 不可见时为零。</summary>
        internal uint BossHealthMilli { get; }

        /// <summary>获取 Boss authority max health；Boss 不可见时为零。</summary>
        internal uint BossMaxHealthMilli { get; }

        /// <summary>获取 Boss authority phase token。</summary>
        internal uint BossPhase { get; }

        /// <summary>获取 Boss 是否已由 authority Death stage 终结。</summary>
        internal bool BossDead { get; }

        /// <summary>获取 presentation smoothing/hard correction 指示。</summary>
        internal bool CorrectionVisible { get; }

        /// <summary>获取 semantic input host 是否应启用。</summary>
        internal bool InputEnabled { get; }

        /// <summary>获取 transport/interpolation 是否处于降级状态。</summary>
        internal bool Degraded { get; }
    }

    /// <summary>
    /// 保存一次可靠 ability event 派生的表现 cue。
    /// </summary>
    internal sealed class ClientGameplayCue
    {
        /// <summary>
        /// 创建由 generation + event identity 唯一标识的 cue。
        /// </summary>
        internal ClientGameplayCue(
            long battleGeneration,
            ulong eventID,
            ulong sourceEntityID,
            uint abilityID,
            ClientBattleAbilityPhase phase)
            : this(
                battleGeneration,
                eventID,
                sourceEntityID,
                1,
                abilityID,
                phase,
                Array.Empty<ulong>())
        {
        }

        /// <summary>
        /// 创建带 exact source generation 与规范 target 集的 immutable cue。
        /// </summary>
        internal ClientGameplayCue(
            long battleGeneration,
            ulong eventID,
            ulong sourceEntityID,
            uint sourceEntityGeneration,
            uint abilityID,
            ClientBattleAbilityPhase phase,
            IReadOnlyList<ulong> targetEntityIDs)
        {
            if (battleGeneration <= 0 ||
                eventID == 0 ||
                sourceEntityID == 0 ||
                sourceEntityGeneration == 0 ||
                !ClientBattleContentIdentity.IsKnownAbility(abilityID) ||
                !Enum.IsDefined(typeof(ClientBattleAbilityPhase), phase) ||
                targetEntityIDs == null)
            {
                throw new ArgumentException("Client gameplay cue is invalid.");
            }

            BattleGeneration = battleGeneration;
            EventID = eventID;
            SourceEntityID = sourceEntityID;
            SourceEntityGeneration = sourceEntityGeneration;
            AbilityID = abilityID;
            Phase = phase;
            TargetEntityIDs = targetEntityIDs;
        }

        /// <summary>获取来源 battle generation。</summary>
        internal long BattleGeneration { get; }

        /// <summary>获取 reliable event identity。</summary>
        internal ulong EventID { get; }

        /// <summary>获取 cue source entity。</summary>
        internal ulong SourceEntityID { get; }

        /// <summary>获取 cue source 的 exact entity generation。</summary>
        internal uint SourceEntityGeneration { get; }

        /// <summary>获取版本化 ability identity。</summary>
        internal uint AbilityID { get; }

        /// <summary>获取权威 ability phase。</summary>
        internal ClientBattleAbilityPhase Phase { get; }

        /// <summary>获取 authority event 的规范 target 集。</summary>
        internal IReadOnlyList<ulong> TargetEntityIDs { get; }
    }

    /// <summary>
    /// 保存 Camera host 可读取的纯 presentation intent。
    /// </summary>
    internal sealed class ClientBattleCameraIntent
    {
        /// <summary>
        /// 创建不参与 aim/hit/gameplay authority 的 camera intent。
        /// </summary>
        internal ClientBattleCameraIntent(
            long battleGeneration,
            ClientBattleCameraMode mode,
            ulong followEntityID,
            ulong impulseEventID)
        {
            if (battleGeneration < 0 ||
                !Enum.IsDefined(typeof(ClientBattleCameraMode), mode) ||
                (battleGeneration == 0 && followEntityID != 0))
            {
                throw new ArgumentException("Client battle camera intent is invalid.");
            }

            BattleGeneration = battleGeneration;
            Mode = mode;
            FollowEntityID = followEntityID;
            ImpulseEventID = impulseEventID;
        }

        /// <summary>获取 current battle generation。</summary>
        internal long BattleGeneration { get; }

        /// <summary>获取 camera rig mode。</summary>
        internal ClientBattleCameraMode Mode { get; }

        /// <summary>获取只用于表现跟随的 entity identity。</summary>
        internal ulong FollowEntityID { get; }

        /// <summary>获取可选一次性 impulse event identity。</summary>
        internal ulong ImpulseEventID { get; }
    }

    /// <summary>
    /// 保存 projector 单次计算得到的完整 immutable Scene state。
    /// </summary>
    internal sealed class ClientGameplayPresentationState
    {
        /// <summary>
        /// 创建 actor、HUD、cue 与 camera 的一致投影。
        /// </summary>
        internal ClientGameplayPresentationState(
            IReadOnlyList<ClientActorViewState> actors,
            ClientBattleHudViewState hud,
            IReadOnlyList<ClientGameplayCue> cues,
            ClientBattleCameraIntent camera)
        {
            Actors = actors ?? throw new ArgumentNullException(nameof(actors));
            Hud = hud ?? throw new ArgumentNullException(nameof(hud));
            Cues = cues ?? throw new ArgumentNullException(nameof(cues));
            Camera = camera ?? throw new ArgumentNullException(nameof(camera));
        }

        /// <summary>获取按 entity identity 排序的 actor views。</summary>
        internal IReadOnlyList<ClientActorViewState> Actors { get; }

        /// <summary>获取唯一 battle HUD state。</summary>
        internal ClientBattleHudViewState Hud { get; }

        /// <summary>获取本次可靠 events 派生的有界 cues。</summary>
        internal IReadOnlyList<ClientGameplayCue> Cues { get; }

        /// <summary>获取唯一 camera intent。</summary>
        internal ClientBattleCameraIntent Camera { get; }
    }

    /// <summary>
    /// 从 replica、prediction 与 interpolation 值快照纯计算 Scene presentation state。
    /// </summary>
    /// <remarks>
    /// 本类型无字段、clock、network、Unity object 或 subscriber；相同输入必定产生相同输出。
    /// Reliable event 的 generation/event 去重由 GameplayReplica 在进入本函数前完成。
    /// </remarks>
    internal static class GameplayPresentationProjector
    {
        /// <summary>
        /// 派生 current generation 的 actor、HUD、cue 与 camera state。
        /// </summary>
        internal static ClientGameplayPresentationState Project(
            ClientGameplayReplicaSnapshot replica,
            ClientGameplayPredictionSnapshot prediction,
            IReadOnlyList<ClientBattleInterpolatedState> remoteStates,
            IReadOnlyList<ClientBattleAbilityEvent> abilityEvents,
            ClientBattleAvailability availability,
            ClientBattleCameraMode requestedCameraMode,
            bool correctionVisible)
        {
            if (replica == null ||
                prediction == null ||
                remoteStates == null ||
                abilityEvents == null ||
                !Enum.IsDefined(typeof(ClientBattleAvailability), availability) ||
                !Enum.IsDefined(typeof(ClientBattleCameraMode), requestedCameraMode) ||
                replica.BattleGeneration != prediction.BattleGeneration)
            {
                throw new ArgumentException(
                    "Gameplay presentation inputs do not share one generation.");
            }

            var remoteByID =
                new Dictionary<ulong, ClientBattleInterpolatedState>();
            for (var index = 0; index < remoteStates.Count; index++)
            {
                var remote = remoteStates[index] ??
                    throw new ArgumentNullException(nameof(remoteStates));
                if (remote.BattleGeneration != replica.BattleGeneration ||
                    remote.EntityID == replica.LocalEntityID ||
                    !remoteByID.TryAdd(remote.EntityID, remote))
                {
                    throw new ArgumentException(
                        "Remote presentation state binding is invalid.");
                }
            }

            var actors = new ClientActorViewState[replica.Entities.Count];
            var degraded = availability != ClientBattleAvailability.Active;
            uint localHealth = 0;
            uint localMaxHealth = 0;
            uint localWeapon = 0;
            uint bossHealth = 0;
            uint bossMaxHealth = 0;
            uint bossPhase = 0;
            var bossDead = false;
            for (var index = 0; index < replica.Entities.Count; index++)
            {
                var entity = replica.Entities[index];
                var local = entity.EntityID == replica.LocalEntityID;
                ClientBattleTransform transform;
                var actorDegraded = degraded;
                if (local)
                {
                    transform = prediction.PresentationTransform;
                    localHealth = entity.HealthMilli;
                    localMaxHealth = entity.MaxHealthMilli;
                    localWeapon = entity.EquippedWeaponID;
                }
                else if (remoteByID.TryGetValue(entity.EntityID, out var remote) &&
                         remote.EntityGeneration == entity.Generation)
                {
                    transform = remote.Transform;
                    actorDegraded |= remote.Stale;
                }
                else
                {
                    transform = entity.Transform;
                    actorDegraded = true;
                }

                actors[index] = new ClientActorViewState(
                    replica.BattleGeneration,
                    entity.EntityID,
                    entity.Generation,
                    local,
                    transform,
                    entity.HealthMilli,
                    entity.MaxHealthMilli,
                    entity.StateFlags,
                    entity.ArchetypeID,
                    entity.EquippedWeaponID,
                    actorDegraded);
                if (entity.ArchetypeID ==
                    ClientBattleContentIdentity.BossArchetype)
                {
                    bossHealth = entity.HealthMilli;
                    bossMaxHealth = entity.MaxHealthMilli;
                    bossPhase = entity.StateFlags &
                        ClientBattleEntityState.PhaseStateMask;
                    bossDead = (entity.StateFlags &
                        ClientBattleEntityState.DeadStateFlag) != 0;
                }
            }

            var cues = new ClientGameplayCue[abilityEvents.Count];
            ulong impulseEventID = 0;
            ulong previousEventID = 0;
            for (var index = 0; index < abilityEvents.Count; index++)
            {
                var ability = abilityEvents[index] ??
                    throw new ArgumentNullException(nameof(abilityEvents));
                if (ability.BattleGeneration != replica.BattleGeneration ||
                    ability.EventID <= previousEventID)
                {
                    throw new ArgumentException(
                        "Gameplay cue events are not current and ordered.");
                }

                previousEventID = ability.EventID;
                cues[index] = new ClientGameplayCue(
                    ability.BattleGeneration,
                    ability.EventID,
                    ability.SourceEntityID,
                    ability.SourceEntityGeneration,
                    ability.AbilityID,
                    ability.Phase,
                    ability.TargetEntityIDs);
                if (ability.Phase == ClientBattleAbilityPhase.Started)
                {
                    impulseEventID = ability.EventID;
                }
            }

            var inputEnabled =
                availability == ClientBattleAvailability.Active &&
                prediction.InputEnabled &&
                !prediction.BaselineRequired;
            var hud = new ClientBattleHudViewState(
                replica.BattleGeneration,
                availability,
                localHealth,
                localMaxHealth,
                localWeapon,
                bossHealth,
                bossMaxHealth,
                bossPhase,
                bossDead,
                correctionVisible,
                inputEnabled,
                degraded);
            var cameraMode = ClientBattleCameraMode.Exploration;
            if (availability == ClientBattleAvailability.Active)
            {
                cameraMode = requestedCameraMode ==
                    ClientBattleCameraMode.Cinematic
                    ? requestedCameraMode
                    : localWeapon == ClientBattleContentIdentity.FanWeapon
                        ? ClientBattleCameraMode.RangedAim
                        : ClientBattleCameraMode.MeleeCombat;
            }
            var camera = new ClientBattleCameraIntent(
                replica.BattleGeneration,
                cameraMode,
                replica.LocalEntityID,
                impulseEventID);
            return new ClientGameplayPresentationState(
                new ReadOnlyCollection<ClientActorViewState>(actors),
                hud,
                new ReadOnlyCollection<ClientGameplayCue>(cues),
                camera);
        }
    }
}
