using System.Collections;
using App.Core;
using TMPro;
using UnityEngine;
using UnityEngine.UI;

namespace App.UI
{
    public sealed class LoadingPage : UIPanel
    {
        [Header("UI References")] [SerializeField]
        private Slider progressSlider;

        [SerializeField] private TMP_Text progressText;
        [SerializeField] private TMP_Text messageText;
        [SerializeField] private TMP_Text versionText;
        [SerializeField] private CanvasGroup canvasGroup;

        [Header("Animation")] [SerializeField] private float fadeInDuration = 0.2f;
        [SerializeField] private float fadeOutDuration = 0.2f;

        private float _progress;
        private string _message;

        protected override void OnInitialize()
        {
            EnsureCanvasGroup();
            SetProgress(0f);
            SetMessage("Loading...");
            SetVersion(AppConfig.Version);

            AppRoot.Instance.Log.Info<LoadingPage>("Initialize.");
        }

        protected override IEnumerator OnOpenRoutine()
        {
            AppRoot.Instance.Log.Info<LoadingPage>("Open routine started.");

            yield return FadeTo(1f, fadeInDuration);

            AppRoot.Instance.Log.Info<LoadingPage>("Open routine completed.");
        }

        protected override void OnShow()
        {
            SetProgress(_progress);
            SetMessage(_message);

            AppRoot.Instance.Log.Info<LoadingPage>("Show.");
        }

        protected override IEnumerator OnCloseRoutine()
        {
            AppRoot.Instance.Log.Info<LoadingPage>("Close routine started.");

            yield return FadeTo(0f, fadeOutDuration);

            AppRoot.Instance.Log.Info<LoadingPage>("Close routine completed.");
        }

        protected override void OnHide()
        {
            AppRoot.Instance.Log.Info<LoadingPage>("Hide.");
        }

        public void SetProgress(float progress)
        {
            _progress = Mathf.Clamp01(progress);

            if (progressSlider != null)
            {
                progressSlider.value = _progress;
            }

            if (progressText != null)
            {
                int percent = Mathf.RoundToInt(_progress * 100f);
                progressText.text = $"{percent}%";
            }
        }

        public void SetMessage(string message)
        {
            _message = string.IsNullOrWhiteSpace(message) ? "Loading..." : message;

            if (messageText != null)
            {
                messageText.text = _message;
            }
        }

        public void SetVersion(string version)
        {
            if (versionText == null)
            {
                return;
            }

            if (string.IsNullOrWhiteSpace(version))
            {
                versionText.text = string.Empty;
                return;
            }

            versionText.text = $"Version {version}";
        }

        public void ResetProgress()
        {
            SetProgress(0f);
            SetMessage("Loading...");
        }

        private void EnsureCanvasGroup()
        {
            if (canvasGroup != null)
            {
                return;
            }

            canvasGroup = GetComponent<CanvasGroup>();

            if (canvasGroup == null)
            {
                canvasGroup = gameObject.AddComponent<CanvasGroup>();
            }
        }

        private IEnumerator FadeTo(float targetAlpha, float duration)
        {
            EnsureCanvasGroup();

            if (duration <= 0f)
            {
                canvasGroup.alpha = targetAlpha;
                yield break;
            }

            float startAlpha = canvasGroup.alpha;
            float time = 0f;

            while (time < duration)
            {
                time += Time.deltaTime;

                float progress = Mathf.Clamp01(time / duration);

                canvasGroup.alpha = Mathf.Lerp(startAlpha, targetAlpha, progress);

                yield return null;
            }

            canvasGroup.alpha = targetAlpha;
        }
    }
}