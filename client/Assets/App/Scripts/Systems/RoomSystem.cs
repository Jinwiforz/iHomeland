using System;
using System.Threading.Tasks;
using Ihomeland.Realtime.V1;
using UnityEngine;

namespace App.Systems
{
    public sealed class RoomSystem : AppSystemBase
    {
        private const string LastRoomIDKey = "ihomeland.room.last_room_id";
        private const string LastRoomPlayerIDKey = "ihomeland.room.last_player_id";
        private const uint MinRoomCapacity = 2;

        public event Action<RoomSnapshot> RoomSnapshotChanged;
        public event Action<string> MessageChanged;

        public RoomSnapshot CurrentRoom { get; private set; }

        public bool HasRoom => CurrentRoom != null && !string.IsNullOrWhiteSpace(CurrentRoom.RoomId);

        public bool IsBusy { get; private set; }

        public string LastRoomID { get; private set; }

        protected override void OnInitialize()
        {
            LastRoomID = PlayerPrefs.GetString(LastRoomIDKey, string.Empty);
            Root.Log.Info<RoomSystem>("Room system initialized.");
        }

        protected override void OnShutdown()
        {
            CurrentRoom = null;
            IsBusy = false;
            RoomSnapshotChanged = null;
            MessageChanged = null;

            Root.Log.Info<RoomSystem>("Room system shutdown.");
        }

        public async Task<RoomOperationResult> CreateRoomAsync(string roomName, uint capacity)
        {
            if (!TryGetCurrentPlayerID(out string playerID, out RoomOperationResult validationResult))
            {
                return validationResult;
            }

            string normalizedName = string.IsNullOrWhiteSpace(roomName) ? "Room" : roomName.Trim();
            uint normalizedCapacity = Math.Max(MinRoomCapacity, capacity);

            return await RunRoomOperationAsync(
                "Create room",
                async () =>
                {
                    CreateRoomResponse response = await Root.Network.CreateRoomAsync(playerID, normalizedName, normalizedCapacity);
                    ApplySnapshot(response.Room);
                    return RoomOperationResult.Ok("Room created. Start available.", CurrentRoom);
                }
            );
        }

        public async Task<RoomOperationResult> JoinRoomAsync(string roomID)
        {
            if (!TryGetCurrentPlayerID(out string playerID, out RoomOperationResult validationResult))
            {
                return validationResult;
            }

            if (string.IsNullOrWhiteSpace(roomID))
            {
                return RoomOperationResult.Fail("Room ID is empty.");
            }

            return await RunRoomOperationAsync(
                "Join room",
                async () =>
                {
                    JoinRoomResponse response = await Root.Network.JoinRoomAsync(playerID, roomID.Trim());
                    ApplySnapshot(response.Room);
                    return RoomOperationResult.Ok("Join room success.", CurrentRoom);
                }
            );
        }

        public async Task<RoomOperationResult> SetReadyAsync(bool ready)
        {
            if (!TryGetCurrentPlayerID(out string playerID, out RoomOperationResult validationResult))
            {
                return validationResult;
            }

            if (!TryGetCurrentRoomID(out string roomID, out validationResult))
            {
                return validationResult;
            }

            return await RunRoomOperationAsync(
                ready ? "Set ready" : "Cancel ready",
                async () =>
                {
                    SetReadyResponse response = await Root.Network.SetReadyAsync(playerID, roomID, ready);
                    ApplySnapshot(response.Room);
                    return RoomOperationResult.Ok(ready ? "Ready." : "Ready canceled.", CurrentRoom);
                }
            );
        }

        public async Task<RoomOperationResult> StartRoomAsync()
        {
            if (!TryGetCurrentPlayerID(out string playerID, out RoomOperationResult validationResult))
            {
                return validationResult;
            }

            if (!TryGetCurrentRoomID(out string roomID, out validationResult))
            {
                return validationResult;
            }

            return await RunRoomOperationAsync(
                "Start room",
                async () =>
                {
                    StartRoomResponse response = await Root.Network.StartRoomAsync(playerID, roomID);
                    ApplySnapshot(response.Room);
                    return RoomOperationResult.Ok("Room started.", CurrentRoom);
                }
            );
        }

