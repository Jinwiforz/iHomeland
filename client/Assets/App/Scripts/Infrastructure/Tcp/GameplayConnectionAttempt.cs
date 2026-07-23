using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Application.Contracts;
using IHomeland.Client.Application.Gameplay;
using IHomeland.Client.Application.Session;

namespace IHomeland.Client.Infrastructure.Tcp
{
    /// <summary>保存已完成 TLS connect 与 preface、等待 channel generation 提交的候选连接。</summary>
    internal sealed class GameplayConnectionCandidate : IDisposable
    {
        /// <summary>创建拥有 transport 的候选结果。</summary>
        internal GameplayConnectionCandidate(
            IClientGameplayConnection connection,
            long sourceSessionGeneration,
            ClientWorldAdmissionPurpose purpose,
            string pendingAdmission)
        {
            Connection = connection ?? throw new ArgumentNullException(nameof(connection));
            SourceSessionGeneration = sourceSessionGeneration;
            Purpose = purpose;
            PendingAdmission = pendingAdmission;
        }

        /// <summary>获取唯一 transport；提交后由 channel 调用 <see cref="TakeConnection"/> 转移。</summary>
        internal IClientGameplayConnection Connection { get; private set; }

        /// <summary>获取 admission 与 ticket 的共同 Session generation。</summary>
        internal long SourceSessionGeneration { get; }

        /// <summary>获取服务端授权 purpose。</summary>
        internal ClientWorldAdmissionPurpose Purpose { get; }

        /// <summary>获取 JOIN/RECONNECT 首 command 所需的一次 admission。</summary>
        internal string PendingAdmission { get; }

        /// <summary>把 transport 所有权单次转移给 channel。</summary>
        internal IClientGameplayConnection TakeConnection()
        {
            var connection = Connection ??
                throw new InvalidOperationException("Gameplay candidate 已提交。");
            Connection = null;
            return connection;
        }

        /// <summary>释放尚未提交的 transport。</summary>
        public void Dispose()
        {
            Connection?.Dispose();
            Connection = null;
        }
    }

    /// <summary>保存 attempt 成功候选或低敏失败分类。</summary>
    internal sealed class GameplayConnectionAttemptResult
    {
        /// <summary>创建 attempt 结果。</summary>
        private GameplayConnectionAttemptResult(
            GameplayConnectionCandidate candidate,
            ClientGameplayDiagnosticStage stage,
            ClientGameplayCloseReason reason,
            Exception exception)
        {
            Candidate = candidate;
            Stage = stage;
            Reason = reason;
            Exception = exception;
        }

        /// <summary>获取成功候选。</summary>
        internal GameplayConnectionCandidate Candidate { get; }

        /// <summary>获取失败阶段。</summary>
        internal ClientGameplayDiagnosticStage Stage { get; }

        /// <summary>获取低敏失败原因。</summary>
        internal ClientGameplayCloseReason Reason { get; }

        /// <summary>获取仅供既有 diagnostics 降敏的异常。</summary>
        internal Exception Exception { get; }

        /// <summary>获取是否成功。</summary>
        internal bool IsSuccess => Candidate != null;

        /// <summary>创建成功候选。</summary>
        internal static GameplayConnectionAttemptResult Succeeded(
            GameplayConnectionCandidate candidate)
        {
            return new GameplayConnectionAttemptResult(
                candidate ?? throw new ArgumentNullException(nameof(candidate)),
                ClientGameplayDiagnosticStage.Connect,
                ClientGameplayCloseReason.None,
                null);
        }

        /// <summary>创建稳定失败。</summary>
        internal static GameplayConnectionAttemptResult Failed(
            ClientGameplayDiagnosticStage stage,
            ClientGameplayCloseReason reason,
            Exception exception = null)
        {
            return new GameplayConnectionAttemptResult(null, stage, reason, exception);
        }
    }

    /// <summary>
    /// 拥有 admission 单次 take、匹配 ticket、TLS connect 与首个 preface write。
    /// </summary>
    internal sealed class GameplayConnectionAttempt
    {
        /// <summary>限制 connect 与 TLS handshake。</summary>
        private static readonly TimeSpan ConnectTimeout = TimeSpan.FromSeconds(10);

        /// <summary>唯一 Session credential owner。</summary>
        private readonly SessionCoordinator _sessionCoordinator;

        /// <summary>为每个 generation 创建独立 transport。</summary>
        private readonly IClientGameplayConnectionFactory _connectionFactory;

        /// <summary>创建 connection attempt flow。</summary>
        internal GameplayConnectionAttempt(
            SessionCoordinator sessionCoordinator,
            IClientGameplayConnectionFactory connectionFactory)
        {
            _sessionCoordinator = sessionCoordinator ??
                throw new ArgumentNullException(nameof(sessionCoordinator));
            _connectionFactory = connectionFactory ??
                throw new ArgumentNullException(nameof(connectionFactory));
        }

