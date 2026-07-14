package tlsconfig

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
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

// TestBuildCreatesVerifiedTLSConfig 覆盖 CA、server name、最低版本与 mTLS identity 构造。
func TestBuildCreatesVerifiedTLSConfig(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	caFile, certificateFile, keyFile := writePEMFixtures(t, directory)
	reference, err := secret.ParseReference("file:" + keyFile)
	if err != nil {
		t.Fatal(err)
	}
	key, err := secret.NewEnvironmentFileProvider().Resolve(context.Background(), reference)
	if err != nil {
		t.Fatal(err)
	}
	defer key.Destroy()
	result, err := Build(config.StorageTLS{
		Enabled: true, ServerName: "storage.internal", CAFile: caFile,
		ClientCertificateFile: certificateFile,
	}, &key)
	if err != nil {
		t.Fatal(err)
	}
	if result.ServerName != "storage.internal" || result.MinVersion != tls.VersionTLS12 || result.RootCAs == nil || len(result.Certificates) != 1 {
		t.Fatalf("TLS config 不完整：%+v", result)
	}
}

// TestBuildErrorsHidePEMPath 防止本机 certificate 路径进入默认启动诊断。
func TestBuildErrorsHidePEMPath(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sensitive-deployment-name.pem")
	_, err := Build(config.StorageTLS{Enabled: true, ServerName: "storage.internal", CAFile: path}, nil)
	if err == nil || strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "sensitive-deployment-name") {
		t.Fatalf("TLS read error 必须脱敏：%v", err)
	}
}

// writePEMFixtures 创建测试私有 self-signed CA 与匹配 client certificate/key。
func writePEMFixtures(t *testing.T, directory string) (string, string, string) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "storage.internal"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, IsCA: true, BasicConstraintsValid: true,
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	caFile := filepath.Join(directory, "ca.pem")
	certificateFile := filepath.Join(directory, "client.pem")
	keyFile := filepath.Join(directory, "client.key")
	for path, content := range map[string][]byte{caFile: certificatePEM, certificateFile: certificatePEM, keyFile: privatePEM} {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return caFile, certificateFile, keyFile
}
