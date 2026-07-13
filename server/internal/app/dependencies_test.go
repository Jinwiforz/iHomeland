package app

import (
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/session"
)

// 编译期断言确认 Composition Root 的生产基础依赖可直接满足 session 消费接口，
// 避免身份模块复制第二套 system clock 或 CSPRNG ID generator。
var (
	_ session.Clock       = SystemClock{}
	_ session.IDGenerator = RandomIDGenerator{}
)

// fakeClock 固定测试时间，避免 lifecycle 断言依赖墙上时钟。
type fakeClock struct {
	// now 是每次读取返回的不可变时间。
	now time.Time
}

// Now 返回测试固定时间。
func (clock fakeClock) Now() time.Time { return clock.now }

// fakeIDGenerator 固定进程 identity，避免测试消耗系统随机源。
type fakeIDGenerator struct {
	// id 是 NewID 返回的确定性值。
	id string
}

// NewID 返回测试 fixture ID。
func (generator fakeIDGenerator) NewID() (string, error) { return generator.id, nil }

// TestRuntimeDependencies 验证 production ID 强度表达和 deterministic test fake 的窄边界。
func TestRuntimeDependencies(t *testing.T) {
	identifier, err := (RandomIDGenerator{}).NewID()
	if err != nil {
		t.Fatal(err)
	}
	if len(identifier) != 32 {
		t.Fatalf("random ID must encode 16 bytes: %q", identifier)
	}
	expectedTime := time.Unix(1, 0)
	if fake := (fakeClock{now: expectedTime}); fake.Now() != expectedTime {
		t.Fatal("fake clock is not deterministic")
	}
	if fake, _ := (fakeIDGenerator{id: "fixture"}).NewID(); fake != "fixture" {
		t.Fatal("fake ID generator is not deterministic")
	}
}
