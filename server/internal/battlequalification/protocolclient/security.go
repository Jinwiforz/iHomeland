package protocolclient

import (
	"context"
	"encoding/binary"
)

const (
	// handshakeAttackPayloadBytes 是 mutation envelope 与 session-start table 总宽度。
	handshakeAttackPayloadBytes = 80
	// handshakeAttackReceiptBytes 是 closed low-sensitive attack receipt 宽度。
	handshakeAttackReceiptBytes = 16
)

// HandshakeAttackKind 是独立客户端实现的 closed pre-session 负例。
type HandshakeAttackKind uint8

const (
	// HandshakeCookieLess 发送结构有效但没有 Retry cookie/proof 的 ClientAuth。
	HandshakeCookieLess HandshakeAttackKind = 1
	// HandshakeProofForgery 完成 Retry 后修改 transcript proof。
	HandshakeProofForgery HandshakeAttackKind = 2
	// HandshakeTicketReplay 在成功消费 ticket 后重放同一 ClientAuth。
	HandshakeTicketReplay HandshakeAttackKind = 3
	// HandshakeSpoofedSource 从不同 UDP source 重放绑定原 source 的 ClientAuth。
	HandshakeSpoofedSource HandshakeAttackKind = 4
)

// HandshakeAttack 是只允许执行一次的 credential-bearing 安全请求。
type HandshakeAttack struct {
	// Kind 决定 cookie、proof、ticket 或 source 边界。
	Kind HandshakeAttackKind
	// Start 复用 exact numeric endpoint 与一次性 credential table。
	Start SessionStart
}

// HandshakeAttackReceipt 是 child 实际 socket 行为的低敏计数。
type HandshakeAttackReceipt struct {
	// Kind 回显已执行的 closed attack。
	Kind HandshakeAttackKind
	// AttackDatagrams 是恶意 datagram 数，不含合法 hello/auth setup。
	AttackDatagrams uint32
	// ResponseDatagrams 是攻击 datagram 收到的 UDP response 数。
	ResponseDatagrams uint32
}

// ProbeHandshakeAttack 消费一次性 credential 并执行真实 pre-session socket 负例。
func (supervisor *Supervisor) ProbeHandshakeAttack(
	ctx context.Context,
	attack HandshakeAttack,
) (HandshakeAttackReceipt, error) {
	supervisor.mutex.Lock()
	if supervisor.credentialDelivered {
		supervisor.mutex.Unlock()
		if attack.Start.Credential != nil {
			attack.Start.Credential.Clear()
		}
		return HandshakeAttackReceipt{}, ErrCredentialAlreadyDelivered
	}
	supervisor.credentialDelivered = true
	supervisor.mutex.Unlock()
	if attack.Kind < HandshakeCookieLess ||
		attack.Kind > HandshakeSpoofedSource {
		if attack.Start.Credential != nil {
			attack.Start.Credential.Clear()
		}
		return HandshakeAttackReceipt{}, ErrInvalidFrame
	}
	start, err := encodeSessionStart(attack.Start)
	if err != nil {
		return HandshakeAttackReceipt{}, err
	}
	defer clear(start)
	payload := make([]byte, handshakeAttackPayloadBytes)
	defer clear(payload)
	payload[0] = byte(attack.Kind)
	copy(payload[4:], start)
	receipt, err := supervisor.exchange(
		ctx,
		KindHandshakeAttackRequest,
		payload,
	)
	if err != nil {
		return HandshakeAttackReceipt{}, err
	}
	defer clear(receipt.Payload)
	if receipt.Kind != KindHandshakeAttackReceipt ||
		len(receipt.Payload) != handshakeAttackReceiptBytes ||
		receipt.Payload[0] != byte(attack.Kind) ||
		receipt.Payload[1] != 1 ||
		binary.BigEndian.Uint16(receipt.Payload[2:4]) != 0 ||
		binary.BigEndian.Uint32(receipt.Payload[4:8]) == 0 ||
		binary.BigEndian.Uint32(receipt.Payload[12:16]) != 0 {
		supervisor.abort()
		return HandshakeAttackReceipt{}, ErrUnexpectedReceipt
	}
	return HandshakeAttackReceipt{
		Kind:              attack.Kind,
		AttackDatagrams:   binary.BigEndian.Uint32(receipt.Payload[4:8]),
		ResponseDatagrams: binary.BigEndian.Uint32(receipt.Payload[8:12]),
	}, nil
}
