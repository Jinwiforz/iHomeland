using System.Text;
using App.Core;
using App.Systems;
using Ihomeland.Realtime.V1;
using TMPro;
using UnityEngine;
using UnityEngine.UI;

namespace App.UI
{
    public sealed class RoomPage : UIPanel
    {
        [Header("Inputs")]
        [SerializeField] private TMP_InputField roomNameInput;
        [SerializeField] private TMP_InputField capacityInput;
        [SerializeField] private TMP_InputField joinRoomIDInput;
        [SerializeField] private TMP_InputField transferHostPlayerIDInput;

        [Header("Actions")]
        [SerializeField] private Button createRoomButton;
        [SerializeField] private Button joinRoomButton;
        [SerializeField] private Button reconnectRoomButton;
        [SerializeField] private Button readyButton;
        [SerializeField] private Button leaveRoomButton;
        [SerializeField] private Button transferHostButton;
        [SerializeField] private Button backButton;

        [Header("Output")]
        [SerializeField] private TMP_Text messageText;
        [SerializeField] private TMP_Text roomInfoText;
        [SerializeField] private TMP_Text memberListText;

        private bool _operationRunning;

        protected override void OnInitialize()
        {
            AddListeners();

            AppRoot.Instance.Log.Info<RoomPage>("Initialize.");
        }

        protected override void OnShow()
        {
            if (AppRoot.Instance.Room != null)
            {
                AppRoot.Instance.Room.RoomSnapshotChanged += OnRoomSnapshotChanged;
                AppRoot.Instance.Room.MessageChanged += SetMessage;
            }

            Refresh(AppRoot.Instance.Room == null ? null : AppRoot.Instance.Room.CurrentRoom);
            SetMessage("Create a room or join by room ID.");

            AppRoot.Instance.Log.Info<RoomPage>("Show.");
        }

        protected override void OnHide()
        {
            if (AppRoot.Instance != null && AppRoot.Instance.Room != null)
            {
                AppRoot.Instance.Room.RoomSnapshotChanged -= OnRoomSnapshotChanged;
                AppRoot.Instance.Room.MessageChanged -= SetMessage;
            }

            AppRoot.Instance.Log.Info<RoomPage>("Hide.");
        }

        private void OnDestroy()
        {
            RemoveListeners();
        }

        private async void OnClickCreateRoom()
        {
            if (!TryGetRoomSystem(out RoomSystem roomSystem))
            {
                return;
            }

            uint capacity = ParseCapacity();
            string roomName = roomNameInput == null ? string.Empty : roomNameInput.text;

            await RunPageOperationAsync(() => roomSystem.CreateRoomAsync(roomName, capacity));
        }

        private async void OnClickJoinRoom()
        {
            if (!TryGetRoomSystem(out RoomSystem roomSystem))
            {
                return;
            }

            string roomID = joinRoomIDInput == null ? string.Empty : joinRoomIDInput.text;

            await RunPageOperationAsync(() => roomSystem.JoinRoomAsync(roomID));
        }

        private async void OnClickReconnectRoom()
        {
            if (!TryGetRoomSystem(out RoomSystem roomSystem))
            {
                return;
            }

            await RunPageOperationAsync(roomSystem.ReconnectRoomAsync);
        }

        private async void OnClickReady()
        {
            if (!TryGetRoomSystem(out RoomSystem roomSystem))
            {
                return;
            }

            bool nextReady = !roomSystem.IsCurrentPlayerReady();

            await RunPageOperationAsync(() => roomSystem.SetReadyAsync(nextReady));
        }

        private async void OnClickLeaveRoom()
        {
            if (!TryGetRoomSystem(out RoomSystem roomSystem))
            {
                return;
            }

            await RunPageOperationAsync(roomSystem.LeaveRoomAsync);
        }

        private async void OnClickTransferHost()
        {
            if (!TryGetRoomSystem(out RoomSystem roomSystem))
            {
                return;
            }

            string targetPlayerID = transferHostPlayerIDInput == null ? string.Empty : transferHostPlayerIDInput.text;

            await RunPageOperationAsync(() => roomSystem.TransferHostAsync(targetPlayerID));
        }

        private void OnClickBack()
        {
            AppRoot.Instance.UI.OpenPage(AppPages.HomePage);
            Close();
        }

        private async System.Threading.Tasks.Task RunPageOperationAsync(System.Func<System.Threading.Tasks.Task<RoomOperationResult>> operation)
        {
            if (_operationRunning || operation == null)
            {
                return;
            }

            _operationRunning = true;
            SetButtonsInteractable(false);

            try
            {
                RoomOperationResult result = await operation();
                SetMessage(result.Message);
                Refresh(AppRoot.Instance.Room == null ? null : AppRoot.Instance.Room.CurrentRoom);
            }
            catch (System.Exception exception)
            {
                SetMessage("Room operation failed.");
                AppRoot.Instance.Log.Error<RoomPage>(exception.ToString());
            }
            finally
            {
                _operationRunning = false;
                SetButtonsInteractable(true);
            }
        }

        private void OnRoomSnapshotChanged(RoomSnapshot snapshot)
        {
            Refresh(snapshot);
        }

        private void Refresh(RoomSnapshot snapshot)
        {
            RefreshRoomInfo(snapshot);
            RefreshMemberList(snapshot);
            RefreshButtonState(snapshot);
        }

        private void RefreshRoomInfo(RoomSnapshot snapshot)
        {
            if (roomInfoText == null)
            {
                return;
            }

            if (snapshot == null)
            {
                roomInfoText.text = "No current room.";
                return;
            }

            roomInfoText.text =
                $"Room: {snapshot.Name}\n" +
                $"ID: {snapshot.RoomId}\n" +
                $"State: {snapshot.State}\n" +
                $"Host: {snapshot.HostPlayerId}\n" +
                $"Capacity: {snapshot.Members.Count}/{snapshot.Capacity}";
        }

