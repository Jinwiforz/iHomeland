package workload

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/correlation"
	battlev1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/battle/v1"
	"google.golang.org/protobuf/proto"
)

const (
	// SimulationCadence 是 B0.1/B0.2 冻结的 20 Hz simulation step。
	SimulationCadence = 50 * time.Millisecond
	// InputCadence 是 B0.2 冻结的 40 Hz input step。
	InputCadence = 25 * time.Millisecond
	// ProbeCadence 是 registry 冻结的 4 Hz probe 上限。
	ProbeCadence = 250 * time.Millisecond
	// inputBundleDepth 是当前 input 加两个历史 command 的固定冗余深度。
	inputBundleDepth = 3
	// maximumAxisMilli 是 BattleInputCommand 量化 movement 轴上限。
	maximumAxisMilli = 1000
	// baselineAxisMilli 是 clean workload 的低幅持续 movement。
	baselineAxisMilli = 250
	// alternatingPatternPeriod 让相邻 movement/boss 输入稳定交替。
	alternatingPatternPeriod = 2
	// baseInputTick 是每个 mapping generation 的首个 InputTick。
	baseInputTick = uint64(1)
	// baseSimulationTick 是 InputTick rational mapping 的权威锚点。
	baseSimulationTick = uint64(1)
	// inputTicksPerSimulationTick 是 40 Hz input 与 20 Hz simulation 的精确比值。
	inputTicksPerSimulationTick = uint64(2)
)

// Snapshot 是生成下一输入时可见的客户端只读接收进度。
type Snapshot struct {
	// MappingGeneration 是当前 session 的 InputTick epoch。
	MappingGeneration uint64
	// LatestServerTick 是已经应用的最新权威 server tick。
	LatestServerTick uint64
	// LatestSnapshotSequence 是已经应用的最新 snapshot sequence。
	LatestSnapshotSequence uint64
	// LastProcessedInputTick 是完整 snapshot 发布的连续输入确认。
	LastProcessedInputTick uint64
}

// Sink 是 workload driver 唯一需要的 battle protocol client 端口。
type Sink interface {
	// SendInput 把 typed bundle 发送到 raw lane。
	SendInput(context.Context, *battlev1.BattleInputBundle) error
	// SendProbe 把 typed probe 发送到 raw lane。
	SendProbe(context.Context, *battlev1.BattleProbe) error
}

// Driver 拥有单个 BattleSession generation 的 input/probe sequence。
type Driver struct {
	// phase 决定输入 kind 与量化参数，不改变 cadence。
	phase correlation.WorkloadPhase
	// nextCommandSequence 是下一个唯一 command sequence。
	nextCommandSequence uint64
	// nextProbeSequence 是下一个唯一 probe sequence。
	nextProbeSequence uint64
	// inputStep 是从零开始的 40 Hz 调用计数。
	inputStep uint64
	// nextInputTick 是首次权威 snapshot 对齐后的 40 Hz 采样 identity。
	nextInputTick uint64
	// history 保存最多三个 command 的独立 protobuf 副本。
	history []*battlev1.BattleInputCommand
}

// New 创建从 sequence 1 开始的 closed workload driver。
func New(phase correlation.WorkloadPhase) (*Driver, error) {
	if !phase.Valid() || phase == correlation.PhaseAdmission {
		return nil, errors.New("workload phase cannot drive battle input")
	}
	return &Driver{
		phase:               phase,
		nextCommandSequence: 1,
		nextProbeSequence:   1,
		history:             make([]*battlev1.BattleInputCommand, 0, inputBundleDepth),
	}, nil
}

// Transition 切换 closed workload phase，同时保留 session 内 sequence、InputTick 与冗余历史。
func (driver *Driver) Transition(phase correlation.WorkloadPhase) error {
	if driver == nil || !phase.Valid() || phase == correlation.PhaseAdmission {
		return errors.New("workload phase transition is invalid")
	}
	driver.phase = phase
	return nil
}

