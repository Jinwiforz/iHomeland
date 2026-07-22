using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using Google.Protobuf;
using IHomeland.Client.Application.Gameplay;
using IHomeland.Client.Application.Session;
using IHomeland.Client.Core.Configuration;
using IHomeland.Client.Core.Lifetime;
using IHomeland.Client.Infrastructure.Http;
using IHomeland.Client.Infrastructure.Tcp;
using IHomeland.Protocol.Common.V1;
using IHomeland.Protocol.Visit.V1;
using IHomeland.Protocol.World.V1;
using NUnit.Framework;

namespace IHomeland.Client.Tests.EditMode
{
    /// <summary>
    /// 验证 gameplay channel 的显式连接、pending、PUSH、背压与关闭所有权。
    /// </summary>
    public sealed class ClientGameplayChannelTests
    {
        /// <summary>
        /// 验证初始化零网络副作用、response correlation、主线程 PUSH 与 safe-return gate。
        /// </summary>
        /// <returns>等待可控 connection 的完整生命周期。</returns>
        [Test]
        public async Task ExplicitConnectCorrelatesResponseAndDispatchesPushes()
        {
            var fixture = await GameplayFixture.CreateAsync(blockGameplayWrites: false);
            Assert.That(fixture.Factory.ConnectCount, Is.EqualTo(0));
            Assert.That(fixture.Channel.Snapshot.State, Is.EqualTo(ClientGameplayChannelState.Ready));

            Assert.That(await fixture.ConnectOwnWorldAsync(), Is.True);
            Assert.That(fixture.Factory.ConnectCount, Is.EqualTo(1));
            Assert.That(fixture.Connection.WriteCount, Is.EqualTo(1));
            Assert.That(fixture.Channel.Snapshot.State, Is.EqualTo(ClientGameplayChannelState.Active));

            var send = fixture.Channel.SendAsync(
                ClientGameplayCatalog.WorldSnapshot,
                new WorldSnapshotRequest(),
                CancellationToken.None);
            await fixture.Connection.WaitForWritesAsync(2);
            var outbound = ParseFramedEnvelope(fixture.Connection.GetWrite(1));
            fixture.Connection.Enqueue(ClientGameplayFramer.Frame(new ReliableEnvelope
            {
                ProtocolVersion = 1,
                MessageId = 2001,
                Kind = MessageKind.Response,
                RequestId = outbound.RequestId,
                Sequence = 1,
                TimestampMs = 1,
                Payload = new WorldSnapshotResponse().ToByteString(),
            }.ToByteArray()));
            Assert.That((await send).IsSuccess, Is.True);

            var worldPushes = 0;
            fixture.Channel.WorldSnapshotReceived += _ => worldPushes++;
            fixture.Connection.Enqueue(ClientGameplayFramer.Frame(new ReliableEnvelope
            {
                ProtocolVersion = 1,
                MessageId = 2002,
                Kind = MessageKind.Push,
                Sequence = 2,
                TimestampMs = 1,
                Payload = new WorldSnapshotPush().ToByteString(),
            }.ToByteArray()));
            await WaitUntilAsync(() => fixture.Dispatcher.PendingCount == 1);
            fixture.Dispatcher.Drain(8);
            Assert.That(worldPushes, Is.EqualTo(1));

            var safeReturns = 0;
            fixture.Channel.SafeReturnReceived += _ => safeReturns++;
            fixture.Connection.Enqueue(ClientGameplayFramer.Frame(new ReliableEnvelope
            {
                ProtocolVersion = 1,
                MessageId = 2122,
                Kind = MessageKind.Push,
                Sequence = 3,
                TimestampMs = 1,
                Payload = new VisitSafeReturnPush().ToByteString(),
            }.ToByteArray()));
            await WaitUntilAsync(() => fixture.Dispatcher.PendingCount == 1);
            fixture.Dispatcher.Drain(8);
            Assert.That(safeReturns, Is.EqualTo(1));
            Assert.That(fixture.Channel.Snapshot.State, Is.EqualTo(ClientGameplayChannelState.Ready));
            Assert.That(fixture.Channel.Snapshot.CloseReason, Is.EqualTo(ClientGameplayCloseReason.ApplicationReturn));
            await fixture.StopAsync();
        }

