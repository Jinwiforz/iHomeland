// Command qualificationtool 生成临时 TLS、探测独立服务端并运行 Q0 场景。
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/testclient"
)

// main 是唯一进程退出边界，不输出 credential、路径或 endpoint。
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "qualification tool failed")
		os.Exit(1)
	}
}

// run 分派封闭子命令，拒绝未知或缺失动作。
func run(arguments []string) error {
	if len(arguments) == 0 {
		return errors.New("qualification tool command is missing")
	}
	switch arguments[0] {
	case "tls":
		return runTLS(arguments[1:])
	case "probe":
		return runProbe(arguments[1:])
	case "run":
		return runScenarios(arguments[1:])
	default:
		return errors.New("qualification tool command is unknown")
	}
}

// runTLS 生成只覆盖 localhost/loopback 且短期有效的自签名测试 identity。
func runTLS(arguments []string) error {
	flags := flag.NewFlagSet("tls", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	directory := flags.String("directory", "", "absolute output directory")
	if err := flags.Parse(arguments); err != nil || flags.NArg() != 0 || !filepath.IsAbs(*directory) {
		return errors.New("qualification TLS arguments are invalid")
	}
	if err := os.MkdirAll(*directory, 0o700); err != nil {
		return err
	}
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "iHomeland qualification"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return err
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return err
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	if err := os.WriteFile(filepath.Join(*directory, "server-cert.pem"), certificatePEM, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*directory, "server-key.pem"), privatePEM, 0o600); err != nil {
		return err
	}
	admissionEntropy := make([]byte, 32)
	if _, err := rand.Read(admissionEntropy); err != nil {
		return err
	}
	// file: secret 是文本边界；hex 保留 256-bit 熵，同时排除 NUL 与换行解析歧义。
	admissionKey := make([]byte, hex.EncodedLen(len(admissionEntropy)))
	hex.Encode(admissionKey, admissionEntropy)
	return os.WriteFile(filepath.Join(*directory, "admission-key"), admissionKey, 0o600)
}

