package testclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
)

const (
	// ManifestSchemaVersion 是当前资格场景清单格式版本。
	ManifestSchemaVersion = 1
	// QualificationVersion 是当前服务端发布资格代际。
	QualificationVersion = "server-v1"
	// maximumScenarioTimeoutMS 限制单场景预算，避免清单创建无界等待。
	maximumScenarioTimeoutMS = 120_000
	// maximumProfileBytes 限制资源 profile 的累计处理字节上限，不形成 soak/benchmark。
	maximumProfileBytes = 16 << 20
)

var (
	// knownGroups 是场景 owner capability 的封闭集合。
	knownGroups = stringSet("contract", "identity", "own_world", "visit_world", "security", "recovery", "resource", "lifecycle")
	// knownPhases 是统一入口可调度阶段的封闭集合。
	knownPhases = stringSet("contract", "functional", "recovery", "resource", "shutdown")
	// knownOutcomes 是 manifest 可声明的成功条件封闭集合。
	knownOutcomes = stringSet("pass")
	// knownEvidence 是资格报告允许引用的证据层级封闭集合。
	knownEvidence = stringSet("contract", "black_box", "storage", "race")
	// knownExecutions 区分当前进程公开 wire、入口前置契约和既有分层证明，防止报告伪称黑盒覆盖。
	knownExecutions = stringSet("contract", "black_box", "layered")
)

// Manifest 固定一次资格运行必须覆盖的版本和场景集合。
type Manifest struct {
	// SchemaVersion 选择 manifest JSON 结构代际。
	SchemaVersion int `json:"schemaVersion"`
	// QualificationVersion 标识被验收的服务端能力基线。
	QualificationVersion string `json:"qualificationVersion"`
	// Scenarios 按提交顺序登记全部公开资格场景。
	Scenarios []ScenarioDefinition `json:"scenarios"`
}

// ScenarioDefinition 是不含运行秘密和内部实现细节的场景元数据。
type ScenarioDefinition struct {
	// ID 是跨版本稳定且安全的场景 identity。
	ID string `json:"id"`
	// Group 是拥有该场景的公开 capability group。
	Group string `json:"group"`
	// Mandatory 决定 skipped 是否令完整资格失败。
	Mandatory bool `json:"mandatory"`
	// Phase 是统一入口中的隔离执行阶段。
	Phase string `json:"phase"`
	// TimeoutMS 是场景总预算，单位为毫秒。
	TimeoutMS int `json:"timeoutMs"`
	// ExpectedOutcome 是场景成功时唯一允许的稳定结果。
	ExpectedOutcome string `json:"expectedOutcome"`
	// Evidence 是证明该场景的最低证据层级。
	Evidence string `json:"evidence"`
	// Execution 表示本场景由当前 wire runner 执行，还是引用本轮已完成的分层 gate。
	Execution string `json:"execution"`
	// Profile 为 resource/shutdown 场景声明机器无关的尝试、并发、字节与时间上限。
	Profile *ScenarioProfile `json:"profile,omitempty"`
}

// ScenarioProfile 是资源资格场景的有限工作量上限，不表达吞吐或延迟排名。
type ScenarioProfile struct {
	// Attempts 是连接、frame 或 lifecycle 操作的总尝试数。
	Attempts int `json:"attempts"`
	// MaxConcurrency 是同一时刻允许的最大操作数。
	MaxConcurrency int `json:"maxConcurrency"`
	// MaxBytes 是该场景允许处理的累计字节上限。
	MaxBytes int `json:"maxBytes"`
	// TimeoutMS 是 profile 自身的墙钟上限，单位为毫秒且不得超过场景预算。
	TimeoutMS int `json:"timeoutMs"`
}

