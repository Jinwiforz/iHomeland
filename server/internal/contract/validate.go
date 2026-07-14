package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	_ "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/account/v1"
	_ "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/common/v1"
	_ "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/control/v1"
	_ "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/session/v1"
	_ "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/visit/v1"
	_ "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/world/v1"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"gopkg.in/yaml.v3"
)

// maximumFrameSize 使 registry 的单消息上限不可能绕过 TLS/TCP codec 的 1 MiB 硬边界。
// 两处保持同值是跨层防御：契约验证阻止错误配置，frame codec 阻止不可信网络输入。
const maximumFrameSize = 1 << 20

// registrySchemaVersion 是当前 validator 唯一理解的 registry 结构代际，未知版本不得猜测解析。
const registrySchemaVersion uint32 = 1

// forbiddenCommandIdentityField 匹配 command 中可能覆盖认证、admission 或 assignment 的字段。
// 目标实体仍可使用明确业务名称表达，例如 target_visitor_id；这里只禁止把受信上下文放回 payload。
var forbiddenCommandIdentityField = regexp.MustCompile(`(?i)^(?:(?:actor|account|player|user)_?id|session_?id|session_?epoch|world_?id|personal_?world_?id|world_?instance_?id|role|endpoint|fencing_?token|assignment_?stamp|runtime_?node_?id)$`)

// registrySymbolPattern 限制公开 symbol 使用稳定的大写下划线形式，避免大小写折叠产生歧义。
var registrySymbolPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// policyKeyPattern 限制内部策略引用使用稳定的小写下划线形式。
var policyKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// messageKeyPattern 限制公开错误文案键位于 error namespace，避免自由文本进入 registry。
var messageKeyPattern = regexp.MustCompile(`^error(?:\.[a-z][a-z0-9_]*)+$`)

// checksumPattern 验证版本目录中的 SHA-256 使用完整小写十六进制表达。
var checksumPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// accountUsernamePattern 是 OpenAPI 与 account domain 必须共同执行的唯一 username 接受集合。
const accountUsernamePattern = `^[A-Za-z0-9](?:[A-Za-z0-9._-]{1,62}[A-Za-z0-9])?$`

// safeASCIIIdentityPattern 是公网 identity 与幂等键允许的可日志化 ASCII 字符集合。
const safeASCIIIdentityPattern = `^[A-Za-z0-9._:-]+$`

// safeOpaqueCredentialPattern 限制 opaque credential 使用无需二次编码的安全 ASCII token 字符。
const safeOpaqueCredentialPattern = `^[A-Za-z0-9._~-]+$`

// Validate 联合检查 registry 唯一性、descriptor 引用、路由策略、身份边界与 OpenAPI 语义。
//
// root 指向仓库根目录，openAPISchemaPath 可为空；非空时必须是已锁定的官方 schema。
// 调用前必须由统一入口生成 Go Protobuf code，使全局 descriptor registry 与当前 schema 一致。
// 校验按依赖顺序短路，任何失败都表示治理源不适合进入 runtime，不产生部分输出。
func Validate(root string, openAPISchemaPath string) error {
	catalog, err := Load(root)
	if err != nil {
		return err
	}
	files := &protoregistryFiles{files: protoregistry.GlobalFiles}
	if err := validateMessages(catalog, files); err != nil {
		return err
	}
	if err := validateErrors(catalog.Errors); err != nil {
		return err
	}
	if err := validateOpenAPI(root, openAPISchemaPath); err != nil {
		return err
	}
	return ValidateVersions(root)
}

// validateOwnerRanges 拒绝无效、重复或重叠的消息编号所有权区间。
// 区间列表是分配权限本身；即使当前没有消息落入冲突部分，也不能允许两个 owner 同时占有编号。
func validateOwnerRanges(ranges []OwnerRange) error {
	if len(ranges) == 0 {
		return errors.New("message registry requires owner ranges")
	}
	owners := make(map[string]struct{}, len(ranges))
	for index, current := range ranges {
		if current.Owner == "" || current.Start == 0 || current.End < current.Start {
			return fmt.Errorf("invalid owner range at index %d", index)
		}
		if _, exists := owners[current.Owner]; exists {
			return fmt.Errorf("duplicate owner range for %s", current.Owner)
		}
		owners[current.Owner] = struct{}{}
		for previousIndex := 0; previousIndex < index; previousIndex++ {
			previous := ranges[previousIndex]
			if current.Start <= previous.End && previous.Start <= current.End {
				return fmt.Errorf("owner ranges %s and %s overlap", previous.Owner, current.Owner)
			}
		}
	}
	return nil
}

// worldVisitErrorProfiles 固定首批 world/visit public error 的完整恢复语义。
var worldVisitErrorProfiles = map[uint32]ErrorEntry{
	2000: {Code: 2000, Name: "WORLD_NOT_FOUND", Owner: "world", Category: "NOT_FOUND", MessageKey: "error.world.not_found", Retryable: false, HTTPStatus: 404},
	2001: {Code: 2001, Name: "WORLD_NOT_READY", Owner: "world", Category: "CONFLICT", MessageKey: "error.world.not_ready", Retryable: true, HTTPStatus: 409},
	2002: {Code: 2002, Name: "WORLD_ASSIGNMENT_STALE", Owner: "world", Category: "CONFLICT", MessageKey: "error.world.assignment_stale", Retryable: false, HTTPStatus: 409},
	2003: {Code: 2003, Name: "WORLD_ADMISSION_INVALID", Owner: "world", Category: "AUTH", MessageKey: "error.world.admission_invalid", Retryable: false, HTTPStatus: 401},
	2004: {Code: 2004, Name: "WORLD_ADMISSION_EXPIRED", Owner: "world", Category: "AUTH", MessageKey: "error.world.admission_expired", Retryable: false, HTTPStatus: 401},
	2005: {Code: 2005, Name: "WORLD_ADMISSION_REPLAYED", Owner: "world", Category: "CONFLICT", MessageKey: "error.world.admission_replayed", Retryable: false, HTTPStatus: 409},
	2006: {Code: 2006, Name: "WORLD_IDEMPOTENCY_CONFLICT", Owner: "world", Category: "CONFLICT", MessageKey: "error.world.idempotency_conflict", Retryable: false, HTTPStatus: 409},
	2100: {Code: 2100, Name: "VISIT_NOT_FOUND", Owner: "visit", Category: "NOT_FOUND", MessageKey: "error.visit.not_found", Retryable: false, HTTPStatus: 404},
	2101: {Code: 2101, Name: "VISIT_INVITE_NOT_FOUND", Owner: "visit", Category: "NOT_FOUND", MessageKey: "error.visit.invite_not_found", Retryable: false, HTTPStatus: 404},
	2102: {Code: 2102, Name: "VISIT_INVITE_EXPIRED", Owner: "visit", Category: "CONFLICT", MessageKey: "error.visit.invite_expired", Retryable: false, HTTPStatus: 410},
	2103: {Code: 2103, Name: "VISIT_CAPACITY_EXCEEDED", Owner: "visit", Category: "CONFLICT", MessageKey: "error.visit.capacity_exceeded", Retryable: false, HTTPStatus: 409},
	2104: {Code: 2104, Name: "VISIT_STATE_CONFLICT", Owner: "visit", Category: "CONFLICT", MessageKey: "error.visit.state_conflict", Retryable: false, HTTPStatus: 409},
	2105: {Code: 2105, Name: "VISIT_REVISION_CONFLICT", Owner: "visit", Category: "CONFLICT", MessageKey: "error.visit.revision_conflict", Retryable: false, HTTPStatus: 409},
	2106: {Code: 2106, Name: "VISIT_IDEMPOTENCY_CONFLICT", Owner: "visit", Category: "CONFLICT", MessageKey: "error.visit.idempotency_conflict", Retryable: false, HTTPStatus: 409},
	2107: {Code: 2107, Name: "VISIT_MEMBERSHIP_REQUIRED", Owner: "visit", Category: "AUTH", MessageKey: "error.visit.membership_required", Retryable: false, HTTPStatus: 403},
	2108: {Code: 2108, Name: "VISIT_OWNER_UNAVAILABLE", Owner: "visit", Category: "CONFLICT", MessageKey: "error.visit.owner_unavailable", Retryable: false, HTTPStatus: 409},
	2109: {Code: 2109, Name: "VISIT_RECONNECT_EXPIRED", Owner: "visit", Category: "CONFLICT", MessageKey: "error.visit.reconnect_expired", Retryable: false, HTTPStatus: 410},
}

