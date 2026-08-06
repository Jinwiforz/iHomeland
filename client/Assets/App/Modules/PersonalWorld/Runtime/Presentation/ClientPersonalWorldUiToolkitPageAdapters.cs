using System;
using IHomeland.Client.PersonalWorld.Presentation;
using UnityEngine.UIElements;

namespace IHomeland.Client.PersonalWorld.Runtime.Presentation
{
    /// <summary>Login 页面控件校验与渲染 adapter。</summary>
    internal sealed class ClientLoginUiToolkitPageAdapter
    {
        /// <summary>校验 Login 稳定 UXML 控件。</summary>
        internal void Validate(VisualElement root)
        {
            Require<TextField>(root, "username-input");
            Require<TextField>(root, "password-input");
            Require<TextField>(root, "display-name-input");
            Require<Toggle>(root, "register-toggle");
            Require<Button>(root, "submit-button");
            Require<Label>(root, "error-label");
        }

        /// <summary>渲染 Login busy 与低敏失败。</summary>
        internal void Render(
            VisualElement root,
            ClientPersonalWorldViewState state,
            Func<ClientPersonalWorldFailure, string> failureText)
        {
            var login = state.Login;
            Require<Button>(root, "submit-button").SetEnabled(!login.Busy);
            Require<TextField>(root, "username-input").SetEnabled(!login.Busy);
            Require<TextField>(root, "password-input").SetEnabled(!login.Busy);
            Require<TextField>(root, "display-name-input").SetEnabled(!login.Busy);
            Require<Toggle>(root, "register-toggle").SetEnabled(!login.Busy);
            Require<Label>(root, "error-label").text =
                failureText(login.Failure);
        }

        /// <summary>取得必需控件。</summary>
        private static T Require<T>(VisualElement root, string name)
            where T : VisualElement
        {
            return root?.Q<T>(name) ??
                   throw new InvalidOperationException(
                       $"Login 缺少必需控件：{name}。");
        }
    }

    /// <summary>Shell 页面控件校验与渲染 adapter。</summary>
    internal sealed class ClientShellUiToolkitPageAdapter
    {
        /// <summary>校验 Shell 稳定 UXML 控件。</summary>
        internal void Validate(VisualElement root)
        {
            Require<Label>(root, "player-id-label");
            Require<Label>(root, "display-name-label");
            Require<Label>(root, "phase-label");
            Require<Button>(root, "retry-button");
            Require<Button>(root, "logout-button");
            Require<Label>(root, "error-label");
        }

        /// <summary>渲染 Shell account、phase 与 action gate。</summary>
        internal void Render(
            VisualElement root,
            ClientPersonalWorldViewState state,
            Func<ClientPersonalWorldFailure, string> failureText)
        {
            Require<Label>(root, "player-id-label").text = state.Shell.PlayerID;
            Require<Label>(root, "display-name-label").text = state.Shell.DisplayName;
            Require<Label>(root, "phase-label").text = state.Shell.Phase.ToString();
            var busy = state.ActiveIntent != ClientPersonalWorldIntent.None;
            Require<Button>(root, "retry-button").SetEnabled(!busy);
            Require<Button>(root, "logout-button").SetEnabled(!busy);
            Require<Label>(root, "error-label").text =
                failureText(state.Shell.Failure);
        }

        /// <summary>取得必需控件。</summary>
        private static T Require<T>(VisualElement root, string name)
            where T : VisualElement
        {
            return root?.Q<T>(name) ??
                   throw new InvalidOperationException(
                       $"Shell 缺少必需控件：{name}。");
        }
    }

    /// <summary>ConnectionLost 页面控件校验与渲染 adapter。</summary>
    internal sealed class ClientConnectionLostUiToolkitPageAdapter
    {
        /// <summary>校验 ConnectionLost 稳定 UXML 控件。</summary>
        internal void Validate(VisualElement root)
        {
            Require<Label>(root, "connection-lost-title");
            Require<Label>(root, "connection-lost-description");
            Require<Button>(root, "retry-button");
            Require<Button>(root, "logout-button");
            Require<Label>(root, "error-label");
        }

