using System;
using System.Collections.Generic;
using IHomeland.Client.PersonalWorldCombat.Application;
using Unity.Cinemachine;
using UnityEngine;

using UnityEngine.Scripting.APIUpdating;

namespace IHomeland.Client.PersonalWorldCombat.Runtime.Scenes
{
    /// <summary>
    /// 把纯 camera intent 映射到已在 Scene/Prefab 配置的 Cinemachine 3.1.7 rigs。
    /// </summary>
    /// <remarks>
    /// Host 只切换 rig GameObject 并更新 follow proxy；具体 Cinemachine components 与 blend
    /// 由 Unity 资产拥有，因此本组件不参与 aim、hit、collision 或 gameplay authority。
    /// </remarks>
    [DisallowMultipleComponent]
    [MovedFrom(true, "IHomeland.Client.Scenes.PersonalWorld", "IHomeland.Client.Runtime", null)]
    public sealed class CinemachineCameraHost : MonoBehaviour
    {
        /// <summary>保存 Exploration Cinemachine rig root。</summary>
        [SerializeField]
        private GameObject _explorationRig;

        /// <summary>保存 MeleeCombat Cinemachine rig root。</summary>
        [SerializeField]
        private GameObject _meleeRig;

        /// <summary>保存 RangedAim Cinemachine rig root。</summary>
        [SerializeField]
        private GameObject _rangedAimRig;

        /// <summary>保存 Cinematic Cinemachine rig root。</summary>
        [SerializeField]
        private GameObject _cinematicRig;

        /// <summary>保存所有 Cinemachine rigs 共同跟随的 Scene proxy。</summary>
        [SerializeField]
        private Transform _followProxy;

        /// <summary>保存只由 presentation event 触发的 Cinemachine impulse source。</summary>
        [SerializeField]
        private CinemachineImpulseSource _impulseSource;

        /// <summary>保存 current Scene generation。</summary>
        private long _sceneGeneration;

        /// <summary>保存最近一次已消费 impulse identity，避免重复表现。</summary>
        private ulong _lastImpulseEventID;

        /// <summary>保存 current intent 明确指定的 follow entity identity。</summary>
        private ulong _followEntityID;

        /// <summary>保存 current intent 选择的 camera mode。</summary>
        private ClientBattleCameraMode _mode;

        /// <summary>验证四个互异 rig 与 follow proxy 都属于 current Scene。</summary>
        internal void ValidateConfiguration()
        {
            if (_explorationRig == null ||
                _meleeRig == null ||
                _rangedAimRig == null ||
                _cinematicRig == null ||
                _followProxy == null ||
                _impulseSource == null)
            {
                throw new InvalidOperationException(
                    "CinemachineCameraHost 缺少 rig、FollowProxy 或 ImpulseSource 引用。");
            }

            var rigs = new HashSet<GameObject>
            {
                _explorationRig,
                _meleeRig,
                _rangedAimRig,
                _cinematicRig,
            };
            if (rigs.Count != 4 ||
                _followProxy.gameObject.scene != gameObject.scene ||
                _impulseSource.gameObject.scene != gameObject.scene)
            {
                throw new InvalidOperationException(
                    "CinemachineCameraHost rigs、FollowProxy 与 ImpulseSource 必须属于 current Scene。");
            }

            foreach (var rig in rigs)
            {
                if (rig.scene != gameObject.scene)
                {
                    throw new InvalidOperationException(
                        "CinemachineCameraHost rig 必须属于 current Scene。");
                }
            }
        }

        /// <summary>绑定 current Scene generation 并提交确定性 Exploration fallback。</summary>
        /// <param name="sceneGeneration">SceneLifetime 分配的正 generation。</param>
        internal void Bind(long sceneGeneration)
        {
            ValidateConfiguration();
            if (_sceneGeneration != 0 || sceneGeneration <= 0)
            {
                throw new InvalidOperationException(
                    "CinemachineCameraHost 不能重复绑定或绑定无效 generation。");
            }

            _sceneGeneration = sceneGeneration;
            _lastImpulseEventID = 0;
            _followEntityID = 0;
            _mode = ClientBattleCameraMode.Exploration;
            SetActiveRig(ClientBattleCameraMode.Exploration);
        }

