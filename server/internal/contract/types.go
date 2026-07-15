// Package contract 负责加载、交叉验证并投影跨端共享的机器可读契约目录。
//
// 该包只处理协议治理事实，不依赖 listener、业务 service 或存储实现。这样协议编号、
// 路由和公开错误可以在服务端启动前独立验收，也不会因某个 transport 的实现细节而改变。
package contract

import (
	"errors"
	"fmt"
)

// Catalog 聚合一次验证所需的全部 registry，避免调用方组合出版本不一致的局部视图。
//
// Catalog 是从磁盘加载后的内存快照；调用方可以读取或复制它，但不应把修改后的值当作
// 新的协议事实。长期事实仍由 shared/contracts/registry 下的源文件持有。
type Catalog struct {
	// Messages 持有消息编号、Protobuf 类型和 capability owner 的唯一绑定。
	Messages MessageRegistry
	// Errors 持有允许跨网络边界公开的稳定错误语义。
	Errors ErrorRegistry
	// Routes 持有每条实时消息唯一允许的传输和执行策略。
	Routes RouteRegistry
}

// OwnerRange 为单一 capability owner 保留闭区间消息编号，防止跨模块争用或偶然复用编号。
// Start 与 End 均包含在范围内；重叠范围必须在 registry 验证阶段被拒绝。
type OwnerRange struct {
	// Owner 是负责分配区间内编号并维护兼容性的 capability 名称。
	Owner string `json:"owner"`
	// Start 是 owner 可分配的最小消息编号，采用闭区间语义。
	Start uint32 `json:"start"`
	// End 是 owner 可分配的最大消息编号，必须不小于 Start。
	End uint32 `json:"end"`
}

// MessageEntry 将稳定编号唯一绑定到一个 Protobuf message、投递语义与传输方向。
// 该绑定一旦发布就不能通过改名或换号静默重解释，否则旧客户端会按错误类型解码 payload。
type MessageEntry struct {
	// ID 是 envelope 携带的全局稳定消息编号，不能与 reserved 编号重叠。
	ID uint32 `json:"id"`
	// Name 是供日志、评审和生成工具使用的稳定大写符号名。
	Name string `json:"name"`
	// Owner 指向负责该消息生命周期和兼容性的 capability。
	Owner string `json:"owner"`
	// Protobuf 是 descriptor 中可解析的 fully-qualified message name。
	Protobuf string `json:"protobuf"`
	// Kind 决定 correlation identifier、幂等和投递完成语义。
	Kind string `json:"kind"`
	// Direction 限定消息允许由客户端、服务端或双方中的哪一方发送。
	Direction string `json:"direction"`
}

// MessageRegistry 统一持有 owner 编号段、退役编号与当前消息绑定，避免编号被静默复用。
// SchemaVersion 只描述 registry 文件结构，不代替网络协议版本。
type MessageRegistry struct {
	// SchemaVersion 选择解析 registry JSON 的结构版本，未知版本必须拒绝。
	SchemaVersion uint32 `json:"schemaVersion"`
	// OwnerRanges 声明各 capability 唯一允许分配的消息编号闭区间。
	OwnerRanges []OwnerRange `json:"ownerRanges"`
	// Reserved 永久保留已退役或禁止使用的编号，防止旧流量被解释成新消息。
	Reserved []uint32 `json:"reserved"`
	// Messages 是当前可用消息的完整集合，不包含仅保留的历史编号。
	Messages []MessageEntry `json:"messages"`
}

