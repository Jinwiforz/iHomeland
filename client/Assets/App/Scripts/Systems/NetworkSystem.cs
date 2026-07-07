using System;
using System.IO;
using System.Net.WebSockets;
using System.Threading;
using System.Threading.Tasks;
using App.Core;
using Google.Protobuf;
using Google.Protobuf.WellKnownTypes;
using Ihomeland.Realtime.V1;

namespace App.Systems
{
    public sealed class NetworkSystem : AppSystemBase
    {
        private const uint ProtocolVersion = 1;
        private const int ReceiveBufferSize = 64 * 1024;

        private readonly SemaphoreSlim _requestLock = new SemaphoreSlim(1, 1);

        private ClientWebSocket _socket;
        private CancellationTokenSource _shutdown;
        private Task _heartbeatTask;
        private ulong _sequence;
        private long _nextHeartbeatAtMs;
        private bool _isShuttingDown;

        public bool IsConnected => _socket != null && _socket.State == WebSocketState.Open;
        public long LastHeartbeatRTTMs { get; private set; }
        public long LastHeartbeatServerTimeMs { get; private set; }

        protected override void OnInitialize()
        {
            _shutdown = new CancellationTokenSource();
            _isShuttingDown = false;
            ScheduleNextHeartbeat();
            Root.Log.Info<NetworkSystem>("Network system initialized.");
        }

        protected override void OnTick(float deltaTime)
        {
            TickHeartbeat();
        }

        protected override void OnShutdown()
        {
            AbortForShutdown();
            Root.Log.Info<NetworkSystem>("Network system shutdown.");
        }

        public async Task<RegisterResponse> RegisterAsync(string account, string password, string displayName)
        {
            return await SendRequestAsync<RegisterResponse>(
                MessageID.RegisterRequest,
                MessageID.RegisterResponse,
                new RegisterRequest
                {
                    Account = account ?? string.Empty,
                    Password = password ?? string.Empty,
                    DisplayName = displayName ?? string.Empty
                }
            );
        }

        public async Task<LoginResponse> LoginAsync(string account, string password)
        {
            return await SendRequestAsync<LoginResponse>(
                MessageID.LoginRequest,
                MessageID.LoginResponse,
                new LoginRequest
                {
                    Account = account ?? string.Empty,
                    Password = password ?? string.Empty
                }
            );
        }

        public async Task<LogoutResponse> LogoutAsync(string sessionToken)
        {
            return await SendRequestAsync<LogoutResponse>(
                MessageID.LogoutRequest,
                MessageID.LogoutResponse,
                new LogoutRequest
                {
                    SessionToken = sessionToken ?? string.Empty
                }
            );
        }

        public async Task<ResumeSessionResponse> ResumeSessionAsync(string sessionToken)
        {
            return await SendRequestAsync<ResumeSessionResponse>(
                MessageID.ResumeSessionRequest,
                MessageID.ResumeSessionResponse,
                new ResumeSessionRequest
                {
                    SessionToken = sessionToken ?? string.Empty
                }
            );
        }

        public async Task<HeartbeatResponse> HeartbeatAsync()
        {
            long clientTimeMs = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();
            return await SendRequestAsync<HeartbeatResponse>(
                MessageID.HeartbeatRequest,
                MessageID.HeartbeatResponse,
                new HeartbeatRequest
                {
                    ClientTimeMs = clientTimeMs
                }
            );
        }

        /// <summary>
        /// 业务主动断开连接时使用优雅关闭，给服务端一个正常 WebSocket close frame，但必须受关闭超时约束。
        /// Unity 生命周期退出不会调用该方法，避免在停止播放或关闭游戏时等待网络握手。
        /// </summary>
        public async Task DisconnectAsync()
        {
            await DisconnectInternalAsync("client disconnect", true);
        }

