package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

// SocketConfig 冻结真实 UDP gateway 的 listener、queue 与 cleanup 参数。
type SocketConfig struct {
	// Scheduler 是已绑定 scenario manifest 的 fault policy。
	Scheduler Config
	// Backend 是同一 C++ SimulationNode 的唯一 UDP listener。
	Backend netip.AddrPort
	// FrontendBind 是唯一 advertised frontend 的本机数字地址；零 port 仅供隔离测试动态分配。
	FrontendBind netip.AddrPort
	// ClientCount 是本 scenario 创建的独立 mapping 数。
	ClientCount uint8
	// IngressQueueItems 是 reader 到唯一 scheduler owner 的 hard cap。
	IngressQueueItems int
	// EvidenceQueueItems 是低敏终局 evidence 的 hard cap。
	EvidenceQueueItems int
	// SchedulerPollInterval 是 monotonic due queue 的最大唤醒粒度。
	SchedulerPollInterval time.Duration
	// MappingLateDelivery 是 predecessor socket 的有界迟到投递窗口。
	MappingLateDelivery time.Duration
}

// Validate 在任何 socket 创建前拒绝 DNS、通配或隐藏默认值。
func (config SocketConfig) Validate() error {
	if err := config.Scheduler.Validate(); err != nil {
		return err
	}
	if !config.Backend.IsValid() ||
		config.Backend.Addr().IsUnspecified() ||
		!config.Backend.Addr().IsLoopback() ||
		config.Backend.Port() == 0 ||
		!config.FrontendBind.IsValid() ||
		config.FrontendBind.Addr().IsUnspecified() ||
		!config.FrontendBind.Addr().IsLoopback() ||
		config.ClientCount == 0 ||
		config.ClientCount > config.Scheduler.MaximumClients ||
		config.IngressQueueItems <= 0 ||
		config.EvidenceQueueItems <= 0 ||
		config.SchedulerPollInterval <= 0 ||
		config.SchedulerPollInterval > config.Scheduler.PacketLifetime ||
		config.MappingLateDelivery <= 0 {
		return ErrInvalidConfig
	}
	return nil
}

// mapping 是一个 client slot 的单一 upstream NAT incarnation。
type mapping struct {
	// generation 是 successor 递增且永不复用的 run-local identity。
	generation uint32
	// connection 是 connected backend socket，过滤非 backend datagram。
	connection *net.UDPConn
	// expiresAt 为零表示 current；非零表示 predecessor 的最后可用时刻。
	expiresAt time.Duration
}

// clientState 保存一个 frontend 与其 current/predecessor upstream sockets。
type clientState struct {
	// slot 是全部 evidence 和 command 使用的 run-local identity。
	slot uint8
	// ticketID 是注册后用于把首个 ClientHello 绑定到 slot 的公开 opaque identity。
	ticketID [battleTicketIDBytes]byte
	// registered 表示 ticketID 已由 admission owner 精确登记。
	registered bool
	// clientRemote 是已认证流程开始前由 ClientHello 建立的唯一 client endpoint。
	clientRemote netip.AddrPort
	// sessionDigest 是首个 secure packet 公开 header 建立的路由摘要。
	sessionDigest [secureSessionDigestBytes]byte
	// activeGeneration 供 frontend reader 无锁取得 current mapping。
	activeGeneration atomic.Uint32
	// pendingGeneration 是已创建但尚未由 backend successor control 响应确认的 mapping。
	pendingGeneration uint32
	// pendingExpiresAt 限制未完成 rotation 占用的 socket 生命周期。
	pendingExpiresAt time.Duration
	// baselineGapArmed 表示已送达首个 uplink KCP，等待丢弃下一枚 downlink Raw。
	baselineGapArmed bool
	// baselineGapInjected 保证每个 client 的显式 baseline fault 只执行一次。
	baselineGapInjected bool
	// mappings 由 event loop 唯一读写，并有界保留 predecessor。
	mappings map[uint32]*mapping
}

// ingress 是 socket reader 转交给唯一 scheduler owner 的消息。
type ingress struct {
	// packet 保存 owned opaque datagram 与 receive identity。
	packet Packet
	// clientRemote 只对 uplink 有效，且不得进入 evidence。
	clientRemote netip.AddrPort
}

// gatewayCommand 是外部 lifecycle 调用与 event loop 的有界 request。
type gatewayCommand struct {
	// kind 是 closed mutation 类型。
	kind gatewayCommandKind
	// clientSlot 是 rotate 的目标；其他 command 使用零。
	clientSlot uint8
	// direction 与 paused 只对 pause command 有效。
	direction Direction
	// paused 是目标方向的新状态。
	paused bool
	// ticketID 只对 register command 有效，属于公开 lookup identity。
	ticketID [battleTicketIDBytes]byte
	// patternSeed 只对 restart-pattern command 有效。
	patternSeed uint32
	// result 完成本次同步 command，容量固定为一。
	result chan commandResult
}

