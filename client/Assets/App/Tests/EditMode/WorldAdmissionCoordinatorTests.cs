using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using Google.Protobuf;
using IHomeland.Client.Application.Gameplay;
using IHomeland.Client.Application.Session;
using IHomeland.Client.Application.World;
using IHomeland.Client.Core.Configuration;
using IHomeland.Client.Core.Lifetime;
using IHomeland.Client.Infrastructure.Http;
using IHomeland.Client.Infrastructure.Tcp;
using IHomeland.Protocol.Common.V1;
using IHomeland.Protocol.Session.V1;
using IHomeland.Protocol.Visit.V1;
using IHomeland.Protocol.World.V1;
using NUnit.Framework;

namespace IHomeland.Client.Tests.EditMode
{
    /// <summary>
    /// 使用确定性 HTTP 与内存 duplex stream 验证 world target coordinator 的关键竞态。
    /// </summary>
    public sealed class WorldAdmissionCoordinatorTests
    {
        /// <summary>验证 bootstrap 未完成时第二个 enter intent 被有界拒绝。</summary>
        [Test]
        public async Task ConcurrentEnterOwnWorldKeepsSingleIntent()
        {
            var fixture = await CoordinatorFixture.CreateAsync(blockBootstrap: true);
            var first = fixture.Coordinator.EnterOwnWorldAsync(CancellationToken.None);
            await fixture.Api.WaitForBootstrapCallAsync();

            Assert.That(
                await fixture.Coordinator.EnterOwnWorldAsync(CancellationToken.None),
                Is.False);
            fixture.Api.CompleteBootstrap();
            Assert.That(await first, Is.True);
            Assert.That(fixture.Api.BootstrapCalls, Is.EqualTo(1));
            Assert.That(fixture.Coordinator.Snapshot.State, Is.EqualTo(ClientWorldFlowState.OwnWorld));
            await fixture.StopAsync();
        }

        /// <summary>验证返回 own-world 失败后保持 ReturningOwnWorld，显式 retry 才可恢复。</summary>
        [Test]
        public async Task ReturnFailureCannotReviveVisitorTarget()
        {
            var fixture = await CoordinatorFixture.CreateAsync(blockBootstrap: false);
            Assert.That(await fixture.Coordinator.EnterOwnWorldAsync(CancellationToken.None), Is.True);
            Assert.That(
                fixture.VisitService.ApplyInvite(CoordinatorFixture.Invite()),
                Is.EqualTo(ClientProjectionApplyResult.Applied));
            var joined = await fixture.Coordinator.JoinVisitAsync(
                "visit_one",
                "invite_one",
                CancellationToken.None);
            Assert.That(
                joined,
                Is.True,
                $"state={fixture.Coordinator.Snapshot.State} failure={fixture.Coordinator.Snapshot.Failure}");
            Assert.That(fixture.Coordinator.Snapshot.State, Is.EqualTo(ClientWorldFlowState.Visiting));

            fixture.Api.FailBootstrap = true;
            Assert.That(await fixture.Coordinator.LeaveVisitAsync(CancellationToken.None), Is.False);
            Assert.That(
                fixture.Coordinator.Snapshot.State,
                Is.EqualTo(ClientWorldFlowState.ReturningOwnWorld));
            Assert.That(fixture.Coordinator.Snapshot.VisitSessionID, Is.Null);
            Assert.That(fixture.Gameplay.Snapshot.State, Is.EqualTo(ClientGameplayChannelState.Ready));

            fixture.Api.FailBootstrap = false;
            Assert.That(await fixture.Coordinator.RetryReturnAsync(CancellationToken.None), Is.True);
            Assert.That(fixture.Coordinator.Snapshot.State, Is.EqualTo(ClientWorldFlowState.OwnWorld));
            Assert.That(fixture.Factory.ConnectionCount, Is.EqualTo(3));
            await fixture.StopAsync();
        }

        /// <summary>验证停止使 current generation 失效并拒绝后续 flow。</summary>
        [Test]
        public async Task ShutdownRejectsLateAndNewFlow()
        {
            var fixture = await CoordinatorFixture.CreateAsync(blockBootstrap: true);
            var pending = fixture.Coordinator.EnterOwnWorldAsync(CancellationToken.None);
            await fixture.Api.WaitForBootstrapCallAsync();
            await fixture.Coordinator.StopAsync(CancellationToken.None);
            fixture.Api.CompleteBootstrap();

            Assert.That(await pending, Is.False);
            Assert.That(fixture.Coordinator.Snapshot.State, Is.EqualTo(ClientWorldFlowState.Stopped));
            Assert.That(await fixture.Coordinator.EnterOwnWorldAsync(CancellationToken.None), Is.False);
            await fixture.StopRemainingAsync();
        }

