using System;
using System.IO;
using System.Net.WebSockets;
using System.Threading;
using System.Threading.Tasks;
using App.Core;
using Google.Protobuf;
using Google.Protobuf.WellKnownTypes;
using Ihomeland.Realtime.V1;
using UnityEditor;
using UnityEngine;

namespace App.Editor
{
    internal static class WebSocketSmokeTestMenu
    {
        private const string MenuPath = "iHomeland/Smoke Test/WebSocket Account";
        private const string TestAccount = "unity_smoke_test";
        private const string TestPassword = "UnitySmokeTest@2026";
        private const string TestDisplayName = "Unity Smoke Test";
        private const int TimeoutSeconds = 10;
        private const uint ProtocolVersion = 1;
        private const int ReceiveBufferSize = 64 * 1024;

        private static bool _isRunning;

        [MenuItem(MenuPath)]
        private static async void RunSmokeTest()
        {
            if (_isRunning)
            {
                Debug.LogWarning("[WebSocketSmokeTest] Smoke test is already running.");
                return;
            }

            _isRunning = true;
            try
            {
                await RunSmokeTestAsync();
                Debug.Log("[WebSocketSmokeTest] OK: websocket, heartbeat, account session and logout passed.");
            }
            catch (Exception exception)
            {
                Debug.LogError($"[WebSocketSmokeTest] FAIL: {exception.Message}");
            }
            finally
            {
                _isRunning = false;
            }
        }

        [MenuItem(MenuPath, true)]
        private static bool CanRunSmokeTest()
        {
            return !_isRunning;
        }

        private static async Task RunSmokeTestAsync()
        {
            using (CancellationTokenSource timeout = new CancellationTokenSource(TimeSpan.FromSeconds(TimeoutSeconds)))
            using (ClientWebSocket socket = new ClientWebSocket())
            {
                Uri uri = new Uri(AppConfig.WebSocketURL);
                Debug.Log($"[WebSocketSmokeTest] Connecting {uri} ...");
                await socket.ConnectAsync(uri, timeout.Token);

                await SendRequestAsync<HeartbeatResponse>(
                    socket,
                    MessageID.HeartbeatRequest,
                    MessageID.HeartbeatResponse,
                    new HeartbeatRequest { ClientTimeMs = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds() },
                    timeout.Token
                );

                string sessionToken = await RegisterOrLoginAsync(socket, timeout.Token);
                await SendRequestAsync<LogoutResponse>(
                    socket,
                    MessageID.LogoutRequest,
                    MessageID.LogoutResponse,
                    new LogoutRequest { SessionToken = sessionToken },
                    timeout.Token
                );

                await socket.CloseAsync(WebSocketCloseStatus.NormalClosure, "smoke test complete", timeout.Token);
            }
        }

        private static async Task<string> RegisterOrLoginAsync(ClientWebSocket socket, CancellationToken token)
        {
            try
            {
                RegisterResponse register = await SendRequestAsync<RegisterResponse>(
                    socket,
                    MessageID.RegisterRequest,
                    MessageID.RegisterResponse,
                    new RegisterRequest
                    {
                        Account = TestAccount,
                        Password = TestPassword,
                        DisplayName = TestDisplayName
                    },
                    token
                );

                Debug.Log($"[WebSocketSmokeTest] Register ok. player_id={register.Player?.PlayerId}");
                return register.SessionToken;
            }
            catch (SmokeTestServerException exception) when (exception.Code == ErrorCode.AccountAlreadyExists)
            {
                Debug.Log("[WebSocketSmokeTest] Test account already exists, fallback to login.");
            }

            LoginResponse login = await SendRequestAsync<LoginResponse>(
                socket,
                MessageID.LoginRequest,
                MessageID.LoginResponse,
                new LoginRequest
                {
                    Account = TestAccount,
                    Password = TestPassword
                },
                token
            );

            Debug.Log($"[WebSocketSmokeTest] Login ok. player_id={login.Player?.PlayerId}");
            return login.SessionToken;
        }

        private static async Task<TResponse> SendRequestAsync<TResponse>(
            ClientWebSocket socket,
            MessageID requestMessageID,
            MessageID responseMessageID,
            IMessage request,
            CancellationToken token
        ) where TResponse : class, IMessage<TResponse>, new()
        {
            string requestID = Guid.NewGuid().ToString("N");
            Envelope requestEnvelope = new Envelope
            {
                ProtocolVersion = ProtocolVersion,
                MessageId = requestMessageID,
                RequestId = requestID,
                Sequence = SmokeTestSequence.Next(),
                TimestampMs = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds(),
                Payload = Any.Pack(request)
            };

            byte[] data = requestEnvelope.ToByteArray();
            await socket.SendAsync(new ArraySegment<byte>(data), WebSocketMessageType.Binary, true, token);

            Envelope responseEnvelope = await ReceiveEnvelopeAsync(socket, token);
            if (!string.Equals(responseEnvelope.RequestId, requestID, StringComparison.Ordinal))
            {
                throw new InvalidOperationException($"request_id mismatch. expected={requestID}, actual={responseEnvelope.RequestId}");
            }

            if (responseEnvelope.MessageId == MessageID.ErrorResponse)
            {
                ErrorResponse error = responseEnvelope.Payload.Unpack<ErrorResponse>();
                throw new SmokeTestServerException(error);
            }

            if (responseEnvelope.MessageId != responseMessageID)
            {
                throw new InvalidOperationException($"unexpected response message_id. expected={responseMessageID}, actual={responseEnvelope.MessageId}");
            }

            return responseEnvelope.Payload.Unpack<TResponse>();
        }

        private static async Task<Envelope> ReceiveEnvelopeAsync(ClientWebSocket socket, CancellationToken token)
        {
            byte[] buffer = new byte[ReceiveBufferSize];
            using (MemoryStream stream = new MemoryStream())
            {
                while (true)
                {
                    WebSocketReceiveResult result = await socket.ReceiveAsync(new ArraySegment<byte>(buffer), token);
                    if (result.MessageType == WebSocketMessageType.Close)
                    {
                        throw new InvalidOperationException($"server closed websocket. close_status={result.CloseStatus}, description={result.CloseStatusDescription}");
                    }

                    if (result.MessageType != WebSocketMessageType.Binary)
                    {
                        throw new InvalidOperationException($"server returned non-binary websocket message. message_type={result.MessageType}");
                    }

                    stream.Write(buffer, 0, result.Count);
                    if (result.EndOfMessage)
                    {
                        break;
                    }
                }

                return Envelope.Parser.ParseFrom(stream.ToArray());
            }
        }

        private static class SmokeTestSequence
        {
            private static ulong _next;

            public static ulong Next()
            {
                return ++_next;
            }
        }

        private sealed class SmokeTestServerException : Exception
        {
            public ErrorCode Code { get; }

            public SmokeTestServerException(ErrorResponse error)
                : base(BuildMessage(error))
            {
                Code = error == null ? ErrorCode.Unspecified : error.Code;
            }

            private static string BuildMessage(ErrorResponse error)
            {
                if (error == null)
                {
                    return "server returned empty error response.";
                }

                return $"server error. code={error.Code}, message={error.Message}, detail={error.Detail}";
            }
        }
    }
}