// LoadManifest 严格解码并验证一个资格场景清单。
func LoadManifest(reader io.Reader) (Manifest, error) {
	if reader == nil {
		return Manifest{}, errors.New("qualification manifest reader is nil")
	}
	decoder := json.NewDecoder(io.LimitReader(reader, 1<<20))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode qualification manifest: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return Manifest{}, err
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// Validate 检查版本、封闭枚举、稳定排序、唯一 ID 和有界预算。
func (manifest Manifest) Validate() error {
	if manifest.SchemaVersion != ManifestSchemaVersion {
		return fmt.Errorf("qualification manifest schemaVersion=%d is unsupported", manifest.SchemaVersion)
	}
	if manifest.QualificationVersion != QualificationVersion {
		return fmt.Errorf("qualificationVersion=%q is unsupported", manifest.QualificationVersion)
	}
	if len(manifest.Scenarios) == 0 {
		return errors.New("qualification manifest has no scenarios")
	}
	seen := make(map[string]struct{}, len(manifest.Scenarios))
	for index, scenario := range manifest.Scenarios {
		if err := scenario.validate(); err != nil {
			return fmt.Errorf("scenario[%d]: %w", index, err)
		}
		if _, exists := seen[scenario.ID]; exists {
			return fmt.Errorf("scenario id %q is duplicated", scenario.ID)
		}
		seen[scenario.ID] = struct{}{}
	}
	return nil
}

// IDs 返回排序后的场景 ID 副本，供 completeness 和报告校验使用。
func (manifest Manifest) IDs() []string {
	ids := make([]string, 0, len(manifest.Scenarios))
	for _, scenario := range manifest.Scenarios {
		ids = append(ids, scenario.ID)
	}
	sort.Strings(ids)
	return ids
}

// validate 检查单个场景元数据是否封闭且可调度。
func (scenario ScenarioDefinition) validate() error {
	if !validStableID(scenario.ID) {
		return fmt.Errorf("id %q is not a stable identifier", scenario.ID)
	}
	if !knownGroups[scenario.Group] {
		return fmt.Errorf("group %q is unknown", scenario.Group)
	}
	if !knownPhases[scenario.Phase] {
		return fmt.Errorf("phase %q is unknown", scenario.Phase)
	}
	if !knownOutcomes[scenario.ExpectedOutcome] {
		return fmt.Errorf("expected outcome %q is unknown", scenario.ExpectedOutcome)
	}
	if !knownEvidence[scenario.Evidence] {
		return fmt.Errorf("evidence %q is unknown", scenario.Evidence)
	}
	if !knownExecutions[scenario.Execution] {
		return fmt.Errorf("execution %q is unknown", scenario.Execution)
	}
	if !validPhaseExecution(scenario.Phase, scenario.Execution) {
		return errors.New("phase and execution pairing is invalid")
	}
	if (scenario.Evidence == "contract") != (scenario.Execution == "contract") {
		return errors.New("contract evidence and execution must appear together")
	}
	if scenario.TimeoutMS <= 0 || scenario.TimeoutMS > maximumScenarioTimeoutMS {
		return fmt.Errorf("timeoutMs=%d is outside (0,%d]", scenario.TimeoutMS, maximumScenarioTimeoutMS)
	}
	requiresProfile := scenario.Phase == "resource" || scenario.Phase == "shutdown"
	if requiresProfile != (scenario.Profile != nil) {
		return errors.New("resource/shutdown phase and profile must appear together")
	}
	if scenario.Profile != nil {
		profile := scenario.Profile
		if profile.Attempts <= 0 || profile.Attempts > 1024 || profile.MaxConcurrency <= 0 || profile.MaxConcurrency > profile.Attempts || profile.MaxBytes <= 0 || profile.MaxBytes > maximumProfileBytes || profile.TimeoutMS <= 0 || profile.TimeoutMS > scenario.TimeoutMS {
			return errors.New("resource profile is outside finite qualification bounds")
		}
	}
	return nil
}

// validPhaseExecution 限制各阶段的实际证明方式；recovery 同时允许真实依赖故障与精细分层注入。
func validPhaseExecution(phase, execution string) bool {
	switch phase {
	case "contract":
		return execution == "contract"
	case "functional":
		return execution == "black_box"
	case "recovery":
		return execution == "black_box" || execution == "layered"
	case "resource", "shutdown":
		return execution == "layered"
	default:
		return false
	}
}

// validStableID 接受小写 ASCII 单词和单连字符分隔，拒绝路径与空段。
func validStableID(value string) bool {
	if len(value) < 3 || len(value) > 96 || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	previousHyphen := false
	for _, character := range value {
		if character == '-' {
			if previousHyphen {
				return false
			}
			previousHyphen = true
			continue
		}
		if character < 'a' || character > 'z' {
			return false
		}
		previousHyphen = false
	}
	return true
}

// stringSet 构造只读使用的字符串成员集合。
func stringSet(values ...string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

// requireJSONEOF 拒绝首个 JSON value 后的任何额外内容。
func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("qualification JSON contains trailing value")
		}
		return fmt.Errorf("decode qualification JSON trailer: %w", err)
	}
	return nil
}
