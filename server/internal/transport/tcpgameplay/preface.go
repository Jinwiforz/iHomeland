package tcpgameplay

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/jinwiforz/ihomeland/server/internal/protocol"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/worldadmission"
)

// Preface 是在任何ReliableEnvelope之前提交的一次性双credential认证材料。
//
// 类型默认格式化始终脱敏；raw credential只在握手调用栈内短暂持有，不进入registry。
type Preface struct {
	// ticket 是已完成规范解码的16-byte一次性nonce。
	ticket session.TicketNonce
	// admission 是固定版本的opaque WorldAdmission credential。
	admission worldadmission.Credential
	// purpose 只选择verifier消费指纹，不能替代返回binding。
	purpose worldadmission.Purpose
}

// Ticket 返回只读nonce值副本。
func (preface Preface) Ticket() session.TicketNonce { return preface.ticket }

// Admission 返回只读opaque credential值。
func (preface Preface) Admission() worldadmission.Credential { return preface.admission }

// Purpose 返回preface声明的封闭消费用途。
func (preface Preface) Purpose() worldadmission.Purpose { return preface.purpose }

// Valid 报告两个credential是否均通过严格语法构造。
func (preface Preface) Valid() bool {
	return preface.ticket.Valid() && preface.admission.Valid() && preface.purpose.Valid()
}

// String 防止默认格式化泄漏任一bearer credential。
func (Preface) String() string { return "[REDACTED_TCP_PREFACE]" }

// GoString 防止详细格式化展开私有字段。
func (Preface) GoString() string { return "[REDACTED_TCP_PREFACE]" }

// EncodePreface 为协议测试客户端确定性编码带4-byte大端长度前缀的authentication preface。
func EncodePreface(ticketHex string, admissionText string, purpose worldadmission.Purpose) ([]byte, error) {
	preface, err := parsePrefaceCredentials(ticketHex, admissionText, purpose)
	if err != nil {
		return nil, err
	}
	if !preface.Valid() {
		return nil, ErrProtocol
	}
	body := make([]byte, prefaceHeaderBytes+ticketTextBytes+admissionTextBytes)
	copy(body[:4], PrefaceMagic)
	binary.BigEndian.PutUint16(body[4:6], PrefaceVersion)
	binary.BigEndian.PutUint16(body[6:8], ticketTextBytes)
	binary.BigEndian.PutUint16(body[8:10], admissionTextBytes)
	body[10] = byte(purpose)
	copy(body[11:11+ticketTextBytes], ticketHex)
	copy(body[11+ticketTextBytes:], admissionText)
	return protocol.EncodeFrame(body)
}

// DecodePreface 解析不含4-byte frame prefix的有界preface payload。
func DecodePreface(payload []byte) (Preface, error) {
	if len(payload) != prefaceHeaderBytes+ticketTextBytes+admissionTextBytes {
		return Preface{}, fmt.Errorf("%w: preface length is invalid", ErrProtocol)
	}
	if string(payload[:4]) != PrefaceMagic || binary.BigEndian.Uint16(payload[4:6]) != PrefaceVersion {
		return Preface{}, fmt.Errorf("%w: preface identity is invalid", ErrProtocol)
	}
	ticketLength := int(binary.BigEndian.Uint16(payload[6:8]))
	admissionLength := int(binary.BigEndian.Uint16(payload[8:10]))
	purpose := worldadmission.Purpose(payload[10])
	if ticketLength != ticketTextBytes || admissionLength != admissionTextBytes || prefaceHeaderBytes+ticketLength+admissionLength != len(payload) {
		return Preface{}, fmt.Errorf("%w: credential lengths are invalid", ErrProtocol)
	}
	if !purpose.Valid() {
		return Preface{}, fmt.Errorf("%w: purpose is invalid", ErrProtocol)
	}
	return parsePrefaceCredentials(string(payload[11:11+ticketLength]), string(payload[11+ticketLength:]), purpose)
}

// ReadPreface 在解析声明长度后先检查预算，再进行一次有界分配和精确读取。
func ReadPreface(reader io.Reader, maximumBytes int) (Preface, error) {
	if reader == nil || maximumBytes < prefaceHeaderBytes+ticketTextBytes+admissionTextBytes {
		return Preface{}, errors.New("tcp gameplay preface reader is incomplete")
	}
	var prefix [4]byte
	if _, err := io.ReadFull(reader, prefix[:]); err != nil {
		return Preface{}, fmt.Errorf("%w: read preface prefix", ErrProtocol)
	}
	length := binary.BigEndian.Uint32(prefix[:])
	if length == 0 || length > uint32(maximumBytes) || length > protocol.MaximumFrameSize {
		return Preface{}, fmt.Errorf("%w: preface frame budget exceeded", ErrProtocol)
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return Preface{}, fmt.Errorf("%w: read preface payload", ErrProtocol)
	}
	return DecodePreface(payload)
}

// parsePrefaceCredentials 严格验证安全ASCII语法，并在解析ticket后清零临时bytes。
func parsePrefaceCredentials(ticketHex string, admissionText string, purpose worldadmission.Purpose) (Preface, error) {
	if len(ticketHex) != ticketTextBytes || len(admissionText) != admissionTextBytes || !safeASCII(ticketHex) || !safeASCII(admissionText) || !purpose.Valid() {
		return Preface{}, fmt.Errorf("%w: credential grammar is invalid", ErrProtocol)
	}
	decoded := make([]byte, ticketTextBytes/2)
	count, err := hex.Decode(decoded, []byte(ticketHex))
	if err != nil || count != len(decoded) || hex.EncodeToString(decoded) != ticketHex {
		clear(decoded)
		return Preface{}, fmt.Errorf("%w: ticket encoding is invalid", ErrProtocol)
	}
	ticket, ticketErr := session.ParseTicketNonce(decoded)
	clear(decoded)
	admission, admissionErr := worldadmission.ParseCredential(admissionText)
	if ticketErr != nil || admissionErr != nil {
		return Preface{}, fmt.Errorf("%w: credential encoding is invalid", ErrProtocol)
	}
	return Preface{ticket: ticket, admission: admission, purpose: purpose}, nil
}

// safeASCII 禁止控制字符、空白和非ASCII字节进入credential parser与错误路径。
func safeASCII(value string) bool {
	for index := range value {
		if value[index] < 0x21 || value[index] > 0x7e {
			return false
		}
	}
	return true
}
