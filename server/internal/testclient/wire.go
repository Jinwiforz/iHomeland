package testclient

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	commonv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/common/v1"
	controlv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/control/v1"
	visitv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/visit/v1"
	worldv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/world/v1"
	"google.golang.org/protobuf/proto"
)

const (
	// realtimeProtocolVersion 是 WSS envelope 与 TLS_TCP envelope 的冻结主版本。
	realtimeProtocolVersion uint32 = 1
	// gameplayPrefaceVersion 是 `IHTP` authentication preface 的冻结版本。
	gameplayPrefaceVersion uint16 = 1
	// maximumRealtimeFrameBytes 是客户端在解析公开 config 前的绝对安全上限。
	maximumRealtimeFrameBytes = 1 << 20
)

var (
	// errWireProtocol 表示公开 realtime 输入不满足冻结 wire 契约。
	errWireProtocol = errors.New("qualification realtime protocol violation")
	// serverMessageTypes 固定当前 v1 服务端可发送 message ID 与精确 payload type。
	serverMessageTypes = map[uint32]func() proto.Message{
		500:  func() proto.Message { return new(controlv1.MaintenancePush) },
		501:  func() proto.Message { return new(controlv1.ForcedLogoutPush) },
		502:  func() proto.Message { return new(controlv1.QueueStatusPush) },
		503:  func() proto.Message { return new(controlv1.EndpointUpdatePush) },
		504:  func() proto.Message { return new(controlv1.SessionInvalidatedPush) },
		2001: func() proto.Message { return new(worldv1.WorldSnapshotResponse) },
		2002: func() proto.Message { return new(worldv1.WorldSnapshotPush) },
		2003: func() proto.Message { return new(worldv1.WorldAssignmentChangedPush) },
		2100: func() proto.Message { return new(visitv1.VisitInvitePush) },
		2101: func() proto.Message { return new(visitv1.VisitOwnerAvailabilityPush) },
		2102: func() proto.Message { return new(visitv1.VisitClosedNoticePush) },
		2104: func() proto.Message { return new(visitv1.VisitOpenResponse) },
		2106: func() proto.Message { return new(visitv1.VisitCreateInviteResponse) },
		2108: func() proto.Message { return new(visitv1.VisitRevokeInviteResponse) },
		2110: func() proto.Message { return new(visitv1.VisitJoinResponse) },
		2112: func() proto.Message { return new(visitv1.VisitLeaveResponse) },
		2114: func() proto.Message { return new(visitv1.VisitKickResponse) },
		2116: func() proto.Message { return new(visitv1.VisitReconnectResponse) },
		2118: func() proto.Message { return new(visitv1.VisitCloseResponse) },
		2120: func() proto.Message { return new(visitv1.VisitSnapshotResponse) },
		2121: func() proto.Message { return new(visitv1.VisitSnapshotPush) },
		2122: func() proto.Message { return new(visitv1.VisitSafeReturnPush) },
	}
	// clientMessageTypes 固定当前 v1 客户端可发送 message ID 与精确 payload type/kind。
	clientMessageTypes = map[uint32]clientMessageProfile{
		2000: {kind: commonv1.MessageKind_MESSAGE_KIND_REQUEST, payload: func() proto.Message { return new(worldv1.WorldSnapshotRequest) }},
		2103: {kind: commonv1.MessageKind_MESSAGE_KIND_COMMAND, payload: func() proto.Message { return new(visitv1.VisitOpenCommand) }},
		2105: {kind: commonv1.MessageKind_MESSAGE_KIND_COMMAND, payload: func() proto.Message { return new(visitv1.VisitCreateInviteCommand) }},
		2107: {kind: commonv1.MessageKind_MESSAGE_KIND_COMMAND, payload: func() proto.Message { return new(visitv1.VisitRevokeInviteCommand) }},
		2109: {kind: commonv1.MessageKind_MESSAGE_KIND_COMMAND, payload: func() proto.Message { return new(visitv1.VisitJoinCommand) }},
		2111: {kind: commonv1.MessageKind_MESSAGE_KIND_COMMAND, payload: func() proto.Message { return new(visitv1.VisitLeaveCommand) }},
		2113: {kind: commonv1.MessageKind_MESSAGE_KIND_COMMAND, payload: func() proto.Message { return new(visitv1.VisitKickCommand) }},
		2115: {kind: commonv1.MessageKind_MESSAGE_KIND_COMMAND, payload: func() proto.Message { return new(visitv1.VisitReconnectCommand) }},
		2117: {kind: commonv1.MessageKind_MESSAGE_KIND_COMMAND, payload: func() proto.Message { return new(visitv1.VisitCloseCommand) }},
		2119: {kind: commonv1.MessageKind_MESSAGE_KIND_REQUEST, payload: func() proto.Message { return new(visitv1.VisitSnapshotRequest) }},
	}
)

