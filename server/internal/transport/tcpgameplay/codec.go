package tcpgameplay

import (
	"errors"
	"fmt"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/contract"
	commonv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/common/v1"
	visitv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/visit/v1"
	worldv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/world/v1"
	"github.com/jinwiforz/ihomeland/server/internal/protocol"
	"google.golang.org/protobuf/proto"
)

// Correlation 保存REQUEST或COMMAND恰好一种16-byte关联标识。
type Correlation struct {
	// RequestID 只用于request/response/error。
	RequestID []byte
	// CommandID 只用于command/response/error。
	CommandID []byte
}

// Valid 报告是否恰好存在一种合法关联标识。
func (value Correlation) Valid() bool {
	return (len(value.RequestID) == 16) != (len(value.CommandID) == 16)
}

// DecodedMessage 是通过registry、envelope与精确generated type校验的C2S输入。
type DecodedMessage struct {
	// route 是启动期冻结的精确执行策略。
	route contract.ProjectedRoute
	// payload 是与route.Protobuf完全一致的新消息实例。
	payload proto.Message
	// correlation 复制自validated envelope。
	correlation Correlation
	// sequence 是本连接严格递增的C2S序号。
	sequence uint64
}

// Route 返回只读路由值副本。
func (message DecodedMessage) Route() contract.ProjectedRoute { return message.route }

// Payload 返回已完成精确类型校验的generated message。
func (message DecodedMessage) Payload() proto.Message { return message.payload }

// Correlation 返回关联标识副本。
func (message DecodedMessage) Correlation() Correlation {
	return Correlation{RequestID: append([]byte(nil), message.correlation.RequestID...), CommandID: append([]byte(nil), message.correlation.CommandID...)}
}

// Sequence 返回C2S连接序号。
func (message DecodedMessage) Sequence() uint64 { return message.sequence }

// EncodedMessage 是待length-prefix写出的不可变ReliableEnvelope。
type EncodedMessage struct {
	// messageID 是metrics与route诊断使用的稳定编号。
	messageID uint32
	// frame 保存4-byte prefix与完整envelope。
	frame []byte
}

// MessageID 返回协议登记编号。
func (message EncodedMessage) MessageID() uint32 { return message.messageID }

// Frame 返回完整frame副本。
func (message EncodedMessage) Frame() []byte { return append([]byte(nil), message.frame...) }

// Size 返回完整frame字节数，不复制内容。
func (message EncodedMessage) Size() int { return len(message.frame) }

// Codec 按冻结contract投影解码C2S并编码S2C。
type Codec struct {
	// catalog 是启动时冻结的完整TCP投影。
	catalog contract.Catalog
	// frameBytes 是route上限之外的全局envelope预算。
	frameBytes int
	// clock 提供S2C可信timestamp。
	clock Clock
}

// NewCodec 校验全部登记route存在后构造无listener codec。
func NewCodec(catalog contract.Catalog, frameBytes int, clock Clock) (*Codec, error) {
	if frameBytes < 1024 || frameBytes > protocol.MaximumFrameSize || clock == nil {
		return nil, errors.New("tcp gameplay codec dependencies are incomplete")
	}
	for _, profile := range contract.TLSGameplayCatalog().Routes.Routes {
		direction := "CLIENT_TO_SERVER"
		for _, message := range contract.TLSGameplayCatalog().Messages.Messages {
			if message.ID == profile.MessageID {
				direction = message.Direction
				break
			}
		}
		if _, err := catalog.LookupTLSGameplay(profile.MessageID, direction); err != nil {
			return nil, fmt.Errorf("tcp gameplay catalog is incomplete: %w", err)
		}
	}
	return &Codec{catalog: catalog, frameBytes: frameBytes, clock: clock}, nil
}

