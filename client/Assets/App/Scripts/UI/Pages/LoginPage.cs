using App.Core;
using TMPro;
using UnityEngine;
using UnityEngine.UI;

namespace App.UI
{
    public sealed class LoginPage : UIPanel
    {
        [Header("UI References")]
        [SerializeField] private TMP_InputField accountInput;
        [SerializeField] private TMP_InputField passwordInput;
        [SerializeField] private Button loginButton;
        [SerializeField] private TMP_Text messageText;

        protected override void OnInitialize()
        {
            if (loginButton != null)
            {
                loginButton.onClick.AddListener(OnClickLogin);
            }

            AppRoot.Instance.Log.Info<LoginPage>("Initialize.");
        }

        protected override void OnShow()
        {
            SetMessage("Please enter account and password.");

            AppRoot.Instance.Log.Info<LoginPage>("Show.");
        }

        protected override void OnHide()
        {
            AppRoot.Instance.Log.Info<LoginPage>("Hide.");
        }

        private void OnDestroy()
        {
            if (loginButton != null)
            {
                loginButton.onClick.RemoveListener(OnClickLogin);
            }
        }

        private void OnClickLogin()
        {
            string account = accountInput == null ? string.Empty : accountInput.text;
            string password = passwordInput == null ? string.Empty : passwordInput.text;

            if (AppRoot.Instance.Account == null)
            {
                SetMessage("Account system is not ready.");
                AppRoot.Instance.Log.Error<LoginPage>("AccountSystem is null.");
                return;
            }

            bool success = AppRoot.Instance.Account.TryLogin(account, password, out string message);

            SetMessage(message);

            if (!success)
            {
                AppRoot.Instance.Log.Warning<LoginPage>($"Login failed. Reason: {message}");
                return;
            }

            AppRoot.Instance.Log.Info<LoginPage>($"Login success. Account: {account}");

            AppRoot.Instance.UI.OpenPage(AppPages.HomePage);

            Close();
        }

        private void SetMessage(string message)
        {
            if (messageText != null)
            {
                messageText.text = message;
            }
        }
    }
}