// validateWorldVisitErrors 拒绝首批稳定错误被改名、改 owner 或改变客户端恢复语义。
func validateWorldVisitErrors(entries []ErrorEntry) error {
	seen := make(map[uint32]struct{}, len(worldVisitErrorProfiles))
	for _, entry := range entries {
		if entry.Owner != "world" && entry.Owner != "visit" {
			continue
		}
		expected, ok := worldVisitErrorProfiles[entry.Code]
		if !ok || entry != expected {
			return fmt.Errorf("world/visit error %d does not match its reviewed profile", entry.Code)
		}
		seen[entry.Code] = struct{}{}
	}
	if len(seen) != len(worldVisitErrorProfiles) {
		return errors.New("world/visit error registry is incomplete")
	}
	return nil
}

// reservedSet 将退役编号转换为查找集合，并拒绝零值或重复声明。
// 重复 reserved 项通常表示合并冲突或生成错误，不能因集合去重而静默接受。
func reservedSet(values []uint32, kind string) (map[uint32]struct{}, error) {
	reserved := make(map[uint32]struct{}, len(values))
	for _, value := range values {
		if value == 0 {
			return nil, fmt.Errorf("%s reserved value must be nonzero", kind)
		}
		if _, exists := reserved[value]; exists {
			return nil, fmt.Errorf("duplicate %s reserved value %d", kind, value)
		}
		reserved[value] = struct{}{}
	}
	return reserved, nil
}

// BuildProjection 按确定顺序合并消息与路由所有权，保证 runtime lookup 可重复生成。
//
// 输入 Catalog 被视为不可变快照，返回值拥有独立的 Routes slice。消息缺少路由或拥有多个
// 路由时立即失败，因为 projection 不能通过任选一条记录掩盖治理冲突。
func BuildProjection(catalog Catalog) (Projection, error) {
	if catalog.Messages.SchemaVersion != registrySchemaVersion || catalog.Routes.SchemaVersion != registrySchemaVersion {
		return Projection{}, errors.New("projection requires supported message and route registry versions")
	}
	// routesByID 同时建立 O(1) 合并索引并在覆盖发生前发现重复路由。
	routesByID := make(map[uint32]RouteEntry, len(catalog.Routes.Routes))
	for _, route := range catalog.Routes.Routes {
		if _, exists := routesByID[route.MessageID]; exists {
			return Projection{}, fmt.Errorf("message %d has more than one route", route.MessageID)
		}
		routesByID[route.MessageID] = route
	}
	projection := Projection{SchemaVersion: registrySchemaVersion, Routes: make([]ProjectedRoute, 0, len(catalog.Messages.Messages))}
	for _, message := range catalog.Messages.Messages {
		route, exists := routesByID[message.ID]
		if !exists {
			return Projection{}, fmt.Errorf("message %d has no route", message.ID)
		}
		projection.Routes = append(projection.Routes, ProjectedRoute{MessageEntry: message, RouteEntry: route})
		delete(routesByID, message.ID)
	}
	if len(routesByID) != 0 {
		return Projection{}, errors.New("route registry contains messages absent from message registry")
	}
	sort.Slice(projection.Routes, func(left, right int) bool { return projection.Routes[left].ID < projection.Routes[right].ID })
	return projection, nil
}

// validateMessages 验证编号所有权、唯一路由、schema 存在性与 command 身份字段安全边界。
//
// files 必须来自当前生成的 Go Protobuf code，确保 registry 不能引用未进入本次构建的 schema。
// 函数只读取 Catalog；返回首个违规项，便于维护者从上游源文件修复而不是修改生成物。
func validateMessages(catalog Catalog, files *protoregistryFiles) error {
	if catalog.Messages.SchemaVersion != registrySchemaVersion {
		return fmt.Errorf("unsupported messages registry schema version %d", catalog.Messages.SchemaVersion)
	}
	if catalog.Routes.SchemaVersion != registrySchemaVersion {
		return fmt.Errorf("unsupported routes registry schema version %d", catalog.Routes.SchemaVersion)
	}
	if err := validateOwnerRanges(catalog.Messages.OwnerRanges); err != nil {
		return err
	}
	if len(catalog.Messages.Messages) == 0 || len(catalog.Routes.Routes) == 0 {
		return errors.New("message and route registries must not be empty")
	}
	// 独立索引分别保护数值 wire identity 与人类可读符号，二者都必须唯一。
	ids := make(map[uint32]struct{}, len(catalog.Messages.Messages))
	names := make(map[string]struct{}, len(catalog.Messages.Messages))
	reserved, err := reservedSet(catalog.Messages.Reserved, "message")
	if err != nil {
		return err
	}
	// routes 在检查消息前先拒绝重复项和越过 frame codec 的配置，避免后续任选其一。
	routes := make(map[uint32]RouteEntry, len(catalog.Routes.Routes))
	for _, route := range catalog.Routes.Routes {
		if route.MessageID == 0 || route.AuthScope == "" || route.QoS == "" || !policyKeyPattern.MatchString(route.RatePolicy) || route.Idempotency == "" {
			return fmt.Errorf("route %d has missing required policy", route.MessageID)
		}
		if _, exists := routes[route.MessageID]; exists {
			return fmt.Errorf("duplicate route for message %d", route.MessageID)
		}
		if route.MaxSize == 0 || route.MaxSize > maximumFrameSize {
			return fmt.Errorf("route %d has invalid maxSize %d", route.MessageID, route.MaxSize)
		}
		routes[route.MessageID] = route
	}
	for _, message := range catalog.Messages.Messages {
		if message.ID == 0 || message.Owner == "" || message.Protobuf == "" || !registrySymbolPattern.MatchString(message.Name) {
			return fmt.Errorf("message %d has invalid required identity fields", message.ID)
		}
		if _, exists := ids[message.ID]; exists {
			return fmt.Errorf("duplicate message id %d", message.ID)
		}
		if _, exists := names[message.Name]; exists {
			return fmt.Errorf("duplicate message name %s", message.Name)
		}
		if _, exists := reserved[message.ID]; exists {
			return fmt.Errorf("message %d reuses a reserved id", message.ID)
		}
		ids[message.ID] = struct{}{}
		names[message.Name] = struct{}{}
		if !ownedBy(message, catalog.Messages.OwnerRanges) {
			return fmt.Errorf("message %d is outside owner %s range", message.ID, message.Owner)
		}
		descriptor, err := files.FindMessage(message.Protobuf)
		if err != nil {
			return fmt.Errorf("message %d references %s: %w", message.ID, message.Protobuf, err)
		}
		if message.Kind == "COMMAND" {
			// command 的操作者来自认证连接；递归扫描可防止通过嵌套 message 绕过身份字段门禁。
			if fieldPath, found := findForbiddenActorField(descriptor, nil); found {
				return fmt.Errorf("command %s declares forbidden actor field %s", message.Protobuf, fieldPath)
			}
		}
		route, exists := routes[message.ID]
		if !exists {
			return fmt.Errorf("message %d has no route", message.ID)
		}
		if err := validateRouteSemantics(message, route); err != nil {
			return err
		}
	}
	for id := range routes {
		if _, exists := ids[id]; !exists {
			return fmt.Errorf("route references unknown message %d", id)
		}
	}
	return nil
}

