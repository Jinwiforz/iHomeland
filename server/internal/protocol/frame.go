package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// MaximumFrameSize 在 payload 进入 Protobuf 解码前限制内存分配与缓冲规模。
// 1 MiB 是可靠实时通道的全局硬上限；业务消息还必须满足 route registry 中更小的 MaxSize。
const MaximumFrameSize = 1 << 20

// FrameDecoder 跨任意 TCP read 边界增量重组带长度前缀的 frame。
//
// 每个连接必须独占一个 decoder，类型不是并发安全的。Push 返回的 payload 已复制，与内部
// buffer 不共享存储，因此调用方可以在下一次 Push 后继续持有或异步处理返回值。
type FrameDecoder struct {
	// buffer 保存尚不足以组成完整 frame 的连接私有字节，并在 Close 后必须为空。
	buffer []byte
}

// EncodeFrame 按 TLS/TCP 契约写入 unsigned big-endian payload 长度前缀。
//
// payload 只在调用期间读取，返回值拥有独立存储。空 payload 或超过 MaximumFrameSize 时不
// 分配 frame，调用方应把错误视为本地协议违规而不是可重试的网络错误。
func EncodeFrame(payload []byte) ([]byte, error) {
	if len(payload) == 0 {
		return nil, errors.New("frame payload is empty")
	}
	if len(payload) > MaximumFrameSize {
		return nil, fmt.Errorf("frame payload exceeds %d bytes", MaximumFrameSize)
	}
	frame := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(payload)))
	copy(frame[4:], payload)
	return frame, nil
}

// Push 接收一次 socket read 并返回全部完整 frame，不把 read 边界误认为消息边界。
//
// chunk 会复制进 decoder 私有缓冲区，调用方可在返回后复用。发生非法长度时 decoder 的状态
// 不保证可继续使用，连接 owner 应记录原因并关闭连接，防止攻击者反复驱动同一损坏流。
func (decoder *FrameDecoder) Push(chunk []byte) ([][]byte, error) {
	decoder.buffer = append(decoder.buffer, chunk...)
	frames := make([][]byte, 0)
	for len(decoder.buffer) >= 4 {
		length := binary.BigEndian.Uint32(decoder.buffer[:4])
		if length == 0 {
			return nil, errors.New("zero-length frame")
		}
		if length > MaximumFrameSize {
			return nil, fmt.Errorf("frame length %d exceeds %d", length, MaximumFrameSize)
		}
		frameLength := 4 + int(length)
		if len(decoder.buffer) < frameLength {
			break
		}
		// 返回独立副本，避免下一次 buffer 压缩或追加修改已经交给 dispatcher 的 payload。
		payload := append([]byte(nil), decoder.buffer[4:frameLength]...)
		frames = append(frames, payload)
		decoder.buffer = decoder.buffer[frameLength:]
	}
	if len(decoder.buffer) == 0 {
		// 完整消费后释放可能由大 frame 扩张的 backing array，避免连接空闲期长期保留峰值内存。
		decoder.buffer = nil
	}
	return frames, nil
}

// Close 验证流结束时没有残留被截断的长度前缀或 payload。
// Close 不释放外部资源，也不会清空残留；非 nil 错误应被连接 owner 记录为截断帧诊断。
func (decoder *FrameDecoder) Close() error {
	if len(decoder.buffer) != 0 {
		return fmt.Errorf("stream ended with %d truncated frame bytes", len(decoder.buffer))
	}
	return nil
}
