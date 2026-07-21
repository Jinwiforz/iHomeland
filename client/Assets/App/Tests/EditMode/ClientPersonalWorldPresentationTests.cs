using System;
using System.Collections.Generic;
using System.Linq;
using System.Reflection;
using IHomeland.Client.Presentation.Navigation;
using IHomeland.Client.Presentation.PersonalWorld;
using IHomeland.Client.Scenes.PersonalWorld;
using NUnit.Framework;
using UnityEngine.UIElements;

namespace IHomeland.Client.Tests.EditMode
{
    /// <summary>
    /// 验证个人世界产品模型、route catalog 与 Scene catalog 的纯 C# 边界。
    /// </summary>
    public sealed class ClientPersonalWorldPresentationTests
    {
        /// <summary>验证产品 UI 不会把内部 failure key 直接显示给玩家。</summary>
        [Test]
        public void FailureTextUsesPlayerFacingChineseCopy()
        {
            var unauthenticated = ClientPersonalWorldUiToolkitView.FailureText(
                ClientPersonalWorldFailure.Unauthenticated);
            var transport = ClientPersonalWorldUiToolkitView.FailureText(
                ClientPersonalWorldFailure.Transport);
            var inviteUnavailable = ClientPersonalWorldUiToolkitView.FailureText(
                ClientPersonalWorldFailure.InviteUnavailable);

            Assert.That(unauthenticated, Is.EqualTo("登录状态已失效，请重新登录。"));
            Assert.That(transport, Is.EqualTo("网络连接失败，请检查服务器或网络后重试。"));
            Assert.That(inviteUnavailable, Is.EqualTo("邀请已撤销或失效，请选择最新邀请。"));
            Assert.That(unauthenticated, Does.Not.Contain("client.personal_world"));
            Assert.That(transport, Does.Not.Contain("client.personal_world"));
        }

        /// <summary>验证完整 View State 不公开 credential、transport 或 generated 类型。</summary>
        [Test]
        public void ViewStatePublicSurfaceContainsNoCredentialOrTransportFacts()
        {
            var forbiddenNames = new[]
            {
                "password", "token", "ticket", "admission", "endpoint", "credential", "payload",
            };
            var modelTypes = typeof(ClientPersonalWorldViewState).Assembly
                .GetTypes()
                .Where(type => type.Namespace == typeof(ClientPersonalWorldViewState).Namespace)
                .Where(type => type.Name.EndsWith("ViewState", StringComparison.Ordinal))
                .ToArray();

            Assert.That(modelTypes, Is.Not.Empty);
            foreach (var type in modelTypes)
            {
                foreach (var property in type.GetProperties(
                             BindingFlags.Instance | BindingFlags.Static |
                             BindingFlags.Public | BindingFlags.NonPublic))
                {
                    Assert.That(
                        forbiddenNames.Any(name => property.Name.IndexOf(name, StringComparison.OrdinalIgnoreCase) >= 0),
                        Is.False,
                        $"{type.Name}.{property.Name} 泄漏禁止语义。");
                    Assert.That(
                        property.PropertyType.Namespace?.StartsWith("IHomeland.Protocol", StringComparison.Ordinal),
                        Is.Not.True,
                        $"{type.Name}.{property.Name} 不得公开 generated message。");
                    Assert.That(
                        property.PropertyType.Namespace?.StartsWith("UnityEngine", StringComparison.Ordinal),
                        Is.Not.True,
                        $"{type.Name}.{property.Name} 不得公开 Unity object。");
                }
            }
        }

        /// <summary>验证 Visit 页面集合经过防御性复制。</summary>
        [Test]
        public void VisitViewStateDefensivelyCopiesCollections()
        {
            var visitors = new List<string> { "player-1" };
            var invites = new List<ClientVisitInviteViewState>
            {
                new ClientVisitInviteViewState("invite-1", "visit-1", "owner-1", "player-1", 1000),
            };

            var state = new ClientWorldVisitViewState(
                true,
                true,
                false,
                "visit-1",
                2,
                2000,
                0,
                visitors,
                invites,
                ClientWorldVisitActionState.None);
            visitors[0] = "changed";
            invites.Clear();

            Assert.That(state.VisitorPlayerIDs, Is.EqualTo(new[] { "player-1" }));
            Assert.That(state.Invites, Has.Count.EqualTo(1));
            Assert.That(state.Invites[0].InviteID, Is.EqualTo("invite-1"));
        }

