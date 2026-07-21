using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using IHomeland.Client.Presentation.Navigation;

namespace IHomeland.Client.Presentation.PersonalWorld
{
    /// <summary>
    /// 提供首个个人世界竖切唯一允许登记的 production route definitions。
    /// </summary>
    internal static class ClientPersonalWorldUiRoutes
    {
        /// <summary>保存构造时已完成 enum 验证的稳定只读 definitions。</summary>
        private static readonly ReadOnlyCollection<ClientUiRouteDefinition> DefinitionsValue =
            new ReadOnlyCollection<ClientUiRouteDefinition>(new[]
            {
                new ClientUiRouteDefinition(
                    ClientUiRouteId.Login,
                    ClientUiFrameworkOwner.UiToolkit,
                    ClientUiLayer.Screen,
                    ClientUiInputMode.Text,
                    ClientUiLifecycle.Cached),
                new ClientUiRouteDefinition(
                    ClientUiRouteId.Shell,
                    ClientUiFrameworkOwner.UiToolkit,
                    ClientUiLayer.Screen,
                    ClientUiInputMode.Ui,
                    ClientUiLifecycle.Cached),
                new ClientUiRouteDefinition(
                    ClientUiRouteId.WorldVisit,
                    ClientUiFrameworkOwner.UiToolkit,
                    ClientUiLayer.Overlay,
                    ClientUiInputMode.Ui,
                    ClientUiLifecycle.Cached),
                new ClientUiRouteDefinition(
                    ClientUiRouteId.WorldHud,
                    ClientUiFrameworkOwner.Ugui,
                    ClientUiLayer.Hud,
                    ClientUiInputMode.Gameplay,
                    ClientUiLifecycle.SceneBound),
                new ClientUiRouteDefinition(
                    ClientUiRouteId.ConnectionLost,
                    ClientUiFrameworkOwner.UiToolkit,
                    ClientUiLayer.Modal,
                    ClientUiInputMode.Modal,
                    ClientUiLifecycle.Recreate),
            });

        /// <summary>
        /// 获取稳定顺序的 production route definitions；`Settings` 未交付且不会出现在集合中。
        /// </summary>
        internal static IReadOnlyList<ClientUiRouteDefinition> Definitions => DefinitionsValue;

        /// <summary>
        /// 检查直接 Host 集合与全部 production definitions 一一对应。
        /// </summary>
        /// <param name="hosts">BootstrapScene 直接序列化引用的 Host 快照。</param>
        /// <exception cref="ArgumentException">缺失、重复、多余或 framework 错配时抛出。</exception>
        /// <exception cref="ArgumentNullException">集合或元素为空时抛出。</exception>
        internal static void ValidateHosts(IReadOnlyList<IClientUiViewHost> hosts)
        {
            _ = new ClientUiRegistry(DefinitionsValue, hosts ?? throw new ArgumentNullException(nameof(hosts)));
        }
    }
}
