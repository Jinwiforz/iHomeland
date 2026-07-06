namespace App.Systems
{
    public sealed class AccountSystem : AppSystemBase
    {
        public bool IsLoggedIn { get; private set; }

        public string CurrentAccount { get; private set; }

        protected override void OnInitialize()
        {
            IsLoggedIn = false;
            CurrentAccount = string.Empty;

            Root.Log.Info<AccountSystem>("Account system initialized.");
        }

        protected override void OnShutdown()
        {
            Logout();

            Root.Log.Info<AccountSystem>("Account system shutdown.");
        }

        public bool TryLogin(string account, string password, out string message)
        {
            account = account?.Trim() ?? string.Empty;
            password = password?.Trim() ?? string.Empty;

            if (string.IsNullOrWhiteSpace(account))
            {
                message = "Account is empty.";
                return false;
            }

            if (string.IsNullOrWhiteSpace(password))
            {
                message = "Password is empty.";
                return false;
            }

            IsLoggedIn = true;
            CurrentAccount = account;
            message = "Login success.";

            Root.Log.Info<AccountSystem>($"Fake login success. Account: {account}");

            return true;
        }

        public void Logout()
        {
            if (!IsLoggedIn)
            {
                CurrentAccount = string.Empty;
                return;
            }

            Root.Log.Info<AccountSystem>($"Logout. Account: {CurrentAccount}");

            IsLoggedIn = false;
            CurrentAccount = string.Empty;
        }
    }
}