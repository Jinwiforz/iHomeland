using App.Systems;
using UnityEngine;

namespace App.Core
{
    public sealed class AppRoot : MonoBehaviour
    {
        public static AppRoot Instance { get; private set; }

        public LogSystem Log { get; private set; }

        public AssetSystem Asset { get; private set; }

        public SceneSystem Scene { get; private set; }

        public UISystem UI { get; private set; }

        public AudioSystem Audio { get; private set; }

        public NetworkSystem Network { get; private set; }

        public AccountSystem Account { get; private set; }

        public bool IsInitialized { get; private set; }

        private bool _isQuitting;

        private void Awake()
        {
            if (Instance != null && Instance != this)
            {
                Destroy(gameObject);
                return;
            }

            Instance = this;
            DontDestroyOnLoad(gameObject);
            CreateSystems();
        }

        private void Start()
        {
            AppBootstrap bootstrap = gameObject.AddComponent<AppBootstrap>();
            bootstrap.Run();
        }

        private void Update()
        {
            if (!IsInitialized)
            {
                return;
            }

            float deltaTime = Time.deltaTime;

            Log.Tick(deltaTime);
            Asset.Tick(deltaTime);
            Scene.Tick(deltaTime);
            UI.Tick(deltaTime);
            Audio.Tick(deltaTime);
            Network.Tick(deltaTime);
            Account.Tick(deltaTime);
        }

        private void OnApplicationQuit()
        {
            _isQuitting = true;
            Shutdown();
        }

        private void OnDestroy()
        {
            if (_isQuitting)
            {
                return;
            }

            if (Instance == this)
            {
                Shutdown();
                Instance = null;
            }
        }

        private void CreateSystems()
        {
            Log = gameObject.AddComponent<LogSystem>();
            Asset = gameObject.AddComponent<AssetSystem>();
            Scene = gameObject.AddComponent<SceneSystem>();
            UI = gameObject.AddComponent<UISystem>();
            Audio = gameObject.AddComponent<AudioSystem>();
            Network = gameObject.AddComponent<NetworkSystem>();
            Account = gameObject.AddComponent<AccountSystem>();
        }

        public void Initialize()
        {
            if (IsInitialized)
            {
                return;
            }

            Log.Initialize(this);
            Log.Info<AppRoot>("App initializing.");

            Asset.Initialize(this);
            Scene.Initialize(this);
            UI.Initialize(this);
            Audio.Initialize(this);
            Network.Initialize(this);
            Account.Initialize(this);

            IsInitialized = true;

            Log.Info<AppRoot>("App initialized.");
        }

        public void Shutdown()
        {
            if (!IsInitialized)
            {
                return;
            }

            Log.Info<AppRoot>("App shutdown started.");

            Account.Shutdown();
            Network.Shutdown();
            Audio.Shutdown();
            UI.Shutdown();
            Scene.Shutdown();
            Asset.Shutdown();

            Log.Info<AppRoot>("App shutdown finished.");
            Log.Shutdown();

            IsInitialized = false;
        }
    }
}
