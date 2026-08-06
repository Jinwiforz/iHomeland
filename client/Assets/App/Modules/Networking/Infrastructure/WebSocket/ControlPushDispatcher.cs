using System;
using IHomeland.Client.Networking.Application.Control;
using IHomeland.Client.Core.Foundation.Lifetime;
using IHomeland.Client.Core.Foundation.Threading;

namespace IHomeland.Client.Networking.Infrastructure.WebSocket
{
    /// <summary>
    /// 把 typed control PUSH 有界投递到主线程，并在执行前复核 connection generation。
    /// </summary>
    internal sealed class ControlPushDispatcher
    {
        /// <summary>保存唯一主线程 dispatcher port。</summary>
        private readonly IClientMainThreadDispatcher _dispatcher;

        /// <summary>创建 typed push dispatcher。</summary>
        internal ControlPushDispatcher(IClientMainThreadDispatcher dispatcher)
        {
            _dispatcher = dispatcher ?? throw new ArgumentNullException(nameof(dispatcher));
        }

        /// <summary>有界排队；generation 失效时静默丢弃已排队 callback。</summary>
        internal bool TryPost(
            long generation,
            ClientControlNotification notification,
            Func<long, bool> isCurrent,
            Action<ClientControlNotification> publish)
        {
            if (notification == null)
            {
                throw new ArgumentNullException(nameof(notification));
            }

            if (isCurrent == null)
            {
                throw new ArgumentNullException(nameof(isCurrent));
            }

            if (publish == null)
            {
                throw new ArgumentNullException(nameof(publish));
            }

            return _dispatcher.TryPost(() =>
            {
                if (isCurrent(generation))
                {
                    publish(notification);
                }
            }) == DispatchPostResult.Accepted;
        }
    }
}
