using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Application.Gameplay;
using IHomeland.Client.Foundation.Time;
using IHomeland.Protocol.Common.V1;

namespace IHomeland.Client.Infrastructure.Tcp
{
    /// <summary>保存 heartbeat owner 的稳定 terminal 结果。</summary>
    internal sealed class GameplayHeartbeatResult
    {
        /// <summary>创建低敏终态。</summary>
        internal GameplayHeartbeatResult(
            ClientGameplayCloseReason reason,
            Exception exception)
        {
            Reason = reason;
            Exception = exception;
        }

        /// <summary>获取是否需要关闭 generation。</summary>
        internal ClientGameplayCloseReason Reason { get; }

        /// <summary>获取仅供既有 diagnostics 降敏的异常。</summary>
        internal Exception Exception { get; }
    }

    /// <summary>拥有单个 active Gameplay generation 的 idle timer 与 heartbeat 决议。</summary>
    internal sealed class GameplayHeartbeat
    {
        /// <summary>固定 active 存活探测间隔。</summary>
        private static readonly TimeSpan Interval = TimeSpan.FromSeconds(15);

        /// <summary>提供可撤销、可测试 delay。</summary>
        private readonly IClientDelay _delay;

        /// <summary>创建 heartbeat owner。</summary>
        internal GameplayHeartbeat(IClientDelay delay)
        {
            _delay = delay ?? throw new ArgumentNullException(nameof(delay));
        }

        /// <summary>串行发送 heartbeat，直到 generation 取消或得出 terminal。</summary>
        internal async Task<GameplayHeartbeatResult> RunAsync(
            Func<bool> isActive,
            Func<CancellationToken, Task<ClientGameplayResult<GameplayHeartbeatResponse>>> send,
            CancellationToken cancellationToken)
        {
            if (isActive == null || send == null)
            {
                throw new ArgumentNullException("Gameplay heartbeat callback 不能为空。");
            }

            try
            {
                while (true)
                {
                    await _delay.DelayAsync(Interval, cancellationToken);
                    if (!isActive())
                    {
                        return new GameplayHeartbeatResult(
                            ClientGameplayCloseReason.None,
                            null);
                    }

                    var heartbeat = await send(cancellationToken);
                    if (heartbeat.IsSuccess)
                    {
                        continue;
                    }

                    if (cancellationToken.IsCancellationRequested)
                    {
                        return new GameplayHeartbeatResult(
                            ClientGameplayCloseReason.None,
                            null);
                    }

                    var reason = heartbeat.ServerError != null ||
                                 heartbeat.Failure == ClientGameplayFailureKind.Protocol
                        ? ClientGameplayCloseReason.Protocol
                        : heartbeat.Failure == ClientGameplayFailureKind.Timeout
                            ? ClientGameplayCloseReason.HeartbeatTimeout
                            : ClientGameplayCloseReason.Transport;
                    return new GameplayHeartbeatResult(reason, null);
                }
            }
            catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
            {
                return new GameplayHeartbeatResult(
                    ClientGameplayCloseReason.None,
                    null);
            }
            catch (ClientGameplayProtocolException exception)
            {
                return new GameplayHeartbeatResult(
                    ClientGameplayCloseReason.Protocol,
                    exception);
            }
            catch (Exception exception)
            {
                return new GameplayHeartbeatResult(
                    ClientGameplayCloseReason.Transport,
                    exception);
            }
        }
    }
}
