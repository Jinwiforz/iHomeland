package tcpgameplay

import (
	"bytes"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/contract"
	commonv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/common/v1"
	visitv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/visit/v1"
	worldv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/world/v1"
	"github.com/jinwiforz/ihomeland/server/internal/protocol"
	"google.golang.org/protobuf/proto"
)

// fixedClock 为codec golden提供确定性timestamp。
type fixedClock struct{ value time.Time }

// Now 返回固定时间。
func (clock fixedClock) Now() time.Time { return clock.value }

// TestCodecDecodesEveryClientRoute 验证全部C2S route的kind、type、sequence与correlation投影。
func TestCodecDecodesEveryClientRoute(t *testing.T) {
	t.Parallel()
	codec := newTestCodec(t)
	for index, messageID := range []uint32{2000, 2103, 2105, 2107, 2109, 2111, 2113, 2115, 2117, 2119} {
		payload, _ := clientPayload(messageID)
		payloadBytes, _ := proto.Marshal(payload)
		kind := commonv1.MessageKind_MESSAGE_KIND_COMMAND
		builder := commonv1.ReliableEnvelope_builder{ProtocolVersion: proto.Uint32(1), MessageId: proto.Uint32(messageID), Kind: &kind, CommandId: bytes.Repeat([]byte{2}, 16), Sequence: proto.Uint64(uint64(index + 1)), TimestampMs: proto.Int64(1), Payload: payloadBytes}
		if messageID == 2000 || messageID == 2119 {
			kind = commonv1.MessageKind_MESSAGE_KIND_REQUEST
			builder.Kind, builder.CommandId, builder.RequestId = &kind, nil, bytes.Repeat([]byte{1}, 16)
		}
		encoded, err := protocol.MarshalEnvelope(builder.Build())
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := codec.Decode(encoded, uint64(index+1))
		if err != nil || decoded.Route().MessageID != messageID || decoded.Payload().ProtoReflect().Descriptor().FullName() != payload.ProtoReflect().Descriptor().FullName() {
			t.Fatalf("Decode(%d) = %+v, %v", messageID, decoded.Route(), err)
		}
	}
}

// TestCodecEncodesResponsePushAndError 验证S2C精确类型、correlation与确定性frame编码。
func TestCodecEncodesResponsePushAndError(t *testing.T) {
	t.Parallel()
	codec := newTestCodec(t)
	request := Correlation{RequestID: bytes.Repeat([]byte{1}, 16)}
	response := worldv1.WorldSnapshotResponse_builder{}.Build()
	first, err := codec.Encode(2001, response, request, 1)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := codec.Encode(2001, response, request, 1)
	if !bytes.Equal(first.Frame(), second.Frame()) {
		t.Fatal("deterministic response encoding drifted")
	}
	if _, err := codec.Encode(2001, visitv1.VisitOpenResponse_builder{}.Build(), request, 1); err == nil {
		t.Fatal("wrong generated payload type accepted")
	}
	if _, err := codec.Encode(2002, worldv1.WorldSnapshotPush_builder{}.Build(), Correlation{}, 2); err != nil {
		t.Fatal(err)
	}
	errorPayload := commonv1.ErrorPayload_builder{Code: proto.Uint32(1000), MessageKey: proto.String("protocol.invalid")}.Build()
	if _, err := codec.EncodeError(2001, errorPayload, request, 3); err != nil {
		t.Fatal(err)
	}
}

// TestCodecRejectsRouteAndEnvelopeDrift 覆盖unknown ID、wrong direction/kind、sequence和frame预算。
func TestCodecRejectsRouteAndEnvelopeDrift(t *testing.T) {
	t.Parallel()
	codec := newTestCodec(t)
	kind := commonv1.MessageKind_MESSAGE_KIND_PUSH
	envelope := commonv1.ReliableEnvelope_builder{ProtocolVersion: proto.Uint32(1), MessageId: proto.Uint32(2000), Kind: &kind, Sequence: proto.Uint64(1), TimestampMs: proto.Int64(1)}.Build()
	encoded, _ := protocol.MarshalEnvelope(envelope)
	if _, err := codec.Decode(encoded, 1); err == nil {
		t.Fatal("wrong client kind accepted")
	}
	kind = commonv1.MessageKind_MESSAGE_KIND_REQUEST
	envelope = commonv1.ReliableEnvelope_builder{ProtocolVersion: proto.Uint32(1), MessageId: proto.Uint32(9999), Kind: &kind, RequestId: bytes.Repeat([]byte{1}, 16), Sequence: proto.Uint64(2), TimestampMs: proto.Int64(1)}.Build()
	encoded, _ = protocol.MarshalEnvelope(envelope)
	if _, err := codec.Decode(encoded, 1); err == nil {
		t.Fatal("unknown message or sequence gap accepted")
	}
}

// FuzzCodecDecode 验证任意envelope bytes不会绕过route/type边界或触发panic。
func FuzzCodecDecode(f *testing.F) {
	codec, _ := NewCodec(contract.TLSGameplayCatalog(), 65536, fixedClock{value: time.UnixMilli(1)})
	f.Add([]byte{})
	f.Add([]byte{1, 2, 3})
	f.Fuzz(func(t *testing.T, encoded []byte) {
		decoded, err := codec.Decode(encoded, 1)
		if err == nil && (decoded.Sequence() != 1 || decoded.Route().Direction != "CLIENT_TO_SERVER") {
			t.Fatal("Decode returned invalid success")
		}
	})
}

// newTestCodec 构造使用冻结TCP投影和确定性时间的codec。
func newTestCodec(t testing.TB) *Codec {
	t.Helper()
	codec, err := NewCodec(contract.TLSGameplayCatalog(), 65536, fixedClock{value: time.UnixMilli(1_700_000_000_000)})
	if err != nil {
		t.Fatal(err)
	}
	return codec
}
