package app

import (
	"container/heap"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/visitsession"
)

// semanticDeadlineKind 是deadline queue允许的封闭业务任务集合。
type semanticDeadlineKind uint8

const (
	// semanticDeadlineUnspecified 拒绝缺失任务类型。
	semanticDeadlineUnspecified semanticDeadlineKind = iota
	// semanticDeadlineAssignmentRenew 续约本进程current assignment。
	semanticDeadlineAssignmentRenew
	// semanticDeadlineAssignmentExpiry 在lease安全边界回收本地runtime。
	semanticDeadlineAssignmentExpiry
	// semanticDeadlineVisitSession 关闭达到absolute expiry的VisitSession。
	semanticDeadlineVisitSession
	// semanticDeadlineInvite 过期仍为pending的invite。
	semanticDeadlineInvite
	// semanticDeadlineReservation 过期仍为reserved的membership。
	semanticDeadlineReservation
	// semanticDeadlineOwnerGrace 关闭matching Owner grace generation。
	semanticDeadlineOwnerGrace
	// semanticDeadlineVisitorGrace 移除matching Visitor reconnect generation。
	semanticDeadlineVisitorGrace
)

// String 返回观测和稳定task identity使用的低基数名称。
func (kind semanticDeadlineKind) String() string {
	switch kind {
	case semanticDeadlineAssignmentRenew:
		return "assignment_renew"
	case semanticDeadlineAssignmentExpiry:
		return "assignment_expiry"
	case semanticDeadlineVisitSession:
		return "visit_session"
	case semanticDeadlineInvite:
		return "invite"
	case semanticDeadlineReservation:
		return "reservation"
	case semanticDeadlineOwnerGrace:
		return "owner_grace"
	case semanticDeadlineVisitorGrace:
		return "visitor_grace"
	default:
		return "unspecified"
	}
}

// Valid 报告kind是否可以进入production queue。
func (kind semanticDeadlineKind) Valid() bool {
	return kind >= semanticDeadlineAssignmentRenew && kind <= semanticDeadlineVisitorGrace
}

// semanticDeadlineKey 唯一定位一个可替换语义任务，不包含socket、payload或credential。
type semanticDeadlineKey struct {
	// kind 区分相同aggregate上的不同到期操作。
	kind semanticDeadlineKind
	// scope 是assignment instance或VisitSession identity。
	scope string
	// target 是可选invite/Visitor identity；aggregate任务为空。
	target string
	// binding 是可选精确connection binding。
	binding string
	// generation 防止旧grace任务覆盖较新generation。
	generation uint64
}

// Valid 限制内部identity尺寸，避免错误adapter值放大queue key。
func (key semanticDeadlineKey) Valid() bool {
	if !key.kind.Valid() || !boundedDeadlineIdentity(key.scope) || len(key.target) > 128 || len(key.binding) > 128 {
		return false
	}
	if (key.target != "" && !boundedDeadlineIdentity(key.target)) || (key.binding != "" && !boundedDeadlineIdentity(key.binding)) {
		return false
	}
	return true
}

// stableValue 返回只在进程内用于map与哈希的确定性表达，不进入普通日志。
func (key semanticDeadlineKey) stableValue() string {
	return key.kind.String() + "\x00" + key.scope + "\x00" + key.target + "\x00" + key.binding + "\x00" + strconv.FormatUint(key.generation, 10)
}