        /// <summary>
        /// 验证 active generation 的 heartbeat 使用独立 typed route、精确 correlation，并能在 close 后建立新 generation。
        /// </summary>
        /// <returns>等待两个 generation 的 heartbeat 与 owner 关闭。</returns>
        [Test]
        public async Task HeartbeatUsesTypedCorrelationAndDoesNotSurviveGenerationClose()
        {
            var fixture = await GameplayFixture.CreateAsync(blockGameplayWrites: false);
            Assert.That(await fixture.ConnectOwnWorldAsync(), Is.True);
            await WaitUntilAsync(() => fixture.HeartbeatDelay.PendingCount == 1);
            fixture.HeartbeatDelay.ReleaseNext();
            await fixture.Connection.WaitForWritesAsync(2);
            var heartbeat = ParseFramedEnvelope(fixture.Connection.GetWrite(1));
            Assert.That(heartbeat.MessageId, Is.EqualTo(1));
            Assert.That(heartbeat.Kind, Is.EqualTo(MessageKind.Request));
            Assert.That(heartbeat.RequestId.Length, Is.EqualTo(16));
            fixture.Connection.Enqueue(ClientGameplayFramer.Frame(new ReliableEnvelope
            {
                ProtocolVersion = 1,
                MessageId = 2,
                Kind = MessageKind.Response,
                RequestId = heartbeat.RequestId,
                Sequence = 1,
                TimestampMs = 1,
                Payload = new GameplayHeartbeatResponse().ToByteString(),
            }.ToByteArray()));
            await WaitUntilAsync(() => fixture.HeartbeatDelay.PendingCount == 1);

            await fixture.Channel.CloseAsync(CancellationToken.None);
            Assert.That(fixture.Channel.Snapshot.State, Is.EqualTo(ClientGameplayChannelState.Ready));
            Assert.That(fixture.Connection.IsDisposed, Is.True);
            Assert.That(await fixture.ConnectOwnWorldAsync(), Is.True);
            Assert.That(fixture.Channel.Snapshot.Generation, Is.EqualTo(2));
            await fixture.StopAsync();
        }

        /// <summary>
        /// 验证 heartbeat 拒绝会终止 current generation，并只发布一次可重连断线结果。
        /// </summary>
        /// <returns>等待 heartbeat failure、owner 退出与主线程 terminal callback。</returns>
        [Test]
        public async Task HeartbeatFailureClosesGenerationAndPublishesTerminalDisconnect()
        {
            var fixture = await GameplayFixture.CreateAsync(blockGameplayWrites: false);
            Assert.That(await fixture.ConnectOwnWorldAsync(), Is.True);
            var disconnects = 0;
            fixture.Channel.UnexpectedDisconnect += _ => disconnects++;
            await WaitUntilAsync(() => fixture.HeartbeatDelay.PendingCount == 1);
            fixture.HeartbeatDelay.ReleaseNext();
            await fixture.Connection.WaitForWritesAsync(2);
            var heartbeat = ParseFramedEnvelope(fixture.Connection.GetWrite(1));
            fixture.Connection.Enqueue(ClientGameplayFramer.Frame(new ReliableEnvelope
            {
                ProtocolVersion = 1,
                MessageId = 2,
                Kind = MessageKind.Error,
                RequestId = heartbeat.RequestId,
                Sequence = 1,
                TimestampMs = 1,
                Payload = new ErrorPayload
                {
                    Code = 101,
                    MessageKey = "error.auth.forbidden",
                }.ToByteString(),
            }.ToByteArray()));

            await DrainUntilAsync(fixture.Dispatcher, () => disconnects == 1);
            Assert.That(fixture.Channel.Snapshot.State, Is.EqualTo(ClientGameplayChannelState.Ready));
            Assert.That(fixture.Channel.Snapshot.CloseReason, Is.EqualTo(ClientGameplayCloseReason.Protocol));
            Assert.That(disconnects, Is.EqualTo(1));
            await fixture.StopAsync();
        }

        /// <summary>验证Development资格故障提交真实transport terminal并释放socket与heartbeat owner。</summary>
        [Test]
        public async Task QualificationFault_UsesTransportTerminalPath()
        {
            var fixture = await GameplayFixture.CreateAsync(blockGameplayWrites: false);
            Assert.That(await fixture.ConnectOwnWorldAsync(), Is.True);
            var disconnects = 0;
            ClientGameplayChannelSnapshot terminal = null;
            fixture.Channel.UnexpectedDisconnect += snapshot =>
            {
                disconnects++;
                terminal = snapshot;
            };

            Assert.That(fixture.Channel.InjectQualificationTransportDisconnect(), Is.True);
            await DrainUntilAsync(fixture.Dispatcher, () => disconnects == 1);

            Assert.That(terminal, Is.Not.Null);
            Assert.That(terminal.CloseReason, Is.EqualTo(ClientGameplayCloseReason.Transport));
            Assert.That(fixture.Channel.QualificationSocketOwnerCount, Is.Zero);
            Assert.That(fixture.Channel.QualificationHeartbeatOwnerCount, Is.Zero);
            Assert.That(fixture.Channel.QualificationPendingOperationCount, Is.Zero);
            await fixture.StopAsync();
        }