        /// <summary>渲染恢复阶段、retry gate 与低敏失败。</summary>
        internal void Render(
            VisualElement root,
            ClientPersonalWorldViewState state,
            Func<ClientPersonalWorldPhase, string> title,
            Func<ClientPersonalWorldPhase, string> description,
            Func<ClientPersonalWorldFailure, string> failureText)
        {
            var busy = state.ActiveIntent != ClientPersonalWorldIntent.None;
            Require<Label>(root, "connection-lost-title").text =
                title(state.Phase);
            Require<Label>(root, "connection-lost-description").text =
                description(state.Phase);
            Require<Button>(root, "retry-button").SetEnabled(
                !busy &&
                state.Phase == ClientPersonalWorldPhase.ConnectionLost);
            Require<Button>(root, "logout-button").SetEnabled(!busy);
            Require<Label>(root, "error-label").text =
                failureText(state.Shell.Failure);
        }

        /// <summary>取得必需控件。</summary>
        private static T Require<T>(VisualElement root, string name)
            where T : VisualElement
        {
            return root?.Q<T>(name) ??
                   throw new InvalidOperationException(
                       $"ConnectionLost 缺少必需控件：{name}。");
        }
    }

    /// <summary>WorldVisit 页面控件、replacement selection 与渲染 adapter。</summary>
    internal sealed class ClientWorldVisitUiToolkitPageAdapter
    {
        /// <summary>current invite selection。</summary>
        internal string SelectedInviteVisitSessionID { get; private set; } =
            string.Empty;

        /// <summary>current invite identity。</summary>
        internal string SelectedInviteID { get; private set; } = string.Empty;

        /// <summary>current Visitor identity。</summary>
        internal string SelectedVisitorPlayerID { get; private set; } =
            string.Empty;

        /// <summary>校验 WorldVisit 稳定 UXML 控件。</summary>
        internal void Validate(VisualElement root)
        {
            foreach (var name in new[]
                     {
                         "role-label", "visit-session-label", "revision-label",
                         "visitor-list", "invite-list", "target-player-input",
                         "open-visit-button", "create-invite-button",
                         "revoke-invite-button", "accept-invite-button",
                         "kick-visitor-button", "close-visit-button",
                         "leave-visit-button", "error-label",
                     })
            {
                if (root?.Q(name) == null)
                {
                    throw new InvalidOperationException(
                        $"WorldVisit 缺少必需控件：{name}。");
                }
            }
        }

        /// <summary>渲染 role、revision、replacement selections 与 action gates。</summary>
        internal void Render(
            VisualElement root,
            ClientPersonalWorldViewState state,
            Action rerender,
            Func<ClientPersonalWorldFailure, string> failureText)
        {
            var visit = state.WorldVisit;
            Require<Label>(root, "role-label").text =
                visit.IsOwner ? "Owner" : visit.IsVisitor ? "Visitor" : "None";
            Require<Label>(root, "visit-session-label").text =
                visit.VisitSessionID;
            Require<Label>(root, "revision-label").text =
                visit.Revision.ToString();

            ClientPersonalWorldUiToolkitView.ReconcileInviteSelection(
                visit.Invites,
                SelectedInviteVisitSessionID,
                SelectedInviteID,
                out var visitSessionID,
                out var inviteID);
            SelectedInviteVisitSessionID = visitSessionID;
            SelectedInviteID = inviteID;
            SelectedVisitorPlayerID =
                ClientPersonalWorldUiToolkitView.ReconcileVisitorSelection(
                    visit.VisitorPlayerIDs,
                    SelectedVisitorPlayerID);

            RenderVisitors(root, visit, rerender);
            RenderInvites(root, visit, rerender);
            var target = ClientPersonalWorldUiToolkitView.NormalizeIdentityInput(
                Require<TextField>(root, "target-player-input").value);
            var createTargetAvailable =
                !string.IsNullOrEmpty(target) &&
                !ContainsInviteTarget(visit, target);
            Require<Button>(root, "open-visit-button")
                .SetEnabled(visit.Actions.CanOpenVisit);
            Require<Button>(root, "create-invite-button")
                .SetEnabled(visit.Actions.CanCreateInvite && createTargetAvailable);
            Require<Button>(root, "revoke-invite-button")
                .SetEnabled(
                    visit.Actions.CanRevokeInvite &&
                    !string.IsNullOrEmpty(SelectedInviteID));
            Require<Button>(root, "kick-visitor-button")
                .SetEnabled(
                    visit.Actions.CanKickVisitor &&
                    !string.IsNullOrEmpty(SelectedVisitorPlayerID));
            Require<Button>(root, "close-visit-button")
                .SetEnabled(visit.Actions.CanCloseVisit);
            Require<Button>(root, "accept-invite-button")
                .SetEnabled(
                    visit.Actions.CanAcceptInvite &&
                    !string.IsNullOrEmpty(SelectedInviteID));
            Require<Button>(root, "leave-visit-button")
                .SetEnabled(visit.Actions.CanLeaveVisit);
            Require<Label>(root, "error-label").text =
                failureText(state.Shell.Failure);
        }