        /// <summary>组合不依赖真实 listener、Unity Services 或 Scene 的完整 coordinator 图。</summary>
        private sealed class CoordinatorFixture
        {
            /// <summary>保存统一 endpoint fixture。</summary>
            private static readonly ClientEndpoint Endpoint =
                new ClientEndpoint(ClientEndpointChannel.TlsTcp, "127.0.0.1", 4433);

            /// <summary>记录 coordinator 是否已单独停止。</summary>
            private bool _coordinatorStopped;

            /// <summary>创建完整 fixture。</summary>
            private CoordinatorFixture(
                ClientConfigurationStore configuration,
                SessionCoordinator session,
                MainThreadDispatcher dispatcher,
                ClientGameplayChannel gameplay,
                PersonalWorldService worldService,
                VisitSessionService visitService,
                WorldAdmissionCoordinator coordinator,
                ScriptedHttpApi api,
                ScriptedConnectionFactory factory)
            {
                Configuration = configuration;
                Session = session;
                Dispatcher = dispatcher;
                Gameplay = gameplay;
                WorldService = worldService;
                VisitService = visitService;
                Coordinator = coordinator;
                Api = api;
                Factory = factory;
            }

            /// <summary>获取配置 owner。</summary>
            internal ClientConfigurationStore Configuration { get; }

            /// <summary>获取 Session owner。</summary>
            internal SessionCoordinator Session { get; }

            /// <summary>获取主线程 dispatcher。</summary>
            internal MainThreadDispatcher Dispatcher { get; }

            /// <summary>获取 gameplay owner。</summary>
            internal ClientGameplayChannel Gameplay { get; }

            /// <summary>获取 PersonalWorld Service。</summary>
            internal PersonalWorldService WorldService { get; }

            /// <summary>获取 VisitSession Service。</summary>
            internal VisitSessionService VisitService { get; }

            /// <summary>获取被测 coordinator。</summary>
            internal WorldAdmissionCoordinator Coordinator { get; }

            /// <summary>获取可控 HTTP fake。</summary>
            internal ScriptedHttpApi Api { get; }

            /// <summary>获取可控 connection factory。</summary>
            internal ScriptedConnectionFactory Factory { get; }

            /// <summary>按 production 初始化顺序创建并认证完整对象图。</summary>
            /// <param name="blockBootstrap">是否阻塞第一次 world bootstrap。</param>
            /// <returns>已初始化且 authenticated fixture。</returns>
            internal static async Task<CoordinatorFixture> CreateAsync(bool blockBootstrap)
            {
                var configuration = new ClientConfigurationStore();
                await configuration.InitializeAsync(CancellationToken.None);
                configuration.Publish(new ClientConfigurationSnapshot(
                    new ClientVersionInfo(1, "0.1.0", "0.1.0"),
                    new ClientBootstrapConfiguration(
                        new[] { Endpoint },
                        new ClientPublicLimits(4096, 65536))));
                var api = new ScriptedHttpApi(Endpoint, blockBootstrap);
                var clock = new FixedClock();
                var session = new SessionCoordinator(configuration, api, clock);
                await session.InitializeAsync(CancellationToken.None);
                Assert.That(
                    (await session.LoginAsync("fixture-user", "password", CancellationToken.None)).IsSuccess,
                    Is.True);
                var dispatcher = new MainThreadDispatcher(Environment.CurrentManagedThreadId, 64);
                await dispatcher.InitializeAsync(CancellationToken.None);
                var factory = new ScriptedConnectionFactory();
                var gameplay = new ClientGameplayChannel(
                    configuration,
                    session,
                    factory,
                    new ClientGameplayCodec(),
                    dispatcher);
                await gameplay.InitializeAsync(CancellationToken.None);
                var world = new PersonalWorldService(null, gameplay);
                var visit = new VisitSessionService(null, gameplay, clock);
                var coordinator = new WorldAdmissionCoordinator(session, gameplay, world, visit);
                await world.InitializeAsync(CancellationToken.None);
                await visit.InitializeAsync(CancellationToken.None);
                await coordinator.InitializeAsync(CancellationToken.None);
                return new CoordinatorFixture(
                    configuration,
                    session,
                    dispatcher,
                    gameplay,
                    world,
                    visit,
                    coordinator,
                    api,
                    factory);
            }

