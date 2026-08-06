using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Session.Application;

namespace IHomeland.Client.Session.Tests.EditMode
{
    /// <summary>
    /// 为Session与Composition测试提供线程安全、可控且不接触磁盘的secure store fake。
    /// </summary>
    internal sealed class FakeClientSecureSessionStore : IClientSecureSessionStore
    {
        /// <summary>测试对象图共享的有效environment binding。</summary>
        internal const string EnvironmentBinding =
            "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";

        /// <summary>保护record、计数与生命周期。</summary>
        private readonly object _sync = new object();

        /// <summary>当前内存record。</summary>
        private ClientSecureSessionRecord _record;

        /// <summary>Store是否已经停止。</summary>
        private bool _stopped;

        /// <summary>Replace调用计数。</summary>
        private int _replaceCount;

        /// <summary>Delete调用计数。</summary>
        private int _deleteCount;

        /// <summary>Read调用计数。</summary>
        private int _readCount;

        /// <summary>获取或设置下一次replace稳定结果。</summary>
        internal ClientSecureSessionStoreOutcome ReplaceOutcome { get; set; } =
            ClientSecureSessionStoreOutcome.Succeeded;

        /// <summary>获取或设置下一次delete稳定结果。</summary>
        internal ClientSecureSessionStoreOutcome DeleteOutcome { get; set; } =
            ClientSecureSessionStoreOutcome.Succeeded;

        /// <summary>获取或设置强制read失败；为空时按record存在性返回。</summary>
        internal ClientSecureSessionStoreOutcome? ReadFailureOutcome { get; set; }

        /// <summary>获取replace调用次数。</summary>
        internal int ReplaceCount => Volatile.Read(ref _replaceCount);

        /// <summary>获取delete调用次数。</summary>
        internal int DeleteCount => Volatile.Read(ref _deleteCount);

        /// <summary>获取read调用次数。</summary>
        internal int ReadCount => Volatile.Read(ref _readCount);

        /// <summary>获取或设置可阻塞、抛错或观察replace的测试delegate。</summary>
        internal Func<ClientSecureSessionRecord, CancellationToken,
            Task<ClientSecureSessionStoreResult<ClientSecureSessionStoreEmpty>>> ReplaceHandler { get; set; }

        /// <summary>获取或设置可阻塞、抛错或观察delete的测试delegate。</summary>
        internal Func<CancellationToken,
            Task<ClientSecureSessionStoreResult<ClientSecureSessionStoreEmpty>>> DeleteHandler { get; set; }

        /// <summary>取得当前fake record快照。</summary>
        /// <returns>当前record或null。</returns>
        internal ClientSecureSessionRecord GetRecord()
        {
            lock (_sync)
            {
                return _record;
            }
        }

        /// <inheritdoc />
        public Task InitializeAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            return Task.CompletedTask;
        }

        /// <inheritdoc />
        public Task<ClientSecureSessionStoreResult<ClientSecureSessionRecord>> ReadAsync(
            CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            Interlocked.Increment(ref _readCount);
            lock (_sync)
            {
                if (_stopped)
                {
                    return Task.FromResult(
                        ClientSecureSessionStoreResult<ClientSecureSessionRecord>.Failed(
                            ClientSecureSessionStoreOutcome.Stopped));
                }

                if (ReadFailureOutcome.HasValue)
                {
                    return Task.FromResult(
                        ClientSecureSessionStoreResult<ClientSecureSessionRecord>.Failed(
                            ReadFailureOutcome.Value));
                }

                return Task.FromResult(_record == null
                    ? ClientSecureSessionStoreResult<ClientSecureSessionRecord>.Failed(
                        ClientSecureSessionStoreOutcome.NotFound)
                    : ClientSecureSessionStoreResult<ClientSecureSessionRecord>.Success(_record));
            }
        }

        /// <inheritdoc />
        public async Task<ClientSecureSessionStoreResult<ClientSecureSessionStoreEmpty>> ReplaceAsync(
            ClientSecureSessionRecord record,
            CancellationToken cancellationToken)
        {
            if (record == null)
            {
                throw new ArgumentNullException(nameof(record));
            }

            Interlocked.Increment(ref _replaceCount);
            var handler = ReplaceHandler;
            var result = handler == null
                ? CreateMutationResult(ReplaceOutcome)
                : await handler(record, cancellationToken);
            if (result.IsSuccess)
            {
                lock (_sync)
                {
                    _record = record;
                }
            }

            return result;
        }

        /// <inheritdoc />
        public async Task<ClientSecureSessionStoreResult<ClientSecureSessionStoreEmpty>> DeleteAsync(
            CancellationToken cancellationToken)
        {
            Interlocked.Increment(ref _deleteCount);
            var handler = DeleteHandler;
            var result = handler == null
                ? CreateMutationResult(DeleteOutcome)
                : await handler(cancellationToken);
            if (result.IsSuccess || result.Outcome == ClientSecureSessionStoreOutcome.NotFound)
            {
                lock (_sync)
                {
                    _record = null;
                }
            }

            return result;
        }

        /// <inheritdoc />
        public Task StopAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            lock (_sync)
            {
                _stopped = true;
            }

            return Task.CompletedTask;
        }

        /// <summary>把可配置outcome转换为合法mutation result。</summary>
        /// <param name="outcome">测试指定的封闭结果。</param>
        /// <returns>成功或不携带值的失败。</returns>
        private static ClientSecureSessionStoreResult<ClientSecureSessionStoreEmpty>
            CreateMutationResult(ClientSecureSessionStoreOutcome outcome)
        {
            return outcome == ClientSecureSessionStoreOutcome.Succeeded
                ? ClientSecureSessionStoreResult<ClientSecureSessionStoreEmpty>.Success(
                    ClientSecureSessionStoreEmpty.Value)
                : ClientSecureSessionStoreResult<ClientSecureSessionStoreEmpty>.Failed(outcome);
        }
    }
}
