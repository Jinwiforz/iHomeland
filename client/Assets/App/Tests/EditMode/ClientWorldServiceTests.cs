using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Application.Session;
using IHomeland.Client.Application.World;
using IHomeland.Client.Infrastructure.Http;
using IHomeland.Protocol.Session.V1;
using IHomeland.Protocol.Visit.V1;
using IHomeland.Protocol.World.V1;
using NUnit.Framework;

namespace IHomeland.Client.Tests.EditMode
{
    /// <summary>
    /// 验证 world/visit Services 的不可变复制、revision gate 与有界 control 收敛。
    /// </summary>
    public sealed class ClientWorldServiceTests
    {
        /// <summary>验证更高 revision 的缺失 assignment 会清除旧 endpoint。</summary>
        [Test]
        public void WorldReplacementClearsAssignmentAndRejectsStaleRevision()
        {
            var service = new PersonalWorldService();
            Assert.That(service.ApplyBootstrap(Bootstrap(4, 7)), Is.EqualTo(ClientProjectionApplyResult.Applied));
            Assert.That(service.Snapshot.PrimaryWorld.Assignment, Is.Not.Null);

            Assert.That(service.ApplyWorldSnapshot(World(5, null)), Is.EqualTo(ClientProjectionApplyResult.Applied));
            Assert.That(service.Snapshot.CurrentWorld.Assignment, Is.Null);
            Assert.That(service.ApplyWorldSnapshot(World(4, Assignment(7))), Is.EqualTo(ClientProjectionApplyResult.Stale));
            Assert.That(service.Snapshot.CurrentWorld.Assignment, Is.Null);
        }

        /// <summary>验证同 revision 等价重放幂等而内容冲突 fail closed。</summary>
        [Test]
        public void SameWorldRevisionIsIdempotentOrConflict()
        {
            var service = new PersonalWorldService();
            var conflicts = 0;
            service.ProjectionConflict += () => conflicts++;

            Assert.That(service.ApplyWorldSnapshot(World(8, Assignment(3))), Is.EqualTo(ClientProjectionApplyResult.Applied));
            Assert.That(service.ApplyWorldSnapshot(World(8, Assignment(3))), Is.EqualTo(ClientProjectionApplyResult.Duplicate));
            var conflicting = World(8, Assignment(3));
            conflicting.World.OwnerPlayerId = "player_other";
            Assert.That(service.ApplyWorldSnapshot(conflicting), Is.EqualTo(ClientProjectionApplyResult.Conflict));
            Assert.That(service.Snapshot.NeedsRefresh, Is.True);
            Assert.That(conflicts, Is.EqualTo(1));
        }

        /// <summary>验证同一 assignment 的 lease 续期不会被误判为 world identity 漂移。</summary>
        [Test]
        public void WorldSnapshotAcceptsMonotonicAssignmentLeaseRenewal()
        {
            var service = new PersonalWorldService();
            Assert.That(
                service.ApplyWorldSnapshot(World(8, Assignment(3, 20_000))),
                Is.EqualTo(ClientProjectionApplyResult.Applied));
            Assert.That(
                service.ApplyWorldSnapshot(World(8, Assignment(3, 30_000))),
                Is.EqualTo(ClientProjectionApplyResult.Applied));
            Assert.That(service.Snapshot.CurrentWorld.Assignment.LeaseExpiresAtMilliseconds, Is.EqualTo(30_000));

            Assert.That(
                service.ApplyWorldSnapshot(World(8, Assignment(3, 25_000))),
                Is.EqualTo(ClientProjectionApplyResult.Stale));
            Assert.That(
                service.ApplyWorldSnapshot(World(8, Assignment(3, 40_000, "instance_other"))),
                Is.EqualTo(ClientProjectionApplyResult.Conflict));
        }

        /// <summary>验证服务端恢复可在 world revision 不变时以更高 assignment generation 替换实例。</summary>
        [Test]
        public void WorldBootstrapAcceptsHigherAssignmentGenerationAtSameWorldRevision()
        {
            var service = new PersonalWorldService();
            Assert.That(
                service.ApplyBootstrap(Bootstrap(1, 2)),
                Is.EqualTo(ClientProjectionApplyResult.Applied));
            service.ClearCurrentTarget();

            var recovered = Bootstrap(1, 3, "instance_recovered");
            Assert.That(
                service.ApplyBootstrap(recovered),
                Is.EqualTo(ClientProjectionApplyResult.Applied));
            Assert.That(service.Snapshot.PrimaryWorld.Revision, Is.EqualTo(1));
            Assert.That(service.Snapshot.PrimaryWorld.Assignment.Generation, Is.EqualTo(3));
            Assert.That(
                service.Snapshot.PrimaryWorld.Assignment.WorldInstanceID,
                Is.EqualTo("instance_recovered"));
        }

