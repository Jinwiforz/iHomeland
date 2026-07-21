package tcpgameplay

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/contract"
	commonv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/common/v1"
	visitv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/visit/v1"
	worldv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/world/v1"
	"github.com/jinwiforz/ihomeland/server/internal/protocol"
	"github.com/jinwiforz/ihomeland/server/internal/worldadmission"
	"google.golang.org/protobuf/proto"
)

// dispatcherApplication 为route catalog测试返回精确generated response。
type dispatcherApplication struct {
	// worldError 模拟安全application错误。
	worldError error
	// panicWorld 模拟application缺陷。
	panicWorld bool
	// worldBlock 模拟遵守context的慢依赖。
	worldBlock <-chan struct{}
}

// WorldSnapshot 返回空但类型精确的response。
func (application *dispatcherApplication) WorldSnapshot(ctx context.Context, _ OperationContext, _ *worldv1.WorldSnapshotRequest) (*worldv1.WorldSnapshotResponse, error) {
	if application.panicWorld {
		panic("sensitive backend panic")
	}
	if application.worldBlock != nil {
		select {
		case <-application.worldBlock:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
	return worldv1.WorldSnapshotResponse_builder{}.Build(), application.worldError
}

// VisitSnapshot 返回类型精确的response。
func (*dispatcherApplication) VisitSnapshot(context.Context, OperationContext, *visitv1.VisitSnapshotRequest) (*visitv1.VisitSnapshotResponse, error) {
	return visitv1.VisitSnapshotResponse_builder{}.Build(), nil
}

// VisitOpen 返回类型精确的response。
func (*dispatcherApplication) VisitOpen(context.Context, OperationContext, *visitv1.VisitOpenCommand, []byte) (*visitv1.VisitOpenResponse, error) {
	return visitv1.VisitOpenResponse_builder{}.Build(), nil
}

// VisitCreateInvite 返回类型精确的response。
func (*dispatcherApplication) VisitCreateInvite(context.Context, OperationContext, *visitv1.VisitCreateInviteCommand, []byte) (*visitv1.VisitCreateInviteResponse, error) {
	return visitv1.VisitCreateInviteResponse_builder{}.Build(), nil
}

// VisitRevokeInvite 返回类型精确的response。
func (*dispatcherApplication) VisitRevokeInvite(context.Context, OperationContext, *visitv1.VisitRevokeInviteCommand, []byte) (*visitv1.VisitRevokeInviteResponse, error) {
	return visitv1.VisitRevokeInviteResponse_builder{}.Build(), nil
}

// VisitJoin 返回类型精确的response。
func (*dispatcherApplication) VisitJoin(context.Context, OperationContext, worldadmission.Qualification, *visitv1.VisitJoinCommand, []byte) (*visitv1.VisitJoinResponse, error) {
	return visitv1.VisitJoinResponse_builder{}.Build(), nil
}

// VisitLeave 返回类型精确的response。
func (*dispatcherApplication) VisitLeave(context.Context, OperationContext, *visitv1.VisitLeaveCommand, []byte) (*visitv1.VisitLeaveResponse, error) {
	return visitv1.VisitLeaveResponse_builder{}.Build(), nil
}

// VisitKick 返回类型精确的response。
func (*dispatcherApplication) VisitKick(context.Context, OperationContext, *visitv1.VisitKickCommand, []byte) (*visitv1.VisitKickResponse, error) {
	return visitv1.VisitKickResponse_builder{}.Build(), nil
}

// VisitReconnect 返回类型精确的response。
func (*dispatcherApplication) VisitReconnect(context.Context, OperationContext, worldadmission.Qualification, *visitv1.VisitReconnectCommand, []byte) (*visitv1.VisitReconnectResponse, error) {
	return visitv1.VisitReconnectResponse_builder{}.Build(), nil
}

// VisitClose 返回类型精确的response。
func (*dispatcherApplication) VisitClose(context.Context, OperationContext, *visitv1.VisitCloseCommand, []byte) (*visitv1.VisitCloseResponse, error) {
	return visitv1.VisitCloseResponse_builder{}.Build(), nil
}

// TestDispatcherProducesResponseErrorAndPanicBoundary 验证统一handler、correlation和安全错误编码。
func TestDispatcherProducesResponseErrorAndPanicBoundary(t *testing.T) {
	t.Parallel()
	config, codec, registry := testRuntime(t)
	application := new(dispatcherApplication)
	dispatcher, err := NewDispatcher(application, new(Handshake), registry, new(testObserver))
	if err != nil {
		t.Fatal(err)
	}
	entry := &connection{
		id: "tcp_00000000000000000000000000000001", codec: codec, observer: new(testObserver), queue: newSendQueue(8, config.Policy.QueueBytes),
		state: ConnectionStateActive, nextServerSequence: 1, nextClientSequence: 1, routeRates: make(map[string]*routeRateState), done: make(chan struct{}),
	}
	for index, mode := range []string{"success", "public_error", "unregistered_error", "panic"} {
		application.worldError, application.panicWorld = nil, false
		if mode == "public_error" {
			application.worldError = PublicError{Code: 2000, MessageKey: "error.world.not_found"}
		}
		if mode == "unregistered_error" {
			application.worldError = PublicError{Code: 2000, MessageKey: "error.internal", Retryable: true}
		}
		if mode == "panic" {
			application.panicWorld = true
		}
		message := decodeWorldRequest(t, codec, uint64(index+1), byte(index+1))
		if err := dispatcher.Dispatch(context.Background(), entry, message); err != nil {
			t.Fatalf("Dispatch(%s): %v", mode, err)
		}
		queued := <-entry.queue.items
		entry.queue.take(queued)
		if bytes.Contains(queued.encoded.frame, []byte("sensitive backend panic")) {
			t.Fatalf("Dispatch(%s) leaked application panic text", mode)
		}
		envelope, err := protocol.UnmarshalEnvelope(queued.encoded.frame[4:])
		if err != nil || envelope.GetMessageId() != 2001 || !bytes.Equal(envelope.GetRequestId(), message.correlation.RequestID) {
			t.Fatalf("Dispatch(%s) envelope=%v err=%v", mode, envelope, err)
		}
		wantKind := commonv1.MessageKind_MESSAGE_KIND_RESPONSE
		if mode != "success" {
			wantKind = commonv1.MessageKind_MESSAGE_KIND_ERROR
		}
		if envelope.GetKind() != wantKind {
			t.Fatalf("Dispatch(%s) kind=%v want=%v", mode, envelope.GetKind(), wantKind)
		}
		if mode == "unregistered_error" {
			payload := new(commonv1.ErrorPayload)
			if err := proto.Unmarshal(envelope.GetPayload(), payload); err != nil || payload.GetCode() != 501 || payload.GetMessageKey() != "error.internal" || !payload.GetRetryable() {
				t.Fatalf("Dispatch(%s) public error=%v err=%v", mode, payload, err)
			}
		}
	}
}

// TestDispatcherClosesInvalidatedTargetAfterError 验证运行态丢失时先入队安全错误再封闭旧 connection。
func TestDispatcherClosesInvalidatedTargetAfterError(t *testing.T) {
	t.Parallel()
	config, codec, registry := testRuntime(t)
	application := &dispatcherApplication{worldError: PublicError{Code: 2002, MessageKey: "error.world.assignment_stale", CloseConnection: true}}
	dispatcher, err := NewDispatcher(application, new(Handshake), registry, new(testObserver))
	if err != nil {
		t.Fatal(err)
	}
	entry := &connection{
		id: "tcp_00000000000000000000000000000001", codec: codec, observer: new(testObserver), queue: newSendQueue(8, config.Policy.QueueBytes),
		state: ConnectionStateActive, closeClass: CloseClassUnexpected, nextServerSequence: 1, nextClientSequence: 1, routeRates: make(map[string]*routeRateState), done: make(chan struct{}),
	}
	if err := dispatcher.Dispatch(context.Background(), entry, decodeWorldRequest(t, codec, 1, 1)); err != nil {
		t.Fatal(err)
	}
	queued := <-entry.queue.items
	entry.queue.take(queued)
	if !queued.closeAfter || entry.State() != ConnectionStateClosing || entry.closeClass != CloseClassInvalidated {
		t.Fatalf("fail-closed queue=%v state=%v class=%v", queued.closeAfter, entry.State(), entry.closeClass)
	}
	envelope, err := protocol.UnmarshalEnvelope(queued.encoded.frame[4:])
	if err != nil {
		t.Fatal(err)
	}
	payload := new(commonv1.ErrorPayload)
	if err := proto.Unmarshal(envelope.GetPayload(), payload); err != nil {
		t.Fatal(err)
	}
	if payload.GetCode() != 2002 || payload.GetMessageKey() != "error.world.assignment_stale" || payload.GetRetryable() {
		t.Fatalf("fail-closed public error=%v", payload)
	}
}

// TestDispatcherRoutesEveryNonAdmissionOperation 验证集中catalog不会遗漏或串错任一普通C2S operation。
// JOIN与RECONNECT需要不可伪造Qualification，分别由真实storage/TCP集成测试覆盖。
func TestDispatcherRoutesEveryNonAdmissionOperation(t *testing.T) {
	t.Parallel()
	config, codec, registry := testRuntime(t)
	dispatcher, err := NewDispatcher(new(dispatcherApplication), new(Handshake), registry, new(testObserver))
	if err != nil {
		t.Fatal(err)
	}
	entry := &connection{
		id: "tcp_00000000000000000000000000000001", codec: codec, observer: new(testObserver), queue: newSendQueue(32, config.Policy.QueueBytes),
		state: ConnectionStateActive, nextServerSequence: 1, nextClientSequence: 1, routeRates: make(map[string]*routeRateState), done: make(chan struct{}),
	}
	tests := []struct {
		messageID  uint32
		responseID uint32
	}{
		{1, 2},
		{2000, 2001},
		{2103, 2104},
		{2105, 2106},
		{2107, 2108},
		{2111, 2112},
		{2113, 2114},
		{2117, 2118},
		{2119, 2120},
	}
	for index, testCase := range tests {
		route, err := contract.TLSGameplayCatalog().LookupTLSGameplay(testCase.messageID, "CLIENT_TO_SERVER")
		if err != nil {
			t.Fatal(err)
		}
		payload, err := clientPayload(testCase.messageID)
		if err != nil {
			t.Fatal(err)
		}
		identity := bytes.Repeat([]byte{byte(index + 1)}, 16)
		correlation := Correlation{CommandID: identity}
		if route.Kind == "REQUEST" {
			correlation = Correlation{RequestID: identity}
		}
		message := DecodedMessage{route: route, payload: payload, correlation: correlation, sequence: uint64(index + 1)}
		if err := dispatcher.Dispatch(context.Background(), entry, message); err != nil {
			t.Fatalf("Dispatch(%d): %v", testCase.messageID, err)
		}
		queued := <-entry.queue.items
		entry.queue.take(queued)
		envelope, err := protocol.UnmarshalEnvelope(queued.encoded.frame[4:])
		if err != nil || envelope.GetMessageId() != testCase.responseID || envelope.GetKind() != commonv1.MessageKind_MESSAGE_KIND_RESPONSE {
			t.Fatalf("Dispatch(%d) response=%v err=%v", testCase.messageID, envelope, err)
		}
		if route.Kind == "REQUEST" && !bytes.Equal(envelope.GetRequestId(), identity) {
			t.Fatalf("Dispatch(%d) request correlation drifted", testCase.messageID)
		}
		if route.Kind == "COMMAND" && !bytes.Equal(envelope.GetCommandId(), identity) {
			t.Fatalf("Dispatch(%d) command correlation drifted", testCase.messageID)
		}
	}
}

// TestDispatcherResourceGuards 验证returning授权、持续限流与application deadline均fail closed。
func TestDispatcherResourceGuards(t *testing.T) {
	t.Parallel()
	config, codec, registry := testRuntime(t)
	application := new(dispatcherApplication)
	dispatcher, err := NewDispatcher(application, new(Handshake), registry, new(testObserver))
	if err != nil {
		t.Fatal(err)
	}
	entry := &connection{
		id: "tcp_00000000000000000000000000000001", codec: codec, observer: new(testObserver), queue: newSendQueue(32, config.Policy.QueueBytes),
		state: ConnectionStateReturning, nextServerSequence: 1, nextClientSequence: 1, routeRates: make(map[string]*routeRateState), done: make(chan struct{}),
	}
	message := decodeWorldRequest(t, codec, 1, 1)
	if err := dispatcher.Dispatch(context.Background(), entry, message); err != nil {
		t.Fatal(err)
	}
	rejected := <-entry.queue.items
	entry.queue.take(rejected)
	envelope, err := protocol.UnmarshalEnvelope(rejected.encoded.frame[4:])
	if err != nil || envelope.GetKind() != commonv1.MessageKind_MESSAGE_KIND_ERROR {
		t.Fatalf("returning state response=%v err=%v", envelope, err)
	}

	entry.state = ConnectionStateActive
	entry.routeRates = make(map[string]*routeRateState)
	now := testNow
	for range 20 {
		allowed, closeConnection := allowRoute(entry, "world_read", now)
		if !allowed || closeConnection {
			t.Fatal("world_read burst rejected before configured capacity")
		}
	}
	for rejection := 1; rejection <= maximumConsecutiveRateRejections; rejection++ {
		allowed, closeConnection := allowRoute(entry, "world_read", now)
		if allowed || closeConnection != (rejection == maximumConsecutiveRateRejections) {
			t.Fatalf("world_read rejection=%d allowed=%v close=%v", rejection, allowed, closeConnection)
		}
	}
	allowed, closeConnection := allowRoute(entry, "world_read", now.Add(time.Minute))
	if !allowed || closeConnection || entry.consecutiveRateRejections != 0 {
		t.Fatal("world_read rate budget did not refill")
	}

	entry.routeRates = make(map[string]*routeRateState)
	application.worldBlock = make(chan struct{})
	timed := decodeWorldRequest(t, codec, 3, 3)
	timed.route.TimeoutMS = 1
	if err := dispatcher.Dispatch(context.Background(), entry, timed); err != nil {
		t.Fatal(err)
	}
	timedOut := <-entry.queue.items
	entry.queue.take(timedOut)
	timeoutEnvelope, _ := protocol.UnmarshalEnvelope(timedOut.encoded.frame[4:])
	if timeoutEnvelope.GetKind() != commonv1.MessageKind_MESSAGE_KIND_ERROR {
		t.Fatalf("application timeout response=%v", timeoutEnvelope)
	}
}

// decodeWorldRequest 构造并通过真实codec解码一条WorldSnapshot请求。
func decodeWorldRequest(t testing.TB, codec *Codec, sequence uint64, identityByte byte) DecodedMessage {
	t.Helper()
	payload, _ := proto.Marshal(worldv1.WorldSnapshotRequest_builder{}.Build())
	kind := commonv1.MessageKind_MESSAGE_KIND_REQUEST
	envelope := commonv1.ReliableEnvelope_builder{
		ProtocolVersion: proto.Uint32(1), MessageId: proto.Uint32(2000), Kind: &kind,
		RequestId: bytes.Repeat([]byte{identityByte}, 16), Sequence: proto.Uint64(sequence), TimestampMs: proto.Int64(1), Payload: payload,
	}.Build()
	encoded, err := protocol.MarshalEnvelope(envelope)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := codec.Decode(encoded, sequence)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}
