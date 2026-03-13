package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTenant_BypassPaths(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "non api", path: "/"},
		{name: "health", path: "/api/v1/health"},
		{name: "admin", path: "/api/v1/admin/tenants"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hit := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hit = true
				w.WriteHeader(http.StatusNoContent)
			})

			h := Tenant()(next)
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if !hit {
				t.Fatalf("expected next handler to be called")
			}
			if rr.Code != http.StatusNoContent {
				t.Fatalf("expected status %d, got %d", http.StatusNoContent, rr.Code)
			}
		})
	}
}

func TestTenant_RequiresTenantID(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	h := Tenant()(next)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/documents", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}
}

func TestTenant_FromHeaderAndQuery(t *testing.T) {
	t.Run("from header", func(t *testing.T) {
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := GetTenantID(r.Context()); got != "tenant-header" {
				t.Fatalf("expected tenant-header, got %s", got)
			}
			if got := GetUserID(r.Context()); got != "user-1" {
				t.Fatalf("expected user-1, got %s", got)
			}
			w.WriteHeader(http.StatusNoContent)
		})

		h := Tenant()(next)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/documents", nil)
		req.Header.Set("X-Tenant-ID", "tenant-header")
		req.Header.Set("X-User-ID", "user-1")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusNoContent {
			t.Fatalf("expected status %d, got %d", http.StatusNoContent, rr.Code)
		}
	})

	t.Run("from query", func(t *testing.T) {
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := GetTenantID(r.Context()); got != "tenant-query" {
				t.Fatalf("expected tenant-query, got %s", got)
			}
			w.WriteHeader(http.StatusNoContent)
		})

		h := Tenant()(next)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/documents?tenant_id=tenant-query", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusNoContent {
			t.Fatalf("expected status %d, got %d", http.StatusNoContent, rr.Code)
		}
	})
}
