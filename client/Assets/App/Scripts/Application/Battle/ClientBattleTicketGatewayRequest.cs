using System;
using IHomeland.Client.Application.Contracts;

namespace IHomeland.Client.Application.Battle
{
    /// <summary>
    /// 保存 issueBattleTicket 的单次 authorization、target 与稳定幂等 identity。
    /// </summary>
    internal sealed class ClientBattleTicketGatewayRequest
    {
        /// <summary>
        /// 创建不可变 BattleTicket gateway 请求。
        /// </summary>
        /// <param name="authorization">只允许 HTTP adapter 单次取得的 access token lease。</param>
        /// <param name="target">不含 credential 的 current target intent。</param>
        /// <param name="idempotencyKey">同一 connect attempt 重试时保持不变的安全 ASCII key。</param>
        internal ClientBattleTicketGatewayRequest(
            ClientCredentialLease authorization,
            ClientBattleTargetIntent target,
            string idempotencyKey)
        {
            Authorization = authorization ??
                throw new ArgumentNullException(nameof(authorization));
            Target = target ?? throw new ArgumentNullException(nameof(target));
            IdempotencyKey = idempotencyKey ??
                throw new ArgumentNullException(nameof(idempotencyKey));
        }

        /// <summary>获取单次 HTTP authorization lease。</summary>
        internal ClientCredentialLease Authorization { get; }

        /// <summary>获取 current immutable target intent。</summary>
        internal ClientBattleTargetIntent Target { get; }

        /// <summary>获取 current connect attempt 的稳定幂等 identity。</summary>
        internal string IdempotencyKey { get; }
    }
}
