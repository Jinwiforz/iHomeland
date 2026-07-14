package protocol

import (
	"bytes"
	"testing"

	commonv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/common/v1"
	"google.golang.org/protobuf/proto"
)

// TestEnvelopeDeterministicRoundTrip 保护合法 envelope 的稳定编码与路由字段 round-trip。
// 相同消息在解码后重新编码必须得到完全相同字节，否则 golden packet 与跨语言摘要将失去意义。
func TestEnvelopeDeterministicRoundTrip(t *testing.T) {
	envelope := commonv1.ReliableEnvelope_builder{
		ProtocolVersion: proto.Uint32(1),
		MessageId:       proto.Uint32(500),
		Kind:            enumPointer(commonv1.MessageKind_MESSAGE_KIND_REQUEST),
		RequestId:       bytes.Repeat([]byte{1}, 16),
		Sequence:        proto.Uint64(1),
		TimestampMs:     proto.Int64(1_700_000_000_000),
		Payload:         []byte{1, 2, 3},
	}.Build()
	first, err := MarshalEnvelope(envelope)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalEnvelope(first)
	if err != nil {
		t.Fatal(err)
	}
	second, err := MarshalEnvelope(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("deterministic encoding changed after round trip")
	}
}

// TestEnvelopeRejectsInvalidKindIdentifierPairs 防止存在歧义的 correlation 到达 dispatch。
// 缺少 command_id 与未知 enum 分别模拟不完整发送方和未来/恶意输入，二者都不能降级处理。
func TestEnvelopeRejectsInvalidKindIdentifierPairs(t *testing.T) {
	envelope := commonv1.ReliableEnvelope_builder{ProtocolVersion: proto.Uint32(1), MessageId: proto.Uint32(2002), Kind: enumPointer(commonv1.MessageKind_MESSAGE_KIND_COMMAND), Sequence: proto.Uint64(1), TimestampMs: proto.Int64(1)}.Build()
	if err := ValidateEnvelope(envelope); err == nil {
		t.Fatal("command without command id should fail")
	}
	envelope.SetKind(commonv1.MessageKind(99))
	if err := ValidateEnvelope(envelope); err == nil {
		t.Fatal("unknown enum should fail")
	}
}

// TestEnvelopeResponseRequiresExactlyOneCorrelation 固定 response 对 request 或 command 的唯一回显规则。
// 同时携带或完全缺少 identifier 都无法确定首次提交结果的归属，必须在 dispatch 前拒绝。
func TestEnvelopeResponseRequiresExactlyOneCorrelation(t *testing.T) {
	base := commonv1.ReliableEnvelope_builder{ProtocolVersion: proto.Uint32(1), MessageId: proto.Uint32(2104), Kind: enumPointer(commonv1.MessageKind_MESSAGE_KIND_RESPONSE), Sequence: proto.Uint64(1), TimestampMs: proto.Int64(1)}.Build()
	if err := ValidateEnvelope(base); err == nil {
		t.Fatal("response without correlation should fail")
	}
	base.SetRequestId(bytes.Repeat([]byte{1}, 16))
	if err := ValidateEnvelope(base); err != nil {
		t.Fatalf("request response correlation rejected: %v", err)
	}
	base.SetCommandId(bytes.Repeat([]byte{2}, 16))
	if err := ValidateEnvelope(base); err == nil {
		t.Fatal("response with both correlations should fail")
	}
	base.ClearRequestId()
	if err := ValidateEnvelope(base); err != nil {
		t.Fatalf("command response correlation rejected: %v", err)
	}
}

// TestEnvelopeRejectsMissingSequenceAndOversize 保护 WSS 与 TLS/TCP 共享的 envelope 资源边界。
// WSS 不经过 length-prefix frame decoder，因此大小限制必须由通道无关 codec 再执行一次。
func TestEnvelopeRejectsMissingSequenceAndOversize(t *testing.T) {
	envelope := commonv1.ReliableEnvelope_builder{ProtocolVersion: proto.Uint32(1), MessageId: proto.Uint32(500), Kind: enumPointer(commonv1.MessageKind_MESSAGE_KIND_REQUEST), RequestId: bytes.Repeat([]byte{1}, 16), TimestampMs: proto.Int64(1)}.Build()
	if err := ValidateEnvelope(envelope); err == nil {
		t.Fatal("zero sequence should fail")
	}
	envelope.SetSequence(1)
	envelope.SetPayload(make([]byte, MaximumFrameSize))
	if _, err := MarshalEnvelope(envelope); err == nil {
		t.Fatal("encoded envelope above the global limit should fail")
	}
}

// enumPointer 为聚焦 codec 测试创建 Edition opaque builder 要求的 enum 指针。
// 指针仅在构建测试消息时使用，不在用例之间共享。
func enumPointer[T ~int32](value T) *T { return &value }