        /// <summary>验证 assignment control hint 独立比较 generation 且不替代完整 snapshot。</summary>
        [Test]
        public void AssignmentHintUsesGenerationWithoutReplacingWorld()
        {
            var service = new PersonalWorldService();
            Assert.That(service.ApplyWorldSnapshot(World(6, Assignment(9))), Is.EqualTo(ClientProjectionApplyResult.Applied));

            var stale = new WorldAssignmentChangedPush
            {
                PersonalWorldId = "world_one",
                Assignment = Assignment(8),
            };
            Assert.That(service.ApplyAssignmentHint(stale), Is.EqualTo(ClientProjectionApplyResult.Stale));
            Assert.That(service.Snapshot.CurrentWorld.Assignment.Generation, Is.EqualTo(9));

            var revoked = new WorldAssignmentChangedPush { PersonalWorldId = "world_one" };
            Assert.That(service.ApplyAssignmentHint(revoked), Is.EqualTo(ClientProjectionApplyResult.Applied));
            Assert.That(service.Snapshot.AssignmentHint, Is.Null);
            Assert.That(service.Snapshot.NeedsRefresh, Is.True);
            Assert.That(service.Snapshot.CurrentWorld.Assignment, Is.Not.Null);
        }

        /// <summary>验证 assignment 清除后保留 generation tombstone，同代或更旧实例都不能复活。</summary>
        [Test]
        public void ClearedAssignmentCannotReappearWithoutNewGeneration()
        {
            var service = new PersonalWorldService();
            Assert.That(service.ApplyWorldSnapshot(World(1, Assignment(9))), Is.EqualTo(ClientProjectionApplyResult.Applied));
            Assert.That(service.ApplyWorldSnapshot(World(2, null)), Is.EqualTo(ClientProjectionApplyResult.Applied));
            Assert.That(service.ApplyWorldSnapshot(World(3, Assignment(9))), Is.EqualTo(ClientProjectionApplyResult.Conflict));
            Assert.That(service.Snapshot.CurrentWorld.Revision, Is.EqualTo(2));
            Assert.That(service.Snapshot.CurrentWorld.Assignment, Is.Null);
            Assert.That(service.Snapshot.NeedsRefresh, Is.True);

            Assert.That(service.ApplyWorldSnapshot(World(4, Assignment(8))), Is.EqualTo(ClientProjectionApplyResult.Conflict));
            Assert.That(service.ApplyWorldSnapshot(World(5, Assignment(10))), Is.EqualTo(ClientProjectionApplyResult.Applied));
            Assert.That(service.Snapshot.CurrentWorld.Assignment.Generation, Is.EqualTo(10));
        }

        /// <summary>验证非法 identity 在进入 Service state 前被拒绝。</summary>
        [Test]
        public void MalformedWorldIdentityIsRejected()
        {
            var service = new PersonalWorldService();
            var snapshot = World(1, Assignment(1));
            snapshot.World.PersonalWorldId = "world/escape";

            Assert.That(service.ApplyWorldSnapshot(snapshot), Is.EqualTo(ClientProjectionApplyResult.Rejected));
            Assert.That(service.Snapshot.CurrentWorld, Is.Null);
        }

        /// <summary>验证 VisitSession 的低 revision、幂等与冲突 gate。</summary>
        [Test]
        public void VisitSnapshotUsesIdentityAndRevisionGate()
        {
            var clock = new FakeClock(1_000);
            var service = new VisitSessionService(clock);
            Assert.That(service.SetTargetRole(ClientVisitRole.Visitor, "visit_one"), Is.True);

            Assert.That(service.ApplySnapshot(Visit(4), ClientVisitRole.Visitor), Is.EqualTo(ClientProjectionApplyResult.Applied));
            Assert.That(service.ApplySnapshot(Visit(3), ClientVisitRole.Visitor), Is.EqualTo(ClientProjectionApplyResult.Stale));
            Assert.That(service.ApplySnapshot(Visit(4), ClientVisitRole.Visitor), Is.EqualTo(ClientProjectionApplyResult.Duplicate));

            var conflict = Visit(4);
            conflict.Capacity = 5;
            Assert.That(service.ApplySnapshot(conflict, ClientVisitRole.Visitor), Is.EqualTo(ClientProjectionApplyResult.Conflict));
            Assert.That(service.Snapshot.NeedsRefresh, Is.True);
        }

