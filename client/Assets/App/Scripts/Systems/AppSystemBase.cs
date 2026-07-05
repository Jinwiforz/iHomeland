using App.Core;
using UnityEngine;

namespace App.Systems
{
    public abstract class AppSystemBase : MonoBehaviour, IAppSystem
    {
        protected AppRoot Root { get; private set; }

        public bool IsInitialized { get; private set; }

        public void Initialize(AppRoot root)
        {
            if (IsInitialized)
            {
                return;
            }

            Root = root;

            OnInitialize();

            IsInitialized = true;
        }

        public void Tick(float deltaTime)
        {
            if (!IsInitialized)
            {
                return;
            }

            OnTick(deltaTime);
        }

        public void Shutdown()
        {
            if (!IsInitialized)
            {
                return;
            }

            OnShutdown();

            IsInitialized = false;
            Root = null;
        }

        protected virtual void OnInitialize()
        {
        }

        protected virtual void OnTick(float deltaTime)
        {
        }

        protected virtual void OnShutdown()
        {
        }
    }
}