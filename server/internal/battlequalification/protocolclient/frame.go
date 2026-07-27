package protocolclient

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
)

const (
	// framePrefixBytes 是 uint32 big-endian body length。
	framePrefixBytes = 4
	// frameHeaderBytes 是 IHBQ closed envelope 宽度。
	frameHeaderBytes = 28
	// maximumFrameBytes 是 prefix 声明的完整 body hard cap。
	maximumFrameBytes = 65_536
	// contractVersion 是 battle-protocol-client-v1 的 numeric version。
	contractVersion = 1
)

var (
	// ErrInvalidFrame 表示 magic/version/kind/flags/sequence/deadline/length 不合法。
	ErrInvalidFrame = errors.New("battle protocol client frame is invalid")
	// ErrUnexpectedReceipt 表示 child 返回错误 kind 或 correlation sequence。
	ErrUnexpectedReceipt = errors.New("battle protocol client receipt is unexpected")
)

// Kind 是 qualification client stdio 的 closed message kind。
type Kind uint8

const (
	// KindDescribeRequest 请求 exact binary/contract identity。
	KindDescribeRequest Kind = 1
	// KindShutdownRequest 请求 child 有界关闭。
	KindShutdownRequest Kind = 2
	// KindSessionStartRequest 一次性交付 UDP endpoint 与 ticket credential。
	KindSessionStartRequest Kind = 3
	// KindWorkloadCommandRequest 驱动 typed input/probe/resync。
	KindWorkloadCommandRequest Kind = 4
	// KindNetworkTransitionRequest 驱动 rebind/rekey/close。
	KindNetworkTransitionRequest Kind = 5
	// KindPollRequest 请求当前低敏事件批次。
	KindPollRequest Kind = 6
	// KindHandshakeAttackRequest 一次性交付 ticket 并执行 closed handshake 负例。
	KindHandshakeAttackRequest Kind = 7
	// KindDescribeReceipt 返回 binary/contract identity。
	KindDescribeReceipt Kind = 129
	// KindShutdownReceipt 确认 child cleanup 完成。
	KindShutdownReceipt Kind = 130
	// KindSessionEvent 返回 handshake/session 低敏状态。
	KindSessionEvent Kind = 131
	// KindWorkloadEvent 返回 Tick/snapshot/reliable event。
	KindWorkloadEvent Kind = 132
	// KindNetworkTransitionEvent 返回 rebind/rekey/close 结果。
	KindNetworkTransitionEvent Kind = 133
	// KindPollReceipt 返回有界 counters/event batch。
	KindPollReceipt Kind = 134
	// KindHandshakeAttackReceipt 返回不含 wire/credential 的攻击计数。
	KindHandshakeAttackReceipt Kind = 135
)

// requestKind 报告 kind 是否允许 supervisor 写入 child。
func requestKind(kind Kind) bool {
	return kind >= KindDescribeRequest && kind <= KindHandshakeAttackRequest
}

// receiptKind 报告 kind 是否允许 child 写入 supervisor。
func receiptKind(kind Kind) bool {
	return kind >= KindDescribeReceipt && kind <= KindHandshakeAttackReceipt
}

// Frame 是已经通过 outer envelope 校验的 owned body。
type Frame struct {
	// Kind 是 closed request/receipt 类型。
	Kind Kind
	// Sequence 是单进程 session 内严格递增 correlation。
	Sequence uint64
	// Deadline 是 request 的绝对 UTC deadline；receipt 必须为零。
	Deadline time.Time
	// Payload 是 caller-owned mutable bytes，secret 使用后必须 clear。
	Payload []byte
}

// writeFrame 编码一个 request；deadline 等于或早于 now 时不执行写入。
func writeFrame(writer io.Writer, frame Frame, now time.Time) error {
	if writer == nil || !requestKind(frame.Kind) ||
		frame.Sequence == 0 || frame.Deadline.IsZero() ||
		!frame.Deadline.After(now) ||
		len(frame.Payload) > maximumFrameBytes-frameHeaderBytes {
		return ErrInvalidFrame
	}
	body := make([]byte, frameHeaderBytes+len(frame.Payload))
	defer clear(body)
	copy(body, "IHBQ")
	body[4] = contractVersion
	body[5] = byte(frame.Kind)
	binary.BigEndian.PutUint64(body[8:16], frame.Sequence)
	deadlineMilliseconds := frame.Deadline.UnixMilli()
	if deadlineMilliseconds <= 0 {
		return ErrInvalidFrame
	}
	binary.BigEndian.PutUint64(body[16:24], uint64(deadlineMilliseconds))
	binary.BigEndian.PutUint32(body[24:28], uint32(len(frame.Payload)))
	copy(body[frameHeaderBytes:], frame.Payload)
	var prefix [framePrefixBytes]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(body)))
	if err := writeExact(writer, prefix[:]); err != nil {
		return err
	}
	return writeExact(writer, body)
}

