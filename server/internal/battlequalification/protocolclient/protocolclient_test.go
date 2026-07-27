package protocolclient

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/netip"
	"os"
	"testing"
	"time"
)

// testBuildIdentity 返回公开、固定的 child build fixture。
func testBuildIdentity() [buildIdentityBytes]byte {
	var identity [buildIdentityBytes]byte
	for index := range identity {
		identity[index] = byte(index + 1)
	}
	return identity
}

// testCredential 返回可验证 cleanup 的一次性 credential fixture。
func testCredential() *SessionCredential {
	credential := &SessionCredential{}
	for index := range credential.TicketID {
		credential.TicketID[index] = byte(0x10 + index)
	}
	for index := range credential.TicketSecret {
		credential.TicketSecret[index] = byte(0x20 + index)
	}
	return credential
}

// allZero 报告 credential 是否已经由 consumer 完整清零。
func allZero(credential *SessionCredential) bool {
	var aggregate byte
	for _, value := range credential.TicketID {
		aggregate |= value
	}
	for _, value := range credential.TicketSecret {
		aggregate |= value
	}
	return aggregate == 0
}

// TestFrameRoundTripAndNegative 验证 request/receipt framing、deadline 与 closed kind。
func TestFrameRoundTripAndNegative(t *testing.T) {
	now := time.Now()
	var request bytes.Buffer
	if err := writeFrame(&request, Frame{
		Kind: KindHandshakeAttackRequest, Sequence: 7, Deadline: now.Add(time.Second),
		Payload: []byte{1, 2, 3},
	}, now); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}
	decoded, err := readRequest(&request, now)
	if err != nil || decoded.Kind != KindHandshakeAttackRequest ||
		decoded.Sequence != 7 || !bytes.Equal(decoded.Payload, []byte{1, 2, 3}) {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
	clear(decoded.Payload)

	var receipt bytes.Buffer
	if err := writeReceipt(
		&receipt,
		KindHandshakeAttackReceipt,
		7,
		[]byte{4},
	); err != nil {
		t.Fatalf("writeReceipt: %v", err)
	}
	decoded, err = readFrame(&receipt)
	if err != nil || decoded.Kind != KindHandshakeAttackReceipt ||
		decoded.Sequence != 7 || !bytes.Equal(decoded.Payload, []byte{4}) {
		t.Fatalf("receipt=%+v err=%v", decoded, err)
	}
	clear(decoded.Payload)

	if err := writeFrame(io.Discard, Frame{
		Kind: KindPollRequest, Sequence: 1, Deadline: now,
	}, now); err != ErrInvalidFrame {
		t.Fatalf("expired deadline err=%v", err)
	}
}

// TestSessionStartConsumesCredential 验证 fixed payload 与所有失败路径 secret cleanup。
func TestSessionStartConsumesCredential(t *testing.T) {
	credential := testCredential()
	payload, err := encodeSessionStart(SessionStart{
		ClientSlot:            1,
		Endpoint:              netip.MustParseAddrPort("127.0.0.1:58445"),
		Credential:            credential,
		TicketExpiresAtUnixMS: 1_700_000_003_000,
	})
	if err != nil {
		t.Fatalf("encodeSessionStart: %v", err)
	}
	defer clear(payload)
	if len(payload) != sessionStartPayloadBytes ||
		payload[0] != 1 || payload[1] != addressFamilyIPv4 ||
		binary.BigEndian.Uint16(payload[2:4]) != 58_445 ||
		binary.BigEndian.Uint64(payload[68:76]) != 1_700_000_003_000 {
		t.Fatalf("session payload header drifted")
	}
	if !allZero(credential) {
		t.Fatal("successful encode retained credential")
	}
	invalid := testCredential()
	if _, err := encodeSessionStart(SessionStart{Credential: invalid}); err == nil {
		t.Fatal("invalid session start was accepted")
	}
	if !allZero(invalid) {
		t.Fatal("failed encode retained credential")
	}
}

// TestSessionStartFailureRegistry 验证 child 失败 receipt 只产生闭合低敏阶段。
func TestSessionStartFailureRegistry(t *testing.T) {
	for failure := SessionStartFailureInvalidRequest; failure <= SessionStartFailureUnknown; failure++ {
		if !failure.Valid() || failure.String() == "invalid" {
			t.Fatalf("failure=%d valid=%t label=%q", failure, failure.Valid(), failure.String())
		}
		message := (&SessionStartError{Failure: failure}).Error()
		if !bytes.Contains([]byte(message), []byte(failure.String())) {
			t.Fatalf("failure message lost stable label: %q", message)
		}
	}
	if SessionStartFailure(0).Valid() || SessionStartFailure(11).Valid() {
		t.Fatal("unknown session-start failure entered closed registry")
	}
}

