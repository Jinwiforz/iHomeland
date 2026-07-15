package httpapi

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/config"
)

// TestComponentServesTLS13AndStopsIdempotently 验证真实listener、TLS握手与graceful Stop边界。
func TestComponentServesTLS13AndStopsIdempotently(t *testing.T) {
	settings := config.DefaultPublicAPI()
	settings.Address = "127.0.0.1:0"
	settings.TLS.Enabled = true
	settings.TLS.CertificateFile = `C:\fixture\certificate.pem`
	settings.TLS.PrivateKeySecret = "env:FIXTURE_KEY"
	tasks := newComponentTaskOwner()
	handler := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) })
	component, err := NewComponent(settings, handler, testTLSConfig(t), tasks)
	if err != nil {
		t.Fatal(err)
	}
	if err := component.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true}}, Timeout: 2 * time.Second} //nolint:gosec -- 测试自签名证书只验证TLS版本与lifecycle。
	response, err := client.Get("https://" + component.Address() + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent || response.TLS == nil || response.TLS.Version != tls.VersionTLS13 {
		t.Fatalf("TLS response status=%d state=%v", response.StatusCode, response.TLS)
	}
	stopContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := component.Stop(stopContext); err != nil {
		t.Fatal(err)
	}
	if err := component.Stop(stopContext); err != nil {
		t.Fatalf("second Stop failed: %v", err)
	}
}

// componentTaskOwner 是component测试的单task监督器。
type componentTaskOwner struct {
	// mutex 保护done只创建一次。
	mutex sync.Mutex
	// done 接收Serve loop最终错误。
	done chan error
}

// newComponentTaskOwner 创建尚未登记task的监督器。
func newComponentTaskOwner() *componentTaskOwner { return &componentTaskOwner{} }

// Go 启动单个受监督测试task。
func (owner *componentTaskOwner) Go(_ string, task func(context.Context) error) error {
	owner.mutex.Lock()
	defer owner.mutex.Unlock()
	if owner.done != nil {
		return errors.New("task already started")
	}
	owner.done = make(chan error, 1)
	go func() { owner.done <- task(context.Background()) }()
	return nil
}

// Stop 等待Serve loop由http.Server shutdown结束。
func (owner *componentTaskOwner) Stop(ctx context.Context, _ error) error {
	owner.mutex.Lock()
	done := owner.done
	owner.mutex.Unlock()
	if done == nil {
		return nil
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// testTLSConfig 生成仅供当前进程自签名握手的TLS 1.3 identity。
func testTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, DNSNames: []string{"localhost"}}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}))
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{identity}}
}
