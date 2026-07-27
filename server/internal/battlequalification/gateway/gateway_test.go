package gateway

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"
)

// echoBackend 启动只回送 opaque bytes 的 loopback UDP fixture。
type echoBackend struct {
	// connection 是 gateway 唯一允许连接的 backend。
	connection *net.UDPConn
	// remotes 记录每个 upstream mapping 的源地址，仅供 test assertion。
	remotes chan netip.AddrPort
	// stopped 等待 fixture reader 退出。
	stopped chan struct{}
}

// newEchoBackend 创建动态端口 fixture，不依赖 production listener。
func newEchoBackend(t *testing.T) *echoBackend {
	t.Helper()
	connection, err := net.ListenUDP(
		"udp",
		net.UDPAddrFromAddrPort(
			netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	backend := &echoBackend{
		connection: connection,
		remotes:    make(chan netip.AddrPort, 8),
		stopped:    make(chan struct{}),
	}
	go func() {
		defer close(backend.stopped)
		buffer := make([]byte, 1_201)
		defer clear(buffer)
		for {
			bytesRead, remote, readErr := connection.ReadFromUDPAddrPort(buffer)
			if readErr != nil {
				return
			}
			backend.remotes <- remote
			if _, writeErr := connection.WriteToUDPAddrPort(buffer[:bytesRead], remote); writeErr != nil {
				return
			}
		}
	}()
	return backend
}

// close 停止 backend 并等待 reader 归还 buffer。
func (backend *echoBackend) close(t *testing.T) {
	t.Helper()
	if err := backend.connection.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("close backend: %v", err)
	}
	select {
	case <-backend.stopped:
	case <-time.After(time.Second):
		t.Fatal("backend reader did not stop")
	}
}

// socketTestConfig 返回没有 impairment 的真实 socket fixture。
func socketTestConfig(backend netip.AddrPort) SocketConfig {
	scheduler := testConfig()
	scheduler.Uplink.BaseLatency, scheduler.Uplink.Jitter = 0, 0
	scheduler.Downlink.BaseLatency, scheduler.Downlink.Jitter = 0, 0
	return SocketConfig{
		Scheduler:             scheduler,
		Backend:               backend,
		FrontendBind:          netip.MustParseAddrPort("127.0.0.1:0"),
		ClientCount:           1,
		IngressQueueItems:     32,
		EvidenceQueueItems:    64,
		SchedulerPollInterval: time.Millisecond,
		MappingLateDelivery:   50 * time.Millisecond,
	}
}

// readEcho 发送一个 opaque datagram 并等待 gateway 双向转发。
func readEcho(t *testing.T, client *net.UDPConn, payload []byte) {
	t.Helper()
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("write client: %v", err)
	}
	if err := client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buffer := make([]byte, len(payload)+1)
	bytesRead, err := client.Read(buffer)
	if err != nil {
		t.Fatalf("read client: %v", err)
	}
	if !bytes.Equal(buffer[:bytesRead], payload) {
		t.Fatal("gateway changed opaque payload")
	}
}

// testTicketID 返回公开且非零的 fixed ClientHello ticket identity。
func testTicketID(seed byte) [battleTicketIDBytes]byte {
	var ticketID [battleTicketIDBytes]byte
	for index := range ticketID {
		ticketID[index] = seed + byte(index)
	}
	return ticketID
}

// clientHelloFixture 构造只用于 gateway demultiplex 的 canonical ClientHello。
func clientHelloFixture(ticketID [battleTicketIDBytes]byte) []byte {
	payload := make([]byte, clientHelloBytes)
	copy(payload[:4], "IHBH")
	payload[secureVersionOffset] = secureWireVersion
	payload[secureKindOffset] = 1
	copy(
		payload[handshakeTicketOffset:handshakeTicketOffset+battleTicketIDBytes],
		ticketID[:],
	)
	return payload
}

