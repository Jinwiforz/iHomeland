package protocol

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// TestFrameDecoderReadBoundaries 验证半帧、粘包与连续帧共享同一 decoder 行为。
// 第一次 Push 故意只提供三字节前缀，第二次同时补齐两个 frame，覆盖 TCP 任意切分与合并。
func TestFrameDecoderReadBoundaries(t *testing.T) {
	first, err := EncodeFrame([]byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := EncodeFrame([]byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	decoder := new(FrameDecoder)
	frames, err := decoder.Push(first[:3])
	if err != nil || len(frames) != 0 {
		t.Fatalf("partial prefix should wait: frames=%d err=%v", len(frames), err)
	}
	frames, err = decoder.Push(append(first[3:], second...))
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 || !bytes.Equal(frames[0], []byte("first")) || !bytes.Equal(frames[1], []byte("second")) {
		t.Fatalf("unexpected decoded frames: %q", frames)
	}
	if err := decoder.Close(); err != nil {
		t.Fatal(err)
	}
	if decoder.buffer != nil {
		t.Fatal("fully consumed decoder should release its backing buffer")
	}
}

// TestFrameDecoderRejectsBounds 验证零长度、超长与截断 frame 在分配或 dispatch 前失败。
// 超长用例只构造四字节前缀，不按攻击者声明的长度分配内存，从而同时保护测试本身的安全性。
func TestFrameDecoderRejectsBounds(t *testing.T) {
	tests := []struct {
		// name 标识拒绝边界，作为 table-driven 子测试名称。
		name string
		// input 只包含触发长度校验所需的最小字节，不代表完整合法 frame。
		input []byte
	}{
		{name: "zero", input: []byte{0, 0, 0, 0}},
		{name: "oversized", input: uint32Bytes(MaximumFrameSize + 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decoder := new(FrameDecoder)
			if _, err := decoder.Push(test.input); err == nil {
				t.Fatal("expected frame rejection")
			}
		})
	}
	decoder := new(FrameDecoder)
	valid, err := EncodeFrame([]byte("truncated"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decoder.Push(valid[:len(valid)-1]); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Close(); err == nil {
		t.Fatal("expected truncated frame rejection")
	}
}

// TestFrameMaximumSize 验证硬上限本身可接受，而多一个字节即被拒绝。
// 该边界防止实现把“最大长度”误写成排他的上限并破坏已登记的合法消息。
func TestFrameMaximumSize(t *testing.T) {
	if _, err := EncodeFrame(make([]byte, MaximumFrameSize)); err != nil {
		t.Fatalf("exact limit should pass: %v", err)
	}
	if _, err := EncodeFrame(make([]byte, MaximumFrameSize+1)); err == nil {
		t.Fatal("payload above limit should fail")
	}
}

// uint32Bytes 创建长度前缀但不分配其声明的 payload，用于安全测试超长输入。
// 返回 slice 由调用方独占，采用网络字节序以匹配真实 TLS/TCP frame。
func uint32Bytes(value uint32) []byte {
	encoded := make([]byte, 4)
	binary.BigEndian.PutUint32(encoded, value)
	return encoded
}
