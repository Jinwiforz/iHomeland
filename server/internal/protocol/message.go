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

	// MessageIDRegisterRequest 是账号注册请求。
	MessageIDRegisterRequest MessageID = 1000
	// MessageIDRegisterResponse 是账号注册响应。
	MessageIDRegisterResponse MessageID = 1001
	// MessageIDLoginRequest 是账号登录请求。
	MessageIDLoginRequest MessageID = 1002
	// MessageIDLoginResponse 是账号登录响应。
	MessageIDLoginResponse MessageID = 1003
	// MessageIDLogoutRequest 是账号登出请求。
	MessageIDLogoutRequest MessageID = 1004
	// MessageIDLogoutResponse 是账号登出响应。
	MessageIDLogoutResponse MessageID = 1005
	// MessageIDResumeSessionRequest 是账号会话恢复请求。
	MessageIDResumeSessionRequest MessageID = 1006
	// MessageIDResumeSessionResponse 是账号会话恢复响应。
	MessageIDResumeSessionResponse MessageID = 1007
	// MessageIDGetCurrentPlayerRequest 是查询当前玩家身份请求。
	MessageIDGetCurrentPlayerRequest MessageID = 1008
	// MessageIDGetCurrentPlayerResponse 是查询当前玩家身份响应。
	MessageIDGetCurrentPlayerResponse MessageID = 1009

	// MessageIDCreateRoomRequest 是创建房间请求。
	MessageIDCreateRoomRequest MessageID = 2000
	// MessageIDCreateRoomResponse 是创建房间响应。
	MessageIDCreateRoomResponse MessageID = 2001
	// MessageIDJoinRoomRequest 是加入房间请求。
	MessageIDJoinRoomRequest MessageID = 2002
	// MessageIDJoinRoomResponse 是加入房间响应。
	MessageIDJoinRoomResponse MessageID = 2003
	// MessageIDSetReadyRequest 是设置准备状态请求。
	MessageIDSetReadyRequest MessageID = 2004
	// MessageIDSetReadyResponse 是设置准备状态响应。
	MessageIDSetReadyResponse MessageID = 2005
	// MessageIDLeaveRoomRequest 是退出房间请求。
	MessageIDLeaveRoomRequest MessageID = 2006
	// MessageIDLeaveRoomResponse 是退出房间响应。
	MessageIDLeaveRoomResponse MessageID = 2007
	// MessageIDTransferHostRequest 是转移房主请求。
	MessageIDTransferHostRequest MessageID = 2008
	// MessageIDTransferHostResponse 是转移房主响应。
	MessageIDTransferHostResponse MessageID = 2009
	// MessageIDReconnectRoomRequest 是重连恢复房间身份请求。
	MessageIDReconnectRoomRequest MessageID = 2010
	// MessageIDReconnectRoomResponse 是重连恢复房间身份响应。
	MessageIDReconnectRoomResponse MessageID = 2011
	// MessageIDRoomSnapshotPushed 是房间快照推送。
	MessageIDRoomSnapshotPushed MessageID = 2012
	// MessageIDStartRoomRequest 是房主请求通过第一阶段开始闸门的请求。
	MessageIDStartRoomRequest MessageID = 2013
	// MessageIDStartRoomResponse 是第一阶段开始闸门通过后的响应。
	MessageIDStartRoomResponse MessageID = 2014
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
	// ErrorCodeAccountAlreadyExists 表示注册账号已存在。
	ErrorCodeAccountAlreadyExists ErrorCode = 5
	// ErrorCodeAccountCredentialInvalid 表示账号凭据无效。
	ErrorCodeAccountCredentialInvalid ErrorCode = 6
	// ErrorCodeSessionInvalid 表示账号 session 无效或已过期。
	ErrorCodeSessionInvalid ErrorCode = 7
	// ErrorCodeUnauthenticated 表示请求缺少已登录身份。
	ErrorCodeUnauthenticated ErrorCode = 8
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

