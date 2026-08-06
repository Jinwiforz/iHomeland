using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Core.Runtime.Scenes;
using NUnit.Framework;

namespace IHomeland.Client.PersonalWorld.Tests.EditMode
{
    /// <summary>
    /// 验证 Scene Scope generation、取消与重复释放不会让迟到 callback 获得写资格。
    /// </summary>
    public sealed class SceneLifetimeOwnerTests
    {
        /// <summary>
        /// 保护新 Scene Scope 单调递增并立即使上一代失效。
        /// </summary>
        /// <returns>等待 owner 初始化、代际切换和清理完成的任务。</returns>
        [Test]
        public async Task NewSceneCancelsAndInvalidatesPreviousGeneration()
        {
            var owner = new SceneLifetimeOwner();
            await owner.InitializeAsync(CancellationToken.None);
            var first = owner.BeginScene();

            var second = owner.BeginScene();

            Assert.That(second.Generation, Is.GreaterThan(first.Generation));
            Assert.That(first.CancellationToken.IsCancellationRequested, Is.True);
            Assert.That(first.CanCommit, Is.False);
            Assert.That(second.CancellationToken.IsCancellationRequested, Is.False);
            Assert.That(second.CanCommit, Is.True);
            await owner.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 保护 SceneContext 卸载与 App Scope 停止重复释放同一代时保持幂等。
        /// </summary>
        /// <returns>等待重复释放和 owner 停止完成的任务。</returns>
        [Test]
        public async Task RepeatedReleaseIsIdempotent()
        {
            var owner = new SceneLifetimeOwner();
            await owner.InitializeAsync(CancellationToken.None);
            var scene = owner.BeginScene();

            scene.Dispose();
            scene.Dispose();
            await owner.StopAsync(CancellationToken.None);

            Assert.That(scene.CancellationToken.IsCancellationRequested, Is.True);
            Assert.That(scene.CanCommit, Is.False);
        }

        /// <summary>
        /// 保护 App Scope 停止先取消当前场景，并永久拒绝旧 owner 创建新场景。
        /// </summary>
        /// <returns>等待 owner 停止和终态断言完成的任务。</returns>
        [Test]
        public async Task AppStopCancelsCurrentSceneAndRejectsNewGeneration()
        {
            var owner = new SceneLifetimeOwner();
            await owner.InitializeAsync(CancellationToken.None);
            var scene = owner.BeginScene();

            await owner.StopAsync(CancellationToken.None);

            Assert.That(scene.CancellationToken.IsCancellationRequested, Is.True);
            Assert.That(scene.CanCommit, Is.False);
            Assert.Throws<InvalidOperationException>(() => owner.BeginScene());
        }
    }
}
