using System.Collections;
using App.UI;
using UnityEngine;

namespace App.Core
{
    public sealed class AppBootstrap : MonoBehaviour
    {
        private bool _hasRun;

        public void Run()
        {
            if (_hasRun)
            {
                return;
            }

            _hasRun = true;
            AppRoot.Instance.Initialize();
            AppRoot.Instance.Log.Info<AppBootstrap>("Bootstrap started.");
            StartCoroutine(StartAppRoutine());
            // EnterApp();
        }

        private void EnterApp()
        {
            AppRoot.Instance.Log.Info<AppBootstrap>("Enter app.");
            AppRoot.Instance.UI.OpenPage("LoginPage");
        }

        private IEnumerator StartAppRoutine()
        {
            LoadingPage loadingPage = AppRoot.Instance.UI.OpenPage<LoadingPage>("LoadingPage");

            if (loadingPage == null)
            {
                AppRoot.Instance.Log.Error<AppBootstrap>("Open LoadingPage failed.");
                yield break;
            }

            loadingPage.SetMessage("Initializing...");
            loadingPage.SetProgress(0.2f);

            yield return new WaitForSeconds(0.5f);

            loadingPage.SetMessage("Checking version...");
            loadingPage.SetProgress(0.5f);

            yield return new WaitForSeconds(0.5f);

            loadingPage.SetMessage("Loading resources...");
            loadingPage.SetProgress(0.8f);

            yield return new WaitForSeconds(0.5f);

            loadingPage.SetMessage("Completed.");
            loadingPage.SetProgress(1f);

            yield return new WaitForSeconds(0.3f);

            yield return AppRoot.Instance.UI.ClosePageAsync("LoadingPage");
        }
    }
}