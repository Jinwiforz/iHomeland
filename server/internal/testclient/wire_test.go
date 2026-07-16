package testclient

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"

	commonv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/common/v1"
	worldv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/world/v1"
	"google.golang.org/protobuf/proto"
)

// TestGameplayPrefaceMatchesGolden 验证独立客户端编码与提交的 IHTP fixture 完全一致。
func TestGameplayPrefaceMatchesGolden(t *testing.T) {
	ticket := "0102030405060708090a0b0c0d0e0f10"
	admission := "wad1_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	frame, err := encodeGameplayPreface(ticket, admission, "OWN_WORLD")
	if err != nil {
		t.Fatalf("encode preface: %v", err)
	}
	want := "0000005b49485450000100200030013031303230333034303530363037303830393061306230633064306530663130776164315f41414141414141414141414141414141414141414141414141414141414141414141414141414141414141"
	if hex.EncodeToString(frame) != want {
		t.Fatalf("preface=%s want=%s", hex.EncodeToString(frame), want)
	}
}

// TestFrameHandlesPartialIOAndBounds 验证任意 read 切分不改变 frame，零长和超长先于分配被拒绝。
func TestFrameHandlesPartialIOAndBounds(t *testing.T) {
	frame, err := encodeFrame([]byte("payload"), 32)
	if err != nil {
		t.Fatalf("encode frame: %v", err)
	}
	reader := &oneByteReader{value: frame}
	payload, err := readFrame(reader, 32)
	if err != nil || string(payload) != "payload" {
		t.Fatalf("read partial frame payload=%q err=%v", payload, err)
	}
	for _, length := range []uint32{0, 33} {
		var prefix [4]byte
		binary.BigEndian.PutUint32(prefix[:], length)
		if _, err := readFrame(bytes.NewReader(prefix[:]), 32); !errors.Is(err, errWireProtocol) {
			t.Fatalf("length=%d error=%v", length, err)
		}
	}
}

// TestEnvelopeRejectsUnknownKindMessageSequenceAndOversize 验证 envelope 在 payload dispatch 前 fail closed。
func TestEnvelopeRejectsUnknownKindMessageSequenceAndOversize(t *testing.T) {
	kind := commonv1.MessageKind_MESSAGE_KIND_PUSH
	base := commonv1.ReliableEnvelope_builder{
		ProtocolVersion: proto.Uint32(1), MessageId: proto.Uint32(500), Kind: &kind,
		Sequence: proto.Uint64(1), TimestampMs: proto.Int64(1), Payload: []byte{},
	}.Build()
	encoded, _ := proto.Marshal(base)
	if _, err := decodeServerEnvelope(encoded, 2, true); err == nil {
		t.Fatal("wrong sequence was accepted")
	}
	base.SetMessageId(9999)
	encoded, _ = proto.Marshal(base)
	if _, err := decodeServerEnvelope(encoded, 1, true); err == nil {
		t.Fatal("unknown message was accepted")
	}
	unknown := commonv1.MessageKind(99)
	base.SetMessageId(500)
	base.SetKind(unknown)
	encoded, _ = proto.Marshal(base)
	if _, err := decodeServerEnvelope(encoded, 1, true); err == nil {
		t.Fatal("unknown kind was accepted")
	}
	if _, err := decodeServerEnvelope(make([]byte, maximumRealtimeFrameBytes+1), 1, true); err == nil {
		t.Fatal("oversize envelope was accepted")
	}
}

// TestClientEnvelopeRequiresExactPayloadAndCorrelation 验证 C2S message ID、kind 和 generated type 不能错配。
func TestClientEnvelopeRequiresExactPayloadAndCorrelation(t *testing.T) {
	correlation := bytes.Repeat([]byte{1}, 16)
	if _, err := encodeClientEnvelope(2000, new(worldv1.WorldSnapshotRequest), correlation, 1); err != nil {
		t.Fatalf("encode valid client envelope: %v", err)
	}
	if _, err := encodeClientEnvelope(9999, new(worldv1.WorldSnapshotRequest), correlation, 1); err == nil {
		t.Fatal("unknown client message was accepted")
	}
	if _, err := encodeClientEnvelope(2000, new(worldv1.WorldSnapshotResponse), correlation, 1); err == nil {
		t.Fatal("wrong generated payload was accepted")
	}
	if _, err := encodeClientEnvelope(2000, new(worldv1.WorldSnapshotRequest), correlation[:15], 1); err == nil {
		t.Fatal("short correlation was accepted")
	}
}

// TestCredentialGrammar 验证 ticket/admission 的大小写、长度、版本和字符集。
func TestCredentialGrammar(t *testing.T) {
	validTicket := strings.Repeat("a", 32)
	validAdmission := "wad1_" + strings.Repeat("A", 43)
	if !validTicketCredential(validTicket) || !validAdmissionCredential(validAdmission) {
		t.Fatal("valid credential grammar was rejected")
	}
	for _, ticket := range []string{strings.Repeat("A", 32), strings.Repeat("g", 32), strings.Repeat("a", 31)} {
		if validTicketCredential(ticket) {
			t.Fatalf("invalid ticket %q was accepted", ticket)
		}
	}
	for _, admission := range []string{"bad1_" + strings.Repeat("A", 43), "wad1_" + strings.Repeat("+", 43), "wad1_short"} {
		if validAdmissionCredential(admission) {
			t.Fatalf("invalid admission %q was accepted", admission)
		}
	}
}

// FuzzRealtimeEnvelope 验证任意 envelope bytes 只产生受控拒绝或完整已知消息。
func FuzzRealtimeEnvelope(f *testing.F) {
	f.Add([]byte{1}, uint64(1), true)
	f.Fuzz(func(t *testing.T, encoded []byte, sequence uint64, wss bool) {
		if sequence == 0 {
			sequence = 1
		}
		message, err := decodeServerEnvelope(encoded, sequence, wss)
		if err == nil && (message.MessageID == 0 || message.Payload == nil || message.Sequence != sequence) {
			t.Fatal("decoder returned incomplete message")
		}
	})
}

// FuzzGameplayPreface 验证任意 credential 与 purpose 不会产生越界或部分 preface。
func FuzzGameplayPreface(f *testing.F) {
	f.Add(strings.Repeat("a", 32), "wad1_"+strings.Repeat("A", 43), "OWN_WORLD")
	f.Fuzz(func(t *testing.T, ticket, admission, purpose string) {
		frame, err := encodeGameplayPreface(ticket, admission, purpose)
		if err == nil {
			if len(frame) != 95 || int(binary.BigEndian.Uint32(frame[:4])) != len(frame)-4 {
				t.Fatalf("invalid successful preface length=%d", len(frame))
			}
		}
	})
}

// oneByteReader 强制 readFrame 处理每次只有一个 byte 的 partial I/O。
type oneByteReader struct {
	// value 保存尚未读取的测试 bytes。
	value []byte
}

// Read 每次最多返回一个 byte。
func (reader *oneByteReader) Read(target []byte) (int, error) {
	if len(reader.value) == 0 {
		return 0, io.EOF
	}
	target[0] = reader.value[0]
	reader.value = reader.value[1:]
	return 1, nil
}