// findForbiddenActorField 深度优先查找可能把操作者身份带回 command payload 的字段路径。
// visited 按 message full name 阻止递归 schema 形成无限循环；带 target_ 等业务限定前缀的字段不匹配。
func findForbiddenActorField(descriptor protoreflect.MessageDescriptor, visited map[protoreflect.FullName]bool) (string, bool) {
	if visited == nil {
		visited = make(map[protoreflect.FullName]bool)
	}
	if visited[descriptor.FullName()] {
		return "", false
	}
	visited[descriptor.FullName()] = true
	defer delete(visited, descriptor.FullName())
	for index := 0; index < descriptor.Fields().Len(); index++ {
		field := descriptor.Fields().Get(index)
		fieldName := string(field.Name())
		if forbiddenCommandIdentityField.MatchString(fieldName) {
			return fieldName, true
		}
		if field.IsMap() {
			if field.MapValue().Message() != nil {
				if nestedPath, found := findForbiddenActorField(field.MapValue().Message(), visited); found {
					return fieldName + "." + nestedPath, true
				}
			}
			continue
		}
		if field.Message() == nil {
			continue
		}
		if nestedPath, found := findForbiddenActorField(field.Message(), visited); found {
			return fieldName + "." + nestedPath, true
		}
	}
	return "", false
}

// validateRouteSemantics 拒绝无法安全执行的 kind、channel 与 correlation 组合。
//
// message 与 route 必须属于同一 ID。这里集中维护跨字段不变量，避免各 listener 对同一消息
// 做出不同解释；失败后不允许 adapter 自动改用另一个 channel 或 correlation 策略。
func validateRouteSemantics(message MessageEntry, route RouteEntry) error {
	// 使用显式集合而不是接受任意字符串，确保新增 kind 必须先定义完整 correlation 语义。
	if !validMessageKind(message.Kind) {
		return fmt.Errorf("message %d has invalid kind %s", message.ID, message.Kind)
	}
	if !validDirection(message.Direction) {
		return fmt.Errorf("message %d has invalid direction %s", message.ID, message.Direction)
	}
	if route.Channel != "WSS" && route.Channel != "TLS_TCP" {
		return fmt.Errorf("message %d has invalid channel %s", message.ID, route.Channel)
	}
	if message.Owner == "control" && route.Channel != "WSS" {
		return fmt.Errorf("control message %d must use WSS", message.ID)
	}
	if route.QoS != "RELIABLE_ORDERED" {
		return fmt.Errorf("message %d has unsupported QoS %s", message.ID, route.QoS)
	}
	if !validAuthScope(route.AuthScope) {
		return fmt.Errorf("message %d has invalid auth scope %s", message.ID, route.AuthScope)
	}
	if (message.Kind == "REQUEST" || message.Kind == "COMMAND") && message.Direction != "CLIENT_TO_SERVER" {
		return fmt.Errorf("message %d kind %s must be client to server", message.ID, message.Kind)
	}
	if (message.Kind == "RESPONSE" || message.Kind == "PUSH" || message.Kind == "ERROR") && message.Direction != "SERVER_TO_CLIENT" {
		return fmt.Errorf("message %d kind %s must be server to client", message.ID, message.Kind)
	}
	if message.Kind == "REQUEST" && route.Idempotency != "REQUEST_ID" {
		return fmt.Errorf("request message %d must use REQUEST_ID", message.ID)
	}
	if message.Kind == "RESPONSE" && route.Idempotency != "CORRELATION_ID" {
		return fmt.Errorf("response message %d must use CORRELATION_ID", message.ID)
	}
	if message.Kind == "COMMAND" && route.Idempotency != "COMMAND_ID" {
		return fmt.Errorf("command message %d must use COMMAND_ID", message.ID)
	}
	if message.Kind == "PUSH" && route.Idempotency != "NONE" {
		return fmt.Errorf("push message %d cannot require correlation", message.ID)
	}
	if message.Kind == "ERROR" && route.Idempotency != "CORRELATION_ID" {
		return fmt.Errorf("error message %d must echo one correlation id", message.ID)
	}
	if message.Kind == "PUSH" && route.TimeoutMS != 0 {
		return fmt.Errorf("push message %d cannot declare a response timeout", message.ID)
	}
	if message.Kind != "PUSH" && route.TimeoutMS == 0 {
		return fmt.Errorf("message %d requires a positive timeout", message.ID)
	}
	return validateWorldVisitRoute(message, route)
}

// worldVisitRouteProfile 保存已评审 world/visit 消息不可漂移的完整路由策略。
type worldVisitRouteProfile struct {
	// channel 是消息唯一允许的可靠传输通道。
	channel string
	// authScope 是 dispatch 前必须持有的连接授权范围。
	authScope string
	// maxSize 是完整 envelope 的最大字节数。
	maxSize uint32
	// ratePolicy 是服务端拥有的限流策略名称。
	ratePolicy string
	// idempotency 是 envelope correlation 规则。
	idempotency string
	// timeoutMS 是服务端处理预算，push 固定为零。
	timeoutMS uint32
}

// validateWorldVisitRoute 固定 P0 world/visit 的控制面、权威业务面与执行预算。
// 新消息没有显式 profile 时默认拒绝，确保 size、rate、timeout 与唯一通道必须先经评审。
func validateWorldVisitRoute(message MessageEntry, route RouteEntry) error {
	if message.Owner != "world" && message.Owner != "visit" {
		return nil
	}
	profile, ok := worldVisitProfile(message.ID)
	if !ok {
		return fmt.Errorf("world/visit message %d has no reviewed route profile", message.ID)
	}
	if route.Channel != profile.channel || route.AuthScope != profile.authScope || route.MaxSize != profile.maxSize || route.RatePolicy != profile.ratePolicy || route.Idempotency != profile.idempotency || route.TimeoutMS != profile.timeoutMS {
		return fmt.Errorf("world/visit message %d does not match its reviewed route profile", message.ID)
	}
	return nil
}

// worldVisitProfile 返回已登记消息的固定路由合同；未登记编号不做范围推断。
func worldVisitProfile(messageID uint32) (worldVisitRouteProfile, bool) {
	switch messageID {
	case 2000, 2119:
		return worldVisitRouteProfile{"TLS_TCP", "GAMEPLAY", 4096, "world_read", "REQUEST_ID", 10000}, true
	case 2001, 2120:
		return worldVisitRouteProfile{"TLS_TCP", "GAMEPLAY", 65536, "world_read", "CORRELATION_ID", 10000}, true
	case 2002, 2121:
		return worldVisitRouteProfile{"TLS_TCP", "GAMEPLAY", 65536, "server_world", "NONE", 0}, true
	case 2003, 2100, 2101, 2102:
		return worldVisitRouteProfile{"WSS", "CONTROL", 16384, "server_control", "NONE", 0}, true
	case 2103, 2105, 2107, 2109, 2111, 2113, 2115, 2117:
		return worldVisitRouteProfile{"TLS_TCP", "GAMEPLAY", 4096, "visit_command", "COMMAND_ID", 5000}, true
	case 2104, 2106, 2108, 2110, 2112, 2114, 2116, 2118:
		return worldVisitRouteProfile{"TLS_TCP", "GAMEPLAY", 16384, "visit_command", "CORRELATION_ID", 5000}, true
	case 2122:
		return worldVisitRouteProfile{"TLS_TCP", "GAMEPLAY", 16384, "server_world", "NONE", 0}, true
	default:
		return worldVisitRouteProfile{}, false
	}
}