// runProbe 只通过公开 HTTPS version/config 判断独立服务端是否 ready。
func runProbe(arguments []string) error {
	flags := flag.NewFlagSet("probe", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	baseURL := flags.String("base-url", "", "public HTTPS base URL")
	caFile := flags.String("ca-file", "", "temporary CA PEM")
	timeout := flags.Duration("timeout", 2*time.Second, "probe deadline")
	if err := flags.Parse(arguments); err != nil || flags.NArg() != 0 {
		return errors.New("qualification probe arguments are invalid")
	}
	client, _, err := newPublicClient(*baseURL, *caFile, *timeout)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if _, err := client.Version(ctx); err != nil {
		return err
	}
	_, err = client.BootstrapConfig(ctx)
	return err
}

// runScenarios 按 manifest 顺序运行选择集，并始终写出已完成/跳过结果。
func runScenarios(arguments []string) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	baseURL := flags.String("base-url", "", "public HTTPS base URL")
	caFile := flags.String("ca-file", "", "temporary CA PEM")
	repositoryRoot := flags.String("repository-root", "", "absolute repository root")
	manifestPath := flags.String("manifest", "", "qualification manifest JSON")
	reportPath := flags.String("report", "", "absolute report JSON path")
	controlDirectory := flags.String("control-directory", "", "absolute fault checkpoint directory")
	runID := flags.String("run-id", "", "stable run identity")
	selectedText := flags.String("scenarios", "all", "comma-separated scenario IDs or all")
	evidenceText := flags.String("evidence", "", "comma-separated completed evidence IDs")
	if err := flags.Parse(arguments); err != nil || flags.NArg() != 0 || !filepath.IsAbs(*repositoryRoot) || !filepath.IsAbs(*manifestPath) || !filepath.IsAbs(*reportPath) || !filepath.IsAbs(*controlDirectory) {
		return errors.New("qualification run arguments are invalid")
	}
	manifestFile, err := os.Open(*manifestPath)
	if err != nil {
		return err
	}
	manifest, err := testclient.LoadManifest(manifestFile)
	_ = manifestFile.Close()
	if err != nil {
		return err
	}
	client, tlsConfig, err := newPublicClient(*baseURL, *caFile, 15*time.Second)
	if err != nil {
		return err
	}
	bootstrapContext, cancelBootstrap := context.WithTimeout(context.Background(), 15*time.Second)
	version, err := client.Version(bootstrapContext)
	if err != nil {
		cancelBootstrap()
		return err
	}
	bootstrap, err := client.BootstrapConfig(bootstrapContext)
	cancelBootstrap()
	if err != nil {
		return err
	}
	digest, _, err := testclient.ContractFreezeDigest(*repositoryRoot)
	if err != nil {
		return err
	}
	started := time.Now().UTC()
	faults, err := testclient.NewFileFaultController(*controlDirectory)
	if err != nil {
		return err
	}
	report := testclient.NewQualificationReport(*runID, version.ProtocolVersion, digest, started)
	selected := parseSet(*selectedText)
	evidence := parseSet(*evidenceText)
	runtime := &testclient.ScenarioRuntime{RepositoryRoot: *repositoryRoot, HTTP: client, TLSConfig: tlsConfig, Bootstrap: bootstrap, Evidence: evidence, Faults: faults}
	failed := false
	for _, definition := range manifest.Scenarios {
		result := testclient.ScenarioReport{ID: definition.ID, Phase: definition.Phase, Evidence: definition.Evidence, Execution: definition.Execution, Mandatory: definition.Mandatory, Outcome: "skipped"}
		if failed || *selectedText != "all" && !selected[definition.ID] {
			report.Scenarios = append(report.Scenarios, result)
			continue
		}
		scenarioStarted := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(definition.TimeoutMS)*time.Millisecond)
		err := testclient.RunScenario(ctx, definition.ID, runtime)
		cancel()
		result.DurationMS = time.Since(scenarioStarted).Milliseconds()
		if err != nil {
			result.Outcome = "fail"
			failed = true
			fmt.Printf("[FAIL] %s category=%s\n", definition.ID, safeFailureCategory(err))
		} else {
			result.Outcome = "pass"
			fmt.Printf("[PASS] %s\n", definition.ID)
		}
		report.Scenarios = append(report.Scenarios, result)
	}
	report.DurationMS = time.Since(started).Milliseconds()
	if err := testclient.WriteQualificationReport(*reportPath, report); err != nil {
		return err
	}
	if failed {
		return errors.New("mandatory qualification scenario failed")
	}
	return nil
}

// safeFailureCategory 把场景错误压缩为低基数类别，不输出 endpoint、identity、payload 或 backend 文本。
func safeFailureCategory(err error) string {
	var httpFailure testclient.PublicError
	if errors.As(err, &httpFailure) {
		return fmt.Sprintf("http_public_%d", httpFailure.Code)
	}
	var realtimeFailure testclient.RealtimePublicError
	if errors.As(err, &realtimeFailure) {
		return fmt.Sprintf("realtime_public_%d", realtimeFailure.Code)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "deadline"
	}
	var networkFailure net.Error
	if errors.As(err, &networkFailure) {
		return "network"
	}
	return "assertion"
}

// newPublicClient 创建只信任临时 CA、固定 TLS 1.3 且有总 timeout 的公开 HTTP client。
func newPublicClient(baseURL, caFile string, timeout time.Duration) (*testclient.HTTPClient, *tls.Config, error) {
	certificate, err := os.ReadFile(caFile)
	if err != nil {
		return nil, nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certificate) {
		return nil, nil, errors.New("qualification CA is invalid")
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "localhost"}
	httpClient := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsConfig.Clone(), ForceAttemptHTTP2: false}, Timeout: timeout}
	client, err := testclient.NewHTTPClient(baseURL, httpClient)
	return client, tlsConfig, err
}

// parseSet 把 comma-separated stable IDs 转换为只读使用的成员集合。
func parseSet(value string) map[string]bool {
	result := make(map[string]bool)
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			result[item] = true
		}
	}
	return result
}
