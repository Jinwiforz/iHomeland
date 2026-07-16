package testclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// EndpointManifestExample 把公开 version 与 bootstrap config 示例组成同一交付样本。
// 字段仍分别服从 OpenAPI 的 VersionResponse 与 BootstrapConfigResponse，不创建运行配置源。
type EndpointManifestExample struct {
	// SchemaVersion 选择交付示例结构代际。
	SchemaVersion int `json:"schemaVersion"`
	// Version 是公开 GET /v1/version 的封闭响应。
	Version VersionProjection `json:"version"`
	// Config 是公开 GET /v1/config 的封闭响应。
	Config BootstrapProjection `json:"config"`
}

// VersionProjection 是客户端启动兼容性所需的公开版本字段。
type VersionProjection struct {
	// ProtocolVersion 是 realtime envelope/preface 协议代际。
	ProtocolVersion uint32 `json:"protocolVersion"`
	// MinimumClientVersion 是服务端接受的最低客户端版本。
	MinimumClientVersion string `json:"minimumClientVersion"`
	// ServerVersion 是服务端公开构建版本。
	ServerVersion string `json:"serverVersion"`
}

// BootstrapProjection 是客户端从公开配置解析的 endpoint 与资源上限。
type BootstrapProjection struct {
	// Endpoints 是当前部署公开的完整 realtime endpoint 集合。
	Endpoints []Endpoint `json:"endpoints"`
	// Limits 是客户端必须执行的公开输入边界。
	Limits PublicLimits `json:"limits"`
}

// Endpoint 是公开 channel 与网络地址的客户端投影。
type Endpoint struct {
	// Channel 是 WSS 或 TLS_TCP。
	Channel string `json:"channel"`
	// Host 是部署公布的连接主机，不应从测试内部配置猜测。
	Host string `json:"host"`
	// Port 是 TCP 端口，范围为 1-65535。
	Port uint16 `json:"port"`
}

// PublicLimits 是公开 HTTP body 与 realtime frame 上限。
type PublicLimits struct {
	// HTTPBodyBytes 是 HTTP request body 上限，单位为字节。
	HTTPBodyBytes int `json:"httpBodyBytes"`
	// RealtimeFrameBytes 是 WSS/TCP frame 上限，单位为字节。
	RealtimeFrameBytes int `json:"realtimeFrameBytes"`
}

// LoadEndpointManifestExample 严格解码并验证公开 endpoint 交付示例。
func LoadEndpointManifestExample(reader io.Reader) (EndpointManifestExample, error) {
	if reader == nil {
		return EndpointManifestExample{}, errors.New("endpoint manifest reader is nil")
	}
	decoder := json.NewDecoder(io.LimitReader(reader, 1<<20))
	decoder.DisallowUnknownFields()
	var example EndpointManifestExample
	if err := decoder.Decode(&example); err != nil {
		return EndpointManifestExample{}, fmt.Errorf("decode endpoint manifest example: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return EndpointManifestExample{}, err
	}
	if err := example.Validate(); err != nil {
		return EndpointManifestExample{}, err
	}
	return example, nil
}

// Validate 确认示例只表达公开 schema 允许的版本、channel 和资源范围。
func (example EndpointManifestExample) Validate() error {
	if example.SchemaVersion != 1 || example.Version.ProtocolVersion == 0 || example.Version.MinimumClientVersion == "" || example.Version.ServerVersion == "" {
		return errors.New("endpoint manifest version projection is incomplete")
	}
	if example.Config.Limits.HTTPBodyBytes < 1024 || example.Config.Limits.HTTPBodyBytes > 1<<20 || example.Config.Limits.RealtimeFrameBytes < 1024 || example.Config.Limits.RealtimeFrameBytes > 1<<20 {
		return errors.New("endpoint manifest public limits are outside OpenAPI bounds")
	}
	channels := make(map[string]struct{}, len(example.Config.Endpoints))
	for _, endpoint := range example.Config.Endpoints {
		if endpoint.Channel != "WSS" && endpoint.Channel != "TLS_TCP" {
			return fmt.Errorf("endpoint channel %q is unsupported", endpoint.Channel)
		}
		if endpoint.Host == "" || endpoint.Port == 0 {
			return fmt.Errorf("endpoint %q is incomplete", endpoint.Channel)
		}
		if _, exists := channels[endpoint.Channel]; exists {
			return fmt.Errorf("endpoint channel %q is duplicated", endpoint.Channel)
		}
		channels[endpoint.Channel] = struct{}{}
	}
	for _, required := range []string{"WSS", "TLS_TCP"} {
		if _, exists := channels[required]; !exists {
			return fmt.Errorf("endpoint channel %q is missing", required)
		}
	}
	return nil
}