        public async Task<TResponse> SendRequestAsync<TResponse>(
            MessageID requestMessageID,
            MessageID responseMessageID,
            IMessage request
        ) where TResponse : class, IMessage<TResponse>, new()
        {
            if (request == null)
            {
                throw new NetworkRequestException(NetworkErrorKind.InvalidRequest, "Request payload is empty.");
            }

            ThrowIfShutdown();

            using (CancellationTokenSource timeout = CreateTimeout(AppConfig.NetworkRequestTimeoutSeconds))
            using (CancellationTokenSource requestScope = CancellationTokenSource.CreateLinkedTokenSource(_shutdown.Token, timeout.Token))
            {
                CancellationToken shutdownToken = _shutdown.Token;
                CancellationToken token = requestScope.Token;
                bool lockTaken = false;
                string requestID = Guid.NewGuid().ToString("N");

                try
                {
                    await _requestLock.WaitAsync(token);
                    lockTaken = true;

                    await EnsureConnectedAsync(token);

                    Envelope envelope = CreateRequestEnvelope(requestMessageID, requestID, request);
                    byte[] data = envelope.ToByteArray();

                    Root.Log.Debug<NetworkSystem>($"Send request. request_id={requestID}, message_id={requestMessageID}, sequence={envelope.Sequence}");
                    await _socket.SendAsync(new ArraySegment<byte>(data), WebSocketMessageType.Binary, true, token);

                    while (true)
                    {
                        Envelope response = await ReceiveEnvelopeAsync(token);

                        if (!string.Equals(response.RequestId, requestID, StringComparison.Ordinal))
                        {
                            if (string.IsNullOrEmpty(response.RequestId))
                            {
                                Root.Log.Debug<NetworkSystem>($"Receive server push while waiting response. push_message_id={response.MessageId}, sequence={response.Sequence}");
                                continue;
                            }

                            string message = $"Response request_id mismatch. expected={requestID}, actual={response.RequestId}, message_id={response.MessageId}";
                            throw new NetworkRequestException(NetworkErrorKind.RequestMismatch, message);
                        }

                        if (response.MessageId == MessageID.ErrorResponse)
                        {
                            ErrorResponse error = UnpackPayload<ErrorResponse>(response, "ErrorResponse");
                            throw NetworkRequestException.FromServerError(error);
                        }

                        if (response.MessageId != responseMessageID)
                        {
                            string message = $"Unexpected response message id. expected={responseMessageID}, actual={response.MessageId}, request_id={requestID}";
                            throw new NetworkRequestException(NetworkErrorKind.ProtocolError, message);
                        }

                        TResponse typedResponse = UnpackPayload<TResponse>(response, typeof(TResponse).Name);
                        Root.Log.Debug<NetworkSystem>($"Receive response. request_id={requestID}, message_id={response.MessageId}, sequence={response.Sequence}");
                        return typedResponse;
                    }
                }
                catch (OperationCanceledException ex) when (shutdownToken.IsCancellationRequested)
                {
                    throw new NetworkRequestException(NetworkErrorKind.Shutdown, "Network is shutting down. Please try again later.", ex);
                }
                catch (OperationCanceledException ex) when (timeout.IsCancellationRequested)
                {
                    CleanupSocket("request timeout", true, true);
                    throw new NetworkRequestException(NetworkErrorKind.RequestTimeout, "Request timed out. Please check the network and try again.", ex);
                }
                catch (WebSocketException ex)
                {
                    CleanupSocket("websocket exception", true, true);
                    throw new NetworkRequestException(NetworkErrorKind.WebSocketError, $"WebSocket error: {ex.Message}", ex);
                }
                catch (ObjectDisposedException ex)
                {
                    CleanupSocket("socket disposed", true, true);
                    throw new NetworkRequestException(NetworkErrorKind.WebSocketError, "Network connection has been closed. Please reconnect.", ex);
                }
                catch (NetworkRequestException ex)
                {
                    if (ex.ShouldDropConnection)
                    {
                        CleanupSocket(ex.Kind.ToString(), true, true);
                    }

                    throw;
                }
                finally
                {
                    if (lockTaken)
                    {
                        _requestLock.Release();
                    }
                }
            }
        }