        /// <summary>
        /// 验证 writer encoded-byte budget 先于无界积压拒绝并完成全部 pending。
        /// </summary>
        /// <returns>等待 blocked writer、背压关闭与 pending 清理。</returns>
        [Test]
        public async Task WriterByteBackpressureClosesConnectionWithoutSilentDrop()
        {
            var fixture = await GameplayFixture.CreateAsync(blockGameplayWrites: true);
            Assert.That(await fixture.ConnectOwnWorldAsync(), Is.True);
            var sends = new List<Task<ClientGameplayResult<VisitKickResponse>>>();
            ClientGameplayResult<VisitKickResponse> rejected = null;
            for (var index = 0; index < 128; index++)
            {
                var send = fixture.Channel.SendAsync(
                    ClientGameplayCatalog.VisitKick,
                    new VisitKickCommand
                    {
                        TargetVisitorId = new string('a', 3900),
                        ExpectedRevision = 1,
                    },
                    CancellationToken.None);
                sends.Add(send);
                if (send.IsCompleted)
                {
                    var result = await send;
                    if (result.Failure == ClientGameplayFailureKind.Backpressure)
                    {
                        rejected = result;
                        break;
                    }
                }
            }

            Assert.That(rejected, Is.Not.Null);
            Assert.That(fixture.Channel.Snapshot.CloseReason, Is.EqualTo(ClientGameplayCloseReason.Backpressure));
            foreach (var send in sends)
            {
                var result = await send;
                Assert.That(
                    result.Failure == ClientGameplayFailureKind.Disconnected ||
                    result.Failure == ClientGameplayFailureKind.Backpressure,
                    Is.True);
            }

            await fixture.StopAsync();
        }

        /// <summary>
        /// 验证小 frame 也受 writer item budget 约束，不能只依赖 encoded-byte 上限。
        /// </summary>
        /// <returns>等待 blocked writer、item 背压关闭与 pending 清理。</returns>
        [Test]
        public async Task WriterItemBackpressureClosesConnectionWithoutSilentDrop()
        {
            var fixture = await GameplayFixture.CreateAsync(blockGameplayWrites: true);
            Assert.That(await fixture.ConnectOwnWorldAsync(), Is.True);
            var sends = new List<Task<ClientGameplayResult<WorldSnapshotResponse>>>();
            ClientGameplayResult<WorldSnapshotResponse> rejected = null;
            for (var index = 0; index < 80; index++)
            {
                var send = fixture.Channel.SendAsync(
                    ClientGameplayCatalog.WorldSnapshot,
                    new WorldSnapshotRequest(),
                    CancellationToken.None);
                sends.Add(send);
                if (send.IsCompleted)
                {
                    var result = await send;
                    if (result.Failure == ClientGameplayFailureKind.Backpressure)
                    {
                        rejected = result;
                        break;
                    }
                }
            }

            Assert.That(rejected, Is.Not.Null);
            Assert.That(fixture.Channel.Snapshot.CloseReason, Is.EqualTo(ClientGameplayCloseReason.Backpressure));
            foreach (var send in sends)
            {
                var result = await send;
                Assert.That(
                    result.Failure == ClientGameplayFailureKind.Disconnected ||
                    result.Failure == ClientGameplayFailureKind.Backpressure,
                    Is.True);
            }

            await fixture.StopAsync();
        }

        /// <summary>
        /// 验证 current control generation invalidation 立即撤销 gameplay transport。
        /// </summary>
        /// <returns>等待显式连接与失效清理。</returns>
        [Test]
        public async Task SessionInvalidationClosesMatchingGeneration()
        {
            var fixture = await GameplayFixture.CreateAsync(blockGameplayWrites: false);
            Assert.That(await fixture.ConnectOwnWorldAsync(), Is.True);

            fixture.Channel.InvalidateSession(fixture.SessionGeneration);

            Assert.That(fixture.Channel.Snapshot.State, Is.EqualTo(ClientGameplayChannelState.Ready));
            Assert.That(fixture.Channel.Snapshot.CloseReason, Is.EqualTo(ClientGameplayCloseReason.SessionInvalidated));
            Assert.That(fixture.Connection.IsDisposed, Is.True);
            await fixture.StopAsync();
        }

        /// <summary>
        /// 验证 App Scope 停止会撤销 socket 并恰好完成尚未响应的 pending。
        /// </summary>
        /// <returns>等待 operation 发送、逆序停止与 pending 结果。</returns>
        [Test]
        public async Task ShutdownCompletesPendingAndDisposesConnection()
        {
            var fixture = await GameplayFixture.CreateAsync(blockGameplayWrites: false);
            Assert.That(await fixture.ConnectOwnWorldAsync(), Is.True);
            var send = fixture.Channel.SendAsync(
                ClientGameplayCatalog.WorldSnapshot,
                new WorldSnapshotRequest(),
                CancellationToken.None);
            await fixture.Connection.WaitForWritesAsync(2);

            await fixture.Channel.StopAsync(CancellationToken.None);

            Assert.That((await send).Failure, Is.EqualTo(ClientGameplayFailureKind.Disconnected));
            Assert.That(fixture.Channel.Snapshot.State, Is.EqualTo(ClientGameplayChannelState.Stopped));
            Assert.That(fixture.Connection.IsDisposed, Is.True);
            await fixture.StopAsync();
        }