// ErrorEntry 定义单个公开错误的稳定安全语义，确保内部异常不会直接暴露给客户端。
// Code、Name 与 MessageKey 都属于兼容性契约；修改它们需要显式协议迁移。
type ErrorEntry struct {
	// Code 是客户端分支处理使用的全局稳定数值错误码，零值无效。
	Code uint32 `json:"code"`
	// Name 是日志、文档和生成工具使用的稳定错误符号名。
	Name string `json:"name"`
	// Owner 是负责错误语义、兼容性和恢复规则的 capability。
	Owner string `json:"owner"`
	// Category 将错误归入协议、认证、房间等稳定处理边界。
	Category string `json:"category"`
	// MessageKey 选择客户端本地化文案，禁止携带内部异常文本。
	MessageKey string `json:"messageKey"`
	// Retryable 表示保持请求不变并稍后重试是否可能成功，而不是要求客户端立即重试。
	Retryable bool `json:"retryable"`
	// HTTPStatus 是 HTTPS adapter 对应的标准状态码，必须位于 4xx 或 5xx 范围。
	HTTPStatus int `json:"httpStatus"`
}

// ErrorRegistry 防止公开错误码重复或在无迁移记录时改变语义。
type ErrorRegistry struct {
	// SchemaVersion 选择 errors.json 的结构版本，与业务错误 Code 无关。
	SchemaVersion uint32 `json:"schemaVersion"`
	// Reserved 永久保留不允许再次分配的公开错误码。
	Reserved []uint32 `json:"reserved"`
	// Errors 是当前允许跨网络边界返回的完整错误集合。
	Errors []ErrorEntry `json:"errors"`
}

// LookupError 返回指定公开错误码的冻结目录项；未登记错误不得跨网络边界发送。
func (catalog Catalog) LookupError(code uint32) (ErrorEntry, error) {
	for _, entry := range catalog.Errors.Errors {
		if entry.Code == code {
			return entry, nil
		}
	}
	return ErrorEntry{}, fmt.Errorf("public error code %d is not registered", code)
}

// RouteEntry 记录单条消息唯一允许的 channel 与运行策略，禁止多通道双入口。
// 这些值由 dispatcher 和边界 adapter 消费，不能由客户端 payload 覆盖。
type RouteEntry struct {
	// MessageID 关联 MessageRegistry 中恰好一条消息。
	MessageID uint32 `json:"messageId"`
	// Channel 是消息唯一允许使用的可靠通道，例如 WSS 或 TLS_TCP。
	Channel string `json:"channel"`
	// AuthScope 是连接 session 在 dispatch 前必须持有的服务端授权范围。
	AuthScope string `json:"authScope"`
	// QoS 描述可靠性与排序要求，供 adapter 选择发送策略。
	QoS string `json:"qos"`
	// MaxSize 是完整编码 envelope 的字节上限，并且不能超过全局 frame 上限。
	MaxSize uint32 `json:"maxSize"`
	// RatePolicy 指向服务端拥有的限流策略名称，客户端不能自行选择。
	RatePolicy string `json:"ratePolicy"`
	// Idempotency 决定消息需要 REQUEST_ID、COMMAND_ID、任一 CORRELATION_ID 还是不允许关联。
	Idempotency string `json:"idempotency"`
	// TimeoutMS 是服务端处理预算的毫秒数；零值仅在明确允许无响应的消息上有效。
	TimeoutMS uint32 `json:"timeoutMs"`
}

// RouteRegistry 提供后续 dispatcher 消费的完整实时路由集合。
type RouteRegistry struct {
	// SchemaVersion 选择 routes.json 的结构版本，未知版本不得降级解析。
	SchemaVersion uint32 `json:"schemaVersion"`
	// Routes 是当前消息到执行策略的一对一完整映射。
	Routes []RouteEntry `json:"routes"`
}

// Projection 是从 registry 在内存中构建的只读视图，供后续 runtime change 直接消费。
// 它不落盘，避免仓库维护一份可由消息与路由源重复推导的 JSON。
type Projection struct {
	// SchemaVersion 选择内存投影的消费结构版本。
	SchemaVersion uint32 `json:"schemaVersion"`
	// Routes 按 MessageEntry.ID 升序保存合并后的只读路由。
	Routes []ProjectedRoute `json:"routes"`
}

