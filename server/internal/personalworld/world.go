package personalworld

import (
	"errors"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
)

// Snapshot 是 storage adapter 与 domain 之间严格构造的 PersonalWorld 持久投影。
//
// 所有字段保持私有，repository 必须使用 NewSnapshot，不能通过 struct literal 绕过
// identity、owner、lifecycle、revision 或时间不变量。
type Snapshot struct {
	// id 是稳定 PersonalWorld identity。
	id PersonalWorldID
	// ownerID 是 account owner 提供的 immutable PlayerID。
	ownerID account.PlayerID
	// lifecycle 只表达持久 active/archived 状态。
	lifecycle Lifecycle
	// revision 是本 snapshot 已提交的持久版本。
	revision Revision
	// createdAt 是服务端选择并以 UTC 持久化的创建时间。
	createdAt time.Time
}

// NewSnapshot 校验 repository hydration 的完整持久事实。
//
// createdAt 被转换为 UTC、截断到 MySQL DATETIME(6) 可保存的微秒精度并移除进程内单调
// 时钟分量，使首次结果、持久 hydration 与跨进程 replay 使用同一绝对时间。构造不接受
// 部分字段，避免 adapter 产生看似有效的残缺 aggregate。
func NewSnapshot(id PersonalWorldID, ownerID account.PlayerID, lifecycle Lifecycle, revision Revision, createdAt time.Time) (Snapshot, error) {
	if !id.Valid() || !ownerID.Valid() || !lifecycle.Valid() || !revision.Valid() || createdAt.IsZero() {
		return Snapshot{}, errors.New("personal world snapshot is incomplete")
	}
	return Snapshot{id: id, ownerID: ownerID, lifecycle: lifecycle, revision: revision, createdAt: createdAt.UTC().Truncate(time.Microsecond)}, nil
}

// ID 返回稳定 PersonalWorld identity。
func (snapshot Snapshot) ID() PersonalWorldID { return snapshot.id }

// OwnerID 返回 account owner 的不可变 PlayerID 值副本。
func (snapshot Snapshot) OwnerID() account.PlayerID { return snapshot.ownerID }

// Lifecycle 返回只描述持久存在性的封闭状态。
func (snapshot Snapshot) Lifecycle() Lifecycle { return snapshot.lifecycle }

// Revision 返回 snapshot 对应的已提交版本。
func (snapshot Snapshot) Revision() Revision { return snapshot.revision }

// CreatedAt 返回移除单调分量后的 UTC 微秒创建时间。
func (snapshot Snapshot) CreatedAt() time.Time { return snapshot.createdAt }

// Valid 报告 snapshot 是否可以安全进入 domain/application。
func (snapshot Snapshot) Valid() bool {
	return snapshot.id.Valid() && snapshot.ownerID.Valid() && snapshot.lifecycle.Valid() && snapshot.revision.Valid() && !snapshot.createdAt.IsZero()
}

// Equal 比较所有持久事实，供 repository result 校验与幂等 replay 使用。
func (snapshot Snapshot) Equal(other Snapshot) bool {
	return snapshot.id == other.id && snapshot.ownerID == other.ownerID && snapshot.lifecycle == other.lifecycle && snapshot.revision == other.revision && snapshot.createdAt.Equal(other.createdAt)
}

// empty 报告 repository 是否返回严格零 snapshot，而不是部分填充的无效记录。
func (snapshot Snapshot) empty() bool { return snapshot == (Snapshot{}) }

// PersonalWorld 是个人持久世界身份与粗粒度生命周期的窄 aggregate。
//
// 它不拥有 WorldInstance、Visitor、地图、任务、奖励、资产或连接状态。所有字段私有且
// aggregate 按值返回，防止 adapter 或调用方绕过 revision 和 lifecycle transition。
type PersonalWorld struct {
	// snapshot 保存当前已知的完整持久事实。
	snapshot Snapshot
}

// NewPersonalWorld 创建 active、revision 1 的 primary PersonalWorld 候选事实。
func NewPersonalWorld(id PersonalWorldID, ownerID account.PlayerID, createdAt time.Time) (PersonalWorld, error) {
	snapshot, err := NewSnapshot(id, ownerID, LifecycleActive, InitialRevision, createdAt)
	if err != nil {
		return PersonalWorld{}, err
	}
	return PersonalWorld{snapshot: snapshot}, nil
}

// HydratePersonalWorld 从受校验 snapshot 恢复等价 aggregate。
//
// Hydration 接受 active 或 archived 持久事实，但不会执行状态迁移、修复 revision 或填充默认值。
func HydratePersonalWorld(snapshot Snapshot) (PersonalWorld, error) {
	if !snapshot.Valid() {
		return PersonalWorld{}, errors.New("personal world snapshot is invalid")
	}
	return PersonalWorld{snapshot: snapshot}, nil
}

// ID 返回 aggregate 的稳定 PersonalWorld identity。
func (world PersonalWorld) ID() PersonalWorldID { return world.snapshot.ID() }

// OwnerID 返回永不随 lifecycle 改变的 account PlayerID。
func (world PersonalWorld) OwnerID() account.PlayerID { return world.snapshot.OwnerID() }

// Lifecycle 返回当前持久存在性状态，不代表 WorldInstance 是否在线。
func (world PersonalWorld) Lifecycle() Lifecycle { return world.snapshot.Lifecycle() }

// Revision 返回当前已提交或待提交 snapshot 的乐观并发版本。
func (world PersonalWorld) Revision() Revision { return world.snapshot.Revision() }

// CreatedAt 返回世界首次创建的 UTC 绝对时间。
func (world PersonalWorld) CreatedAt() time.Time { return world.snapshot.CreatedAt() }

// Snapshot 返回不包含引用字段的持久投影值副本。
func (world PersonalWorld) Snapshot() Snapshot { return world.snapshot }

// Valid 报告 aggregate 是否来自合法创建或 hydration 路径。
func (world PersonalWorld) Valid() bool { return world.snapshot.Valid() }

// archive 构造 terminal archived snapshot，并精确推进一次 revision。
//
// PersonalWorld 使用值语义，因此失败与成功都不会修改 receiver；调用方必须只持久化返回值。
func (world PersonalWorld) archive() (PersonalWorld, error) {
	if !world.Valid() {
		return PersonalWorld{}, errors.New("personal world is invalid")
	}
	if world.Lifecycle() != LifecycleActive {
		return PersonalWorld{}, errors.New("personal world lifecycle cannot transition")
	}
	nextRevision, err := world.Revision().next()
	if err != nil {
		return PersonalWorld{}, err
	}
	snapshot, err := NewSnapshot(world.ID(), world.OwnerID(), LifecycleArchived, nextRevision, world.CreatedAt())
	if err != nil {
		return PersonalWorld{}, err
	}
	return PersonalWorld{snapshot: snapshot}, nil
}