// validMessageKind 限定 registry 可表达的可靠 envelope 语义。
func validMessageKind(kind string) bool {
	switch kind {
	case "REQUEST", "RESPONSE", "COMMAND", "PUSH", "ERROR":
		return true
	default:
		return false
	}
}

// validDirection 限定第一阶段实时连接支持的单向消息方向。
func validDirection(direction string) bool {
	return direction == "CLIENT_TO_SERVER" || direction == "SERVER_TO_CLIENT"
}

// validAuthScope 防止拼写错误的 scope 进入 dispatcher projection 后永久拒绝合法连接。
func validAuthScope(scope string) bool {
	switch scope {
	case "CONTROL", "GAMEPLAY":
		return true
	default:
		return false
	}
}

// validateErrors 防止公开 code、name 与 message key 出现歧义。
//
// registry 被视为完整快照；任一必填字段、唯一性、reserved 或 HTTP 映射违规都会使整体失败，
// 防止客户端针对同一错误码执行不一致恢复逻辑。
func validateErrors(registry ErrorRegistry) error {
	if registry.SchemaVersion != registrySchemaVersion {
		return fmt.Errorf("unsupported errors registry schema version %d", registry.SchemaVersion)
	}
	if len(registry.Errors) == 0 {
		return errors.New("error registry must not be empty")
	}
	codes := make(map[uint32]struct{}, len(registry.Errors))
	names := make(map[string]struct{}, len(registry.Errors))
	keys := make(map[string]struct{}, len(registry.Errors))
	reserved, err := reservedSet(registry.Reserved, "error")
	if err != nil {
		return err
	}
	for _, entry := range registry.Errors {
		if entry.Code == 0 || !registrySymbolPattern.MatchString(entry.Name) || !validErrorOwner(entry.Owner) || !validErrorCategory(entry.Category) || !messageKeyPattern.MatchString(entry.MessageKey) {
			return errors.New("error entries require code, symbolic name, owner, category, and messageKey")
		}
		if _, exists := codes[entry.Code]; exists {
			return fmt.Errorf("duplicate error code %d", entry.Code)
		}
		if _, exists := names[entry.Name]; exists {
			return fmt.Errorf("duplicate error name %s", entry.Name)
		}
		if _, exists := keys[entry.MessageKey]; exists {
			return fmt.Errorf("duplicate error messageKey %s", entry.MessageKey)
		}
		if _, exists := reserved[entry.Code]; exists {
			return fmt.Errorf("error %d reuses a reserved code", entry.Code)
		}
		if entry.HTTPStatus < 400 || entry.HTTPStatus > 599 {
			return fmt.Errorf("error %d has invalid HTTP status %d", entry.Code, entry.HTTPStatus)
		}
		codes[entry.Code] = struct{}{}
		names[entry.Name] = struct{}{}
		keys[entry.MessageKey] = struct{}{}
	}
	return validateWorldVisitErrors(registry.Errors)
}

// validErrorOwner 限定基础契约中可以发布稳定错误语义的 capability。
func validErrorOwner(owner string) bool {
	switch owner {
	case "common", "session", "account", "world", "visit":
		return true
	default:
		return false
	}
}

// validErrorCategory 限定客户端恢复逻辑允许分支的稳定错误类别。
func validErrorCategory(category string) bool {
	switch category {
	case "PROTOCOL", "AUTH", "VALIDATION", "NOT_FOUND", "CONFLICT", "RATE_LIMIT", "DEPENDENCY", "INTERNAL":
		return true
	default:
		return false
	}
}

// validateOpenAPI 联合官方 schema 与项目 operation/error 规则，避免只通过结构校验却违反业务契约。
//
// schemaPath 为空只用于不具备外部 schema 的单元测试；正式 verify 必须传入锁定 schema。
// 函数不修改文档，所有诊断都指向 openapi.yaml 中需要修复的源定义。
func validateOpenAPI(root string, schemaPath string) error {
	path := filepath.Join(root, "shared", "contracts", "http", "v1", "openapi.yaml")
	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read OpenAPI: %w", err)
	}
	// 使用无类型文档保留 OpenAPI 的完整结构，使官方 JSON Schema 而非手写结构体决定合法字段。
	var document any
	if err := yaml.Unmarshal(contents, &document); err != nil {
		return fmt.Errorf("decode OpenAPI: %w", err)
	}
	if schemaPath != "" {
		compiler := jsonschema.NewCompiler()
		schema, err := compiler.Compile(schemaPath)
		if err != nil {
			return fmt.Errorf("compile OpenAPI schema: %w", err)
		}
		if err := schema.Validate(document); err != nil {
			return fmt.Errorf("validate OpenAPI schema: %w", err)
		}
	}
	rootMap, ok := document.(map[string]any)
	if !ok {
		return errors.New("OpenAPI root must be an object")
	}
	paths, ok := rootMap["paths"].(map[string]any)
	if !ok || len(paths) == 0 {
		return errors.New("OpenAPI paths must not be empty")
	}
	// operationId 是后续客户端生成和 handler 映射的稳定键，因此必须跨全部 path 唯一。
	operationIDs := make(map[string]struct{})
	// requiredOperations 固定 S0 承诺的最小 HTTPS 面，新增 operation 允许存在但不能替代这些入口。
	requiredOperations := map[string]string{
		"GET /v1/version":          "getVersion",
		"GET /v1/config":           "getBootstrapConfig",
		"POST /v1/auth/register":   "registerAccount",
		"POST /v1/auth/login":      "loginAccount",
		"POST /v1/auth/refresh":    "refreshSession",
		"POST /v1/auth/logout":     "logoutSession",
		"POST /v1/session/tickets": "issueConnectionTicket",
		"GET /v1/world/bootstrap":  "getWorldBootstrap",
		"POST /v1/visits/{visitSessionId}/invites/{inviteId}/accept": "acceptVisitInvite",
		"POST /v1/world/admissions":                                  "issueWorldAdmission",
	}
	seenOperations := make(map[string]string, len(requiredOperations))
	for route, rawPath := range paths {
		pathItem, ok := rawPath.(map[string]any)
		if !ok {
			return fmt.Errorf("OpenAPI path %s must be an object", route)
		}
		for method, rawOperation := range pathItem {
			if !isHTTPMethod(method) {
				continue
			}
			operation, ok := rawOperation.(map[string]any)
			if !ok {
				return fmt.Errorf("OpenAPI operation %s %s must be an object", method, route)
			}
			operationID, ok := operation["operationId"].(string)
			if !ok || operationID == "" {
				return fmt.Errorf("OpenAPI operation %s %s needs operationId", method, route)
			}
			if _, exists := operationIDs[operationID]; exists {
				return fmt.Errorf("duplicate OpenAPI operationId %s", operationID)
			}
			operationIDs[operationID] = struct{}{}
			if _, ok := operation["responses"].(map[string]any); !ok {
				return fmt.Errorf("OpenAPI operation %s needs responses", operationID)
			}
			if err := validateOperationErrorResponse(operationID, operation); err != nil {
				return err
			}
			if err := validateOperationPolicy(method, operationID, operation); err != nil {
				return err
			}
			seenOperations[strings.ToUpper(method)+" "+route] = operationID
		}
	}
	for route, expectedOperationID := range requiredOperations {
		if seenOperations[route] != expectedOperationID {
			return fmt.Errorf("OpenAPI requires %s with operationId %s", route, expectedOperationID)
		}
	}
	if err := validateAccountHTTPSchemas(rootMap); err != nil {
		return err
	}
	if err := validateWorldHTTPSchemas(rootMap, paths); err != nil {
		return err
	}
	return validateHTTPFixtureRoutes(root, paths)
}

