package protocol

import (
	"fmt"

	pb "ihomeland/server/internal/protocol/pb/realtime/v1"
)

const (
	// MinSupportedVersion 是当前服务端支持的最小实时协议版本。
	MinSupportedVersion uint32 = 1
	// MaxSupportedVersion 是当前服务端支持的最大实时协议版本。
	MaxSupportedVersion uint32 = 1
)

// VersionError 表示客户端协议版本不在服务端支持范围内。
type VersionError struct {
	ClientVersion uint32
	MinSupported  uint32
	MaxSupported  uint32
}

// Error 返回可诊断的协议版本错误描述。
func (e VersionError) Error() string {
	return fmt.Sprintf("protocol version %d is unsupported, supported range is %d-%d", e.ClientVersion, e.MinSupported, e.MaxSupported)
}

// Response 构造可返回给客户端的协议版本不兼容消息。
func (e VersionError) Response() *pb.ProtocolVersionUnsupported {
	return &pb.ProtocolVersionUnsupported{
		ClientVersion:       e.ClientVersion,
		MinSupportedVersion: e.MinSupported,
		MaxSupportedVersion: e.MaxSupported,
	}
}

// CheckVersion 校验客户端实时协议版本是否在服务端支持范围内。
func CheckVersion(version uint32) error {
	if version < MinSupportedVersion || version > MaxSupportedVersion {
		return VersionError{
			ClientVersion: version,
			MinSupported:  MinSupportedVersion,
			MaxSupported:  MaxSupportedVersion,
		}
	}
	return nil
}
