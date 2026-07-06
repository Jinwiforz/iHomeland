using System.Collections;
using System.Collections.Generic;
using App.Core;
using App.UI;
using UnityEngine;
using UnityEngine.EventSystems;
using UnityEngine.InputSystem.UI;
using UnityEngine.UI;

namespace App.Systems
{
    public sealed class UISystem : AppSystemBase
    {
        private Canvas _canvas;
        private RectTransform _uiRoot;
        private EventSystem _eventSystem;
        private bool _createdEventSystem;

        private readonly Dictionary<string, UIPanel> _pages = new();

        protected override void OnInitialize()
        {
            CreateUIRoot();
            CreateEventSystemIfNeeded();
            Root.Log.Info<UISystem>("UI system initialized.");
        }

        protected override void OnShutdown()
        {
            foreach (UIPanel page in _pages.Values)
            {
                if (page != null)
                {
                    Destroy(page.gameObject);
                }
            }

            _pages.Clear();

            if (_canvas != null)
            {
                Destroy(_canvas.gameObject);
            }

            if (_createdEventSystem && _eventSystem != null)
            {
                Destroy(_eventSystem.gameObject);
            }

            _canvas = null;
            _uiRoot = null;
            _eventSystem = null;
            _createdEventSystem = false;

            Root.Log.Info<UISystem>("UI system shutdown.");
        }

        public UIPanel OpenPage(string pageName)
        {
            UIPanel page = GetOrCreatePage(pageName);

            if (page == null)
            {
                return null;
            }

            StartCoroutine(OpenPageRoutine(page));

            return page;
        }

        public T OpenPage<T>(string pageName) where T : UIPanel
        {
            UIPanel page = OpenPage(pageName);

            return page as T;
        }

        public IEnumerator OpenPageAsync(string pageName)
        {
            UIPanel page = GetOrCreatePage(pageName);

            if (page == null)
            {
                yield break;
            }

            yield return OpenPageRoutine(page);
        }

        public IEnumerator OpenPageAsync<T>(string pageName) where T : UIPanel
        {
            yield return OpenPageAsync(pageName);
        }

        public void ClosePage(string pageName)
        {
            if (!TryGetPage(pageName, out UIPanel page))
            {
                return;
            }

            StartCoroutine(ClosePageRoutine(page));
        }

        public IEnumerator ClosePageAsync(string pageName)
        {
            if (!TryGetPage(pageName, out UIPanel page))
            {
                yield break;
            }

            yield return ClosePageRoutine(page);
        }

        public void DestroyPage(string pageName)
        {
            StartCoroutine(DestroyPageRoutine(pageName));
        }

        public IEnumerator DestroyPageAsync(string pageName)
        {
            yield return DestroyPageRoutine(pageName);
        }

        public bool IsPageCached(string pageName)
        {
            return !string.IsNullOrWhiteSpace(pageName) && _pages.ContainsKey(pageName);
        }

        public void BringPageToFront(string pageName)
        {
            if (!TryGetPage(pageName, out UIPanel page))
            {
                return;
            }

            page.transform.SetAsLastSibling();
        }

        public bool TryGetPage(string pageName, out UIPanel page)
        {
            page = null;

            if (string.IsNullOrWhiteSpace(pageName))
            {
                Root.Log.Warning<UISystem>("Try get page failed. Page name is null or empty.");
                return false;
            }

            if (!_pages.TryGetValue(pageName, out page))
            {
                return false;
            }

            if (page == null)
            {
                _pages.Remove(pageName);
                return false;
            }

            return true;
        }

        private UIPanel GetOrCreatePage(string pageName)
        {
            if (string.IsNullOrWhiteSpace(pageName))
            {
                Root.Log.Error<UISystem>("Open page failed. Page name is null or empty.");
                return null;
            }

            if (_pages.TryGetValue(pageName, out UIPanel cachedPage))
            {
                if (cachedPage != null)
                {
                    return cachedPage;
                }

                _pages.Remove(pageName);
            }

            string path = AppConfig.UIPagePath + pageName;

            UIPanel page = Root.Asset.Instantiate<UIPanel>(path, _uiRoot);

            if (page == null)
            {
                Root.Log.Error<UISystem>($"Open page failed. Page: {pageName}, path: {path}");
                return null;
            }

            page.name = pageName;
            page.gameObject.SetActive(false);
            page.Initialize(this, pageName);

            _pages.Add(pageName, page);

            Root.Log.Info<UISystem>($"Create page: {pageName}");

            return page;
        }

        private IEnumerator OpenPageRoutine(UIPanel page)
        {
            if (page == null)
            {
                yield break;
            }

            page.transform.SetAsLastSibling();

            Root.Log.Info<UISystem>($"Open page started: {page.PageName}");

            yield return page.OpenRoutine();

            Root.Log.Info<UISystem>($"Open page completed: {page.PageName}");
        }

        private IEnumerator ClosePageRoutine(UIPanel page)
        {
            if (page == null)
            {
                yield break;
            }

            Root.Log.Info<UISystem>($"Close page started: {page.PageName}");

            yield return page.CloseRoutine();

            Root.Log.Info<UISystem>($"Close page completed: {page.PageName}");
        }

        private IEnumerator DestroyPageRoutine(string pageName)
        {
            if (!TryGetPage(pageName, out UIPanel page))
            {
                yield break;
            }

            _pages.Remove(pageName);

            Root.Log.Info<UISystem>($"Destroy page started: {pageName}");

            yield return page.CloseRoutine();

            if (page != null)
            {
                Destroy(page.gameObject);
            }

            Root.Log.Info<UISystem>($"Destroy page completed: {pageName}");
        }

        private void CreateUIRoot()
        {
            GameObject canvasObject = new GameObject("UIRoot");

            canvasObject.transform.SetParent(Root.transform, false);

            _canvas = canvasObject.AddComponent<Canvas>();
            _canvas.renderMode = RenderMode.ScreenSpaceOverlay;

            CanvasScaler scaler = canvasObject.AddComponent<CanvasScaler>();
            scaler.uiScaleMode = CanvasScaler.ScaleMode.ScaleWithScreenSize;
            scaler.referenceResolution = new Vector2(1920, 1080);
            scaler.matchWidthOrHeight = 0.5f;

            canvasObject.AddComponent<GraphicRaycaster>();

            _uiRoot = canvasObject.GetComponent<RectTransform>();
            _uiRoot.anchorMin = Vector2.zero;
            _uiRoot.anchorMax = Vector2.one;
            _uiRoot.offsetMin = Vector2.zero;
            _uiRoot.offsetMax = Vector2.zero;
        }

        private void CreateEventSystemIfNeeded()
        {
            _eventSystem = Object.FindAnyObjectByType<EventSystem>();

            if (_eventSystem != null)
            {
                _createdEventSystem = false;
                return;
            }

            GameObject eventSystemObject = new GameObject("EventSystem");

            eventSystemObject.transform.SetParent(Root.transform, false);

            _eventSystem = eventSystemObject.AddComponent<EventSystem>();

            eventSystemObject.AddComponent<InputSystemUIInputModule>();

            _createdEventSystem = true;
        }
    }
}