// validateOperationErrorResponse 强制所有公开 operation 使用同一安全错误 envelope。
// 具体 stable code 来自 errors.json；OpenAPI 不得为单条 route 另建自由文本错误结构。
func validateOperationErrorResponse(operationID string, operation map[string]any) error {
	responses, ok := operation["responses"].(map[string]any)
	if !ok {
		return fmt.Errorf("OpenAPI operation %s needs responses", operationID)
	}
	defaultResponse, ok := responses["default"].(map[string]any)
	if !ok || defaultResponse["$ref"] != "#/components/responses/ErrorResponse" {
		return fmt.Errorf("OpenAPI operation %s must use the shared default error response", operationID)
	}
	return nil
}

// validateAccountHTTPSchemas 防止账号字段约束仅停留在描述文本或偏离 domain 接受集合。
func validateAccountHTTPSchemas(root map[string]any) error {
	components, ok := root["components"].(map[string]any)
	if !ok {
		return errors.New("OpenAPI components must be an object")
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		return errors.New("OpenAPI component schemas must be an object")
	}
	for _, schemaName := range []string{"RegisterRequest", "LoginRequest"} {
		schema, ok := schemas[schemaName].(map[string]any)
		if !ok {
			return fmt.Errorf("OpenAPI requires schema %s", schemaName)
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			return fmt.Errorf("OpenAPI schema %s requires properties", schemaName)
		}
		username, ok := properties["username"].(map[string]any)
		if !ok || username["pattern"] != accountUsernamePattern || username["minLength"] != 3 || username["maxLength"] != 64 {
			return fmt.Errorf("OpenAPI schema %s username must match account canonical boundary", schemaName)
		}
		if description, ok := username["description"].(string); !ok || strings.TrimSpace(description) == "" {
			return fmt.Errorf("OpenAPI schema %s username requires canonicalization description", schemaName)
		}
	}
	register := schemas["RegisterRequest"].(map[string]any)
	properties := register["properties"].(map[string]any)
	displayName, ok := properties["displayName"].(map[string]any)
	if !ok {
		return errors.New("OpenAPI RegisterRequest requires displayName")
	}
	if description, ok := displayName["description"].(string); !ok || description == "" {
		return errors.New("OpenAPI displayName requires normalization description")
	}
	return nil
}

// validateWorldHTTPSchemas 固定 world/visit HTTP payload 的最小公开面与身份边界。
// 该校验有意只接受已设计字段，防止 actor、assignment fence 或 credential claims 通过
// additional schema 悄然进入公网 ABI。
func validateWorldHTTPSchemas(root map[string]any, paths map[string]any) error {
	components, ok := root["components"].(map[string]any)
	if !ok {
		return errors.New("OpenAPI components must be an object")
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		return errors.New("OpenAPI component schemas must be an object")
	}
	parameters, ok := components["parameters"].(map[string]any)
	if !ok {
		return errors.New("OpenAPI component parameters must be an object")
	}
	idempotency, ok := parameters["IdempotencyKey"].(map[string]any)
	if !ok || idempotency["name"] != "Idempotency-Key" || idempotency["in"] != "header" || idempotency["required"] != true {
		return errors.New("OpenAPI requires the Idempotency-Key header parameter")
	}
	idempotencySchema, ok := idempotency["schema"].(map[string]any)
	if !ok || idempotencySchema["type"] != "string" || idempotencySchema["minLength"] != 16 || idempotencySchema["maxLength"] != 128 || idempotencySchema["pattern"] != safeASCIIIdentityPattern {
		return errors.New("OpenAPI Idempotency-Key must be a bounded string")
	}
	if err := requireExactSchema(schemas, "AcceptVisitInviteRequest", []string{"expectedRevision"}, []string{"expectedRevision"}); err != nil {
		return err
	}
	if err := requireExactSchema(schemas, "OwnWorldAdmissionTarget", []string{"kind"}, []string{"kind"}); err != nil {
		return err
	}
	if err := requireExactSchema(schemas, "VisitWorldAdmissionTarget", []string{"kind", "visitSessionId"}, []string{"kind", "visitSessionId"}); err != nil {
		return err
	}
	if err := requireExactSchema(schemas, "WorldAssignment", []string{"personalWorldId", "worldInstanceId", "endpoint", "generation", "leaseExpiresAtMs"}, []string{"personalWorldId", "worldInstanceId", "endpoint", "generation", "leaseExpiresAtMs"}); err != nil {
		return err
	}
	if err := requireExactSchema(schemas, "WorldAdmissionResponse", []string{"credential", "endpoint", "role", "purpose", "expiresAtMs"}, []string{"credential", "endpoint", "role", "purpose", "expiresAtMs"}); err != nil {
		return err
	}
	if err := requireExactSchema(schemas, "GameplayEndpoint", []string{"channel", "host", "port"}, []string{"channel", "host", "port"}); err != nil {
		return err
	}
	assignment := schemas["WorldAssignment"].(map[string]any)
	assignmentProperties := assignment["properties"].(map[string]any)
	response := schemas["WorldAdmissionResponse"].(map[string]any)
	responseProperties := response["properties"].(map[string]any)
	if schemaRef(assignmentProperties["endpoint"]) != "#/components/schemas/GameplayEndpoint" || schemaRef(responseProperties["endpoint"]) != "#/components/schemas/GameplayEndpoint" {
		return errors.New("OpenAPI world endpoints must use GameplayEndpoint")
	}
	credential, ok := responseProperties["credential"].(map[string]any)
	description, hasDescription := credential["description"].(string)
	if !ok || credential["type"] != "string" || credential["minLength"] != 32 || credential["maxLength"] != 4096 || credential["pattern"] != safeOpaqueCredentialPattern || !hasDescription || strings.TrimSpace(description) == "" {
		return errors.New("OpenAPI admission credential must remain bounded and opaque")
	}
	ownTarget := schemas["OwnWorldAdmissionTarget"].(map[string]any)["properties"].(map[string]any)
	visitTarget := schemas["VisitWorldAdmissionTarget"].(map[string]any)["properties"].(map[string]any)
	if !exactStringEnum(ownTarget["kind"], []string{"OWN_WORLD"}) || !exactStringEnum(visitTarget["kind"], []string{"VISIT_WORLD"}) {
		return errors.New("OpenAPI admission target kinds must remain closed")
	}
	if !exactStringEnum(responseProperties["role"], []string{"OWNER", "VISITOR"}) || !exactStringEnum(responseProperties["purpose"], []string{"OWN_WORLD", "JOIN", "RECONNECT"}) {
		return errors.New("OpenAPI admission role and purpose must remain closed")
	}
	gameplayEndpoint := schemas["GameplayEndpoint"].(map[string]any)
	gameplayProperties := gameplayEndpoint["properties"].(map[string]any)
	if !exactStringEnum(gameplayProperties["channel"], []string{"TLS_TCP"}) {
		return errors.New("OpenAPI GameplayEndpoint must use TLS_TCP")
	}
	request, ok := schemas["WorldAdmissionRequest"].(map[string]any)
	if !ok {
		return errors.New("OpenAPI requires WorldAdmissionRequest")
	}
	oneOf, ok := request["oneOf"].([]any)
	if !ok || len(oneOf) != 2 || schemaRef(oneOf[0]) != "#/components/schemas/OwnWorldAdmissionTarget" || schemaRef(oneOf[1]) != "#/components/schemas/VisitWorldAdmissionTarget" {
		return errors.New("OpenAPI WorldAdmissionRequest must use the fixed target one-of")
	}
	for _, route := range []string{"/v1/visits/{visitSessionId}/invites/{inviteId}/accept", "/v1/world/admissions"} {
		pathItem, ok := paths[route].(map[string]any)
		if !ok {
			return fmt.Errorf("OpenAPI requires path %s", route)
		}
		operation, ok := pathItem["post"].(map[string]any)
		if !ok || !hasParameterRef(operation, "#/components/parameters/IdempotencyKey") {
			return fmt.Errorf("OpenAPI operation POST %s requires Idempotency-Key", route)
		}
	}
	return nil
}

