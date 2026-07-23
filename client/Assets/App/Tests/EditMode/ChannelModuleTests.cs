using System;
using System.Collections.Generic;
using System.Threading;
using IHomeland.Client.Application.Control;
using IHomeland.Client.Application.Gameplay;
using IHomeland.Client.Foundation.Lifetime;
using IHomeland.Client.Foundation.Threading;
using IHomeland.Client.Infrastructure.Tcp;
using IHomeland.Client.Infrastructure.WebSocket;
using IHomeland.Protocol.Common.V1;
using NUnit.Framework;

namespace IHomeland.Client.Tests.EditMode
{
    /// <summary>验证拆分后的 Control 与 Gameplay 连接子组件边界。</summary>
    internal sealed class ChannelModuleTests
    {
        /// <summary>Control retry 只为可恢复终态消耗冻结预算。</summary>
        [Test]
        public void ControlRetryPolicyRejectsTerminalFailuresAndExhaustedBudget()
        {
            var policy = new ControlRetryPolicy(
                new[] { TimeSpan.FromMilliseconds(10), TimeSpan.FromMilliseconds(20) });

            Assert.That(
                policy.TryGetDelay(
                    ClientControlCloseReason.TransportFailure,
                    0,
                    out var first),
                Is.True);
            Assert.That(first, Is.EqualTo(TimeSpan.FromMilliseconds(10)));
            Assert.That(
                policy.TryGetDelay(
                    ClientControlCloseReason.ProtocolFailure,
                    0,
                    out _),
                Is.False);
            Assert.That(
                policy.TryGetDelay(
                    ClientControlCloseReason.PeerClosed,
                    2,
                    out _),
                Is.False);
        }

        /// <summary>Dispatcher 在执行时复核 generation，并显式传递 backpressure。</summary>
        [Test]
        public void ControlPushDispatcherGatesGenerationAndBackpressure()
        {
            var mainThread = new RecordingDispatcher();
            var dispatcher = new ControlPushDispatcher(mainThread);
            var notification = new ClientQueueStatusPush(1, 2, 3, 4);
            ClientControlNotification published = null;

            Assert.That(
                dispatcher.TryPost(
                    7,
                    notification,
                    generation => generation == 7,
                    value => published = value),
                Is.True);
            mainThread.Drain();
            Assert.That(published, Is.SameAs(notification));

            mainThread.NextResult = DispatchPostResult.QueueFull;
            Assert.That(
                dispatcher.TryPost(8, notification, _ => true, _ => { }),
                Is.False);
        }

        /// <summary>Gameplay writer 保持 FIFO，并同时执行 item 与 byte 上限。</summary>
        [Test]
        public void GameplayWriterIsBoundedAndOrdered()
        {
            using (var writer = new GameplayWriter(itemCapacity: 2, byteCapacity: 5))
            {
                writer.Enqueue(new byte[] { 1, 2 });
                writer.Enqueue(new byte[] { 3, 4, 5 });

                Assert.That(writer.CanEnqueue(1), Is.False);
                Assert.That(writer.TryDequeue(out var first), Is.True);
                Assert.That(first, Is.EqualTo(new byte[] { 1, 2 }));
                Assert.That(writer.TryDequeue(out var second), Is.True);
                Assert.That(second, Is.EqualTo(new byte[] { 3, 4, 5 }));
                Assert.That(writer.TryDequeue(out _), Is.False);
            }
        }

        /// <summary>Pending registry 只按完整 correlation 取出，并在断开时 exactly-once 清理。</summary>
        [Test]
        public void GameplayPendingRegistryEnforcesCorrelationAndTerminalCleanup()
        {
            var registry = new GameplayPendingRegistry(capacity: 1);
            var operation = new GameplayPendingOperation(
                responseMessageID: 22,
                correlationKind: MessageKind.Response,
                parseResponse: _ => new object(),
                activatesConnection: false);

            Assert.That(registry.TryAdd("request-a", operation), Is.True);
            Assert.That(
                registry.TryTake(
                    "request-a",
                    messageID: 23,
                    correlationKind: MessageKind.Response,
                    out _),
                Is.False);
            Assert.That(registry.Count, Is.EqualTo(1));

            registry.FailAll(ClientGameplayFailureKind.Disconnected);
            Assert.That(registry.Count, Is.Zero);
            Assert.That(operation.Completion.Task.Result.Failure,
                Is.EqualTo(ClientGameplayFailureKind.Disconnected));
        }

        /// <summary>保存可控投递结果，并把 callback 留到显式 drain。</summary>
        private sealed class RecordingDispatcher : IClientMainThreadDispatcher
        {
            /// <summary>保存尚未执行的 callback。</summary>
            private readonly Queue<Action> _callbacks = new Queue<Action>();

            /// <summary>获取或设置下一次普通投递结果。</summary>
            internal DispatchPostResult NextResult { get; set; } =
                DispatchPostResult.Accepted;

            /// <summary>按配置接受或拒绝普通 callback。</summary>
            public DispatchPostResult TryPost(Action callback)
            {
                var result = NextResult;
                NextResult = DispatchPostResult.Accepted;
                if (result == DispatchPostResult.Accepted)
                {
                    _callbacks.Enqueue(callback);
                }

                return result;
            }

            /// <summary>测试替身把 critical callback 视作普通 callback。</summary>
            public DispatchPostResult TryPostCritical(Action callback)
            {
                return TryPost(callback);
            }

            /// <summary>顺序执行全部已接受 callback。</summary>
            internal void Drain()
            {
                while (_callbacks.Count > 0)
                {
                    _callbacks.Dequeue()();
                }
            }
        }
    }
}