        /// <summary>验证 VisitSession revision 与 current assignment lease 使用各自的单调门。</summary>
        [Test]
        public void VisitSnapshotAcceptsCurrentAssignmentLeaseRenewal()
        {
            var service = new VisitSessionService(new FakeClock(1_000));
            Assert.That(service.SetTargetRole(ClientVisitRole.Owner, null), Is.True);

            var initial = Visit(4);
            initial.Assignment.LeaseExpiresAtMs = 20_000;
            Assert.That(service.ApplySnapshot(initial, ClientVisitRole.Owner), Is.EqualTo(ClientProjectionApplyResult.Applied));

            var sameRevisionRenewal = Visit(4);
            sameRevisionRenewal.Assignment.LeaseExpiresAtMs = 25_000;
            Assert.That(service.ApplySnapshot(sameRevisionRenewal, ClientVisitRole.Owner), Is.EqualTo(ClientProjectionApplyResult.Applied));

            var mutationResponse = Visit(5);
            mutationResponse.Assignment.LeaseExpiresAtMs = 30_000;
            Assert.That(service.ApplySnapshot(mutationResponse, ClientVisitRole.Owner), Is.EqualTo(ClientProjectionApplyResult.Applied));
            Assert.That(service.Snapshot.Current.Revision, Is.EqualTo(5));
            Assert.That(service.Snapshot.Current.Assignment.LeaseExpiresAtMilliseconds, Is.EqualTo(30_000));

            var regressedLease = Visit(5);
            regressedLease.Assignment.LeaseExpiresAtMs = 25_000;
            Assert.That(service.ApplySnapshot(regressedLease, ClientVisitRole.Owner), Is.EqualTo(ClientProjectionApplyResult.Stale));

            var changedIdentity = Visit(6);
            changedIdentity.Assignment.WorldInstanceId = "instance_other";
            Assert.That(service.ApplySnapshot(changedIdentity, ClientVisitRole.Owner), Is.EqualTo(ClientProjectionApplyResult.Conflict));
        }

        /// <summary>验证 Visitor 集合必须稳定排序、唯一且不包含 Owner。</summary>
        [Test]
        public void VisitVisitorsMustBeCanonical()
        {
            var service = new VisitSessionService(new FakeClock(1_000));
            service.SetTargetRole(ClientVisitRole.Owner, null);
            var snapshot = Visit(1);
            snapshot.Visitors.Add(new VisitVisitorSummary
            {
                PlayerId = "visitor_a",
                State = VisitMembershipState.Joined,
            });
            snapshot.Visitors.Add(new VisitVisitorSummary
            {
                PlayerId = "visitor_a",
                State = VisitMembershipState.Reconnecting,
            });

            Assert.That(service.ApplySnapshot(snapshot, ClientVisitRole.Owner), Is.EqualTo(ClientProjectionApplyResult.Rejected));
            Assert.That(service.Snapshot.Current, Is.Null);
        }

        /// <summary>验证 invite identity 去重、同 revision 冲突和等于即失效。</summary>
        [Test]
        public void InviteInboxIsRevisionGatedAndExpiryBound()
        {
            var clock = new FakeClock(10_000);
            var service = new VisitSessionService(clock);
            var invite = Invite("invite_one", 3, 11_000);

            Assert.That(service.ApplyInvite(invite), Is.EqualTo(ClientProjectionApplyResult.Applied));
            Assert.That(service.ApplyInvite(Invite("invite_one", 3, 11_000)), Is.EqualTo(ClientProjectionApplyResult.Duplicate));
            var conflict = Invite("invite_one", 3, 12_000);
            Assert.That(service.ApplyInvite(conflict), Is.EqualTo(ClientProjectionApplyResult.Conflict));

            clock.UtcNowMilliseconds = 11_000;
            Assert.That(service.Snapshot.Invites, Is.Empty);
            Assert.That(service.ApplyInvite(invite), Is.EqualTo(ClientProjectionApplyResult.Rejected));
        }