// requireExactSchema 拒绝公开 schema 增加未评审字段或放松必填集合。
func requireExactSchema(schemas map[string]any, name string, propertiesExpected, requiredExpected []string) error {
	schema, ok := schemas[name].(map[string]any)
	if !ok || schema["additionalProperties"] != false {
		return fmt.Errorf("OpenAPI schema %s must be closed", name)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok || len(properties) != len(propertiesExpected) {
		return fmt.Errorf("OpenAPI schema %s has an unexpected public field set", name)
	}
	for _, property := range propertiesExpected {
		if _, exists := properties[property]; !exists {
			return fmt.Errorf("OpenAPI schema %s requires property %s", name, property)
		}
	}
	required, ok := schema["required"].([]any)
	if !ok || len(required) != len(requiredExpected) {
		return fmt.Errorf("OpenAPI schema %s has an unexpected required field set", name)
	}
	requiredSet := make(map[string]struct{}, len(required))
	for _, value := range required {
		requiredName, ok := value.(string)
		if !ok {
			return fmt.Errorf("OpenAPI schema %s has a non-string required field", name)
		}
		requiredSet[requiredName] = struct{}{}
	}
	for _, property := range requiredExpected {
		if _, exists := requiredSet[property]; !exists {
			return fmt.Errorf("OpenAPI schema %s requires required field %s", name, property)
		}
	}
	return nil
}

// exactStringEnum 报告 schema 是否按顺序声明完整且唯一的字符串枚举。
func exactStringEnum(value any, expected []string) bool {
	schema, ok := value.(map[string]any)
	if !ok || schema["type"] != "string" {
		return false
	}
	values, ok := schema["enum"].([]any)
	if !ok || len(values) != len(expected) {
		return false
	}
	for index, item := range values {
		if item != expected[index] {
			return false
		}
	}
	return true
}

// schemaRef 读取 one-of item 的本地 schema 引用，其他形态返回空值并由调用方拒绝。
func schemaRef(value any) string {
	item, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	reference, _ := item["$ref"].(string)
	return reference
}

// hasParameterRef 报告 operation 是否显式引用指定公共 parameter。
func hasParameterRef(operation map[string]any, expected string) bool {
	parameters, ok := operation["parameters"].([]any)
	if !ok {
		return false
	}
	for _, parameter := range parameters {
		if schemaRef(parameter) == expected {
			return true
		}
	}
	return false
}

// httpFixtureRouteManifest 是 contract validator 消费的 HTTPS fixture 窄投影。
// Body 结构仍由 OpenAPI 与 fixture generator 拥有，此处只读取 route 和 status 引用。
type httpFixtureRouteManifest struct {
	// SchemaVersion 选择 cases.json 的结构代际。
	SchemaVersion uint32 `json:"schemaVersion"`
	// Cases 是必须指向已登记 OpenAPI operation 的示例集合。
	Cases []httpFixtureRouteCase `json:"cases"`
}

// httpFixtureRouteCase 保存单个示例的稳定名称、请求路由与响应状态。
type httpFixtureRouteCase struct {
	// Name 用于在失败诊断中定位版本化 fixture。
	Name string `json:"name"`
	// Request 指向 OpenAPI path 与 method。
	Request httpFixtureRouteRequest `json:"request"`
	// Response 指向 operation 声明的 status 或 default response。
	Response httpFixtureRouteResponse `json:"response"`
}

// httpFixtureRouteRequest 是验证路由引用所需的最小请求投影。
type httpFixtureRouteRequest struct {
	// Method 是大写或小写 HTTP method。
	Method string `json:"method"`
	// Path 必须与 OpenAPI paths 的键完全一致。
	Path string `json:"path"`
	// Headers 保存 fixture 显式提供的契约 header；bearer 等环境凭据不进入版本化示例。
	Headers map[string]string `json:"headers,omitempty"`
}

// httpFixtureRouteResponse 是验证 response 引用所需的最小响应投影。
type httpFixtureRouteResponse struct {
	// Status 是示例预期的标准 HTTP status code。
	Status int `json:"status"`
}

// validateHTTPFixtureRoutes 防止版本化示例引用不存在的 operation 或 response status。
func validateHTTPFixtureRoutes(root string, paths map[string]any) error {
	contents, err := os.ReadFile(filepath.Join(root, "shared", "contracts", "fixtures", "http", "cases.json"))
	if err != nil {
		return fmt.Errorf("read HTTP fixtures: %w", err)
	}
	var manifest httpFixtureRouteManifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		return fmt.Errorf("decode HTTP fixtures: %w", err)
	}
	if manifest.SchemaVersion != 1 || len(manifest.Cases) == 0 {
		return errors.New("HTTP fixtures require schema version 1 and at least one case")
	}
	names := make(map[string]struct{}, len(manifest.Cases))
	for _, fixture := range manifest.Cases {
		if fixture.Name == "" || fixture.Request.Method == "" || fixture.Request.Path == "" {
			return errors.New("HTTP fixture requires name, method, and path")
		}
		if _, exists := names[fixture.Name]; exists {
			return fmt.Errorf("duplicate HTTP fixture name %s", fixture.Name)
		}
		names[fixture.Name] = struct{}{}
		pathItem, ok := paths[fixture.Request.Path].(map[string]any)
		if !ok {
			return fmt.Errorf("HTTP fixture %s references unknown path %s", fixture.Name, fixture.Request.Path)
		}
		operation, ok := pathItem[strings.ToLower(fixture.Request.Method)].(map[string]any)
		if !ok {
			return fmt.Errorf("HTTP fixture %s references unknown method %s", fixture.Name, fixture.Request.Method)
		}
		if operation["x-ihomeland-idempotency"] == "IDEMPOTENCY_KEY_REQUIRED" && strings.TrimSpace(fixture.Request.Headers["Idempotency-Key"]) == "" {
			return fmt.Errorf("HTTP fixture %s requires Idempotency-Key", fixture.Name)
		}
		responses, ok := operation["responses"].(map[string]any)
		if !ok {
			return fmt.Errorf("HTTP fixture %s operation has no responses", fixture.Name)
		}
		if _, exists := responses[strconv.Itoa(fixture.Response.Status)]; !exists {
			if _, hasDefault := responses["default"]; !hasDefault {
				return fmt.Errorf("HTTP fixture %s references undeclared status %d", fixture.Name, fixture.Response.Status)
			}
		}
	}
	return nil
}

