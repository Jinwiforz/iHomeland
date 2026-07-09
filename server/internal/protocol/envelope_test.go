package protocol

import (
	"errors"
	"testing"
	"time"

	pb "ihomeland/server/internal/protocol/pb/realtime/v1"
)

func TestBuildAndDecodeEnvelope(t *testing.T) {
	envelope, err := BuildEnvelope(BuildOptions{
		ProtocolVersion: MaxSupportedVersion,
		MessageID:       MessageIDHeartbeatRequest,
		RequestID:       "req-1",
		Sequence:        7,
		Timestamp:       time.UnixMilli(1000),
	}, &pb.HeartbeatRequest{ClientTimeMs: 123})
	if err != nil {
		t.Fatalf("BuildEnvelope returned error: %v", err)
	}

	if envelope.GetProtocolVersion() != MaxSupportedVersion {
		t.Fatalf("protocol version = %d, want %d", envelope.GetProtocolVersion(), MaxSupportedVersion)
	}
	if envelope.GetRequestId() != "req-1" {
		t.Fatalf("request id = %q, want req-1", envelope.GetRequestId())
	}
	if envelope.GetSequence() != 7 {
		t.Fatalf("sequence = %d, want 7", envelope.GetSequence())
	}
	if envelope.GetTimestampMs() != 1000 {
		t.Fatalf("timestamp = %d, want 1000", envelope.GetTimestampMs())
	}

	decoded, err := DecodeEnvelope(envelope, true)
	if err != nil {
		t.Fatalf("DecodeEnvelope returned error: %v", err)
	}
	heartbeat, ok := decoded.(*pb.HeartbeatRequest)
	if !ok {
		t.Fatalf("decoded message type = %T, want *pb.HeartbeatRequest", decoded)
	}
	if heartbeat.GetClientTimeMs() != 123 {
		t.Fatalf("client time = %d, want 123", heartbeat.GetClientTimeMs())
	}
}

func TestSystemMessageRegistry(t *testing.T) {
	messages := SystemMessages()
	if len(messages) != 4 {
		t.Fatalf("system messages = %d, want 4", len(messages))
	}

	seen := map[MessageID]bool{}
	for _, msg := range messages {
		if seen[msg.ID] {
			t.Fatalf("duplicate message id: %d", msg.ID)
		}
		seen[msg.ID] = true
		if msg.Owner == "" {
			t.Fatalf("message %s has empty owner", msg.Name)
		}
		if !IsSystemMessageID(msg.ID) {
			t.Fatalf("message id %d is outside system range", msg.ID)
		}
	}

	for _, id := range []MessageID{
		MessageIDHeartbeatRequest,
		MessageIDHeartbeatResponse,
		MessageIDErrorResponse,
		MessageIDProtocolVersionUnsupported,
	} {
		if !seen[id] {
			t.Fatalf("message id %d is missing from registry", id)
		}
	}
}

func TestRoomMessageRegistry(t *testing.T) {
	messages := RoomMessages()
	if len(messages) != 15 {
		t.Fatalf("room messages = %d, want 15", len(messages))
	}

	seen := map[MessageID]bool{}
	for _, msg := range messages {
		if seen[msg.ID] {
			t.Fatalf("duplicate message id: %d", msg.ID)
		}
		seen[msg.ID] = true
		if msg.Owner != "room" {
			t.Fatalf("message %s owner = %q, want room", msg.Name, msg.Owner)
		}
		if !IsRoomMessageID(msg.ID) {
			t.Fatalf("message id %d is outside room range", msg.ID)
		}
	}

	for _, id := range []MessageID{
		MessageIDCreateRoomRequest,
		MessageIDCreateRoomResponse,
		MessageIDJoinRoomRequest,
		MessageIDJoinRoomResponse,
		MessageIDSetReadyRequest,
		MessageIDSetReadyResponse,
		MessageIDLeaveRoomRequest,
		MessageIDLeaveRoomResponse,
		MessageIDTransferHostRequest,
		MessageIDTransferHostResponse,
		MessageIDReconnectRoomRequest,
		MessageIDReconnectRoomResponse,
		MessageIDStartRoomRequest,
		MessageIDStartRoomResponse,
		MessageIDRoomSnapshotPushed,
	} {
		if !seen[id] {
			t.Fatalf("message id %d is missing from registry", id)
		}
	}
}

func TestAccountMessageRegistry(t *testing.T) {
	messages := AccountMessages()
	if len(messages) != 10 {
		t.Fatalf("account messages = %d, want 10", len(messages))
	}

	seen := map[MessageID]bool{}
	for _, msg := range messages {
		if seen[msg.ID] {
			t.Fatalf("duplicate message id: %d", msg.ID)
		}
		seen[msg.ID] = true
		if msg.Owner != "account" {
			t.Fatalf("message %s owner = %q, want account", msg.Name, msg.Owner)
		}
		if !IsAccountMessageID(msg.ID) {
			t.Fatalf("message id %d is outside account range", msg.ID)
		}
	}

	for _, id := range []MessageID{
		MessageIDRegisterRequest,
		MessageIDRegisterResponse,
		MessageIDLoginRequest,
		MessageIDLoginResponse,
		MessageIDLogoutRequest,
		MessageIDLogoutResponse,
		MessageIDResumeSessionRequest,
		MessageIDResumeSessionResponse,
		MessageIDGetCurrentPlayerRequest,
		MessageIDGetCurrentPlayerResponse,
	} {
		if !seen[id] {
			t.Fatalf("message id %d is missing from registry", id)
		}
	}
}