// ProjectedRoute 合并消息与路由信息，使 runtime 无需维护第二份手写映射表。
type ProjectedRoute struct {
	// MessageEntry 保留消息编号、类型、owner 和方向等协议身份。
	MessageEntry
	// RouteEntry 补充该消息唯一允许的传输和执行策略。
	RouteEntry
}

// LookupRoute 解析已登记消息，并拒绝调用方选择不同于 registry 的第二通道。
//
// channel 必须来自实际接收连接的 adapter，而不能来自 payload。成功时返回 catalog 中的值拷贝；
// 未知消息、缺失路由或通道不匹配都返回错误，调用方不得回退到其他通道重试。
func (catalog Catalog) LookupRoute(messageID uint32, channel string) (MessageEntry, RouteEntry, error) {
	// 指针同时表达“尚未找到”，并避免在确认路由前复制可能继续扩展的消息登记项。
	var message *MessageEntry
	for index := range catalog.Messages.Messages {
		if catalog.Messages.Messages[index].ID == messageID {
			message = &catalog.Messages.Messages[index]
			break
		}
	}
	if message == nil {
		return MessageEntry{}, RouteEntry{}, fmt.Errorf("unknown message %d", messageID)
	}
	var matchedRoute *RouteEntry
	for index := range catalog.Routes.Routes {
		route := &catalog.Routes.Routes[index]
		if route.MessageID != messageID {
			continue
		}
		if matchedRoute != nil {
			return MessageEntry{}, RouteEntry{}, fmt.Errorf("message %d has more than one route", messageID)
		}
		matchedRoute = route
	}
	if matchedRoute == nil {
		return MessageEntry{}, RouteEntry{}, fmt.Errorf("message %d has no route", messageID)
	}
	if matchedRoute.Channel != channel {
		return MessageEntry{}, RouteEntry{}, fmt.Errorf("message %d requires channel %s", messageID, matchedRoute.Channel)
	}
	return *message, *matchedRoute, nil
}

// LookupWSSPush 返回可由control publisher发送的只读路由投影。
//
// 本入口同时校验channel、scope、direction、kind与无correlation策略，避免WSS adapter
// 只检查message ID后误发TLS/TCP response、client command或需要关联的消息。
func (catalog Catalog) LookupWSSPush(messageID uint32) (ProjectedRoute, error) {
	message, route, err := catalog.LookupRoute(messageID, "WSS")
	if err != nil {
		return ProjectedRoute{}, err
	}
	if message.Direction != "SERVER_TO_CLIENT" || message.Kind != "PUSH" || route.AuthScope != "CONTROL" ||
		route.QoS != "RELIABLE_ORDERED" || route.Idempotency != "NONE" || route.TimeoutMS != 0 || route.MaxSize == 0 {
		return ProjectedRoute{}, fmt.Errorf("message %d is not a valid WSS control push", messageID)
	}
	return ProjectedRoute{MessageEntry: message, RouteEntry: route}, nil
}

// LookupTLSGameplay 返回由TLS/TCP gameplay adapter消费的严格路由投影。
//
// direction必须是实际socket方向，当前只接受CLIENT_TO_SERVER或SERVER_TO_CLIENT。
// 本入口统一校验channel、scope、QoS与完整frame预算，防止dispatcher或publisher
// 只按message ID建立第二套宽松路由表。
func (catalog Catalog) LookupTLSGameplay(messageID uint32, direction string) (ProjectedRoute, error) {
	message, route, err := catalog.LookupRoute(messageID, "TLS_TCP")
	if err != nil {
		return ProjectedRoute{}, err
	}
	if direction != "CLIENT_TO_SERVER" && direction != "SERVER_TO_CLIENT" {
		return ProjectedRoute{}, errors.New("TLS/TCP gameplay direction is invalid")
	}
	if message.Direction != direction || route.AuthScope != "GAMEPLAY" || route.QoS != "RELIABLE_ORDERED" || route.MaxSize == 0 {
		return ProjectedRoute{}, fmt.Errorf("message %d is not a valid TLS/TCP gameplay route", messageID)
	}
	if direction == "CLIENT_TO_SERVER" && message.Kind != "REQUEST" && message.Kind != "COMMAND" {
		return ProjectedRoute{}, fmt.Errorf("message %d is not a client gameplay operation", messageID)
	}
	if direction == "SERVER_TO_CLIENT" && message.Kind != "RESPONSE" && message.Kind != "PUSH" {
		return ProjectedRoute{}, fmt.Errorf("message %d is not a server gameplay result", messageID)
	}
	return ProjectedRoute{MessageEntry: message, RouteEntry: route}, nil
}