// gatewayCommandKind 是 gateway event loop 的 closed command。
type gatewayCommandKind uint8

const (
	// gatewayCommandRotate 创建 successor upstream mapping。
	gatewayCommandRotate gatewayCommandKind = iota + 1
	// gatewayCommandPause 切换 scheduler 单方向 pause。
	gatewayCommandPause
	// gatewayCommandRegisterTicket 绑定 admission slot 与公开 ticket identity。
	gatewayCommandRegisterTicket
	// gatewayCommandActivateImpairments 在 warmup 后单向启用 deterministic scheduler。
	gatewayCommandActivateImpairments
	// gatewayCommandRestartPattern 为后续 workload phase 重启有限 fault 模式。
	gatewayCommandRestartPattern
	// gatewayCommandQuiesceImpairments 终止新 fault ingress 并保留既有 queued copy。
	gatewayCommandQuiesceImpairments
	// gatewayCommandAwaitQuiescence 等待全部既有 queued copy 产生唯一终局。
	gatewayCommandAwaitQuiescence
)

// commandResult 返回 generation 或稳定错误。
type commandResult struct {
	// generation 只在 rotate 成功时非零。
	generation uint32
	// endpoint 只在 rotate 成功时返回 server 实际观察的 successor mapping。
	endpoint netip.AddrPort
	// err 表示 command 未提交。
	err error
}

// MappingRotation 是 NAT successor 的 generation 与 server-facing numeric endpoint。
type MappingRotation struct {
	// Generation 是严格递增且不复用的 mapping identity。
	Generation uint32
	// Endpoint 是 authenticated rebind request 必须公布的 gateway upstream endpoint。
	Endpoint netip.AddrPort
}

// Gateway 拥有 qualification-only UDP sockets、scheduler goroutine 与 evidence channel。
//
// Start 成功后每个 socket 恰有一个 reader，所有 socket write、mapping mutation、scheduler
// 和 evidence publication 由 event loop 串行化。Close 可并发且会等待全部 reader 退出。
type Gateway struct {
	// config 是构造时完整验证的 immutable 配置。
	config SocketConfig
	// scheduler 拥有 PRNG、fault queue 与 bandwidth timeline。
	scheduler *Scheduler
	// startedAt 是所有 evidence duration 的 monotonic origin。
	startedAt time.Time
	// frontend 是全部 BattleTicket 共用的唯一 advertised UDP listener。
	frontend *net.UDPConn
	// frontendEndpoint 是启动完成后不可变的 advertised endpoint。
	frontendEndpoint netip.AddrPort
	// clients 在 Start 后只增不减，mapping 内容仅 event loop 修改。
	clients map[uint8]*clientState
	// ticketSlots 把公开 ClientHello ticket identity 映射到 run-local slot。
	ticketSlots map[[battleTicketIDBytes]byte]uint8
	// remoteSlots 把已接受的 client endpoint 映射到唯一 slot。
	remoteSlots map[netip.AddrPort]uint8
	// sessionSlots 把 secure public routing digest 映射到唯一 slot。
	sessionSlots map[[secureSessionDigestBytes]byte]uint8
	// ingressQueue 是全部 socket reader 的有界汇聚点。
	ingressQueue chan ingress
	// commands 串行化 rotate 与 pause mutation。
	commands chan gatewayCommand
	// evidence 只承载不含 payload/IP/secret 的终局 metadata。
	evidence chan Metadata
	// cancel 停止 event loop 与 reader，但不保存 parent context。
	cancel context.CancelFunc
	// done 在 event loop 和全部 reader 完成后关闭。
	done chan struct{}
	// terminalError 保存首个不可恢复 socket/evidence failure。
	terminalError error
	// terminalMutex 保护 terminalError。
	terminalMutex sync.Mutex
	// lifecycleMutex 保护 Start/Close 与 cancel publication。
	lifecycleMutex sync.Mutex
	// started 表示全部 initial sockets 已成功创建。
	started bool
	// closed 表示 lifecycle 已进入不可逆终态。
	closed bool
	// failOnce 保证首个 terminal error 触发唯一 cancellation。
	failOnce sync.Once
	// readers 等待全部 socket reader 归还 buffer。
	readers sync.WaitGroup
	// receiveSequence 为所有 reader 分配全局正整数。
	receiveSequence atomic.Uint64
}