// Decode 校验完整C2S envelope、连续sequence、route预算与精确generated payload类型。
func (codec *Codec) Decode(encoded []byte, expectedSequence uint64) (DecodedMessage, error) {
	if expectedSequence == 0 {
		return DecodedMessage{}, errors.New("tcp gameplay expected sequence is invalid")
	}
	envelope, err := protocol.UnmarshalEnvelope(encoded)
	if err != nil {
		return DecodedMessage{}, fmt.Errorf("%w: invalid envelope", ErrProtocol)
	}
	if envelope.GetProtocolVersion() != ProtocolVersion || envelope.GetSequence() != expectedSequence {
		return DecodedMessage{}, fmt.Errorf("%w: envelope version or sequence is invalid", ErrProtocol)
	}
	route, err := codec.catalog.LookupTLSGameplay(envelope.GetMessageId(), "CLIENT_TO_SERVER")
	if err != nil {
		return DecodedMessage{}, fmt.Errorf("%w: route rejected", ErrProtocol)
	}
	if len(encoded) > codec.frameBytes || uint32(len(encoded)) > route.MaxSize || envelope.GetKind() != messageKind(route.Kind) {
		return DecodedMessage{}, fmt.Errorf("%w: route profile mismatch", ErrProtocol)
	}
	payload, err := clientPayload(envelope.GetMessageId())
	if err != nil {
		return DecodedMessage{}, err
	}
	if string(payload.ProtoReflect().Descriptor().FullName()) != route.Protobuf {
		return DecodedMessage{}, fmt.Errorf("%w: generated payload type drift", ErrProtocol)
	}
	if err := (proto.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(envelope.GetPayload(), payload); err != nil {
		return DecodedMessage{}, fmt.Errorf("%w: payload decode failed", ErrProtocol)
	}
	return DecodedMessage{route: route, payload: payload, correlation: Correlation{RequestID: append([]byte(nil), envelope.GetRequestId()...), CommandID: append([]byte(nil), envelope.GetCommandId()...)}, sequence: envelope.GetSequence()}, nil
}

// Encode 校验精确S2C payload、correlation与预算，并确定性生成完整length-prefixed frame。
func (codec *Codec) Encode(messageID uint32, payload proto.Message, correlation Correlation, sequence uint64) (EncodedMessage, error) {
	if payload == nil || !payload.ProtoReflect().IsValid() || sequence == 0 {
		return EncodedMessage{}, errors.New("tcp gameplay output requires payload and positive sequence")
	}
	route, err := codec.catalog.LookupTLSGameplay(messageID, "SERVER_TO_CLIENT")
	if err != nil {
		return EncodedMessage{}, err
	}
	if string(payload.ProtoReflect().Descriptor().FullName()) != route.Protobuf {
		return EncodedMessage{}, fmt.Errorf("message %d payload type does not match contract", messageID)
	}
	kind := messageKind(route.Kind)
	if (kind == commonv1.MessageKind_MESSAGE_KIND_RESPONSE) != correlation.Valid() {
		return EncodedMessage{}, errors.New("tcp gameplay output correlation is invalid")
	}
	return codec.encodeEnvelope(messageID, kind, payload, correlation, sequence, route.MaxSize)
}

// EncodeError 使用response route预算发送共享ErrorPayload，不回显内部错误文本。
func (codec *Codec) EncodeError(responseMessageID uint32, payload *commonv1.ErrorPayload, correlation Correlation, sequence uint64) (EncodedMessage, error) {
	if payload == nil || !correlation.Valid() || sequence == 0 {
		return EncodedMessage{}, errors.New("tcp gameplay error output is invalid")
	}
	route, err := codec.catalog.LookupTLSGameplay(responseMessageID, "SERVER_TO_CLIENT")
	if err != nil || route.Kind != "RESPONSE" {
		return EncodedMessage{}, errors.New("tcp gameplay error route is invalid")
	}
	return codec.encodeEnvelope(responseMessageID, commonv1.MessageKind_MESSAGE_KIND_ERROR, payload, correlation, sequence, route.MaxSize)
}

// encodeEnvelope 集中执行deterministic payload/envelope编码与完整frame预算检查。
func (codec *Codec) encodeEnvelope(messageID uint32, kind commonv1.MessageKind, payload proto.Message, correlation Correlation, sequence uint64, routeMax uint32) (EncodedMessage, error) {
	payloadBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(payload)
	if err != nil {
		return EncodedMessage{}, fmt.Errorf("encode message %d payload", messageID)
	}
	now := codec.clock.Now().UTC()
	if now.IsZero() || now.Before(time.UnixMilli(1)) {
		return EncodedMessage{}, errors.New("tcp gameplay clock returned invalid time")
	}
	envelope := commonv1.ReliableEnvelope_builder{
		ProtocolVersion: proto.Uint32(ProtocolVersion), MessageId: proto.Uint32(messageID), Kind: &kind,
		RequestId: append([]byte(nil), correlation.RequestID...), CommandId: append([]byte(nil), correlation.CommandID...),
		Sequence: proto.Uint64(sequence), TimestampMs: proto.Int64(now.UnixMilli()), Payload: payloadBytes,
	}.Build()
	encoded, err := protocol.MarshalEnvelope(envelope)
	if err != nil {
		return EncodedMessage{}, fmt.Errorf("encode message %d envelope: %w", messageID, err)
	}
	if len(encoded) > codec.frameBytes || uint32(len(encoded)) > routeMax {
		return EncodedMessage{}, fmt.Errorf("message %d exceeds encoded frame budget", messageID)
	}
	frame, err := protocol.EncodeFrame(encoded)
	if err != nil {
		return EncodedMessage{}, err
	}
	return EncodedMessage{messageID: messageID, frame: frame}, nil
}

// clientPayload 为每个允许C2S route创建精确generated message实例。
func clientPayload(messageID uint32) (proto.Message, error) {
	switch messageID {
	case 1:
		return new(commonv1.GameplayHeartbeatRequest), nil
	case 2000:
		return new(worldv1.WorldSnapshotRequest), nil
	case 2103:
		return new(visitv1.VisitOpenCommand), nil
	case 2105:
		return new(visitv1.VisitCreateInviteCommand), nil
	case 2107:
		return new(visitv1.VisitRevokeInviteCommand), nil
	case 2109:
		return new(visitv1.VisitJoinCommand), nil
	case 2111:
		return new(visitv1.VisitLeaveCommand), nil
	case 2113:
		return new(visitv1.VisitKickCommand), nil
	case 2115:
		return new(visitv1.VisitReconnectCommand), nil
	case 2117:
		return new(visitv1.VisitCloseCommand), nil
	case 2119:
		return new(visitv1.VisitSnapshotRequest), nil
	default:
		return nil, fmt.Errorf("%w: unknown client message", ErrProtocol)
	}
}

// messageKind 把已校验contract字符串映射为封闭wire enum。
func messageKind(value string) commonv1.MessageKind {
	switch value {
	case "REQUEST":
		return commonv1.MessageKind_MESSAGE_KIND_REQUEST
	case "RESPONSE":
		return commonv1.MessageKind_MESSAGE_KIND_RESPONSE
	case "COMMAND":
		return commonv1.MessageKind_MESSAGE_KIND_COMMAND
	case "PUSH":
		return commonv1.MessageKind_MESSAGE_KIND_PUSH
	default:
		return commonv1.MessageKind_MESSAGE_KIND_UNSPECIFIED
	}
}