        private void RefreshMemberList(RoomSnapshot snapshot)
        {
            if (memberListText == null)
            {
                return;
            }

            if (snapshot == null || snapshot.Members.Count == 0)
            {
                memberListText.text = "Members: none";
                return;
            }

            StringBuilder builder = new StringBuilder();
            builder.AppendLine("Members");

            foreach (RoomMemberSnapshot member in snapshot.Members)
            {
                if (member == null)
                {
                    continue;
                }

                builder.Append(member.Host ? "* " : "- ");
                builder.Append(member.PlayerId);
                builder.Append(" | seat ");
                builder.Append(member.Seat);
                builder.Append(" | ");
                builder.Append(member.Team);
                builder.Append(" | ");
                builder.Append(member.Ready ? "ready" : "not ready");
                builder.Append(" | ");
                builder.Append(member.ConnectionState);
                builder.AppendLine();
            }

            memberListText.text = builder.ToString();
        }

        private void RefreshButtonState(RoomSnapshot snapshot)
        {
            bool hasRoom = snapshot != null;
            bool isHost = AppRoot.Instance.Room != null && AppRoot.Instance.Room.IsCurrentPlayerHost();
            bool isReady = AppRoot.Instance.Room != null && AppRoot.Instance.Room.IsCurrentPlayerReady();

            SetButtonText(readyButton, isReady ? "Cancel Ready" : "Ready");

            if (readyButton != null)
            {
                readyButton.interactable = hasRoom && !_operationRunning;
            }

            if (leaveRoomButton != null)
            {
                leaveRoomButton.interactable = hasRoom && !_operationRunning;
            }

            if (transferHostButton != null)
            {
                transferHostButton.interactable = hasRoom && isHost && !_operationRunning;
            }

            if (reconnectRoomButton != null)
            {
                reconnectRoomButton.interactable = !_operationRunning && AppRoot.Instance.Room != null && !string.IsNullOrWhiteSpace(AppRoot.Instance.Room.LastRoomID);
            }
        }

        private void SetButtonsInteractable(bool interactable)
        {
            SetButtonInteractable(createRoomButton, interactable);
            SetButtonInteractable(joinRoomButton, interactable);
            SetButtonInteractable(reconnectRoomButton, interactable);
            SetButtonInteractable(readyButton, interactable);
            SetButtonInteractable(leaveRoomButton, interactable);
            SetButtonInteractable(transferHostButton, interactable);
            SetButtonInteractable(backButton, interactable);
        }

        private bool TryGetRoomSystem(out RoomSystem roomSystem)
        {
            roomSystem = AppRoot.Instance == null ? null : AppRoot.Instance.Room;
            if (roomSystem != null)
            {
                return true;
            }

            SetMessage("Room system is not ready.");
            AppRoot.Instance.Log.Error<RoomPage>("RoomSystem is null.");
            return false;
        }

        private uint ParseCapacity()
        {
            if (capacityInput == null || string.IsNullOrWhiteSpace(capacityInput.text))
            {
                return 4;
            }

            return uint.TryParse(capacityInput.text, out uint capacity) ? capacity : 4;
        }

        private void AddListeners()
        {
            if (createRoomButton != null)
            {
                createRoomButton.onClick.AddListener(OnClickCreateRoom);
            }

            if (joinRoomButton != null)
            {
                joinRoomButton.onClick.AddListener(OnClickJoinRoom);
            }

            if (reconnectRoomButton != null)
            {
                reconnectRoomButton.onClick.AddListener(OnClickReconnectRoom);
            }

            if (readyButton != null)
            {
                readyButton.onClick.AddListener(OnClickReady);
            }

            if (leaveRoomButton != null)
            {
                leaveRoomButton.onClick.AddListener(OnClickLeaveRoom);
            }

            if (transferHostButton != null)
            {
                transferHostButton.onClick.AddListener(OnClickTransferHost);
            }

            if (backButton != null)
            {
                backButton.onClick.AddListener(OnClickBack);
            }
        }

        private void RemoveListeners()
        {
            if (createRoomButton != null)
            {
                createRoomButton.onClick.RemoveListener(OnClickCreateRoom);
            }

            if (joinRoomButton != null)
            {
                joinRoomButton.onClick.RemoveListener(OnClickJoinRoom);
            }

            if (reconnectRoomButton != null)
            {
                reconnectRoomButton.onClick.RemoveListener(OnClickReconnectRoom);
            }

            if (readyButton != null)
            {
                readyButton.onClick.RemoveListener(OnClickReady);
            }

            if (leaveRoomButton != null)
            {
                leaveRoomButton.onClick.RemoveListener(OnClickLeaveRoom);
            }

            if (transferHostButton != null)
            {
                transferHostButton.onClick.RemoveListener(OnClickTransferHost);
            }

            if (backButton != null)
            {
                backButton.onClick.RemoveListener(OnClickBack);
            }
        }

        private void SetMessage(string message)
        {
            if (messageText != null)
            {
                messageText.text = message;
            }
        }

        private static void SetButtonInteractable(Button button, bool interactable)
        {
            if (button != null)
            {
                button.interactable = interactable;
            }
        }

        private static void SetButtonText(Button button, string text)
        {
            if (button == null)
            {
                return;
            }

            TMP_Text label = button.GetComponentInChildren<TMP_Text>();
            if (label != null)
            {
                label.text = text;
            }
        }
    }
}
