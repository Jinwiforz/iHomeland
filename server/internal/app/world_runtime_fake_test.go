package app

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
)

// processWorldRuntime 是 placement/application 单元测试使用的确定性进程内 fake。
//
// 它不保存PersonalWorld内容、VisitSession或socket；PlacementStore仍是current assignment
// 的唯一线性化点。registry只证明本进程已为完整stamp准备好承载既有gameplay入口。
type processWorldRuntime struct {
	// mutex 保护closed与instances，并使并发Start/Stop具有单一进程内线性化点。
	mutex sync.Mutex
	// ready 只读观察公开gameplay graph是否允许新runtime进入ready。
	ready func() bool
	// maximum 是逻辑runtime数量硬上限，replacement短暂并存也计入。
	maximum int
	// nodeID 拒绝其他进程或节点的assignment被误登记为本地runtime。
	nodeID placement.RuntimeNodeID
	// instances 按不可复活WorldInstanceID保存完整stamp。
	instances map[string]placement.AssignmentStamp
	// observer 只接收逻辑runtime数量；在首次Start前由Composition Root绑定。
	observer personalWorldSliceObserver
	// countRevision 在 runtime 数量变化时递增，防止并发 gauge 回调倒序。
	countRevision uint64
	// closed 阻止draining后重新创建runtime。
	closed bool
}

// bindObserver 在 runtime 首次使用前绑定低敏资源观测器。
func (runtime *processWorldRuntime) bindObserver(observer personalWorldSliceObserver) error {
	if runtime == nil || observer == nil {
		return errors.New("process world runtime observer is invalid")
	}
	runtime.mutex.Lock()
	if runtime.observer != nil || runtime.closed || len(runtime.instances) != 0 {
		runtime.mutex.Unlock()
		return errors.New("process world runtime observer cannot be rebound")
	}
	runtime.observer = observer
	runtime.mutex.Unlock()
	runtime.observeCount()
	return nil
}

// newProcessWorldRuntime 构造不启动goroutine且有固定容量的RuntimeController。
func newProcessWorldRuntime(nodeID placement.RuntimeNodeID, maximum int, ready func() bool) (*processWorldRuntime, error) {
	if !nodeID.Valid() || maximum < 1 || ready == nil {
		return nil, errors.New("process world runtime dependencies are incomplete")
	}
	return &processWorldRuntime{ready: ready, maximum: maximum, nodeID: nodeID, instances: make(map[string]placement.AssignmentStamp, maximum)}, nil
}

