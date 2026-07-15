package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/config"
	"github.com/jinwiforz/ihomeland/server/internal/secret"
)

// TestReadBoundedCertificateRejectsOversizedFile 验证启动期证书读取不会按文件大小无界分配。
func TestReadBoundedCertificateRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.pem")
	if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, maximumCertificateBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if contents, err := readBoundedCertificate(path); err == nil || len(contents) != 0 {
		t.Fatalf("oversized certificate result bytes=%d err=%v", len(contents), err)
	}
}

// TestPreparePublicAPIResolvesTLSAndDerivationKey 验证 secret 只在解析阶段暴露且 TLS 1.3 被固定。
func TestPreparePublicAPIResolvesTLSAndDerivationKey(t *testing.T) {
	certificatePath, privateKeyPEM := writeTestCertificate(t)
	t.Setenv("TEST_PUBLIC_PRIVATE_KEY", string(privateKeyPEM))
	t.Setenv("TEST_ADMISSION_KEY", strings.Repeat("k", 32))
	settings := config.DefaultPublicAPI()
	settings.TLS = config.PublicTLS{Enabled: true, CertificateFile: certificatePath, PrivateKeySecret: "env:TEST_PUBLIC_PRIVATE_KEY"}
	settings.WorldAdmission.DerivationKeySecret = "env:TEST_ADMISSION_KEY"

	prepared, err := preparePublicAPI(context.Background(), settings, secret.NewEnvironmentFileProvider())
	if err != nil {
		t.Fatal(err)
	}
	if prepared.tlsConfig == nil || prepared.tlsConfig.MinVersion != 0x0304 || len(prepared.tlsConfig.Certificates) != 1 {
		t.Fatalf("unexpected TLS config: %+v", prepared.tlsConfig)
	}
	if len(prepared.derivationKey) != 32 {
		t.Fatalf("derivation key length = %d", len(prepared.derivationKey))
	}
	owned := prepared.derivationKey
	prepared.Destroy()
	for _, value := range owned {
		if value != 0 {
			t.Fatal("Destroy() did not clear derivation key")
		}
	}
}

// TestPreparePublicAPIFailsClosedAndRedactsSecret 验证短 key 与无效 TLS pair 不产生可用结果或泄漏。
func TestPreparePublicAPIFailsClosedAndRedactsSecret(t *testing.T) {
	t.Setenv("TEST_SHORT_ADMISSION_KEY", "sensitive-short-key")
	settings := config.DefaultPublicAPI()
	settings.WorldAdmission.DerivationKeySecret = "env:TEST_SHORT_ADMISSION_KEY"
	_, err := preparePublicAPI(context.Background(), settings, secret.NewEnvironmentFileProvider())
	if err == nil || strings.Contains(err.Error(), "sensitive-short-key") {
		t.Fatalf("short key error must be redacted: %v", err)
	}

	certificatePath, _ := writeTestCertificate(t)
	t.Setenv("TEST_BAD_PRIVATE_KEY", "sensitive-invalid-private-key")
	t.Setenv("TEST_VALID_ADMISSION_KEY", strings.Repeat("a", 32))
	settings.TLS = config.PublicTLS{Enabled: true, CertificateFile: certificatePath, PrivateKeySecret: "env:TEST_BAD_PRIVATE_KEY"}
	settings.WorldAdmission.DerivationKeySecret = "env:TEST_VALID_ADMISSION_KEY"
	_, err = preparePublicAPI(context.Background(), settings, secret.NewEnvironmentFileProvider())
	if err == nil || strings.Contains(err.Error(), "sensitive-invalid-private-key") {
		t.Fatalf("invalid TLS identity error must be redacted: %v", err)
	}
}

// writeTestCertificate 创建只用于进程内 TLS parser 测试的短期自签名 identity。
func writeTestCertificate(t *testing.T) (string, []byte) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{"localhost"},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	path := filepath.Join(t.TempDir(), "server.pem")
	if err := os.WriteFile(path, certificatePEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, privateKeyPEM
}
