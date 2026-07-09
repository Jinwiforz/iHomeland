package protocol

import (
	"errors"
	"fmt"
	"time"

	pb "ihomeland/server/internal/protocol/pb/realtime/v1"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

var (
	// ErrMessageIDUnsupported 表示 message id 未在当前服务端注册或支持。
	ErrMessageIDUnsupported = errors.New("message id unsupported")
	// ErrPayloadInvalid 表示 envelope payload 缺失或无法按目标类型解码。
	ErrPayloadInvalid = errors.New("payload invalid")
	// ErrRequestIDRequired 表示需要关联响应的消息缺少 request id。
	ErrRequestIDRequired = errors.New("request id required")
)

var messageTypes = map[MessageID]func() proto.Message{
	MessageIDHeartbeatRequest:           func() proto.Message { return &pb.HeartbeatRequest{} },
	MessageIDHeartbeatResponse:          func() proto.Message { return &pb.HeartbeatResponse{} },
	MessageIDErrorResponse:              func() proto.Message { return &pb.ErrorResponse{} },
	MessageIDProtocolVersionUnsupported: func() proto.Message { return &pb.ProtocolVersionUnsupported{} },
	MessageIDRegisterRequest:            func() proto.Message { return &pb.RegisterRequest{} },
	MessageIDRegisterResponse:           func() proto.Message { return &pb.RegisterResponse{} },
	MessageIDLoginRequest:               func() proto.Message { return &pb.LoginRequest{} },
	MessageIDLoginResponse:              func() proto.Message { return &pb.LoginResponse{} },
	MessageIDLogoutRequest:              func() proto.Message { return &pb.LogoutRequest{} },
	MessageIDLogoutResponse:             func() proto.Message { return &pb.LogoutResponse{} },
	MessageIDResumeSessionRequest:       func() proto.Message { return &pb.ResumeSessionRequest{} },
	MessageIDResumeSessionResponse:      func() proto.Message { return &pb.ResumeSessionResponse{} },
	MessageIDGetCurrentPlayerRequest:    func() proto.Message { return &pb.GetCurrentPlayerRequest{} },
	MessageIDGetCurrentPlayerResponse:   func() proto.Message { return &pb.GetCurrentPlayerResponse{} },
	MessageIDCreateRoomRequest:          func() proto.Message { return &pb.CreateRoomRequest{} },
	MessageIDCreateRoomResponse:         func() proto.Message { return &pb.CreateRoomResponse{} },
	MessageIDJoinRoomRequest:            func() proto.Message { return &pb.JoinRoomRequest{} },
	MessageIDJoinRoomResponse:           func() proto.Message { return &pb.JoinRoomResponse{} },
	MessageIDSetReadyRequest:            func() proto.Message { return &pb.SetReadyRequest{} },
	MessageIDSetReadyResponse:           func() proto.Message { return &pb.SetReadyResponse{} },
	MessageIDLeaveRoomRequest:           func() proto.Message { return &pb.LeaveRoomRequest{} },
	MessageIDLeaveRoomResponse:          func() proto.Message { return &pb.LeaveRoomResponse{} },
	MessageIDTransferHostRequest:        func() proto.Message { return &pb.TransferHostRequest{} },
	MessageIDTransferHostResponse:       func() proto.Message { return &pb.TransferHostResponse{} },
	MessageIDReconnectRoomRequest:       func() proto.Message { return &pb.ReconnectRoomRequest{} },
	MessageIDReconnectRoomResponse:      func() proto.Message { return &pb.ReconnectRoomResponse{} },
	MessageIDRoomSnapshotPushed:         func() proto.Message { return &pb.RoomSnapshotPushed{} },
	MessageIDStartRoomRequest:          func() proto.Message { return &pb.StartRoomRequest{} },
	MessageIDStartRoomResponse:         func() proto.Message { return &pb.StartRoomResponse{} },
}

// BuildOptions 描述构造 envelope 所需的稳定元数据。
type BuildOptions struct {
	ProtocolVersion uint32
	MessageID       MessageID
	RequestID       string
	Sequence        uint64
	Timestamp       time.Time
}

// BuildEnvelope 将 Protobuf 消息包装为统一实时通信 envelope。
func BuildEnvelope(opts BuildOptions, msg proto.Message) (*pb.Envelope, error) {
	if msg == nil {
		return nil, ErrPayloadInvalid
	}
	newMessage, ok := messageTypes[opts.MessageID]
	if !ok {
		return nil, fmt.Errorf("%w: %d", ErrMessageIDUnsupported, opts.MessageID)
	}
	if proto.MessageName(msg) != proto.MessageName(newMessage()) {
		return nil, fmt.Errorf("%w: message id %d expects %s, got %s", ErrPayloadInvalid, opts.MessageID, proto.MessageName(newMessage()), proto.MessageName(msg))
	}

	payload, err := anypb.New(msg)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPayloadInvalid, err)
	}

	timestamp := opts.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now()
	}

	return &pb.Envelope{
		ProtocolVersion: opts.ProtocolVersion,
		MessageId:       pb.MessageID(opts.MessageID),
		RequestId:       opts.RequestID,
		Sequence:        opts.Sequence,
		TimestampMs:     timestamp.UnixMilli(),
		Payload:         payload,
	}, nil
}

// DecodeEnvelope 校验 envelope 基础字段并按 message id 解码 payload。
func DecodeEnvelope(envelope *pb.Envelope, requestIDRequired bool) (proto.Message, error) {
	if envelope == nil || envelope.GetPayload() == nil {
		return nil, ErrPayloadInvalid
	}
	if requestIDRequired && envelope.GetRequestId() == "" {
		return nil, ErrRequestIDRequired
	}

	id := MessageID(envelope.GetMessageId())
	newMessage, ok := messageTypes[id]
	if !ok {
		return nil, fmt.Errorf("%w: %d", ErrMessageIDUnsupported, id)
	}

	msg := newMessage()
	if err := envelope.GetPayload().UnmarshalTo(msg); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPayloadInvalid, err)
	}
	return msg, nil
}

// BuildErrorEnvelope 构造保留 request id 的结构化错误 envelope。
func BuildErrorEnvelope(requestID string, sequence uint64, code ErrorCode, message string, detail string) (*pb.Envelope, error) {
	return BuildEnvelope(BuildOptions{
		ProtocolVersion: MaxSupportedVersion,
		MessageID:       MessageIDErrorResponse,
		RequestID:       requestID,
		Sequence:        sequence,
	}, &pb.ErrorResponse{
		Code:    pb.ErrorCode(code),
		Message: message,
		Detail:  detail,
	})
}