func TestBuildAndDecodeRoomEnvelope(t *testing.T) {
	envelope, err := BuildEnvelope(BuildOptions{
		ProtocolVersion: MaxSupportedVersion,
		MessageID:       MessageIDCreateRoomRequest,
		RequestID:       "req-room",
		Sequence:        1,
	}, &pb.CreateRoomRequest{
		PlayerId: "player-1",
		RoomName: "test room",
		Capacity: 4,
	})
	if err != nil {
		t.Fatalf("BuildEnvelope returned error: %v", err)
	}

	decoded, err := DecodeEnvelope(envelope, true)
	if err != nil {
		t.Fatalf("DecodeEnvelope returned error: %v", err)
	}
	request, ok := decoded.(*pb.CreateRoomRequest)
	if !ok {
		t.Fatalf("decoded message type = %T, want *pb.CreateRoomRequest", decoded)
	}
	if request.GetPlayerId() != "player-1" {
		t.Fatalf("PlayerId = %q, want player-1", request.GetPlayerId())
	}
}

func TestBuildAndDecodeAccountEnvelope(t *testing.T) {
	envelope, err := BuildEnvelope(BuildOptions{
		ProtocolVersion: MaxSupportedVersion,
		MessageID:       MessageIDRegisterRequest,
		RequestID:       "req-account",
		Sequence:        1,
	}, &pb.RegisterRequest{
		Account:     "tester",
		Password:    "secret",
		DisplayName: "Tester",
	})
	if err != nil {
		t.Fatalf("BuildEnvelope returned error: %v", err)
	}

	decoded, err := DecodeEnvelope(envelope, true)
	if err != nil {
		t.Fatalf("DecodeEnvelope returned error: %v", err)
	}
	request, ok := decoded.(*pb.RegisterRequest)
	if !ok {
		t.Fatalf("decoded message type = %T, want *pb.RegisterRequest", decoded)
	}
	if request.GetAccount() != "tester" {
		t.Fatalf("Account = %q, want tester", request.GetAccount())
	}
}

func TestRequestIDRequired(t *testing.T) {
	envelope, err := BuildEnvelope(BuildOptions{
		ProtocolVersion: MaxSupportedVersion,
		MessageID:       MessageIDHeartbeatRequest,
		Sequence:        1,
	}, &pb.HeartbeatRequest{ClientTimeMs: 123})
	if err != nil {
		t.Fatalf("BuildEnvelope returned error: %v", err)
	}

	_, err = DecodeEnvelope(envelope, true)
	if !errors.Is(err, ErrRequestIDRequired) {
		t.Fatalf("DecodeEnvelope error = %v, want ErrRequestIDRequired", err)
	}
}

func TestAccountErrorCodeValues(t *testing.T) {
	tests := map[string]struct {
		got  ErrorCode
		want pb.ErrorCode
	}{
		"account already exists": {
			got:  ErrorCodeAccountAlreadyExists,
			want: pb.ErrorCode_ERROR_CODE_ACCOUNT_ALREADY_EXISTS,
		},
		"credential invalid": {
			got:  ErrorCodeAccountCredentialInvalid,
			want: pb.ErrorCode_ERROR_CODE_ACCOUNT_CREDENTIAL_INVALID,
		},
		"session invalid": {
			got:  ErrorCodeSessionInvalid,
			want: pb.ErrorCode_ERROR_CODE_SESSION_INVALID,
		},
		"unauthenticated": {
			got:  ErrorCodeUnauthenticated,
			want: pb.ErrorCode_ERROR_CODE_UNAUTHENTICATED,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if pb.ErrorCode(tt.got) != tt.want {
				t.Fatalf("error code = %s, want %s", pb.ErrorCode(tt.got), tt.want)
			}
		})
	}
}

func TestErrorEnvelopeKeepsRequestID(t *testing.T) {
	envelope, err := BuildErrorEnvelope("req-error", 12, ErrorCodePayloadInvalid, "payload invalid", "missing payload")
	if err != nil {
		t.Fatalf("BuildErrorEnvelope returned error: %v", err)
	}

	if envelope.GetRequestId() != "req-error" {
		t.Fatalf("request id = %q, want req-error", envelope.GetRequestId())
	}

	decoded, err := DecodeEnvelope(envelope, true)
	if err != nil {
		t.Fatalf("DecodeEnvelope returned error: %v", err)
	}
	response, ok := decoded.(*pb.ErrorResponse)
	if !ok {
		t.Fatalf("decoded message type = %T, want *pb.ErrorResponse", decoded)
	}
	if response.GetCode() != pb.ErrorCode_ERROR_CODE_PAYLOAD_INVALID {
		t.Fatalf("error code = %s, want ERROR_CODE_PAYLOAD_INVALID", response.GetCode())
	}
}

func TestUnsupportedMessageID(t *testing.T) {
	_, err := BuildEnvelope(BuildOptions{
		ProtocolVersion: MaxSupportedVersion,
		MessageID:       MessageID(999),
		RequestID:       "req-1",
		Sequence:        1,
	}, &pb.HeartbeatRequest{ClientTimeMs: 123})
	if !errors.Is(err, ErrMessageIDUnsupported) {
		t.Fatalf("BuildEnvelope error = %v, want ErrMessageIDUnsupported", err)
	}
}

func TestBuildEnvelopeRejectsMismatchedMessageType(t *testing.T) {
	_, err := BuildEnvelope(BuildOptions{
		ProtocolVersion: MaxSupportedVersion,
		MessageID:       MessageIDHeartbeatRequest,
		RequestID:       "req-1",
		Sequence:        1,
	}, &pb.ErrorResponse{Code: pb.ErrorCode_ERROR_CODE_PAYLOAD_INVALID})
	if !errors.Is(err, ErrPayloadInvalid) {
		t.Fatalf("BuildEnvelope error = %v, want ErrPayloadInvalid", err)
	}
}