// New 验证全部配置并创建尚未打开 socket 的 gateway。
func New(config SocketConfig) (*Gateway, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	scheduler, err := NewScheduler(config.Scheduler)
	if err != nil {
		return nil, err
	}
	return &Gateway{
		config:       config,
		scheduler:    scheduler,
		clients:      make(map[uint8]*clientState, config.ClientCount),
		ticketSlots:  make(map[[battleTicketIDBytes]byte]uint8, config.ClientCount),
		remoteSlots:  make(map[netip.AddrPort]uint8, config.ClientCount),
		sessionSlots: make(map[[secureSessionDigestBytes]byte]uint8, config.ClientCount),
		ingressQueue: make(chan ingress, config.IngressQueueItems),
		commands:     make(chan gatewayCommand),
		evidence:     make(chan Metadata, config.EvidenceQueueItems),
		done:         make(chan struct{}),
	}, nil
}

// Start 原子创建唯一 frontend 与全部 current mapping 后启动唯一 event loop。
//
// 任一 bind/connect 失败会关闭此前已创建 socket，且不会留下 goroutine。ctx 只派生
// cancellation，不存入 Gateway。
func (gateway *Gateway) Start(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: nil context", ErrInvalidConfig)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	gateway.lifecycleMutex.Lock()
	defer gateway.lifecycleMutex.Unlock()
	if gateway.started || gateway.closed {
		return fmt.Errorf("%w: already started", ErrInvalidConfig)
	}
	runContext, cancel := context.WithCancel(ctx)
	gateway.cancel = cancel
	gateway.startedAt = time.Now()

	frontend, err := net.ListenUDP(
		"udp",
		net.UDPAddrFromAddrPort(gateway.config.FrontendBind))
	if err != nil {
		cancel()
		gateway.scheduler.Close()
		gateway.closed = true
		gateway.terminalError = err
		close(gateway.evidence)
		close(gateway.done)
		return fmt.Errorf("open gateway frontend: %w", err)
	}
	gateway.frontend = frontend
	gateway.frontendEndpoint = frontend.LocalAddr().(*net.UDPAddr).AddrPort()
	for slot := uint8(1); slot <= gateway.config.ClientCount; slot++ {
		state, err := gateway.openClient(slot)
		if err != nil {
			cancel()
			gateway.closeSockets()
			gateway.scheduler.Close()
			gateway.closed = true
			gateway.terminalError = err
			close(gateway.evidence)
			close(gateway.done)
			return fmt.Errorf("open gateway client slot %d: %w", slot, err)
		}
		gateway.clients[slot] = state
	}
	gateway.startFrontendReader(runContext)
	for _, client := range gateway.clients {
		gateway.startMappingReader(runContext, client, client.mappings[1])
	}
	gateway.started = true
	go gateway.run(runContext)
	return nil
}

// openClient 创建一个独立 initial connected upstream mapping。
func (gateway *Gateway) openClient(slot uint8) (*clientState, error) {
	upstream, err := net.DialUDP(
		"udp",
		nil,
		net.UDPAddrFromAddrPort(gateway.config.Backend))
	if err != nil {
		return nil, err
	}
	state := &clientState{
		slot: slot,
		mappings: map[uint32]*mapping{
			1: {generation: 1, connection: upstream},
		},
	}
	state.activeGeneration.Store(1)
	return state, nil
}

// FrontendEndpoint 返回 client slot 的 advertised gateway 地址。
func (gateway *Gateway) FrontendEndpoint(clientSlot uint8) (netip.AddrPort, error) {
	gateway.lifecycleMutex.Lock()
	defer gateway.lifecycleMutex.Unlock()
	if !gateway.started || gateway.closed {
		return netip.AddrPort{}, ErrClosed
	}
	_, ok := gateway.clients[clientSlot]
	if !ok {
		return netip.AddrPort{}, ErrInvalidPacket
	}
	return gateway.frontendEndpoint, nil
}

// Evidence 返回只读低敏终局事件流；caller 必须持续 drain，否则 gateway fail closed。
func (gateway *Gateway) Evidence() <-chan Metadata {
	return gateway.evidence
}

// Elapsed 返回相对 gateway 启动时刻的 monotonic window cursor。
func (gateway *Gateway) Elapsed() (time.Duration, error) {
	gateway.lifecycleMutex.Lock()
	defer gateway.lifecycleMutex.Unlock()
	if !gateway.started || gateway.closed {
		return 0, ErrClosed
	}
	return time.Since(gateway.startedAt), nil
}

// RegisterTicket 把公开 ticket identity 精确绑定到 client slot。
//
// 每个 slot 只能登记一次，每个 ticket identity 也只能属于一个 slot。调用必须发生在
// 对应 protocol client 发送 ClientHello 前。
func (gateway *Gateway) RegisterTicket(
	ctx context.Context,
	clientSlot uint8,
	ticketID [battleTicketIDBytes]byte,
) error {
	_, err := gateway.sendCommand(ctx, gatewayCommand{
		kind:       gatewayCommandRegisterTicket,
		clientSlot: clientSlot,
		ticketID:   ticketID,
		result:     make(chan commandResult, 1),
	})
	return err
}

