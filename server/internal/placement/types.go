package placement

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
)

const (
	// worldInstanceIDPrefix 把通用随机材料固定到 WorldInstance namespace。
	worldInstanceIDPrefix = "winst_"
	// runtimeNodeIDPrefix 区分受信运行节点与 endpoint、进程地址或玩家 identity。
	runtimeNodeIDPrefix = "rnode_"
	// maximumIdentifierBytes 限制运行态索引、诊断值和 adapter key 的输入规模。
	maximumIdentifierBytes = 128
	// redactedFencingToken 防止默认格式化扩散持久写入授权序列。
	redactedFencingToken = "[REDACTED_FENCING_TOKEN]"
)

// WorldInstanceID 标识一次不可复活的 PersonalWorld 运行承载。
//
// ID 由受信服务端生成；lease 过期、重建或迁移后必须生成新值，不能让客户端选择或
// 通过复用旧值恢复 assignment 资格。
type WorldInstanceID struct {
	// value 保存带 WorldInstance namespace 前缀的安全 ASCII identifier。
	value string
}

// NewWorldInstanceID 校验服务端生成或 store hydration 返回的运行实例 identity。
func NewWorldInstanceID(value string) (WorldInstanceID, error) {
	if err := validateIdentifier(value, worldInstanceIDPrefix); err != nil {
		return WorldInstanceID{}, fmt.Errorf("world instance identifier: %w", err)
	}
	return WorldInstanceID{value: value}, nil
}

// String 返回 adapter 索引和受控关联日志使用的 WorldInstance identifier。
func (id WorldInstanceID) String() string { return id.value }

// Valid 报告 WorldInstanceID 是否经过完整构造。
func (id WorldInstanceID) Valid() bool { return id.value != "" }

// RuntimeNodeID 标识受信服务端运行节点，不表示可连接 endpoint。
//
// Node identity 只参与 placement stamp 与 runtime controller 路由；协议 URL、端口和
// 客户端地址必须由后续 endpoint owner 单独解析。
type RuntimeNodeID struct {
	// value 保存带 RuntimeNode namespace 前缀的安全 ASCII identifier。
	value string
}

// NewRuntimeNodeID 校验 Composition Root 或 store hydration 提供的节点 identity。
func NewRuntimeNodeID(value string) (RuntimeNodeID, error) {
	if err := validateIdentifier(value, runtimeNodeIDPrefix); err != nil {
		return RuntimeNodeID{}, fmt.Errorf("runtime node identifier: %w", err)
	}
	return RuntimeNodeID{value: value}, nil
}

// String 返回 adapter 索引和受控内部路由使用的 RuntimeNode identifier。
func (id RuntimeNodeID) String() string { return id.value }

// Valid 报告 RuntimeNodeID 是否经过完整构造。
func (id RuntimeNodeID) Valid() bool { return id.value != "" }

// AssignmentGeneration 是 current assignment 对控制面可观察的单调版本。
//
// Generation 由 PlacementStore 按 PersonalWorld 原子分配，不能由 wall clock 或客户端
// 输入推导；已经撤销的值不得在 allocator 恢复后复用。
type AssignmentGeneration uint64

// NewAssignmentGeneration 校验 store hydration 返回的正 generation。
func NewAssignmentGeneration(value uint64) (AssignmentGeneration, error) {
	if value == 0 {
		return 0, errors.New("assignment generation must be positive")
	}
	return AssignmentGeneration(value), nil
}

// Uint64 返回 adapter 持久化和后续协议投影使用的 generation 数值。
func (generation AssignmentGeneration) Uint64() uint64 { return uint64(generation) }

// Valid 报告 generation 是否可以进入 assignment stamp。
func (generation AssignmentGeneration) Valid() bool { return generation > 0 }

// String 返回 generation 的低敏诊断表达；它不具备写入授权能力。
func (generation AssignmentGeneration) String() string {
	return strconv.FormatUint(uint64(generation), 10)
}

// FencingToken 是持久 mutation 边界必须重新比较的单调写者序列。
//
// Token 只能由 PlacementStore 按 PersonalWorld 分配。默认格式化始终脱敏；adapter 仅能
// 通过 Uint64 取得精确值，普通错误和日志不得输出该数值。
type FencingToken uint64

// NewFencingToken 校验 store hydration 返回的正 fencing token。
func NewFencingToken(value uint64) (FencingToken, error) {
	if value == 0 {
		return 0, errors.New("fencing token must be positive")
	}
	return FencingToken(value), nil
}

// Uint64 返回持久 fence 比较边界使用的精确无符号值。
func (token FencingToken) Uint64() uint64 { return uint64(token) }

// Valid 报告 token 是否可以进入受信 assignment stamp。
func (token FencingToken) Valid() bool { return token > 0 }

// String 防止默认格式化把 fencing token 写入日志或边界错误。
func (FencingToken) String() string { return redactedFencingToken }

// GoString 防止 `%#v` 绕过普通 String 脱敏边界。
func (FencingToken) GoString() string { return redactedFencingToken }

// LogValue 让结构化日志只记录稳定占位值。
func (FencingToken) LogValue() slog.Value { return slog.StringValue(redactedFencingToken) }

// Phase 表达 current assignment 是否已经发布为 gameplay active。
type Phase uint8

const (
	// PhaseUnspecified 是禁止进入 snapshot 与 store 的零值。
	PhaseUnspecified Phase = iota
	// PhaseStarting 表示候选 runtime 已获得独占 lease，但尚未报告 ready。
	PhaseStarting
	// PhaseActive 表示 runtime 已报告 ready；实际写入仍必须重新验证 lease 与 fence。
	PhaseActive
)

// String 返回 storage enum、指标与诊断使用的稳定低基数名称。
func (phase Phase) String() string {
	switch phase {
	case PhaseStarting:
		return "starting"
	case PhaseActive:
		return "active"
	default:
		return "unspecified"
	}
}

// Valid 报告 phase 是否属于 current assignment 的封闭状态集合。
func (phase Phase) Valid() bool { return phase == PhaseStarting || phase == PhaseActive }

// validateIdentifier 统一执行运行 identity 的 namespace、长度与字符约束。
func validateIdentifier(value string, prefix string) error {
	if !strings.HasPrefix(value, prefix) || len(value) <= len(prefix) || len(value) > maximumIdentifierBytes {
		return errors.New("prefix or length is invalid")
	}
	for index := len(prefix); index < len(value); index++ {
		if !asciiAlphaNumeric(value[index]) {
			return errors.New("contains unsupported characters")
		}
	}
	return nil
}

// asciiAlphaNumeric 限制 identity 为跨数据库、Redis 和协议稳定的 ASCII 子集。
func asciiAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}