        /// <summary>
        /// 验证调用方取消只完成本地等待，迟到 response 仍被 current reader 安全消费。
        /// </summary>
        /// <returns>等待取消、迟到 response 与下一次正常 operation。</returns>
        [Test]
        public async Task CallerCancellationKeepsLateResponseCorrelated()
        {
            var fixture = await GameplayFixture.CreateAsync(blockGameplayWrites: false);
            Assert.That(await fixture.ConnectOwnWorldAsync(), Is.True);

            using (var cancellation = new CancellationTokenSource())
            {
                var cancelledSend = fixture.Channel.SendAsync(
                    ClientGameplayCatalog.WorldSnapshot,
                    new WorldSnapshotRequest(),
                    cancellation.Token);
                await fixture.Connection.WaitForWritesAsync(2);
                var cancelledEnvelope = ParseFramedEnvelope(fixture.Connection.GetWrite(1));
                cancellation.Cancel();
                Assert.That(
                    (await cancelledSend).Failure,
                    Is.EqualTo(ClientGameplayFailureKind.CallerCancelled));

                fixture.Connection.Enqueue(ClientGameplayFramer.Frame(new ReliableEnvelope
                {
                    ProtocolVersion = 1,
                    MessageId = 2001,
                    Kind = MessageKind.Response,
                    RequestId = cancelledEnvelope.RequestId,
                    Sequence = 1,
                    TimestampMs = 1,
                    Payload = new WorldSnapshotResponse().ToByteString(),
                }.ToByteArray()));
            }

            var nextSend = fixture.Channel.SendAsync(
                ClientGameplayCatalog.WorldSnapshot,
                new WorldSnapshotRequest(),
                CancellationToken.None);
            await fixture.Connection.WaitForWritesAsync(3);
            var nextEnvelope = ParseFramedEnvelope(fixture.Connection.GetWrite(2));
            fixture.Connection.Enqueue(ClientGameplayFramer.Frame(new ReliableEnvelope
            {
                ProtocolVersion = 1,
                MessageId = 2001,
                Kind = MessageKind.Response,
                RequestId = nextEnvelope.RequestId,
                Sequence = 2,
                TimestampMs = 1,
                Payload = new WorldSnapshotResponse().ToByteString(),
            }.ToByteArray()));

            Assert.That((await nextSend).IsSuccess, Is.True);
            Assert.That(fixture.Channel.Snapshot.State, Is.EqualTo(ClientGameplayChannelState.Active));
            await fixture.StopAsync();
        }

        /// <summary>
        /// 验证 request response 不能改用 command correlation 字段绕过 pending kind 校验。
        /// </summary>
        /// <returns>等待协议关闭完成 pending。</returns>
        [Test]
        public async Task ResponseCorrelationKindMustMatchPendingOperation()
        {
            var fixture = await GameplayFixture.CreateAsync(blockGameplayWrites: false);
            var disconnectCount = 0;
            ClientGameplayChannelSnapshot disconnected = null;
            fixture.Channel.UnexpectedDisconnect += snapshot =>
            {
                disconnectCount++;
                disconnected = snapshot;
            };
            Assert.That(await fixture.ConnectOwnWorldAsync(), Is.True);
            var send = fixture.Channel.SendAsync(
                ClientGameplayCatalog.WorldSnapshot,
                new WorldSnapshotRequest(),
                CancellationToken.None);
            await fixture.Connection.WaitForWritesAsync(2);
            var outbound = ParseFramedEnvelope(fixture.Connection.GetWrite(1));

            fixture.Connection.Enqueue(ClientGameplayFramer.Frame(new ReliableEnvelope
            {
                ProtocolVersion = 1,
                MessageId = 2001,
                Kind = MessageKind.Response,
                CommandId = outbound.RequestId,
                Sequence = 1,
                TimestampMs = 1,
                Payload = new WorldSnapshotResponse().ToByteString(),
            }.ToByteArray()));

            Assert.That((await send).Failure, Is.EqualTo(ClientGameplayFailureKind.Disconnected));
            Assert.That(fixture.Channel.Snapshot.CloseReason, Is.EqualTo(ClientGameplayCloseReason.Protocol));
            await WaitUntilAsync(() => fixture.Dispatcher.PendingCount > 0);
            Assert.That(disconnectCount, Is.Zero);

            await DrainUntilAsync(fixture.Dispatcher, () => disconnectCount == 1);

            Assert.That(disconnectCount, Is.EqualTo(1));
            Assert.That(disconnected, Is.Not.Null);
            Assert.That(disconnected.CloseReason, Is.EqualTo(ClientGameplayCloseReason.Protocol));
            await fixture.StopAsync();
        }

