using System;
using System.Collections.Generic;

namespace IHomeland.Client.Infrastructure.WebSocket
{
    /// <summary>纯计算 control transient failure 的有限 retry budget 与 backoff。</summary>
    internal sealed class ControlRetryPolicy
    {
        /// <summary>保存冻结、正数 backoff。</summary>
        private readonly TimeSpan[] _delays;

        /// <summary>创建有限 retry policy。</summary>
        internal ControlRetryPolicy(IReadOnlyList<TimeSpan> delays)
        {
            if (delays == null)
            {
                throw new ArgumentNullException(nameof(delays));
            }

            _delays = new TimeSpan[delays.Count];
            for (var index = 0; index < delays.Count; index++)
            {
                if (delays[index] <= TimeSpan.Zero)
                {
                    throw new ArgumentException(
                        "Control retry delay 必须为正数。",
                        nameof(delays));
                }

                _delays[index] = delays[index];
            }
        }

        /// <summary>尝试为可恢复失败取得当前 retry delay。</summary>
        internal bool TryGetDelay(
            ClientControlCloseReason reason,
            int retryIndex,
            out TimeSpan delay)
        {
            var recoverable =
                reason == ClientControlCloseReason.TransportFailure ||
                reason == ClientControlCloseReason.PeerClosed;
            if (!recoverable || retryIndex < 0 || retryIndex >= _delays.Length)
            {
                delay = default;
                return false;
            }

            delay = _delays[retryIndex];
            return true;
        }
    }
}
