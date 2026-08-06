using UnityEngine;

namespace IHomeland.Client.PersonalWorldCombat.Runtime.Input
{
    /// <summary>
    /// 保存唯一 App Scope Input owner 在当前帧采样的非权威 battle 输入。
    /// </summary>
    internal readonly struct ClientBattleSceneInputSample
    {
        /// <summary>创建 move、aim 与离散 action edge 的当前帧快照。</summary>
        internal ClientBattleSceneInputSample(
            Vector2 move,
            Vector2 aim,
            bool jumpPressed,
            bool primaryPressed,
            bool secondaryPressed,
            bool interactPressed,
            bool switchWeaponPressed = false)
        {
            Move = Vector2.ClampMagnitude(move, 1f);
            Aim = aim;
            JumpPressed = jumpPressed;
            PrimaryPressed = primaryPressed;
            SecondaryPressed = secondaryPressed;
            InteractPressed = interactPressed;
            SwitchWeaponPressed = switchWeaponPressed;
        }

        /// <summary>获取归一化移动轴。</summary>
        internal Vector2 Move { get; }

        /// <summary>获取设备产生的相对瞄准增量。</summary>
        internal Vector2 Aim { get; }

        /// <summary>获取本帧是否产生 Jump edge。</summary>
        internal bool JumpPressed { get; }

        /// <summary>获取本帧是否产生 Primary edge。</summary>
        internal bool PrimaryPressed { get; }

        /// <summary>获取本帧是否产生 Secondary edge。</summary>
        internal bool SecondaryPressed { get; }

        /// <summary>获取本帧是否产生 Interact edge。</summary>
        internal bool InteractPressed { get; }

        /// <summary>获取本帧是否产生 SwitchWeapon edge。</summary>
        internal bool SwitchWeaponPressed { get; }

        /// <summary>
        /// 把local strafe/forward轴按semantic aim yaw投影为world X/Z方向。
        /// </summary>
        /// <remarks>
        /// 该计算只使用已量化的semantic yaw，不读取Camera或Scene Transform，
        /// 因此不会把表现状态升级为gameplay authority。
        /// </remarks>
        /// <param name="aimYawMillidegrees">当前semantic yaw，单位millidegree。</param>
        /// <returns>保持原始幅度的world X/Z移动轴。</returns>
        internal Vector2 ProjectMoveToWorld(int aimYawMillidegrees)
        {
            var radians = aimYawMillidegrees * 0.001f * Mathf.Deg2Rad;
            var sine = Mathf.Sin(radians);
            var cosine = Mathf.Cos(radians);
            return Vector2.ClampMagnitude(
                new Vector2(
                    Move.x * cosine + Move.y * sine,
                    Move.y * cosine - Move.x * sine),
                1f);
        }
    }

    /// <summary>
    /// 保存唯一Input owner对当前render frame的明确battle可用性与样本。
    /// </summary>
    /// <remarks>
    /// `GameplayAvailable=false`是完整的生命周期语义，Scene host必须将它
    /// 投影为neutral continuous intent，不得延续上一帧输入。
    /// </remarks>
    internal readonly struct ClientBattleSceneInputFrame
    {
        /// <summary>创建一个明确的input owner帧快照。</summary>
        /// <param name="gameplayAvailable">Gameplay owner当前是否允许采样。</param>
        /// <param name="sample">可用时的当前样本；不可用时必须为default。</param>
        internal ClientBattleSceneInputFrame(
            bool gameplayAvailable,
            ClientBattleSceneInputSample sample)
        {
            GameplayAvailable = gameplayAvailable;
            Sample = gameplayAvailable ? sample : default;
        }

        /// <summary>获取Gameplay owner当前是否允许battle采样。</summary>
        internal bool GameplayAvailable { get; }

        /// <summary>获取可用样本；不可用时为default neutral值。</summary>
        internal ClientBattleSceneInputSample Sample { get; }
    }

    /// <summary>
    /// 定义 Scene battle host 可读取的唯一 Input System 窄端口。
    /// </summary>
    internal interface IClientBattleInputSource
    {
        /// <summary>
        /// 验证 production Player map 包含完整且唯一的 battle semantic actions。
        /// </summary>
        void ValidateBattleInputConfiguration();

        /// <summary>
        /// 捕获 Gameplay input owner 当前可用性与帧样本。
        /// </summary>
        /// <returns>
        /// 始终返回明确快照；UI、失焦、release gate或停止期间返回
        /// `GameplayAvailable=false`，而不是模糊地复用上一样本。
        /// </returns>
        ClientBattleSceneInputFrame CaptureBattleInput();
    }
}
