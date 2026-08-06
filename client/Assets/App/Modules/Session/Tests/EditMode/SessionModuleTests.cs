using System;
using IHomeland.Client.Networking.Application.Contracts;
using IHomeland.Client.Session.Application;
using NUnit.Framework;

namespace IHomeland.Client.Session.Tests.EditMode
{
    /// <summary>验证拆分后的纯 Session state/credential 组件。</summary>
    internal sealed class SessionModuleTests
    {
        /// <summary>旧 generation 与不递增 epoch 都不能取得 invalidation commit。</summary>
        [Test]
        public void StateMachineRejectsStaleGenerationAndEpoch()
        {
            var machine = new SessionStateMachine();
            var snapshot = Snapshot(generation: 7, epoch: 11);

            Assert.That(
                machine.CanAcceptInvalidation(
                    ClientSessionOwnerState.Authenticated,
                    snapshot,
                    sourceGeneration: 6,
                    invalidatedEpoch: 12),
                Is.False);
            Assert.That(
                machine.CanAcceptInvalidation(
                    ClientSessionOwnerState.Authenticated,
                    snapshot,
                    sourceGeneration: 7,
                    invalidatedEpoch: 11),
                Is.False);
            Assert.That(
                machine.CanAcceptInvalidation(
                    ClientSessionOwnerState.Authenticated,
                    snapshot,
                    sourceGeneration: 7,
                    invalidatedEpoch: 12),
                Is.True);
        }

        /// <summary>Ticket lease 只允许匹配 generation 在 expiry 前取得一次。</summary>
        [Test]
        public void CredentialRegistryEnforcesGenerationExpiryAndSingleTake()
        {
            var registry = new SessionCredentialRegistry();
            var lease = registry.CreateConnectionTicket(
                new ClientConnectionTicket(
                    "opaque-ticket",
                    new ClientEndpoint(
                        ClientEndpointChannel.Wss,
                        "control.test",
                        443),
                    new[] { ClientConnectionScope.Control },
                    expiresAtMilliseconds: 2_000),
                sourceGeneration: 4);

            Assert.That(
                registry.TryTakeConnectionTicket(
                    lease,
                    currentGeneration: 3,
                    utcNowMilliseconds: 1_000,
                    out _),
                Is.False);
            Assert.That(
                registry.TryTakeConnectionTicket(
                    lease,
                    currentGeneration: 4,
                    utcNowMilliseconds: 1_000,
                    out var use),
                Is.True);
            Assert.That(use.SourceGeneration, Is.EqualTo(4));
            Assert.That(
                registry.TryTakeConnectionTicket(
                    lease,
                    currentGeneration: 4,
                    utcNowMilliseconds: 1_001,
                    out _),
                Is.False);
        }

        /// <summary>Generation exhaustion 必须 fail closed，不能回绕为旧 lineage。</summary>
        [Test]
        public void StateMachineRejectsGenerationOverflow()
        {
            var machine = new SessionStateMachine();
            Assert.Throws<InvalidOperationException>(
                () => machine.NextGeneration(long.MaxValue));
        }

        /// <summary>创建不含外部依赖的完整 Session snapshot。</summary>
        private static ClientSessionSnapshot Snapshot(long generation, long epoch)
        {
            return new ClientSessionSnapshot(
                new ClientAccountSummary("account", "display", 1),
                new ClientSessionSummary("session", epoch, 10_000),
                new ClientTokenPair("access", "refresh", 5_000, 9_000),
                generation);
        }
    }
}
