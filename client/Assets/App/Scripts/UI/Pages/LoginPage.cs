using App.Core;
using App.Systems;
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
        [SerializeField] private Button registerButton;
        [SerializeField] private TMP_Text messageText;

        protected override void OnInitialize()
        {
            if (loginButton != null)
            {
                loginButton.onClick.AddListener(OnClickLogin);
            }

            if (registerButton != null)
            {
                registerButton.onClick.AddListener(OnClickRegister);
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

            if (registerButton != null)
            {
                registerButton.onClick.RemoveListener(OnClickRegister);
            }
        }

        private async void OnClickLogin()
        {
            await SubmitAccountOperation(true);
        }

        private async void OnClickRegister()
        {
            await SubmitAccountOperation(false);
        }

        private async System.Threading.Tasks.Task SubmitAccountOperation(bool login)
        {
            string account = accountInput == null ? string.Empty : accountInput.text;
            string password = passwordInput == null ? string.Empty : passwordInput.text;

            if (AppRoot.Instance.Account == null)
            {
                SetMessage("Account system is not ready.");
                AppRoot.Instance.Log.Error<LoginPage>("AccountSystem is null.");
                return;
            }

            SetButtonsInteractable(false);
            SetMessage(login ? "Logging in..." : "Registering...");

            AccountOperationResult result = login
                ? await AppRoot.Instance.Account.LoginAsync(account, password)
                : await AppRoot.Instance.Account.RegisterAsync(account, password);

            SetMessage(result.Message);
            SetButtonsInteractable(true);

            if (!result.Success)
            {
                AppRoot.Instance.Log.Warning<LoginPage>($"{(login ? "Login" : "Register")} failed. {result.ToDiagnosticLine()}");
                return;
            }

            AppRoot.Instance.Log.Info<LoginPage>($"{(login ? "Login" : "Register")} success. Account: {account}");

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

        private void SetButtonsInteractable(bool interactable)
        {
            if (loginButton != null)
            {
                loginButton.interactable = interactable;
            }

            if (registerButton != null)
            {
                registerButton.interactable = interactable;
            }
        }
    }
}