// WSSPushCatalog 返回编译进服务端的control push只读投影。
//
// 该投影只包含当前registry允许由WSS发送的9个PUSH。contract测试必须把它与
// shared/contracts/registry逐字段比对，避免运行时依赖工作区文件或维护第二套宽松路由。
func WSSPushCatalog() Catalog {
	profiles := []ProjectedRoute{
		wssPushProfile(500, "CONTROL_MAINTENANCE_PUSH", "control", "ihomeland.control.v1.MaintenancePush", 4096),
		wssPushProfile(501, "CONTROL_FORCED_LOGOUT_PUSH", "control", "ihomeland.control.v1.ForcedLogoutPush", 2048),
		wssPushProfile(502, "CONTROL_QUEUE_STATUS_PUSH", "control", "ihomeland.control.v1.QueueStatusPush", 1024),
		wssPushProfile(503, "CONTROL_ENDPOINT_UPDATE_PUSH", "control", "ihomeland.control.v1.EndpointUpdatePush", 8192),
		wssPushProfile(504, "CONTROL_SESSION_INVALIDATED_PUSH", "control", "ihomeland.control.v1.SessionInvalidatedPush", 2048),
		wssPushProfile(2003, "WORLD_ASSIGNMENT_CHANGED_PUSH", "world", "ihomeland.world.v1.WorldAssignmentChangedPush", 16384),
		wssPushProfile(2100, "VISIT_INVITE_PUSH", "visit", "ihomeland.visit.v1.VisitInvitePush", 16384),
		wssPushProfile(2101, "VISIT_OWNER_AVAILABILITY_PUSH", "visit", "ihomeland.visit.v1.VisitOwnerAvailabilityPush", 16384),
		wssPushProfile(2102, "VISIT_CLOSED_NOTICE_PUSH", "visit", "ihomeland.visit.v1.VisitClosedNoticePush", 16384),
	}
	catalog := Catalog{Messages: MessageRegistry{SchemaVersion: 1}, Routes: RouteRegistry{SchemaVersion: 1}}
	for _, profile := range profiles {
		catalog.Messages.Messages = append(catalog.Messages.Messages, profile.MessageEntry)
		catalog.Routes.Routes = append(catalog.Routes.Routes, profile.RouteEntry)
	}
	return catalog
}

