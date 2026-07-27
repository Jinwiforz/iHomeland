package protocolclient

import (
	"encoding/binary"
	"fmt"
	"net/netip"
)

const (
	// sessionStartPayloadBytes 是 session-start-v1 fixed field table 宽度。
	sessionStartPayloadBytes = 76
	// sessionSuccessPayloadBytes 是 session-event-v1 成功变体宽度。
	sessionSuccessPayloadBytes = 2
	// sessionEstablishedEvent 是唯一允许的成功 handshake 状态。
	sessionEstablishedEvent = 1
	// sessionFailurePayloadBytes 是 session-event-v1 失败变体宽度。
	sessionFailurePayloadBytes = 3
	// sessionFailedEvent 表示 child 未建立 session 并返回闭合阶段码。
	sessionFailedEvent = 2
	// addressFamilyIPv4 是 numeric IPv4 的 closed discriminator。
	addressFamilyIPv4 = 4
	// addressFamilyIPv6 是 numeric IPv6 的 closed discriminator。
	addressFamilyIPv6 = 6
)

// SessionStartFailure 是不含 credential、endpoint 或 payload 的闭合失败阶段。
type SessionStartFailure uint8

const (
	// SessionStartFailureInvalidRequest 表示 fixed request 未通过闭合校验。
	SessionStartFailureInvalidRequest SessionStartFailure = 1
	// SessionStartFailureDeadline 表示 request、ticket 或 handshake deadline 无效。
	SessionStartFailureDeadline SessionStartFailure = 2
	// SessionStartFailureSocket 表示 numeric UDP socket 无法建立。
	SessionStartFailureSocket SessionStartFailure = 3
	// SessionStartFailureHandshakeSetup 表示 transcript 或 secret owner 无法初始化。
	SessionStartFailureHandshakeSetup SessionStartFailure = 4
	// SessionStartFailureRetryExchange 表示 Retry exchange 未完成。
	SessionStartFailureRetryExchange SessionStartFailure = 5
	// SessionStartFailureRetryValidation 表示 Retry 未通过 closed envelope/transcript 校验。
	SessionStartFailureRetryValidation SessionStartFailure = 6
	// SessionStartFailureAcceptExchange 表示 ServerAccept exchange 未完成。
	SessionStartFailureAcceptExchange SessionStartFailure = 7
	// SessionStartFailureAcceptValidation 表示 ServerAccept 未通过认证解密。
	SessionStartFailureAcceptValidation SessionStartFailure = 8
	// SessionStartFailureTransportSetup 表示认证握手后 transport owner 无法建立。
	SessionStartFailureTransportSetup SessionStartFailure = 9
	// SessionStartFailureUnknown 表示未进入更精确阶段的内部失败。
	SessionStartFailureUnknown SessionStartFailure = 10
)

// Valid 报告失败阶段是否属于 closed session-event-v1 registry。
func (failure SessionStartFailure) Valid() bool {
	return failure >= SessionStartFailureInvalidRequest &&
		failure <= SessionStartFailureUnknown
}

// String 返回稳定低敏标签，不包含底层异常文本。
func (failure SessionStartFailure) String() string {
	switch failure {
	case SessionStartFailureInvalidRequest:
		return "invalid-request"
	case SessionStartFailureDeadline:
		return "deadline"
	case SessionStartFailureSocket:
		return "socket"
	case SessionStartFailureHandshakeSetup:
		return "handshake-setup"
	case SessionStartFailureRetryExchange:
		return "retry-exchange"
	case SessionStartFailureRetryValidation:
		return "retry-validation"
	case SessionStartFailureAcceptExchange:
		return "accept-exchange"
	case SessionStartFailureAcceptValidation:
		return "accept-validation"
	case SessionStartFailureTransportSetup:
		return "transport-setup"
	case SessionStartFailureUnknown:
		return "unknown"
	default:
		return "invalid"
	}
}

// SessionStartError 保存 child 返回的闭合阶段，不持有原始 receipt。
type SessionStartError struct {
	// Failure 是 session-event-v1 登记的失败阶段。
	Failure SessionStartFailure
}

// Error 返回可进入低敏 evidence 的稳定文本。
func (failure *SessionStartError) Error() string {
	if failure == nil {
		return "battle protocol client session start failed: invalid"
	}
	return "battle protocol client session start failed: " +
		failure.Failure.String()
}

// SessionCredential 是只允许写入 child stdin 一次的 secret material。
type SessionCredential struct {
	// TicketID 是 16-byte opaque ticket lookup identity。
	TicketID [16]byte
	// TicketSecret 是 HTTPS response 交付的 256-bit bearer。
	TicketSecret [32]byte
}

// Clear 清零全部 credential material；调用者必须在所有成功/失败路径执行。
func (credential *SessionCredential) Clear() {
	if credential == nil {
		return
	}
	clear(credential.TicketID[:])
	clear(credential.TicketSecret[:])
}

// SessionStart 冻结 client slot、numeric gateway endpoint 与一次性 credential。
type SessionStart struct {
	// ClientSlot 是 run-local 1..8 actor slot。
	ClientSlot uint8
	// Endpoint 是 fault gateway 的 numeric advertised endpoint。
	Endpoint netip.AddrPort
	// Credential 是调用后无论成功失败都会被清零的 secret owner。
	Credential *SessionCredential
	// TicketExpiresAtUnixMS 是握手本地 absolute deadline。
	TicketExpiresAtUnixMS uint64
}

// encodeSessionStart 消费 credential 并生成 fixed 76-byte payload。
func encodeSessionStart(start SessionStart) ([]byte, error) {
	if start.Credential == nil {
		return nil, fmt.Errorf("%w: missing credential", ErrInvalidFrame)
	}
	defer start.Credential.Clear()
	if start.ClientSlot == 0 || start.ClientSlot > 8 ||
		!start.Endpoint.IsValid() ||
		start.Endpoint.Addr().IsUnspecified() ||
		start.Endpoint.Port() == 0 ||
		start.TicketExpiresAtUnixMS == 0 {
		return nil, ErrInvalidFrame
	}
	payload := make([]byte, sessionStartPayloadBytes)
	payload[0] = start.ClientSlot
	if start.Endpoint.Addr().Is4() {
		payload[1] = addressFamilyIPv4
	} else {
		payload[1] = addressFamilyIPv6
	}
	binary.BigEndian.PutUint16(payload[2:4], start.Endpoint.Port())
	address := start.Endpoint.Addr().As16()
	copy(payload[4:20], address[:])
	copy(payload[20:36], start.Credential.TicketID[:])
	copy(payload[36:68], start.Credential.TicketSecret[:])
	binary.BigEndian.PutUint64(payload[68:76], start.TicketExpiresAtUnixMS)
	return payload, nil
}