        /// <summary>
        /// 应用 current camera intent；actor 缺失时保留 Exploration fallback 与 proxy 位置。
        /// </summary>
        /// <param name="intent">Application 派生的纯表现 camera intent。</param>
        /// <param name="actors">Current Scene actor view registry。</param>
        internal void Apply(
            ClientBattleCameraIntent intent,
            ClientActorViewRegistry actors)
        {
            if (_sceneGeneration == 0 || intent == null || actors == null)
            {
                return;
            }

            _followEntityID = intent.FollowEntityID;
            _mode = intent.Mode;
            Tick(actors);

            if (intent.ImpulseEventID > _lastImpulseEventID)
            {
                _lastImpulseEventID = intent.ImpulseEventID;
                _impulseSource.GenerateImpulse();
            }
        }

        /// <summary>
        /// 在每个 render frame 让 FollowProxy 跟随已平滑的 actor presentation。
        /// </summary>
        /// <param name="actors">Current Scene actor view registry。</param>
        internal void Tick(ClientActorViewRegistry actors)
        {
            TickInternal(
                actors,
                hasSemanticAim: false,
                semanticAimYawMillidegrees: 0);
        }

        /// <summary>
        /// 在每个render frame跟随已平滑Actor位置，并以current semantic aim更新交互式rig方向。
        /// </summary>
        /// <param name="actors">Current Scene actor view registry。</param>
        /// <param name="semanticAimYawMillidegrees">同帧输入owner累积的semantic yaw。</param>
        internal void Tick(
            ClientActorViewRegistry actors,
            int semanticAimYawMillidegrees)
        {
            TickInternal(
                actors,
                hasSemanticAim: true,
                semanticAimYawMillidegrees: semanticAimYawMillidegrees);
        }

        /// <summary>提交一帧follow proxy，保持Cinematic的actor projection方向。</summary>
        /// <param name="actors">Current Scene actor view registry。</param>
        /// <param name="hasSemanticAim">当前是否已提供render-cadence semantic aim。</param>
        /// <param name="semanticAimYawMillidegrees">当前semantic yaw，单位millidegree。</param>
        private void TickInternal(
            ClientActorViewRegistry actors,
            bool hasSemanticAim,
            int semanticAimYawMillidegrees)
        {
            if (_sceneGeneration == 0 ||
                actors == null ||
                _followEntityID == 0)
            {
                return;
            }

            if (actors.TryGetActorTransform(
                    _followEntityID,
                    out var actorTransform))
            {
                var rotation = hasSemanticAim &&
                    _mode != ClientBattleCameraMode.Cinematic
                        ? Quaternion.Euler(
                            0f,
                            semanticAimYawMillidegrees * 0.001f,
                            0f)
                        : actorTransform.rotation;
                _followProxy.SetPositionAndRotation(
                    actorTransform.position,
                    rotation);
                SetActiveRig(_mode);
            }
            else
            {
                SetActiveRig(ClientBattleCameraMode.Exploration);
            }
        }

        /// <summary>退役 Scene generation 并恢复不会泄露旧 target 的 fallback。</summary>
        internal void Unbind()
        {
            _sceneGeneration = 0;
            _lastImpulseEventID = 0;
            _followEntityID = 0;
            _mode = ClientBattleCameraMode.Exploration;
            if (_explorationRig != null)
            {
                SetActiveRig(ClientBattleCameraMode.Exploration);
            }
        }

        /// <summary>在 Unity 销毁阶段退役全部 camera intent。</summary>
        private void OnDestroy()
        {
            Unbind();
        }

        /// <summary>保证任一时刻恰好一个 Scene-owned Cinemachine rig active。</summary>
        /// <param name="mode">Current presentation camera mode。</param>
        private void SetActiveRig(ClientBattleCameraMode mode)
        {
            _explorationRig.SetActive(mode == ClientBattleCameraMode.Exploration);
            _meleeRig.SetActive(mode == ClientBattleCameraMode.MeleeCombat);
            _rangedAimRig.SetActive(mode == ClientBattleCameraMode.RangedAim);
            _cinematicRig.SetActive(mode == ClientBattleCameraMode.Cinematic);
        }
    }
}