        private async Task DisconnectInternalAsync(string reason, bool recreateShutdownToken)
        {
            CancellationTokenSource currentShutdown = _shutdown;
            if (currentShutdown == null)
            {
                CleanupSocket(reason, true, false);
                return;
            }

            currentShutdown.Cancel();

            bool lockTaken = false;
            try
            {
                lockTaken = await _requestLock.WaitAsync(TimeSpan.FromSeconds(AppConfig.NetworkCloseTimeoutSeconds));
                if (!lockTaken)
                {
                    Root.Log.Warning<NetworkSystem>($"Disconnect timed out waiting for active request. reason={reason}");
                    CleanupSocket(reason, true, true);
                    return;
                }

                using (CancellationTokenSource closeTimeout = CreateTimeout(AppConfig.NetworkCloseTimeoutSeconds))
                {
                    await CloseSocketGracefullyAsync(reason, closeTimeout.Token);
                }
            }
            finally
            {
                if (lockTaken)
                {
                    _requestLock.Release();
                }

                if (recreateShutdownToken && !_isShuttingDown)
                {
                    _shutdown = new CancellationTokenSource();
                    if (lockTaken)
                    {
                        currentShutdown.Dispose();
                    }
                }
            }
        }

        private async Task EnsureConnectedAsync(CancellationToken requestToken)
        {
            if (IsConnected)
            {
                return;
            }

            CleanupSocket("new connection", true, false);

            ClientWebSocket socket = new ClientWebSocket();
            _socket = socket;
            Uri uri = new Uri(AppConfig.WebSocketURL);

            using (CancellationTokenSource connectTimeout = CreateTimeout(AppConfig.NetworkConnectTimeoutSeconds))
            using (CancellationTokenSource connectScope = CancellationTokenSource.CreateLinkedTokenSource(requestToken, connectTimeout.Token))
            {
                try
                {
                    await socket.ConnectAsync(uri, connectScope.Token);
                    ScheduleNextHeartbeat();
                    Root.Log.Info<NetworkSystem>($"Connected websocket. url={uri}");
                }
                catch (OperationCanceledException ex) when (connectTimeout.IsCancellationRequested)
                {
                    CleanupSocket("connect timeout", true, true);
                    throw new NetworkRequestException(NetworkErrorKind.ConnectionTimeout, "Connection timed out. Please make sure the server is running.", ex);
                }
                catch (WebSocketException ex)
                {
                    CleanupSocket("connect websocket exception", true, true);
                    throw new NetworkRequestException(NetworkErrorKind.WebSocketError, $"Failed to connect server: {ex.Message}", ex);
                }
            }
        }

        private async Task<Envelope> ReceiveEnvelopeAsync(CancellationToken token)
        {
            if (_socket == null || _socket.State != WebSocketState.Open)
            {
                throw new NetworkRequestException(NetworkErrorKind.WebSocketClosed, "Network connection is not open.");
            }

            byte[] buffer = new byte[ReceiveBufferSize];
            using (MemoryStream stream = new MemoryStream())
            {
                while (true)
                {
                    WebSocketReceiveResult result = await _socket.ReceiveAsync(new ArraySegment<byte>(buffer), token);
                    if (result.MessageType == WebSocketMessageType.Close)
                    {
                        throw new NetworkRequestException(NetworkErrorKind.WebSocketClosed, $"Server closed websocket. close_status={result.CloseStatus}, description={result.CloseStatusDescription}");
                    }

                    if (result.MessageType != WebSocketMessageType.Binary)
                    {
                        throw new NetworkRequestException(NetworkErrorKind.ProtocolError, $"Server returned non-binary websocket message. message_type={result.MessageType}");
                    }

                    stream.Write(buffer, 0, result.Count);

                    if (result.EndOfMessage)
                    {
                        break;
                    }
                }

                byte[] data = stream.ToArray();
                try
                {
                    Envelope envelope = Envelope.Parser.ParseFrom(data);
                    if (envelope.Payload == null)
                    {
                        throw new NetworkRequestException(NetworkErrorKind.ProtocolError, $"Response payload is missing. message_id={envelope.MessageId}, request_id={envelope.RequestId}");
                    }

                    return envelope;
                }
                catch (InvalidProtocolBufferException ex)
                {
                    throw new NetworkRequestException(NetworkErrorKind.PayloadInvalid, $"Response envelope cannot be parsed. bytes={data.Length}", ex);
                }
            }
        }

