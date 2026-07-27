package protocolclient

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"testing"
	"time"
)

// TestOperationPayloads 验证三类 request 的 exact field table 与边界拒绝。
func TestOperationPayloads(t *testing.T) {
	workload, err := encodeWorkloadCommand(WorkloadCommand{
		ClientSlot:          2,
		Operation:           WorkloadInputBundle,
		CommandKind:         1,
		RepeatCount:         3,
		ApplicationSequence: 4,
		ApplicationTick:     5,
		ValueA:              -1000,
		ValueB:              1000,
		Delivery:            DeliveryTamperedTag,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(workload) != workloadCommandPayloadBytes ||
		workload[0] != 2 || workload[1] != 1 ||
		workload[2] != 1 || workload[3] != 3 ||
		binary.BigEndian.Uint64(workload[4:12]) != 4 ||
		binary.BigEndian.Uint64(workload[12:20]) != 5 ||
		int32(binary.BigEndian.Uint32(workload[20:24])) != -1000 ||
		int32(binary.BigEndian.Uint32(workload[24:28])) != 1000 ||
		workload[28] != byte(DeliveryTamperedTag) ||
		workload[29] != 0 || workload[30] != 0 ||
		workload[31] != 0 {
		t.Fatalf("workload payload drifted: %x", workload)
	}
	if _, err := encodeWorkloadCommand(WorkloadCommand{
		ClientSlot: 1, Operation: WorkloadProbe, CommandKind: 1,
		RepeatCount: 1, ApplicationSequence: 1, ApplicationTick: 1,
	}); !errors.Is(err, ErrInvalidFrame) {
		t.Fatalf("probe accepted an input command kind: %v", err)
	}

	transition, err := encodeNetworkTransition(NetworkTransition{
		ClientSlot: 3,
		Operation:  NetworkRekey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(transition) != networkTransitionPayloadBytes ||
		transition[0] != 3 || transition[1] != 2 ||
		binary.BigEndian.Uint64(transition[8:16]) != 0 ||
		binary.BigEndian.Uint64(transition[16:24]) != 0 {
		t.Fatalf("network transition payload drifted: %x", transition)
	}
	rebind, err := encodeNetworkTransition(NetworkTransition{
		ClientSlot: 1, Operation: NetworkRebind,
		AdvertisedEndpoint: netip.MustParseAddrPort("127.0.0.1:58446"),
	})
	if err != nil || rebind[2] != addressFamilyIPv4 ||
		binary.BigEndian.Uint16(rebind[4:6]) != 58446 ||
		rebind[18] != 0xff || rebind[19] != 0xff ||
		validateRebindTransition(rebind) != nil {
		t.Fatalf("rebind transition=%x err=%v", rebind, err)
	}

	poll, err := encodePoll(Poll{
		ClientSlot:    4,
		MaximumEvents: 8,
		Wait:          250 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(poll) != pollPayloadBytes || poll[0] != 4 ||
		poll[1] != 8 || binary.BigEndian.Uint16(poll[2:4]) != 0 ||
		binary.BigEndian.Uint32(poll[4:8]) != 250 {
		t.Fatalf("poll payload drifted: %x", poll)
	}
}

// validateRebindTransition 为测试显式检查全部 reserved bytes。
func validateRebindTransition(payload []byte) error {
	if payload[3] != 0 || payload[6] != 0 || payload[7] != 0 {
		return errors.New("reserved transition bytes drifted")
	}
	return nil
}

// TestOperationReceipts 验证低敏 receipt 只接受闭合字段和值域。
func TestOperationReceipts(t *testing.T) {
	workload := make([]byte, workloadEventPayloadBytes)
	workload[0] = 1
	workload[1] = byte(WorkloadSnapshotReceived)
	workload[3] = 2
	binary.BigEndian.PutUint64(workload[4:12], 3)
	binary.BigEndian.PutUint64(workload[12:20], 4)
	binary.BigEndian.PutUint64(workload[20:28], 5)
	event, err := decodeWorkloadEvent(workload)
	if err != nil || event.Kind != WorkloadSnapshotReceived ||
		event.Count != 2 || event.PacketSequence != 3 ||
		event.ApplicationSequence != 4 || event.ApplicationTick != 5 {
		t.Fatalf("workload event=%+v err=%v", event, err)
	}
	workload[31] = 1
	if _, err := decodeWorkloadEvent(workload); !errors.Is(err, ErrUnexpectedReceipt) {
		t.Fatalf("workload event accepted reserved drift: %v", err)
	}
	clear(workload)
	workload[0] = 1
	workload[1] = byte(WorkloadReliableQueued)
	workload[2] = byte(WorkloadResyncRequest)
	binary.BigEndian.PutUint64(workload[12:20], 1)
	binary.BigEndian.PutUint64(workload[20:28], 2)
	queued, err := decodeWorkloadEvent(workload)
	if err != nil || queued.Kind != WorkloadReliableQueued ||
		queued.Count != 0 {
		t.Fatalf("queued workload event=%+v err=%v", queued, err)
	}

	transition := make([]byte, networkTransitionEventPayloadBytes)
	transition[0] = 1
	transition[1] = byte(NetworkRebind)
	transition[2] = 1
	binary.BigEndian.PutUint32(transition[4:8], 2)
	transitionEvent, err := decodeNetworkTransitionEvent(transition)
	if err != nil || !transitionEvent.Committed ||
		transitionEvent.Generation != 2 {
		t.Fatalf("transition event=%+v err=%v", transitionEvent, err)
	}
	transition[1] = byte(NetworkClose)
	if _, err := decodeNetworkTransitionEvent(transition); !errors.Is(err, ErrUnexpectedReceipt) {
		t.Fatalf("close accepted nonzero generation: %v", err)
	}
	clear(transition)
	transition[0] = 1
	transition[1] = byte(NetworkRebind)
	transition[3] = byte(TransitionFailureRebindChallengeReceive)
	if _, err := decodeNetworkTransitionEvent(transition); err == nil {
		t.Fatal("transition failure receipt was accepted as success")
	} else {
		var failure NetworkTransitionFailure
		if !errors.As(err, &failure) ||
			failure.Operation != NetworkRebind ||
			failure.Code != TransitionFailureRebindChallengeReceive ||
			failure.QualificationFailureCode() !=
				"client-transition-rebind-challenge-receive" {
			t.Fatalf("transition failure=%T %v", err, err)
		}
	}
	transition[1] = byte(NetworkClose)
	if _, err := decodeNetworkTransitionEvent(transition); !errors.Is(
		err,
		ErrUnexpectedReceipt,
	) {
		t.Fatalf("close accepted rebind failure code: %v", err)
	}

	poll := make([]byte, pollReceiptPayloadBytes)
	poll[0] = 1
	poll[1] = 2
	binary.BigEndian.PutUint64(poll[8:16], 3)
	binary.BigEndian.PutUint64(poll[16:24], 4)
	binary.BigEndian.PutUint64(poll[24:32], 5)
	binary.BigEndian.PutUint64(poll[32:40], 6)
	binary.BigEndian.PutUint64(poll[40:48], 7)
	binary.BigEndian.PutUint64(poll[48:56], 8)
	binary.BigEndian.PutUint64(poll[56:64], 9)
	binary.BigEndian.PutUint64(poll[64:72], 10)
	binary.BigEndian.PutUint64(poll[72:80], 11)
	binary.BigEndian.PutUint64(poll[80:88], 11)
	binary.BigEndian.PutUint64(poll[88:96], 12)
	binary.BigEndian.PutUint64(poll[96:104], 13)
	binary.BigEndian.PutUint64(poll[104:112], 14)
	binary.BigEndian.PutUint64(poll[112:120], 15)
	binary.BigEndian.PutUint64(poll[120:128], 1)
	binary.BigEndian.PutUint64(poll[128:136], 2)
	binary.BigEndian.PutUint64(poll[136:144], 3)
	pollReceipt, err := decodePollReceipt(poll)
	if err != nil || pollReceipt.EventCount != 2 ||
		pollReceipt.SnapshotCount != 3 ||
		pollReceipt.ReliableCount != 4 ||
		pollReceipt.LatestServerTick != 5 ||
		pollReceipt.LatestApplicationSequence != 6 ||
		pollReceipt.LatestSnapshotSequence != 7 ||
		pollReceipt.BaselineID != 8 ||
		pollReceipt.LastProcessedInputTick != 9 ||
		pollReceipt.BaselineGapCount != 10 ||
		pollReceipt.KCP.SecureDatagrams != 11 ||
		pollReceipt.KCP.InputDatagrams != 11 ||
		pollReceipt.KCP.InputACKCommands != 12 ||
		pollReceipt.KCP.InputPushCommands != 13 ||
		pollReceipt.KCP.OutputDatagrams != 14 ||
		pollReceipt.KCP.ReconciledMessages != 15 ||
		pollReceipt.KCP.QueuedMessages != 1 ||
		pollReceipt.KCP.InflightMessages != 2 ||
		pollReceipt.KCP.WaitingSegments != 3 {
		t.Fatalf("poll receipt=%+v err=%v", pollReceipt, err)
	}
	clear(poll)
	poll[0] = 1
	poll[2] = byte(PollFailureSnapshot)
	_, err = decodePollReceipt(poll)
	var pollFailure PollFailure
	if !errors.As(err, &pollFailure) ||
		pollFailure.Code != PollFailureSnapshot ||
		pollFailure.QualificationFailureCode() != "client-poll-snapshot" {
		t.Fatalf("poll failure=%+v err=%v", pollFailure, err)
	}
	clear(poll)
	poll[0] = 1
	poll[2] = byte(PollFailureKCPInflightExpiry)
	poll[3] = byte(ClientKCPInflightExpired)
	binary.BigEndian.PutUint64(poll[72:80], 5)
	binary.BigEndian.PutUint64(poll[80:88], 5)
	binary.BigEndian.PutUint64(poll[88:96], 4)
	binary.BigEndian.PutUint64(poll[104:112], 6)
	binary.BigEndian.PutUint64(poll[112:120], 3)
	binary.BigEndian.PutUint64(poll[128:136], 1)
	binary.BigEndian.PutUint64(poll[136:144], 1)
	_, err = decodePollReceipt(poll)
	if !errors.As(err, &pollFailure) ||
		pollFailure.Code != PollFailureKCPInflightExpiry ||
		pollFailure.Receipt.ClientSlot != 1 ||
		pollFailure.Receipt.KCP.InputACKCommands != 4 ||
		pollFailure.Receipt.KCP.ReconciledMessages != 3 ||
		pollFailure.Receipt.KCP.InflightMessages != 1 ||
		pollFailure.QualificationFailureCode() != "client-poll-kcp-inflight-expiry" {
		t.Fatalf("KCP expiry failure=%+v err=%v", pollFailure, err)
	}
	poll[1] = 1
	if _, err := decodePollReceipt(poll); !errors.Is(err, ErrUnexpectedReceipt) {
		t.Fatalf("poll failure accepted runtime fields: %v", err)
	}
}
