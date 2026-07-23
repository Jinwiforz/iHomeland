using System;
using IHomeland.Client.Application.Contracts;

namespace IHomeland.Client.Application.Gameplay
{
    /// <summary>
    /// 标识 gameplay 诊断发生的固定生命周期阶段。
    /// </summary>
    internal enum ClientGameplayDiagnosticStage
    {
        /// <summary>签发 ticket 或建立 transport。</summary>
        Connect = 0,

        /// <summary>写入认证 preface。</summary>
        PrefaceWrite = 1,

        /// <summary>读取、framing 或解码 S2C frame。</summary>
        Reader = 2,

        /// <summary>序列化写入 C2S frame。</summary>
        Writer = 3,

        /// <summary>等待或处理 active heartbeat。</summary>
        Heartbeat = 4,

        /// <summary>等待 correlated operation 结果。</summary>
        Pending = 5,

        /// <summary>线性化关闭 current generation。</summary>
        Close = 6,
    }

    /// <summary>
    /// 保存不含 endpoint、credential、payload、异常文本或业务 identity 的 Development 诊断。
    /// </summary>
    internal sealed class ClientGameplayDiagnostic
    {
        /// <summary>创建不可变低敏诊断事件。</summary>
        /// <param name="generation">事件所属 connection generation。</param>
        /// <param name="stage">固定生命周期阶段。</param>
        /// <param name="closeReason">稳定关闭分类。</param>
        /// <param name="exceptionType">可选 CLR 异常类型名，不包含 message 或 stack。</param>
        internal ClientGameplayDiagnostic(
            long generation,
            ClientGameplayDiagnosticStage stage,
            ClientGameplayCloseReason closeReason,
            string exceptionType)
        {
            Generation = generation;
            Stage = stage;
            CloseReason = closeReason;
            ExceptionType = exceptionType ?? string.Empty;
        }

        /// <summary>获取事件所属 connection generation。</summary>
        internal long Generation { get; }

        /// <summary>获取固定生命周期阶段。</summary>
        internal ClientGameplayDiagnosticStage Stage { get; }

        /// <summary>获取稳定关闭分类。</summary>
        internal ClientGameplayCloseReason CloseReason { get; }

        /// <summary>获取可选 CLR 异常类型名。</summary>
        internal string ExceptionType { get; }
    }

    /// <summary>
    /// 标识唯一 gameplay channel 的安全可观察生命周期。
    /// </summary>
    internal enum ClientGameplayChannelState
    {
        /// <summary>尚未由 AppLifetime 初始化。</summary>
        Created = 0,

        /// <summary>允许显式 connect，但没有网络资源。</summary>
        Ready = 1,

        /// <summary>正在签发凭据或建立 transport。</summary>
        Connecting = 2,

        /// <summary>JOIN/RECONNECT 等待首个匹配 command 完成。</summary>
        Pending = 3,

        /// <summary>Gameplay connection 可发送登记 operation。</summary>
        Active = 4,

        /// <summary>正在撤销发送、pending 与 transport。</summary>
        Closing = 5,

        /// <summary>App Scope 已停止且不可重启。</summary>
        Stopped = 6,
    }

    /// <summary>
    /// 标识不包含 endpoint、payload 或异常文本的稳定关闭原因。
    /// </summary>
    internal enum ClientGameplayCloseReason
    {
        /// <summary>尚未发生连接关闭。</summary>
        None = 0,

        /// <summary>调用方显式结束当前连接。</summary>
        Caller = 1,

        /// <summary>Peer 正常或无更多字节地关闭。</summary>
        Remote = 2,

        /// <summary>Frame、envelope、route 或 correlation 违反协议。</summary>
        Protocol = 3,

        /// <summary>Connect 或 pending operation 超过 deadline。</summary>
        Timeout = 4,

        /// <summary>有界 writer 或主线程队列达到上限。</summary>
        Backpressure = 5,

        /// <summary>统一 Session owner 已接受更高 epoch。</summary>
        SessionInvalidated = 6,

        /// <summary>Safe-return 等权威业务结果关闭旧 target。</summary>
        ApplicationReturn = 7,

        /// <summary>App Scope 正在逆序停止。</summary>
        Shutdown = 8,