        private async Task CloseSocketGracefullyAsync(string reason, CancellationToken token)
        {
            ClientWebSocket socket = _socket;
            if (socket == null)
            {
                return;
            }

            try
            {
                if (socket.State == WebSocketState.Open || socket.State == WebSocketState.CloseReceived)
                {
                    await socket.CloseAsync(WebSocketCloseStatus.NormalClosure, reason, token);
                    Root.Log.Info<NetworkSystem>($"WebSocket closed. reason={reason}");
                }
            }
            catch (Exception ex) when (ex is WebSocketException || ex is OperationCanceledException || ex is ObjectDisposedException)
            {
                Root.Log.Warning<NetworkSystem>($"Close websocket failed. reason={reason}, error={ex.Message}");
                socket.Abort();
            }
            finally
            {
                CleanupSocket(reason, false, false);
            }
        }

        private Envelope CreateRequestEnvelope(MessageID messageID, string requestID, IMessage request)
        {
            return new Envelope
            {
                ProtocolVersion = ProtocolVersion,
                MessageId = messageID,
                RequestId = requestID,
                Sequence = ++_sequence,
                TimestampMs = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds(),
                Payload = Any.Pack(request)
            };
        }

        private void TickHeartbeat()
        {
            if (_isShuttingDown || !IsConnected)
            {
                return;
            }

            if (_heartbeatTask != null && !_heartbeatTask.IsCompleted)
            {
                return;
            }

            long nowMs = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();
            if (nowMs < _nextHeartbeatAtMs)
            {
                return;
            }

            _heartbeatTask = SendHeartbeatTickAsync();
        }

        private async Task SendHeartbeatTickAsync()
        {
            long sentAtMs = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();
            try
            {
                HeartbeatResponse response = await HeartbeatAsync();
                long receivedAtMs = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();

                LastHeartbeatRTTMs = Math.Max(0, receivedAtMs - response.ClientTimeMs);
                LastHeartbeatServerTimeMs = response.ServerTimeMs;
                ScheduleNextHeartbeat(receivedAtMs);

                Root.Log.Debug<NetworkSystem>($"Heartbeat ok. rtt_ms={LastHeartbeatRTTMs}, server_time_ms={LastHeartbeatServerTimeMs}");
            }
            catch (NetworkRequestException ex) when (ex.Kind == NetworkErrorKind.Shutdown)
            {
            }
            catch (Exception ex)
            {
                ScheduleNextHeartbeat(sentAtMs);
                Root.Log.Warning<NetworkSystem>($"Heartbeat failed. reason={ex.Message}");
            }
        }

        private void ScheduleNextHeartbeat()
        {
            ScheduleNextHeartbeat(DateTimeOffset.UtcNow.ToUnixTimeMilliseconds());
        }

        private void ScheduleNextHeartbeat(long baseTimeMs)
        {
            _nextHeartbeatAtMs = baseTimeMs + AppConfig.NetworkHeartbeatIntervalSeconds * 1000L;
        }

        private TPayload UnpackPayload<TPayload>(Envelope envelope, string payloadName)
            where TPayload : class, IMessage<TPayload>, new()
        {
            try
            {
                return envelope.Payload.Unpack<TPayload>();
            }
            catch (Exception ex) when (ex is InvalidProtocolBufferException || ex is InvalidOperationException || ex is ArgumentException)
            {
                string typeURL = envelope.Payload == null ? string.Empty : envelope.Payload.TypeUrl;
                string message = $"Response {payloadName} payload cannot be parsed. message_id={envelope.MessageId}, request_id={envelope.RequestId}, type_url={typeURL}";
                throw new NetworkRequestException(NetworkErrorKind.PayloadInvalid, message, ex);
            }
        }

