using System;
using System.Collections.Generic;
using System.Threading.Tasks;
using IHomeland.Client.Application.Contracts;
using IHomeland.Client.Application.Gameplay;
using IHomeland.Protocol.Common.V1;

namespace IHomeland.Client.Infrastructure.Tcp
{
    /// <summary>
    /// 保存单个 connection generation 的有界 correlation entries 与 exactly-once completion。
    /// </summary>
    internal sealed class GameplayPendingRegistry
    {
        /// <summary>保存冻结 capacity。</summary>
        private readonly int _capacity;

        /// <summary>按 opaque correlation key 保存 pending。</summary>
        private readonly Dictionary<string, GameplayPendingOperation> _entries =
            new Dictionary<string, GameplayPendingOperation>(StringComparer.Ordinal);

        /// <summary>创建 generation-scoped pending registry。</summary>
        internal GameplayPendingRegistry(int capacity)
        {
            if (capacity <= 0)
            {
                throw new ArgumentOutOfRangeException(nameof(capacity));
            }

            _capacity = capacity;
        }

        /// <summary>获取 current generation 的 entry 数量。</summary>
        internal int Count => _entries.Count;

        /// <summary>尝试登记唯一 correlation。</summary>
        internal bool TryAdd(string key, GameplayPendingOperation operation)
        {
            if (string.IsNullOrEmpty(key))
            {
                throw new ArgumentException("Pending key 不能为空。", nameof(key));
            }

            if (operation == null)
            {
                throw new ArgumentNullException(nameof(operation));
            }

            if (_entries.Count >= _capacity || _entries.ContainsKey(key))
            {
                return false;
            }

            _entries.Add(key, operation);
            return true;
        }

        /// <summary>按 correlation、response route 与 kind 原子移除匹配 entry。</summary>
        internal bool TryTake(
            string key,
            uint messageID,
            MessageKind correlationKind,
            out GameplayPendingOperation operation)
        {
            if (!_entries.TryGetValue(key, out operation) ||
                operation.ResponseMessageID != messageID ||
                operation.CorrelationKind != correlationKind)
            {
                operation = null;
                return false;
            }

            _entries.Remove(key);
            return true;
        }

        /// <summary>以同一 terminal failure 完成并清空 current generation entries。</summary>
        internal void FailAll(ClientGameplayFailureKind failure)
        {
            foreach (var pending in _entries.Values)
            {
                pending.Completion.TrySetResult(
                    GameplayPendingCompletion.Failed(failure));
            }

            _entries.Clear();
        }
    }

    /// <summary>保存 pending route、parser 与 exactly-once completion。</summary>
    internal sealed class GameplayPendingOperation
    {
        /// <summary>创建 pending entry。</summary>
        internal GameplayPendingOperation(
            uint responseMessageID,
            MessageKind correlationKind,
            Func<ClientGameplayEnvelope, object> parseResponse,
            bool activatesConnection)
        {
            ResponseMessageID = responseMessageID;
            CorrelationKind = correlationKind;
            ParseResponse = parseResponse ??
                throw new ArgumentNullException(nameof(parseResponse));
            ActivatesConnection = activatesConnection;
            Completion = new TaskCompletionSource<GameplayPendingCompletion>(
                TaskCreationOptions.RunContinuationsAsynchronously);
        }

        /// <summary>获取唯一 expected response ID。</summary>
        internal uint ResponseMessageID { get; }

        /// <summary>获取 response/error 必须沿用的 correlation kind。</summary>
        internal MessageKind CorrelationKind { get; }

        /// <summary>获取固定 generated response parser。</summary>
        internal Func<ClientGameplayEnvelope, object> ParseResponse { get; }

        /// <summary>报告成功 response 是否把 pending target 提升为 Active。</summary>
        internal bool ActivatesConnection { get; }

        /// <summary>获取 exactly-once completion owner。</summary>
        internal TaskCompletionSource<GameplayPendingCompletion> Completion { get; }
    }

    /// <summary>保存 pending 成功、服务端拒绝或本地失败。</summary>
    internal sealed class GameplayPendingCompletion
    {
        /// <summary>创建内部三选一结果。</summary>
        private GameplayPendingCompletion(
            object value,
            ClientServerError error,
            ClientGameplayFailureKind failure)
        {
            Value = value;
            Error = error;
            Failure = failure;
        }

        /// <summary>获取成功 response。</summary>
        internal object Value { get; }

        /// <summary>获取服务端公开错误。</summary>
        internal ClientServerError Error { get; }

        /// <summary>获取本地失败。</summary>
        internal ClientGameplayFailureKind Failure { get; }

        /// <summary>创建成功结果。</summary>
        internal static GameplayPendingCompletion Succeeded(object value)
        {
            return new GameplayPendingCompletion(
                value ?? throw new ArgumentNullException(nameof(value)),
                null,
                default);
        }

        /// <summary>创建服务端拒绝结果。</summary>
        internal static GameplayPendingCompletion Rejected(ClientServerError error)
        {
            return new GameplayPendingCompletion(
                null,
                error ?? throw new ArgumentNullException(nameof(error)),
                default);
        }

        /// <summary>创建本地失败结果。</summary>
        internal static GameplayPendingCompletion Failed(
            ClientGameplayFailureKind failure)
        {
            return new GameplayPendingCompletion(null, null, failure);
        }
    }
}