// boundedDeadlineIdentity 仅接受已有owner identifier使用的安全ASCII子集。
func boundedDeadlineIdentity(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') && !(character >= '0' && character <= '9') && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

// semanticDeadlineTask 保存执行时必须重新提交给领域/store校验的稳定条件。
type semanticDeadlineTask struct {
	// key 是queue去重和旧callback拒绝的业务identity。
	key semanticDeadlineKey
	// deadline 是worker下一次执行时点；dependency retry只推进该值。
	deadline time.Time
	// conditionDeadline 是领域command必须重复提交的原始absolute deadline；run retry只改deadline。
	conditionDeadline time.Time
	// revision 是登记snapshot的expected revision；assignment任务允许为0。
	revision uint64
	// execute 在worker goroutine执行，不得自行启动后台任务。
	execute func(context.Context, semanticDeadlineTask) error
	// index 由heap维护，禁止业务读取。
	index int
}

// valid 校验任务完整性并规范deadline精度。
func (task semanticDeadlineTask) valid() bool {
	if !task.key.Valid() || task.deadline.IsZero() || task.execute == nil {
		return false
	}
	if task.key.kind >= semanticDeadlineVisitSession && task.conditionDeadline.IsZero() {
		return false
	}
	if task.key.kind >= semanticDeadlineVisitSession && task.revision == 0 {
		return false
	}
	return true
}

// visitCommandID 从完整task identity确定性派生VisitSession system command。
func (task semanticDeadlineTask) visitCommandID() (visitsession.CommandID, error) {
	if !task.valid() || task.key.kind < semanticDeadlineVisitSession {
		return visitsession.CommandID{}, errors.New("semantic deadline is not a visit command")
	}
	canonical := strings.Join([]string{task.key.stableValue(), strconv.FormatInt(task.conditionDeadline.UTC().Truncate(time.Microsecond).UnixMicro(), 10), strconv.FormatUint(task.revision, 10)}, "\x00")
	digest := sha256.Sum256([]byte(canonical))
	return visitsession.NewCommandID("vcmd_" + hex.EncodeToString(digest[:16]))
}

// semanticDeadlineHeap 按deadline、stable key确定性排序。
type semanticDeadlineHeap []*semanticDeadlineTask

// Len 返回当前语义任务数量。
func (entries semanticDeadlineHeap) Len() int { return len(entries) }

// Less 按绝对时间与稳定 key 提供确定性顺序。
func (entries semanticDeadlineHeap) Less(left, right int) bool {
	if entries[left].deadline.Equal(entries[right].deadline) {
		return entries[left].key.stableValue() < entries[right].key.stableValue()
	}
	return entries[left].deadline.Before(entries[right].deadline)
}

// Swap 交换任务并同步 heap 索引。
func (entries semanticDeadlineHeap) Swap(left, right int) {
	entries[left], entries[right] = entries[right], entries[left]
	entries[left].index, entries[right].index = left, right
}

// Push 追加一个已校验任务并记录其 heap 索引。
func (entries *semanticDeadlineHeap) Push(value any) {
	task := value.(*semanticDeadlineTask)
	task.index = len(*entries)
	*entries = append(*entries, task)
}

// Pop 移除最末任务并清除其 heap 索引。
func (entries *semanticDeadlineHeap) Pop() any {
	old := *entries
	last := len(old) - 1
	task := old[last]
	old[last] = nil
	task.index = -1
	*entries = old[:last]
	return task
}

// semanticTimer 是worker唯一活动timer的可测试最窄接口。
type semanticTimer interface {
	// C 返回到期信号；同一worker任意时刻最多等待一个该通道。
	C() <-chan time.Time
	// Stop 撤销尚未到期的等待，返回值沿用time.Timer停止语义。
	Stop() bool
}

// semanticDeadlineClock 同时提供绝对时间与单个可取消timer。
type semanticDeadlineClock interface {
	// Now 返回用于比较绝对语义deadline的当前时间。
	Now() time.Time
	// NewTimer 创建worker唯一拥有的相对等待timer。
	NewTimer(time.Duration) semanticTimer
}

// systemSemanticClock 为production worker桥接Go time package。
type systemSemanticClock struct{}

// Now 返回 production wall clock 当前时间。
func (systemSemanticClock) Now() time.Time { return time.Now() }

// NewTimer 创建 production worker 使用的一次性 timer。
func (systemSemanticClock) NewTimer(duration time.Duration) semanticTimer {
	return goSemanticTimer{timer: time.NewTimer(duration)}
}

// applicationSemanticClock 复用Composition Root clock计算绝对时间，并只用Go timer等待相对时长。
type applicationSemanticClock struct{ Clock }

// NewTimer 仅负责相对等待；绝对时间仍由嵌入的应用 Clock 提供。
func (clock applicationSemanticClock) NewTimer(duration time.Duration) semanticTimer {
	return goSemanticTimer{timer: time.NewTimer(duration)}
}

// goSemanticTimer 隐藏*time.Timer，便于fake clock替换。
type goSemanticTimer struct{ timer *time.Timer }

// C 返回底层 timer 的只读触发通道。
func (timer goSemanticTimer) C() <-chan time.Time { return timer.timer.C }

// Stop 取消尚未触发的底层 timer。
func (timer goSemanticTimer) Stop() bool { return timer.timer.Stop() }

// semanticDeadlineOwner 以单worker和单timer拥有全部可恢复语义deadline。
type semanticDeadlineOwner struct {
	// mutex 保护entries、byKey、running与closed。
	mutex sync.Mutex
	// clock 可由测试确定性推进。
	clock semanticDeadlineClock
	// maximum 是entry硬上限。
	maximum int
	// wake 合并schedule/cancel通知，避免调用方阻塞。
	wake chan struct{}
	// entries 是最早deadline优先队列。
	entries semanticDeadlineHeap
	// byKey 支持同一语义任务原位替换。
	byKey map[string]*semanticDeadlineTask
	// observer 在worker启动前绑定，只接收queue数量与低基数kind/outcome。
	observer personalWorldSliceObserver
	// countRevision 在 entry 数量变化时递增，防止并发 observer 回调覆盖较新 gauge。
	countRevision uint64
	// running 防止多个worker破坏执行顺序。
	running bool
	// closed 使draining后Schedule fail closed。
	closed bool
}

// BindObserver 在 worker 启动前一次性绑定低敏资源观测器。
func (owner *semanticDeadlineOwner) BindObserver(observer personalWorldSliceObserver) error {
	if owner == nil || observer == nil {
		return errors.New("semantic deadline observer is invalid")
	}
	owner.mutex.Lock()
	if owner.observer != nil || owner.running || owner.closed || len(owner.entries) != 0 {
		owner.mutex.Unlock()
		return errors.New("semantic deadline observer cannot be rebound")
	}
	owner.observer = observer
	owner.mutex.Unlock()
	owner.observeCount()
	return nil
}

// newSemanticDeadlineOwner 构造不启动goroutine的有界deadline owner。
func newSemanticDeadlineOwner(maximum int, clock semanticDeadlineClock) (*semanticDeadlineOwner, error) {
	if maximum < 1 || clock == nil {
		return nil, errors.New("semantic deadline owner dependencies are incomplete")
	}
	return &semanticDeadlineOwner{clock: clock, maximum: maximum, wake: make(chan struct{}, 1), byKey: make(map[string]*semanticDeadlineTask, maximum)}, nil
}

// Schedule 新增或原位替换同key任务；事实已提交后capacity错误必须由调用方撤销readiness。
func (owner *semanticDeadlineOwner) Schedule(task semanticDeadlineTask) error {
	if owner == nil || !task.valid() {
		return errors.New("semantic deadline task is invalid")
	}
	task.deadline = task.deadline.UTC().Truncate(time.Microsecond)
	if !task.conditionDeadline.IsZero() {
		task.conditionDeadline = task.conditionDeadline.UTC().Truncate(time.Microsecond)
	}
	key := task.key.stableValue()
	owner.mutex.Lock()
	if owner.closed {
		owner.mutex.Unlock()
		return errors.New("semantic deadline owner is closed")
	}
	if existing, exists := owner.byKey[key]; exists {
		existing.deadline = task.deadline
		existing.conditionDeadline = task.conditionDeadline
		existing.revision = task.revision
		existing.execute = task.execute
		heap.Fix(&owner.entries, existing.index)
		owner.mutex.Unlock()
		owner.notify()
		return nil
	}
	if len(owner.entries) >= owner.maximum {
		owner.mutex.Unlock()
		return errors.New("semantic deadline capacity is exhausted")
	}
	copyTask := task
	heap.Push(&owner.entries, &copyTask)
	owner.byKey[key] = &copyTask
	owner.countRevision++
	owner.mutex.Unlock()
	owner.observeCount()
	owner.notify()
	return nil
}

// Cancel 删除精确key；missing表示较新snapshot已经收敛过该任务。
func (owner *semanticDeadlineOwner) Cancel(key semanticDeadlineKey) {
	if owner == nil || !key.Valid() {
		return
	}
	stable := key.stableValue()
	owner.mutex.Lock()
	removed := false
	if existing, exists := owner.byKey[stable]; exists {
		heap.Remove(&owner.entries, existing.index)
		delete(owner.byKey, stable)
		owner.countRevision++
		removed = true
	}
	owner.mutex.Unlock()
	if removed {
		owner.observeCount()
	}
	owner.notify()
}

// Run 执行唯一worker loop；callback error作为受监督fatal failure返回。
func (owner *semanticDeadlineOwner) Run(ctx context.Context) error {
	if owner == nil || ctx == nil {
		return errors.New("semantic deadline run context is invalid")
	}
	owner.mutex.Lock()
	if owner.running || owner.closed {
		owner.mutex.Unlock()
		return errors.New("semantic deadline owner cannot run")
	}
	owner.running = true
	owner.mutex.Unlock()
	defer func() {
		owner.mutex.Lock()
		owner.running = false
		owner.mutex.Unlock()
	}()
	for {
		task, wait, closed := owner.next()
		if closed {
			return nil
		}
		if task != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			if err := task.execute(ctx, *task); err != nil {
				if ctx.Err() != nil || owner.isClosed() {
					return nil
				}
				owner.observeExecution(task.key.kind, "failed")
				return err
			}
			owner.observeExecution(task.key.kind, "executed")
			continue
		}
		if wait < 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-owner.wake:
				continue
			}
		}
		timer := owner.clock.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-owner.wake:
			timer.Stop()
		case <-timer.C():
		}
	}
}