// RotateMapping 创建 successor upstream socket并有界保留 predecessor。
func (gateway *Gateway) RotateMapping(ctx context.Context, clientSlot uint8) (uint32, error) {
	rotation, err := gateway.RotateMappingEndpoint(ctx, clientSlot)
	return rotation.Generation, err
}

// RotateMappingEndpoint 创建 successor upstream 并返回 server-facing rebind endpoint。
func (gateway *Gateway) RotateMappingEndpoint(
	ctx context.Context,
	clientSlot uint8,
) (MappingRotation, error) {
	result, err := gateway.sendCommand(ctx, gatewayCommand{
		kind:       gatewayCommandRotate,
		clientSlot: clientSlot,
		result:     make(chan commandResult, 1),
	})
	return MappingRotation{
		Generation: result.generation,
		Endpoint:   result.endpoint,
	}, err
}

// SetPaused 切换单方向 network pause，不改变另一方向或既有 queued copy。
func (gateway *Gateway) SetPaused(ctx context.Context, direction Direction, paused bool) error {
	_, err := gateway.sendCommand(ctx, gatewayCommand{
		kind:      gatewayCommandPause,
		direction: direction,
		paused:    paused,
		result:    make(chan commandResult, 1),
	})
	return err
}

// ActivateImpairments 在 warmup 结束后单向启用 deterministic fault policy。
//
// 激活前的 admission、handshake 与 warmup 流量直接转发，不消耗 PRNG 或 bandwidth
// timeline；重复激活保持幂等，确保 lifecycle caller 可安全重试 receipt。
func (gateway *Gateway) ActivateImpairments(ctx context.Context) error {
	_, err := gateway.sendCommand(ctx, gatewayCommand{
		kind:   gatewayCommandActivateImpairments,
		result: make(chan commandResult, 1),
	})
	return err
}

// RestartImpairmentPattern 为后续 workload phase 切换派生 seed 与重复模式起点。
func (gateway *Gateway) RestartImpairmentPattern(ctx context.Context, seed uint32) error {
	_, err := gateway.sendCommand(ctx, gatewayCommand{
		kind:        gatewayCommandRestartPattern,
		patternSeed: seed,
		result:      make(chan commandResult, 1),
	})
	return err
}

// QuiesceImpairments 结束 measurement fault ingress，但不提前投递或清除已排队 copy。
//
// 后续 cleanup/control 流量直接转发；调用方必须再跨越一次外部采样屏障，
// 让 measurement queue 按原 due/deadline 自然收敛后才能关闭 session。
func (gateway *Gateway) QuiesceImpairments(ctx context.Context) error {
	_, err := gateway.sendCommand(ctx, gatewayCommand{
		kind:   gatewayCommandQuiesceImpairments,
		result: make(chan commandResult, 1),
	})
	return err
}

// AwaitQuiescence 等待 quiesce 前已排队的 fault copy 全部自然送达或过期。
//
// 本屏障不提前投递、不丢弃 packet，也不等待 quiesce 后直接转发的持续业务流量。
func (gateway *Gateway) AwaitQuiescence(ctx context.Context) error {
	_, err := gateway.sendCommand(ctx, gatewayCommand{
		kind:   gatewayCommandAwaitQuiescence,
		result: make(chan commandResult, 1),
	})
	return err
}

// sendCommand 在 caller deadline、gateway terminal 或 command receipt 之间显式决议。
func (gateway *Gateway) sendCommand(ctx context.Context, command gatewayCommand) (commandResult, error) {
	gateway.lifecycleMutex.Lock()
	available := ctx != nil && gateway.started && !gateway.closed
	gateway.lifecycleMutex.Unlock()
	if !available {
		return commandResult{}, ErrClosed
	}
	select {
	case gateway.commands <- command:
	case <-ctx.Done():
		return commandResult{}, ctx.Err()
	case <-gateway.done:
		return commandResult{}, ErrClosed
	}
	select {
	case result := <-command.result:
		return result, result.err
	case <-ctx.Done():
		return commandResult{}, ctx.Err()
	case <-gateway.done:
		return commandResult{}, ErrClosed
	}
}

// Close 取消 gateway、关闭 sockets 并在 ctx deadline 内等待全部 owner 退出。
func (gateway *Gateway) Close(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidConfig
	}
	gateway.lifecycleMutex.Lock()
	if !gateway.started {
		if !gateway.closed {
			gateway.closed = true
			gateway.scheduler.Close()
			close(gateway.evidence)
			close(gateway.done)
		}
		gateway.lifecycleMutex.Unlock()
		return nil
	}
	gateway.closed = true
	cancel := gateway.cancel
	gateway.lifecycleMutex.Unlock()
	cancel()
	select {
	case <-gateway.done:
		gateway.terminalMutex.Lock()
		defer gateway.terminalMutex.Unlock()
		return gateway.terminalError
	case <-ctx.Done():
		return ctx.Err()
	}
}