        /// <summary>Socket、TLS 或 stream I/O 失败。</summary>
        Transport = 9,

        /// <summary>Active generation 未在冻结 deadline 内完成 gameplay heartbeat。</summary>
        HeartbeatTimeout = 10,
    }

    /// <summary>
    /// 标识 gameplay operation 的稳定本地失败分类。
    /// </summary>
    internal enum ClientGameplayFailureKind
    {
        /// <summary>Channel 状态、purpose 或 session generation 不允许调用。</summary>
        Policy = 0,

        /// <summary>调用方只取消本地等待。</summary>
        CallerCancelled = 1,

        /// <summary>Pending deadline 已到期。</summary>
        Timeout = 2,

        /// <summary>Pending 或 writer queue 达到硬上限。</summary>
        Backpressure = 3,

        /// <summary>Connection 在响应前结束。</summary>
        Disconnected = 4,

        /// <summary>Peer 返回的 response/payload 不符合契约。</summary>
        Protocol = 5,
    }

    /// <summary>
    /// 保存一次 gameplay operation 的成功、服务端拒绝或本地失败三选一结果。
    /// </summary>
    /// <typeparam name="T">强类型 response payload。</typeparam>
    internal sealed class ClientGameplayResult<T>
        where T : class
    {
        /// <summary>创建不可变 operation 结果。</summary>
        private ClientGameplayResult(
            T value,
            ClientServerError serverError,
            ClientGameplayFailureKind? failure)
        {
            Value = value;
            ServerError = serverError;
            Failure = failure;
        }

        /// <summary>获取成功 payload；非成功时为空。</summary>
        internal T Value { get; }

        /// <summary>获取结构有效的服务端 ErrorPayload；其他结果为空。</summary>
        internal ClientServerError ServerError { get; }

        /// <summary>获取稳定本地失败；成功或服务端拒绝时为空。</summary>
        internal ClientGameplayFailureKind? Failure { get; }

        /// <summary>报告结果是否为成功 payload。</summary>
        internal bool IsSuccess => Value != null && ServerError == null && !Failure.HasValue;

        /// <summary>创建成功结果。</summary>
        /// <param name="value">非空强类型 response。</param>
        /// <returns>只包含成功值的结果。</returns>
        internal static ClientGameplayResult<T> Success(T value)
        {
            return new ClientGameplayResult<T>(
                value ?? throw new ArgumentNullException(nameof(value)),
                null,
                null);
        }

        /// <summary>创建服务端拒绝结果。</summary>
        /// <param name="error">结构有效的公开 ErrorPayload。</param>
        /// <returns>只包含服务端错误的结果。</returns>
        internal static ClientGameplayResult<T> Rejected(ClientServerError error)
        {
            return new ClientGameplayResult<T>(
                null,
                error ?? throw new ArgumentNullException(nameof(error)),
                null);
        }

        /// <summary>创建稳定本地失败结果。</summary>
        /// <param name="failure">本地失败分类。</param>
        /// <returns>不包含 exception 或 payload 的结果。</returns>
        internal static ClientGameplayResult<T> Failed(ClientGameplayFailureKind failure)
        {
            return new ClientGameplayResult<T>(null, null, failure);
        }
    }

    /// <summary>
    /// 保存 gameplay channel 的低敏状态快照。
    /// </summary>
    internal sealed class ClientGameplayChannelSnapshot
    {
        /// <summary>创建状态、原因与 generation 快照。</summary>
        /// <param name="state">当前生命周期状态。</param>
        /// <param name="closeReason">最近稳定关闭原因。</param>
        /// <param name="generation">每次显式 connect 递增的本地 generation。</param>
        internal ClientGameplayChannelSnapshot(
            ClientGameplayChannelState state,
            ClientGameplayCloseReason closeReason,
            long generation)
        {
            State = state;
            CloseReason = closeReason;
            Generation = generation;
        }

        /// <summary>获取当前状态。</summary>
        internal ClientGameplayChannelState State { get; }

        /// <summary>获取最近稳定关闭原因。</summary>
        internal ClientGameplayCloseReason CloseReason { get; }

        /// <summary>获取阻止旧 pump 回写的本地 generation。</summary>
        internal long Generation { get; }
    }
}
