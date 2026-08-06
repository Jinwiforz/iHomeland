using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Networking.Application.Contracts;
using IHomeland.Client.Networking.Application.Ports;

namespace IHomeland.Client.Session.Application
{
    /// <summary>执行 register/login gateway 调用，不保存 Session 最终事实。</summary>
    internal sealed class SessionAuthenticationFlow
    {
        /// <summary>保存固定 Session gateway port。</summary>
        private readonly IClientSessionGateway _gateway;

        /// <summary>创建 authentication flow。</summary>
        internal SessionAuthenticationFlow(IClientSessionGateway gateway)
        {
            _gateway = gateway ?? throw new ArgumentNullException(nameof(gateway));
        }

        /// <summary>执行单笔 register；password lease 只存在于当前调用栈。</summary>
        internal Task<ClientGatewayResult<ClientAuthentication>> RegisterAsync(
            string username,
            string password,
            string displayName,
            CancellationToken cancellationToken)
        {
            return _gateway.RegisterAsync(
                new ClientRegisterGatewayRequest(
                    username,
                    new ClientCredentialLease(
                        password,
                        ClientCredentialPurpose.RegisterPassword),
                    displayName),
                cancellationToken);
        }

        /// <summary>执行单笔 login；password lease 只存在于当前调用栈。</summary>
        internal Task<ClientGatewayResult<ClientAuthentication>> LoginAsync(
            string username,
            string password,
            CancellationToken cancellationToken)
        {
            return _gateway.LoginAsync(
                new ClientLoginGatewayRequest(
                    username,
                    new ClientCredentialLease(
                        password,
                        ClientCredentialPurpose.LoginPassword)),
                cancellationToken);
        }
    }

    /// <summary>执行 refresh gateway 调用，不拥有 single-flight 或提交。</summary>
    internal sealed class SessionRefreshFlow
    {
        /// <summary>保存固定 Session gateway port。</summary>
        private readonly IClientSessionGateway _gateway;

        /// <summary>创建 refresh flow。</summary>
        internal SessionRefreshFlow(IClientSessionGateway gateway)
        {
            _gateway = gateway ?? throw new ArgumentNullException(nameof(gateway));
        }

        /// <summary>使用单次 refresh credential lease 执行轮换候选。</summary>
        internal Task<ClientGatewayResult<ClientTokenPair>> ExecuteAsync(
            string refreshToken,
            CancellationToken cancellationToken)
        {
            return _gateway.RefreshAsync(
                new ClientCredentialGatewayRequest(
                    new ClientCredentialLease(
                        refreshToken,
                        ClientCredentialPurpose.RefreshSession)),
                cancellationToken);
        }
    }

    /// <summary>执行 secure restore 所需 refresh 候选，不提交 owner snapshot。</summary>
    internal sealed class SessionRestoreFlow
    {
        /// <summary>复用无状态 refresh flow。</summary>
        private readonly SessionRefreshFlow _refresh;

        /// <summary>创建 restore flow。</summary>
        internal SessionRestoreFlow(SessionRefreshFlow refresh)
        {
            _refresh = refresh ?? throw new ArgumentNullException(nameof(refresh));
        }

        /// <summary>轮换 secure record 中的 refresh lineage。</summary>
        internal Task<ClientGatewayResult<ClientTokenPair>> ExecuteAsync(
            ClientSecureSessionRecord record,
            CancellationToken cancellationToken)
        {
            if (record == null)
            {
                throw new ArgumentNullException(nameof(record));
            }

            return _refresh.ExecuteAsync(record.RefreshToken, cancellationToken);
        }
    }

    /// <summary>执行 logout gateway 调用，不决定 commit-unknown 终态。</summary>
    internal sealed class SessionTerminationFlow
    {
        /// <summary>保存固定 Session gateway port。</summary>
        private readonly IClientSessionGateway _gateway;

        /// <summary>创建 termination flow。</summary>
        internal SessionTerminationFlow(IClientSessionGateway gateway)
        {
            _gateway = gateway ?? throw new ArgumentNullException(nameof(gateway));
        }

        /// <summary>使用一次性 authorization lease 执行 logout。</summary>
        internal Task<ClientGatewayResult<ClientGatewayEmpty>> LogoutAsync(
            string accessToken,
            CancellationToken cancellationToken)
        {
            return _gateway.LogoutAsync(
                new ClientCredentialGatewayRequest(
                    new ClientCredentialLease(
                        accessToken,
                        ClientCredentialPurpose.HttpAuthorization)),
                cancellationToken);
        }
    }
}
