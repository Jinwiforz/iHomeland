using App.Core;

namespace App.Systems
{
    public interface IAppSystem
    {
        bool IsInitialized { get; }

        void Initialize(AppRoot root);
        void Tick(float deltaTime);
        void Shutdown();
    }
}