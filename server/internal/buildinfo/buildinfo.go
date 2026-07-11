// Package buildinfo 提供进程启动后保持不变的构建身份。
package buildinfo

import (
	"errors"
	"time"
)

var (
	// version 由发布构建通过 linker 注入，本地默认值保持明确未发布状态。
	version = "0.0.0"
	// commit 由发布构建注入来源提交，本地运行不伪造 Git identity。
	commit = "development"
	// builtAt 可选注入 RFC3339 时间，空值保持可重复本地构建。
	builtAt = ""
)

// Info 是诊断端点允许公开的有界构建元数据。
type Info struct {
	// Version 是服务端发布版本，不承担工具链版本治理。
	Version string `json:"version"`
	// Commit 是构建来源提交；本地运行使用 development。
	Commit string `json:"commit"`
	// BuiltAt 是可选 RFC3339 构建时间，空值表示本地未注入。
	BuiltAt string `json:"builtAt,omitempty"`
}

// Current 读取 linker 注入值并返回不可变副本。
func Current() Info {
	return Info{Version: version, Commit: commit, BuiltAt: builtAt}
}

// Validate 保证诊断响应有界，并拒绝构建流水线注入的畸形元数据。
func (info Info) Validate() error {
	if len(info.Version) < 1 || len(info.Version) > 32 {
		return errors.New("build version must contain 1-32 bytes")
	}
	if len(info.Commit) < 1 || len(info.Commit) > 64 {
		return errors.New("build commit must contain 1-64 bytes")
	}
	if info.BuiltAt != "" {
		if len(info.BuiltAt) > 64 {
			return errors.New("build time must not exceed 64 bytes")
		}
		if _, err := time.Parse(time.RFC3339, info.BuiltAt); err != nil {
			return errors.New("build time must use RFC3339")
		}
	}
	return nil
}