            /// <summary>创建 current target Visitor 的定向 invite。</summary>
            /// <returns>有效 invite PUSH。</returns>
            internal static VisitInvitePush Invite()
            {
                return new VisitInvitePush
                {
                    OwnerPlayerId = "player_owner",
                    Invite = new VisitInviteSummary
                    {
                        InviteId = "invite_one",
                        VisitSessionId = "visit_one",
                        TargetVisitorId = "player_visitor",
                        State = VisitInviteState.Pending,
                        CreatedRevision = 1,
                        ExpiresAtMs = 90_000,
                    },
                };
            }

            /// <summary>按 production 逆序停止完整 fixture。</summary>
            /// <returns>全部 owner 已停止时完成。</returns>
            internal async Task StopAsync()
            {
                await Coordinator.StopAsync(CancellationToken.None);
                _coordinatorStopped = true;
                await StopRemainingAsync();
            }

            /// <summary>停止 coordinator 之外的其余 owner。</summary>
            /// <returns>全部剩余 owner 已停止时完成。</returns>
            internal async Task StopRemainingAsync()
            {
                if (!_coordinatorStopped && Coordinator.Snapshot.State != ClientWorldFlowState.Stopped)
                {
                    await Coordinator.StopAsync(CancellationToken.None);
                }

                _coordinatorStopped = true;
                await VisitService.StopAsync(CancellationToken.None);
                await WorldService.StopAsync(CancellationToken.None);
                await Gameplay.StopAsync(CancellationToken.None);
                await Session.StopAsync(CancellationToken.None);
                await Configuration.StopAsync(CancellationToken.None);
                await Dispatcher.StopAsync(CancellationToken.None);
            }

            /// <summary>提供 bootstrap/accept/admission/ticket 的确定性 HTTP fake。</summary>
            internal sealed class ScriptedHttpApi : IClientHttpApi
            {
                /// <summary>保存统一 TLS/TCP endpoint。</summary>
                private readonly ClientEndpoint _endpoint;

                /// <summary>通知测试第一次 bootstrap 已进入 fake。</summary>
                private readonly TaskCompletionSource<bool> _bootstrapCalled =
                    new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);

                /// <summary>可选阻塞 bootstrap completion。</summary>
                private readonly TaskCompletionSource<ClientHttpResult<ClientWorldBootstrap>> _bootstrapGate;

                /// <summary>创建 scripted HTTP fake。</summary>
                internal ScriptedHttpApi(ClientEndpoint endpoint, bool blockBootstrap)
                {
                    _endpoint = endpoint;
                    if (blockBootstrap)
                    {
                        _bootstrapGate = new TaskCompletionSource<ClientHttpResult<ClientWorldBootstrap>>(
                            TaskCreationOptions.RunContinuationsAsynchronously);
                    }
                }

                /// <summary>获取 bootstrap 调用次数。</summary>
                internal int BootstrapCalls { get; private set; }

                /// <summary>控制后续 bootstrap 返回 transport failure。</summary>
                internal bool FailBootstrap { get; set; }

                /// <summary>等待第一次 bootstrap 调用到达。</summary>
                /// <returns>调用已到达时完成。</returns>
                internal Task WaitForBootstrapCallAsync() => _bootstrapCalled.Task;