// Run 以冻结 40 Hz cadence 驱动到 deadline；disconnect-drain 只保持连接不发输入。
func (driver *Driver) Run(
	ctx context.Context,
	duration time.Duration,
	latest func() Snapshot,
	sink Sink,
) error {
	if driver == nil || ctx == nil || duration <= 0 || latest == nil || sink == nil {
		return errors.New("workload run input is invalid")
	}
	inputTicker := time.NewTicker(InputCadence)
	defer inputTicker.Stop()
	deadline := time.NewTimer(duration)
	defer deadline.Stop()
	started := time.Now()
	for {
		select {
		case <-inputTicker.C:
			snapshot := latest()
			bundle, err := driver.NextInput(snapshot.LatestServerTick)
			if err != nil {
				return err
			}
			if bundle != nil {
				if err := sink.SendInput(ctx, bundle); err != nil {
					return err
				}
			}
			if driver.inputStep%uint64(ProbeCadence/InputCadence) == 0 {
				elapsedMicroseconds := time.Since(started).Microseconds()
				if elapsedMicroseconds <= 0 {
					return errors.New("workload monotonic clock did not advance")
				}
				probe := driver.NextProbe(snapshot, uint64(elapsedMicroseconds))
				if err := sink.SendProbe(ctx, probe); err != nil {
					return err
				}
			}
		case <-deadline.C:
			return nil
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
}

// NextInput 生成下一个 typed input bundle；disconnect-drain 表示显式 input gap。
func (driver *Driver) NextInput(
	latestServerTick uint64,
) (*battlev1.BattleInputBundle, error) {
	if driver == nil {
		return nil, errors.New("workload driver is unavailable")
	}
	driver.inputStep++
	if driver.nextInputTick == 0 {
		if latestServerTick < baseSimulationTick {
			return nil, nil
		}
		serverDelta := latestServerTick - baseSimulationTick
		if serverDelta > (math.MaxUint64-baseInputTick)/
			inputTicksPerSimulationTick {
			return nil, errors.New("workload InputTick alignment overflowed")
		}
		driver.nextInputTick = baseInputTick +
			serverDelta*inputTicksPerSimulationTick
	}
	inputTick := driver.nextInputTick
	if inputTick == math.MaxUint64 {
		return nil, errors.New("workload InputTick exhausted")
	}
	driver.nextInputTick++
	if driver.phase == correlation.PhaseDisconnectDrain {
		return nil, nil
	}
	command := driver.nextCommand()
	driver.history = append(driver.history, command)
	if len(driver.history) > inputBundleDepth {
		driver.history = driver.history[len(driver.history)-inputBundleDepth:]
	}
	commands := make([]*battlev1.BattleInputCommand, len(driver.history))
	for index, historical := range driver.history {
		commands[index] = proto.Clone(historical).(*battlev1.BattleInputCommand)
	}
	return battlev1.BattleInputBundle_builder{
		NewestInputTick:          proto.Uint64(inputTick),
		LatestObservedServerTick: proto.Uint64(latestServerTick),
		Commands:                 commands,
	}.Build(), nil
}

// NextProbe 生成与当前 snapshot 进度关联的 4 Hz typed probe。
func (driver *Driver) NextProbe(snapshot Snapshot, monotonicTimeUS uint64) *battlev1.BattleProbe {
	if driver == nil || monotonicTimeUS == 0 {
		return nil
	}
	sequence := driver.nextProbeSequence
	driver.nextProbeSequence++
	return battlev1.BattleProbe_builder{
		ProbeSequence:          proto.Uint64(sequence),
		LatestSnapshotSequence: proto.Uint64(snapshot.LatestSnapshotSequence),
		ClientMonotonicTimeUs:  proto.Uint64(monotonicTimeUS),
	}.Build()
}

// nextCommand 只从 BattleInputKind allowlist 生成请求，不包含权威结果字段。
func (driver *Driver) nextCommand() *battlev1.BattleInputCommand {
	sequence := driver.nextCommandSequence
	driver.nextCommandSequence++
	kind := battlev1.BattleInputKind_BATTLE_INPUT_KIND_MOVE
	builder := battlev1.BattleInputCommand_builder{
		CommandSequence: proto.Uint64(sequence),
		Kind:            &kind,
		MoveXMilli:      proto.Int32(baselineAxisMilli),
		MoveYMilli:      proto.Int32(0),
	}
	switch driver.phase {
	case correlation.PhaseIdle:
		builder.MoveXMilli = proto.Int32(0)
		builder.MoveYMilli = proto.Int32(0)
	case correlation.PhaseMovementHeavy, correlation.PhaseKCPRetransmit,
		correlation.PhaseQueuePressure:
		axis := int32(maximumAxisMilli)
		if sequence%alternatingPatternPeriod == 0 {
			axis = -axis
		}
		builder.MoveXMilli = proto.Int32(axis)
		builder.MoveYMilli = proto.Int32(-axis)
	case correlation.PhaseCombatHeavy:
		kind = battlev1.BattleInputKind_BATTLE_INPUT_KIND_PRIMARY_ABILITY
		builder = battlev1.BattleInputCommand_builder{
			CommandSequence: proto.Uint64(sequence), Kind: &kind,
		}
	case correlation.PhaseBossBurst:
		kind = battlev1.BattleInputKind_BATTLE_INPUT_KIND_PRIMARY_ABILITY
		if sequence%alternatingPatternPeriod == 0 {
			kind = battlev1.BattleInputKind_BATTLE_INPUT_KIND_SECONDARY_ABILITY
		}
		builder = battlev1.BattleInputCommand_builder{
			CommandSequence: proto.Uint64(sequence), Kind: &kind,
		}
	}
	return builder.Build()
}
