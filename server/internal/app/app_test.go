package app

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"ihomeland/server/internal/config"
)

func TestNewHTTPServer(t *testing.T) {
	server, err := NewHTTPServer(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewHTTPServer() error = %v", err)
	}
	if server.Addr != config.Default().HTTPAddr {
		t.Fatalf("Addr = %q, want %q", server.Addr, config.Default().HTTPAddr)
	}

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	server.Handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
}
