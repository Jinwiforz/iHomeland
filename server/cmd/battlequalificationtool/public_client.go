package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/testclient"
)

// newPublicClient 创建只信任 run-local CA、固定 TLS 1.3 且有总 timeout 的 HTTP client。
func newPublicClient(
	baseURL string,
	caFile string,
	timeout time.Duration,
) (*testclient.HTTPClient, *tls.Config, error) {
	if timeout <= 0 {
		return nil, nil, errors.New("battle qualification HTTP timeout is invalid")
	}
	certificate, err := os.ReadFile(caFile)
	if err != nil {
		return nil, nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certificate) {
		return nil, nil, errors.New("battle qualification CA is invalid")
	}
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS13,
		MaxVersion: tls.VersionTLS13,
		RootCAs:    roots,
		ServerName: "localhost",
	}
	transport := &http.Transport{
		TLSClientConfig:     tlsConfig.Clone(),
		ForceAttemptHTTP2:   false,
		MaxIdleConns:        8,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     timeout,
	}
	client, err := testclient.NewHTTPClient(baseURL, &http.Client{
		Transport: transport,
		Timeout:   timeout,
	})
	return client, tlsConfig, err
}
