package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type ctxKey string

const (
	TenantIDKey ctxKey = "tenant_id"
	UserIDKey   ctxKey = "user_id"
)

type Middleware func(http.Handler) http.Handler

func Chain(h http.Handler, middlewares ...Middleware) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}

func CORS() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With, X-Tenant-ID, X-User-ID")
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE, PATCH")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func Logging() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}

func Recovery() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					JSONError(w, http.StatusInternalServerError, "internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func Tenant() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			if !strings.HasPrefix(path, "/api/") || path == "/api/v1/health" || strings.HasPrefix(path, "/api/v1/admin/") {
				next.ServeHTTP(w, r)
				return
			}

			tenantIDStr := r.Header.Get("X-Tenant-ID")
			if tenantIDStr == "" {
				tenantIDStr = r.URL.Query().Get("tenant_id")
			}
			if tenantIDStr == "" {
				JSONError(w, http.StatusBadRequest, "tenant_id is required")
				return
			}

			ctx := context.WithValue(r.Context(), TenantIDKey, tenantIDStr)
			userIDStr := r.Header.Get("X-User-ID")
			if userIDStr != "" {
				ctx = context.WithValue(ctx, UserIDKey, userIDStr)
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func GetTenantID(ctx context.Context) string {
	if tenantID, ok := ctx.Value(TenantIDKey).(string); ok {
		return tenantID
	}
	return ""
}

func GetUserID(ctx context.Context) string {
	if userID, ok := ctx.Value(UserIDKey).(string); ok {
		return userID
	}
	return ""
}

func JSONResponse(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func JSONError(w http.ResponseWriter, status int, message string) {
	JSONResponse(w, status, map[string]string{"error": message})
}
