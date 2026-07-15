package config

import (
	"strings"
	"testing"
	"time"
)

// TestDefaultPublicAPIValidates 保护本地公开入口只绑定 loopback 且策略完整。
func TestDefaultPublicAPIValidates(t *testing.T) {
	t.Parallel()
	settings := Default()
	if err := settings.Validate(); err != nil {
		t.Fatalf("Default().Validate() error = %v", err)
	}
	if settings.PublicAPI.TLS.Enabled || settings.PublicAPI.Address != "127.0.0.1:8080" {
		t.Fatalf("unexpected local public API defaults: %+v", settings.PublicAPI)
	}
}

// TestPublicAPIValidationRejectsUnsafeCrossFieldValues 覆盖 TLS、地址、endpoint、rate 与 TTL 约束。
func TestPublicAPIValidationRejectsUnsafeCrossFieldValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		// name 是当前非法配置场景。
		name string
		// mutate 只修改一个待验证边界。
		mutate func(*Config)
		// want 是脱敏错误中的稳定片段。
		want string
	}{
		{name: "与诊断地址冲突", mutate: func(value *Config) { value.PublicAPI.Address = value.Diagnostic.Address }, want: "differ"},
		{name: "本地明文暴露公网", mutate: func(value *Config) { value.PublicAPI.Address = "0.0.0.0:8080" }, want: "loopback"},
		{name: "production 明文", mutate: func(value *Config) { value.Environment = "production" }, want: "tls must be enabled"},
		{name: "TLS 相对证书", mutate: func(value *Config) {
			value.PublicAPI.TLS = PublicTLS{Enabled: true, CertificateFile: "cert.pem", PrivateKeySecret: "env:KEY"}
		}, want: "absolute"},
		{name: "endpoint 零端口", mutate: func(value *Config) { value.PublicAPI.Endpoints.TLSTCP.Port = 0 }, want: "tlsTcp.port"},
		{name: "endpoint label 尾连字符", mutate: func(value *Config) { value.PublicAPI.Endpoints.WSS.Host = "control-.example.test" }, want: "endpoints.wss.host"},
		{name: "endpoint label 过长", mutate: func(value *Config) { value.PublicAPI.Endpoints.WSS.Host = strings.Repeat("a", 64) + ".example.test" }, want: "endpoints.wss.host"},
		{name: "endpoint DNS name 过长", mutate: func(value *Config) { value.PublicAPI.Endpoints.WSS.Host = strings.Repeat("a.", 127) + "a" }, want: "endpoints.wss.host"},
		{name: "缺失 operation policy", mutate: func(value *Config) { delete(value.PublicAPI.Rates, "loginAccount") }, want: "every public operation"},
		{name: "burst 超过 rate", mutate: func(value *Config) {
			value.PublicAPI.Rates["loginAccount"] = RatePolicy{Requests: 1, Window: time.Minute, Burst: 2}
		}, want: "requests/burst"},
		{name: "session TTL 逆序", mutate: func(value *Config) { value.PublicAPI.Session.AccessTTL = value.PublicAPI.Session.TicketTTL }, want: "TTL order"},
		{name: "VisitSession reservation 超出领域上限", mutate: func(value *Config) { value.PublicAPI.VisitSession.ReservationLifetime = 3 * time.Minute }, want: "reservationLifetime"},
		{name: "VisitSession Owner grace 超出领域上限", mutate: func(value *Config) { value.PublicAPI.VisitSession.OwnerGrace = 6 * time.Minute }, want: "ownerGrace"},
		{name: "非法 derivation reference", mutate: func(value *Config) { value.PublicAPI.WorldAdmission.DerivationKeySecret = "raw-key" }, want: "reference"},
		{name: "WSS path 漂移", mutate: func(value *Config) { value.PublicAPI.WebSocketControl.Path = "/control" }, want: "frozen contract"},
		{name: "WSS Host 含 scheme", mutate: func(value *Config) { value.PublicAPI.WebSocketControl.AllowedHosts = []string{"https://localhost"} }, want: "allowedHosts"},
		{name: "WSS Origin 含 path", mutate: func(value *Config) {
			value.PublicAPI.WebSocketControl.AllowedOrigins = []string{"https://client.example.test/path"}
		}, want: "allowedOrigins"},
		{name: "WSS queue 小于 frame", mutate: func(value *Config) { value.PublicAPI.WebSocketControl.QueueBytes = 1024 }, want: "queue budget"},
		{name: "WSS deadline 逆序", mutate: func(value *Config) {
			value.PublicAPI.WebSocketControl.PongTimeout = value.PublicAPI.WebSocketControl.IdleTimeout
		}, want: "deadlines"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			settings := Default()
			test.mutate(&settings)
			err := settings.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want fragment %q", err, test.want)
			}
		})
	}
}

// TestPublicAPIEnvironmentOverridesAreExplicit 验证公开入口只接受登记键且错误不回显原值。
func TestPublicAPIEnvironmentOverridesAreExplicit(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, "environment: test\n")
	values := map[string]string{
		"IHOMELAND_PUBLIC_ADDRESS":                "127.0.0.1:0",
		"IHOMELAND_PUBLIC_WSS_HOST":               "control.example.test",
		"IHOMELAND_PUBLIC_WSS_PORT":               "9443",
		"IHOMELAND_PUBLIC_TLS_TCP_HOST":           "game.example.test",
		"IHOMELAND_PUBLIC_TLS_TCP_PORT":           "9444",
		"IHOMELAND_PASSWORD_HASH_CONCURRENCY":     "3",
		"IHOMELAND_WSS_ALLOWED_HOSTS":             "control.example.test:9443",
		"IHOMELAND_WSS_MAX_CONNECTIONS":           "2048",
		"IHOMELAND_UNREGISTERED_PUBLIC_API_VALUE": "ignored",
	}
	settings, err := Load(path, func(key string) (string, bool) { value, ok := values[key]; return value, ok })
	if err != nil {
		t.Fatal(err)
	}
	if settings.PublicAPI.Address != "127.0.0.1:0" || settings.PublicAPI.Endpoints.WSS.Port != 9443 || settings.PublicAPI.Account.MaxConcurrentHashes != 3 ||
		settings.PublicAPI.WebSocketControl.MaxConnections != 2048 || len(settings.PublicAPI.WebSocketControl.AllowedHosts) != 1 {
		t.Fatalf("public API overrides not applied: %+v", settings.PublicAPI)
	}

	secret := "private-invalid-port"
	_, err = Load(path, func(key string) (string, bool) {
		if key == "IHOMELAND_PUBLIC_WSS_PORT" {
			return secret, true
		}
		return "", false
	})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("environment error must be redacted: %v", err)
	}
}