        /// <summary>验证等价快照不会销毁并重建现有邀请列表节点。</summary>
        [Test]
        public void UnchangedLabelListPreservesExistingVisualElements()
        {
            var container = new VisualElement();
            var existing = new Label("invite-1");
            container.Add(existing);

            ClientPersonalWorldUiToolkitView.ReplaceLabels(
                container,
                new[] { "invite-1" });

            Assert.That(container.childCount, Is.EqualTo(1));
            Assert.That(container.ElementAt(0), Is.SameAs(existing));

            ClientPersonalWorldUiToolkitView.ReplaceLabels(
                container,
                new[] { "invite-2" });

            Assert.That(container.childCount, Is.EqualTo(1));
            Assert.That(container.ElementAt(0), Is.Not.SameAs(existing));
            Assert.That(((Label)container.ElementAt(0)).text, Is.EqualTo("invite-2"));
        }

        /// <summary>验证 WorldVisit action capability 由权威投影显式给出。</summary>
        [Test]
        public void WorldVisitActionsDoNotDependOnViewRoleGuessing()
        {
            var invite = new ClientVisitInviteViewState(
                "invite-1",
                "visit-1",
                "owner-1",
                "visitor-1",
                1000);
            var available = new ClientWorldVisitViewState(
                true,
                true,
                false,
                string.Empty,
                0,
                0,
                0,
                Array.Empty<string>(),
                new[] { invite },
                new ClientWorldVisitActionState(false, false, false, false, false, true, false));
            var activeOwnerSession = new ClientWorldVisitViewState(
                true,
                true,
                false,
                "visit-own",
                1,
                2000,
                0,
                Array.Empty<string>(),
                new[] { invite },
                new ClientWorldVisitActionState(false, true, true, false, true, false, false));

            Assert.That(available.Actions.CanAcceptInvite, Is.True);
            Assert.That(available.Actions.CanCloseVisit, Is.False);
            Assert.That(activeOwnerSession.Actions.CanAcceptInvite, Is.False);
            Assert.That(activeOwnerSession.Actions.CanRevokeInvite, Is.True);
        }

        /// <summary>验证邀请摘要包含接受动作所需的两个关联标识。</summary>
        [Test]
        public void InviteSummaryExposesVisitSessionAndInviteIdentifiers()
        {
            var invite = new ClientVisitInviteViewState(
                "invite-1",
                "visit-1",
                "owner-1",
                "visitor-1",
                1000);

            var summary = ClientPersonalWorldUiToolkitView.FormatInviteSummary(invite);

            Assert.That(summary, Does.Contain("visit-1"));
            Assert.That(summary, Does.Contain("invite-1"));
            Assert.That(summary, Does.Contain("visitor-1"));
        }

        /// <summary>验证新 replacement 会使旧 invite selection 失效并确定性选择唯一新项。</summary>
        [Test]
        public void InviteSelectionFollowsCurrentReplacementIdentity()
        {
            var invite = new ClientVisitInviteViewState(
                "invite-1",
                "visit-1",
                "owner-1",
                "visitor-1",
                1000);
            Assert.That(
                ClientPersonalWorldUiToolkitView.ReconcileInviteSelection(
                    new[] { invite },
                    "visit-old",
                    "invite-old",
                    out var visitSessionID,
                    out var inviteID),
                Is.True);
            Assert.That(visitSessionID, Is.EqualTo("visit-1"));
            Assert.That(inviteID, Is.EqualTo("invite-1"));
            Assert.That(
                ClientPersonalWorldUiToolkitView.ReconcileInviteSelection(
                    new[]
                    {
                        invite,
                        new ClientVisitInviteViewState(
                            "invite-2",
                            "visit-2",
                            "owner-2",
                            "visitor-1",
                            2000),
                    },
                    "visit-old",
                    "invite-old",
                    out _,
                    out _),
                Is.False);
            Assert.That(
                ClientPersonalWorldUiToolkitView.ReconcileInviteSelection(
                    Array.Empty<ClientVisitInviteViewState>(),
                    "visit-1",
                    "invite-1",
                    out var emptyVisitSessionID,
                    out var emptyInviteID),
                Is.False);
            Assert.That(emptyVisitSessionID, Is.Empty);
            Assert.That(emptyInviteID, Is.Empty);
            Assert.That(
                ClientPersonalWorldUiToolkitView.ReconcileVisitorSelection(
                    Array.Empty<string>(),
                    "visitor-old"),
                Is.Empty);
        }

        /// <summary>验证人工复制 identity 的首尾空白不会阻止合法 command。</summary>
        [Test]
        public void IdentityInputTrimsOnlySurroundingWhitespace()
        {
            Assert.That(
                ClientPersonalWorldUiToolkitView.NormalizeIdentityInput(" \tply_target_1\r\n"),
                Is.EqualTo("ply_target_1"));
            Assert.That(
                ClientPersonalWorldUiToolkitView.NormalizeIdentityInput("ply target"),
                Is.EqualTo("ply target"));
            Assert.That(
                ClientPersonalWorldUiToolkitView.NormalizeIdentityInput(null),
                Is.Empty);
        }

