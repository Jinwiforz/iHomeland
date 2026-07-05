using UnityEngine;

namespace App.Systems
{
    public sealed class AssetSystem : AppSystemBase
    {
        public T Load<T>(string path) where T : Object
        {
            if (string.IsNullOrWhiteSpace(path))
            {
                Root.Log.Error<AssetSystem>("Load failed. Path is null or empty.");
                return null;
            }

            T asset = Resources.Load<T>(path);

            if (asset == null)
            {
                Root.Log.Error<AssetSystem>($"Load failed. Path: {path}");
                return null;
            }

            Root.Log.Debug<AssetSystem>($"Load success. Path: {path}");

            return asset;
        }

        public GameObject Instantiate(string path, Transform parent = null)
        {
            GameObject prefab = Load<GameObject>(path);

            if (prefab == null)
            {
                return null;
            }

            GameObject instance = Object.Instantiate(prefab, parent);

            return instance;
        }

        public T Instantiate<T>(string path, Transform parent = null) where T : Component
        {
            GameObject obj = Instantiate(path, parent);

            if (obj == null)
            {
                return null;
            }

            T component = obj.GetComponent<T>();

            if (component == null)
            {
                Root.Log.Error<AssetSystem>($"Component {typeof(T).Name} not found on prefab: {path}");
                return null;
            }

            return component;
        }
    }
}