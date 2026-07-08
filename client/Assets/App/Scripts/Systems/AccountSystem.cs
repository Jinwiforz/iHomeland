using System;
using System.Threading.Tasks;
using Ihomeland.Realtime.V1;
using UnityEngine;

namespace App.Systems
{
    public sealed class AccountSystem : AppSystemBase
    {
        private const string SessionTokenKey = "ihomeland.account.session_token";
        private const string AccountKey = "ihomeland.account.account";
        private const string PlayerIDKey = "ihomeland.account.player_id";
        private const string DisplayNameKey = "ihomeland.account.display_name";

        public bool IsLoggedIn { get; private set; }

        public string CurrentAccount { get; private set; }

        public string CurrentPlayerID { get; private set; }

        public string DisplayName { get; private set; }

        public string SessionToken { get; private set; }

        protected override void OnInitialize()
        {
            IsLoggedIn = false;
            CurrentAccount = string.Empty;
            CurrentPlayerID = string.Empty;
            DisplayName = string.Empty;
            SessionToken = string.Empty;

            Root.Log.Info<AccountSystem>("Account system initialized.");
        }

        protected override void OnShutdown()
        {
            ClearSession(false);

            Root.Log.Info<AccountSystem>("Account system shutdown.");
        }

        public async Task<AccountOperationResult> RegisterAsync(string account, string password)
        {
            if (!ValidateCredentials(account, password, out string validationMessage))
            {
                return AccountOperationResult.Fail(validationMessage);
            }

            try
            {
                RegisterResponse response = await Root.Network.RegisterAsync(account.Trim(), password.Trim(), account.Trim());
                ApplySession(response.Player, response.SessionToken);
                Root.Log.Info<AccountSystem>($"Register success. Account: {CurrentAccount}");
                return AccountOperationResult.Ok("Register success.");
            }
            catch (Exception ex)
            {
                ClearSession(false);
                return AccountOperationResult.Fail(ex);
            }
        }

        public async Task<AccountOperationResult> LoginAsync(string account, string password)
        {
            if (!ValidateCredentials(account, password, out string validationMessage))
            {
                return AccountOperationResult.Fail(validationMessage);
            }

            try
            {
                LoginResponse response = await Root.Network.LoginAsync(account.Trim(), password.Trim());
                ApplySession(response.Player, response.SessionToken);
                Root.Log.Info<AccountSystem>($"Login success. Account: {CurrentAccount}");
                return AccountOperationResult.Ok("Login success.");
            }
            catch (Exception ex)
            {
                ClearSession(false);
                return AccountOperationResult.Fail(ex);
            }
        }

        public async Task<AccountOperationResult> LogoutAsync()
        {
            if (!IsLoggedIn)
            {
                ClearSession(true);
                await DisconnectAfterLogoutAsync();
                return AccountOperationResult.Ok("Logout success.");
            }

            AccountOperationResult result;
            try
            {
                await Root.Network.LogoutAsync(SessionToken);
                Root.Log.Info<AccountSystem>($"Logout. Account: {CurrentAccount}");
                result = AccountOperationResult.Ok("Logout success.");
            }
            catch (Exception ex)
            {
                result = AccountOperationResult.Fail(ex);
            }

            // 玩家主动退出登录时，客户端账号态以本地选择为准；即使服务端响应失败，也要清理本地 token 并断开连接。
            ClearSession(true);
            await DisconnectAfterLogoutAsync();
            return result;
        }

        public async Task<AccountOperationResult> RestoreSessionAsync()
        {
            string token = PlayerPrefs.GetString(SessionTokenKey, string.Empty);
            if (string.IsNullOrWhiteSpace(token))
            {
                return AccountOperationResult.Fail("No saved session.");
            }

            try
            {
                ResumeSessionResponse response = await Root.Network.ResumeSessionAsync(token);
                ApplySession(response.Player, response.SessionToken);
                Root.Log.Info<AccountSystem>($"Restore session success. Account: {CurrentAccount}");
                return AccountOperationResult.Ok("Restore session success.");
            }
            catch (Exception ex)
            {
                ClearSession(true);
                return AccountOperationResult.Fail(ex);
            }
        }

        private bool ValidateCredentials(string account, string password, out string message)
        {
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

            message = string.Empty;
            return true;
        }

        private void ApplySession(PlayerProfile player, string sessionToken)
        {
            IsLoggedIn = true;
            CurrentPlayerID = player == null ? string.Empty : player.PlayerId;
            CurrentAccount = player == null ? string.Empty : player.Account;
            DisplayName = player == null ? string.Empty : player.DisplayName;
            SessionToken = sessionToken ?? string.Empty;

            PlayerPrefs.SetString(SessionTokenKey, SessionToken);
            PlayerPrefs.SetString(AccountKey, CurrentAccount);
            PlayerPrefs.SetString(PlayerIDKey, CurrentPlayerID);
            PlayerPrefs.SetString(DisplayNameKey, DisplayName);
            PlayerPrefs.Save();
        }

        private void ClearSession(bool clearSaved)
        {
            IsLoggedIn = false;
            CurrentAccount = string.Empty;
            CurrentPlayerID = string.Empty;
            DisplayName = string.Empty;
            SessionToken = string.Empty;

            if (Root != null && Root.Room != null)
            {
                Root.Room.ClearLocalState(clearSaved);
            }

            if (!clearSaved)
            {
                return;
            }

            PlayerPrefs.DeleteKey(SessionTokenKey);
            PlayerPrefs.DeleteKey(AccountKey);
            PlayerPrefs.DeleteKey(PlayerIDKey);
            PlayerPrefs.DeleteKey(DisplayNameKey);
            PlayerPrefs.Save();
        }

        private async Task DisconnectAfterLogoutAsync()
        {
            try
            {
                await Root.Network.DisconnectAsync();
            }
            catch (Exception ex)
            {
                Root.Log.Warning<AccountSystem>($"Disconnect after logout failed. reason={ex.Message}");
            }
        }
    }

    /// <summary>
    /// AccountOperationResult 是 UI 和账号系统之间的结果边界：UI 使用 Message 展示，日志使用 code/message/detail 诊断。
    /// </summary>
    public readonly struct AccountOperationResult
    {
        public bool Success { get; }
        public string Message { get; }
        public ErrorCode Code { get; }
        public string Detail { get; }
        public NetworkErrorKind NetworkKind { get; }

        private AccountOperationResult(bool success, string message, ErrorCode code, string detail, NetworkErrorKind networkKind)
        {
            Success = success;
            Message = message;
            Code = code;
            Detail = detail ?? string.Empty;
            NetworkKind = networkKind;
        }

        public static AccountOperationResult Ok(string message)
        {
            return new AccountOperationResult(true, message, ErrorCode.Unspecified, string.Empty, NetworkErrorKind.Unknown);
        }

        public static AccountOperationResult Fail(string message)
        {
            return new AccountOperationResult(false, message, ErrorCode.Unspecified, string.Empty, NetworkErrorKind.InvalidRequest);
        }

        public static AccountOperationResult Fail(Exception exception)
        {
            if (exception is NetworkRequestException networkException)
            {
                return new AccountOperationResult(
                    false,
                    networkException.Message,
                    networkException.Code,
                    networkException.Detail,
                    networkException.Kind
                );
            }

            return new AccountOperationResult(false, exception.Message, ErrorCode.Unspecified, string.Empty, NetworkErrorKind.Unknown);
        }

        public string ToDiagnosticLine()
        {
            return $"code={Code}, message={Message}, detail={Detail}, network_kind={NetworkKind}";
        }
    }
}