// Close 阻止新增任务、唤醒worker并清空所有可推导entry。
func (owner *semanticDeadlineOwner) Close() {
	if owner == nil {
		return
	}
	owner.mutex.Lock()
	owner.closed = true
	clear(owner.byKey)
	clear(owner.entries)
	owner.entries = nil
	owner.countRevision++
	owner.mutex.Unlock()
	owner.observeCount()
	owner.notify()
}

// Len 返回低敏资源计数。
func (owner *semanticDeadlineOwner) Len() int {
	if owner == nil {
		return 0
	}
	owner.mutex.Lock()
	defer owner.mutex.Unlock()
	return len(owner.entries)
}

// next 原子弹出到期任务，或返回距离最早 deadline 的等待时间；closed 区分终止与空 queue。
func (owner *semanticDeadlineOwner) next() (*semanticDeadlineTask, time.Duration, bool) {
	owner.mutex.Lock()
	if owner.closed {
		owner.mutex.Unlock()
		return nil, 0, true
	}
	if len(owner.entries) == 0 {
		owner.mutex.Unlock()
		return nil, -1, false
	}
	now := owner.clock.Now().UTC()
	first := owner.entries[0]
	if first.deadline.After(now) {
		wait := first.deadline.Sub(now)
		owner.mutex.Unlock()
		return nil, wait, false
	}
	task := heap.Pop(&owner.entries).(*semanticDeadlineTask)
	delete(owner.byKey, task.key.stableValue())
	owner.countRevision++
	owner.mutex.Unlock()
	owner.observeCount()
	return task, 0, false
}

