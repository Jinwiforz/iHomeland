using System;
using IHomeland.Client.Application.Configuration;
using UnityEngine;

namespace IHomeland.Client.Core.Configuration
{
    /// <summary>
    /// 保存 BootstrapScene 可序列化的非敏感客户端环境定义。
    /// </summary>
    /// <remarks>
    /// 该资产只提供部署配置，不保存账号、token、ticket 或任何在线事实。运行时在构造
    /// App Scope 前把字段复制到不可变 <see cref="ClientEnvironment"/>，业务代码不持有资产引用。
    /// </remarks>
    [CreateAssetMenu(fileName = "ClientEnvironment", menuName = "iHomeland/Client Environment")]
    public sealed class ClientEnvironmentProfile : ScriptableObject
    {
        /// <summary>
        /// 保存决定明文 loopback 例外是否可用的环境类别。
        /// </summary>
        [SerializeField]
        [Tooltip("Production 只允许 HTTPS；Local/Test 的 HTTP 仍必须指向 loopback。")]
        private ClientEnvironmentKind _environmentKind = ClientEnvironmentKind.Local;

        /// <summary>
        /// 保存公开 HTTP API 的部署 base URI，不包含业务 path、query、userinfo 或 secret。
        /// </summary>
        [SerializeField]
        [Tooltip("公开 HTTP API 根地址，例如本地 http://127.0.0.1:8080/。")]
        private string _httpBaseUri = string.Empty;

        /// <summary>
        /// 验证序列化配置并创建 App Scope 使用的不可变环境快照。
        /// </summary>
        /// <param name="clientVersion">来自 PlayerSettings/Application 的当前客户端版本。</param>
        /// <param name="protocolVersion">来自冻结 contract baseline 的正整数协议版本。</param>
        /// <returns>不再引用当前 ScriptableObject 的已验证环境快照。</returns>
        /// <exception cref="InvalidOperationException">环境类别、URI 或 build identity 不符合安全约束时抛出。</exception>
        internal ClientEnvironment Build(string clientVersion, int protocolVersion)
        {
            return ClientEnvironment.Create(
                _environmentKind,
                _httpBaseUri,
                clientVersion,
                protocolVersion);
        }
    }
}
