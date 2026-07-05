using App.Core;
using UnityEngine;

namespace App.UI
{
    public sealed class LoginPage : UIPanel
    {
        protected override void OnInitialize()
        {
            AppRoot.Instance.Log.Info<LoginPage>("Initialize.");
        }

        protected override void OnShow()
        {
            AppRoot.Instance.Log.Info<LoginPage>("Show.");
        }

        protected override void OnHide()
        {
            AppRoot.Instance.Log.Info<LoginPage>("Hide.");
        }

        public void OnClickStartGame()
        {
            AppRoot.Instance.Log.Info<LoginPage>("Click start app.");
        }

        public void OnClickExitGame()
        {
            AppRoot.Instance.Log.Info<LoginPage>("Click exit app.");
            Application.Quit();
        }
    }
}