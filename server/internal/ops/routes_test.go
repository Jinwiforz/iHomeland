package ops

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"ihomeland/server/internal/infra"
)

func TestHealthz(t *testing.T) {
	router := newTestRouter(t, VersionPaths{}, fakeChecker{
		statuses: []infra.Status{
			{Name: "mysql", Ready: false, Error: "connection refused"},
			{Name: "redis", Ready: false, Error: "connection refused"},
		},
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), `"status":"ok"`) {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestReadyz(t *testing.T) {
	router := newTestRouter(t, VersionPaths{}, fakeChecker{
		statuses: []infra.Status{
			{Name: "mysql", Ready: true},
			{Name: "redis", Ready: true},
		},
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), `"status":"ready"`) {
		t.Fatalf("body = %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"name":"mysql"`) {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestReadyzReportsMySQLUnavailable(t *testing.T) {
	router := newTestRouter(t, VersionPaths{}, fakeChecker{
		statuses: []infra.Status{
			{Name: "mysql", Ready: false, Error: "connection refused"},
			{Name: "redis", Ready: true},
		},
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	router.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(response.Body.String(), `"status":"not_ready"`) {
		t.Fatalf("body = %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"name":"mysql"`) {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestReadyzReportsRedisUnavailable(t *testing.T) {
	router := newTestRouter(t, VersionPaths{}, fakeChecker{
		statuses: []infra.Status{
			{Name: "mysql", Ready: true},
			{Name: "redis", Ready: false, Error: "connection refused"},
		},
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	router.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(response.Body.String(), `"name":"redis"`) {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestVersion(t *testing.T) {
	dir := t.TempDir()
	paths := VersionPaths{
		Release: filepath.Join(dir, "release.json"),
		Server:  filepath.Join(dir, "server.json"),
		Client:  filepath.Join(dir, "client.json"),
	}
	writeFile(t, paths.Release, `{"release":"0.1.0","client":"0.1.0","server":"0.1.0","protocol":1}`)
	writeFile(t, paths.Server, `{"name":"server","version":"0.1.0","buildNumber":2,"commit":"abc","buildTime":"2026-06-25 18:00:00"}`)
	writeFile(t, paths.Client, `{"name":"client","version":"0.1.0","buildNumber":3,"commit":"def","buildTime":"2026-06-25 18:01:00"}`)
	router := newTestRouter(t, paths, fakeChecker{})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/version", nil)
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"protocol":1`) {
		t.Fatalf("body = %s", response.Body.String())
	}
}

type fakeChecker struct {
	statuses []infra.Status
}

func (f fakeChecker) Check(context.Context) []infra.Status {
	return f.statuses
}

func newTestRouter(t *testing.T, paths VersionPaths, checker DependencyChecker) *gin.Engine {
	t.Helper()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router, paths, checker, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return router
}