// TestSupervisorProcessContract 验证 identity、一次性 secret、receipt 与正常 shutdown。
func TestSupervisorProcessContract(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}
	startupContext, cancelStartup := context.WithTimeout(context.Background(), 5*time.Second)
	supervisor, err := startWithArguments(
		startupContext,
		Config{
			ExecutablePath:        executable,
			ExpectedBuildIdentity: testBuildIdentity(),
		},
		[]string{"-test.run=^TestProtocolClientHelperProcess$"},
		[]string{"IHOMELAND_PROTOCOL_CLIENT_HELPER=1"},
	)
	if err != nil {
		t.Fatalf("startWithArguments: %v", err)
	}
	cancelStartup()
	credential := testCredential()
	requestContext, cancelRequest := context.WithTimeout(context.Background(), time.Second)
	receipt, err := supervisor.StartSession(requestContext, SessionStart{
		ClientSlot:            1,
		Endpoint:              netip.MustParseAddrPort("127.0.0.1:58445"),
		Credential:            credential,
		TicketExpiresAtUnixMS: uint64(time.Now().Add(time.Minute).UnixMilli()),
	})
	cancelRequest()
	if err != nil || receipt.ClientSlot != 1 {
		t.Fatalf("StartSession receipt=%+v err=%v", receipt, err)
	}
	if !allZero(credential) {
		t.Fatal("supervisor retained credential")
	}
	second := testCredential()
	requestContext, cancelRequest = context.WithTimeout(context.Background(), time.Second)
	_, err = supervisor.StartSession(requestContext, SessionStart{Credential: second})
	cancelRequest()
	if !errors.Is(err, ErrCredentialAlreadyDelivered) || !allZero(second) {
		t.Fatalf("second credential err=%v zero=%v", err, allZero(second))
	}
	closeContext, cancelClose := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelClose()
	if err := supervisor.Close(closeContext); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if supervisor.HadStderr() {
		t.Fatal("helper unexpectedly wrote stderr")
	}
}

// TestSupervisorStartupRollbackWaitsForChild 验证 identity 失败在返回前同步回收 child。
func TestSupervisorStartupRollbackWaitsForChild(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}
	expected := testBuildIdentity()
	expected[0] ^= 0xff
	processContext, cancelProcess := context.WithTimeout(
		context.Background(),
		startupRollbackTimeout,
	)
	defer cancelProcess()
	startedAt := time.Now()
	supervisor, err := startWithArguments(
		processContext,
		Config{
			ExecutablePath:        executable,
			ExpectedBuildIdentity: expected,
		},
		[]string{"-test.run=^TestProtocolClientHelperProcess$"},
		[]string{"IHOMELAND_PROTOCOL_CLIENT_HELPER=1"},
	)
	if supervisor != nil || !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("startup result supervisor=%v err=%v", supervisor, err)
	}
	if time.Since(startedAt) >= startupRollbackTimeout {
		t.Fatal("startup rollback consumed the complete hard deadline")
	}
}

// FuzzReadFrameRejectsMalformedInput 验证任意 stdin bytes 不会绕过 closed frame ceiling。
func FuzzReadFrameRejectsMalformedInput(f *testing.F) {
	var valid bytes.Buffer
	if err := writeReceipt(
		&valid,
		KindPollReceipt,
		1,
		[]byte{1},
	); err != nil {
		f.Fatalf("seed receipt: %v", err)
	}
	f.Add(valid.Bytes())
	f.Add([]byte{})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})
	f.Fuzz(func(t *testing.T, input []byte) {
		frame, err := readFrame(bytes.NewReader(input))
		if err != nil {
			return
		}
		defer clear(frame.Payload)
		if !receiptKind(frame.Kind) ||
			frame.Sequence == 0 ||
			len(frame.Payload) > maximumFrameBytes-frameHeaderBytes {
			t.Fatalf("malformed frame decoded: %+v", frame)
		}
	})
}

// TestProtocolClientHelperProcess 是 supervisor test 拥有的独立 child contract fixture。
func TestProtocolClientHelperProcess(t *testing.T) {
	if os.Getenv("IHOMELAND_PROTOCOL_CLIENT_HELPER") != "1" {
		return
	}
	describe, err := readRequest(os.Stdin, time.Now())
	if err != nil || describe.Kind != KindDescribeRequest {
		t.Fatalf("describe=%+v err=%v", describe, err)
	}
	var description [describePayloadBytes]byte
	identity := testBuildIdentity()
	copy(description[:buildIdentityBytes], identity[:])
	description[buildIdentityBytes] = contractVersion
	binary.BigEndian.PutUint32(description[buildIdentityBytes+1:], maximumFrameBytes)
	if err := writeReceipt(os.Stdout, KindDescribeReceipt, describe.Sequence, description[:]); err != nil {
		t.Fatalf("describe receipt: %v", err)
	}

	start, err := readRequest(os.Stdin, time.Now())
	if err != nil || start.Kind != KindSessionStartRequest ||
		len(start.Payload) != sessionStartPayloadBytes {
		t.Fatalf("start kind=%d bytes=%d err=%v", start.Kind, len(start.Payload), err)
	}
	var proofAggregate byte
	for _, value := range start.Payload[36:68] {
		proofAggregate |= value
	}
	clientSlot := start.Payload[0]
	clear(start.Payload)
	if proofAggregate == 0 {
		t.Fatal("helper did not receive proof material")
	}
	if err := writeReceipt(
		os.Stdout,
		KindSessionEvent,
		start.Sequence,
		[]byte{clientSlot, sessionEstablishedEvent},
	); err != nil {
		t.Fatalf("session receipt: %v", err)
	}

	shutdown, err := readRequest(os.Stdin, time.Now())
	if err != nil || shutdown.Kind != KindShutdownRequest {
		t.Fatalf("shutdown=%+v err=%v", shutdown, err)
	}
	if err := writeReceipt(os.Stdout, KindShutdownReceipt, shutdown.Sequence, nil); err != nil {
		t.Fatalf("shutdown receipt: %v", err)
	}
}