        /// <summary>验证 accept 成功证明与更高 revision PUSH 都会退役旧 pending identity。</summary>
        [Test]
        public void InviteInboxRetiresAcceptedAndSupersededIdentity()
        {
            var service = new VisitSessionService(new FakeClock(1_000));
            Assert.That(
                service.ApplyInvite(Invite("invite_old", 3, 20_000)),
                Is.EqualTo(ClientProjectionApplyResult.Applied));
            Assert.That(
                service.RetireAcceptedInvite("visit_one", "invite_old"),
                Is.True);
            Assert.That(service.Snapshot.Invites, Is.Empty);

            Assert.That(
                service.ApplyInvite(Invite("invite_replaced", 5, 20_000)),
                Is.EqualTo(ClientProjectionApplyResult.Applied));
            Assert.That(
                service.ApplyInvite(Invite("invite_current", 7, 20_000)),
                Is.EqualTo(ClientProjectionApplyResult.Applied));

            Assert.That(service.Snapshot.Invites, Has.Count.EqualTo(1));
            Assert.That(service.Snapshot.Invites[0].InviteID, Is.EqualTo("invite_current"));
            Assert.That(
                service.RetireAcceptedInvite("visit_one", "invite_replaced"),
                Is.False);
        }

        /// <summary>验证 RETIRED PUSH 即使携带已过期时间也只删除完整匹配的 invite identity。</summary>
        [Test]
        public void InviteInboxAppliesAuthoritativeRetirementByExactIdentity()
        {
            var service = new VisitSessionService(new FakeClock(1_000));
            Assert.That(
                service.ApplyInvite(Invite("invite_retired", 3, 20_000)),
                Is.EqualTo(ClientProjectionApplyResult.Applied));
            Assert.That(
                service.ApplyInvite(Invite("invite_current", 4, 20_000, "player_other")),
                Is.EqualTo(ClientProjectionApplyResult.Applied));

            var retired = Invite("invite_retired", 3, 500);
            retired.Invite.State = VisitInviteState.Retired;
            Assert.That(
                service.ApplyInvite(retired),
                Is.EqualTo(ClientProjectionApplyResult.Applied));

            Assert.That(service.Snapshot.Invites, Has.Count.EqualTo(1));
            Assert.That(service.Snapshot.Invites[0].InviteID, Is.EqualTo("invite_current"));
        }

        /// <summary>验证解除 Visitor target 会同步清除旧完整投影、hint 与刷新标记。</summary>
        [Test]
        public void ClearTargetRoleRemovesVisitProjection()
        {
            var service = new VisitSessionService(new FakeClock(1_000));
            Assert.That(service.SetTargetRole(ClientVisitRole.Visitor, "visit_one"), Is.True);
            Assert.That(service.ApplySnapshot(Visit(4), ClientVisitRole.Visitor), Is.EqualTo(ClientProjectionApplyResult.Applied));
            Assert.That(service.ApplyOwnerAvailability(new VisitOwnerAvailabilityPush
            {
                VisitSessionId = "visit_one",
                Available = false,
                GraceExpiresAtMs = 15_000,
                Revision = 5,
            }), Is.EqualTo(ClientProjectionApplyResult.Applied));

            ClientVisitSessionServiceSnapshot changed = null;
            service.Changed += snapshot => changed = snapshot;
            service.ClearTargetRole();

            Assert.That(changed, Is.Not.Null);
            Assert.That(changed.Current, Is.Null);
            Assert.That(changed.ControlHint, Is.Null);
            Assert.That(changed.NeedsRefresh, Is.False);
            Assert.That(service.ApplySnapshot(Visit(6), ClientVisitRole.Visitor), Is.EqualTo(ClientProjectionApplyResult.Rejected));

            Assert.That(service.SetTargetRole(ClientVisitRole.Visitor, "visit_one"), Is.True);
            Assert.That(service.ApplySnapshot(Visit(3), ClientVisitRole.Visitor), Is.EqualTo(ClientProjectionApplyResult.Stale));
            Assert.That(service.ApplySnapshot(Visit(4), ClientVisitRole.Visitor), Is.EqualTo(ClientProjectionApplyResult.Duplicate));
            Assert.That(service.Snapshot.Current.Revision, Is.EqualTo(4));
        }

