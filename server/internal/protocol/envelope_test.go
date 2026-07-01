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
