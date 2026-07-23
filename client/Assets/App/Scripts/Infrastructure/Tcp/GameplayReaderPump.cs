using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Application.Configuration;
using IHomeland.Client.Application.Gameplay;
using IHomeland.Protocol.Common.V1;

namespace IHomeland.Client.Infrastructure.Tcp
{
    /// <summary>
    /// 拥有单个 Gameplay generation 的 frame cap、decode 与 S2C sequence cursor。
    /// </summary>
    internal sealed class GameplayReaderPump
    {
        /// <summary>提供冻结 realtime frame cap。</summary>
        private readonly ClientConfigurationStore _configurationStore;

        /// <summary>严格解码 Gameplay envelope。</summary>
        private readonly ClientGameplayCodec _codec;

        /// <summary>创建无连接生命周期所有权的 reader。</summary>
        internal GameplayReaderPump(
            ClientConfigurationStore configurationStore,
            ClientGameplayCodec codec)
        {
            _configurationStore = configurationStore ??
                throw new ArgumentNullException(nameof(configurationStore));
            _codec = codec ?? throw new ArgumentNullException(nameof(codec));
        }

        /// <summary>读取、解码并把 response/PUSH 交给唯一 route dispatcher。</summary>
        internal async Task RunAsync(
            long generation,
            Func<long, IClientGameplayConnection> getConnection,
            Action<long, ClientGameplayEnvelope> dispatch,
            Action<long, ClientGameplayDiagnosticStage, ClientGameplayCloseReason, Exception>
                recordDiagnostic,
            Action<long, ClientGameplayCloseReason> close,
            CancellationToken cancellationToken)
        {
            if (getConnection == null || dispatch == null ||
                recordDiagnostic == null || close == null)
            {
                throw new ArgumentNullException("Gameplay reader callback 不能为空。");
            }

            ulong expectedSequence = 1;
            try
            {
                while (!cancellationToken.IsCancellationRequested)
                {
                    if (!_configurationStore.TryGetCurrent(out var configuration))
                    {
                        throw new ClientGameplayProtocolException(
                            "Gameplay config 不可用。");
                    }

                    var maximumFrameBytes = Math.Min(
                        configuration.Configuration.Limits.RealtimeFrameBytes,
                        ClientGameplayFramer.MaximumFrameBytes);
                    var body = await ClientGameplayFramer.ReadFrameAsync(
                        getConnection(generation),
                        maximumFrameBytes,
                        cancellationToken);
                    var envelope = _codec.Decode(body, expectedSequence);
                    expectedSequence++;
                    dispatch(generation, envelope);
                }
            }
            catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
            {
            }
            catch (ClientGameplayProtocolException exception)
            {
                recordDiagnostic(
                    generation,
                    ClientGameplayDiagnosticStage.Reader,
                    ClientGameplayCloseReason.Protocol,
                    exception);
                close(generation, ClientGameplayCloseReason.Protocol);
            }
            catch (System.IO.EndOfStreamException exception)
            {
                recordDiagnostic(
                    generation,
                    ClientGameplayDiagnosticStage.Reader,
                    ClientGameplayCloseReason.Remote,
                    exception);
                close(generation, ClientGameplayCloseReason.Remote);
            }
            catch (Exception exception)
            {
                recordDiagnostic(
                    generation,
                    ClientGameplayDiagnosticStage.Reader,
                    ClientGameplayCloseReason.Transport,
                    exception);
                close(generation, ClientGameplayCloseReason.Transport);
            }
        }
    }
}
