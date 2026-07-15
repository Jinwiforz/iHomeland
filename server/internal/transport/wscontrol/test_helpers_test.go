package wscontrol

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/jinwiforz/ihomeland/server/internal/config"
)

// testClock 为codec、限流和timestamp提供确定时间。
type testClock struct{ now time.Time }

// Now 返回固定测试时刻。
func (clock testClock) Now() time.Time { return clock.now }

// testIDs 创建不重复但可预测的测试ConnectionID材料。
type testIDs struct{ next atomic.Uint64 }

// NewID 返回符合生产标识字符边界的测试值。
func (ids *testIDs) NewID() (string, error) {
	return fmtID(ids.next.Add(1)), nil
}

// testStaticIDs 返回单个注入值，用于验证ID generator契约失败路径。
type testStaticIDs struct{ value string }

// NewID 返回测试指定的原始材料。
func (ids testStaticIDs) NewID() (string, error) { return ids.value, nil }

// fmtID 保持测试ID固定长度且不引入额外依赖。
func fmtID(value uint64) string {
	const digits = "0123456789abcdef"
	result := make([]byte, 32)
	for index := len(result) - 1; index >= 0; index-- {
		result[index] = digits[value&15]
		value >>= 4
	}
	return string(result)
}

// testObserver 实现无副作用窄observer。
type testObserver struct{}

// ObserveWSSHandshake 丢弃测试握手结果。
func (testObserver) ObserveWSSHandshake(string) {}

// SetWSSConnections 丢弃测试连接计数。
func (testObserver) SetWSSConnections(int) {}

// ObserveWSSPush 丢弃测试PUSH结果。
func (testObserver) ObserveWSSPush(uint32, string, int) {}

// ObserveWSSQueue 丢弃测试队列结果。
func (testObserver) ObserveWSSQueue(string, int, int) {}

// ObserveWSSHeartbeat 丢弃测试心跳结果。
func (testObserver) ObserveWSSHeartbeat(string) {}

// ObserveWSSClose 丢弃测试关闭原因。
func (testObserver) ObserveWSSClose(string) {}

// ObserveWSSInvalidation 丢弃测试失效结果。
func (testObserver) ObserveWSSInvalidation(string) {}

// testSocket 可阻塞reader并捕获serialized writer结果。
type testSocket struct {
	readMessage bool
	pingErr     error
	pingBlock   bool
	writeErr    error
	panicWrite  bool
	writeBlock  <-chan struct{}
	writeSignal chan struct{}
	closeBlock  <-chan struct{}
	closed      chan struct{}
	closeOnce   sync.Once
	mu          sync.Mutex
	writes      [][]byte
}

// newTestSocket 构造默认等待context取消的socket。
func newTestSocket() *testSocket { return &testSocket{closed: make(chan struct{})} }

// Read 返回一次可选application frame，否则等待连接关闭。
func (socket *testSocket) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	if socket.readMessage {
		socket.readMessage = false
		return websocket.MessageBinary, []byte{1}, nil
	}
	select {
	case <-ctx.Done():
		return 0, nil, context.Cause(ctx)
	case <-socket.closed:
		return 0, nil, errors.New("closed")
	}
}

// Write 捕获frame，或按测试场景阻塞、失败或panic。
func (socket *testSocket) Write(_ context.Context, _ websocket.MessageType, payload []byte) error {
	if socket.panicWrite {
		panic("sensitive panic text")
	}
	if socket.writeErr != nil {
		return socket.writeErr
	}
	if socket.writeSignal != nil {
		select {
		case socket.writeSignal <- struct{}{}:
		default:
		}
	}
	if socket.writeBlock != nil {
		<-socket.writeBlock
	}
	socket.mu.Lock()
	socket.writes = append(socket.writes, append([]byte(nil), payload...))
	socket.mu.Unlock()
	return nil
}

// Ping 返回测试注入的heartbeat结果，或遵守ctx阻塞到deadline以验证idle上限。
func (socket *testSocket) Ping(ctx context.Context) error {
	if socket.pingBlock {
		<-ctx.Done()
		return context.Cause(ctx)
	}
	return socket.pingErr
}

// Close 幂等广播测试socket关闭。
func (socket *testSocket) Close(websocket.StatusCode, string) error {
	if socket.closeBlock != nil {
		<-socket.closeBlock
	}
	socket.closeOnce.Do(func() { close(socket.closed) })
	return nil
}

// CloseNow 复用测试socket立即关闭信号。
func (socket *testSocket) CloseNow() error {
	socket.closeOnce.Do(func() { close(socket.closed) })
	return nil
}

// SetReadLimit 在内存测试socket中无需分配读取buffer。
func (*testSocket) SetReadLimit(int64) {}

// testConfig 返回通过生产边界且足够小的并发测试策略。
func testConfig() Config {
	return Config{Policy: config.WebSocketControlPolicy{
		Path: Path, Subprotocol: Subprotocol, AllowedHosts: []string{"127.0.0.1:8080"},
		PreAuthRate:    config.RatePolicy{Requests: 10000, Window: time.Minute, Burst: 10000},
		MaxConnections: 64, MaxPerRemote: 64, MaxPerSession: 8, MaxPerPlayer: 16,
		MaxRemoteEntries: 64, RemoteIdleTTL: time.Minute, QueueItems: 8, QueueBytes: 1 << 20,
		WriteTimeout: time.Second, PingInterval: 50 * time.Second, PongTimeout: 5 * time.Second,
		IdleTimeout: time.Minute, CloseTimeout: 50 * time.Millisecond,
	}, FrameBytes: 1 << 20, AllowPlaintext: true, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}
