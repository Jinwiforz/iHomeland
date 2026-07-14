//go:build storage_integration

package redis

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/config"
	"github.com/jinwiforz/ihomeland/server/internal/secret"
	redisclient "github.com/redis/go-redis/v9"
)

// integrationTaskOwner 捕获 component probe，便于在 dependency stop 后同步观察 fatal 阈值。
type integrationTaskOwner struct {
	// task 是唯一 Redis required probe；测试在 fault injection 后显式运行。
	task func(context.Context) error
}

// responseLossFault 在目标 command 写入后阻塞并丢弃 response，确定性制造 commit-unknown。
type responseLossFault struct {
	// commandWritten 在 EVAL bytes 全部交给 socket 后关闭，作为请求已发送的同步屏障。
	commandWritten chan struct{}
	// releaseRead 由测试确认服务端 mutation 已落地后关闭，使被阻塞 Read 返回注入错误。
	releaseRead chan struct{}
	// writeOnce 保证连接重建或重复 Write 不会二次关闭 commandWritten。
	writeOnce sync.Once
	// releaseOnce 让成功、失败和 defer cleanup 可以安全重复释放被阻塞 Read。
	releaseOnce sync.Once
	// dropping 标记后续 Read 必须等待 releaseRead，而不能消费 Redis response。
	dropping atomic.Bool
}

// responseLossConn 包装测试专用连接，只在检测到 EVAL write 后注入 response loss。
type responseLossConn struct {
	// Conn 持有由 net.Dialer 创建且最终由 go-redis 关闭的真实 TCP connection。
	net.Conn
	// fault 由该测试 client 的全部重连共享，保证只注入一次目标故障。
	fault *responseLossFault
}

// Write 先把完整 payload 交给 socket，再为 EVAL 建立 request-sent 屏障。
func (connection *responseLossConn) Write(payload []byte) (int, error) {
	written, err := connection.Conn.Write(payload)
	if err == nil && bytes.Contains(bytes.ToUpper(payload[:written]), []byte("\r\nEVAL\r\n")) {
		connection.fault.dropping.Store(true)
		connection.fault.writeOnce.Do(func() { close(connection.fault.commandWritten) })
	}
	return written, err
}

// Read 在目标 EVAL 后等待测试确认 mutation，再返回不包含 endpoint/key/value 的稳定注入错误。
func (connection *responseLossConn) Read(payload []byte) (int, error) {
	if connection.fault.dropping.Load() {
		<-connection.fault.releaseRead
		return 0, errors.New("injected redis response loss")
	}
	return connection.Conn.Read(payload)
}

// release 解除可能阻塞的 Read；重复调用保持幂等。
func (fault *responseLossFault) release() {
	fault.releaseOnce.Do(func() { close(fault.releaseRead) })
}

// Go 保存 probe 而不并发启动，避免 fault injection 时序依赖 scheduler。
func (owner *integrationTaskOwner) Go(_ string, task func(context.Context) error) error {
	owner.task = task
	return nil
}

// Stop 保持 fake owner 幂等，不拥有外部资源。
func (*integrationTaskOwner) Stop(context.Context, error) error { return nil }

// integrationObserver 丢弃低基数观测，测试只断言 storage 行为。
type integrationObserver struct{}

// ObserveStorageProbe 满足 component observer contract。
func (integrationObserver) ObserveStorageProbe(string, string, float64) {}

// SetStoragePool 满足 component observer contract。
func (integrationObserver) SetStoragePool(string, string, int) {}