        /// <summary>验证 command 与 role binding 共用公开 identity grammar。</summary>
        [Test]
        public void TargetRoleRejectsMalformedIdentity()
        {
            var service = new VisitSessionService(new FakeClock(1_000));

            Assert.That(service.SetTargetRole(ClientVisitRole.Visitor, "visit/escape"), Is.False);
            Assert.That(ClientWorldProjectionMapper.IsValidIdentity(new string('a', 129)), Is.False);
            Assert.That(ClientWorldProjectionMapper.IsValidIdentity("visit_one"), Is.True);
        }

        /// <summary>验证未过期 invite 达到硬上限后返回 overflow 而不增长集合。</summary>
        [Test]
        public void InviteInboxHasHardCapacity()
        {
            var service = new VisitSessionService(new FakeClock(1_000));
            for (var index = 0; index < VisitSessionService.InviteCapacity; index++)
            {
                Assert.That(
                    service.ApplyInvite(Invite(
                        "invite_" + index,
                        (ulong)(index + 1),
                        20_000,
                        "player_visitor_" + index)),
                    Is.EqualTo(ClientProjectionApplyResult.Applied));
            }

            Assert.That(
                service.ApplyInvite(Invite("invite_overflow", 999, 20_000, "player_overflow")),
                Is.EqualTo(ClientProjectionApplyResult.Overflow));
            Assert.That(service.Snapshot.Invites.Count, Is.EqualTo(VisitSessionService.InviteCapacity));
        }

        /// <summary>验证 WSS availability 只设置 hint/refresh，不修改 gameplay lifecycle。</summary>
        [Test]
        public void AvailabilityHintDoesNotReplaceVisitSnapshot()
        {
            var service = new VisitSessionService(new FakeClock(1_000));
            service.SetTargetRole(ClientVisitRole.Visitor, "visit_one");
            service.ApplySnapshot(Visit(4), ClientVisitRole.Visitor);

            Assert.That(service.ApplyOwnerAvailability(new VisitOwnerAvailabilityPush
            {
                VisitSessionId = "visit_one",
                Available = false,
                GraceExpiresAtMs = 15_000,
                Revision = 5,
            }), Is.EqualTo(ClientProjectionApplyResult.Applied));

            Assert.That(service.Snapshot.Current.Lifecycle, Is.EqualTo(ClientVisitLifecycle.Open));
            Assert.That(service.Snapshot.ControlHint.OwnerAvailable, Is.False);
            Assert.That(service.Snapshot.NeedsRefresh, Is.True);
        }

        /// <summary>验证初始化与停止只登记/解除 subscriber，且停止后拒绝迟到输入。</summary>
        [Test]
        public async Task ServicesStopRejectsLateProjection()
        {
            var world = new PersonalWorldService();
            var visit = new VisitSessionService(new FakeClock(1_000));
            await world.InitializeAsync(CancellationToken.None);
            await visit.InitializeAsync(CancellationToken.None);
            await visit.StopAsync(CancellationToken.None);
            await world.StopAsync(CancellationToken.None);

            Assert.That(world.ApplyWorldSnapshot(World(1, null)), Is.EqualTo(ClientProjectionApplyResult.Rejected));
            Assert.That(visit.ApplyInvite(Invite("late", 1, 2_000)), Is.EqualTo(ClientProjectionApplyResult.Rejected));
        }

        /// <summary>验证 subscriber 异常发生在提交后，且不妨碍随后受控停止。</summary>
        [Test]
        public async Task SubscriberFailureDoesNotRollbackProjectionOrPreventStop()
        {
            var service = new PersonalWorldService();
            await service.InitializeAsync(CancellationToken.None);
            service.Changed += _ => throw new InvalidOperationException("fixture subscriber failure");

            Assert.Throws<InvalidOperationException>(() => service.ApplyWorldSnapshot(World(2, null)));
            Assert.That(service.Snapshot.CurrentWorld.Revision, Is.EqualTo(2));
            Assert.DoesNotThrowAsync(async () => await service.StopAsync(CancellationToken.None));
        }

