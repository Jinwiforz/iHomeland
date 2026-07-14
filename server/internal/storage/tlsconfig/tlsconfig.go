// Package tlsconfig 从已验证配置构造 storage TLS client policy。
//
// 该包只依赖 config、secret 与有界 PEM 文件读取，不建立网络连接。返回的 tls.Config
// 由调用方持有并在交给 client 后保持只读；private key 的原始 Value 仍由调用方销毁。
package tlsconfig

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/jinwiforz/ihomeland/server/internal/config"
	"github.com/jinwiforz/ihomeland/server/internal/secret"
)

// maximumPEMBytes 限制单个 CA 或 client certificate 文件的启动期内存占用。
const maximumPEMBytes = 1024 * 1024

// Build 构造启用身份验证且最低为 TLS 1.2 的 caller-owned client config。
//
// 该函数只执行有界文件 I/O，不建立连接；返回值交给 client 后不得并发修改。clientKey
// 仅在 callback 内借用，调用方仍须 Destroy 原始 Value；解析后的 private key 由返回的
// tls.Config 持有。CA/certificate 读取错误只标识配置字段，不回显路径或 PEM 内容。
func Build(policy config.StorageTLS, clientKey *secret.Value) (*tls.Config, error) {
	if !policy.Enabled {
		return nil, nil
	}
	caPEM, err := readPEM(policy.CAFile)
	if err != nil {
		return nil, errors.New("read storage tls.caFile failed")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("storage tls.caFile contains no valid certificate")
	}
	result := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: policy.ServerName,
		RootCAs:    roots,
	}
	if policy.ClientCertificateFile == "" {
		return result, nil
	}
	if clientKey == nil {
		return nil, errors.New("storage tls client key is required")
	}
	certificatePEM, err := readPEM(policy.ClientCertificateFile)
	if err != nil {
		return nil, errors.New("read storage tls.clientCertificateFile failed")
	}
	var certificate tls.Certificate
	err = clientKey.Expose(func(keyPEM []byte) error {
		parsed, parseErr := tls.X509KeyPair(certificatePEM, keyPEM)
		if parseErr != nil {
			return errors.New("invalid storage tls client certificate or key")
		}
		certificate = parsed
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("build storage tls client identity: %w", err)
	}
	result.Certificates = []tls.Certificate{certificate}
	return result, nil
}

// readPEM 只读取有界本地文件；调用方统一隐藏路径和 parser 原始内容。
func readPEM(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		// 只读 PEM 的关闭错误不改变已读取内容，也没有可执行的恢复动作。
		_ = file.Close()
	}()
	content, err := io.ReadAll(io.LimitReader(file, maximumPEMBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) == 0 || len(content) > maximumPEMBytes {
		return nil, errors.New("storage tls PEM file is empty or exceeds size limit")
	}
	return content, nil
}