        /// <summary>
        /// 验证 JOIN 首帧只能由 channel 内部注入已消费 admission，并在成功后激活 generation。
        /// </summary>
        /// <returns>等待 JOIN response 与 channel 停止。</returns>
        [Test]
        public async Task PendingJoinKeepsAdmissionInsideGameplayChannel()
        {
            var fixture = await GameplayFixture.CreateAsync(blockGameplayWrites: false);
            Assert.That(await fixture.ConnectVisitAsync(), Is.True);
            Assert.That(fixture.Channel.Snapshot.State, Is.EqualTo(ClientGameplayChannelState.Pending));

            var join = fixture.Channel.JoinPendingVisitAsync(5, CancellationToken.None);
            await fixture.Connection.WaitForWritesAsync(2);
            var outbound = ParseFramedEnvelope(fixture.Connection.GetWrite(1));
            var command = VisitJoinCommand.Parser.ParseFrom(outbound.Payload);
            Assert.That(command.ExpectedRevision, Is.EqualTo(5));
            Assert.That(command.AdmissionCredential, Is.EqualTo("wad1_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"));

            fixture.Connection.Enqueue(ClientGameplayFramer.Frame(new ReliableEnvelope
            {
                ProtocolVersion = 1,
                MessageId = 2110,
                Kind = MessageKind.Response,
                CommandId = outbound.CommandId,
                Sequence = 1,
                TimestampMs = 1,
                Payload = new VisitJoinResponse { Result = new VisitMutationResult() }.ToByteString(),
            }.ToByteArray()));

            Assert.That((await join).IsSuccess, Is.True);
            Assert.That(fixture.Channel.Snapshot.State, Is.EqualTo(ClientGameplayChannelState.Active));
            Assert.That(
                (await fixture.Channel.JoinPendingVisitAsync(6, CancellationToken.None)).Failure,
                Is.EqualTo(ClientGameplayFailureKind.Policy));
            await fixture.StopAsync();
        }

        /// <summary>解析测试捕获的完整 framed envelope。</summary>
        /// <param name="frame">4-byte prefix 与 envelope body。</param>
        /// <returns>生成 ReliableEnvelope。</returns>
        private static ReliableEnvelope ParseFramedEnvelope(byte[] frame)
        {
            var body = new byte[frame.Length - 4];
            Buffer.BlockCopy(frame, 4, body, 0, body.Length);
            return ReliableEnvelope.Parser.ParseFrom(body);
        }

        /// <summary>在短期有界轮询中等待异步 pump 产生可观察结果。</summary>
        /// <param name="condition">无副作用条件。</param>
        /// <returns>条件成立时完成。</returns>
        private static async Task WaitUntilAsync(Func<bool> condition)
        {
            var deadline = DateTime.UtcNow.AddSeconds(2);
            while (!condition())
            {
                if (DateTime.UtcNow >= deadline)
                {
                    Assert.Fail("Gameplay async condition 未在测试 deadline 内成立。");
                }

                await Task.Delay(5);
            }
        }

        /// <summary>持续执行有限主线程批次，直到指定权威 callback 提交状态。</summary>
        /// <param name="dispatcher">被测有界主线程 dispatcher。</param>
        /// <param name="condition">由 callback 提交的无副作用完成条件。</param>
        /// <returns>条件成立时完成。</returns>
        private static async Task DrainUntilAsync(
            MainThreadDispatcher dispatcher,
            Func<bool> condition)
        {
            var deadline = DateTime.UtcNow.AddSeconds(2);
            while (!condition())
            {
                dispatcher.Drain(maximumCallbacks: 8);
                if (condition())
                {
                    return;
                }

                if (DateTime.UtcNow >= deadline)
                {
                    Assert.Fail("Gameplay terminal callback 未在测试 deadline 内提交。");
                }

                await Task.Delay(5);
            }
        }

        /// <summary>
        /// 组合不依赖 Unity Scene 或真实网络的完整 gameplay App Scope 测试图。
        /// </summary>
        private sealed class GameplayFixture
        {
            /// <summary>创建已初始化且认证的 fixture。</summary>
            private GameplayFixture(
                ClientConfigurationStore configurationStore,
                SessionCoordinator session,
                MainThreadDispatcher dispatcher,
                FakeConnectionFactory factory,
                FakeGameplayDelay heartbeatDelay,
                ClientGameplayChannel channel,
                long sessionGeneration)
            {
                ConfigurationStore = configurationStore;
                Session = session;
                Dispatcher = dispatcher;
                Factory = factory;
                HeartbeatDelay = heartbeatDelay;
                Channel = channel;
                SessionGeneration = sessionGeneration;
            }