// clientMessageProfile 绑定一个 C2S message ID 的 kind 与 generated payload 类型。
type clientMessageProfile struct {
	// kind 是冻结 registry 声明的 REQUEST 或 COMMAND。
	kind commonv1.MessageKind
	// payload 创建精确 generated payload 零值。
	payload func() proto.Message
}

// DecodedRealtimeMessage 是经过 envelope、sequence、correlation 与 payload 类型校验的消息。
type DecodedRealtimeMessage struct {
	// MessageID 是公开 message registry 编号。
	MessageID uint32
	// Kind 是公开 envelope 投递语义。
	Kind commonv1.MessageKind
	// RequestID 是 response/error 对应的 16-byte request identity。
	RequestID []byte
	// CommandID 是 response/error 对应的 16-byte command identity。
	CommandID []byte
	// Sequence 是服务端在单连接内从 1 开始的递增序号。
	Sequence uint64
	// Payload 是 message ID 对应的精确 generated Protobuf。
	Payload proto.Message
}

// newCorrelationID 创建 realtime envelope 使用的 16-byte CSPRNG identity。
func newCorrelationID() ([]byte, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return nil, fmt.Errorf("generate realtime correlation: %w", err)
	}
	return value, nil
}

// encodeClientEnvelope 校验 registry profile 后确定性编码一个 C2S envelope。
func encodeClientEnvelope(messageID uint32, payload proto.Message, correlation []byte, sequence uint64) ([]byte, error) {
	profile, exists := clientMessageTypes[messageID]
	if !exists || payload == nil || !payload.ProtoReflect().IsValid() || sequence == 0 || len(correlation) != 16 {
		return nil, errWireProtocol
	}
	if payload.ProtoReflect().Descriptor().FullName() != profile.payload().ProtoReflect().Descriptor().FullName() {
		return nil, errWireProtocol
	}
	payloadBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal realtime payload: %w", err)
	}
	kind := profile.kind
	builder := commonv1.ReliableEnvelope_builder{
		ProtocolVersion: proto.Uint32(realtimeProtocolVersion),
		MessageId:       proto.Uint32(messageID),
		Kind:            &kind,
		Sequence:        proto.Uint64(sequence),
		TimestampMs:     proto.Int64(time.Now().UTC().UnixMilli()),
		Payload:         payloadBytes,
	}
	if kind == commonv1.MessageKind_MESSAGE_KIND_REQUEST {
		builder.RequestId = append([]byte(nil), correlation...)
	} else {
		builder.CommandId = append([]byte(nil), correlation...)
	}
	envelope := builder.Build()
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("marshal realtime envelope: %w", err)
	}
	if len(encoded) == 0 || len(encoded) > maximumRealtimeFrameBytes {
		return nil, errWireProtocol
	}
	return encoded, nil
}