// establishGatewayClient 登记 ticket、发送 ClientHello 并排空初始双向 evidence。
func establishGatewayClient(
	t *testing.T,
	gateway *Gateway,
	backend *echoBackend,
	client *net.UDPConn,
	slot uint8,
) netip.AddrPort {
	t.Helper()
	ticketID := testTicketID(slot)
	commandContext, cancelCommand := context.WithTimeout(
		context.Background(),
		time.Second,
	)
	defer cancelCommand()
	if err := gateway.RegisterTicket(commandContext, slot, ticketID); err != nil {
		t.Fatalf("RegisterTicket: %v", err)
	}
	readEcho(t, client, clientHelloFixture(ticketID))
	var remote netip.AddrPort
	select {
	case remote = <-backend.remotes:
	case <-time.After(time.Second):
		t.Fatal("backend did not observe ClientHello mapping")
	}
	for evidenceCount := 0; evidenceCount < 2; evidenceCount++ {
		select {
		case metadata := <-gateway.Evidence():
			if metadata.Disposition != DispositionDelivered ||
				metadata.PacketKind != PublicPacketHandshake {
				t.Fatalf("ClientHello metadata=%+v", metadata)
			}
		case <-time.After(time.Second):
			t.Fatal("ClientHello evidence was incomplete")
		}
	}
	return remote
}

// activateGateway 在测试 measurement 边界提交单向 fault activation。
func activateGateway(t *testing.T, gateway *Gateway) {
	t.Helper()
	commandContext, cancelCommand := context.WithTimeout(
		context.Background(),
		time.Second,
	)
	defer cancelCommand()
	if err := gateway.ActivateImpairments(commandContext); err != nil {
		t.Fatalf("ActivateImpairments: %v", err)
	}
}