            /// <summary>获取配置 owner。</summary>
            internal ClientConfigurationStore ConfigurationStore { get; }

            /// <summary>获取 session owner。</summary>
            internal SessionCoordinator Session { get; }

            /// <summary>获取主线程 dispatcher。</summary>
            internal MainThreadDispatcher Dispatcher { get; }

            /// <summary>获取可控 connection factory。</summary>
            internal FakeConnectionFactory Factory { get; }

            /// <summary>获取可控 heartbeat scheduler。</summary>
            internal FakeGameplayDelay HeartbeatDelay { get; }

            /// <summary>获取待测试 gameplay owner。</summary>
            internal ClientGameplayChannel Channel { get; }

            /// <summary>获取 current authenticated session generation。</summary>
            internal long SessionGeneration { get; }

            /// <summary>获取 factory 创建的唯一 connection。</summary>
            internal FakeConnection Connection => Factory.Connection;

            /// <summary>构造配置、session、dispatcher 与 channel，初始化过程不 connect。</summary>
            /// <param name="blockGameplayWrites">是否阻塞 preface 后的 writer。</param>
            /// <returns>Ready 且 authenticated fixture。</returns>
            internal static async Task<GameplayFixture> CreateAsync(bool blockGameplayWrites)
            {
                var endpoint = new ClientEndpoint(ClientEndpointChannel.TlsTcp, "127.0.0.1", 4433);
                var configurationStore = new ClientConfigurationStore();
                await configurationStore.InitializeAsync(CancellationToken.None);
                configurationStore.Publish(new ClientConfigurationSnapshot(
                    new ClientVersionInfo(1, "0.1.0", "0.1.0"),
                    new ClientBootstrapConfiguration(
                        new[] { endpoint },
                        new ClientPublicLimits(4096, 65536))));
                var api = new FakeHttpApi(endpoint);
                var session = new SessionCoordinator(
                    configurationStore,
                    api,
                    new FakeClock(),
                    new FakeClientSecureSessionStore(),
                    FakeClientSecureSessionStore.EnvironmentBinding);
                await session.InitializeAsync(CancellationToken.None);
                var login = await session.LoginAsync("fixture-user", "password", CancellationToken.None);
                var dispatcher = new MainThreadDispatcher(Environment.CurrentManagedThreadId, 32);
                await dispatcher.InitializeAsync(CancellationToken.None);
                var factory = new FakeConnectionFactory(blockGameplayWrites);
                var heartbeatDelay = new FakeGameplayDelay();
                var channel = new ClientGameplayChannel(
                    configurationStore,
                    session,
                    factory,
                    new ClientGameplayCodec(),
                    dispatcher,
                    heartbeatDelay);
                await channel.InitializeAsync(CancellationToken.None);
                return new GameplayFixture(
                    configurationStore,
                    session,
                    dispatcher,
                    factory,
                    heartbeatDelay,
                    channel,
                    login.Value.Generation);
            }

            /// <summary>签发并连接 own-world admission。</summary>
            /// <returns>Gameplay connect 结果。</returns>
            internal async Task<bool> ConnectOwnWorldAsync()
            {
                var admission = await Session.IssueWorldAdmissionAsync(
                    ClientWorldAdmissionTarget.OwnWorld(),
                    "fixture-admission-key-01",
                    CancellationToken.None);
                return await Channel.ConnectAsync(admission.Value, CancellationToken.None);
            }

            /// <summary>签发并连接 Visitor JOIN admission。</summary>
            /// <returns>Gameplay connect 结果。</returns>
            internal async Task<bool> ConnectVisitAsync()
            {
                var admission = await Session.IssueWorldAdmissionAsync(
                    ClientWorldAdmissionTarget.VisitWorld("visit-fixture"),
                    "fixture-visit-admission-key-01",
                    CancellationToken.None);
                return await Channel.ConnectAsync(admission.Value, CancellationToken.None);
            }

            /// <summary>按生产逆序停止测试对象图。</summary>
            /// <returns>全部 owner 停止时完成。</returns>
            internal async Task StopAsync()
            {
                await Channel.StopAsync(CancellationToken.None);
                await Session.StopAsync(CancellationToken.None);
                await ConfigurationStore.StopAsync(CancellationToken.None);
                await Dispatcher.StopAsync(CancellationToken.None);
            }
        }

        /// <summary>提供认证、ticket 与 admission 的确定性 HTTP 替换。</summary>
        private sealed class FakeHttpApi : IClientHttpApi
        {
            /// <summary>保存 ticket/admission 共同绑定 endpoint。</summary>
            private readonly ClientEndpoint _endpoint;

