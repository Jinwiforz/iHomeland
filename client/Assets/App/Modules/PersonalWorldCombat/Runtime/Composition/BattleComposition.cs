using System;
using IHomeland.Client.Core.Runtime.Composition;
using IHomeland.Client.Networking.Runtime.Composition;
using IHomeland.Client.PersonalWorld.Runtime.Composition;
using IHomeland.Client.PersonalWorldCombat.Application;
using IHomeland.Client.PersonalWorldCombat.Application.WorldIntegration;
using IHomeland.Client.PersonalWorld.Application;
using IHomeland.Client.PersonalWorldCombat.Infrastructure;
using IHomeland.Client.Networking.Infrastructure.Http;
using IHomeland.Client.Session.Runtime.Composition;

namespace IHomeland.Client.PersonalWorldCombat.Runtime.Composition
{
    /// <summary>
    /// 显式创建 client battle native、network 与纯 gameplay owners。
    /// </summary>
    internal static class BattleComposition
    {
        /// <summary>
        /// 在 world owner 已创建后建立无 service locator 的 battle runtime 对象图。
        /// </summary>
        internal static BattleCompositionBundle Create(
            FoundationCompositionBundle foundation,
            InfrastructureCompositionBundle infrastructure,
            SessionCompositionBundle session,
            WorldCompositionBundle world)
        {
            if (foundation == null ||
                infrastructure == null ||
                session == null ||
                world == null)
            {
                throw new ArgumentNullException(
                    "Battle module dependencies cannot be null.");
            }

            var targetSource = new ClientBattleWorldTargetSource(
                session.SessionCoordinator,
                world.Admission,
                world.PersonalWorld,
                world.VisitSession);
            var native = new ClientBattleNativeProvider();
            var ticketApi = new ClientBattleTicketHttpApi(
                infrastructure.Transport,
                new ClientBattleTicketCodec(),
                new ClientHttpContractMapper());
            var connectAttempt = new ClientBattleConnectAttempt(
                session.SessionCoordinator,
                ticketApi,
                new ClientBattleDatagramSocketFactory(),
                native);
            var inbound = new ClientBattleInboundRouter(
                foundation.Dispatcher);
            var network = new BattleNetworkClient(
                connectAttempt,
                new ClientBattleProtocolAdapter(),
                inbound);
            var prediction = new GameplayPrediction(
                ClientBattlePolicy.Current,
                network);
            var replica = new GameplayReplica(
                ClientBattlePolicy.Current,
                network);
            var interpolation = new GameplayInterpolation(
                ClientBattlePolicy.Current);
            var runtime = new ClientBattleRuntimeCoordinator(
                targetSource,
                network,
                replica,
                prediction,
                interpolation,
                foundation.Clock,
                world.Recovery);
            inbound.Bind(runtime);
            return new BattleCompositionBundle(
                targetSource,
                inbound,
                native,
                network,
                prediction,
                replica,
                interpolation,
                runtime);
        }
    }

    /// <summary>
    /// 封闭 battle module 的 lifecycle、tick 与 Scene 所需强类型入口。
    /// </summary>
    internal sealed class BattleCompositionBundle
    {
        /// <summary>创建完整且不可为空的 battle composition bundle。</summary>
        internal BattleCompositionBundle(
            ClientBattleWorldTargetSource targetSource,
            ClientBattleInboundRouter inbound,
            ClientBattleNativeProvider native,
            BattleNetworkClient network,
            GameplayPrediction prediction,
            GameplayReplica replica,
            GameplayInterpolation interpolation,
            ClientBattleRuntimeCoordinator runtime)
        {
            TargetSource = targetSource ??
                throw new ArgumentNullException(nameof(targetSource));
            Inbound = inbound ??
                throw new ArgumentNullException(nameof(inbound));
            Native = native ?? throw new ArgumentNullException(nameof(native));
            Network = network ?? throw new ArgumentNullException(nameof(network));
            Prediction = prediction ??
                throw new ArgumentNullException(nameof(prediction));
            Replica = replica ?? throw new ArgumentNullException(nameof(replica));
            Interpolation = interpolation ??
                throw new ArgumentNullException(nameof(interpolation));
            Runtime = runtime ?? throw new ArgumentNullException(nameof(runtime));
        }

        /// <summary>获取唯一 Session/world target adapter。</summary>
        internal ClientBattleWorldTargetSource TargetSource { get; }

        /// <summary>获取唯一有界main-thread inbound adapter。</summary>
        internal ClientBattleInboundRouter Inbound { get; }

        /// <summary>获取唯一 native primitive lifecycle owner。</summary>
        internal ClientBattleNativeProvider Native { get; }

        /// <summary>获取唯一 BattleNetworkClient connection owner。</summary>
        internal BattleNetworkClient Network { get; }

        /// <summary>获取唯一 input/prediction owner与主线程 tickable。</summary>
        internal GameplayPrediction Prediction { get; }

        /// <summary>获取唯一 authority replica owner。</summary>
        internal GameplayReplica Replica { get; }

        /// <summary>获取唯一 remote interpolation owner。</summary>
        internal GameplayInterpolation Interpolation { get; }

        /// <summary>获取唯一 battle runtime facade。</summary>
        internal ClientBattleRuntimeCoordinator Runtime { get; }
    }
}