// Start 幂等登记starting assignment；返回nil只表示本地runtime ready，不发布active事实。
func (runtime *processWorldRuntime) Start(ctx context.Context, snapshot placement.AssignmentSnapshot) error {
	if runtime == nil || ctx == nil || !snapshot.Valid() || snapshot.Phase() != placement.PhaseStarting || snapshot.NodeID() != runtime.nodeID {
		return errors.New("process world runtime start input is invalid")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	runtime.mutex.Lock()
	if runtime.closed {
		runtime.mutex.Unlock()
		return errors.New("process world runtime is closed")
	}
	key := snapshot.InstanceID().String()
	if existing, exists := runtime.instances[key]; exists {
		runtime.mutex.Unlock()
		if existing.Equal(snapshot.Stamp()) {
			return nil
		}
		return errors.New("world instance identity was reused with another assignment")
	}
	if len(runtime.instances) >= runtime.maximum {
		runtime.mutex.Unlock()
		return errors.New("process world runtime capacity is exhausted")
	}
	if !runtime.ready() {
		runtime.mutex.Unlock()
		return errors.New("gameplay runtime is not ready")
	}
	runtime.instances[key] = snapshot.Stamp()
	runtime.countRevision++
	runtime.mutex.Unlock()
	runtime.observeCount()
	return nil
}

// Drain 验证 exact stamp 仍由本进程承载；旧逻辑 runtime 没有额外输入队列可排空。
func (runtime *processWorldRuntime) Drain(ctx context.Context, stamp placement.AssignmentStamp) error {
	if runtime == nil || ctx == nil || !stamp.Valid() {
		return errors.New("process world runtime drain input is invalid")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	existing, exists := runtime.instances[stamp.InstanceID().String()]
	if !exists {
		return errors.New("process world runtime drain target is missing")
	}
	if !existing.Equal(stamp) {
		return errors.New("process world runtime drain stamp is stale")
	}
	return nil
}

// Stop 只移除完整stamp匹配的predecessor；missing表示先前清理已完成。
func (runtime *processWorldRuntime) Stop(ctx context.Context, stamp placement.AssignmentStamp) error {
	if runtime == nil || ctx == nil || !stamp.Valid() {
		return errors.New("process world runtime stop input is invalid")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	runtime.mutex.Lock()
	key := stamp.InstanceID().String()
	existing, exists := runtime.instances[key]
	if !exists {
		runtime.mutex.Unlock()
		return nil
	}
	if !existing.Equal(stamp) {
		runtime.mutex.Unlock()
		return errors.New("process world runtime stop stamp is stale")
	}
	delete(runtime.instances, key)
	runtime.countRevision++
	runtime.mutex.Unlock()
	runtime.observeCount()
	return nil
}

// Close 进入terminal状态并释放全部可推导本地runtime；持久/Redis事实由各自owner清理。
func (runtime *processWorldRuntime) Close() {
	if runtime == nil {
		return
	}
	runtime.mutex.Lock()
	runtime.closed = true
	clear(runtime.instances)
	runtime.countRevision++
	runtime.mutex.Unlock()
	runtime.observeCount()
}

// observeCount 在锁外更新 gauge，并在并发数量变化后重试到最新 revision。
func (runtime *processWorldRuntime) observeCount() {
	for {
		runtime.mutex.Lock()
		observer, count, revision := runtime.observer, len(runtime.instances), runtime.countRevision
		runtime.mutex.Unlock()
		if observer == nil {
			return
		}
		observer.SetWorldRuntimes(count)
		runtime.mutex.Lock()
		stable := runtime.countRevision == revision
		runtime.mutex.Unlock()
		if stable {
			return
		}
	}
}

// contains 报告精确stamp是否仍由本进程承载，只供coordinator与测试验证本地事实。
func (runtime *processWorldRuntime) contains(stamp placement.AssignmentStamp) bool {
	if runtime == nil || !stamp.Valid() {
		return false
	}
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	existing, exists := runtime.instances[stamp.InstanceID().String()]
	return exists && existing.Equal(stamp)
}

// Contains 报告精确 stamp 是否仍由本进程承载。
func (runtime *processWorldRuntime) Contains(stamp placement.AssignmentStamp) bool {
	return runtime.contains(stamp)
}

// count 返回当前逻辑runtime数量，不暴露identity或assignment内容。
func (runtime *processWorldRuntime) count() int {
	if runtime == nil {
		return 0
	}
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	return len(runtime.instances)
}

// stampsForWorld 返回指定 PersonalWorld 的本进程完整stamp副本，不暴露给transport或日志。
func (runtime *processWorldRuntime) stampsForWorld(worldID personalworld.PersonalWorldID) []placement.AssignmentStamp {
	if runtime == nil || !worldID.Valid() {
		return nil
	}
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	stamps := make([]placement.AssignmentStamp, 0, 1)
	for _, stamp := range runtime.instances {
		if stamp.WorldID() == worldID {
			stamps = append(stamps, stamp)
		}
	}
	return stamps
}

// StampsForWorld 返回指定 PersonalWorld 的本进程完整 stamps 副本。
func (runtime *processWorldRuntime) StampsForWorld(worldID personalworld.PersonalWorldID) []placement.AssignmentStamp {
	return runtime.stampsForWorld(worldID)
}

// Stamps 返回 fake 当前全部 runtime stamps 的稳定副本。
func (runtime *processWorldRuntime) Stamps() []placement.AssignmentStamp {
	if runtime == nil {
		return nil
	}
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	stamps := make([]placement.AssignmentStamp, 0, len(runtime.instances))
	for _, stamp := range runtime.instances {
		stamps = append(stamps, stamp)
	}
	sort.Slice(stamps, func(first int, second int) bool {
		return stamps[first].InstanceID().String() < stamps[second].InstanceID().String()
	})
	return stamps
}

var _ placement.RuntimeController = (*processWorldRuntime)(nil)