            /// <summary>创建 gameplay HTTP fake。</summary>
            internal FakeHttpApi(ClientEndpoint endpoint)
            {
                _endpoint = endpoint;
            }

            /// <inheritdoc />
            public Task<ClientHttpResult<ClientVersionInfo>> GetVersionAsync(CancellationToken cancellationToken) =>
                throw new NotSupportedException();

            /// <inheritdoc />
            public Task<ClientHttpResult<ClientBootstrapConfiguration>> GetBootstrapConfigurationAsync(CancellationToken cancellationToken) =>
                throw new NotSupportedException();

            /// <inheritdoc />
            public Task<ClientHttpResult<ClientAuthentication>> RegisterAsync(string username, string password, string displayName, CancellationToken cancellationToken) =>
                throw new NotSupportedException();

            /// <inheritdoc />
            public Task<ClientHttpResult<ClientAuthentication>> LoginAsync(string username, string password, CancellationToken cancellationToken)
            {
                return Task.FromResult(ClientHttpResult<ClientAuthentication>.Success(
                    new ClientAuthentication(
                        new ClientAccountSummary("account-fixture", "Fixture", 1),
                        new ClientSessionSummary("session-fixture", 1, 10000),
                        new ClientTokenPair("access-fixture", "refresh-fixture", 9000, 10000),
                        new[] { _endpoint })));
            }

            /// <inheritdoc />
            public Task<ClientHttpResult<ClientTokenPair>> RefreshAsync(string refreshToken, CancellationToken cancellationToken) =>
                throw new NotSupportedException();

            /// <inheritdoc />
            public Task<ClientHttpResult<ClientHttpEmpty>> LogoutAsync(string accessToken, CancellationToken cancellationToken) =>
                throw new NotSupportedException();

            /// <inheritdoc />
            public Task<ClientHttpResult<ClientConnectionTicket>> IssueConnectionTicketAsync(string accessToken, ClientEndpointChannel channel, CancellationToken cancellationToken)
            {
                return Task.FromResult(ClientHttpResult<ClientConnectionTicket>.Success(
                    new ClientConnectionTicket(
                        "0102030405060708090a0b0c0d0e0f10",
                        _endpoint,
                        new[] { ClientConnectionScope.Gameplay },
                        9000)));
            }

            /// <inheritdoc />
            public Task<ClientHttpResult<ClientWorldBootstrap>> GetWorldBootstrapAsync(string accessToken, CancellationToken cancellationToken) =>
                throw new NotSupportedException();

            /// <inheritdoc />
            public Task<ClientHttpResult<ClientVisitReservation>> AcceptVisitInviteAsync(string accessToken, ClientVisitInviteAcceptRequest request, string idempotencyKey, CancellationToken cancellationToken) =>
                throw new NotSupportedException();

            /// <inheritdoc />
            public Task<ClientHttpResult<ClientWorldAdmission>> IssueWorldAdmissionAsync(string accessToken, ClientWorldAdmissionTarget target, string idempotencyKey, CancellationToken cancellationToken)
            {
                var visit = target.Kind == ClientWorldAdmissionTargetKind.VisitWorld;
                return Task.FromResult(ClientHttpResult<ClientWorldAdmission>.Success(
                    new ClientWorldAdmission(
                        "wad1_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
                        _endpoint,
                        visit ? ClientWorldRole.Visitor : ClientWorldRole.Owner,
                        visit ? ClientWorldAdmissionPurpose.Join : ClientWorldAdmissionPurpose.OwnWorld,
                        visit ? 2UL : 0UL,
                        9000)));
            }
        }

        /// <summary>由测试显式释放每个 heartbeat interval，不依赖真实时间。</summary>
        private sealed class FakeGameplayDelay : IClientGameplayDelay
        {
            /// <summary>保护等待队列。</summary>
            private readonly object _sync = new object();

            /// <summary>保存尚未释放的 interval。</summary>
            private readonly Queue<TaskCompletionSource<bool>> _pending =
                new Queue<TaskCompletionSource<bool>>();

            /// <summary>获取当前等待 interval 数量。</summary>
            internal int PendingCount
            {
                get
                {
                    lock (_sync)
                    {
                        return _pending.Count;
                    }
                }
            }

            /// <inheritdoc />
            public async Task DelayAsync(TimeSpan delay, CancellationToken cancellationToken)
            {
                if (delay <= TimeSpan.Zero)
                {
                    throw new ArgumentOutOfRangeException(nameof(delay));
                }

                var completion = new TaskCompletionSource<bool>(
                    TaskCreationOptions.RunContinuationsAsynchronously);
                lock (_sync)
                {
                    _pending.Enqueue(completion);
                }

                using (cancellationToken.Register(() => completion.TrySetCanceled()))
                {
                    await completion.Task;
                }
            }

