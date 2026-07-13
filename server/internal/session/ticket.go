package session

import (
	"crypto/sha256"
	"errors"
	"log/slog"
)

const (
	// ticketNonceBytes 与跨端 ConnectionTicket 契约保持一致，提供 128-bit 一次性随机值。
	ticketNonceBytes = 16
	// ticketNoncePlaceholder 防止 nonce 通过默认格式化或结构化日志泄漏。
	ticketNoncePlaceholder = "[REDACTED_TICKET_NONCE]"
)

// TicketNonce 是 connection ticket 内用于服务端原子消费的 16-byte 一次性随机值。
//
// Nonce 本身不携带授权；session、epoch、channel、endpoint、scopes 与 expiry 仍以
// SessionStore record 为权威。Transport adapter 只负责在领域值与协议 bytes 间转换。
type TicketNonce struct {
	// value 保存协议要求的固定长度随机 bytes；零值无效。
	value [ticketNonceBytes]byte
}

// NewTicketNonce 使用注入的 CSPRNG 生成协议长度固定的一次性 nonce。
func NewTicketNonce(generator SecretGenerator) (TicketNonce, error) {
	if generator == nil {
		return TicketNonce{}, errors.New("ticket nonce generator is required")
	}
	var value [ticketNonceBytes]byte
	if err := generator.Fill(value[:]); err != nil {
		return TicketNonce{}, err
	}
	nonce := TicketNonce{value: value}
	if !nonce.Valid() {
		return TicketNonce{}, errors.New("ticket nonce entropy is invalid")
	}
	return nonce, nil
}

// ParseTicketNonce 校验不受信协议 bytes 的精确长度并复制内容，避免保留网络 buffer。
func ParseTicketNonce(raw []byte) (TicketNonce, error) {
	if len(raw) != ticketNonceBytes {
		return TicketNonce{}, errors.New("ticket nonce length is invalid")
	}
	var value [ticketNonceBytes]byte
	copy(value[:], raw)
	nonce := TicketNonce{value: value}
	if !nonce.Valid() {
		return TicketNonce{}, errors.New("ticket nonce value is invalid")
	}
	return nonce, nil
}

// Bytes 返回 nonce 副本，仅供协议 adapter 编码；调用方不得记录、缓存或持久化明文。
func (nonce TicketNonce) Bytes() [ticketNonceBytes]byte { return nonce.value }

// Digest 返回 SessionStore lookup 使用的固定长度 SHA-256 摘要。
func (nonce TicketNonce) Digest() Digest { return Digest{value: sha256.Sum256(nonce.value[:])} }

// Valid 报告 nonce 是否不是禁止进入 store 的未初始化零值。
func (nonce TicketNonce) Valid() bool { return nonce.value != [ticketNonceBytes]byte{} }

// String 返回固定占位符，避免 `%v` 泄漏一次性认证材料。
func (TicketNonce) String() string { return ticketNoncePlaceholder }

// GoString 防止 `%#v` 展开 nonce 私有 bytes。
func (TicketNonce) GoString() string { return ticketNoncePlaceholder }

// LogValue 让 slog 记录固定占位符而不是 nonce bytes。
func (TicketNonce) LogValue() slog.Value { return slog.StringValue(ticketNoncePlaceholder) }
