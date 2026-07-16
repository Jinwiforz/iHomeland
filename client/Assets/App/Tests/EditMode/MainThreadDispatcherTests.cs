using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Core.Lifetime;
using NUnit.Framework;

namespace IHomeland.Client.Tests.EditMode
{
    /// <summary>
    /// 验证主线程 Dispatcher 的线程、容量、错误隔离和停止边界。
    /// </summary>
    public sealed class MainThreadDispatcherTests
    {
        /// <summary>
        /// 保护后台投递不会内联执行，并只在捕获主线程 drain 时运行一次。
        /// </summary>
        /// <returns>等待后台投递、主线程 drain 和清理完成的任务。</returns>
        [Test]
        public async Task BackgroundPostExecutesOnlyDuringMainThreadDrain()
        {
            var mainThreadId = Environment.CurrentManagedThreadId;
            var dispatcher = new MainThreadDispatcher(mainThreadId, capacity: 4);
            await dispatcher.InitializeAsync(CancellationToken.None);
            var callbackThreadId = 0;

            var postResult = await Task.Run(
                () => dispatcher.TryPost(() => callbackThreadId = Environment.CurrentManagedThreadId));

            Assert.That(postResult, Is.EqualTo(DispatchPostResult.Accepted));
            Assert.That(callbackThreadId, Is.EqualTo(0));
            var drain = dispatcher.Drain(maximumCallbacks: 4);
            Assert.That(drain.ExecutedCount, Is.EqualTo(1));
            Assert.That(drain.Errors, Is.Empty);
            Assert.That(callbackThreadId, Is.EqualTo(mainThreadId));
            await dispatcher.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 保护固定容量满后产生明确拒绝而不是无界积压。
        /// </summary>
        /// <returns>等待 Dispatcher 初始化和清理完成的任务。</returns>
        [Test]
        public async Task CapacityRejectsAdditionalCallbacks()
        {
            var dispatcher = new MainThreadDispatcher(Environment.CurrentManagedThreadId, capacity: 1);
            await dispatcher.InitializeAsync(CancellationToken.None);

            Assert.That(dispatcher.TryPost(() => { }), Is.EqualTo(DispatchPostResult.Accepted));
            Assert.That(dispatcher.TryPost(() => { }), Is.EqualTo(DispatchPostResult.QueueFull));
            Assert.That(dispatcher.PendingCount, Is.EqualTo(1));
            await dispatcher.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 保护单个 callback 异常被收集且不会阻止同批后续工作。
        /// </summary>
        /// <returns>等待 Dispatcher 初始化和清理完成的任务。</returns>
        [Test]
        public async Task CallbackFailureDoesNotBlockRemainingBatch()
        {
            var dispatcher = new MainThreadDispatcher(Environment.CurrentManagedThreadId, capacity: 2);
            await dispatcher.InitializeAsync(CancellationToken.None);
            var secondExecuted = false;
            var callbackError = new InvalidOperationException("injected callback failure");
            dispatcher.TryPost(() => throw callbackError);
            dispatcher.TryPost(() => secondExecuted = true);

            var result = dispatcher.Drain(maximumCallbacks: 2);

            Assert.That(result.ExecutedCount, Is.EqualTo(2));
            Assert.That(result.Errors, Has.Count.EqualTo(1));
            Assert.That(result.Errors[0], Is.SameAs(callbackError));
            Assert.That(secondExecuted, Is.True);
            await dispatcher.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 保护 drain 结果复制错误集合，调用方后续修改源集合不会改变已发布诊断。
        /// </summary>
        [Test]
        public void DrainResultCopiesErrorCollection()
        {
            var callbackError = new InvalidOperationException("injected callback failure");
            var sourceErrors = new List<Exception> { callbackError };

            var result = new MainThreadDrainResult(executedCount: 1, errors: sourceErrors);
            sourceErrors.Clear();

            Assert.That(result.Errors, Has.Count.EqualTo(1));
            Assert.That(result.Errors[0], Is.SameAs(callbackError));
        }

        /// <summary>
        /// 保护停止清空旧工作并永久拒绝后续投递。
        /// </summary>
        /// <returns>等待 Dispatcher 初始化与停止完成的任务。</returns>
        [Test]
        public async Task StopClearsPendingCallbacksAndRejectsNewWork()
        {
            var dispatcher = new MainThreadDispatcher(Environment.CurrentManagedThreadId, capacity: 2);
            await dispatcher.InitializeAsync(CancellationToken.None);
            dispatcher.TryPost(() => { });

            await dispatcher.StopAsync(CancellationToken.None);

            Assert.That(dispatcher.PendingCount, Is.EqualTo(0));
            Assert.That(dispatcher.TryPost(() => { }), Is.EqualTo(DispatchPostResult.Stopped));
            Assert.Throws<InvalidOperationException>(
                () => dispatcher.InitializeAsync(CancellationToken.None));
        }

        /// <summary>
        /// 保护非捕获线程不能执行 callback 或访问 Unity 主线程所有权。
        /// </summary>
        /// <returns>等待后台拒绝结果与清理完成的任务。</returns>
        [Test]
        public async Task DrainFromBackgroundThreadIsRejected()
        {
            var dispatcher = new MainThreadDispatcher(Environment.CurrentManagedThreadId, capacity: 1);
            await dispatcher.InitializeAsync(CancellationToken.None);
            dispatcher.TryPost(() => { });

            var error = await Task.Run(
                () => Assert.Throws<InvalidOperationException>(() => dispatcher.Drain(maximumCallbacks: 1)));

            Assert.That(error, Is.Not.Null);
            Assert.That(dispatcher.PendingCount, Is.EqualTo(1));
            await dispatcher.StopAsync(CancellationToken.None);
        }
    }
}