        /// <summary>渲染 Visitor selection buttons。</summary>
        private void RenderVisitors(
            VisualElement root,
            ClientWorldVisitViewState visit,
            Action rerender)
        {
            var container = Require<VisualElement>(root, "visitor-list");
            container.Clear();
            foreach (var playerID in visit.VisitorPlayerIDs)
            {
                var captured = playerID;
                var selected = string.Equals(
                    SelectedVisitorPlayerID,
                    captured,
                    StringComparison.Ordinal);
                var button = new Button(() =>
                {
                    SelectedVisitorPlayerID = captured;
                    rerender();
                })
                {
                    text = selected ? $"✓ {captured}" : captured,
                };
                button.SetEnabled(visit.Actions.CanKickVisitor);
                container.Add(button);
            }
        }

        /// <summary>渲染 invite selection buttons。</summary>
        private void RenderInvites(
            VisualElement root,
            ClientWorldVisitViewState visit,
            Action rerender)
        {
            var container = Require<VisualElement>(root, "invite-list");
            container.Clear();
            foreach (var invite in visit.Invites)
            {
                var captured = invite;
                var selected =
                    string.Equals(
                        SelectedInviteVisitSessionID,
                        captured.VisitSessionID,
                        StringComparison.Ordinal) &&
                    string.Equals(
                        SelectedInviteID,
                        captured.InviteID,
                        StringComparison.Ordinal);
                var summary =
                    ClientPersonalWorldUiToolkitView.FormatInviteSummary(captured);
                var button = new Button(() =>
                {
                    SelectedInviteVisitSessionID = captured.VisitSessionID;
                    SelectedInviteID = captured.InviteID;
                    rerender();
                })
                {
                    text = selected ? $"✓ {summary}" : summary,
                };
                button.SetEnabled(
                    visit.Actions.CanAcceptInvite ||
                    visit.Actions.CanRevokeInvite);
                container.Add(button);
            }
        }

        /// <summary>判断 target 已有 pending invite。</summary>
        private static bool ContainsInviteTarget(
            ClientWorldVisitViewState visit,
            string target)
        {
            foreach (var invite in visit.Invites)
            {
                if (string.Equals(
                        invite.TargetVisitorID,
                        target,
                        StringComparison.Ordinal))
                {
                    return true;
                }
            }

            return false;
        }

        /// <summary>取得必需控件。</summary>
        private static T Require<T>(VisualElement root, string name)
            where T : VisualElement
        {
            return root?.Q<T>(name) ??
                   throw new InvalidOperationException(
                       $"WorldVisit 缺少必需控件：{name}。");
        }
    }
}