// AccountMessages 返回第一阶段账号会话消息注册表。
func AccountMessages() []MessageDescriptor {
	return []MessageDescriptor{
		{ID: MessageIDRegisterRequest, Name: "RegisterRequest", Owner: "account", Comment: "客户端账号注册请求"},
		{ID: MessageIDRegisterResponse, Name: "RegisterResponse", Owner: "account", Comment: "服务端账号注册响应"},
		{ID: MessageIDLoginRequest, Name: "LoginRequest", Owner: "account", Comment: "客户端账号登录请求"},
		{ID: MessageIDLoginResponse, Name: "LoginResponse", Owner: "account", Comment: "服务端账号登录响应"},
		{ID: MessageIDLogoutRequest, Name: "LogoutRequest", Owner: "account", Comment: "客户端账号登出请求"},
		{ID: MessageIDLogoutResponse, Name: "LogoutResponse", Owner: "account", Comment: "服务端账号登出响应"},
		{ID: MessageIDResumeSessionRequest, Name: "ResumeSessionRequest", Owner: "account", Comment: "客户端恢复账号会话请求"},
		{ID: MessageIDResumeSessionResponse, Name: "ResumeSessionResponse", Owner: "account", Comment: "服务端恢复账号会话响应"},
		{ID: MessageIDGetCurrentPlayerRequest, Name: "GetCurrentPlayerRequest", Owner: "account", Comment: "客户端查询当前玩家身份请求"},
		{ID: MessageIDGetCurrentPlayerResponse, Name: "GetCurrentPlayerResponse", Owner: "account", Comment: "服务端查询当前玩家身份响应"},
	}
}

// RoomMessages 返回第一阶段房间大厅消息注册表。
func RoomMessages() []MessageDescriptor {
	return []MessageDescriptor{
		{ID: MessageIDCreateRoomRequest, Name: "CreateRoomRequest", Owner: "room", Comment: "客户端创建自定义房间请求"},
		{ID: MessageIDCreateRoomResponse, Name: "CreateRoomResponse", Owner: "room", Comment: "服务端创建自定义房间响应"},
		{ID: MessageIDJoinRoomRequest, Name: "JoinRoomRequest", Owner: "room", Comment: "客户端加入自定义房间请求"},
		{ID: MessageIDJoinRoomResponse, Name: "JoinRoomResponse", Owner: "room", Comment: "服务端加入自定义房间响应"},
		{ID: MessageIDSetReadyRequest, Name: "SetReadyRequest", Owner: "room", Comment: "客户端设置准备状态请求"},
		{ID: MessageIDSetReadyResponse, Name: "SetReadyResponse", Owner: "room", Comment: "服务端设置准备状态响应"},
		{ID: MessageIDLeaveRoomRequest, Name: "LeaveRoomRequest", Owner: "room", Comment: "客户端退出房间请求"},
		{ID: MessageIDLeaveRoomResponse, Name: "LeaveRoomResponse", Owner: "room", Comment: "服务端退出房间响应"},
		{ID: MessageIDTransferHostRequest, Name: "TransferHostRequest", Owner: "room", Comment: "客户端转移房主请求"},
		{ID: MessageIDTransferHostResponse, Name: "TransferHostResponse", Owner: "room", Comment: "服务端转移房主响应"},
		{ID: MessageIDReconnectRoomRequest, Name: "ReconnectRoomRequest", Owner: "room", Comment: "客户端重连恢复房间身份请求"},
		{ID: MessageIDReconnectRoomResponse, Name: "ReconnectRoomResponse", Owner: "room", Comment: "服务端重连恢复房间身份响应"},
		{ID: MessageIDRoomSnapshotPushed, Name: "RoomSnapshotPushed", Owner: "room", Comment: "服务端房间快照推送"},
		{ID: MessageIDStartRoomRequest, Name: "StartRoomRequest", Owner: "room", Comment: "房主请求通过第一阶段开始闸门"},
		{ID: MessageIDStartRoomResponse, Name: "StartRoomResponse", Owner: "room", Comment: "服务端开始闸门通过响应"},
	}
}

// IsSystemMessageID 判断 id 是否属于系统与网关消息号段。
func IsSystemMessageID(id MessageID) bool {
	return id >= 1 && id <= 999
}

// IsAccountMessageID 判断 id 是否属于账号会话消息号段。
func IsAccountMessageID(id MessageID) bool {
	return id >= 1000 && id <= 1999
}

// IsRoomMessageID 判断 id 是否属于房间大厅消息号段。
func IsRoomMessageID(id MessageID) bool {
	return id >= 2000 && id <= 2999
}
