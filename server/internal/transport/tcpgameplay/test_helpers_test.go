package tcpgameplay

import (
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/config"
	"github.com/jinwiforz/ihomeland/server/internal/contract"
)

// testIDs 为并发registry测试生成规范且不重复的32位十六进制材料。
type testIDs struct{ next atomic.Uint64 }

// NewID 返回确定性递增材料。
func (ids *testIDs) NewID() (string, error) { return fmt.Sprintf("%032x", ids.next.Add(1)), nil }

// testObserver 实现全量低基数观测端口，测试按需覆盖计数。
type testObserver struct{}

// ObserveTCPHandshake 忽略握手事件。
func (*testObserver) ObserveTCPHandshake(string, string) {}

// SetTCPConnections 忽略连接gauge。
func (*testObserver) SetTCPConnections(string, int) {}

// ObserveTCPFrame 忽略frame事件。
func (*testObserver) ObserveTCPFrame(string, string, int) {}

// ObserveTCPDispatch 忽略dispatch事件。
func (*testObserver) ObserveTCPDispatch(uint32, string, time.Duration) {}

// AddTCPInFlight 忽略执行中operation数量变化。
func (*testObserver) AddTCPInFlight(int) {}

// ObserveTCPQueue 忽略queue事件。
func (*testObserver) ObserveTCPQueue(string, int, int) {}

// ObserveTCPPush 忽略push事件。
func (*testObserver) ObserveTCPPush(uint32, string) {}

// ObserveTCPClose 忽略关闭事件。
func (*testObserver) ObserveTCPClose(string) {}

// ObserveTCPInvalidation 忽略失效事件。
func (*testObserver) ObserveTCPInvalidation(string) {}

// testRuntime 构造经过配置与contract校验的codec/registry。
func testRuntime(t testing.TB) (Config, *Codec, *Registry) {
	t.Helper()
	policy := config.DefaultPublicAPI().GameplayTCP
	runtimeConfig := Config{Policy: policy, FrameBytes: 65536, AllowPlaintext: true, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	codec, err := NewCodec(contract.TLSGameplayCatalog(), runtimeConfig.FrameBytes, fixedClock{value: testNow})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(runtimeConfig, codec, fixedClock{value: testNow}, new(testIDs), new(testObserver))
	if err != nil {
		t.Fatal(err)
	}
	return runtimeConfig, codec, registry
}
