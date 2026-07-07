namespace App.Core
{
    public static class AppConfig
    {
        public const string ProductName = "iHomeland";
        public const string Version = "0.1.0";

        public const string WebSocketURL = "ws://127.0.0.1:8080/ws";
        public const int NetworkConnectTimeoutSeconds = 5;
        public const int NetworkRequestTimeoutSeconds = 10;
        public const int NetworkCloseTimeoutSeconds = 2;
        public const int NetworkHeartbeatIntervalSeconds = 10;

        public const string UIPagePath = "UI/Pages/";
        public const string BgmPath = "Audio/BGM/";
        public const string SfxPath = "Audio/SFX/";
    }
}