// validateOperationPolicy 强制每个 HTTP operation 声明 adapter 必须执行的资源与重试边界。
// 扩展字段属于项目契约而非 OpenAPI 标准字段，因此由本 validator 负责类型、范围和方法语义。
func validateOperationPolicy(method string, operationID string, operation map[string]any) error {
	bodyLimit, err := operationInteger(operation, "x-ihomeland-body-limit-bytes", 0, maximumFrameSize)
	if err != nil {
		return fmt.Errorf("OpenAPI operation %s: %w", operationID, err)
	}
	_, hasRequestBody := operation["requestBody"]
	if hasRequestBody && bodyLimit == 0 {
		return fmt.Errorf("OpenAPI operation %s requires a positive body limit", operationID)
	}
	if !hasRequestBody && bodyLimit != 0 {
		return fmt.Errorf("OpenAPI operation %s has no request body but declares body limit %d", operationID, bodyLimit)
	}
	if _, err := operationInteger(operation, "x-ihomeland-timeout-ms", 1, 60_000); err != nil {
		return fmt.Errorf("OpenAPI operation %s: %w", operationID, err)
	}
	idempotency, ok := operation["x-ihomeland-idempotency"].(string)
	if !ok || (idempotency != "SAFE" && idempotency != "IDEMPOTENT" && idempotency != "NON_IDEMPOTENT" && idempotency != "IDEMPOTENCY_KEY_REQUIRED") {
		return fmt.Errorf("OpenAPI operation %s has invalid idempotency policy", operationID)
	}
	if strings.EqualFold(method, "get") && idempotency != "SAFE" {
		return fmt.Errorf("OpenAPI GET operation %s must be SAFE", operationID)
	}
	if !strings.EqualFold(method, "get") && idempotency == "SAFE" {
		return fmt.Errorf("OpenAPI operation %s cannot declare SAFE for method %s", operationID, method)
	}
	if idempotency == "IDEMPOTENCY_KEY_REQUIRED" && !hasParameterRef(operation, "#/components/parameters/IdempotencyKey") {
		return fmt.Errorf("OpenAPI operation %s requires Idempotency-Key parameter", operationID)
	}
	return nil
}

// operationInteger 读取 YAML 解码后的整数扩展，并统一执行闭区间校验。
func operationInteger(operation map[string]any, key string, minimum int, maximum int) (int, error) {
	value, ok := operation[key].(int)
	if !ok {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	if value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be between %d and %d", key, minimum, maximum)
	}
	return value, nil
}

// versionValue 表达中央目录中参与跨文件一致性校验的最小版本节点。
// Source 等治理元数据由文档工具消费，不进入此处的执行时比较。
type versionValue struct {
	// Version 是必须原样出现在对应生态配置中的锁定版本字符串。
	Version string `yaml:"version"`
}

// windowsBinaryVersionValue 为项目局部 Windows 工具补充供应链校验摘要。
// 下载器必须同时锁定版本和官方资产 SHA-256，不能只信任可变 URL 或文件名。
type windowsBinaryVersionValue struct {
	// Version 是工具输出和下载 URL 必须共同匹配的正式版本。
	Version string `yaml:"version"`
	// WindowsAMD64SHA256 是官方 win64 发行资产的完整十六进制 SHA-256。
	WindowsAMD64SHA256 string `yaml:"windows_amd64_sha256"`
}

// schemaVersionValue 为固定外部 validation schema 补充内容摘要。
type schemaVersionValue struct {
	// Version 是被验证协议规范的正式版本。
	Version string `yaml:"version"`
	// SchemaSHA256 锁定 validator 实际消费的 schema revision 内容。
	SchemaSHA256 string `yaml:"schema_sha256"`
}

// protocolVersionCatalog 收集会直接改变 wire 或 HTTP schema 解释的协议版本。
type protocolVersionCatalog struct {
	// OpenAPI 选择 shared HTTP contract 使用的 OpenAPI 规范版本。
	OpenAPI schemaVersionValue `yaml:"openapi"`
	// Edition 选择所有 .proto 源文件必须统一使用的 Protobuf Edition。
	Edition versionValue `yaml:"protobuf_edition"`
}

// toolchainVersionCatalog 收集生成配置必须精确引用的工具链版本。
type toolchainVersionCatalog struct {
	// BufCLI 锁定统一 schema 治理与生成编排入口。
	BufCLI windowsBinaryVersionValue `yaml:"buf_cli"`
	// BufConfig 选择 buf.yaml 的配置文件 schema 版本。
	BufConfig versionValue `yaml:"buf_config"`
	// ProtobufGoGenerator 锁定 Go 生成插件，并与 go.mod runtime 保持兼容。
	ProtobufGoGenerator versionValue `yaml:"protobuf_go_generator"`
	// Protoc 锁定 Buf 调用的官方 compiler 及其内置 C# generator。
	Protoc windowsBinaryVersionValue `yaml:"protoc"`
}

// languageVersionCatalog 收集直接影响源代码解析与构建行为的语言工具链版本。
type languageVersionCatalog struct {
	// Go 是 go.mod 与项目局部 SDK 必须共同使用的精确版本。
	Go windowsBinaryVersionValue `yaml:"go"`
}

// libraryVersionCatalog 收集运行时代码直接依赖且需要中央治理的基础库版本。
type libraryVersionCatalog struct {
	// PrometheusClientGo 锁定诊断 metrics 使用的官方 Go client。
	PrometheusClientGo versionValue `yaml:"prometheus_client_go"`
	// GoText 锁定账号 Unicode normalization 使用的官方扩展库。
	GoText versionValue `yaml:"golang_x_text"`
}

// versionCatalog 是 ValidateVersions 所需的 versions.yaml 只读投影。
// 使用窄结构可以让其他版本条目独立增长，同时仍对当前执行链要求的字段保持强校验。
type versionCatalog struct {
	// Protocols 持有 wire 和文档规范版本。
	Protocols protocolVersionCatalog `yaml:"protocols"`
	// Toolchains 持有生成与配置工具版本。
	Toolchains toolchainVersionCatalog `yaml:"toolchains"`
	// Libraries 持有跨运行基础使用的直接 Go library 版本。
	Libraries libraryVersionCatalog `yaml:"libraries"`
	// Languages 持有编译项目源代码的语言版本。
	Languages languageVersionCatalog `yaml:"languages"`
}