        private void AbortForShutdown()
        {
            _isShuttingDown = true;

            // Unity 停止播放或应用退出时，主线程不能等待 WebSocket close 握手；强制中止比卡死 Editor 更可控。
            _shutdown?.Cancel();
            CleanupSocket("client shutdown", true, false);
        }

        /// <summary>
        /// 清理当前 socket 引用。业务优雅关闭已完成 close frame 时不再 Abort；异常、超时和生命周期退出必须 Abort。
        /// </summary>
        private void CleanupSocket(string reason, bool abort, bool log)
        {
            ClientWebSocket socket = _socket;
            _socket = null;

            if (socket == null)
            {
                return;
            }

            try
            {
                if (abort && socket.State != WebSocketState.Closed)
                {
                    socket.Abort();
                }
            }
            catch (ObjectDisposedException)
            {
            }
            finally
            {
                socket.Dispose();
            }

            if (log)
            {
                Root.Log.Debug<NetworkSystem>($"Socket cleaned. reason={reason}");
            }

            ScheduleNextHeartbeat();
        }

        private void ThrowIfShutdown()
        {
            if (_shutdown == null || _shutdown.IsCancellationRequested)
            {
                throw new NetworkRequestException(NetworkErrorKind.Shutdown, "Network system is shutting down.");
            }
        }

        private static CancellationTokenSource CreateTimeout(int seconds)
        {
            return new CancellationTokenSource(TimeSpan.FromSeconds(Math.Max(1, seconds)));
        }
    }

    public enum NetworkErrorKind
    {
        Unknown = 0,
        InvalidRequest = 1,
        ConnectionTimeout = 2,
        RequestTimeout = 3,
        WebSocketClosed = 4,
        WebSocketError = 5,
        ProtocolError = 6,
        PayloadInvalid = 7,
        RequestMismatch = 8,
        ServerError = 9,
        Shutdown = 10
    }

    /// <summary>
    /// NetworkRequestException 保留网络层可判断的错误分类，同时透传服务端 ErrorResponse 的 code/message/detail。
    /// </summary>
    public sealed class NetworkRequestException : Exception
    {
        public NetworkErrorKind Kind { get; }
        public ErrorCode Code { get; }
        public string Detail { get; }

        public bool ShouldDropConnection =>
            Kind == NetworkErrorKind.RequestTimeout ||
            Kind == NetworkErrorKind.WebSocketClosed ||
            Kind == NetworkErrorKind.WebSocketError ||
            Kind == NetworkErrorKind.ProtocolError ||
            Kind == NetworkErrorKind.PayloadInvalid ||
            Kind == NetworkErrorKind.RequestMismatch;

        public NetworkRequestException(NetworkErrorKind kind, string message)
            : this(kind, message, ErrorCode.Unspecified, string.Empty, null)
        {
        }

        public NetworkRequestException(NetworkErrorKind kind, string message, Exception innerException)
            : this(kind, message, ErrorCode.Unspecified, string.Empty, innerException)
        {
        }

        private NetworkRequestException(NetworkErrorKind kind, string message, ErrorCode code, string detail, Exception innerException)
            : base(string.IsNullOrWhiteSpace(message) ? "Network request failed." : message, innerException)
        {
            Kind = kind;
            Code = code;
            Detail = detail ?? string.Empty;
        }

        public static NetworkRequestException FromServerError(ErrorResponse error)
        {
            if (error == null)
            {
                return new NetworkRequestException(NetworkErrorKind.PayloadInvalid, "Server returned empty error response.");
            }

            string message = string.IsNullOrWhiteSpace(error.Message)
                ? $"Server rejected request. code={error.Code}"
                : error.Message;

            return new NetworkRequestException(NetworkErrorKind.ServerError, message, error.Code, error.Detail, null);
        }
    }
}
