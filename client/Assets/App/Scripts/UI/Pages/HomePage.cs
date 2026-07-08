using App.Core;
using TMPro;
using UnityEngine;
using UnityEngine.UI;

namespace App.UI
{
    public sealed class HomePage : UIPanel
    {
        [Header("UI References")]
        [SerializeField] private Button startGameButton;
        [SerializeField] private Button settingButton;
        [SerializeField] private Button quitButton;
        [SerializeField] private TMP_Text messageText;

        protected override void OnInitialize()
        {
            if (startGameButton != null)
            {
                startGameButton.onClick.AddListener(OnClickStartGame);
            }

            if (settingButton != null)
            {
                settingButton.onClick.AddListener(OnClickSetting);
            }

            if (quitButton != null)
            {
                quitButton.onClick.AddListener(OnClickQuit);
            }

            AppRoot.Instance.Log.Info<HomePage>("Initialize.");
        }

        protected override void OnShow()
        {
            string account = AppRoot.Instance.Account == null
                ? "Player"
                : AppRoot.Instance.Account.CurrentAccount;

            SetMessage($"Welcome, {account}.");

            AppRoot.Instance.Log.Info<HomePage>("Show.");
        }

        protected override void OnHide()
        {
            AppRoot.Instance.Log.Info<HomePage>("Hide.");
        }

        private void OnDestroy()
        {
            if (startGameButton != null)
            {
                startGameButton.onClick.RemoveListener(OnClickStartGame);
            }

            if (settingButton != null)
            {
                settingButton.onClick.RemoveListener(OnClickSetting);
            }

            if (quitButton != null)
            {
                quitButton.onClick.RemoveListener(OnClickQuit);
            }
        }

        private void OnClickStartGame()
        {
            if (AppRoot.Instance.Account == null || !AppRoot.Instance.Account.IsLoggedIn)
            {
                SetMessage("Please login first.");
                AppRoot.Instance.UI.OpenPage(AppPages.LoginPage);
                Close();
                return;
            }

            if (AppRoot.Instance.UI.OpenPage(AppPages.RoomPage) == null)
            {
                SetMessage("Room page is not ready.");
                AppRoot.Instance.Log.Error<HomePage>("Open RoomPage failed.");
                return;
            }

            Close();
        }

        private void OnClickSetting()
        {
            SetMessage("Setting page is not implemented yet.");
            AppRoot.Instance.Log.Info<HomePage>("Setting clicked.");
        }

        private async void OnClickQuit()
        {
            SetMessage("Logging out...");
            AppRoot.Instance.Log.Info<HomePage>("Logout clicked.");

            if (AppRoot.Instance.Account != null)
            {
                await AppRoot.Instance.Account.LogoutAsync();
            }

            AppRoot.Instance.UI.OpenPage(AppPages.LoginPage);
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
