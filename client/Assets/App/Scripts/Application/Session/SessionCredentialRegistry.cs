using System;
using IHomeland.Client.Application.Contracts;

namespace IHomeland.Client.Application.Session
{
    /// <summary>
    /// 集中创建并单次交付 connection ticket 与 world admission lease。
    /// </summary>
    /// <remarks>
    /// Registry 不保存 owner snapshot；current generation 与时间必须由 Session owner 在锁内传入。
    /// </remarks>
    internal sealed class SessionCredentialRegistry
    {
        /// <summary>创建绑定签发 generation 的 connection ticket lease。</summary>
        internal ClientConnectionTicketLease CreateConnectionTicket(
            ClientConnectionTicket ticket,
            long sourceGeneration)
        {
            return new ClientConnectionTicketLease(
                ticket ?? throw new ArgumentNullException(nameof(ticket)),
                sourceGeneration);
        }

        /// <summary>创建绑定签发 generation 的 world admission lease。</summary>
        internal ClientWorldAdmissionLease CreateWorldAdmission(
            ClientWorldAdmission admission,
            long sourceGeneration)
        {
            return new ClientWorldAdmissionLease(
                admission ?? throw new ArgumentNullException(nameof(admission)),
                sourceGeneration);
        }

        /// <summary>按 current generation/expiry 单次取得 ticket。</summary>
        internal bool TryTakeConnectionTicket(
            ClientConnectionTicketLease lease,
            long currentGeneration,
            long utcNowMilliseconds,
            out ClientConnectionTicketUse ticketUse)
        {
            if (lease == null)
            {
                throw new ArgumentNullException(nameof(lease));
            }

            return lease.TryTake(
                currentGeneration,
                utcNowMilliseconds,
                out ticketUse);
        }

        /// <summary>按 current generation/expiry/binding 单次取得 admission。</summary>
        internal bool TryTakeWorldAdmission(
            ClientWorldAdmissionLease lease,
            long currentGeneration,
            long utcNowMilliseconds,
            out ClientWorldAdmissionUse admissionUse)
        {
            if (lease == null)
            {
                throw new ArgumentNullException(nameof(lease));
            }

            return lease.TryTake(
                currentGeneration,
                utcNowMilliseconds,
                out admissionUse);
        }
    }
}