        public async Task<RoomOperationResult> LeaveRoomAsync()
        {
            if (!TryGetCurrentPlayerID(out string playerID, out RoomOperationResult validationResult))
            {
                return validationResult;
            }

            if (!TryGetCurrentRoomID(out string roomID, out validationResult))
            {
                return validationResult;
            }

            return await RunRoomOperationAsync(
                "Leave room",
                async () =>
                {
                    await Root.Network.LeaveRoomAsync(playerID, roomID);
                    ClearLocalState(true);
                    return RoomOperationResult.Ok("Leave room success.", null);
                }
            );
        }

        public async Task<RoomOperationResult> TransferHostAsync(string targetPlayerID)
        {
            if (!TryGetCurrentPlayerID(out string playerID, out RoomOperationResult validationResult))
            {
                return validationResult;
            }

            if (!TryGetCurrentRoomID(out string roomID, out validationResult))
            {
                return validationResult;
            }

            if (string.IsNullOrWhiteSpace(targetPlayerID))
            {
                return RoomOperationResult.Fail("Target player ID is empty.");
            }

            return await RunRoomOperationAsync(
                "Transfer host",
                async () =>
                {
                    TransferHostResponse response = await Root.Network.TransferHostAsync(playerID, roomID, targetPlayerID.Trim());
                    ApplySnapshot(response.Room);
                    return RoomOperationResult.Ok("Transfer host success.", CurrentRoom);
                }
            );
        }

        public async Task<RoomOperationResult> ReconnectRoomAsync()
        {
            if (!TryGetCurrentPlayerID(out string playerID, out RoomOperationResult validationResult))
            {
                return validationResult;
            }

            if (string.IsNullOrWhiteSpace(LastRoomID))
            {
                return RoomOperationResult.Fail("No saved room to reconnect.");
            }

            return await RunRoomOperationAsync(
                "Reconnect room",
                async () =>
                {
                    ReconnectRoomResponse response = await Root.Network.ReconnectRoomAsync(playerID, LastRoomID);
                    ApplySnapshot(response.Room);
                    return RoomOperationResult.Ok("Reconnect room success.", CurrentRoom);
                },
                true
            );
        }

        public bool IsCurrentPlayerReady()
        {
            RoomMemberSnapshot member = FindCurrentPlayerMember();
            return member != null && member.Ready;
        }

        public bool IsCurrentPlayerHost()
        {
            if (CurrentRoom == null || Root == null || Root.Account == null)
            {
                return false;
            }

            if (SamePlayerID(CurrentRoom.HostPlayerId, Root.Account.CurrentPlayerID))
            {
                return true;
            }

            RoomMemberSnapshot member = FindCurrentPlayerMember();
            return member != null && member.Host;
        }

        public bool IsCurrentRoomStarted()
        {
            return CurrentRoom != null && CurrentRoom.State == RoomState.Started;
        }

        public RoomMemberSnapshot FindCurrentPlayerMember()
        {
            if (CurrentRoom == null || Root == null || Root.Account == null)
            {
                return null;
            }

            string playerID = Root.Account.CurrentPlayerID;
            if (string.IsNullOrWhiteSpace(playerID))
            {
                return null;
            }

            foreach (RoomMemberSnapshot member in CurrentRoom.Members)
            {
                if (member != null && SamePlayerID(member.PlayerId, playerID))
                {
                    return member;
                }
            }

            return null;
        }

        public void ClearLocalState(bool clearSavedContext)
        {
            CurrentRoom = null;
            IsBusy = false;

            if (clearSavedContext)
            {
                LastRoomID = string.Empty;
                PlayerPrefs.DeleteKey(LastRoomIDKey);
                PlayerPrefs.DeleteKey(LastRoomPlayerIDKey);
                PlayerPrefs.Save();
            }

            RoomSnapshotChanged?.Invoke(CurrentRoom);
        }

        private void ApplySnapshot(RoomSnapshot snapshot)
        {
            if (snapshot == null || string.IsNullOrWhiteSpace(snapshot.RoomId))
            {
                CurrentRoom = null;
                RoomSnapshotChanged?.Invoke(CurrentRoom);
                return;
            }

            CurrentRoom = snapshot.Clone();
            SaveReconnectContext(CurrentRoom.RoomId);
            RoomSnapshotChanged?.Invoke(CurrentRoom);
        }

