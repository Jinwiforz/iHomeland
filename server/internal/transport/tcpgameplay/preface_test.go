package tcpgameplay

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jinwiforz/ihomeland/server/internal/worldadmission"
)

// prefaceFixture 是跨端golden中公开的合成credential与确定性wire结果。
type prefaceFixture struct {
	// SchemaVersion 选择fixture结构版本。
	SchemaVersion uint32 `json:"schemaVersion"`
	// Magic 是固定preface识别符。
	Magic string `json:"magic"`
	// Version 是固定preface版本。
	Version uint16 `json:"version"`
	// Purpose 是fixture使用的封闭admission用途。
	Purpose string `json:"purpose"`
	// Ticket 是不授予任何权限的合成ticket文本。
	Ticket string `json:"ticket"`
	// Admission 是不对应store记录的合成admission文本。
	Admission string `json:"admission"`
	// FrameHex 是包含长度前缀的完整wire bytes。
	FrameHex string `json:"frameHex"`
}

// TestPrefaceGoldenRoundTrip 验证跨端fixture、严格语法、确定性编码和默认脱敏。
func TestPrefaceGoldenRoundTrip(t *testing.T) {
	t.Parallel()
	fixture := loadPrefaceFixture(t)
	frame, err := EncodePreface(fixture.Ticket, fixture.Admission, worldadmission.PurposeOwnWorld)
	if err != nil {
		t.Fatal(err)
	}
	if actual := hex.EncodeToString(frame); actual != fixture.FrameHex {
		t.Fatalf("frame = %s, want %s", actual, fixture.FrameHex)
	}
	preface, err := ReadPreface(bytes.NewReader(frame), 8192)
	if err != nil || !preface.Valid() {
		t.Fatalf("ReadPreface() = %v, %v", preface, err)
	}
	formatted := fmt.Sprintf("%v %#v", preface, preface)
	if strings.Contains(formatted, fixture.Ticket) || strings.Contains(formatted, fixture.Admission) {
		t.Fatal("formatted preface leaked credential")
	}
}

// TestPrefaceRejectsMalformedInput 覆盖零长、超预算、截断、magic/version、长度与ASCII边界。
func TestPrefaceRejectsMalformedInput(t *testing.T) {
	t.Parallel()
	fixture := loadPrefaceFixture(t)
	valid, _ := hex.DecodeString(fixture.FrameHex)
	tests := map[string][]byte{
		"零长":           {0, 0, 0, 0},
		"超预算":          {0, 0, 32, 1},
		"截断":           valid[:len(valid)-1],
		"错误 magic":     mutateByte(valid, 4, 'X'),
		"错误 version":   mutateByte(valid, 9, 2),
		"错误 ticket 长度": mutateByte(valid, 11, 31),
		"非法 purpose":   mutateByte(valid, 14, 0xff),
		"非 ASCII":      mutateByte(valid, 15, 0x80),
	}
	for name, input := range tests {
		input := input
		t.Run(name, func(t *testing.T) {
			if _, err := ReadPreface(bytes.NewReader(input), 8192); err == nil || strings.Contains(err.Error(), fixture.Ticket) || strings.Contains(err.Error(), fixture.Admission) {
				t.Fatalf("ReadPreface() error = %v", err)
			}
		})
	}
}

// FuzzDecodePreface 验证任意payload只能得到完整preface或脱敏错误且不会panic。
func FuzzDecodePreface(f *testing.F) {
	fixture := loadPrefaceFixture(f)
	frame, _ := hex.DecodeString(fixture.FrameHex)
	f.Add(frame[4:])
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, payload []byte) {
		preface, err := DecodePreface(payload)
		if err == nil && !preface.Valid() {
			t.Fatal("DecodePreface returned invalid success")
		}
		if err != nil && (strings.Contains(err.Error(), fixture.Ticket) || strings.Contains(err.Error(), fixture.Admission)) {
			t.Fatal("DecodePreface error leaked credential")
		}
	})
}

// mutateByte 返回只修改单个位置的wire副本。
func mutateByte(source []byte, index int, value byte) []byte {
	result := append([]byte(nil), source...)
	result[index] = value
	return result
}

// loadPrefaceFixture 从仓库根读取跨端fixture，不依赖当前测试工作目录。
func loadPrefaceFixture(t testing.TB) prefaceFixture {
	t.Helper()
	_, current, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(current), "..", "..", "..", "..", "shared", "contracts", "fixtures", "realtime", "tcp-preface.json")
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture prefaceFixture
	if err := json.Unmarshal(encoded, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.SchemaVersion != 1 || fixture.Magic != PrefaceMagic || fixture.Version != PrefaceVersion || fixture.Purpose != "OWN_WORLD" {
		t.Fatal("preface fixture metadata drifted")
	}
	return fixture
}
