package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jizhuozhi/knowledge/internal/config"
	"github.com/jizhuozhi/knowledge/internal/handlers"
)

func newTestServer(t *testing.T, staticDir string) *httptest.Server {
	t.Helper()
	cfg := &config.Config{}
	cfg.App.Version = "test-version"
	cfg.App.StaticDir = staticDir

	h := handlers.NewHandler(cfg, nil, nil)
	r := SetupRouter(cfg, h)
	return httptest.NewServer(r)
}

func TestHealthEndpoint_E2E(t *testing.T) {
	ts := newTestServer(t, "")
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/health")
	if err != nil {
		t.Fatalf("health request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode health payload failed: %v", err)
	}

	if payload["status"] != "healthy" {
		t.Fatalf("expected status healthy, got %v", payload["status"])
	}
	if payload["version"] != "test-version" {
		t.Fatalf("expected version test-version, got %v", payload["version"])
	}
}

func TestTenantMiddleware_E2E(t *testing.T) {
	ts := newTestServer(t, "")
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/documents", nil)
	if err != nil {
		t.Fatalf("create request failed: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("documents request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, resp.StatusCode)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode error payload failed: %v", err)
	}
	if payload["error"] != "tenant_id is required" {
		t.Fatalf("unexpected error payload: %v", payload)
	}
}

func TestCORSPreflight_E2E(t *testing.T) {
	ts := newTestServer(t, "")
	defer ts.Close()

	req, err := http.NewRequest(http.MethodOptions, ts.URL+"/api/v1/documents", nil)
	if err != nil {
		t.Fatalf("create preflight request failed: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("preflight request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, resp.StatusCode)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("missing CORS allow-origin header")
	}
}

func TestStaticAndSPAFallback_E2E(t *testing.T) {
	staticDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("<html>index</html>"), 0o644); err != nil {
		t.Fatalf("write index.html failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(staticDir, "assets"), 0o755); err != nil {
		t.Fatalf("mkdir assets failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staticDir, "assets", "app.js"), []byte("console.log('ok')"), 0o644); err != nil {
		t.Fatalf("write app.js failed: %v", err)
	}

	ts := newTestServer(t, staticDir)
	defer ts.Close()

	t.Run("root serves index", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/")
		if err != nil {
			t.Fatalf("root request failed: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
		}
	})

	t.Run("assets has cache header", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/assets/app.js")
		if err != nil {
			t.Fatalf("asset request failed: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
		}
		cacheHeader := resp.Header.Get("Cache-Control")
		if !strings.Contains(cacheHeader, "immutable") {
			t.Fatalf("expected immutable cache header, got %q", cacheHeader)
		}
	})

	t.Run("spa fallback", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/app/settings")
		if err != nil {
			t.Fatalf("spa fallback request failed: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
			t.Fatalf("expected html content type, got %q", ct)
		}
	})
}