// TestGatewayLoopbackMappingAndEvidence 验证真实双向 socket、mapping rotation 与低敏证据。
func TestGatewayLoopbackMappingAndEvidence(t *testing.T) {
	backend := newEchoBackend(t)
	defer backend.close(t)

	gateway, err := New(socketTestConfig(
		backend.connection.LocalAddr().(*net.UDPAddr).AddrPort()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runContext, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	if err := gateway.Start(runContext); err != nil {
		t.Fatalf("Start: %v", err)
	}
	frontend, err := gateway.FrontendEndpoint(1)
	if err != nil {
		t.Fatalf("FrontendEndpoint: %v", err)
	}
	client, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(frontend))
	if err != nil {
		t.Fatalf("DialUDP client: %v", err)
	}
	defer client.Close()

	firstRemote := establishGatewayClient(t, gateway, backend, client, 1)
	firstPayload := secureFixture(1, 64)
	readEcho(t, client, firstPayload)
	select {
	case observedRemote := <-backend.remotes:
		if observedRemote != firstRemote {
			t.Fatal("initial mapping changed before rotation")
		}
	case <-time.After(time.Second):
		t.Fatal("backend did not observe initial mapping")
	}

	commandContext, cancelCommand := context.WithTimeout(context.Background(), time.Second)
	rotation, err := gateway.RotateMappingEndpoint(commandContext, 1)
	cancelCommand()
	if err != nil || rotation.Generation != 2 {
		t.Fatalf(
			"RotateMappingEndpoint generation=%d err=%v",
			rotation.Generation,
			err,
		)
	}
	rebindRequest := secureFixture(3, 64)
	readEcho(t, client, rebindRequest)
	select {
	case observedRemote := <-backend.remotes:
		if observedRemote != firstRemote {
			t.Fatal("rebind request did not traverse predecessor mapping")
		}
	case <-time.After(time.Second):
		t.Fatal("backend did not observe rebind request")
	}
	challenge := secureFixture(3, 64)
	if _, err := backend.connection.WriteToUDPAddrPort(
		challenge,
		rotation.Endpoint,
	); err != nil {
		t.Fatalf("write successor challenge: %v", err)
	}
	if err := client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	challengeBuffer := make([]byte, len(challenge)+1)
	challengeBytes, err := client.Read(challengeBuffer)
	if err != nil {
		t.Fatalf("read successor challenge: %v", err)
	}
	if !bytes.Equal(challengeBuffer[:challengeBytes], challenge) {
		t.Fatal("gateway changed successor challenge")
	}
	secondPayload := secureFixture(2, 64)
	readEcho(t, client, secondPayload)
	var secondRemote netip.AddrPort
	select {
	case secondRemote = <-backend.remotes:
	case <-time.After(time.Second):
		t.Fatal("backend did not observe successor mapping")
	}
	if firstRemote == secondRemote {
		t.Fatalf("mapping source did not change: %s", firstRemote)
	}

	evidence := make([]Metadata, 0, 7)
	deadline := time.After(2 * time.Second)
	for len(evidence) < 7 {
		select {
		case metadata := <-gateway.Evidence():
			evidence = append(evidence, metadata)
		case <-deadline:
			t.Fatalf("evidence count=%d", len(evidence))
		}
	}
	for _, metadata := range evidence {
		if metadata.Disposition != DispositionDelivered ||
			metadata.LengthBytes != 64 ||
			metadata.Correlation == "" {
			t.Fatalf("metadata=%+v", metadata)
		}
	}

	closeContext, cancelClose := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelClose()
	if err := gateway.Close(closeContext); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestGatewaySharesAdvertisedEndpointWithIndependentMappings 验证多 actor 只使用一个
// BattleTicket endpoint，同时 backend 仍观察到每 slot 的独立 NAT mapping。
func TestGatewaySharesAdvertisedEndpointWithIndependentMappings(t *testing.T) {
	backend := newEchoBackend(t)
	defer backend.close(t)
	config := socketTestConfig(
		backend.connection.LocalAddr().(*net.UDPAddr).AddrPort(),
	)
	config.ClientCount = 2
	gateway, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	firstEndpoint, err := gateway.FrontendEndpoint(1)
	if err != nil {
		t.Fatalf("FrontendEndpoint(1): %v", err)
	}
	secondEndpoint, err := gateway.FrontendEndpoint(2)
	if err != nil {
		t.Fatalf("FrontendEndpoint(2): %v", err)
	}
	if firstEndpoint != secondEndpoint {
		t.Fatalf("advertised endpoint diverged: first=%s second=%s", firstEndpoint, secondEndpoint)
	}
	firstClient, err := net.DialUDP(
		"udp",
		nil,
		net.UDPAddrFromAddrPort(firstEndpoint),
	)
	if err != nil {
		t.Fatalf("DialUDP first client: %v", err)
	}
	defer firstClient.Close()
	secondClient, err := net.DialUDP(
		"udp",
		nil,
		net.UDPAddrFromAddrPort(secondEndpoint),
	)
	if err != nil {
		t.Fatalf("DialUDP second client: %v", err)
	}
	defer secondClient.Close()

	firstRemote := establishGatewayClient(t, gateway, backend, firstClient, 1)
	secondRemote := establishGatewayClient(t, gateway, backend, secondClient, 2)
	if firstRemote == secondRemote {
		t.Fatal("independent client slots reused one backend mapping")
	}

	closeContext, cancelClose := context.WithTimeout(
		context.Background(),
		2*time.Second,
	)
	defer cancelClose()
	if err := gateway.Close(closeContext); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestGatewayExpiresQueuedPredecessorDownlink 验证 mapping overlap 只允许有界迟到，
// 已排队但越过 predecessor deadline 的 downlink 不会到达 client。
func TestGatewayExpiresQueuedPredecessorDownlink(t *testing.T) {
	backend := newEchoBackend(t)
	defer backend.close(t)
	config := socketTestConfig(
		backend.connection.LocalAddr().(*net.UDPAddr).AddrPort(),
	)
	config.Scheduler.Downlink.BaseLatency = 40 * time.Millisecond
	config.MappingLateDelivery = 15 * time.Millisecond
	config.Scheduler.PacketLifetime = 200 * time.Millisecond
	gateway, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	frontend, err := gateway.FrontendEndpoint(1)
	if err != nil {
		t.Fatalf("FrontendEndpoint: %v", err)
	}
	client, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(frontend))
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer client.Close()
	predecessor := establishGatewayClient(t, gateway, backend, client, 1)
	activateGateway(t, gateway)

	commandContext, cancelCommand := context.WithTimeout(
		context.Background(),
		time.Second,
	)
	rotation, err := gateway.RotateMappingEndpoint(commandContext, 1)
	if err != nil {
		cancelCommand()
		t.Fatalf("RotateMappingEndpoint: %v", err)
	}
	cancelCommand()
	if _, err := client.Write(secureFixture(3, 64)); err != nil {
		t.Fatalf("write rebind request: %v", err)
	}
	select {
	case observedRemote := <-backend.remotes:
		if observedRemote != predecessor {
			t.Fatal("rebind request bypassed predecessor mapping")
		}
	case <-time.After(time.Second):
		t.Fatal("backend did not observe rebind request")
	}
	challenge := secureFixture(3, 64)
	if _, err := backend.connection.WriteToUDPAddrPort(
		challenge,
		rotation.Endpoint,
	); err != nil {
		t.Fatalf("write successor challenge: %v", err)
	}
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	challengeBuffer := make([]byte, len(challenge)+1)
	challengeBytes, err := client.Read(challengeBuffer)
	if err != nil {
		t.Fatalf("read successor challenge: %v", err)
	}
	if !bytes.Equal(challengeBuffer[:challengeBytes], challenge) {
		t.Fatal("gateway changed successor challenge")
	}
	payload := secureFixture(1, 64)
	if _, err := backend.connection.WriteToUDPAddrPort(payload, predecessor); err != nil {
		t.Fatalf("write predecessor downlink: %v", err)
	}
	var expired bool
	deadline := time.After(time.Second)
	for !expired {
		select {
		case metadata := <-gateway.Evidence():
			if metadata.Direction == DirectionDownlink &&
				metadata.Disposition == DispositionMappingExpired {
				expired = true
			}
		case <-deadline:
			t.Fatal("queued predecessor downlink did not expire")
		}
	}
	if err := client.SetReadDeadline(time.Now().Add(75 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buffer := make([]byte, len(payload)+1)
	if _, err := client.Read(buffer); err == nil {
		t.Fatal("expired predecessor downlink reached client")
	}

	closeContext, cancelClose := context.WithTimeout(
		context.Background(),
		2*time.Second,
	)
	defer cancelClose()
	if err := gateway.Close(closeContext); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestGatewayPromotesOnlyAfterObservedSuccessorControl 验证客户端公开 Control
// 不能提前提交 NAT successor，只有 backend 经 successor 返回的 Control 才能提交。
func TestGatewayPromotesOnlyAfterObservedSuccessorControl(t *testing.T) {
	backend := newEchoBackend(t)
	defer backend.close(t)
	config := socketTestConfig(
		backend.connection.LocalAddr().(*net.UDPAddr).AddrPort(),
	)
	gateway, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	frontend, err := gateway.FrontendEndpoint(1)
	if err != nil {
		t.Fatalf("FrontendEndpoint: %v", err)
	}
	client, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(frontend))
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer client.Close()
	_ = establishGatewayClient(t, gateway, backend, client, 1)
	activateGateway(t, gateway)

	commandContext, cancelCommand := context.WithTimeout(
		context.Background(),
		time.Second,
	)
	rotation, err := gateway.RotateMappingEndpoint(commandContext, 1)
	cancelCommand()
	if err != nil {
		t.Fatalf("RotateMappingEndpoint: %v", err)
	}
	readEcho(t, client, secureFixture(3, 64))

	commandContext, cancelCommand = context.WithTimeout(
		context.Background(),
		time.Second,
	)
	_, err = gateway.RotateMappingEndpoint(commandContext, 1)
	cancelCommand()
	if !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("uplink Control prematurely promoted successor: %v", err)
	}

	successorControl := secureFixture(3, 64)
	if _, err := backend.connection.WriteToUDPAddrPort(
		successorControl,
		rotation.Endpoint,
	); err != nil {
		t.Fatalf("write successor Control: %v", err)
	}
	if err := client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	received := make([]byte, len(successorControl)+1)
	bytesRead, err := client.Read(received)
	if err != nil {
		t.Fatalf("read successor Control: %v", err)
	}
	if !bytes.Equal(received[:bytesRead], successorControl) {
		t.Fatal("gateway changed successor Control")
	}

	commandContext, cancelCommand = context.WithTimeout(
		context.Background(),
		time.Second,
	)
	nextRotation, err := gateway.RotateMappingEndpoint(commandContext, 1)
	cancelCommand()
	if err != nil {
		t.Fatalf("successor Control did not promote mapping: %v", err)
	}
	if nextRotation.Generation != rotation.Generation+1 {
		t.Fatalf(
			"next generation=%d want=%d",
			nextRotation.Generation,
			rotation.Generation+1,
		)
	}

	closeContext, cancelClose := context.WithTimeout(
		context.Background(),
		2*time.Second,
	)
	defer cancelClose()
	if err := gateway.Close(closeContext); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestGatewayPauseResume 验证单方向 pause 不影响恢复后的同一 mapping。
func TestGatewayPauseResume(t *testing.T) {
	backend := newEchoBackend(t)
	defer backend.close(t)
	gateway, err := New(socketTestConfig(
		backend.connection.LocalAddr().(*net.UDPAddr).AddrPort()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	frontend, err := gateway.FrontendEndpoint(1)
	if err != nil {
		t.Fatalf("FrontendEndpoint: %v", err)
	}
	client, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(frontend))
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer client.Close()

	_ = establishGatewayClient(t, gateway, backend, client, 1)
	activateGateway(t, gateway)
	commandContext, cancelCommand := context.WithTimeout(context.Background(), time.Second)
	if err := gateway.SetPaused(commandContext, DirectionUplink, true); err != nil {
		cancelCommand()
		t.Fatalf("pause: %v", err)
	}
	cancelCommand()
	if _, err := client.Write(secureFixture(1, 64)); err != nil {
		t.Fatalf("write paused: %v", err)
	}
	select {
	case metadata := <-gateway.Evidence():
		if metadata.Disposition != DispositionPaused {
			t.Fatalf("paused metadata=%+v", metadata)
		}
	case <-time.After(time.Second):
		t.Fatal("paused packet had no terminal evidence")
	}

	commandContext, cancelCommand = context.WithTimeout(context.Background(), time.Second)
	if err := gateway.SetPaused(commandContext, DirectionUplink, false); err != nil {
		cancelCommand()
		t.Fatalf("resume: %v", err)
	}
	cancelCommand()
	readEcho(t, client, secureFixture(1, 64))

	closeContext, cancelClose := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelClose()
	if err := gateway.Close(closeContext); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestGatewayActivatesImpairmentsAfterWarmup 验证 warmup 不消耗 fault policy，
// measurement 激活后同一 socket owner 才执行 deterministic loss。
func TestGatewayActivatesImpairmentsAfterWarmup(t *testing.T) {
	backend := newEchoBackend(t)
	defer backend.close(t)
	config := socketTestConfig(
		backend.connection.LocalAddr().(*net.UDPAddr).AddrPort(),
	)
	config.Scheduler.Uplink.LossPercent = percentageCeiling
	gateway, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	frontend, err := gateway.FrontendEndpoint(1)
	if err != nil {
		t.Fatalf("FrontendEndpoint: %v", err)
	}
	client, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(frontend))
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer client.Close()
	_ = establishGatewayClient(t, gateway, backend, client, 1)

	activateGateway(t, gateway)
	activateGateway(t, gateway)
	if _, err := client.Write(secureFixture(1, 64)); err != nil {
		t.Fatalf("write impaired packet: %v", err)
	}
	select {
	case metadata := <-gateway.Evidence():
		if metadata.Disposition != DispositionLoss {
			t.Fatalf("impaired metadata=%+v", metadata)
		}
	case <-time.After(time.Second):
		t.Fatal("impaired packet had no terminal evidence")
	}
	commandContext, cancelCommand := context.WithTimeout(
		context.Background(),
		time.Second,
	)
	if err := gateway.QuiesceImpairments(commandContext); err != nil {
		cancelCommand()
		t.Fatalf("QuiesceImpairments: %v", err)
	}
	cancelCommand()
	readEcho(t, client, secureFixture(1, 64))

	closeContext, cancelClose := context.WithTimeout(
		context.Background(),
		2*time.Second,
	)
	defer cancelClose()
	if err := gateway.Close(closeContext); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestGatewayAwaitQuiescenceWaitsForScheduledCopies 验证 barrier 只在既有 fault copy
// 自然产生终局后完成，随后流量仍走同一 socket owner 的直接转发路径。
func TestGatewayAwaitQuiescenceWaitsForScheduledCopies(t *testing.T) {
	backend := newEchoBackend(t)
	defer backend.close(t)
	config := socketTestConfig(
		backend.connection.LocalAddr().(*net.UDPAddr).AddrPort(),
	)
	config.Scheduler.Uplink.BaseLatency = 75 * time.Millisecond
	gateway, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	frontend, err := gateway.FrontendEndpoint(1)
	if err != nil {
		t.Fatalf("FrontendEndpoint: %v", err)
	}
	client, err := net.DialUDP(
		"udp",
		nil,
		net.UDPAddrFromAddrPort(frontend),
	)
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer client.Close()
	_ = establishGatewayClient(t, gateway, backend, client, 1)
	activateGateway(t, gateway)

	commandContext, cancelCommand := context.WithTimeout(
		context.Background(),
		time.Second,
	)
	if err := gateway.AwaitQuiescence(commandContext); !errors.Is(err, ErrInvalidPacket) {
		cancelCommand()
		t.Fatalf("pre-quiesce AwaitQuiescence error = %v", err)
	}
	cancelCommand()
	payload := secureFixture(1, 64)
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("write scheduled packet: %v", err)
	}
	pendingDeadline := time.Now().Add(time.Second)
	for gateway.scheduler.PendingCopies() == 0 {
		if time.Now().After(pendingDeadline) {
			t.Fatal("scheduled packet did not enter fault queue")
		}
		time.Sleep(time.Millisecond)
	}
	commandContext, cancelCommand = context.WithTimeout(
		context.Background(),
		time.Second,
	)
	if err := gateway.QuiesceImpairments(commandContext); err != nil {
		cancelCommand()
		t.Fatalf("QuiesceImpairments: %v", err)
	}
	if err := gateway.AwaitQuiescence(commandContext); err != nil {
		cancelCommand()
		t.Fatalf("AwaitQuiescence: %v", err)
	}
	cancelCommand()
	if pending := gateway.scheduler.PendingCopies(); pending != 0 {
		t.Fatalf("pending copies after barrier = %d", pending)
	}
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	echo := make([]byte, len(payload)+1)
	bytesRead, err := client.Read(echo)
	if err != nil || !bytes.Equal(echo[:bytesRead], payload) {
		t.Fatalf("scheduled echo bytes=%d err=%v", bytesRead, err)
	}
	readEcho(t, client, secureFixture(1, 65))

	closeContext, cancelClose := context.WithTimeout(
		context.Background(),
		2*time.Second,
	)
	defer cancelClose()
	if err := gateway.Close(closeContext); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestGatewayInjectsOneOpaqueBaselineGap 验证已送达 uplink KCP 只武装一次
// downlink Raw 丢失，且 gateway 不读取受保护 payload。
func TestGatewayInjectsOneOpaqueBaselineGap(t *testing.T) {
	backend := newEchoBackend(t)
	defer backend.close(t)
	config := socketTestConfig(
		backend.connection.LocalAddr().(*net.UDPAddr).AddrPort(),
	)
	config.Scheduler.BaselineGap = true
	gateway, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	frontend, err := gateway.FrontendEndpoint(1)
	if err != nil {
		t.Fatalf("FrontendEndpoint: %v", err)
	}
	client, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(frontend))
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer client.Close()
	_ = establishGatewayClient(t, gateway, backend, client, 1)
	activateGateway(t, gateway)

	readEcho(t, client, secureFixture(2, 64))
	if _, err := client.Write(secureFixture(1, 64)); err != nil {
		t.Fatalf("write baseline full: %v", err)
	}
	if err := client.SetReadDeadline(
		time.Now().Add(75 * time.Millisecond),
	); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buffer := make([]byte, 65)
	if _, err := client.Read(buffer); err == nil {
		t.Fatal("armed baseline Raw reached client")
	}
	var observedLoss bool
	deadline := time.After(time.Second)
	for !observedLoss {
		select {
		case metadata := <-gateway.Evidence():
			observedLoss =
				metadata.Direction == DirectionDownlink &&
					metadata.PacketKind == PublicPacketRaw &&
					metadata.Disposition == DispositionLoss
		case <-deadline:
			t.Fatal("baseline gap had no terminal evidence")
		}
	}
	readEcho(t, client, secureFixture(1, 64))

	commandContext, cancelCommand := context.WithTimeout(
		context.Background(),
		time.Second,
	)
	if err := gateway.QuiesceImpairments(commandContext); err != nil {
		cancelCommand()
		t.Fatalf("QuiesceImpairments: %v", err)
	}
	cancelCommand()
	closeContext, cancelClose := context.WithTimeout(
		context.Background(),
		2*time.Second,
	)
	defer cancelClose()
	if err := gateway.Close(closeContext); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestSocketConfigRejectsUnsupportedEnvironment 验证非 loopback 与缺失 queue fail closed。
func TestSocketConfigRejectsUnsupportedEnvironment(t *testing.T) {
	config := socketTestConfig(netip.MustParseAddrPort("127.0.0.1:58445"))
	config.Backend = netip.MustParseAddrPort("192.0.2.1:58445")
	if _, err := New(config); err != ErrInvalidConfig {
		t.Fatalf("non-loopback err=%v", err)
	}
	config = socketTestConfig(netip.MustParseAddrPort("127.0.0.1:58445"))
	config.EvidenceQueueItems = 0
	if _, err := New(config); err != ErrInvalidConfig {
		t.Fatalf("zero evidence queue err=%v", err)
	}
}