// TestRedisIntegrationRuntimeFlushRestartAndFailure 覆盖 expiry、flush、restart、commit-unknown 和 probe fatal。
func TestRedisIntegrationRuntimeFlushRestartAndFailure(t *testing.T) {
	requireIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	settings := config.DefaultStorage().Redis
	settings.Address = os.Getenv("IHOMELAND_TEST_REDIS_ADDRESS")
	settings.Probe.Interval = 100 * time.Millisecond
	settings.Probe.Timeout = 50 * time.Millisecond
	settings.Probe.FailureThreshold = 2
	password := resolveIntegrationSecret(t, os.Getenv("IHOMELAND_TEST_REDIS_PASSWORD_FILE"))
	owner := &integrationTaskOwner{}
	component, err := New(settings, password, nil, owner, integrationObserver{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := component.Start(ctx); err != nil {
		t.Fatalf("component.Start() = %v", err)
	}
	client := component.Client()
	if client == nil {
		t.Fatal("started component must expose client")
	}

	definition := testDefinition()
	registry, err := NewRegistry([]Definition{definition})
	if err != nil {
		t.Fatal(err)
	}
	keyspace, err := NewKeyspace("test", registry)
	if err != nil {
		t.Fatal(err)
	}
	key, err := keyspace.Build(definition.Name, DigestIdentity([]byte("sensitive-session")))
	if err != nil {
		t.Fatal(err)
	}
	ttl, err := TTLUntil(time.Now().Add(500*time.Millisecond), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, key.Value(), "v1", ttl).Err(); err != nil {
		t.Fatal(err)
	}
	// 额外 200ms 覆盖 Windows/CI timer 调度抖动，仍远小于测试总 deadline。
	time.Sleep(700 * time.Millisecond)
	if err := client.Get(ctx, key.Value()).Err(); !errors.Is(err, redisclient.Nil) {
		t.Fatalf("expired key error = %v", err)
	}
	if err := client.Set(ctx, key.Value(), "must-not-recover", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	restartContainer(t, os.Getenv("IHOMELAND_TEST_REDIS_CONTAINER"))
	waitForPing(t, client)
	if err := client.Get(ctx, key.Value()).Err(); !errors.Is(err, redisclient.Nil) {
		t.Fatalf("flushed key must not recover from memory: %v", err)
	}

	faultClient, fault := newResponseLossClient(t, settings, os.Getenv("IHOMELAND_TEST_REDIS_PASSWORD_FILE"))
	defer func() {
		fault.release()
		// fault client 只属于测试注入路径，主断言已覆盖其 command outcome。
		_ = faultClient.Close()
	}()
	mutationResult := make(chan error, 1)
	go func() {
		mutationResult <- faultClient.Eval(context.Background(), `return redis.call('SET', KEYS[1], ARGV[1])`, []string{key.Value()}, "uncertain").Err()
	}()
	select {
	case <-fault.commandWritten:
	case <-time.After(5 * time.Second):
		t.Fatal("测试连接未观察到 EVAL write")
	}
	waitForValue(t, client, key.Value(), "uncertain")
	fault.release()
	mutationErr := <-mutationResult
	if mutationErr == nil || ClassifyCommandError(OperationAtomicMutation, mutationErr) != CommandCommitUnknown {
		t.Fatalf("interrupted mutation outcome = %v", mutationErr)
	}
	stopContainer(t, os.Getenv("IHOMELAND_TEST_REDIS_CONTAINER"))
	probeContext, cancelProbe := context.WithTimeout(context.Background(), 5*time.Second)
	probeErr := owner.task(probeContext)
	cancelProbe()
	if probeErr == nil {
		t.Fatal("持续依赖中断必须触发 probe fatal error")
	}
	startContainer(t, os.Getenv("IHOMELAND_TEST_REDIS_CONTAINER"))
	waitForPing(t, client)
	if err := component.Stop(ctx); err != nil {
		t.Fatalf("component.Stop() = %v", err)
	}
	if err := component.Stop(ctx); err != nil {
		t.Fatalf("idempotent Stop() = %v", err)
	}
}

// waitForValue 在 deadline 内轮询权威 Redis 状态，避免用固定 sleep 猜测 command 调度时序。
func waitForValue(t *testing.T, client *redisclient.Client, key string, expected string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		value, err := client.Get(ctx, key).Result()
		if err == nil && value == expected {
			return
		}
		if err != nil && !errors.Is(err, redisclient.Nil) {
			t.Fatalf("查询 mutation 结果失败: %v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("deadline 内未观察到 mutation 结果")
		case <-ticker.C:
		}
	}
}

// newResponseLossClient 创建单连接、禁用 retry 的测试 client，并只对 EVAL response 注入 read failure。
func newResponseLossClient(t *testing.T, settings config.Redis, passwordPath string) (*redisclient.Client, *responseLossFault) {
	t.Helper()
	fault := &responseLossFault{commandWritten: make(chan struct{}), releaseRead: make(chan struct{})}
	dialer := &net.Dialer{Timeout: settings.DialTimeout}
	options := &redisclient.Options{
		Addr: settings.Address, Username: settings.Username, DB: settings.Database,
		DialTimeout: settings.DialTimeout, ReadTimeout: settings.ReadTimeout, WriteTimeout: settings.WriteTimeout,
		PoolSize: 1, MaxRetries: -1, MinRetryBackoff: -1, MaxRetryBackoff: -1,
		Dialer: func(ctx context.Context, network string, address string) (net.Conn, error) {
			connection, err := dialer.DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			return &responseLossConn{Conn: connection, fault: fault}, nil
		},
	}
	password := resolveIntegrationSecret(t, passwordPath)
	if err := password.Expose(func(value []byte) error {
		options.Password = string(value)
		return nil
	}); err != nil {
		password.Destroy()
		t.Fatal(err)
	}
	password.Destroy()
	client := redisclient.NewClient(options)
	if err := client.Ping(context.Background()).Err(); err != nil {
		// 启动失败时 client 尚未公开，关闭错误不覆盖原始 probe 断言。
		_ = client.Close()
		t.Fatalf("response-loss client startup probe failed: %v", err)
	}
	return client, fault
}

// resolveIntegrationSecret 复用 production file provider 读取 harness 私有 secret。
func resolveIntegrationSecret(t *testing.T, path string) secret.Value {
	t.Helper()
	reference, err := secret.ParseReference("file:" + path)
	if err != nil {
		t.Fatal(err)
	}
	value, err := secret.NewEnvironmentFileProvider().Resolve(context.Background(), reference)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// restartContainer 注入 dependency restart，并保留固定 loopback host port。
func restartContainer(t *testing.T, name string) {
	t.Helper()
	runDocker(t, "restart", name)
}

// stopContainer 有界停止 Redis，制造真实连接中断。
func stopContainer(t *testing.T, name string) {
	t.Helper()
	runDocker(t, "stop", "--time", "1", name)
}

// startContainer 恢复同一 container 与持久 AOF volume。
func startContainer(t *testing.T, name string) {
	t.Helper()
	runDocker(t, "start", name)
}

// runDocker 只执行 harness 已登记 container 的 fault injection。
func runDocker(t *testing.T, arguments ...string) {
	t.Helper()
	command := exec.Command("docker", arguments...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("docker operation failed: %v (%s)", err, output)
	}
}

// waitForPing 有界等待 client pool 在 container restart 后重新建连。
func waitForPing(t *testing.T, client *redisclient.Client) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := client.Ping(ctx).Err()
		cancel()
		if err == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("Redis did not recover after restart")
}

// requireIntegration 防止开发者绕过统一 harness 误连本机 Redis。
func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("IHOMELAND_STORAGE_INTEGRATION") != "1" {
		t.Skip("storage integration harness is required")
	}
}