// decodeServerEnvelope 严格验证一个 S2C envelope 和精确 generated payload。
func decodeServerEnvelope(encoded []byte, expectedSequence uint64, wss bool) (DecodedRealtimeMessage, error) {
	if len(encoded) == 0 || len(encoded) > maximumRealtimeFrameBytes || expectedSequence == 0 {
		return DecodedRealtimeMessage{}, errWireProtocol
	}
	envelope := new(commonv1.ReliableEnvelope)
	if err := (proto.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(encoded, envelope); err != nil || len(envelope.ProtoReflect().GetUnknown()) != 0 {
		return DecodedRealtimeMessage{}, errWireProtocol
	}
	if envelope.GetProtocolVersion() != realtimeProtocolVersion || envelope.GetSequence() != expectedSequence || envelope.GetTimestampMs() <= 0 {
		return DecodedRealtimeMessage{}, errWireProtocol
	}
	if err := validateServerKindAndChannel(envelope, wss); err != nil {
		return DecodedRealtimeMessage{}, err
	}
	var payload proto.Message
	if envelope.GetKind() == commonv1.MessageKind_MESSAGE_KIND_ERROR {
		payload = new(commonv1.ErrorPayload)
	} else {
		factory, exists := serverMessageTypes[envelope.GetMessageId()]
		if !exists {
			return DecodedRealtimeMessage{}, errWireProtocol
		}
		payload = factory()
	}
	if err := (proto.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(envelope.GetPayload(), payload); err != nil || len(payload.ProtoReflect().GetUnknown()) != 0 {
		return DecodedRealtimeMessage{}, errWireProtocol
	}
	return DecodedRealtimeMessage{
		MessageID: envelope.GetMessageId(), Kind: envelope.GetKind(),
		RequestID: append([]byte(nil), envelope.GetRequestId()...), CommandID: append([]byte(nil), envelope.GetCommandId()...),
		Sequence: envelope.GetSequence(), Payload: payload,
	}, nil
}

// validateServerKindAndChannel 拒绝 unknown kind、错误 correlation 和跨 channel message。
func validateServerKindAndChannel(envelope *commonv1.ReliableEnvelope, wss bool) error {
	requestLength, commandLength := len(envelope.GetRequestId()), len(envelope.GetCommandId())
	if requestLength != 0 && requestLength != 16 || commandLength != 0 && commandLength != 16 {
		return errWireProtocol
	}
	if wss {
		if envelope.GetKind() != commonv1.MessageKind_MESSAGE_KIND_PUSH || requestLength != 0 || commandLength != 0 || !isWSSMessage(envelope.GetMessageId()) {
			return errWireProtocol
		}
		return nil
	}
	if isWSSMessage(envelope.GetMessageId()) {
		return errWireProtocol
	}
	switch envelope.GetKind() {
	case commonv1.MessageKind_MESSAGE_KIND_RESPONSE, commonv1.MessageKind_MESSAGE_KIND_ERROR:
		if (requestLength == 16) == (commandLength == 16) {
			return errWireProtocol
		}
	case commonv1.MessageKind_MESSAGE_KIND_PUSH:
		if requestLength != 0 || commandLength != 0 {
			return errWireProtocol
		}
	default:
		return errWireProtocol
	}
	return nil
}

// isWSSMessage 报告 message registry 是否把编号唯一分配给 control channel。
func isWSSMessage(messageID uint32) bool {
	return messageID >= 500 && messageID <= 504 || messageID == 2003 || messageID >= 2100 && messageID <= 2102
}

// encodeGameplayPreface 编码固定 IHTP 双 credential authentication frame。
func encodeGameplayPreface(ticket, admission string, purpose string) ([]byte, error) {
	purposeByte := byte(0)
	switch purpose {
	case "OWN_WORLD":
		purposeByte = 1
	case "JOIN":
		purposeByte = 2
	case "RECONNECT":
		purposeByte = 3
	default:
		return nil, errWireProtocol
	}
	if !validTicketCredential(ticket) || !validAdmissionCredential(admission) {
		return nil, errWireProtocol
	}
	body := make([]byte, 11+len(ticket)+len(admission))
	copy(body[:4], "IHTP")
	binary.BigEndian.PutUint16(body[4:6], gameplayPrefaceVersion)
	binary.BigEndian.PutUint16(body[6:8], uint16(len(ticket)))
	binary.BigEndian.PutUint16(body[8:10], uint16(len(admission)))
	body[10] = purposeByte
	copy(body[11:43], ticket)
	copy(body[43:], admission)
	return encodeFrame(body, maximumRealtimeFrameBytes)
}

// validTicketCredential 只接受 16-byte nonce 的规范小写 hex 表达。
func validTicketCredential(value string) bool {
	if len(value) != 32 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 16 && hex.EncodeToString(decoded) == value
}

// validAdmissionCredential 只接受 wad1_ 与 32-byte raw-base64url 的规范字符/长度。
func validAdmissionCredential(value string) bool {
	if len(value) != 48 || value[:5] != "wad1_" {
		return false
	}
	for index := 5; index < len(value); index++ {
		character := value[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

// encodeFrame 为 TLS_TCP payload 添加 4-byte 大端长度前缀。
func encodeFrame(payload []byte, maximumBytes int) ([]byte, error) {
	if len(payload) == 0 || maximumBytes <= 0 || len(payload) > maximumBytes {
		return nil, errWireProtocol
	}
	frame := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(payload)))
	copy(frame[4:], payload)
	return frame, nil
}

// readFrame 在分配前验证 4-byte 声明长度，再精确读取单个 TLS_TCP frame。
func readFrame(reader io.Reader, maximumBytes int) ([]byte, error) {
	if reader == nil || maximumBytes <= 0 || maximumBytes > maximumRealtimeFrameBytes {
		return nil, errWireProtocol
	}
	var prefix [4]byte
	if _, err := io.ReadFull(reader, prefix[:]); err != nil {
		return nil, err
	}
	length := int(binary.BigEndian.Uint32(prefix[:]))
	if length <= 0 || length > maximumBytes {
		return nil, errWireProtocol
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// safeCredentialASCII 拒绝 whitespace、control 与非 ASCII credential bytes。
func safeCredentialASCII(value string) bool {
	for index := range value {
		if value[index] < 0x21 || value[index] > 0x7e {
			return false
		}
	}
	return true
}