        private void SaveReconnectContext(string roomID)
        {
            if (Root == null || Root.Account == null || string.IsNullOrWhiteSpace(roomID))
            {
                return;
            }

            LastRoomID = roomID;
            PlayerPrefs.SetString(LastRoomIDKey, roomID);
            PlayerPrefs.SetString(LastRoomPlayerIDKey, Root.Account.CurrentPlayerID ?? string.Empty);
            PlayerPrefs.Save();
        }

        private async Task<RoomOperationResult> RunRoomOperationAsync(string operation, Func<Task<RoomOperationResult>> action, bool clearContextOnFailure = false)
        {
            if (IsBusy)
            {
                return RoomOperationResult.Fail("Room operation is already running.");
            }

            IsBusy = true;
            NotifyMessage($"{operation}...");

            try
            {
                RoomOperationResult result = await action();
                NotifyMessage(result.Message);
                return result;
            }
            catch (Exception exception)
            {
                if (clearContextOnFailure)
                {
                    ClearLocalState(true);
                }

                RoomOperationResult result = RoomOperationResult.Fail(exception);
                NotifyMessage(result.Message);
                Root.Log.Warning<RoomSystem>($"{operation} failed. {result.ToDiagnosticLine()}");
                return result;
            }
            finally
            {
                IsBusy = false;
            }
        }

        private bool TryGetCurrentPlayerID(out string playerID, out RoomOperationResult result)
        {
            playerID = string.Empty;

            if (Root == null || Root.Account == null || !Root.Account.IsLoggedIn)
            {
                result = RoomOperationResult.Fail("Please login first.");
                return false;
            }

            playerID = Root.Account.CurrentPlayerID;
            if (string.IsNullOrWhiteSpace(playerID))
            {
                result = RoomOperationResult.Fail("Player identity is missing. Please login again.");
                return false;
            }

            result = default;
            return true;
        }

        private bool TryGetCurrentRoomID(out string roomID, out RoomOperationResult result)
        {
            roomID = CurrentRoom == null ? string.Empty : CurrentRoom.RoomId;
            if (string.IsNullOrWhiteSpace(roomID))
            {
                result = RoomOperationResult.Fail("No current room.");
                return false;
            }

            result = default;
            return true;
        }

        private void NotifyMessage(string message)
        {
            if (!string.IsNullOrWhiteSpace(message))
            {
                MessageChanged?.Invoke(message);
            }
        }

        private static bool SamePlayerID(string left, string right)
        {
            return string.Equals((left ?? string.Empty).Trim(), (right ?? string.Empty).Trim(), StringComparison.Ordinal);
        }
    }

    public readonly struct RoomOperationResult
    {
        public bool Success { get; }
        public string Message { get; }
        public ErrorCode Code { get; }
        public string Detail { get; }
        public NetworkErrorKind NetworkKind { get; }
        public RoomSnapshot Room { get; }

        private RoomOperationResult(bool success, string message, ErrorCode code, string detail, NetworkErrorKind networkKind, RoomSnapshot room)
        {
            Success = success;
            Message = string.IsNullOrWhiteSpace(message) ? "Room operation completed." : message;
            Code = code;
            Detail = detail ?? string.Empty;
            NetworkKind = networkKind;
            Room = room;
        }

        public static RoomOperationResult Ok(string message, RoomSnapshot room)
        {
            return new RoomOperationResult(true, message, ErrorCode.Unspecified, string.Empty, NetworkErrorKind.Unknown, room);
        }

        public static RoomOperationResult Fail(string message)
        {
            return new RoomOperationResult(false, message, ErrorCode.Unspecified, string.Empty, NetworkErrorKind.InvalidRequest, null);
        }

        public static RoomOperationResult Fail(Exception exception)
        {
            if (exception is NetworkRequestException networkException)
            {
                return new RoomOperationResult(
                    false,
                    networkException.Message,
                    networkException.Code,
                    networkException.Detail,
                    networkException.Kind,
                    null
                );
            }

            return new RoomOperationResult(false, exception.Message, ErrorCode.Unspecified, string.Empty, NetworkErrorKind.Unknown, null);
        }

        public string ToDiagnosticLine()
        {
            return $"code={Code}, message={Message}, detail={Detail}, network_kind={NetworkKind}";
        }
    }
}
