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
        }

        public void LoadScene(string sceneName)
        {
            if (string.IsNullOrWhiteSpace(sceneName))
            {
                Root.Log.Error<SceneSystem>("Scene name is null or empty.");
                return;
            }

            Root.StartCoroutine(LoadSceneRoutine(sceneName));
        }

        private IEnumerator LoadSceneRoutine(string sceneName)
        {
            Root.Log.Debug<SceneSystem>($"Loading scene: {sceneName}");

            AsyncOperation operation = SceneManager.LoadSceneAsync(sceneName);

            if (operation == null)
            {
                Root.Log.Error<SceneSystem>($"LoadSceneAsync failed: {sceneName}");
                yield break;
            }

            while (!operation.isDone)
            {
                yield return null;
            }

            CurrentSceneName = sceneName;
            Root.Log.Debug<SceneSystem>($"Scene loaded: {sceneName}");
        }
    }
}