// readFrame 读取一个 receipt 并拒绝 request kind、非零 deadline 和 trailing drift。
func readFrame(reader io.Reader) (Frame, error) {
	if reader == nil {
		return Frame{}, ErrInvalidFrame
	}
	var prefix [framePrefixBytes]byte
	if _, err := io.ReadFull(reader, prefix[:]); err != nil {
		return Frame{}, err
	}
	length := binary.BigEndian.Uint32(prefix[:])
	if length < frameHeaderBytes || length > maximumFrameBytes {
		return Frame{}, ErrInvalidFrame
	}
	body := make([]byte, length)
	defer clear(body)
	if _, err := io.ReadFull(reader, body); err != nil {
		return Frame{}, err
	}
	kind := Kind(body[5])
	payloadLength := binary.BigEndian.Uint32(body[24:28])
	if string(body[:4]) != "IHBQ" ||
		body[4] != contractVersion ||
		!receiptKind(kind) ||
		body[6] != 0 || body[7] != 0 ||
		binary.BigEndian.Uint64(body[8:16]) == 0 ||
		binary.BigEndian.Uint64(body[16:24]) != 0 ||
		uint64(payloadLength)+frameHeaderBytes != uint64(length) {
		return Frame{}, ErrInvalidFrame
	}
	return Frame{
		Kind:     kind,
		Sequence: binary.BigEndian.Uint64(body[8:16]),
		Payload:  append([]byte(nil), body[frameHeaderBytes:]...),
	}, nil
}

// writeReceipt 只用于 contract tests 的独立 child fixture。
func writeReceipt(writer io.Writer, kind Kind, sequence uint64, payload []byte) error {
	if writer == nil || !receiptKind(kind) || sequence == 0 ||
		len(payload) > maximumFrameBytes-frameHeaderBytes {
		return ErrInvalidFrame
	}
	body := make([]byte, frameHeaderBytes+len(payload))
	defer clear(body)
	copy(body, "IHBQ")
	body[4] = contractVersion
	body[5] = byte(kind)
	binary.BigEndian.PutUint64(body[8:16], sequence)
	binary.BigEndian.PutUint32(body[24:28], uint32(len(payload)))
	copy(body[frameHeaderBytes:], payload)
	var prefix [framePrefixBytes]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(body)))
	if err := writeExact(writer, prefix[:]); err != nil {
		return err
	}
	return writeExact(writer, body)
}

// readRequest 只用于 contract tests 的独立 child fixture。
func readRequest(reader io.Reader, now time.Time) (Frame, error) {
	var prefix [framePrefixBytes]byte
	if _, err := io.ReadFull(reader, prefix[:]); err != nil {
		return Frame{}, err
	}
	length := binary.BigEndian.Uint32(prefix[:])
	if length < frameHeaderBytes || length > maximumFrameBytes {
		return Frame{}, ErrInvalidFrame
	}
	body := make([]byte, length)
	defer clear(body)
	if _, err := io.ReadFull(reader, body); err != nil {
		return Frame{}, err
	}
	kind := Kind(body[5])
	deadlineMilliseconds := binary.BigEndian.Uint64(body[16:24])
	payloadLength := binary.BigEndian.Uint32(body[24:28])
	if string(body[:4]) != "IHBQ" || body[4] != contractVersion ||
		!requestKind(kind) || body[6] != 0 || body[7] != 0 ||
		binary.BigEndian.Uint64(body[8:16]) == 0 ||
		deadlineMilliseconds == 0 ||
		uint64(payloadLength)+frameHeaderBytes != uint64(length) {
		return Frame{}, ErrInvalidFrame
	}
	deadline := time.UnixMilli(int64(deadlineMilliseconds))
	if !deadline.After(now) {
		return Frame{}, ErrInvalidFrame
	}
	return Frame{
		Kind:     kind,
		Sequence: binary.BigEndian.Uint64(body[8:16]),
		Deadline: deadline,
		Payload:  append([]byte(nil), body[frameHeaderBytes:]...),
	}, nil
}

// writeExact 拒绝 writer 的无错误短写。
func writeExact(writer io.Writer, payload []byte) error {
	for len(payload) > 0 {
		written, err := writer.Write(payload)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(payload) {
			return fmt.Errorf("%w: short write", ErrInvalidFrame)
		}
		payload = payload[written:]
	}
	return nil
}
