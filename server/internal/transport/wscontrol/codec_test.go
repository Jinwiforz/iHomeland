package wscontrol

import (
	"bytes"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/contract"
	commonv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/common/v1"
	controlv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/control/v1"
	visitv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/visit/v1"
	worldv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/world/v1"
	"github.com/jinwiforz/ihomeland/server/internal/protocol"
	"google.golang.org/protobuf/proto"
)

// TestCodecEncodesAllRegisteredPushes 验证9类PUSH、sequence、timestamp与确定性编码。
func TestCodecEncodesAllRegisteredPushes(t *testing.T) {
	clock := testClock{now: time.UnixMilli(1700000000123)}
	codec, err := NewCodec(contract.WSSPushCatalog(), 1<<20, clock)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		id      uint32
		payload proto.Message
	}{
		{500, controlv1.MaintenancePush_builder{}.Build()},
		{501, controlv1.ForcedLogoutPush_builder{}.Build()},
		{502, controlv1.QueueStatusPush_builder{}.Build()},
		{503, controlv1.EndpointUpdatePush_builder{}.Build()},
		{504, controlv1.SessionInvalidatedPush_builder{}.Build()},
		{2003, worldv1.WorldAssignmentChangedPush_builder{}.Build()},
		{2100, visitv1.VisitInvitePush_builder{}.Build()},
		{2101, visitv1.VisitOwnerAvailabilityPush_builder{}.Build()},
		{2102, visitv1.VisitClosedNoticePush_builder{}.Build()},
	}
	for _, test := range cases {
		first, err := codec.Encode(test.id, test.payload, 7)
		if err != nil {
			t.Fatalf("encode %d: %v", test.id, err)
		}
		second, err := codec.Encode(test.id, test.payload, 7)
		if err != nil || !bytes.Equal(first.bytes, second.bytes) {
			t.Fatalf("message %d is not deterministic: %v", test.id, err)
		}
		envelope, err := protocol.UnmarshalEnvelope(first.bytes)
		if err != nil {
			t.Fatalf("decode %d: %v", test.id, err)
		}
		if envelope.GetMessageId() != test.id || envelope.GetKind() != commonv1.MessageKind_MESSAGE_KIND_PUSH || envelope.GetSequence() != 7 || envelope.GetTimestampMs() != clock.now.UnixMilli() {
			t.Fatalf("message %d envelope metadata drifted", test.id)
		}
	}
}

// TestCodecFailsClosed 验证unknown、wrong type、零sequence与route预算超限均被拒绝。
func TestCodecFailsClosed(t *testing.T) {
	codec, err := NewCodec(contract.WSSPushCatalog(), 1<<20, testClock{now: time.UnixMilli(1)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codec.Encode(999, controlv1.MaintenancePush_builder{}.Build(), 1); err == nil {
		t.Fatal("unknown message was accepted")
	}
	if _, err := codec.Encode(500, controlv1.QueueStatusPush_builder{}.Build(), 1); err == nil {
		t.Fatal("wrong payload type was accepted")
	}
	if _, err := codec.Encode(500, controlv1.MaintenancePush_builder{}.Build(), 0); err == nil {
		t.Fatal("zero sequence was accepted")
	}
	large := controlv1.MaintenancePush_builder{MessageKey: proto.String(string(bytes.Repeat([]byte{'x'}, 5000)))}.Build()
	if _, err := codec.Encode(500, large, 1); err == nil {
		t.Fatal("oversized route payload was accepted")
	}
}

// FuzzCodecRejectsUnknownMessageID 保持不受信message ID不能选择未登记route。
func FuzzCodecRejectsUnknownMessageID(f *testing.F) {
	for _, seed := range []uint32{0, 1, 499, 505, 2000, 2103, ^uint32(0)} {
		f.Add(seed)
	}
	codec, err := NewCodec(contract.WSSPushCatalog(), 1<<20, testClock{now: time.UnixMilli(1)})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, messageID uint32) {
		for _, allowed := range []uint32{500, 501, 502, 503, 504, 2003, 2100, 2101, 2102} {
			if messageID == allowed {
				t.Skip()
			}
		}
		if _, err := codec.Encode(messageID, controlv1.MaintenancePush_builder{}.Build(), 1); err == nil {
			t.Fatal("unknown message ID was accepted")
		}
	})
}