// run 串行拥有 scheduler、mapping transition、socket write 与 evidence publication。
func (gateway *Gateway) run(ctx context.Context) {
	ticker := time.NewTicker(gateway.config.SchedulerPollInterval)
	defer ticker.Stop()
	impairmentsActive := false
	impairmentsQuiesced := false
	var quiescenceWaiters []chan commandResult
	defer func() {
		gateway.completeQuiescenceWaiters(
			&quiescenceWaiters,
			ErrClosed,
		)
		for _, metadata := range gateway.scheduler.Close() {
			if !gateway.publishEvidence(metadata) {
				break
			}
		}
		gateway.closeSockets()
		gateway.readers.Wait()
		gateway.lifecycleMutex.Lock()
		gateway.closed = true
		gateway.lifecycleMutex.Unlock()
		close(gateway.evidence)
		close(gateway.done)
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case received := <-gateway.ingressQueue:
			gateway.handleIngress(received, impairmentsActive)
		case command := <-gateway.commands:
			gateway.handleCommand(
				ctx,
				command,
				&impairmentsActive,
				&impairmentsQuiesced,
				&quiescenceWaiters,
			)
		case <-ticker.C:
			now := time.Since(gateway.startedAt)
			gateway.deliverDue(now)
			gateway.expireMappings(now)
			gateway.completeQuiescenceWaiters(
				&quiescenceWaiters,
				nil,
			)
		}
	}
}

// handleIngress 通过注册 ticket 建立 frontend remote，再按 lifecycle phase 转发。
func (gateway *Gateway) handleIngress(received ingress, impairmentsActive bool) {
	if received.packet.Direction == DirectionUplink {
		client, ok := gateway.resolveUplinkClient(
			received.clientRemote,
			received.packet.Payload,
		)
		if !ok {
			gateway.publishEvidence(metadataForPacket(received.packet, DispositionSourceRejected))
			clear(received.packet.Payload)
			return
		}
		received.packet.ClientSlot = client.slot
		received.packet.MappingGeneration = client.activeGeneration.Load()
	}
	gateway.promoteObservedSuccessorControl(
		received.packet,
		received.packet.ReceivedAt,
	)
	if !impairmentsActive {
		gateway.deliverUnimpaired(received.packet)
	} else if gateway.consumeBaselineGap(received.packet) {
		metadata := metadataForPacket(
			received.packet,
			DispositionLoss,
		)
		clear(received.packet.Payload)
		if !gateway.publishEvidence(metadata) {
			return
		}
	} else {
		result, err := gateway.scheduler.Schedule(received.packet)
		clear(received.packet.Payload)
		if err != nil {
			gateway.fail(err)
			return
		}
		for _, metadata := range result.Finalized {
			if !gateway.publishEvidence(metadata) {
				return
			}
		}
	}
}

// consumeBaselineGap 只依据公开 direction/lane 与一次性 client 状态注入 full 丢失。
func (gateway *Gateway) consumeBaselineGap(packet Packet) bool {
	if !gateway.config.Scheduler.BaselineGap ||
		packet.Direction != DirectionDownlink {
		return false
	}
	client := gateway.clients[packet.ClientSlot]
	if client == nil || !client.baselineGapArmed ||
		client.baselineGapInjected {
		return false
	}
	kind, _ := classifyPacket(packet.Payload)
	if kind != PublicPacketRaw {
		return false
	}
	client.baselineGapArmed = false
	client.baselineGapInjected = true
	return true
}

// deliverUnimpaired 复用正式 socket/mapping owner，但不触碰 fault scheduler 状态。
func (gateway *Gateway) deliverUnimpaired(packet Packet) {
	delivery := Delivery{
		Packet: packet,
		DueAt:  packet.ReceivedAt,
	}
	deliveredAt := time.Since(gateway.startedAt)
	disposition := gateway.writeDelivery(deliveredAt, delivery)
	metadata, err := FinalizeDelivery(
		delivery,
		disposition,
		deliveredAt,
	)
	if err != nil {
		gateway.fail(err)
		return
	}
	if !gateway.publishEvidence(metadata) {
		return
	}
	if disposition == DispositionWriteFailed {
		gateway.fail(errors.New("battle qualification gateway socket write failed"))
	}
}

