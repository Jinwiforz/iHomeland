using System;
using IHomeland.Client.AppShell.Runtime.Presentation.Hosts;
using IHomeland.Client.AppShell.Runtime.Presentation.Hosts.UGUI;
using IHomeland.Client.AppShell.Runtime.Presentation.Hosts.UIToolkit;
using IHomeland.Client.PersonalWorldCombat.Runtime.Input;
using NUnit.Framework;
using UnityEngine;
using UnityEngine.InputSystem;

namespace IHomeland.Client.PersonalWorldCombat.Tests.EditMode
{
    /// <summary>
    /// 验证唯一 Input owner 对 battle semantic actions 的 closed production contract。
    /// </summary>
    public sealed class ClientBattleInputContractTests
    {
        /// <summary>确认完整 battle actions 与双设备关键 bindings 被接受。</summary>
        [Test]
        public void CompleteBattleSemanticActionsAreAccepted()
        {
            var fixture = CreateFixture(includeSecondary: true);
            try
            {
                Assert.That(
                    fixture.Host.ValidateBattleInputConfiguration,
                    Throws.Nothing);
            }
            finally
            {
                fixture.Dispose();
            }
        }

        /// <summary>确认错误的 Secondary Action Type 即使 control type 留空也会被拒绝。</summary>
        [Test]
        public void SecondaryWithNonButtonActionTypeIsRejected()
        {
            var fixture = CreateFixture(
                includeSecondary: true,
                secondaryType: InputActionType.Value);
            try
            {
                Assert.That(
                    fixture.Host.ValidateBattleInputConfiguration,
                    Throws.TypeOf<InvalidOperationException>());
            }
            finally
            {
                fixture.Dispose();
            }
        }

        /// <summary>确认缺失 Secondary action 会在任何 runtime input 采样前 fail closed。</summary>
        [Test]
        public void MissingBattleSemanticActionIsRejected()
        {
            var fixture = CreateFixture(includeSecondary: false);
            try
            {
                Assert.That(
                    fixture.Host.ValidateBattleInputConfiguration,
                    Throws.TypeOf<InvalidOperationException>());
            }
            finally
            {
                fixture.Dispose();
            }
        }

        /// <summary>确认 SwitchWeapon 缺少 gamepad binding 时在 runtime clone 前失败。</summary>
        [Test]
        public void SwitchWeaponWithoutGamepadBindingIsRejected()
        {
            var fixture = CreateFixture(
                includeSecondary: true,
                includeSwitchGamepadBinding: false);
            try
            {
                Assert.That(
                    fixture.Host.ValidateBattleInputConfiguration,
                    Throws.TypeOf<InvalidOperationException>());
            }
            finally
            {
                fixture.Dispose();
            }
        }

        /// <summary>
        /// 确认local Move只按semantic yaw转换为world方向，不改变输入幅度。
        /// </summary>
        [Test]
        public void LocalMoveProjectsToCurrentSemanticAimDirection()
        {
            var forward = new ClientBattleSceneInputSample(
                Vector2.up,
                Vector2.zero,
                jumpPressed: false,
                primaryPressed: false,
                secondaryPressed: false,
                interactPressed: false);

            AssertVector(forward.ProjectMoveToWorld(0), 0f, 1f);
            AssertVector(forward.ProjectMoveToWorld(90000), 1f, 0f);
            AssertVector(forward.ProjectMoveToWorld(-90000), -1f, 0f);

            var diagonal = new ClientBattleSceneInputSample(
                new Vector2(1f, 1f),
                Vector2.zero,
                jumpPressed: false,
                primaryPressed: false,
                secondaryPressed: false,
                interactPressed: false);
            Assert.That(
                diagonal.ProjectMoveToWorld(37000).magnitude,
                Is.EqualTo(1f).Within(0.0001f));
        }

        /// <summary>断言world X/Z投影向量。</summary>
        /// <param name="actual">实际投影。</param>
        /// <param name="expectedX">期望world X。</param>
        /// <param name="expectedZ">期望world Z。</param>
        private static void AssertVector(
            Vector2 actual,
            float expectedX,
            float expectedZ)
        {
            Assert.That(actual.x, Is.EqualTo(expectedX).Within(0.0001f));
            Assert.That(actual.y, Is.EqualTo(expectedZ).Within(0.0001f));
        }

        /// <summary>创建 inactive Host 与只读 InputActionAsset 测试模板。</summary>
        /// <param name="includeSecondary">是否登记完整 Secondary action。</param>
        /// <param name="secondaryType">用于正向与 action type 漂移回归的 Secondary 类型。</param>
        /// <param name="includeSwitchGamepadBinding">是否登记 SwitchWeapon gamepad binding。</param>
        /// <returns>需要显式销毁的测试 fixture。</returns>
        private static InputFixture CreateFixture(
            bool includeSecondary,
            InputActionType secondaryType = InputActionType.Button,
            bool includeSwitchGamepadBinding = true)
        {
            var input = ScriptableObject.CreateInstance<InputActionAsset>();
            var player = input.AddActionMap("Player");
            player.AddAction(
                "Move",
                InputActionType.Value,
                expectedControlLayout: "Vector2");
            player.AddAction(
                "Aim",
                InputActionType.Value,
                expectedControlLayout: "Vector2");
            player.AddAction(
                "Jump",
                InputActionType.Button);
            var primary = player.AddAction(
                "Primary",
                InputActionType.Button);
            primary.AddBinding("<Mouse>/leftButton");
            primary.AddBinding("<Gamepad>/buttonWest");
            if (includeSecondary)
            {
                player.AddAction(
                    "Secondary",
                    secondaryType);
            }

            player.AddAction(
                "Interact",
                InputActionType.Button);
            var switchWeapon = player.AddAction(
                "SwitchWeapon",
                InputActionType.Button);
            switchWeapon.AddBinding("<Keyboard>/q");
            if (includeSwitchGamepadBinding)
            {
                switchWeapon.AddBinding("<Gamepad>/leftShoulder");
            }
            player.AddAction("Menu", InputActionType.Button);
            var ui = input.AddActionMap("UI");
            ui.AddAction("Cancel", InputActionType.Button);

            var gameObject = new GameObject("ClientBattleInputContractHost");
            gameObject.SetActive(false);
            var host = gameObject.AddComponent<ClientUiHostRoot>();
            host.ConfigureBeforeActivation(
                input,
                Array.Empty<ClientUiToolkitHost>(),
                Array.Empty<ClientUguiHost>());
            return new InputFixture(gameObject, input, host);
        }

        /// <summary>持有一个必须在测试结束时释放的 Input asset 与 Host。</summary>
        private sealed class InputFixture : IDisposable
        {
            /// <summary>保存测试 GameObject。</summary>
            private readonly GameObject _gameObject;

            /// <summary>保存测试 InputActionAsset。</summary>
            private readonly InputActionAsset _input;

            /// <summary>创建显式拥有 Unity objects 的 fixture。</summary>
            internal InputFixture(
                GameObject gameObject,
                InputActionAsset input,
                ClientUiHostRoot host)
            {
                _gameObject = gameObject;
                _input = input;
                Host = host;
            }

            /// <summary>获取 inactive Input owner Host。</summary>
            internal ClientUiHostRoot Host { get; }

            /// <summary>销毁 fixture 创建的全部 Unity objects。</summary>
            public void Dispose()
            {
                UnityEngine.Object.DestroyImmediate(_gameObject);
                UnityEngine.Object.DestroyImmediate(_input);
            }
        }
    }
}
