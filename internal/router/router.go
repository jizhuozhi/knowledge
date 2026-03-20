package router

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/jizhuozhi/knowledge/internal/config"
	"github.com/jizhuozhi/knowledge/internal/handlers"
	"github.com/jizhuozhi/knowledge/internal/middleware"
)

func SetupRouter(cfg *config.Config, handler *handlers.Handler) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/health", handler.HealthCheck)

	mux.HandleFunc("GET /api/v1/admin/tenants", handler.ListTenants)
	mux.HandleFunc("POST /api/v1/admin/tenants", handler.CreateTenant)
	mux.HandleFunc("GET /api/v1/admin/tenants/{id}", handler.GetTenant)
	mux.HandleFunc("PUT /api/v1/admin/tenants/{id}", handler.UpdateTenant)
	mux.HandleFunc("DELETE /api/v1/admin/tenants/{id}", handler.DeleteTenant)

	mux.HandleFunc("GET /api/v1/knowledge-bases", handler.ListKnowledgeBases)
	mux.HandleFunc("POST /api/v1/knowledge-bases", handler.CreateKnowledgeBase)
	mux.HandleFunc("GET /api/v1/knowledge-bases/{id}", handler.GetKnowledgeBase)
	mux.HandleFunc("PUT /api/v1/knowledge-bases/{id}", handler.UpdateKnowledgeBase)
	mux.HandleFunc("DELETE /api/v1/knowledge-bases/{id}", handler.DeleteKnowledgeBase)
	mux.HandleFunc("GET /api/v1/knowledge-bases/{id}/observability", handler.GetKnowledgeBaseObservability)

	mux.HandleFunc("GET /api/v1/documents", handler.ListDocuments)
	mux.HandleFunc("POST /api/v1/documents", handler.CreateDocument)
	mux.HandleFunc("POST /api/v1/documents/upload", handler.UploadDocument)
	mux.HandleFunc("GET /api/v1/documents/{id}", handler.GetDocument)
	mux.HandleFunc("PUT /api/v1/documents/{id}", handler.UpdateDocument)
	mux.HandleFunc("DELETE /api/v1/documents/{id}", handler.DeleteDocument)
	mux.HandleFunc("POST /api/v1/documents/{id}/process", handler.ProcessDocument)
	mux.HandleFunc("GET /api/v1/documents/{id}/processing-events", handler.GetDocumentProcessingEvents)
	mux.HandleFunc("GET /api/v1/documents/{id}/observability", handler.GetDocumentObservability)

	mux.HandleFunc("POST /api/v1/rag/query", handler.RAGQuery)
	mux.HandleFunc("POST /api/v1/query/analyze", handler.AnalyzeQueryIntent)
	mux.HandleFunc("GET /api/v1/documents/{id}/toc", handler.GetDocumentTOC)

	staticDir := cfg.App.StaticDir
	if staticDir == "" {
		staticDir = "./static"
	}

	if info, err := os.Stat(staticDir); err == nil && info.IsDir() {
		fmt.Printf("Serving frontend static files from: %s\n", staticDir)
		mux.Handle("/", spaHandler(staticDir))
	} else {
		fmt.Printf("Static directory not found (%s), skipping frontend serving\n", staticDir)
	}

	return middleware.Chain(mux,
		middleware.Recovery(),
		middleware.Logging(),
		middleware.CORS(),
		middleware.Tenant(),
	)
}

func spaHandler(staticDir string) http.Handler {
	absDir, _ := filepath.Abs(staticDir)
	fileServer := http.FileServer(http.Dir(absDir))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}

		fullPath := filepath.Join(absDir, filepath.Clean(path))
		if !strings.HasPrefix(fullPath, absDir) {
			http.NotFound(w, r)
			return
		}

		if _, err := fs.Stat(os.DirFS(absDir), strings.TrimPrefix(filepath.Clean(path), "/")); err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			http.ServeFile(w, r, filepath.Join(absDir, "index.html"))
			return
		}

		if strings.HasPrefix(path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}

		fileServer.ServeHTTP(w, r)
	})
}