// TLSGameplayCatalog 返回编译进服务端的world/visit可靠业务路由投影。
//
// 该投影必须由contract测试与registry逐字段比较。它同时携带dispatcher可能公开的最小错误集合，
// 但不包含WSS消息、握手preface、未登记heartbeat或generic world action，因此adapter无法在运行时扩张协议面。
func TLSGameplayCatalog() Catalog {
	profiles := []ProjectedRoute{
		tlsGameplayProfile(2000, "WORLD_SNAPSHOT_REQUEST", "world", "ihomeland.world.v1.WorldSnapshotRequest", "REQUEST", "CLIENT_TO_SERVER", 4096, "world_read", "REQUEST_ID", 10000),
		tlsGameplayProfile(2001, "WORLD_SNAPSHOT_RESPONSE", "world", "ihomeland.world.v1.WorldSnapshotResponse", "RESPONSE", "SERVER_TO_CLIENT", 65536, "world_read", "CORRELATION_ID", 10000),
		tlsGameplayProfile(2002, "WORLD_SNAPSHOT_PUSH", "world", "ihomeland.world.v1.WorldSnapshotPush", "PUSH", "SERVER_TO_CLIENT", 65536, "server_world", "NONE", 0),
		tlsGameplayProfile(2103, "VISIT_OPEN_COMMAND", "visit", "ihomeland.visit.v1.VisitOpenCommand", "COMMAND", "CLIENT_TO_SERVER", 4096, "visit_command", "COMMAND_ID", 5000),
		tlsGameplayProfile(2104, "VISIT_OPEN_RESPONSE", "visit", "ihomeland.visit.v1.VisitOpenResponse", "RESPONSE", "SERVER_TO_CLIENT", 16384, "visit_command", "CORRELATION_ID", 5000),
		tlsGameplayProfile(2105, "VISIT_CREATE_INVITE_COMMAND", "visit", "ihomeland.visit.v1.VisitCreateInviteCommand", "COMMAND", "CLIENT_TO_SERVER", 4096, "visit_command", "COMMAND_ID", 5000),
		tlsGameplayProfile(2106, "VISIT_CREATE_INVITE_RESPONSE", "visit", "ihomeland.visit.v1.VisitCreateInviteResponse", "RESPONSE", "SERVER_TO_CLIENT", 16384, "visit_command", "CORRELATION_ID", 5000),
		tlsGameplayProfile(2107, "VISIT_REVOKE_INVITE_COMMAND", "visit", "ihomeland.visit.v1.VisitRevokeInviteCommand", "COMMAND", "CLIENT_TO_SERVER", 4096, "visit_command", "COMMAND_ID", 5000),
		tlsGameplayProfile(2108, "VISIT_REVOKE_INVITE_RESPONSE", "visit", "ihomeland.visit.v1.VisitRevokeInviteResponse", "RESPONSE", "SERVER_TO_CLIENT", 16384, "visit_command", "CORRELATION_ID", 5000),
		tlsGameplayProfile(2109, "VISIT_JOIN_COMMAND", "visit", "ihomeland.visit.v1.VisitJoinCommand", "COMMAND", "CLIENT_TO_SERVER", 4096, "visit_command", "COMMAND_ID", 5000),
		tlsGameplayProfile(2110, "VISIT_JOIN_RESPONSE", "visit", "ihomeland.visit.v1.VisitJoinResponse", "RESPONSE", "SERVER_TO_CLIENT", 16384, "visit_command", "CORRELATION_ID", 5000),
		tlsGameplayProfile(2111, "VISIT_LEAVE_COMMAND", "visit", "ihomeland.visit.v1.VisitLeaveCommand", "COMMAND", "CLIENT_TO_SERVER", 4096, "visit_command", "COMMAND_ID", 5000),
		tlsGameplayProfile(2112, "VISIT_LEAVE_RESPONSE", "visit", "ihomeland.visit.v1.VisitLeaveResponse", "RESPONSE", "SERVER_TO_CLIENT", 16384, "visit_command", "CORRELATION_ID", 5000),
		tlsGameplayProfile(2113, "VISIT_KICK_COMMAND", "visit", "ihomeland.visit.v1.VisitKickCommand", "COMMAND", "CLIENT_TO_SERVER", 4096, "visit_command", "COMMAND_ID", 5000),
		tlsGameplayProfile(2114, "VISIT_KICK_RESPONSE", "visit", "ihomeland.visit.v1.VisitKickResponse", "RESPONSE", "SERVER_TO_CLIENT", 16384, "visit_command", "CORRELATION_ID", 5000),
		tlsGameplayProfile(2115, "VISIT_RECONNECT_COMMAND", "visit", "ihomeland.visit.v1.VisitReconnectCommand", "COMMAND", "CLIENT_TO_SERVER", 4096, "visit_command", "COMMAND_ID", 5000),
		tlsGameplayProfile(2116, "VISIT_RECONNECT_RESPONSE", "visit", "ihomeland.visit.v1.VisitReconnectResponse", "RESPONSE", "SERVER_TO_CLIENT", 16384, "visit_command", "CORRELATION_ID", 5000),
		tlsGameplayProfile(2117, "VISIT_CLOSE_COMMAND", "visit", "ihomeland.visit.v1.VisitCloseCommand", "COMMAND", "CLIENT_TO_SERVER", 4096, "visit_command", "COMMAND_ID", 5000),
		tlsGameplayProfile(2118, "VISIT_CLOSE_RESPONSE", "visit", "ihomeland.visit.v1.VisitCloseResponse", "RESPONSE", "SERVER_TO_CLIENT", 16384, "visit_command", "CORRELATION_ID", 5000),
		tlsGameplayProfile(2119, "VISIT_SNAPSHOT_REQUEST", "visit", "ihomeland.visit.v1.VisitSnapshotRequest", "REQUEST", "CLIENT_TO_SERVER", 4096, "world_read", "REQUEST_ID", 10000),
		tlsGameplayProfile(2120, "VISIT_SNAPSHOT_RESPONSE", "visit", "ihomeland.visit.v1.VisitSnapshotResponse", "RESPONSE", "SERVER_TO_CLIENT", 65536, "world_read", "CORRELATION_ID", 10000),
		tlsGameplayProfile(2121, "VISIT_SNAPSHOT_PUSH", "visit", "ihomeland.visit.v1.VisitSnapshotPush", "PUSH", "SERVER_TO_CLIENT", 65536, "server_world", "NONE", 0),
		tlsGameplayProfile(2122, "VISIT_SAFE_RETURN_PUSH", "visit", "ihomeland.visit.v1.VisitSafeReturnPush", "PUSH", "SERVER_TO_CLIENT", 16384, "server_world", "NONE", 0),
	}
	catalog := Catalog{
		Messages: MessageRegistry{SchemaVersion: 1},
		Errors:   tlsGameplayErrorRegistry(),
		Routes:   RouteRegistry{SchemaVersion: 1},
	}
	for _, profile := range profiles {
		catalog.Messages.Messages = append(catalog.Messages.Messages, profile.MessageEntry)
		catalog.Routes.Routes = append(catalog.Routes.Routes, profile.RouteEntry)
	}
	return catalog
}

