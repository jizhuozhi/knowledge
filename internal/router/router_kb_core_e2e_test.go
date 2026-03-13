package router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jizhuozhi/knowledge/internal/config"
	"github.com/jizhuozhi/knowledge/internal/database"
	"github.com/jizhuozhi/knowledge/internal/handlers"
	"github.com/jizhuozhi/knowledge/internal/models"
	"github.com/jizhuozhi/knowledge/internal/services"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupCoreApp(t *testing.T) *httptest.Server {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "core-e2e.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}

	if err := db.AutoMigrate(&models.Tenant{}, &models.KnowledgeBase{}, &models.Document{}); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}

	database.DB = db

	cfg := &config.Config{}
	cfg.App.Version = "test-version"
	cfg.LLM.EmbeddingDimension = 1024

	docService := services.NewDocumentService(db, cfg)
	ragService := services.NewRAGService(db, cfg)
	h := handlers.NewHandler(cfg, docService, ragService)
	r := SetupRouter(cfg, h)
	return httptest.NewServer(r)
}

func doJSONRequest(t *testing.T, method, url string, tenantID string, payload any) (*http.Response, map[string]any) {
	t.Helper()
	var body []byte
	var err error
	if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload failed: %v", err)
		}
	}

	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if tenantID != "" {
		req.Header.Set("X-Tenant-ID", tenantID)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	defer resp.Body.Close()
	var data map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&data)
	return resp, data
}

func TestKnowledgeBaseCoreFlow_E2E(t *testing.T) {
	ts := setupCoreApp(t)
	defer ts.Close()

	tenantCode := fmt.Sprintf("tenant-%d", time.Now().UnixNano())
	resp, tenant := doJSONRequest(t, http.MethodPost, ts.URL+"/api/v1/admin/tenants", "", map[string]any{
		"name": fmt.Sprintf("Tenant %d", time.Now().UnixNano()),
		"code": tenantCode,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create tenant expected %d, got %d, body=%v", http.StatusCreated, resp.StatusCode, tenant)
	}
	tenantID, _ := tenant["id"].(string)
	if tenantID == "" {
		t.Fatalf("tenant id should not be empty")
	}

	resp, kb := doJSONRequest(t, http.MethodPost, ts.URL+"/api/v1/knowledge-bases", tenantID, map[string]any{
		"name":        "Core KB",
		"description": "core flow test",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create kb expected %d, got %d, body=%v", http.StatusCreated, resp.StatusCode, kb)
	}
	kbID, _ := kb["id"].(string)
	if kbID == "" {
		t.Fatalf("knowledge base id should not be empty")
	}

	resp, listKB := doJSONRequest(t, http.MethodGet, ts.URL+"/api/v1/knowledge-bases", tenantID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list kb expected %d, got %d, body=%v", http.StatusOK, resp.StatusCode, listKB)
	}
	if listKB["total"] == nil {
		t.Fatalf("list kb should return total")
	}
	if data, ok := listKB["data"].([]any); !ok || len(data) == 0 {
		t.Fatalf("list kb should return non-empty data, got=%v", listKB["data"])
	}

	resp, updatedKB := doJSONRequest(t, http.MethodPut, ts.URL+"/api/v1/knowledge-bases/"+kbID, tenantID, map[string]any{
		"name":   "Core KB Updated",
		"status": "inactive",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update kb expected %d, got %d, body=%v", http.StatusOK, resp.StatusCode, updatedKB)
	}
	if updatedKB["name"] != "Core KB Updated" {
		t.Fatalf("updated kb name mismatch: %v", updatedKB["name"])
	}

	resp, doc := doJSONRequest(t, http.MethodPost, ts.URL+"/api/v1/documents", tenantID, map[string]any{
		"title":             "Architecture Spec",
		"content":           "Knowledge base core content",
		"knowledge_base_id": kbID,
		"doc_type":          "knowledge",
		"format":            "markdown",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create document expected %d, got %d, body=%v", http.StatusCreated, resp.StatusCode, doc)
	}
	docID, _ := doc["id"].(string)
	if docID == "" {
		t.Fatalf("document id should not be empty")
	}

	resp, docList := doJSONRequest(t, http.MethodGet, ts.URL+"/api/v1/documents?knowledge_base_id="+kbID, tenantID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list docs expected %d, got %d, body=%v", http.StatusOK, resp.StatusCode, docList)
	}
	if data, ok := docList["data"].([]any); !ok || len(data) == 0 {
		t.Fatalf("list docs should return created document, got=%v", docList["data"])
	}

	resp, gotKB := doJSONRequest(t, http.MethodGet, ts.URL+"/api/v1/knowledge-bases/"+kbID, tenantID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get kb expected %d, got %d, body=%v", http.StatusOK, resp.StatusCode, gotKB)
	}

	resp, del := doJSONRequest(t, http.MethodDelete, ts.URL+"/api/v1/knowledge-bases/"+kbID, tenantID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete kb expected %d, got %d, body=%v", http.StatusOK, resp.StatusCode, del)
	}

	resp, notFound := doJSONRequest(t, http.MethodGet, ts.URL+"/api/v1/knowledge-bases/"+kbID, tenantID, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get deleted kb expected %d, got %d, body=%v", http.StatusNotFound, resp.StatusCode, notFound)
	}
}
