using System;

namespace IHomeland.Client.Session.Application
{
    /// <summary>标识启动恢复唯一状态机的稳定低敏终态。</summary>
    internal enum ClientSessionRestoreOutcome
    {
        /// <summary>没有可恢复record或平台明确Unsupported。</summary>
        NotAvailable = 0,

        /// <summary>Refresh轮换、secure replace与内存commit全部成功。</summary>
        Restored = 1,

        /// <summary>Record过期或服务端权威拒绝旧lineage。</summary>
        Rejected = 2,

        /// <summary>Bootstrap或refresh结果无法在当前运行中确定。</summary>
        Unresolved = 3,

        /// <summary>读取、保护、替换或退休失败。</summary>
        StorageFailure = 4,

        /// <summary>App Scope停止并拒绝迟到恢复commit。</summary>
        Stopped = 5,

        /// <summary>另一进程已经拥有同一production secure profile。</summary>
        ProfileInUse = 6,
    }

    /// <summary>保存启动恢复终态与仅成功时存在的current snapshot。</summary>
    internal sealed class ClientSessionRestoreResult
    {
        /// <summary>创建封闭恢复结果。</summary>
        /// <param name="outcome">稳定恢复终态。</param>
        /// <param name="snapshot">仅Restored时存在的snapshot。</param>
        private ClientSessionRestoreResult(
            ClientSessionRestoreOutcome outcome,
            ClientSessionSnapshot snapshot)
        {
            if (outcome == ClientSessionRestoreOutcome.Restored && snapshot == null)
            {
                throw new ArgumentNullException(nameof(snapshot));
            }

            if (outcome != ClientSessionRestoreOutcome.Restored && snapshot != null)
            {
                throw new ArgumentException("非成功restore结果不得携带snapshot。", nameof(snapshot));
            }

            Outcome = outcome;
            Snapshot = snapshot;
        }

        /// <summary>获取稳定恢复终态。</summary>
        internal ClientSessionRestoreOutcome Outcome { get; }

        /// <summary>获取成功时current snapshot。</summary>
        internal ClientSessionSnapshot Snapshot { get; }

        /// <summary>创建成功恢复结果。</summary>
        /// <param name="snapshot">已发布为current的snapshot。</param>
        /// <returns>Restored结果。</returns>
        internal static ClientSessionRestoreResult Restored(ClientSessionSnapshot snapshot)
        {
            return new ClientSessionRestoreResult(
                ClientSessionRestoreOutcome.Restored,
                snapshot);
        }

        /// <summary>创建不携带identity或credential的非成功结果。</summary>
        /// <param name="outcome">Restored以外的终态。</param>
        /// <returns>封闭低敏结果。</returns>
        internal static ClientSessionRestoreResult Failed(ClientSessionRestoreOutcome outcome)
        {
            if (outcome == ClientSessionRestoreOutcome.Restored)
            {
                throw new ArgumentException("Restored必须携带snapshot。", nameof(outcome));
            }

            return new ClientSessionRestoreResult(outcome, null);
        }

        /// <summary>返回不含snapshot、identity或credential的固定摘要。</summary>
        /// <returns>只包含outcome。</returns>
        public override string ToString()
        {
            return $"ClientSessionRestoreResult outcome={Outcome}";
        }
    }
}
