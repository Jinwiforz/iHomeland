// Package protocol 实现不依赖 listener 的实时 envelope、frame codec 与 revision 边界。
//
// 该包只处理 wire-level 不变量，不负责认证、路由授权或业务状态迁移。保持纯 Go 边界使所有
// transport adapter 能共享同一解码规则，也让畸形输入在接触 dispatcher 前被独立测试和拒绝。
package protocol

import (
	"errors"
	"fmt"

	commonv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/common/v1"
	"google.golang.org/protobuf/proto"
)

// MarshalEnvelope 在确定性编码前验证路由与 correlation 不变量，避免生成无法分派的数据。
//
// envelope 在调用期间只读且不会被保留；返回字节由调用方独占。验证失败或 Protobuf 编码失败
// 均不返回部分数据，调用方不得绕过该入口直接发送未经校验的 envelope。
func MarshalEnvelope(envelope *commonv1.ReliableEnvelope) ([]byte, error) {
	if err := ValidateEnvelope(envelope); err != nil {
		return nil, err
	}
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("marshal reliable envelope: %w", err)
	}
	if len(encoded) > MaximumFrameSize {
		return nil, fmt.Errorf("reliable envelope exceeds %d bytes", MaximumFrameSize)
	}
	return encoded, nil
}

// UnmarshalEnvelope 在 dispatch 前拒绝畸形字节和非法 correlation 组合。
//
// encoded 可以在返回后立即复用，Protobuf decoder 会构建独立消息。未知字段被保留以支持兼容
// 转发，但未知 MessageKind 和违反当前 correlation 契约的输入仍会失败。
func UnmarshalEnvelope(encoded []byte) (*commonv1.ReliableEnvelope, error) {
	if len(encoded) == 0 {
		return nil, errors.New("envelope is empty")
	}
	if len(encoded) > MaximumFrameSize {
		return nil, fmt.Errorf("reliable envelope exceeds %d bytes", MaximumFrameSize)
	}
	envelope := new(commonv1.ReliableEnvelope)
	if err := (proto.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(encoded, envelope); err != nil {
		return nil, fmt.Errorf("unmarshal reliable envelope: %w", err)
	}
	if err := ValidateEnvelope(envelope); err != nil {
		return nil, err
	}
	return envelope, nil
}

// ValidateEnvelope 在不假设具体网络 listener 的情况下执行各 kind 对 identifier 的约束。
//
// 该函数不检查 message registry、认证身份或 payload 类型；这些职责依赖连接上下文，必须由
// dispatcher 在 codec 成功后完成。错误表示 envelope 不可路由，不具备自动重试语义。
func ValidateEnvelope(envelope *commonv1.ReliableEnvelope) error {
	if envelope == nil {
		return errors.New("envelope is nil")
	}
	if envelope.GetProtocolVersion() == 0 || envelope.GetMessageId() == 0 {
		return errors.New("protocol version and message id must be nonzero")
	}
	if envelope.GetSequence() == 0 || envelope.GetTimestampMs() <= 0 {
		return errors.New("sequence and timestamp must be positive")
	}
	// 先统一验证长度，再判断 kind，确保所有分支都只处理“缺失”或合法 16-byte identifier。
	requestLength := len(envelope.GetRequestId())
	commandLength := len(envelope.GetCommandId())
	if requestLength != 0 && requestLength != 16 {
		return fmt.Errorf("request id must be 16 bytes, got %d", requestLength)
	}
	if commandLength != 0 && commandLength != 16 {
		return fmt.Errorf("command id must be 16 bytes, got %d", commandLength)
	}
	switch envelope.GetKind() {
	case commonv1.MessageKind_MESSAGE_KIND_REQUEST:
		if requestLength != 16 || commandLength != 0 {
			return errors.New("request requires request id and forbids command id")
		}
	case commonv1.MessageKind_MESSAGE_KIND_RESPONSE:
		if (requestLength == 16) == (commandLength == 16) {
			return errors.New("response requires exactly one request or command id")
		}
	case commonv1.MessageKind_MESSAGE_KIND_COMMAND:
		if commandLength != 16 || requestLength != 0 {
			return errors.New("command requires command id and forbids request id")
		}
	case commonv1.MessageKind_MESSAGE_KIND_PUSH:
		if requestLength != 0 || commandLength != 0 {
			return errors.New("push forbids correlation identifiers")
		}
	case commonv1.MessageKind_MESSAGE_KIND_ERROR:
		if requestLength == 0 && commandLength == 0 {
			return errors.New("error requires one correlation identifier")
		}
		if requestLength != 0 && commandLength != 0 {
			return errors.New("error cannot carry both correlation identifiers")
		}
	default:
		return fmt.Errorf("unknown message kind %d", envelope.GetKind())
	}
	return nil
}
