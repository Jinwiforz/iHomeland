// Package ops 提供健康检查、就绪检查和版本等控制面 HTTP 能力。
package ops

import (
	"encoding/json"
	"fmt"
	"os"
)

// VersionPaths 描述版本元数据文件路径。
type VersionPaths struct {
	Release string
	Server  string
	Client  string
}

// ReleaseVersion 描述全局发布版本信息。
type ReleaseVersion struct {
	Release  string `json:"release"`
	Client   string `json:"client"`
	Server   string `json:"server"`
	Protocol int    `json:"protocol"`
}

// ComponentVersion 描述单个组件版本文件。
type ComponentVersion struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	BuildNumber int    `json:"buildNumber"`
	Commit      string `json:"commit"`
	BuildTime   string `json:"buildTime"`
}

// VersionResponse 是 /version 的机器可读响应。
type VersionResponse struct {
	Release ReleaseVersion   `json:"release"`
	Server  ComponentVersion `json:"server"`
	Client  ComponentVersion `json:"client"`
}

// LoadVersion 读取 release、server 和 client 版本元数据。
func LoadVersion(paths VersionPaths) (VersionResponse, error) {
	release, err := readJSON[ReleaseVersion](paths.Release)
	if err != nil {
		return VersionResponse{}, fmt.Errorf("load release version: %w", err)
	}
	server, err := readJSON[ComponentVersion](paths.Server)
	if err != nil {
		return VersionResponse{}, fmt.Errorf("load server version: %w", err)
	}
	client, err := readJSON[ComponentVersion](paths.Client)
	if err != nil {
		return VersionResponse{}, fmt.Errorf("load client version: %w", err)
	}
	return VersionResponse{Release: release, Server: server, Client: client}, nil
}

func readJSON[T any](path string) (T, error) {
	var value T

	data, err := os.ReadFile(path)
	if err != nil {
		return value, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return value, fmt.Errorf("decode %s: %w", path, err)
	}
	return value, nil
}