// isClosed 报告 owner 是否已进入 terminal draining 状态。
func (owner *semanticDeadlineOwner) isClosed() bool {
	owner.mutex.Lock()
	defer owner.mutex.Unlock()
	return owner.closed
}

// observeCount 在锁外更新 gauge，并在并发数量变化后重试，避免旧回调成为最终观测值。
func (owner *semanticDeadlineOwner) observeCount() {
	for {
		owner.mutex.Lock()
		observer, count, revision := owner.observer, len(owner.entries), owner.countRevision
		owner.mutex.Unlock()
		if observer == nil {
			return
		}
		observer.SetSemanticDeadlines(count)
		owner.mutex.Lock()
		stable := owner.countRevision == revision
		owner.mutex.Unlock()
		if stable {
			return
		}
	}
}

// observeExecution 避免在Run主循环中暴露observer字段的并发绑定细节。
func (owner *semanticDeadlineOwner) observeExecution(kind semanticDeadlineKind, outcome string) {
	owner.mutex.Lock()
	observer := owner.observer
	owner.mutex.Unlock()
	if observer != nil {
		observer.ObserveSemanticDeadline(kind.String(), outcome)
	}
}

// notify 合并多个变更，worker每次唤醒都会重新读取heap head。
func (owner *semanticDeadlineOwner) notify() {
	select {
	case owner.wake <- struct{}{}:
	default:
	}
}