// resolveUplinkClient 把 remote 精确绑定到已登记 ClientHello，拒绝抢占或未知 source。
func (gateway *Gateway) resolveUplinkClient(
	remote netip.AddrPort,
	payload []byte,
) (*clientState, bool) {
	if !remote.IsValid() {
		return nil, false
	}
	if slot, exists := gateway.remoteSlots[remote]; exists {
		client := gateway.clients[slot]
		gateway.observeSessionDigest(client, payload)
		return client, true
	}
	ticketID, ok := clientHelloTicketID(payload)
	if !ok {
		return nil, false
	}
	slot, exists := gateway.ticketSlots[ticketID]
	if !exists {
		return nil, false
	}
	client := gateway.clients[slot]
	if client == nil || !client.registered || client.ticketID != ticketID ||
		client.clientRemote.IsValid() {
		return nil, false
	}
	client.clientRemote = remote
	gateway.remoteSlots[remote] = slot
	return client, true
}

// observeSessionDigest 为已绑定 remote 登记第一个 secure routing handle。
//
// 后续不一致 header 仍交给真实 server 安全边界裁决，但不能污染 gateway correlation。
func (gateway *Gateway) observeSessionDigest(client *clientState, payload []byte) {
	digest, ok := secureSessionDigest(payload)
	if !ok {
		return
	}
	if !allZero(client.sessionDigest[:]) {
		return
	}
	if existing, exists := gateway.sessionSlots[digest]; exists &&
		existing != client.slot {
		return
	}
	client.sessionDigest = digest
	gateway.sessionSlots[digest] = client.slot
}

// handleCommand 执行 closed mutation 并向 caller 返回单次 receipt。
func (gateway *Gateway) handleCommand(
	ctx context.Context,
	command gatewayCommand,
	impairmentsActive *bool,
	impairmentsQuiesced *bool,
	quiescenceWaiters *[]chan commandResult,
) {
	result := commandResult{}
	switch command.kind {
	case gatewayCommandPause:
		result.err = gateway.scheduler.SetPaused(command.direction, command.paused)
	case gatewayCommandRotate:
		result.generation, result.endpoint, result.err =
			gateway.rotateMapping(ctx, command.clientSlot)
	case gatewayCommandRegisterTicket:
		result.err = gateway.registerTicket(command.clientSlot, command.ticketID)
	case gatewayCommandActivateImpairments:
		if *impairmentsQuiesced {
			result.err = ErrInvalidPacket
		} else if !*impairmentsActive {
			result.err = gateway.scheduler.RestartPattern(
				gateway.config.Scheduler.Seed,
				time.Since(gateway.startedAt),
			)
			*impairmentsActive = result.err == nil
		}
	case gatewayCommandRestartPattern:
		if !*impairmentsActive || *impairmentsQuiesced {
			result.err = ErrInvalidPacket
			break
		}
		result.err = gateway.scheduler.RestartPattern(
			command.patternSeed,
			time.Since(gateway.startedAt),
		)
	case gatewayCommandQuiesceImpairments:
		if !*impairmentsQuiesced {
			if !*impairmentsActive {
				result.err = ErrInvalidPacket
				break
			}
			*impairmentsActive = false
			*impairmentsQuiesced = true
		}
	case gatewayCommandAwaitQuiescence:
		if !*impairmentsQuiesced {
			result.err = ErrInvalidPacket
		} else if gateway.scheduler.PendingCopies() != 0 {
			*quiescenceWaiters = append(
				*quiescenceWaiters,
				command.result,
			)
			return
		}
	default:
		result.err = ErrInvalidPacket
	}
	command.result <- result
}

// completeQuiescenceWaiters 在 scheduler 清空或 gateway 终结时完成全部 barrier。
func (gateway *Gateway) completeQuiescenceWaiters(
	waiters *[]chan commandResult,
	err error,
) {
	if len(*waiters) == 0 ||
		(err == nil && gateway.scheduler.PendingCopies() != 0) {
		return
	}
	result := commandResult{err: err}
	for _, waiter := range *waiters {
		waiter <- result
	}
	*waiters = nil
}

// registerTicket 提交唯一 slot/ticket binding，拒绝零值、重复或已收包的 slot。
func (gateway *Gateway) registerTicket(
	clientSlot uint8,
	ticketID [battleTicketIDBytes]byte,
) error {
	client, ok := gateway.clients[clientSlot]
	if !ok || allZero(ticketID[:]) || client.registered ||
		client.clientRemote.IsValid() {
		return ErrInvalidPacket
	}
	if _, exists := gateway.ticketSlots[ticketID]; exists {
		return ErrInvalidPacket
	}
	client.ticketID = ticketID
	client.registered = true
	gateway.ticketSlots[ticketID] = clientSlot
	return nil
}

