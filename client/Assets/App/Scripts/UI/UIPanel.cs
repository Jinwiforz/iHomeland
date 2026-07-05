using System.Collections;
using App.Core;
using App.Systems;
using UnityEngine;

namespace App.UI
{
    public enum UIPanelState
    {
        Closed,
        Opening,
        Opened,
        Closing
    }

    public abstract class UIPanel : MonoBehaviour
    {
        protected UISystem UI { get; private set; }

        public string PageName { get; private set; }

        public bool IsInitialized { get; private set; }

        public UIPanelState State { get; private set; } = UIPanelState.Closed;

        public bool IsVisible => State == UIPanelState.Opening || State == UIPanelState.Opened;

        public void Initialize(UISystem uiSystem, string pageName)
        {
            if (IsInitialized)
            {
                return;
            }

            UI = uiSystem;
            PageName = string.IsNullOrWhiteSpace(pageName) ? GetType().Name : pageName;
            IsInitialized = true;

            AppRoot.Instance.Log.Info("Initialize.", GetType().Name);
            OnInitialize();
        }

        public IEnumerator OpenRoutine()
        {
            if (State == UIPanelState.Opening || State == UIPanelState.Opened)
            {
                yield break;
            }

            if (!IsInitialized)
            {
                Initialize(UI, PageName);
            }

            State = UIPanelState.Opening;
            gameObject.SetActive(true);

            AppRoot.Instance.Log.Info("Open started.", GetType().Name);

            yield return OnOpenRoutine();

            OnShow();

            State = UIPanelState.Opened;

            AppRoot.Instance.Log.Info("Open completed.", GetType().Name);
        }

        public IEnumerator CloseRoutine()
        {
            if (State == UIPanelState.Closed || State == UIPanelState.Closing)
            {
                yield break;
            }

            State = UIPanelState.Closing;

            AppRoot.Instance.Log.Info("Close started.", GetType().Name);

            yield return OnCloseRoutine();

            OnHide();

            gameObject.SetActive(false);

            State = UIPanelState.Closed;

            AppRoot.Instance.Log.Info("Close completed.", GetType().Name);
        }

        public void Show()
        {
            StartCoroutine(OpenRoutine());
        }

        public void Hide()
        {
            StartCoroutine(CloseRoutine());
        }

        public void Close()
        {
            if (UI == null)
            {
                AppRoot.Instance.Log.Warning("Close failed. UISystem is null.", GetType().Name);
                return;
            }

            UI.ClosePage(PageName);
        }

        public void DestroySelf()
        {
            if (UI == null)
            {
                AppRoot.Instance.Log.Warning("Destroy self failed. UISystem is null.", GetType().Name);
                return;
            }

            UI.DestroyPage(PageName);
        }

        protected virtual void OnInitialize()
        {
        }

        protected virtual IEnumerator OnOpenRoutine()
        {
            yield break;
        }

        protected virtual IEnumerator OnCloseRoutine()
        {
            yield break;
        }

        protected virtual void OnShow()
        {
        }

        protected virtual void OnHide()
        {
        }
    }
}