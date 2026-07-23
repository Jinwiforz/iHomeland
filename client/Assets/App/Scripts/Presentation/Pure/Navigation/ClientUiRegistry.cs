using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;

namespace IHomeland.Client.Presentation.Navigation
{
    /// <summary>
    /// 在任何 Runtime Host/Input 副作用前冻结并验证 route definition 与显式 Host 一一绑定。
    /// </summary>
    internal sealed class ClientUiRegistry
    {
        /// <summary>按 route identity 保存防御性复制后的 definitions。</summary>
        private readonly Dictionary<ClientUiRouteId, ClientUiRouteDefinition> _definitions;

        /// <summary>按 route identity 保存唯一显式 Host。</summary>
        private readonly Dictionary<ClientUiRouteId, IClientUiViewHost> _hosts;

        /// <summary>保存稳定顺序的只读 definition 快照。</summary>
        private readonly ReadOnlyCollection<ClientUiRouteDefinition> _definitionSnapshot;

        /// <summary>
        /// 创建完整冻结 registry；空 definitions/hosts 只用于 isolated fixture。
        /// </summary>
        /// <param name="definitions">完整 route definitions。</param>
        /// <param name="hosts">BootstrapScene 显式登记的完整 Host 集合。</param>
        /// <exception cref="ArgumentException">重复、缺失或 framework 错配时抛出。</exception>
        /// <exception cref="ArgumentNullException">集合或元素为空时抛出。</exception>
        internal ClientUiRegistry(
            IReadOnlyList<ClientUiRouteDefinition> definitions,
            IReadOnlyList<IClientUiViewHost> hosts)
        {
            if (definitions == null)
            {
                throw new ArgumentNullException(nameof(definitions));
            }

            if (hosts == null)
            {
                throw new ArgumentNullException(nameof(hosts));
            }

            _definitions = new Dictionary<ClientUiRouteId, ClientUiRouteDefinition>(definitions.Count);
            var definitionCopy = new ClientUiRouteDefinition[definitions.Count];
            for (var index = 0; index < definitions.Count; index++)
            {
                var source = definitions[index] ??
                    throw new ArgumentNullException(nameof(definitions), $"UI definition 索引 {index} 不能为空。");
                var copy = new ClientUiRouteDefinition(
                    source.RouteId,
                    source.FrameworkOwner,
                    source.Layer,
                    source.InputMode,
                    source.Lifecycle);
                if (!_definitions.TryAdd(copy.RouteId, copy))
                {
                    throw new ArgumentException($"UI route {copy.RouteId} 被重复登记。", nameof(definitions));
                }

                definitionCopy[index] = copy;
            }

            _hosts = new Dictionary<ClientUiRouteId, IClientUiViewHost>(hosts.Count);
            var hostInstances = new HashSet<IClientUiViewHost>(ReferenceEqualityComparer<IClientUiViewHost>.Instance);
            for (var index = 0; index < hosts.Count; index++)
            {
                var host = hosts[index] ??
                    throw new ArgumentNullException(nameof(hosts), $"UI Host 索引 {index} 不能为空。");
                if (!Enum.IsDefined(typeof(ClientUiRouteId), host.RouteId) ||
                    host.RouteId == ClientUiRouteId.None)
                {
                    throw new ArgumentException(
                        $"UI Host 索引 {index} 的 route identity 未登记。",
                        nameof(hosts));
                }

                if (!hostInstances.Add(host))
                {
                    throw new ArgumentException($"同一 UI Host 实例不能绑定多个 route：{host.RouteId}。", nameof(hosts));
                }

                if (!_hosts.TryAdd(host.RouteId, host))
                {
                    throw new ArgumentException($"UI route {host.RouteId} 存在多个 Host。", nameof(hosts));
                }
            }

            foreach (var pair in _definitions)
            {
                if (!_hosts.TryGetValue(pair.Key, out var host))
                {
                    throw new ArgumentException($"UI route {pair.Key} 缺少显式 Host。", nameof(hosts));
                }

                if (host.FrameworkOwner != pair.Value.FrameworkOwner)
                {
                    throw new ArgumentException(
                        $"UI route {pair.Key} 的 Host framework 与 definition 不一致。",
                        nameof(hosts));
                }
            }

            foreach (var pair in _hosts)
            {
                if (!_definitions.ContainsKey(pair.Key))
                {
                    throw new ArgumentException($"UI Host {pair.Key} 缺少 route definition。", nameof(hosts));
                }
            }

            _definitionSnapshot = new ReadOnlyCollection<ClientUiRouteDefinition>(definitionCopy);
        }

        /// <summary>获取构造时冻结的稳定 definition 快照。</summary>
        internal IReadOnlyList<ClientUiRouteDefinition> Definitions => _definitionSnapshot;

        /// <summary>
        /// 尝试取得已冻结 route definition。
        /// </summary>
        /// <param name="routeId">封闭 route identity。</param>
        /// <param name="definition">找到时返回不可变 definition。</param>
        /// <returns>route 已登记时返回 true。</returns>
        internal bool TryGetDefinition(ClientUiRouteId routeId, out ClientUiRouteDefinition definition)
        {
            return _definitions.TryGetValue(routeId, out definition);
        }

        /// <summary>
        /// 尝试取得与 definition 一一绑定的显式 Host。
        /// </summary>
        /// <param name="routeId">封闭 route identity。</param>
        /// <param name="host">找到时返回唯一 Host。</param>
        /// <returns>route 已登记时返回 true。</returns>
        internal bool TryGetHost(ClientUiRouteId routeId, out IClientUiViewHost host)
        {
            return _hosts.TryGetValue(routeId, out host);
        }

        /// <summary>
        /// 以引用身份而非可变 equality 比较 Host，阻止同一实例重复绑定。
        /// </summary>
        /// <typeparam name="T">引用类型。</typeparam>
        private sealed class ReferenceEqualityComparer<T> : IEqualityComparer<T>
            where T : class
        {
            /// <summary>获取当前封闭泛型的单例 comparer。</summary>
            internal static ReferenceEqualityComparer<T> Instance { get; } = new ReferenceEqualityComparer<T>();

            /// <summary>
            /// 判断两个引用是否为同一对象。
            /// </summary>
            /// <param name="left">左引用。</param>
            /// <param name="right">右引用。</param>
            /// <returns>引用相同时返回 true。</returns>
            public bool Equals(T left, T right)
            {
                return ReferenceEquals(left, right);
            }

            /// <summary>
            /// 获取不受对象自定义 equality 影响的引用哈希。
            /// </summary>
            /// <param name="value">非空对象引用。</param>
            /// <returns>运行时引用哈希。</returns>
            public int GetHashCode(T value)
            {
                return System.Runtime.CompilerServices.RuntimeHelpers.GetHashCode(value);
            }
        }
    }
}