// rotateMapping 创建 exact next generation，并在成功后才发布 activeGeneration。
func (gateway *Gateway) rotateMapping(
	ctx context.Context,
	clientSlot uint8,
) (uint32, netip.AddrPort, error) {
	client, ok := gateway.clients[clientSlot]
	if !ok || client.pendingGeneration != 0 {
		return 0, netip.AddrPort{}, ErrInvalidPacket
	}
	currentGeneration := client.activeGeneration.Load()
	if currentGeneration == ^uint32(0) {
		return 0, netip.AddrPort{}, ErrClosed
	}
	successor, err := net.DialUDP(
		"udp",
		nil,
		net.UDPAddrFromAddrPort(gateway.config.Backend))
	if err != nil {
		return 0, netip.AddrPort{}, err
	}
	nextGeneration := currentGeneration + 1
	next := &mapping{generation: nextGeneration, connection: successor}
	client.mappings[nextGeneration] = next
	client.pendingGeneration = nextGeneration
	client.pendingExpiresAt = time.Since(gateway.startedAt) +
		gateway.config.MappingLateDelivery
	gateway.startMappingReader(ctx, client, next)
	endpoint := successor.LocalAddr().(*net.UDPAddr).AddrPort()
	return nextGeneration, endpoint, nil
}

// promoteObservedSuccessorControl 只在 connected successor socket 已收到 backend
// Control 响应后提交 mapping；客户端 uplink 的公开 kind 不能证明 server 已接受 rebind。
func (gateway *Gateway) promoteObservedSuccessorControl(
	packet Packet,
	observedAt time.Duration,
) {
	if packet.Direction != DirectionDownlink {
		return
	}
	client := gateway.clients[packet.ClientSlot]
	if client == nil ||
		client.pendingGeneration == 0 ||
		packet.MappingGeneration != client.pendingGeneration {
		return
	}
	kind, _ := classifyPacket(packet.Payload)
	if kind != PublicPacketControl {
		return
	}
	gateway.promotePendingMapping(client, observedAt)
}

// promotePendingMapping 在 successor response 已由 backend 发出后切换唯一 active mapping。
//
// gateway 只依据 connected backend socket 与 secure header 的公开 Control kind 建立边界，
// 不读取或改写受保护 payload。
func (gateway *Gateway) promotePendingMapping(
	client *clientState,
	now time.Duration,
) {
	nextGeneration := client.pendingGeneration
	if nextGeneration == 0 {
		return
	}
	currentGeneration := client.activeGeneration.Load()
	current := client.mappings[currentGeneration]
	next := client.mappings[nextGeneration]
	if current == nil || next == nil {
		gateway.fail(errors.New("battle qualification pending mapping is incomplete"))
		return
	}
	current.expiresAt = now + gateway.config.MappingLateDelivery
	client.activeGeneration.Store(nextGeneration)
	client.pendingGeneration = 0
	client.pendingExpiresAt = 0
}

// deliverDue 写入当前/迟到 mapping 或 frontend，并为每个 copy 生成唯一终局。
func (gateway *Gateway) deliverDue(now time.Duration) {
	deliveries, finalized, err := gateway.scheduler.PopDue(now)
	if err != nil {
		gateway.fail(err)
		return
	}
	for _, metadata := range finalized {
		if !gateway.publishEvidence(metadata) {
			return
		}
	}
	for _, delivery := range deliveries {
		disposition := gateway.writeDelivery(now, delivery)
		if disposition == DispositionDelivered {
			gateway.observeDeliveredFaultTrigger(delivery)
		}
		metadata, metadataErr := FinalizeDelivery(
			delivery,
			disposition,
			now,
		)
		if metadataErr != nil {
			gateway.fail(metadataErr)
			return
		}
		if !gateway.publishEvidence(metadata) {
			return
		}
		if disposition == DispositionWriteFailed {
			gateway.fail(errors.New("battle qualification gateway socket write failed"))
			return
		}
	}
}

// observeDeliveredFaultTrigger 在真实 server write 成功后才武装 opaque baseline gap。
func (gateway *Gateway) observeDeliveredFaultTrigger(delivery Delivery) {
	if !gateway.config.Scheduler.BaselineGap ||
		delivery.Packet.Direction != DirectionUplink {
		return
	}
	client := gateway.clients[delivery.Packet.ClientSlot]
	if client == nil || client.baselineGapArmed ||
		client.baselineGapInjected {
		return
	}
	kind, _ := classifyPacket(delivery.Packet.Payload)
	if kind == PublicPacketKCP {
		client.baselineGapArmed = true
	}
}

// writeDelivery 只在 mapping 与 client remote 仍有效时执行一次 UDP write。
func (gateway *Gateway) writeDelivery(now time.Duration, delivery Delivery) Disposition {
	client := gateway.clients[delivery.Packet.ClientSlot]
	mapping := client.mappings[delivery.Packet.MappingGeneration]
	if mapping == nil ||
		(mapping.expiresAt != 0 && now >= mapping.expiresAt) {
		return DispositionMappingExpired
	}
	var err error
	switch delivery.Packet.Direction {
	case DirectionUplink:
		_, err = mapping.connection.Write(delivery.Packet.Payload)
	case DirectionDownlink:
		if !client.clientRemote.IsValid() {
			return DispositionWriteFailed
		}
		_, err = gateway.frontend.WriteToUDPAddrPort(
			delivery.Packet.Payload,
			client.clientRemote)
	default:
		return DispositionWriteFailed
	}
	if err != nil {
		return DispositionWriteFailed
	}
	return DispositionDelivered
}

