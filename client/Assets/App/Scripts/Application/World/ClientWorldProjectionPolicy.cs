using System;
using System.Text;
using IHomeland.Client.Application.Contracts;

namespace IHomeland.Client.Application.World
{
    /// <summary>
    /// 表示公开投影违反客户端冻结合同。
    /// </summary>
    internal sealed class ClientWorldProjectionException : Exception
    {
        /// <summary>创建不携带 payload、endpoint 或 credential 的稳定校验异常。</summary>
        /// <param name="message">低敏合同失败说明。</param>
        internal ClientWorldProjectionException(string message)
            : base(message)
        {
        }
    }

    /// <summary>
    /// 提供无 generated、无 transport 的 world projection 结构校验与 HTTP candidate 转换。
    /// </summary>
    internal static class ClientWorldProjectionPolicy
    {
        /// <summary>协议 identity 的最大 UTF-8 字节数。</summary>
        private const int IdentityMaximumBytes = 128;

        /// <summary>公开 endpoint host 的最大 UTF-8 字节数。</summary>
        private const int HostMaximumBytes = 253;

        /// <summary>将已映射的 own-world bootstrap 转换为完整 primary world 投影。</summary>
        /// <param name="bootstrap">Session gateway 已验证的 bootstrap。</param>
        /// <returns>完整不可变 PersonalWorld 投影。</returns>
        internal static ClientPersonalWorldProjection FromBootstrap(ClientWorldBootstrap bootstrap)
        {
            if (bootstrap?.World == null)
            {
                throw Invalid("World bootstrap 缺少 world。");
            }

            var world = bootstrap.World;
            var worldID = RequireIdentity(world.PersonalWorldID, "PersonalWorldID");
            var ownerID = RequireIdentity(world.OwnerPlayerID, "OwnerPlayerID");
            var lifecycle = world.Lifecycle == ClientPersonalWorldLifecycle.Active
                ? ClientWorldLifecycle.Active
                : world.Lifecycle == ClientPersonalWorldLifecycle.Archived
                    ? ClientWorldLifecycle.Archived
                    : throw Invalid("World lifecycle 未登记。");
            var revision = RequirePositive(world.Revision, "world revision");
            RequirePositiveTimestamp(world.CreatedAtMilliseconds, "world createdAt");
            var assignment = bootstrap.Assignment == null
                ? null
                : FromAssignment(bootstrap.Assignment, worldID);
            if (lifecycle == ClientWorldLifecycle.Archived && assignment != null)
            {
                throw Invalid("Archived PersonalWorld 不能携带 active assignment。");
            }

            return new ClientPersonalWorldProjection(
                worldID,
                ownerID,
                lifecycle,
                revision,
                world.CreatedAtMilliseconds,
                assignment);
        }

        /// <summary>判断公开 identity 是否符合非空、长度与安全 ASCII 字符集约束。</summary>
        /// <param name="value">待验证 identity。</param>
        /// <returns>Identity 可安全进入 protocol command 或投影时返回 true。</returns>
        internal static bool IsValidIdentity(string value)
        {
            if (string.IsNullOrEmpty(value) ||
                Encoding.UTF8.GetByteCount(value) > IdentityMaximumBytes)
            {
                return false;
            }

            foreach (var character in value)
            {
                var valid = character >= 'a' && character <= 'z' ||
                            character >= 'A' && character <= 'Z' ||
                            character >= '0' && character <= '9' ||
                            character == '.' || character == '_' ||
                            character == ':' || character == '-';
                if (!valid)
                {
                    return false;
                }
            }

            return true;
        }

        /// <summary>把 gateway assignment 转换为统一 Application projection。</summary>
        private static ClientWorldAssignmentProjection FromAssignment(
            ClientWorldAssignment assignment,
            string expectedWorldID)
        {
            var worldID = RequireIdentity(
                assignment.PersonalWorldID,
                "Assignment PersonalWorldID");
            if (!string.Equals(worldID, expectedWorldID, StringComparison.Ordinal) ||
                assignment.Endpoint == null ||
                assignment.Endpoint.Channel != ClientEndpointChannel.TlsTcp)
            {
                throw Invalid("Gateway assignment binding 或 channel 无效。");
            }

            return new ClientWorldAssignmentProjection(
                worldID,
                RequireIdentity(assignment.WorldInstanceID, "WorldInstanceID"),
                MapEndpoint(assignment.Endpoint.Host, assignment.Endpoint.Port),
                RequirePositive(assignment.Generation, "assignment generation"),
                RequirePositiveTimestamp(
                    assignment.LeaseExpiresAtMilliseconds,
                    "assignment lease"));
        }

        /// <summary>验证 host/port 并创建不可变 endpoint。</summary>
        private static ClientWorldEndpointProjection MapEndpoint(string host, int port)
        {
            if (string.IsNullOrWhiteSpace(host) ||
                Encoding.UTF8.GetByteCount(host) > HostMaximumBytes ||
                port < 1 || port > 65535)
            {
                throw Invalid("World endpoint 无效。");
            }

            return new ClientWorldEndpointProjection(host, port);
        }

        /// <summary>验证公开 identity 并返回原值。</summary>
        private static string RequireIdentity(string value, string name)
        {
            if (!IsValidIdentity(value))
            {
                throw Invalid(name + " 无效。");
            }

            return value;
        }

        /// <summary>验证 signed long 正数并安全转换为 ulong。</summary>
        private static ulong RequirePositive(long value, string name)
        {
            if (value <= 0)
            {
                throw Invalid(name + " 必须为正数。");
            }

            return checked((ulong)value);
        }

        /// <summary>验证 Unix millisecond timestamp 为正数。</summary>
        private static long RequirePositiveTimestamp(long value, string name)
        {
            if (value <= 0)
            {
                throw Invalid(name + " 必须为正数。");
            }

            return value;
        }

        /// <summary>创建统一低敏 projection exception。</summary>
        private static ClientWorldProjectionException Invalid(string message)
        {
            return new ClientWorldProjectionException(message);
        }
    }
}
