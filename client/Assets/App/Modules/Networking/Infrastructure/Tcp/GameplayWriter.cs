using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;

namespace IHomeland.Client.Networking.Infrastructure.Tcp
{
    /// <summary>
    /// 拥有单个 gameplay connection generation 的有界 FIFO frame queue 与 writer signal。
    /// </summary>
    internal sealed class GameplayWriter : IDisposable
    {
        /// <summary>保存 frame count hard cap。</summary>
        private readonly int _itemCapacity;

        /// <summary>保存 encoded byte hard cap。</summary>
        private readonly int _byteCapacity;

        /// <summary>保存顺序 frame queue。</summary>
        private readonly Queue<byte[]> _frames = new Queue<byte[]>();

        /// <summary>通知唯一 writer pump。</summary>
        private readonly SemaphoreSlim _signal = new SemaphoreSlim(0);

        /// <summary>保存 current queued bytes。</summary>
        private int _bytes;

        /// <summary>创建有界 writer queue。</summary>
        internal GameplayWriter(int itemCapacity, int byteCapacity)
        {
            if (itemCapacity <= 0 || byteCapacity <= 0)
            {
                throw new ArgumentOutOfRangeException(
                    itemCapacity <= 0 ? nameof(itemCapacity) : nameof(byteCapacity));
            }

            _itemCapacity = itemCapacity;
            _byteCapacity = byteCapacity;
        }

        /// <summary>判断 frame 是否能在不越界时入队。</summary>
        internal bool CanEnqueue(int frameBytes)
        {
            return frameBytes > 0 &&
                   _frames.Count < _itemCapacity &&
                   _bytes <= _byteCapacity - frameBytes;
        }

        /// <summary>在调用方同步边界内顺序入队并唤醒 pump。</summary>
        internal void Enqueue(byte[] frame)
        {
            if (frame == null)
            {
                throw new ArgumentNullException(nameof(frame));
            }

            if (!CanEnqueue(frame.Length))
            {
                throw new InvalidOperationException("Gameplay writer queue 已满。");
            }

            _frames.Enqueue(frame);
            _bytes += frame.Length;
            _signal.Release();
        }

        /// <summary>等待新 frame 或 generation cancellation。</summary>
        internal Task WaitAsync(CancellationToken cancellationToken)
        {
            return _signal.WaitAsync(cancellationToken);
        }

        /// <summary>在调用方同步边界内取得 FIFO 下一帧。</summary>
        internal bool TryDequeue(out byte[] frame)
        {
            if (_frames.Count == 0)
            {
                frame = null;
                return false;
            }

            frame = _frames.Dequeue();
            _bytes -= frame.Length;
            return true;
        }

        /// <summary>撤销 current generation 尚未写出的全部 frame。</summary>
        internal void Clear()
        {
            _frames.Clear();
            _bytes = 0;
        }

        /// <summary>释放 writer signal。</summary>
        public void Dispose()
        {
            _signal.Dispose();
        }
    }
}