                /// <summary>释放被阻塞的 bootstrap 成功结果。</summary>
                internal void CompleteBootstrap()
                {
                    _bootstrapGate?.TrySetResult(ClientHttpResult<ClientWorldBootstrap>.Success(Bootstrap()));
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
                            new ClientAccountSummary("account_fixture", "Fixture", 1),
                            new ClientSessionSummary("session_fixture", 1, 100_000),
                            new ClientTokenPair("access_fixture", "refresh_fixture", 90_000, 100_000),
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
                            90_000)));
                }

                /// <inheritdoc />
                public Task<ClientHttpResult<ClientWorldBootstrap>> GetWorldBootstrapAsync(string accessToken, CancellationToken cancellationToken)
                {
                    BootstrapCalls++;
                    _bootstrapCalled.TrySetResult(true);
                    if (_bootstrapGate != null && BootstrapCalls == 1)
                    {
                        return _bootstrapGate.Task;
                    }

                    if (FailBootstrap)
                    {
                        return Task.FromResult(ClientHttpResult<ClientWorldBootstrap>.Failed(
                            new ClientHttpFailure(ClientHttpFailureKind.Transport, "getWorldBootstrap")));
                    }

                    return Task.FromResult(ClientHttpResult<ClientWorldBootstrap>.Success(Bootstrap()));
                }

                /// <inheritdoc />
                public Task<ClientHttpResult<ClientVisitReservation>> AcceptVisitInviteAsync(string accessToken, ClientVisitInviteAcceptRequest request, string idempotencyKey, CancellationToken cancellationToken)
                {
                    return Task.FromResult(ClientHttpResult<ClientVisitReservation>.Success(
                        new ClientVisitReservation(request.VisitSessionID, 2, 80_000)));
                }

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
                            90_000)));
                }

                /// <summary>创建 current actor 的 HTTP bootstrap。</summary>
                /// <returns>带 active assignment 的 bootstrap。</returns>
                private static ClientWorldBootstrap Bootstrap()
                {
                    return new ClientWorldBootstrap(
                        new ClientPersonalWorldSummary(
                            "world_self",
                            "player_visitor",
                            ClientPersonalWorldLifecycle.Active,
                            1,
                            1_000),
                        new ClientWorldAssignment(
                            "world_self",
                            "instance_self",
                            Endpoint,
                            1,
                            90_000));
                }
            }

            /// <summary>按连接序号创建 own/visit/return scripted duplex stream。</summary>
            internal sealed class ScriptedConnectionFactory : IClientGameplayConnectionFactory
            {
                /// <summary>获取已创建 connection 数量。</summary>
                internal int ConnectionCount { get; private set; }

                /// <inheritdoc />
                public Task<IClientGameplayConnection> ConnectAsync(ClientEndpoint endpoint, CancellationToken cancellationToken)
                {
                    cancellationToken.ThrowIfCancellationRequested();
                    ConnectionCount++;
                    return Task.FromResult<IClientGameplayConnection>(new ScriptedConnection(ConnectionCount));
                }
            }

            /// <summary>把客户端 request/command 同步映射为确定性 response 的内存 stream。</summary>
            private sealed class ScriptedConnection : IClientGameplayConnection
            {
                /// <summary>保护读取队列与 dispose。</summary>
                private readonly object _sync = new object();

                /// <summary>保存 peer 待返回字节。</summary>
                private readonly Queue<byte> _reads = new Queue<byte>();

                /// <summary>通知 reader 有数据或连接已关闭。</summary>
                private readonly SemaphoreSlim _signal = new SemaphoreSlim(0);

                /// <summary>标识 own 初次、visit 或 own return connection。</summary>
                private readonly int _connectionIndex;

                /// <summary>保存当前 connection 的 S2C sequence。</summary>
                private ulong _sequence;

                /// <summary>区分首个 IHTP preface 与后续 framed envelope。</summary>
                private bool _prefaceWritten;

                /// <summary>保存 dispose 状态。</summary>
                private bool _disposed;

                /// <summary>创建指定用途的 scripted connection。</summary>
                internal ScriptedConnection(int connectionIndex)
                {
                    _connectionIndex = connectionIndex;
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
                                var copied = Math.Min(count, _reads.Count);
                                for (var index = 0; index < copied; index++)
                                {
                                    buffer[offset + index] = _reads.Dequeue();
                                }

                                return copied;
                            }

                            if (_disposed)
                            {
                                return 0;
                            }
                        }

                        await _signal.WaitAsync(cancellationToken);
                    }
                }

                /// <inheritdoc />
                public Task WriteAsync(byte[] buffer, CancellationToken cancellationToken)
                {
                    cancellationToken.ThrowIfCancellationRequested();
                    if (!_prefaceWritten)
                    {
                        _prefaceWritten = true;
                        return Task.CompletedTask;
                    }

                    var envelope = ParseFrame(buffer);
                    IMessage response;
                    uint responseID;
                    switch (envelope.MessageId)
                    {
                        case 2000:
                            responseID = 2001;
                            response = new WorldSnapshotResponse
                            {
                                Snapshot = _connectionIndex == 2
                                    ? World("world_owner", "player_owner", "instance_owner")
                                    : World("world_self", "player_visitor", "instance_self"),
                            };
                            break;
                        case 2109:
                            responseID = 2110;
                            response = new VisitJoinResponse
                            {
                                Result = new VisitMutationResult { Snapshot = Visit(3, VisitLifecycle.Open) },
                            };
                            break;
                        case 2111:
                            responseID = 2112;
                            response = new VisitLeaveResponse
                            {
                                Result = new VisitMutationResult { Snapshot = Visit(4, VisitLifecycle.Closed) },
                            };
                            break;
                        default:
                            throw new InvalidOperationException("Fixture 收到未登记 gameplay operation。");
                    }

                    var outgoing = new ReliableEnvelope
                    {
                        ProtocolVersion = 1,
                        MessageId = responseID,
                        Kind = MessageKind.Response,
                        Sequence = ++_sequence,
                        TimestampMs = 1,
                        Payload = response.ToByteString(),
                    };
                    if (envelope.Kind == MessageKind.Command)
                    {
                        outgoing.CommandId = envelope.CommandId;
                    }
                    else
                    {
                        outgoing.RequestId = envelope.RequestId;
                    }

                    Enqueue(ClientGameplayFramer.Frame(outgoing.ToByteArray()));
                    return Task.CompletedTask;
                }

                /// <inheritdoc />
                public void Dispose()
                {
                    lock (_sync)
                    {
                        _disposed = true;
                    }

                    _signal.Release();
                }

                /// <summary>解析客户端完整 framed envelope。</summary>
                /// <param name="frame">4-byte prefix 与 envelope body。</param>
                /// <returns>Generated reliable envelope。</returns>
                private static ReliableEnvelope ParseFrame(byte[] frame)
                {
                    var body = new byte[frame.Length - 4];
                    Buffer.BlockCopy(frame, 4, body, 0, body.Length);
                    return ReliableEnvelope.Parser.ParseFrom(body);
                }

                /// <summary>把完整 peer frame 加入读取队列。</summary>
                /// <param name="frame">Framed response。</param>
                private void Enqueue(byte[] frame)
                {
                    lock (_sync)
                    {
                        foreach (var value in frame)
                        {
                            _reads.Enqueue(value);
                        }
                    }

                    _signal.Release();
                }

                /// <summary>创建 world/assignment 完整 replacement。</summary>
                /// <param name="worldID">PersonalWorldID。</param>
                /// <param name="ownerID">Owner PlayerID。</param>
                /// <param name="instanceID">WorldInstanceID。</param>
                /// <returns>Generated world snapshot。</returns>
                private static WorldSnapshot World(string worldID, string ownerID, string instanceID)
                {
                    return new WorldSnapshot
                    {
                        World = new PersonalWorldSnapshot
                        {
                            PersonalWorldId = worldID,
                            OwnerPlayerId = ownerID,
                            Lifecycle = PersonalWorldLifecycle.Active,
                            Revision = 1,
                            CreatedAtMs = 1_000,
                        },
                        Assignment = Assignment(worldID, instanceID),
                    };
                }

                /// <summary>创建 VisitSession 完整 replacement。</summary>
                /// <param name="revision">Aggregate revision。</param>
                /// <param name="lifecycle">Visit lifecycle。</param>
                /// <returns>Generated VisitSession snapshot。</returns>
                private static VisitSessionSnapshot Visit(ulong revision, VisitLifecycle lifecycle)
                {
                    return new VisitSessionSnapshot
                    {
                        VisitSessionId = "visit_one",
                        OwnerPlayerId = "player_owner",
                        Assignment = Assignment("world_owner", "instance_owner"),
                        Lifecycle = lifecycle,
                        Revision = revision,
                        Capacity = 4,
                        CreatedAtMs = 1_000,
                        ExpiresAtMs = 90_000,
                    };
                }

                /// <summary>创建统一 TLS/TCP assignment。</summary>
                /// <param name="worldID">PersonalWorldID。</param>
                /// <param name="instanceID">WorldInstanceID。</param>
                /// <returns>Generated assignment。</returns>
                private static WorldAssignment Assignment(string worldID, string instanceID)
                {
                    return new WorldAssignment
                    {
                        PersonalWorldId = worldID,
                        WorldInstanceId = instanceID,
                        Endpoint = new Endpoint
                        {
                            Channel = TransportChannel.TlsTcp,
                            Host = "127.0.0.1",
                            Port = 4433,
                        },
                        Generation = 1,
                        LeaseExpiresAtMs = 90_000,
                    };
                }
            }

            /// <summary>提供未过期固定 Unix 时间。</summary>
            private sealed class FixedClock : IClientClock
            {
                /// <summary>获取固定 Unix 时间，单位为毫秒。</summary>
                public long UtcNowMilliseconds => 1_000;
            }
        }
    }
}
