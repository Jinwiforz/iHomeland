package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// Clock 为生命周期耗时和可测试 deadline 提供最窄时间边界。
type Clock interface {
	// Now 返回当前绝对时间；调用方负责选择持久或单调语义。
	Now() time.Time
}

// SystemClock 使用 Go time package 提供生产时间。
type SystemClock struct{}

// Now 返回包含单调分量的当前时间。
func (SystemClock) Now() time.Time { return time.Now() }

// IDGenerator 为进程实例和后续 correlation 创建不可预测标识。
type IDGenerator interface {
	// NewID 返回固定 16-byte 随机值的十六进制表达。
	NewID() (string, error)
}

// RandomIDGenerator 使用 crypto/rand，避免可预测序列进入身份或 correlation 边界。
type RandomIDGenerator struct{}

// NewID 从操作系统 CSPRNG 读取 128 bit，并在熵源失败时显式返回错误。
func (RandomIDGenerator) NewID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate process id: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}
