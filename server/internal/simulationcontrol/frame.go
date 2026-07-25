package simulationcontrol

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
)

var knownKinds = map[string]struct{}{
	"battle.session.closed":        {},
	"battle.session.revoke":        {},
	"battle.ticket.install":        {},
	"battle.ticket.installed":      {},
	"battle.ticket.revoke":         {},
	"battle.ticket.revoked":        {},
	"battle.ticket.status.query":   {},
	"battle.ticket.status.receipt": {},
	"instance.drain":               {},
	"instance.drained":             {},
	"instance.ready":               {},
	"instance.start":               {},
	"instance.status.query":        {},
	"instance.status.receipt":      {},
	"instance.stop":                {},
	"instance.stopped":             {},
	"node.health.query":            {},
	"node.health.receipt":          {},
	"node.hello.challenge":         {},
	"node.hello.receipt":           {},
	"node.listener.status.query":   {},
	"node.listener.status.receipt": {},
	"node.shutdown":                {},
	"node.stopped":                 {},
	"result.ack":                   {},
	"result.proposal":              {},
}

// Frame 是私有 stdio control protocol 的 canonical envelope。
type Frame struct {
	// Kind 是冻结 message inventory 中的名称。
	Kind string
	// Payload 是已规范化的 closed JSON object。
	Payload json.RawMessage
	// RequestID 关联 request/receipt 或 result handshake。
	RequestID RequestID
	// SchemaVersion 固定为 simulation-control-v1。
	SchemaVersion string
	// Sequence 是双向共享的单调正整数。
	Sequence uint64
	// SessionNonce 是单次 child bootstrap 的 256-bit nonce。
	SessionNonce Digest
}

// frameWire 使用字段声明顺序生成冻结 key 排序。
type frameWire struct {
	// Kind 是冻结 message inventory 名称。
	Kind string `json:"kind"`
	// Payload 是 canonical JSON object。
	Payload json.RawMessage `json:"payload"`
	// RequestID 是稳定 correlation identity。
	RequestID string `json:"requestId"`
	// SchemaVersion 是冻结 frame 版本。
	SchemaVersion string `json:"schemaVersion"`
	// Sequence 使用规范十进制文本，避免跨语言整数精度差异。
	Sequence string `json:"sequence"`
	// SessionNonce 绑定单次 child incarnation。
	SessionNonce string `json:"sessionNonce"`
}

// NewFrame 校验公共 frame 字段并规范 payload。
func NewFrame(kind string, payload any, requestID RequestID, sequence uint64, nonce Digest) (Frame, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Frame{}, fmt.Errorf("marshal control payload: %w", err)
	}
	frame := Frame{
		Kind:          kind,
		Payload:       raw,
		RequestID:     requestID,
		SchemaVersion: SchemaVersion,
		Sequence:      sequence,
		SessionNonce:  nonce,
	}
	if err := frame.Validate(); err != nil {
		return Frame{}, err
	}
	return frame, nil
}

// Validate 检查 inventory、identity、sequence 与 canonical object payload。
func (frame Frame) Validate() error {
	if frame.SchemaVersion != SchemaVersion || frame.Sequence == 0 ||
		!frame.RequestID.Valid() || !frame.SessionNonce.Valid() {
		return errors.New("control frame identity is invalid")
	}
	if _, ok := knownKinds[frame.Kind]; !ok {
		return errors.New("control frame kind is unknown")
	}
	canonical, err := canonicalObject(frame.Payload)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, frame.Payload) {
		return errors.New("control frame payload is not canonical")
	}
	return nil
}

