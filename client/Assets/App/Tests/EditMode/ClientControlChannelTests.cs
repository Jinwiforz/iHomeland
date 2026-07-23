using System;
using System.Collections.Generic;
using System.Net.WebSockets;
using System.Threading;
using System.Threading.Tasks;
using Google.Protobuf;
using IHomeland.Client.Application.Control;
using IHomeland.Client.Application.Session;
using IHomeland.Client.Application.Configuration;
using IHomeland.Client.Foundation.Lifetime;
using IHomeland.Client.Foundation.Time;
using IHomeland.Client.Application.Contracts;
using IHomeland.Client.Application.Ports;
using IHomeland.Client.Infrastructure.Http;
using IHomeland.Client.Infrastructure.WebSocket;
using IHomeland.Protocol.Common.V1;
using IHomeland.Protocol.Control.V1;
using NUnit.Framework;

namespace IHomeland.Client.Tests.EditMode
{
    /// <summary>
    /// 验证 control owner 的 ticket、receive、主线程、失效、恢复与停止边界。
    /// </summary>
    [TestFixture]
    internal sealed class ClientControlChannelTests
    {
        /// <summary>
        /// 确认 fragmented binary envelope 只在完整重组后投递一次主线程 callback。
        /// </summary>
        [Test]
        public async Task Run_FragmentedPush_DispatchesOnceOnMainThread()
        {
            var frame = EnvelopeBytes(500, 1, Maintenance());
            var first = new byte[frame.Length / 2];
            var second = new byte[frame.Length - first.Length];
            Array.Copy(frame, 0, first, 0, first.Length);
            Array.Copy(frame, first.Length, second, 0, second.Length);
            var socket = new FakeSocket(
                false,
                ClientWebSocketConnectRequest.ControlSubprotocol,
                ReceiveStep.Binary(first, false),
                ReceiveStep.Binary(second, true),
                ReceiveStep.Close(WebSocketCloseStatus.PolicyViolation));
            var factory = new FakeSocketFactory(socket);
            var harness = await CreateHarnessAsync(factory, Array.Empty<TimeSpan>());
            var received = new List<ClientControlNotification>();
            harness.Channel.PushReceived += received.Add;

            await harness.Channel.RunAsync(CancellationToken.None);

            Assert.That(received, Is.Empty);
            var drain = harness.Dispatcher.Drain(8);
            Assert.That(drain.Errors, Is.Empty);
            Assert.That(received.Count, Is.EqualTo(1));
            Assert.That(received[0].Kind, Is.EqualTo(ClientControlPushKind.Maintenance));
            Assert.That(harness.Channel.Snapshot.CloseReason, Is.EqualTo(ClientControlCloseReason.ProtocolFailure));
            Assert.That(socket.ConnectRequest.Endpoint.AbsoluteUri, Is.EqualTo("ws://127.0.0.1:8443/v1/control"));
            Assert.That(socket.ConnectRequest.ToString(), Does.Not.Contain(socket.ConnectRequest.TicketCredential));
            await harness.Channel.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 确认主线程队列满时关闭连接，而不是丢弃 sequence 后继续读取。
        /// </summary>
        [Test]
        public async Task Run_DispatcherFull_StopsWithBackpressure()
        {
            var socket = new FakeSocket(
                false,
                ClientWebSocketConnectRequest.ControlSubprotocol,
                ReceiveStep.Binary(EnvelopeBytes(500, 1, Maintenance()), true));
            var harness = await CreateHarnessAsync(
                new FakeSocketFactory(socket),
                Array.Empty<TimeSpan>(),
                dispatcherCapacity: 1);
            Assert.That(harness.Dispatcher.TryPost(() => { }), Is.EqualTo(DispatchPostResult.Accepted));

            await harness.Channel.RunAsync(CancellationToken.None);

            Assert.That(harness.Channel.Snapshot.CloseReason, Is.EqualTo(ClientControlCloseReason.MainThreadBackpressure));
            Assert.That(socket.ReceiveCount, Is.EqualTo(1));
            await harness.Channel.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 确认 forced logout 先清除 current session，再终止重连并投递通知。
        /// </summary>
        [Test]
        public async Task Run_ForcedLogout_InvalidatesSessionAndStopsRecovery()
        {
            var socket = new FakeSocket(
                false,
                ClientWebSocketConnectRequest.ControlSubprotocol,
                ReceiveStep.Binary(
                    EnvelopeBytes(
                        501,
                        1,
                        new ForcedLogoutPush { SessionEpoch = 2, ReasonKey = "fixture" }),
                    true));
            var harness = await CreateHarnessAsync(
                new FakeSocketFactory(socket),
                new[] { TimeSpan.FromMilliseconds(1) });

            await harness.Channel.RunAsync(CancellationToken.None);

            Assert.That(harness.Session.State, Is.EqualTo(ClientSessionOwnerState.Unauthenticated));
            Assert.That(harness.Channel.Snapshot.State, Is.EqualTo(ClientControlChannelState.SessionInvalidated));
            Assert.That(harness.Channel.Snapshot.CloseReason, Is.EqualTo(ClientControlCloseReason.SessionInvalidated));
            Assert.That(harness.HttpApi.TicketCount, Is.EqualTo(1));
            await harness.Channel.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 确认瞬时连接失败消耗有限 backoff，且每次 attempt 都签发不同 ticket。
        /// </summary>
        [Test]
        public async Task Run_TransientConnectFailure_UsesFreshTicketOnRetry()
        {
            var first = new FakeSocket(true, ClientWebSocketConnectRequest.ControlSubprotocol);
            var second = new FakeSocket(
                false,
                ClientWebSocketConnectRequest.ControlSubprotocol,
                ReceiveStep.Close(WebSocketCloseStatus.PolicyViolation));
            var factory = new FakeSocketFactory(first, second);
            var delay = new FakeDelay();
            var harness = await CreateHarnessAsync(
                factory,
                new[] { TimeSpan.FromMilliseconds(5) },
                delay: delay);

            await harness.Channel.RunAsync(CancellationToken.None);

            Assert.That(harness.HttpApi.TicketCount, Is.EqualTo(2));
            Assert.That(delay.Delays, Is.EqualTo(new[] { TimeSpan.FromMilliseconds(5) }));
            Assert.That(first.ConnectRequest.TicketCredential, Is.Not.EqualTo(second.ConnectRequest.TicketCredential));
            Assert.That(harness.Channel.Snapshot.Attempt, Is.EqualTo(2));
            Assert.That(harness.Channel.Snapshot.CloseReason, Is.EqualTo(ClientControlCloseReason.ProtocolFailure));
            await harness.Channel.StopAsync(CancellationToken.None);
        }

        /// <summary>确认初始连接屏障只在 WebSocket 已协商 control subprotocol 后报告就绪。</summary>
        [Test]
        public async Task WaitUntilConnected_HandshakeAccepted_ReturnsTrue()
        {
            var socket = new FakeSocket(
                false,
                ClientWebSocketConnectRequest.ControlSubprotocol,
                ReceiveStep.Close(WebSocketCloseStatus.PolicyViolation));
            var harness = await CreateHarnessAsync(
                new FakeSocketFactory(socket),
                Array.Empty<TimeSpan>());

            var run = harness.Channel.RunAsync(CancellationToken.None);
            var connected = await harness.Channel.WaitUntilConnectedAsync(CancellationToken.None);
            await run;

            Assert.That(connected, Is.True);
            await harness.Channel.StopAsync(CancellationToken.None);
        }

        /// <summary>确认首次连接在 Connected 前耗尽预算时解除等待并报告不可接收 push。</summary>
        [Test]
        public async Task WaitUntilConnected_InitialConnectFails_ReturnsFalse()
        {
            var socket = new FakeSocket(
                true,
                ClientWebSocketConnectRequest.ControlSubprotocol);
            var harness = await CreateHarnessAsync(
                new FakeSocketFactory(socket),
                Array.Empty<TimeSpan>());

            var run = harness.Channel.RunAsync(CancellationToken.None);
            var connected = await harness.Channel.WaitUntilConnectedAsync(CancellationToken.None);
            await run;

            Assert.That(connected, Is.False);
            await harness.Channel.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 确认 upgrade 未协商冻结 subprotocol 时立即终止且不消耗重试预算。
        /// </summary>
        [Test]
        public async Task Run_WrongSubprotocol_StopsWithoutRetry()
        {
            var socket = new FakeSocket(false, "unexpected.control.v1");
            var harness = await CreateHarnessAsync(
                new FakeSocketFactory(socket),
                new[] { TimeSpan.FromMilliseconds(1) },
                delay: new FakeDelay());

            await harness.Channel.RunAsync(CancellationToken.None);

            Assert.That(harness.HttpApi.TicketCount, Is.EqualTo(1));
            Assert.That(harness.Channel.Snapshot.CloseReason, Is.EqualTo(ClientControlCloseReason.ProtocolFailure));
            Assert.That(socket.ReceiveCount, Is.EqualTo(0));
            await harness.Channel.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 确认 text application message 不能降级为 JSON 或未知 control event。
        /// </summary>
        [Test]
        public async Task Run_TextMessage_StopsAsProtocolFailure()
        {
            var socket = new FakeSocket(
                false,
                ClientWebSocketConnectRequest.ControlSubprotocol,
                ReceiveStep.Text(new byte[] { 0x7b, 0x7d }));
            var harness = await CreateHarnessAsync(
                new FakeSocketFactory(socket),
                Array.Empty<TimeSpan>());

            await harness.Channel.RunAsync(CancellationToken.None);

            Assert.That(harness.Channel.Snapshot.CloseReason, Is.EqualTo(ClientControlCloseReason.ProtocolFailure));
            Assert.That(socket.ReceiveCount, Is.EqualTo(1));
            await harness.Channel.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 确认有限恢复耗尽后停止后台循环但不擅自清除 HTTP session。
        /// </summary>
        [Test]
        public async Task Run_RetryBudgetExhausted_KeepsCurrentSession()
        {
            var factory = new FakeSocketFactory(
                new FakeSocket(true, ClientWebSocketConnectRequest.ControlSubprotocol),
                new FakeSocket(true, ClientWebSocketConnectRequest.ControlSubprotocol));
            var harness = await CreateHarnessAsync(
                factory,
                new[] { TimeSpan.FromMilliseconds(1) },
                delay: new FakeDelay());

            await harness.Channel.RunAsync(CancellationToken.None);

            Assert.That(harness.HttpApi.TicketCount, Is.EqualTo(2));
            Assert.That(harness.Session.State, Is.EqualTo(ClientSessionOwnerState.Authenticated));
            Assert.That(harness.Channel.Snapshot.State, Is.EqualTo(ClientControlChannelState.Disconnected));
            Assert.That(harness.Channel.Snapshot.CloseReason, Is.EqualTo(ClientControlCloseReason.RetryExhausted));
            await harness.Channel.StopAsync(CancellationToken.None);
        }

        /// <summary>确认终态通知内立即发起下一代run不会被旧run的finally清理窗口拒绝。</summary>
        [Test]
        public async Task Run_TerminalNotification_AllowsImmediateNextGeneration()
        {
            var factory = new FakeSocketFactory(
                new FakeSocket(true, ClientWebSocketConnectRequest.ControlSubprotocol),
                new FakeSocket(false, "unexpected.control.v1"));
            var harness = await CreateHarnessAsync(factory, Array.Empty<TimeSpan>());
            Task nextRun = null;
            var terminalCount = 0;
            harness.Channel.HealthChanged += snapshot =>
            {
                if (snapshot.State != ClientControlChannelState.Disconnected ||
                    ++terminalCount != 1)
                {
                    return;
                }

                nextRun = harness.Channel.RunAsync(CancellationToken.None);
            };

            var firstRun = harness.Channel.RunAsync(CancellationToken.None);
            await firstRun;
            Assert.That(nextRun, Is.Not.Null);
            await nextRun;

            Assert.That(harness.HttpApi.TicketCount, Is.EqualTo(2));
            Assert.That(harness.Channel.Snapshot.Generation, Is.GreaterThan(1));
            Assert.That(
                harness.Channel.Snapshot.CloseReason,
                Is.EqualTo(ClientControlCloseReason.ProtocolFailure));
            await harness.Channel.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 确认 App Scope stop 取消 pending receive、执行 close 并只释放 socket 一次。
        /// </summary>
        [Test]
        public async Task Stop_PendingReceive_CancelsAndDisposesSocket()
        {
            var socket = new FakeSocket(false, ClientWebSocketConnectRequest.ControlSubprotocol);
            var harness = await CreateHarnessAsync(
                new FakeSocketFactory(socket),
                Array.Empty<TimeSpan>());
            var runTask = harness.Channel.RunAsync(CancellationToken.None);
            await WaitForStateAsync(harness.Channel, ClientControlChannelState.Connected);

            await harness.Channel.StopAsync(CancellationToken.None);
            await runTask;

            Assert.That(harness.Channel.Snapshot.State, Is.EqualTo(ClientControlChannelState.Stopped));
            Assert.That(socket.CloseCount, Is.EqualTo(1));
            Assert.That(socket.DisposeCount, Is.EqualTo(1));
        }

        /// <summary>确认active run期间拒绝第二个run，并保持唯一run与socket owner。</summary>
        [Test]
        public async Task Run_ConcurrentStartRejectsSecondOwner()
        {
            var socket = new FakeSocket(
                false,
                ClientWebSocketConnectRequest.ControlSubprotocol);
            var harness = await CreateHarnessAsync(
                new FakeSocketFactory(socket),
                Array.Empty<TimeSpan>());

            var run = harness.Channel.RunAsync(CancellationToken.None);
            await WaitForStateAsync(harness.Channel, ClientControlChannelState.Connected);

            Assert.Throws<InvalidOperationException>(
                () => harness.Channel.RunAsync(CancellationToken.None));
            Assert.That(harness.Channel.QualificationRunOwnerCount, Is.EqualTo(1));

            await harness.Channel.StopAsync(CancellationToken.None);
            await run;
            Assert.That(harness.Channel.QualificationRunOwnerCount, Is.EqualTo(0));
            Assert.That(socket.DisposeCount, Is.EqualTo(1));
        }

        /// <summary>确认Development资格故障只取消current attempt并使用新ticket恢复同一run。</summary>
        [Test]
        public async Task QualificationFault_CancelsAttemptAndRecoversSameRun()
        {
            var first = new FakeSocket(false, ClientWebSocketConnectRequest.ControlSubprotocol);
            var second = new FakeSocket(false, ClientWebSocketConnectRequest.ControlSubprotocol);
            var harness = await CreateHarnessAsync(
                new FakeSocketFactory(first, second),
                new[] { TimeSpan.FromMilliseconds(1) },
                delay: new FakeDelay());
            var run = harness.Channel.RunAsync(CancellationToken.None);
            await WaitForAttemptAsync(harness.Channel, 1, ClientControlChannelState.Connected);

            Assert.That(harness.Channel.InjectQualificationTransportDisconnect(), Is.True);
            await WaitForAttemptAsync(harness.Channel, 2, ClientControlChannelState.Connected);

            Assert.That(first.DisposeCount, Is.EqualTo(1));
            Assert.That(harness.HttpApi.TicketCount, Is.EqualTo(2));
            Assert.That(harness.Channel.QualificationRunOwnerCount, Is.EqualTo(1));
            await harness.Channel.StopAsync(CancellationToken.None);
            await run;
        }

        /// <summary>
        /// 确认停止后尚未 drain 的旧 run PUSH 不会再调用 subscriber。
        /// </summary>
        [Test]
        public async Task Stop_QueuedPush_DoesNotInvokeSubscriber()
        {
            var socket = new FakeSocket(
                false,
                ClientWebSocketConnectRequest.ControlSubprotocol,
                ReceiveStep.Binary(EnvelopeBytes(500, 1, Maintenance()), true),
                ReceiveStep.Close(WebSocketCloseStatus.PolicyViolation));
            var harness = await CreateHarnessAsync(
                new FakeSocketFactory(socket),
                Array.Empty<TimeSpan>());
            var received = new List<ClientControlNotification>();
            harness.Channel.PushReceived += received.Add;

            await harness.Channel.RunAsync(CancellationToken.None);
            Assert.That(harness.Dispatcher.PendingCount, Is.EqualTo(1));

            await harness.Channel.StopAsync(CancellationToken.None);
            var drain = harness.Dispatcher.Drain(8);

            Assert.That(drain.Errors, Is.Empty);
            Assert.That(received, Is.Empty);
        }

        /// <summary>确认上一connection已排队PUSH不能在同run下一attempt连上后重新进入业务投影。</summary>
        [Test]
        public async Task Recovery_DropsQueuedPushFromRetiredConnectionAttempt()
        {
            var first = new FakeSocket(
                false,
                ClientWebSocketConnectRequest.ControlSubprotocol,
                ReceiveStep.Binary(EnvelopeBytes(500, 1, Maintenance()), true),
                ReceiveStep.Close(WebSocketCloseStatus.NormalClosure));
            var second = new FakeSocket(
                false,
                ClientWebSocketConnectRequest.ControlSubprotocol);
            var harness = await CreateHarnessAsync(
                new FakeSocketFactory(first, second),
                new[] { TimeSpan.FromMilliseconds(1) });
            var received = new List<ClientControlNotification>();
            harness.Channel.PushReceived += received.Add;

            var run = harness.Channel.RunAsync(CancellationToken.None);
            await WaitForAttemptAsync(harness.Channel, 2, ClientControlChannelState.Connected);
            Assert.That(harness.Dispatcher.PendingCount, Is.EqualTo(1));

            var drain = harness.Dispatcher.Drain(8);

            Assert.That(drain.Errors, Is.Empty);
            Assert.That(received, Is.Empty);
            await harness.Channel.StopAsync(CancellationToken.None);
            await run;
        }

        /// <summary>
        /// 创建初始化、配置和认证均已完成的 control test harness。
        /// </summary>
        /// <param name="factory">按 attempt 顺序提供 fake socket 的 factory。</param>
        /// <param name="retryDelays">本次测试的有限恢复 policy。</param>
        /// <param name="dispatcherCapacity">主线程 callback 容量。</param>
        /// <param name="delay">可选可观察 delay；为空时使用立即完成替换。</param>
        /// <returns>可显式运行的完整纯 C# 对象图。</returns>
        private static async Task<ChannelHarness> CreateHarnessAsync(
            FakeSocketFactory factory,
            IReadOnlyList<TimeSpan> retryDelays,
            int dispatcherCapacity = 8,
            FakeDelay delay = null)
        {
            var configurationStore = new ClientConfigurationStore();
            await configurationStore.InitializeAsync(CancellationToken.None);
            configurationStore.Publish(new ClientConfigurationSnapshot(
                new ClientVersionInfo(1, "0.1.0", "0.1.0"),
                new ClientBootstrapConfiguration(
                    new[] { new ClientEndpoint(ClientEndpointChannel.Wss, "127.0.0.1", 8443) },
                    new ClientPublicLimits(4096, 65536))));
            var httpApi = new FakeHttpApi();
            var session = new SessionCoordinator(
                configurationStore,
                httpApi,
                new FakeClock(1000),
                new FakeClientSecureSessionStore(),
                FakeClientSecureSessionStore.EnvironmentBinding);
            await session.InitializeAsync(CancellationToken.None);
            var login = await session.LoginAsync("fixture", "password", CancellationToken.None);
            Assert.That(login.IsSuccess, Is.True);
            var dispatcher = new MainThreadDispatcher(
                Environment.CurrentManagedThreadId,
                dispatcherCapacity);
            await dispatcher.InitializeAsync(CancellationToken.None);
            var channel = new ClientControlChannel(
                ClientEnvironment.Create(
                    ClientEnvironmentKind.Test,
                    "http://127.0.0.1:8080/",
                    "0.1.0",
                    1),
                configurationStore,
                session,
                factory,
                new ClientControlCodec(new ClientControlCatalog()),
                new ClientControlProtocolAdapter(),
                dispatcher,
                delay ?? new FakeDelay(),
                retryDelays);
            await channel.InitializeAsync(CancellationToken.None);
            return new ChannelHarness(channel, dispatcher, session, httpApi);
        }

        /// <summary>等待指定attempt进入目标状态，不使用真实网络或业务延时。</summary>
        /// <param name="channel">被测control owner。</param>
        /// <param name="attempt">目标attempt序号。</param>
        /// <param name="state">目标健康状态。</param>
        /// <returns>目标snapshot已提交时完成。</returns>
        private static async Task WaitForAttemptAsync(
            ClientControlChannel channel,
            int attempt,
            ClientControlChannelState state)
        {
            var completion = new TaskCompletionSource<bool>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            Action<ClientControlChannelSnapshot> handler = snapshot =>
            {
                if (snapshot.Attempt == attempt && snapshot.State == state)
                {
                    completion.TrySetResult(true);
                }
            };
            channel.HealthChanged += handler;
            try
            {
                handler(channel.Snapshot);
                using (var deadline = new CancellationTokenSource(TimeSpan.FromSeconds(5)))
                using (deadline.Token.Register(() => completion.TrySetCanceled()))
                {
                    await completion.Task;
                }

                return;
            }
            catch (TaskCanceledException)
            {
                var final = channel.Snapshot;
                Assert.Fail(
                    $"Control attempt未进入目标状态：expected={attempt}/{state}, " +
                    $"actual={final.Attempt}/{final.State}/{final.CloseReason}。");
            }
            finally
            {
                channel.HealthChanged -= handler;
            }
        }

        /// <summary>
        /// 等待异步 receive pump 到达指定状态，并给测试设置有限 deadline。
        /// </summary>
        /// <param name="channel">待观察的 control owner。</param>
        /// <param name="expected">期望状态。</param>
        /// <returns>状态到达时完成的任务。</returns>
        private static async Task WaitForStateAsync(
            ClientControlChannel channel,
            ClientControlChannelState expected)
        {
            for (var attempt = 0; attempt < 100; attempt++)
            {
                if (channel.Snapshot.State == expected)
                {
                    return;
                }

                await Task.Delay(1);
            }

            Assert.Fail($"Control channel 未在测试 deadline 内进入 {expected}。");
        }

        /// <summary>
        /// 创建确定性 binary PUSH envelope。
        /// </summary>
        /// <param name="messageID">冻结 route ID。</param>
        /// <param name="sequence">当前 connection sequence。</param>
        /// <param name="payload">Generated payload。</param>
        /// <returns>单条 WebSocket message bytes。</returns>
        private static byte[] EnvelopeBytes(uint messageID, ulong sequence, IMessage payload)
        {
            return new ReliableEnvelope
            {
                ProtocolVersion = 1,
                MessageId = messageID,
                Kind = MessageKind.Push,
                Sequence = sequence,
                TimestampMs = 1700000000000,
                Payload = payload.ToByteString(),
            }.ToByteArray();
        }

        /// <summary>创建满足 Application adapter 字段合同的维护通知。</summary>
        /// <returns>可用于 channel receive fixture 的 generated payload。</returns>
        private static MaintenancePush Maintenance()
        {
            return new MaintenancePush
            {
                StartsAtMs = 1000,
                ExpectedEndAtMs = 2000,
                MessageKey = "maintenance.fixture",
            };
        }

        /// <summary>
        /// 聚合测试需要观察的强类型对象，不提供任意类型查询。
        /// </summary>
        private sealed class ChannelHarness
        {
            /// <summary>
            /// 创建测试对象图引用集合。
            /// </summary>
            /// <param name="channel">待测试 control owner。</param>
            /// <param name="dispatcher">待手动 drain 的主线程 dispatcher。</param>
            /// <param name="session">待观察的唯一 Session owner。</param>
            /// <param name="httpApi">待观察 ticket 次数的 fake HTTP API。</param>
            internal ChannelHarness(
                ClientControlChannel channel,
                MainThreadDispatcher dispatcher,
                SessionCoordinator session,
                FakeHttpApi httpApi)
            {
                Channel = channel;
                Dispatcher = dispatcher;
                Session = session;
                HttpApi = httpApi;
            }

            /// <summary>
            /// 获取待测试 control owner。
            /// </summary>
            internal ClientControlChannel Channel { get; }

            /// <summary>
            /// 获取捕获当前 test thread 的 dispatcher。
            /// </summary>
            internal MainThreadDispatcher Dispatcher { get; }

            /// <summary>
            /// 获取唯一 Session owner。
            /// </summary>
            internal SessionCoordinator Session { get; }

            /// <summary>
            /// 获取 fake HTTP API。
            /// </summary>
            internal FakeHttpApi HttpApi { get; }
        }

        /// <summary>
        /// 保存 fake socket 单次 receive 脚本。
        /// </summary>
        private sealed class ReceiveStep
        {
            /// <summary>
            /// 创建一次 receive 脚本。
            /// </summary>
            /// <param name="bytes">待复制到调用方 buffer 的 bytes。</param>
            /// <param name="messageType">Binary 或 Close。</param>
            /// <param name="endOfMessage">当前 fragment 是否结束 message。</param>
            /// <param name="closeStatus">Close 时的标准状态。</param>
            private ReceiveStep(
                byte[] bytes,
                WebSocketMessageType messageType,
                bool endOfMessage,
                WebSocketCloseStatus? closeStatus)
            {
                Bytes = bytes;
                MessageType = messageType;
                EndOfMessage = endOfMessage;
                CloseStatus = closeStatus;
            }

            /// <summary>
            /// 获取待复制 bytes。
            /// </summary>
            internal byte[] Bytes { get; }

            /// <summary>
            /// 获取 message 类型。
            /// </summary>
            internal WebSocketMessageType MessageType { get; }

            /// <summary>
            /// 获取 fragment 结束标记。
            /// </summary>
            internal bool EndOfMessage { get; }

            /// <summary>
            /// 获取可选 close status。
            /// </summary>
            internal WebSocketCloseStatus? CloseStatus { get; }

            /// <summary>
            /// 创建 binary fragment。
            /// </summary>
            /// <param name="bytes">Fragment bytes。</param>
            /// <param name="endOfMessage">是否结束完整 message。</param>
            /// <returns>Binary receive 脚本。</returns>
            internal static ReceiveStep Binary(byte[] bytes, bool endOfMessage)
            {
                return new ReceiveStep(bytes, WebSocketMessageType.Binary, endOfMessage, null);
            }

            /// <summary>
            /// 创建必须被 control receive pump 拒绝的 text message。
            /// </summary>
            /// <param name="bytes">不可信 text bytes。</param>
            /// <returns>完整 text receive 脚本。</returns>
            internal static ReceiveStep Text(byte[] bytes)
            {
                return new ReceiveStep(bytes, WebSocketMessageType.Text, true, null);
            }

            /// <summary>
            /// 创建 peer close 脚本。
            /// </summary>
            /// <param name="closeStatus">标准 close status。</param>
            /// <returns>Close receive 脚本。</returns>
            internal static ReceiveStep Close(WebSocketCloseStatus closeStatus)
            {
                return new ReceiveStep(Array.Empty<byte>(), WebSocketMessageType.Close, true, closeStatus);
            }
        }

        /// <summary>
        /// 提供确定性 connect/receive/close 行为的只读 fake socket。
        /// </summary>
        private sealed class FakeSocket : IClientWebSocket
        {
            /// <summary>
            /// 保存按调用顺序消费的 receive 脚本。
            /// </summary>
            private readonly Queue<ReceiveStep> _steps;

            /// <summary>
            /// 指示 connect 是否返回稳定 transport failure。
            /// </summary>
            private readonly bool _failConnect;

            /// <summary>
            /// 创建 fake socket。
            /// </summary>
            /// <param name="failConnect">Connect 时是否失败。</param>
            /// <param name="subProtocol">成功 connect 后公布的协商 subprotocol。</param>
            /// <param name="steps">按顺序返回的 receive 脚本。</param>
            internal FakeSocket(bool failConnect, string subProtocol, params ReceiveStep[] steps)
            {
                _failConnect = failConnect;
                SubProtocol = subProtocol;
                _steps = new Queue<ReceiveStep>(steps ?? Array.Empty<ReceiveStep>());
                State = WebSocketState.None;
            }

            /// <summary>
            /// 获取 fake socket 当前状态。
            /// </summary>
            public WebSocketState State { get; private set; }

            /// <summary>
            /// 获取测试指定的协商 subprotocol。
            /// </summary>
            public string SubProtocol { get; }

            /// <summary>
            /// 获取实际收到的脱敏 connect request。
            /// </summary>
            internal ClientWebSocketConnectRequest ConnectRequest { get; private set; }

            /// <summary>
            /// 获取 receive 调用次数。
            /// </summary>
            internal int ReceiveCount { get; private set; }

            /// <summary>
            /// 获取 close 调用次数。
            /// </summary>
            internal int CloseCount { get; private set; }

            /// <summary>
            /// 获取 dispose 调用次数。
            /// </summary>
            internal int DisposeCount { get; private set; }

            /// <summary>
            /// 捕获 request 并按脚本成功或失败。
            /// </summary>
            /// <param name="request">单次 ticket request。</param>
            /// <param name="cancellationToken">运行取消信号。</param>
            /// <returns>Connect 结果任务。</returns>
            public Task ConnectAsync(
                ClientWebSocketConnectRequest request,
                CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                ConnectRequest = request;
                if (_failConnect)
                {
                    throw new ClientWebSocketTransportException();
                }

                State = WebSocketState.Open;
                return Task.CompletedTask;
            }

            /// <summary>
            /// 返回下一段脚本；无脚本时等待 cancellation 模拟 idle receive。
            /// </summary>
            /// <param name="buffer">调用方有界 receive segment。</param>
            /// <param name="cancellationToken">运行取消信号。</param>
            /// <returns>下一段 receive 结果。</returns>
            public async Task<ClientWebSocketReadResult> ReceiveAsync(
                ArraySegment<byte> buffer,
                CancellationToken cancellationToken)
            {
                ReceiveCount++;
                if (_steps.Count == 0)
                {
                    await Task.Delay(Timeout.Infinite, cancellationToken);
                }

                var step = _steps.Dequeue();
                if (step.Bytes.Length > buffer.Count)
                {
                    throw new InvalidOperationException("测试 receive fragment 超过调用方 buffer。");
                }

                Array.Copy(step.Bytes, 0, buffer.Array, buffer.Offset, step.Bytes.Length);
                if (step.MessageType == WebSocketMessageType.Close)
                {
                    State = WebSocketState.CloseReceived;
                }

                return new ClientWebSocketReadResult(
                    step.Bytes.Length,
                    step.MessageType,
                    step.EndOfMessage,
                    step.CloseStatus);
            }

            /// <summary>
            /// 记录有界 close 并使 pending receive cancellation 可结束。
            /// </summary>
            /// <param name="cancellationToken">停止 deadline。</param>
            /// <returns>同步 close 完成任务。</returns>
            public Task CloseAsync(CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                CloseCount++;
                State = WebSocketState.Closed;
                return Task.CompletedTask;
            }

            /// <summary>
            /// 记录 adapter 所有权释放。
            /// </summary>
            public void Dispose()
            {
                DisposeCount++;
                State = WebSocketState.Closed;
            }
        }

        /// <summary>
        /// 按 attempt 顺序返回 socket，防止连接复用旧 header/ticket。
        /// </summary>
        private sealed class FakeSocketFactory : IClientWebSocketFactory
        {
            /// <summary>
            /// 保存尚未交付的单次 socket。
            /// </summary>
            private readonly Queue<IClientWebSocket> _sockets;

            /// <summary>
            /// 创建有界 socket 序列。
            /// </summary>
            /// <param name="sockets">每次 factory 调用依次返回的 socket。</param>
            internal FakeSocketFactory(params IClientWebSocket[] sockets)
            {
                _sockets = new Queue<IClientWebSocket>(sockets ?? Array.Empty<IClientWebSocket>());
            }

            /// <summary>
            /// 返回下一 attempt 的独立 socket。
            /// </summary>
            /// <returns>尚未使用的 fake socket。</returns>
            public IClientWebSocket Create()
            {
                return _sockets.Dequeue();
            }
        }

        /// <summary>
        /// 记录 backoff 而不让 EditMode test 真实等待。
        /// </summary>
        private sealed class FakeDelay : IClientDelay
        {
            /// <summary>
            /// 获取按发生顺序记录的 delay。
            /// </summary>
            internal List<TimeSpan> Delays { get; } = new List<TimeSpan>();

            /// <summary>
            /// 记录 delay 并立即完成。
            /// </summary>
            /// <param name="delay">固定 backoff。</param>
            /// <param name="cancellationToken">运行取消信号。</param>
            /// <returns>立即完成任务。</returns>
            public Task DelayAsync(TimeSpan delay, CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                Delays.Add(delay);
                return Task.CompletedTask;
            }
        }

        /// <summary>
        /// 提供固定 Unix 毫秒的 ticket expiry 测试时钟。
        /// </summary>
        private sealed class FakeClock : IClientClock
        {
            /// <summary>
            /// 创建固定测试时钟。
            /// </summary>
            /// <param name="utcNowMilliseconds">当前 Unix 时间，单位为毫秒。</param>
            internal FakeClock(long utcNowMilliseconds)
            {
                UtcNowMilliseconds = utcNowMilliseconds;
            }

            /// <summary>
            /// 获取固定 Unix 时间，单位为毫秒。
            /// </summary>
            public long UtcNowMilliseconds { get; }
        }

        /// <summary>
        /// 提供认证与每次 attempt 新 ticket 的强类型 HTTP fake。
        /// </summary>
        private sealed class FakeHttpApi : IClientBootstrapGateway, IClientSessionGateway
        {
            /// <summary>
            /// 获取已签发 ticket 次数。
            /// </summary>
            internal int TicketCount { get; private set; }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientVersionInfo>> GetVersionAsync(CancellationToken cancellationToken)
            {
                throw new NotSupportedException();
            }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientBootstrapConfiguration>> GetBootstrapConfigurationAsync(
                CancellationToken cancellationToken)
            {
                throw new NotSupportedException();
            }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientAuthentication>> RegisterAsync(ClientRegisterGatewayRequest request, CancellationToken cancellationToken)
            {
                throw new NotSupportedException();
            }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientAuthentication>> LoginAsync(ClientLoginGatewayRequest request, CancellationToken cancellationToken)
            {
                return Task.FromResult(ClientGatewayResult<ClientAuthentication>.Success(
                    new ClientAuthentication(
                        new ClientAccountSummary("account-fixture", "Fixture", 1),
                        new ClientSessionSummary("session-fixture", 1, 9000),
                        new ClientTokenPair("access-fixture", "refresh-fixture", 7000, 8000),
                        Array.Empty<ClientEndpoint>())));
            }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientTokenPair>> RefreshAsync(ClientCredentialGatewayRequest request, CancellationToken cancellationToken)
            {
                throw new NotSupportedException();
            }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientGatewayEmpty>> LogoutAsync(ClientCredentialGatewayRequest request, CancellationToken cancellationToken)
            {
                throw new NotSupportedException();
            }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientConnectionTicket>> IssueConnectionTicketAsync(ClientConnectionTicketGatewayRequest request, CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                TicketCount++;
                return Task.FromResult(ClientGatewayResult<ClientConnectionTicket>.Success(
                    new ClientConnectionTicket(
                        TicketCount.ToString("x32"),
                        new ClientEndpoint(ClientEndpointChannel.Wss, "127.0.0.1", 8443),
                        new[] { ClientConnectionScope.Control },
                        5000)));
            }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientWorldBootstrap>> GetWorldBootstrapAsync(ClientCredentialGatewayRequest request, CancellationToken cancellationToken)
            {
                throw new NotSupportedException();
            }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientVisitReservation>> AcceptVisitInviteAsync(ClientAcceptVisitInviteGatewayRequest request, CancellationToken cancellationToken)
            {
                throw new NotSupportedException();
            }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientWorldAdmission>> IssueWorldAdmissionAsync(ClientWorldAdmissionGatewayRequest request, CancellationToken cancellationToken)
            {
                throw new NotSupportedException();
            }
        }
    }
}
