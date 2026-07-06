using System;
using System.Collections;
using UnityEngine;
using UnityEngine.SceneManagement;

namespace App.Systems
{
    public sealed class SceneSystem : AppSystemBase
    {
        public string CurrentSceneName { get; private set; }

        protected override void OnInitialize()
        {
            CurrentSceneName = SceneManager.GetActiveScene().name;

            Root.Log.Info<SceneSystem>($"Scene system initialized. Current scene: {CurrentSceneName}");
        }

        public void LoadScene(string sceneName)
        {
            if (string.IsNullOrWhiteSpace(sceneName))
            {
                Root.Log.Error<SceneSystem>("Load scene failed. Scene name is null or empty.");
                return;
            }

            Root.StartCoroutine(LoadSceneAsync(sceneName));
        }

        public IEnumerator LoadSceneAsync(string sceneName, Action<float> onProgress = null)
        {
            if (string.IsNullOrWhiteSpace(sceneName))
            {
                Root.Log.Error<SceneSystem>("Load scene failed. Scene name is null or empty.");
                yield break;
            }

            Root.Log.Info<SceneSystem>($"Load scene started: {sceneName}");

            AsyncOperation operation = SceneManager.LoadSceneAsync(sceneName, LoadSceneMode.Single);

            if (operation == null)
            {
                Root.Log.Error<SceneSystem>($"Load scene failed. AsyncOperation is null. Scene: {sceneName}");
                yield break;
            }

            while (!operation.isDone)
            {
                float progress = Mathf.Clamp01(operation.progress / 0.9f);

                onProgress?.Invoke(progress);

                yield return null;
            }

            CurrentSceneName = SceneManager.GetActiveScene().name;

            onProgress?.Invoke(1f);

            Root.Log.Info<SceneSystem>($"Load scene completed: {CurrentSceneName}");
        }
    }
}