using System.Collections;
using System.Threading.Tasks;
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

            StartCoroutine(StartAppRoutine());
        }

        private IEnumerator StartAppRoutine()
        {
            LoadingPage loadingPage = AppRoot.Instance.UI.OpenPage<LoadingPage>(AppPages.LoadingPage);

            if (loadingPage == null)
            {
                AppRoot.Instance.Log.Error<AppBootstrap>("Open LoadingPage failed.");
                yield break;
            }

            loadingPage.ResetProgress();

            yield return RunBootPipeline(loadingPage);

            loadingPage.SetMessage("Completed.");
            yield return loadingPage.SetProgressSmooth(1f, 0.2f);

            yield return new WaitForSeconds(0.15f);

            yield return EnterAppRoutine();
        }

        private IEnumerator RunBootPipeline(LoadingPage loadingPage)
        {
            float progress = 0f;

            yield return RunStep(
                loadingPage,
                "Initializing config...",
                progress,
                progress + 0.1f,
                InitializeConfigRoutine()
            );

            progress += 0.1f;

            yield return RunStep(
                loadingPage,
                "Checking version...",
                progress,
                progress + 0.2f,
                CheckVersionRoutine()
            );

            progress += 0.2f;

            yield return RunStep(
                loadingPage,
                "Loading resources...",
                progress,
                progress + 0.35f,
                PreloadResourcesRoutine()
            );

            progress += 0.35f;

            yield return RunStep(
                loadingPage,
                "Checking account...",
                progress,
                progress + 0.2f,
                CheckAccountRoutine()
            );

            progress += 0.2f;

            yield return RunStep(
                loadingPage,
                "Preparing login page...",
                progress,
                0.95f,
                PrepareLoginRoutine()
            );
        }

        private IEnumerator RunStep(
            LoadingPage loadingPage,
            string message,
            float fromProgress,
            float toProgress,
            IEnumerator routine
        )
        {
            loadingPage.SetMessage(message);
            loadingPage.SetProgress(fromProgress);

            yield return routine;

            yield return loadingPage.SetProgressSmooth(toProgress, 0.15f);
        }

        private IEnumerator InitializeConfigRoutine()
        {
            AppRoot.Instance.Log.Info<AppBootstrap>("Initialize config.");

            // 当前首屏只需要默认 AppConfig；后续接入本地设置、语言或表格配置时在这里扩展初始化步骤。
            yield return null;
        }

        private IEnumerator CheckVersionRoutine()
        {
            AppRoot.Instance.Log.Info<AppBootstrap>("Check version.");

            // 当前版本信息由服务端 `/version` 和本地 version.json 验证；后续接入热更时在这里扩展阻断式检查。
            yield return null;
        }

        private IEnumerator PreloadResourcesRoutine()
        {
            AppRoot.Instance.Log.Info<AppBootstrap>("Preload resources.");

            // 当前 UI 资源按页面打开时加载；后续接入公共图集或音效预热时在这里扩展预加载清单。
            yield return null;
        }

        private IEnumerator CheckAccountRoutine()
        {
            AppRoot.Instance.Log.Info<AppBootstrap>("Check account.");

            Task restoreTask = AppRoot.Instance.Account.RestoreSessionAsync();
            while (!restoreTask.IsCompleted)
            {
                yield return null;
            }

            if (restoreTask.IsFaulted)
            {
                AppRoot.Instance.Log.Warning<AppBootstrap>("Restore account task failed.");
            }
        }

        private IEnumerator PrepareLoginRoutine()
        {
            AppRoot.Instance.Log.Info<AppBootstrap>("Prepare login page.");

            string pageName = AppRoot.Instance.Account != null && AppRoot.Instance.Account.IsLoggedIn
                ? AppPages.HomePage
                : AppPages.LoginPage;

            UIPanel loginPage = AppRoot.Instance.UI.OpenPage(pageName);

            if (loginPage == null)
            {
                AppRoot.Instance.Log.Error<AppBootstrap>($"Open {pageName} failed.");
                yield break;
            }

            AppRoot.Instance.UI.BringPageToFront(AppPages.LoadingPage);

            yield return WaitForPageOpened(loginPage);
        }

        private IEnumerator EnterAppRoutine()
        {
            AppRoot.Instance.Log.Info<AppBootstrap>("Enter app.");

            AppRoot.Instance.UI.BringPageToFront(AppPages.LoadingPage);

            yield return AppRoot.Instance.UI.ClosePageAsync(AppPages.LoadingPage);
        }

        private IEnumerator WaitForPageOpened(UIPanel page)
        {
            yield return null;

            while (page != null)
            {
                if (page.State == UIPanelState.Opened)
                {
                    yield break;
                }

                if (page.State == UIPanelState.Closed)
                {
                    AppRoot.Instance.Log.Warning<AppBootstrap>($"Wait page opened stopped. Page is closed: {page.PageName}");
                    yield break;
                }

                yield return null;
            }
        }
    }
}