// expireMappings 关闭已越过迟到窗口的 predecessor socket。
func (gateway *Gateway) expireMappings(now time.Duration) {
	for _, client := range gateway.clients {
		if client.pendingGeneration != 0 &&
			now >= client.pendingExpiresAt {
			pending := client.mappings[client.pendingGeneration]
			if pending != nil {
				_ = pending.connection.Close()
				delete(client.mappings, client.pendingGeneration)
			}
			client.pendingGeneration = 0
			client.pendingExpiresAt = 0
		}
		active := client.activeGeneration.Load()
		for generation, mapping := range client.mappings {
			if generation != active &&
				mapping.expiresAt != 0 &&
				now >= mapping.expiresAt {
				_ = mapping.connection.Close()
				delete(client.mappings, generation)
			}
		}
	}
}

// startFrontendReader 为唯一 advertised frontend 注册唯一 reader。
func (gateway *Gateway) startFrontendReader(ctx context.Context) {
	gateway.readers.Add(1)
	go func() {
		defer gateway.readers.Done()
		gateway.readDatagrams(ctx, nil, nil)
	}()
}

// startMappingReader 为一个 mapping 注册唯一 connected socket reader。
func (gateway *Gateway) startMappingReader(ctx context.Context, client *clientState, source *mapping) {
	gateway.readers.Add(1)
	go func() {
		defer gateway.readers.Done()
		gateway.readDatagrams(ctx, client, source)
	}()
}

// readDatagrams 使用固定 maximum+1 buffer 检测 oversize，且不保留 endpoint evidence。
func (gateway *Gateway) readDatagrams(ctx context.Context, client *clientState, source *mapping) {
	buffer := make([]byte, gateway.config.Scheduler.MaximumDatagramBytes+1)
	defer clear(buffer)
	for {
		var (
			bytesRead    int
			clientRemote netip.AddrPort
			err          error
			direction    Direction
			generation   uint32
		)
		if source == nil {
			direction = DirectionUplink
			bytesRead, clientRemote, err = gateway.frontend.ReadFromUDPAddrPort(buffer)
		} else {
			direction = DirectionDownlink
			generation = source.generation
			bytesRead, err = source.connection.Read(buffer)
		}
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			gateway.fail(err)
			return
		}
		sequence := gateway.receiveSequence.Add(1)
		clientSlot := uint8(0)
		if client != nil {
			clientSlot = client.slot
		}
		packet := Packet{
			Direction:         direction,
			ClientSlot:        clientSlot,
			MappingGeneration: generation,
			ReceiveSequence:   sequence,
			ReceivedAt:        time.Since(gateway.startedAt),
			Payload:           append([]byte(nil), buffer[:bytesRead]...),
		}
		message := ingress{packet: packet, clientRemote: clientRemote}
		select {
		case gateway.ingressQueue <- message:
		case <-ctx.Done():
			clear(packet.Payload)
			return
		default:
			metadata := metadataForPacket(packet, DispositionQueue)
			clear(packet.Payload)
			if !gateway.publishEvidence(metadata) {
				return
			}
		}
	}
}

// publishEvidence 在 consumer 未持续 drain 时 fail closed，避免静默丢失守恒记录。
func (gateway *Gateway) publishEvidence(metadata Metadata) bool {
	select {
	case gateway.evidence <- metadata:
		return true
	default:
		gateway.fail(errors.New("battle qualification gateway evidence queue is full"))
		return false
	}
}

// fail 保存首个稳定 cause 并取消全部 socket owner。
func (gateway *Gateway) fail(err error) {
	gateway.failOnce.Do(func() {
		gateway.terminalMutex.Lock()
		gateway.terminalError = err
		gateway.terminalMutex.Unlock()
		gateway.lifecycleMutex.Lock()
		cancel := gateway.cancel
		gateway.lifecycleMutex.Unlock()
		if cancel != nil {
			cancel()
		}
	})
}

// closeSockets 幂等关闭全部 frontend/current/predecessor sockets以唤醒 reader。
func (gateway *Gateway) closeSockets() {
	if gateway.frontend != nil {
		_ = gateway.frontend.Close()
	}
	for _, client := range gateway.clients {
		for _, mapping := range client.mappings {
			if mapping.connection != nil {
				_ = mapping.connection.Close()
			}
		}
	}
}