        /// <summary>创建 HTTP own-world bootstrap fixture。</summary>
        /// <param name="revision">World revision。</param>
        /// <param name="generation">Assignment generation。</param>
        /// <param name="worldInstanceID">WorldInstance identity。</param>
        /// <returns>完整 HTTP fixture。</returns>
        private static ClientWorldBootstrap Bootstrap(
            long revision,
            long generation,
            string worldInstanceID = "instance_one")
        {
            return new ClientWorldBootstrap(
                new ClientPersonalWorldSummary(
                    "world_one",
                    "player_owner",
                    ClientPersonalWorldLifecycle.Active,
                    revision,
                    1_000),
                new ClientWorldAssignment(
                    "world_one",
                    worldInstanceID,
                    new ClientEndpoint(ClientEndpointChannel.TlsTcp, "world.example.test", 9443),
                    generation,
                    20_000));
        }

        /// <summary>创建 generated world fixture。</summary>
        /// <param name="revision">World revision。</param>
        /// <param name="assignment">可选 assignment。</param>
        /// <returns>完整 world snapshot。</returns>
        private static WorldSnapshot World(ulong revision, WorldAssignment assignment)
        {
            return new WorldSnapshot
            {
                World = new PersonalWorldSnapshot
                {
                    PersonalWorldId = "world_one",
                    OwnerPlayerId = "player_owner",
                    Lifecycle = PersonalWorldLifecycle.Active,
                    Revision = revision,
                    CreatedAtMs = 1_000,
                },
                Assignment = assignment,
            };
        }

        /// <summary>创建 generated TLS/TCP assignment fixture。</summary>
        /// <param name="generation">Assignment generation。</param>
        /// <param name="leaseExpiresAtMilliseconds">Lease Unix expiry，单位为毫秒。</param>
        /// <param name="worldInstanceID">WorldInstance identity。</param>
        /// <returns>完整 assignment。</returns>
        private static WorldAssignment Assignment(
            ulong generation,
            long leaseExpiresAtMilliseconds = 20_000,
            string worldInstanceID = "instance_one")
        {
            return new WorldAssignment
            {
                PersonalWorldId = "world_one",
                WorldInstanceId = worldInstanceID,
                Endpoint = new Endpoint
                {
                    Channel = TransportChannel.TlsTcp,
                    Host = "world.example.test",
                    Port = 9443,
                },
                Generation = generation,
                LeaseExpiresAtMs = leaseExpiresAtMilliseconds,
            };
        }

        /// <summary>创建 generated VisitSession fixture。</summary>
        /// <param name="revision">Aggregate revision。</param>
        /// <returns>完整 VisitSession snapshot。</returns>
        private static VisitSessionSnapshot Visit(ulong revision)
        {
            return new VisitSessionSnapshot
            {
                VisitSessionId = "visit_one",
                OwnerPlayerId = "player_owner",
                Assignment = Assignment(2),
                Lifecycle = VisitLifecycle.Open,
                Revision = revision,
                Capacity = 4,
                CreatedAtMs = 1_000,
                ExpiresAtMs = 20_000,
            };
        }

        /// <summary>创建定向 invite PUSH fixture。</summary>
        /// <param name="inviteID">Invite identity。</param>
        /// <param name="revision">Created revision。</param>
        /// <param name="expiry">Unix expiry，单位为毫秒。</param>
        /// <param name="targetVisitorID">目标 Visitor；默认使用稳定 fixture identity。</param>
        /// <returns>完整 invite PUSH。</returns>
        private static VisitInvitePush Invite(
            string inviteID,
            ulong revision,
            long expiry,
            string targetVisitorID = "player_visitor")
        {
            return new VisitInvitePush
            {
                OwnerPlayerId = "player_owner",
                Invite = new VisitInviteSummary
                {
                    InviteId = inviteID,
                    VisitSessionId = "visit_one",
                    TargetVisitorId = targetVisitorID,
                    State = VisitInviteState.Pending,
                    CreatedRevision = revision,
                    ExpiresAtMs = expiry,
                },
            };
        }

        /// <summary>提供测试可控的 Unix millisecond 时钟。</summary>
        private sealed class FakeClock : IClientClock
        {
            /// <summary>创建指定当前时间的时钟。</summary>
            /// <param name="utcNowMilliseconds">Unix 时间，单位为毫秒。</param>
            internal FakeClock(long utcNowMilliseconds)
            {
                UtcNowMilliseconds = utcNowMilliseconds;
            }

            /// <summary>获取或设置当前 Unix 时间，单位为毫秒。</summary>
            public long UtcNowMilliseconds { get; set; }
        }
    }
}