        /// <summary>验证本地双客户端邀请窗口与服务端配置一致，并保持在 v1 一小时上限内。</summary>
        [Test]
        public void InviteLifetimeMatchesServerV1Policy()
        {
            Assert.That(
                ClientPersonalWorldExperience.InviteLifetimeMilliseconds,
                Is.EqualTo(30u * 60u * 1000u));
            Assert.That(
                ClientPersonalWorldExperience.InviteLifetimeMilliseconds,
                Is.LessThanOrEqualTo(60u * 60u * 1000u));
        }

        /// <summary>验证 inactive 基线不显示任一产品页面。</summary>
        [Test]
        public void InactiveViewStateHasNoVisibleProductPage()
        {
            var state = ClientPersonalWorldViewState.Inactive;

            Assert.That(state.Phase, Is.EqualTo(ClientPersonalWorldPhase.Inactive));
            Assert.That(state.Login.Visible, Is.False);
            Assert.That(state.Shell.Visible, Is.False);
            Assert.That(state.WorldVisit.Visible, Is.False);
            Assert.That(state.WorldHud.Visible, Is.False);
            Assert.That(state.ActiveIntent, Is.EqualTo(ClientPersonalWorldIntent.None));
        }

        /// <summary>验证 action result 不能伪造 None failure。</summary>
        [Test]
        public void FailedActionRejectsNoneFailure()
        {
            Assert.Throws<ArgumentOutOfRangeException>(() =>
                ClientPersonalWorldActionResult.Failed(ClientPersonalWorldFailure.None));
            Assert.That(ClientPersonalWorldActionResult.Success().Succeeded, Is.True);
        }

        /// <summary>验证 production route 只有已交付的五个 identity。</summary>
        [Test]
        public void ProductRoutesExcludeSettingsAndPreserveUniqueIdentity()
        {
            var definitions = ClientPersonalWorldUiRoutes.Definitions;

            Assert.That(definitions, Has.Count.EqualTo(5));
            Assert.That(definitions.Select(item => item.RouteId).Distinct().Count(), Is.EqualTo(5));
            Assert.That(definitions.Any(item => item.RouteId == ClientUiRouteId.Settings), Is.False);
            Assert.That(definitions.Any(item => item.RouteId == ClientUiRouteId.WorldHud), Is.True);
        }

        /// <summary>验证 WorldHud 的 framework、layer、input 与 scene lifecycle 固定。</summary>
        [Test]
        public void WorldHudRouteIsSceneBoundUguiGameplayHud()
        {
            var definition = ClientPersonalWorldUiRoutes.Definitions.Single(
                item => item.RouteId == ClientUiRouteId.WorldHud);

            Assert.That(definition.FrameworkOwner, Is.EqualTo(ClientUiFrameworkOwner.Ugui));
            Assert.That(definition.Layer, Is.EqualTo(ClientUiLayer.Hud));
            Assert.That(definition.InputMode, Is.EqualTo(ClientUiInputMode.Gameplay));
            Assert.That(definition.Lifecycle, Is.EqualTo(ClientUiLifecycle.SceneBound));
        }

        /// <summary>验证 ConnectionLost 固定为重建型 modal。</summary>
        [Test]
        public void ConnectionLostRouteIsRecreatedModal()
        {
            var definition = ClientPersonalWorldUiRoutes.Definitions.Single(
                item => item.RouteId == ClientUiRouteId.ConnectionLost);

            Assert.That(definition.FrameworkOwner, Is.EqualTo(ClientUiFrameworkOwner.UiToolkit));
            Assert.That(definition.Layer, Is.EqualTo(ClientUiLayer.Modal));
            Assert.That(definition.InputMode, Is.EqualTo(ClientUiInputMode.Modal));
            Assert.That(definition.Lifecycle, Is.EqualTo(ClientUiLifecycle.Recreate));
        }

        /// <summary>验证 Scene catalog 不接受任意或预留 identity。</summary>
        [Test]
        public void SceneCatalogOnlyAcceptsPersonalWorldIdentity()
        {
            Assert.That(
                ClientWorldSceneCatalog.TryGetSceneName(ClientWorldSceneId.PersonalWorld, out var sceneName),
                Is.True);
            Assert.That(sceneName, Is.EqualTo("PersonalWorldScene"));
            Assert.That(
                ClientWorldSceneCatalog.TryGetSceneName(ClientWorldSceneId.None, out var missing),
                Is.False);
            Assert.That(missing, Is.Empty);
        }
    }
}