// ValidateVersions 确保各生态配置声明不会偏离中央版本目录。
//
// 检查采用精确文本锚点而不是推断兼容范围，因为 versions.yaml 是唯一治理源。任何缺失或
// 不一致都要求先完成版本升级验证，不能在单个生成模板或 go.mod 中局部放宽。
func ValidateVersions(root string) error {
	contents, err := os.ReadFile(filepath.Join(root, "versions.yaml"))
	if err != nil {
		return fmt.Errorf("read versions.yaml: %w", err)
	}
	// 只解码 validator 实际治理的节点，避免未使用字段意外成为放宽版本门禁的旁路。
	var versions versionCatalog
	if err := yaml.Unmarshal(contents, &versions); err != nil {
		return fmt.Errorf("decode versions.yaml: %w", err)
	}
	if versions.Protocols.OpenAPI.Version == "" || versions.Protocols.Edition.Version == "" ||
		versions.Toolchains.BufCLI.Version == "" || versions.Toolchains.BufConfig.Version == "" ||
		versions.Toolchains.ProtobufGoGenerator.Version == "" || versions.Toolchains.Protoc.Version == "" ||
		versions.Libraries.PrometheusClientGo.Version == "" || versions.Libraries.GoText.Version == "" || versions.Languages.Go.Version == "" {
		return errors.New("versions.yaml is missing a required protocol, toolchain, library, or language version")
	}
	// 每项同时声明目标文件和应出现的精确锚点，使新增生态配置必须显式加入治理。
	checks := []struct {
		// path 是不得绕过中央目录自行锁定版本的生态配置文件。
		path string
		// expected 是由 versions.yaml 构造且必须原样存在的版本声明。
		expected string
	}{
		{filepath.Join(root, "buf.yaml"), "version: " + versions.Toolchains.BufConfig.Version},
		{filepath.Join(root, "tools", "proto", "buf.gen.go.yaml"), "- local: protoc-gen-go"},
		{filepath.Join(root, "tools", "proto", "buf.gen.go.yaml"), "out: server/internal/generated/proto"},
		{filepath.Join(root, "tools", "proto", "buf.gen.csharp.yaml"), "- protoc_builtin: csharp"},
		{filepath.Join(root, "tools", "proto", "buf.gen.csharp.yaml"), "protoc_path: .local/protoc/" + versions.Toolchains.Protoc.Version + "/bin/protoc.exe"},
		{filepath.Join(root, "shared", "contracts", "http", "v1", "openapi.yaml"), "openapi: " + versions.Protocols.OpenAPI.Version},
		{filepath.Join(root, "server", "go.mod"), "go " + versions.Languages.Go.Version},
		{filepath.Join(root, "server", "go.mod"), "google.golang.org/protobuf v" + versions.Toolchains.ProtobufGoGenerator.Version},
		{filepath.Join(root, "server", "go.mod"), "github.com/prometheus/client_golang v" + versions.Libraries.PrometheusClientGo.Version},
		{filepath.Join(root, "server", "go.mod"), "golang.org/x/text v" + versions.Libraries.GoText.Version},
		{filepath.Join(root, ".gitignore"), "/.local/"},
		{filepath.Join(root, ".gitignore"), "/server/internal/generated/proto/"},
		{filepath.Join(root, ".gitignore"), "/client/Assets/App/Generated/"},
	}
	for _, check := range checks {
		data, err := os.ReadFile(check.path)
		if err != nil {
			return err
		}
		if !containsExactLine(data, check.expected) {
			return fmt.Errorf("%s does not match versions.yaml value %q", check.path, check.expected)
		}
	}
	if !checksumPattern.MatchString(versions.Toolchains.Protoc.WindowsAMD64SHA256) {
		return fmt.Errorf("versions.yaml protoc windows_amd64_sha256 must be 64 lowercase hexadecimal characters")
	}
	if !checksumPattern.MatchString(versions.Toolchains.BufCLI.WindowsAMD64SHA256) {
		return fmt.Errorf("versions.yaml Buf windows_amd64_sha256 must be 64 lowercase hexadecimal characters")
	}
	if !checksumPattern.MatchString(versions.Protocols.OpenAPI.SchemaSHA256) {
		return fmt.Errorf("versions.yaml OpenAPI schema_sha256 must be 64 lowercase hexadecimal characters")
	}
	if !checksumPattern.MatchString(versions.Languages.Go.WindowsAMD64SHA256) {
		return fmt.Errorf("versions.yaml Go windows_amd64_sha256 must be 64 lowercase hexadecimal characters")
	}
	for _, forbidden := range []string{
		filepath.Join(root, "shared", "contracts", "descriptor.bin"),
		filepath.Join(root, "shared", "contracts", "registry", "projection.json"),
	} {
		if _, err := os.Stat(forbidden); err == nil {
			return fmt.Errorf("reproducible artifact must not be persisted: %s", forbidden)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect forbidden artifact %s: %w", forbidden, err)
		}
	}
	editionLine := "edition = \"" + versions.Protocols.Edition.Version + "\";"
	allowedProtoDirectories := map[string]string{
		"ihomeland/common/v1":  "ihomeland.common.v1",
		"ihomeland/account/v1": "ihomeland.account.v1",
		"ihomeland/session/v1": "ihomeland.session.v1",
		"ihomeland/control/v1": "ihomeland.control.v1",
		"ihomeland/world/v1":   "ihomeland.world.v1",
		"ihomeland/visit/v1":   "ihomeland.visit.v1",
	}
	protoRoot := filepath.Join(root, "shared", "proto")
	return filepath.WalkDir(protoRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".proto" {
			return walkErr
		}
		relativePath, err := filepath.Rel(protoRoot, path)
		if err != nil {
			return err
		}
		relativeDirectory := filepath.ToSlash(filepath.Dir(relativePath))
		expectedPackage, allowed := allowedProtoDirectories[relativeDirectory]
		if !allowed {
			return fmt.Errorf("%s is outside the S0 protocol package scope", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !containsExactLine(data, editionLine) {
			return fmt.Errorf("%s does not use catalog edition %s", path, versions.Protocols.Edition.Version)
		}
		if !containsExactLine(data, "package "+expectedPackage+";") {
			return fmt.Errorf("%s does not declare expected package %s", path, expectedPackage)
		}
		return nil
	})
}

// containsExactLine 比较去除首尾空白后的完整配置行，避免注释或较长值中的子串误通过版本门禁。
func containsExactLine(data []byte, expected string) bool {
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == expected {
			return true
		}
	}
	return false
}

// ownedBy 确认消息编号位于声明 owner 被分配的范围内。
// ranges 使用闭区间；未找到 owner 或范围不匹配都返回 false，由调用方生成带消息上下文的错误。
func ownedBy(message MessageEntry, ranges []OwnerRange) bool {
	for _, ownerRange := range ranges {
		if ownerRange.Owner == message.Owner && message.ID >= ownerRange.Start && message.ID <= ownerRange.End {
			return true
		}
	}
	return false
}

// isHTTPMethod 从 Path Item 中筛出真实 HTTP operation，忽略其他元数据字段。
// 比较不区分大小写以适配 YAML parser 输出，但只接受 OpenAPI 明确定义的方法集合。
func isHTTPMethod(value string) bool {
	switch strings.ToLower(value) {
	case "get", "put", "post", "delete", "options", "head", "patch", "trace", "query":
		return true
	default:
		return false
	}
}

// protoregistryFiles 封装生成代码注册的 descriptor 查询，避免调用方依赖全局 registry 的其他能力。
type protoregistryFiles struct {
	// files 由生成 package 的 init 注册并在进程生命周期内保持只读。
	files *protoregistry.Files
}

// FindMessage 只解析 message descriptor，并拒绝其他 Protobuf symbol 类型。
// name 必须是 registry 中声明的 fully-qualified name；返回 descriptor 只供本次静态校验读取。
func (files *protoregistryFiles) FindMessage(name string) (protoreflect.MessageDescriptor, error) {
	descriptor, err := files.files.FindDescriptorByName(protoreflect.FullName(name))
	if err != nil {
		return nil, err
	}
	message, ok := descriptor.(protoreflect.MessageDescriptor)
	if !ok {
		return nil, fmt.Errorf("%s is not a message", name)
	}
	return message, nil
}