// tlsGameplayProfile 集中构造编译期TLS/TCP profile，参数逐字段对应registry事实。
func tlsGameplayProfile(id uint32, name string, owner string, protobufName string, kind string, direction string, maxSize uint32, ratePolicy string, idempotency string, timeoutMS uint32) ProjectedRoute {
	return ProjectedRoute{
		MessageEntry: MessageEntry{ID: id, Name: name, Owner: owner, Protobuf: protobufName, Kind: kind, Direction: direction},
		RouteEntry: RouteEntry{MessageID: id, Channel: "TLS_TCP", AuthScope: "GAMEPLAY", QoS: "RELIABLE_ORDERED", MaxSize: maxSize,
			RatePolicy: ratePolicy, Idempotency: idempotency, TimeoutMS: timeoutMS},
	}
}

// wssPushProfile 集中冻结所有WSS PUSH共享策略，只让身份和frame预算逐项变化。
func wssPushProfile(id uint32, name string, owner string, protobufName string, maxSize uint32) ProjectedRoute {
	return ProjectedRoute{
		MessageEntry: MessageEntry{ID: id, Name: name, Owner: owner, Protobuf: protobufName, Kind: "PUSH", Direction: "SERVER_TO_CLIENT"},
		RouteEntry:   RouteEntry{MessageID: id, Channel: "WSS", AuthScope: "CONTROL", QoS: "RELIABLE_ORDERED", MaxSize: maxSize, RatePolicy: "server_control", Idempotency: "NONE"},
	}
}