// EncodeFrame 生成 4-byte big-endian length-prefixed canonical JSON。
func EncodeFrame(frame Frame) ([]byte, error) {
	if err := frame.Validate(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(frameWire{
		Kind:          frame.Kind,
		Payload:       frame.Payload,
		RequestID:     frame.RequestID.String(),
		SchemaVersion: frame.SchemaVersion,
		Sequence:      strconv.FormatUint(frame.Sequence, 10),
		SessionNonce:  frame.SessionNonce.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal control frame: %w", err)
	}
	if len(payload) == 0 || len(payload) > MaximumFrameBytes {
		return nil, errors.New("control frame exceeds hard limit")
	}
	encoded := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(encoded[:4], uint32(len(payload)))
	copy(encoded[4:], payload)
	return encoded, nil
}

// DecodeFrame 读取一个完整 frame；clean EOF 返回 io.EOF，partial EOF 返回 io.ErrUnexpectedEOF。
func DecodeFrame(reader *bufio.Reader) (Frame, error) {
	if reader == nil {
		return Frame{}, errors.New("control reader is nil")
	}
	var prefix [4]byte
	if _, err := io.ReadFull(reader, prefix[:]); err != nil {
		return Frame{}, err
	}
	length := binary.BigEndian.Uint32(prefix[:])
	if length == 0 || length > MaximumFrameBytes {
		return Frame{}, errors.New("control frame prefix exceeds hard limit")
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return Frame{}, err
	}
	if err := rejectDuplicateMembers(payload); err != nil {
		return Frame{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var wire frameWire
	if err := decoder.Decode(&wire); err != nil {
		return Frame{}, fmt.Errorf("decode control frame: %w", err)
	}
	sequence, err := parseCanonicalUint64(wire.Sequence)
	if err != nil {
		return Frame{}, fmt.Errorf("decode control sequence: %w", err)
	}
	requestID, err := NewRequestID(wire.RequestID)
	if err != nil {
		return Frame{}, err
	}
	nonce, err := NewDigest(wire.SessionNonce)
	if err != nil {
		return Frame{}, err
	}
	frame := Frame{
		Kind:          wire.Kind,
		Payload:       wire.Payload,
		RequestID:     requestID,
		SchemaVersion: wire.SchemaVersion,
		Sequence:      sequence,
		SessionNonce:  nonce,
	}
	if err := frame.Validate(); err != nil {
		return Frame{}, err
	}
	reencoded, err := EncodeFrame(frame)
	if err != nil {
		return Frame{}, err
	}
	if !bytes.Equal(reencoded[4:], payload) {
		return Frame{}, errors.New("control frame is not canonical JSON")
	}
	return frame, nil
}

// WriteFrame 完整写入 frame；任何 short write 都视为 terminal protocol failure。
func WriteFrame(writer io.Writer, frame Frame) error {
	if writer == nil {
		return errors.New("control writer is nil")
	}
	encoded, err := EncodeFrame(frame)
	if err != nil {
		return err
	}
	written, err := writer.Write(encoded)
	if err != nil {
		return fmt.Errorf("write control frame: %w", err)
	}
	if written != len(encoded) {
		return io.ErrShortWrite
	}
	return nil
}

// canonicalObject 解析任意 JSON object 后用 encoding/json 稳定重编码。
func canonicalObject(payload []byte) ([]byte, error) {
	if err := rejectDuplicateMembers(payload); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode control payload: %w", err)
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, errors.New("control payload must be object")
	}
	if decoder.More() {
		return nil, errors.New("control payload has trailing value")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("canonicalize control payload: %w", err)
	}
	return canonical, nil
}

// rejectDuplicateMembers 遍历所有 JSON object 并拒绝重复 key 或 trailing token。
func rejectDuplicateMembers(payload []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("control JSON has trailing token")
		}
		return fmt.Errorf("scan control JSON: %w", err)
	}
	return nil
}

// scanJSONValue 递归消费单个 JSON value 并维护每层 object key 集合。
func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("scan control JSON: %w", err)
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("scan control object key: %w", err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("control object key is not string")
			}
			if _, duplicate := seen[key]; duplicate {
				return errors.New("control JSON contains duplicate member")
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("control JSON object is incomplete")
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("control JSON array is incomplete")
		}
	default:
		return errors.New("control JSON delimiter is invalid")
	}
	return nil
}

// parseCanonicalUint64 拒绝空值、前导零和越界整数。
func parseCanonicalUint64(value string) (uint64, error) {
	if value == "" || len(value) > 1 && value[0] == '0' {
		return 0, errors.New("control decimal is not canonical")
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed == 0 {
		return 0, errors.New("control decimal is invalid")
	}
	return parsed, nil
}