        /// <summary>运行到 preface 成功或稳定失败，不提交 channel generation。</summary>
        internal async Task<GameplayConnectionAttemptResult> RunAsync(
            ClientWorldAdmissionLease admissionLease,
            CancellationToken lifetimeToken,
            CancellationToken callerToken)
        {
            if (!_sessionCoordinator.TryTakeWorldAdmission(
                    admissionLease,
                    out var admissionUse) ||
                !IsAdmissionBindingValid(admissionUse))
            {
                return GameplayConnectionAttemptResult.Failed(
                    ClientGameplayDiagnosticStage.Connect,
                    ClientGameplayCloseReason.Protocol);
            }

            ClientGatewayResult<ClientConnectionTicketLease> ticketResult;
            try
            {
                ticketResult = await _sessionCoordinator.IssueConnectionTicketAsync(
                    ClientEndpointChannel.TlsTcp,
                    callerToken);
            }
            catch (OperationCanceledException)
            {
                return GameplayConnectionAttemptResult.Failed(
                    ClientGameplayDiagnosticStage.Connect,
                    ClientGameplayCloseReason.Caller);
            }
            catch (Exception exception)
            {
                return GameplayConnectionAttemptResult.Failed(
                    ClientGameplayDiagnosticStage.Connect,
                    ClientGameplayCloseReason.Transport,
                    exception);
            }

            if (!ticketResult.IsSuccess ||
                !_sessionCoordinator.TryTakeConnectionTicket(
                    ticketResult.Value,
                    out var ticketUse) ||
                !EndpointsEqual(ticketUse.Endpoint, admissionUse.Endpoint) ||
                ticketUse.SourceGeneration != admissionUse.SourceGeneration)
            {
                return GameplayConnectionAttemptResult.Failed(
                    ClientGameplayDiagnosticStage.Connect,
                    ClientGameplayCloseReason.Protocol);
            }

            byte[] preface;
            try
            {
                preface = ClientGameplayPreface.Encode(
                    ticketUse.Credential,
                    admissionUse.Credential,
                    admissionUse.Purpose);
            }
            catch (ClientGameplayProtocolException exception)
            {
                return GameplayConnectionAttemptResult.Failed(
                    ClientGameplayDiagnosticStage.PrefaceWrite,
                    ClientGameplayCloseReason.Protocol,
                    exception);
            }

            IClientGameplayConnection connection = null;
            var stage = ClientGameplayDiagnosticStage.Connect;
            try
            {
                using (var timeout = new CancellationTokenSource(ConnectTimeout))
                using (var linked = CancellationTokenSource.CreateLinkedTokenSource(
                           callerToken,
                           timeout.Token,
                           lifetimeToken))
                {
                    connection = await _connectionFactory.ConnectAsync(
                        admissionUse.Endpoint,
                        linked.Token);
                    stage = ClientGameplayDiagnosticStage.PrefaceWrite;
                    await connection.WriteAsync(preface, linked.Token);
                }

                var candidate = new GameplayConnectionCandidate(
                    connection,
                    admissionUse.SourceGeneration,
                    admissionUse.Purpose,
                    admissionUse.Purpose == ClientWorldAdmissionPurpose.OwnWorld
                        ? null
                        : admissionUse.Credential);
                connection = null;
                return GameplayConnectionAttemptResult.Succeeded(candidate);
            }
            catch (OperationCanceledException)
            {
                return GameplayConnectionAttemptResult.Failed(
                    stage,
                    callerToken.IsCancellationRequested
                        ? ClientGameplayCloseReason.Caller
                        : ClientGameplayCloseReason.Timeout);
            }
            catch (Exception exception)
            {
                return GameplayConnectionAttemptResult.Failed(
                    stage,
                    ClientGameplayCloseReason.Transport,
                    exception);
            }
            finally
            {
                connection?.Dispose();
            }
        }

        /// <summary>验证 admission channel、purpose 与 scope。</summary>
        private static bool IsAdmissionBindingValid(ClientWorldAdmissionUse admission)
        {
            if (admission == null ||
                admission.Endpoint.Channel != ClientEndpointChannel.TlsTcp)
            {
                return false;
            }

            return admission.Purpose == ClientWorldAdmissionPurpose.OwnWorld
                ? admission.Role == ClientWorldRole.Owner
                : admission.Role == ClientWorldRole.Visitor &&
                  (admission.Purpose == ClientWorldAdmissionPurpose.Join ||
                   admission.Purpose == ClientWorldAdmissionPurpose.Reconnect);
        }

        /// <summary>比较 ticket 与 admission endpoint。</summary>
        private static bool EndpointsEqual(ClientEndpoint left, ClientEndpoint right)
        {
            return left != null &&
                   right != null &&
                   left.Channel == right.Channel &&
                   left.Port == right.Port &&
                   string.Equals(left.Host, right.Host, StringComparison.OrdinalIgnoreCase);
        }
    }
}
