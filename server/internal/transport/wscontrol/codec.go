package wscontrol

import (
	"errors"
	"fmt"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/contract"
	commonv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/common/v1"
	"github.com/jinwiforz/ihomeland/server/internal/protocol"
	"google.golang.org/protobuf/proto"
)

// EncodedMessage 是入队后不可变的完整ReliableEnvelope。
//
// bytes始终由构造函数复制，访问器也返回副本，避免publisher在并发写出期间修改内容。
type EncodedMessage struct {
	// messageID 是metrics与调试使用的稳定协议编号。
	messageID uint32
	// bytes 保存确定性编码后的完整envelope。
	bytes []byte
}

// MessageID 返回协议登记编号。
func (message EncodedMessage) MessageID() uint32 { return message.messageID }

// Bytes 返回完整frame副本。
func (message EncodedMessage) Bytes() []byte { return append([]byte(nil), message.bytes...) }

// Size 返回编码后字节数，不复制内容。
func (message EncodedMessage) Size() int { return len(message.bytes) }

// Codec 通过contract catalog把typed generated payload编码成唯一WSS envelope。
type Codec struct {
	// catalog 是启动时冻结的只读内存投影。
	catalog contract.Catalog
	// frameBytes 是所有route共享的绝对上限。
	frameBytes int
	// clock 提供服务端观测时间。
	clock Clock
}

// NewCodec 构造无listener、无状态的WSS编码器。
func NewCodec(catalog contract.Catalog, frameBytes int, clock Clock) (*Codec, error) {
	if frameBytes < 1024 || frameBytes > protocol.MaximumFrameSize || clock == nil {
		return nil, errors.New("websocket control codec dependencies are incomplete")
	}
	for _, id := range []uint32{500, 501, 502, 503, 504, 2003, 2100, 2101, 2102} {
		if _, err := catalog.LookupWSSPush(id); err != nil {
			return nil, fmt.Errorf("websocket control catalog is incomplete: %w", err)
		}
	}
	return &Codec{catalog: catalog, frameBytes: frameBytes, clock: clock}, nil
}

// Encode 校验精确generated payload类型并确定性编码完整PUSH envelope。
func (codec *Codec) Encode(messageID uint32, payload proto.Message, sequence uint64) (EncodedMessage, error) {
	if payload == nil || !payload.ProtoReflect().IsValid() || sequence == 0 {
		return EncodedMessage{}, errors.New("websocket control push requires payload and positive sequence")
	}
	route, err := codec.catalog.LookupWSSPush(messageID)
	if err != nil {
		return EncodedMessage{}, err
	}
	actualType := string(payload.ProtoReflect().Descriptor().FullName())
	if actualType != route.Protobuf {
		return EncodedMessage{}, fmt.Errorf("message %d payload type does not match contract", messageID)
	}
	payloadBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(payload)
	if err != nil {
		return EncodedMessage{}, fmt.Errorf("encode message %d payload: %w", messageID, err)
	}
	now := codec.clock.Now().UTC()
	if now.IsZero() || now.Before(time.UnixMilli(1)) {
		return EncodedMessage{}, errors.New("websocket control clock returned invalid time")
	}
	kind := commonv1.MessageKind_MESSAGE_KIND_PUSH
	envelope := commonv1.ReliableEnvelope_builder{
		ProtocolVersion: proto.Uint32(ProtocolVersion),
		MessageId:       proto.Uint32(messageID),
		Kind:            &kind,
		Sequence:        proto.Uint64(sequence),
		TimestampMs:     proto.Int64(now.UnixMilli()),
		Payload:         payloadBytes,
	}.Build()
	encoded, err := protocol.MarshalEnvelope(envelope)
	if err != nil {
		return EncodedMessage{}, fmt.Errorf("encode message %d envelope: %w", messageID, err)
	}
	if len(encoded) > codec.frameBytes || uint32(len(encoded)) > route.MaxSize {
		return EncodedMessage{}, fmt.Errorf("message %d exceeds encoded frame budget", messageID)
	}
	return EncodedMessage{messageID: messageID, bytes: append([]byte(nil), encoded...)}, nil
}
