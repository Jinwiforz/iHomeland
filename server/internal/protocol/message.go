package protocol

// MessageID 是实时协议的稳定消息编号。
type MessageID uint32

const (
	// MessageIDUnspecified 表示未知或未设置的消息编号。
	MessageIDUnspecified MessageID = 0

	// MessageIDHeartbeatRequest 是客户端心跳请求。
	MessageIDHeartbeatRequest MessageID = 1
	// MessageIDHeartbeatResponse 是服务端心跳响应。
	MessageIDHeartbeatResponse MessageID = 2
	// MessageIDErrorResponse 是服务端结构化错误响应。
	MessageIDErrorResponse MessageID = 3
	// MessageIDProtocolVersionUnsupported 是协议版本不兼容响应。
	MessageIDProtocolVersionUnsupported MessageID = 4
)

// ErrorCode 是可跨端识别的稳定协议错误码。
type ErrorCode uint32

const (
	// ErrorCodeUnspecified 表示未知错误。
	ErrorCodeUnspecified ErrorCode = 0
	// ErrorCodeProtocolVersionUnsupported 表示客户端协议版本不在服务端支持范围内。
	ErrorCodeProtocolVersionUnsupported ErrorCode = 1
	// ErrorCodeMessageIDUnsupported 表示服务端不支持该 message id。
	ErrorCodeMessageIDUnsupported ErrorCode = 2
	// ErrorCodePayloadInvalid 表示 payload 缺失、格式错误或类型不匹配。
	ErrorCodePayloadInvalid ErrorCode = 3
	// ErrorCodeRequestIDRequired 表示请求缺少必需的 request id。
	ErrorCodeRequestIDRequired ErrorCode = 4
)

// MessageDescriptor 描述一个可路由协议消息的稳定注册信息。
type MessageDescriptor struct {
	ID      MessageID
	Name    string
	Owner   string
	Comment string
}

// SystemMessages 返回第一阶段系统消息注册表。
func SystemMessages() []MessageDescriptor {
	return []MessageDescriptor{
		{ID: MessageIDHeartbeatRequest, Name: "HeartbeatRequest", Owner: "protocol", Comment: "客户端到服务端的基础心跳请求"},
		{ID: MessageIDHeartbeatResponse, Name: "HeartbeatResponse", Owner: "protocol", Comment: "服务端对心跳请求的响应"},
		{ID: MessageIDErrorResponse, Name: "ErrorResponse", Owner: "protocol", Comment: "服务端结构化错误响应"},
		{ID: MessageIDProtocolVersionUnsupported, Name: "ProtocolVersionUnsupported", Owner: "protocol", Comment: "协议版本不兼容响应"},
	}
}

// IsSystemMessageID 判断 id 是否属于系统与网关消息号段。
func IsSystemMessageID(id MessageID) bool {
	return id >= 1 && id <= 999
}
