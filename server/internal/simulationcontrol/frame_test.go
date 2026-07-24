package simulationcontrol

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCanonicalGolden 验证 Go 编码与冻结跨语言 golden 完全一致。
func TestCanonicalGolden(t *testing.T) {
	t.Parallel()
	path := filepath.Join("..", "..", "..", "shared", "contracts", "fixtures", "simulation-control", "canonical-golden.json")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var document struct {
		Frames []struct {
			CanonicalJSON   string `json:"canonicalJson"`
			LengthPrefixHex string `json:"lengthPrefixHex"`
		} `json:"frames"`
	}
	if err := json.Unmarshal(content, &document); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(document.Frames) != 1 {
		t.Fatalf("golden frame count = %d", len(document.Frames))
	}
	requestID, _ := NewRequestID("sctl_health0000000001")
	nonce, _ := NewDigest(strings.Repeat("1", 64))
	frame, err := NewFrame(
		"node.health.query",
		struct {
			SimulationNodeID string `json:"simulationNodeId"`
		}{SimulationNodeID: "snode_fixture_1"},
		requestID,
		3,
		nonce,
	)
	if err != nil {
		t.Fatalf("NewFrame: %v", err)
	}
	encoded, err := EncodeFrame(frame)
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	if got := string(encoded[4:]); got != document.Frames[0].CanonicalJSON {
		t.Fatalf("canonical JSON drifted:\n%s", got)
	}
	if got := hex.EncodeToString(encoded[:4]); got != document.Frames[0].LengthPrefixHex {
		t.Fatalf("length prefix = %s", got)
	}
	decoded, err := DecodeFrame(bufio.NewReader(bytes.NewReader(encoded)))
	if err != nil {
		t.Fatalf("DecodeFrame: %v", err)
	}
	if decoded.Sequence != 3 || decoded.Kind != "node.health.query" {
		t.Fatalf("decoded frame = %#v", decoded)
	}
}

// TestDecodeFrameRejectsMalformed 覆盖 fragmentation、duplicate、unknown 与 canonical failure。
func TestDecodeFrameRejectsMalformed(t *testing.T) {
	t.Parallel()
	tests := map[string][]byte{
		"partial-prefix": {0, 0},
		"oversize":       {0, 1, 0, 1},
		"partial-body":   {0, 0, 0, 8, '{', '}'},
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeFrame(bufio.NewReader(bytes.NewReader(input))); err == nil {
				t.Fatal("malformed frame was accepted")
			}
		})
	}
	canonical := `{"kind":"node.health.query","payload":{"simulationNodeId":"snode_fixture_1"},"requestId":"sctl_health0000000001","schemaVersion":"simulation-control-v1","sequence":"3","sessionNonce":"` + strings.Repeat("1", 64) + `"}`
	mutations := []string{
		strings.Replace(canonical, `"kind":`, `"kind":"node.health.query","kind":`, 1),
		strings.Replace(canonical, `"node.health.query"`, `"node.unknown"`, 1),
		strings.Replace(canonical, `"sequence":"3"`, `"sequence":"03"`, 1),
		" " + canonical,
	}
	for _, mutation := range mutations {
		body := []byte(mutation)
		input := make([]byte, 4+len(body))
		input[0] = byte(len(body) >> 24)
		input[1] = byte(len(body) >> 16)
		input[2] = byte(len(body) >> 8)
		input[3] = byte(len(body))
		copy(input[4:], body)
		if _, err := DecodeFrame(bufio.NewReader(bytes.NewReader(input))); err == nil {
			t.Fatalf("mutation was accepted: %s", mutation[:min(len(mutation), 48)])
		}
	}
}

// TestWriteFrameRejectsShortWrite 验证 partial write 不会伪装成功。
func TestWriteFrameRejectsShortWrite(t *testing.T) {
	t.Parallel()
	requestID, _ := NewRequestID("sctl_health0000000001")
	nonce, _ := NewDigest(strings.Repeat("1", 64))
	frame, _ := NewFrame("node.health.query", map[string]string{"simulationNodeId": "snode_fixture_1"}, requestID, 1, nonce)
	if err := WriteFrame(shortWriter{}, frame); err == nil {
		t.Fatal("short write was accepted")
	}
}

// shortWriter 模拟只写入部分 frame 且不返回底层错误的违约 writer。
type shortWriter struct{}

// Write 只报告一个 byte，触发 io.ErrShortWrite。
func (shortWriter) Write(input []byte) (int, error) {
	if len(input) == 0 {
		return 0, nil
	}
	return 1, nil
}

// FuzzDecodeFrame 确保任意 fragmentation/corruption 不会 panic，接受值必须可规范重编码。
func FuzzDecodeFrame(f *testing.F) {
	requestID, _ := NewRequestID("sctl_health0000000001")
	nonce, _ := NewDigest(strings.Repeat("1", 64))
	frame, _ := NewFrame("node.health.query", map[string]string{"simulationNodeId": "snode_fixture_1"}, requestID, 1, nonce)
	encoded, _ := EncodeFrame(frame)
	f.Add(encoded)
	f.Add([]byte{0, 0, 0, 1, '{'})
	f.Fuzz(func(t *testing.T, input []byte) {
		decoded, err := DecodeFrame(bufio.NewReader(bytes.NewReader(input)))
		if err != nil {
			return
		}
		reencoded, err := EncodeFrame(decoded)
		if err != nil {
			t.Fatalf("accepted frame cannot re-encode: %v", err)
		}
		if !bytes.Equal(reencoded, input) {
			t.Fatal("accepted frame was not canonical and complete")
		}
	})
}