            /// <summary>释放最早的 heartbeat interval。</summary>
            internal void ReleaseNext()
            {
                TaskCompletionSource<bool> completion;
                lock (_sync)
                {
                    completion = _pending.Dequeue();
                }

                completion.TrySetResult(true);
            }
        }

        /// <summary>提供固定 Unix 毫秒时间的 session lease 时钟。</summary>
        private sealed class FakeClock : IClientClock
        {
            /// <summary>获取未到期测试时间。</summary>
            public long UtcNowMilliseconds => 1000;
        }

        /// <summary>每次测试只创建一个可控 duplex connection。</summary>
        private sealed class FakeConnectionFactory : IClientGameplayConnectionFactory
        {
            /// <summary>决定是否阻塞 preface 后的 writer。</summary>
            private readonly bool _blockGameplayWrites;

            /// <summary>创建 factory。</summary>
            internal FakeConnectionFactory(bool blockGameplayWrites)
            {
                _blockGameplayWrites = blockGameplayWrites;
            }

            /// <summary>获取 connect 调用次数。</summary>
            internal int ConnectCount { get; private set; }

            /// <summary>获取最近创建的 connection。</summary>
            internal FakeConnection Connection { get; private set; }

            /// <inheritdoc />
            public Task<IClientGameplayConnection> ConnectAsync(ClientEndpoint endpoint, CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                ConnectCount++;
                Connection = new FakeConnection(_blockGameplayWrites);
                return Task.FromResult<IClientGameplayConnection>(Connection);
            }
        }

        /// <summary>模拟 partial reader、captured writer 与可取消 blocked writer。</summary>
        private sealed class FakeConnection : IClientGameplayConnection
        {
            /// <summary>保护 read bytes、writes 与 dispose state。</summary>
            private readonly object _sync = new object();

            /// <summary>保存 peer 待返回字节。</summary>
            private readonly Queue<byte> _reads = new Queue<byte>();

            /// <summary>保存客户端完整 writes。</summary>
            private readonly List<byte[]> _writes = new List<byte[]>();

            /// <summary>通知 reader 有新字节或 dispose。</summary>
            private readonly SemaphoreSlim _readSignal = new SemaphoreSlim(0);

            /// <summary>是否阻塞 preface 后的 writes。</summary>
            private readonly bool _blockGameplayWrites;

            /// <summary>保存 dispose 状态。</summary>
            private bool _disposed;

            /// <summary>创建可控 connection。</summary>
            internal FakeConnection(bool blockGameplayWrites)
            {
                _blockGameplayWrites = blockGameplayWrites;
            }

            /// <summary>获取已捕获 write 数量。</summary>
            internal int WriteCount
            {
                get
                {
                    lock (_sync)
                    {
                        return _writes.Count;
                    }
                }
            }

            /// <summary>获取是否已释放。</summary>
            internal bool IsDisposed
            {
                get
                {
                    lock (_sync)
                    {
                        return _disposed;
                    }
                }
            }

            /// <summary>取得指定完整 write 的副本。</summary>
            internal byte[] GetWrite(int index)
            {
                lock (_sync)
                {
                    return (byte[])_writes[index].Clone();
                }
            }

            /// <summary>等待捕获指定数量 writes。</summary>
            internal async Task WaitForWritesAsync(int count)
            {
                await WaitUntilAsync(() => WriteCount >= count);
            }

            /// <summary>向 reader 加入完整 peer frame。</summary>
            internal void Enqueue(byte[] bytes)
            {
                lock (_sync)
                {
                    foreach (var value in bytes)
                    {
                        _reads.Enqueue(value);
                    }
                }

                _readSignal.Release();
            }

            /// <inheritdoc />
            public async Task<int> ReadAsync(byte[] buffer, int offset, int count, CancellationToken cancellationToken)
            {
                while (true)
                {
                    lock (_sync)
                    {
                        if (_reads.Count > 0)
                        {
                            var length = Math.Min(Math.Min(count, 3), _reads.Count);
                            for (var index = 0; index < length; index++)
                            {
                                buffer[offset + index] = _reads.Dequeue();
                            }

                            return length;
                        }

                        if (_disposed)
                        {
                            return 0;
                        }
                    }

                    await _readSignal.WaitAsync(cancellationToken);
                }
            }

            /// <inheritdoc />
            public async Task WriteAsync(byte[] buffer, CancellationToken cancellationToken)
            {
                lock (_sync)
                {
                    _writes.Add((byte[])buffer.Clone());
                    if (!_blockGameplayWrites || _writes.Count == 1)
                    {
                        return;
                    }
                }

                await Task.Delay(Timeout.Infinite, cancellationToken);
            }

            /// <summary>释放 fake 并唤醒 reader。</summary>
            public void Dispose()
            {
                lock (_sync)
                {
                    _disposed = true;
                }

                _readSignal.Release();
            }
        }
    }
}
