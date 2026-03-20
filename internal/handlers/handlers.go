package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jizhuozhi/knowledge/internal/config"
	"github.com/jizhuozhi/knowledge/internal/database"
	"github.com/jizhuozhi/knowledge/internal/middleware"
	"github.com/jizhuozhi/knowledge/internal/models"
	"github.com/jizhuozhi/knowledge/internal/services"
	"gorm.io/gorm"
)

type Handler struct {
	config       *config.Config
	docService   *services.DocumentService
	ragService   *services.RAGService
	ragServiceV2 *services.RAGServiceV2
}

func NewHandler(cfg *config.Config, docService *services.DocumentService, ragService *services.RAGService, ragServiceV2 *services.RAGServiceV2) *Handler {
	return &Handler{
		config:       cfg,
		docService:   docService,
		ragService:   ragService,
		ragServiceV2: ragServiceV2,
	}
}

// parseIDParam extracts a string ID from URL path value
func parseIDParam(r *http.Request, name string) (string, error) {
	s := r.PathValue(name)
	if s == "" {
		return "", fmt.Errorf("missing path parameter: %s", name)
	}
	return s, nil
}

// ===========================================
// Health Check
// ===========================================

func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	middleware.JSONResponse(w, http.StatusOK, map[string]interface{}{
		"status":  "healthy",
		"version": h.config.App.Version,
	})
}

// ===========================================
// Tenant Management
// ===========================================

func (h *Handler) ListTenants(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	page, _ := strconv.Atoi(query.Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(query.Get("page_size"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	var tenants []models.Tenant
	var total int64

	db := database.DB.Model(&models.Tenant{})
	if keyword := query.Get("keyword"); keyword != "" {
		db = db.Where("name ILIKE ? OR code ILIKE ?", "%"+keyword+"%", "%"+keyword+"%")
	}
	if status := query.Get("status"); status != "" {
		db = db.Where("status = ?", status)
	}

	db.Count(&total)
	if err := db.Offset((page - 1) * pageSize).Limit(pageSize).Find(&tenants).Error; err != nil {
		middleware.JSONError(w, http.StatusInternalServerError, "failed to list tenants: "+err.Error())
		return
	}

	middleware.JSONResponse(w, http.StatusOK, map[string]interface{}{
		"data":      tenants,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

func (h *Handler) CreateTenant(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Code        string `json:"code"`
		Description string `json:"description"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.JSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" || req.Code == "" {
		middleware.JSONError(w, http.StatusBadRequest, "name and code are required")
		return
	}

	tenant := models.Tenant{
		Name:        req.Name,
		Code:        req.Code,
		Description: req.Description,
		Status:      "active",
	}

	if err := database.DB.Create(&tenant).Error; err != nil {
		middleware.JSONError(w, http.StatusInternalServerError, "failed to create tenant: "+err.Error())
		return
	}

	middleware.JSONResponse(w, http.StatusCreated, tenant)
}

func (h *Handler) GetTenant(w http.ResponseWriter, r *http.Request) {
	tenantID, err := parseIDParam(r, "id")
	if err != nil {
		middleware.JSONError(w, http.StatusBadRequest, "invalid tenant id")
		return
	}

	var tenant models.Tenant
	if err := database.DB.First(&tenant, tenantID).Error; err != nil {
		middleware.JSONError(w, http.StatusNotFound, "tenant not found")
		return
	}

	middleware.JSONResponse(w, http.StatusOK, tenant)
}

func (h *Handler) UpdateTenant(w http.ResponseWriter, r *http.Request) {
	tenantID, err := parseIDParam(r, "id")
	if err != nil {
		middleware.JSONError(w, http.StatusBadRequest, "invalid tenant id")
		return
	}

	var tenant models.Tenant
	if err := database.DB.First(&tenant, tenantID).Error; err != nil {
		middleware.JSONError(w, http.StatusNotFound, "tenant not found")
		return
	}

	var req struct {
		Name        string `json:"name"`
		Code        string `json:"code"`
		Description string `json:"description"`
		Status      string `json:"status"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.JSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	updates := make(map[string]interface{})
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.Code != "" {
		updates["code"] = req.Code
	}
	if req.Description != "" {
		updates["description"] = req.Description
	}
	if req.Status != "" {
		updates["status"] = req.Status
	}

	if err := database.DB.Model(&tenant).Updates(updates).Error; err != nil {
		middleware.JSONError(w, http.StatusInternalServerError, "failed to update tenant: "+err.Error())
		return
	}

	database.DB.First(&tenant, tenantID)
	middleware.JSONResponse(w, http.StatusOK, tenant)
}

func (h *Handler) DeleteTenant(w http.ResponseWriter, r *http.Request) {
	tenantID, err := parseIDParam(r, "id")
	if err != nil {
		middleware.JSONError(w, http.StatusBadRequest, "invalid tenant id")
		return
	}

	if err := database.DB.Delete(&models.Tenant{}, tenantID).Error; err != nil {
		middleware.JSONError(w, http.StatusInternalServerError, "failed to delete tenant: "+err.Error())
		return
	}

	middleware.JSONResponse(w, http.StatusOK, map[string]string{"message": "tenant deleted"})
}

// ===========================================
// Knowledge Base Management
// ===========================================

func (h *Handler) ListKnowledgeBases(w http.ResponseWriter, r *http.Request) {
	tenantID := middleware.GetTenantID(r.Context())
	query := r.URL.Query()
	page, _ := strconv.Atoi(query.Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(query.Get("page_size"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	var kbs []models.KnowledgeBase
	var total int64

	db := database.DB.Model(&models.KnowledgeBase{}).Where("tenant_id = ?", tenantID)
	if keyword := query.Get("keyword"); keyword != "" {
		db = db.Where("name ILIKE ?", "%"+keyword+"%")
	}
	if status := query.Get("status"); status != "" {
		db = db.Where("status = ?", status)
	}

	if err := db.Count(&total).Error; err != nil {
		middleware.JSONError(w, http.StatusInternalServerError, "failed to count knowledge bases: "+err.Error())
		return
	}
	if err := db.Offset((page - 1) * pageSize).Limit(pageSize).Find(&kbs).Error; err != nil {
		middleware.JSONError(w, http.StatusInternalServerError, "failed to list knowledge bases: "+err.Error())
		return
	}

	middleware.JSONResponse(w, http.StatusOK, map[string]interface{}{
		"data":      kbs,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

func (h *Handler) CreateKnowledgeBase(w http.ResponseWriter, r *http.Request) {
	tenantID := middleware.GetTenantID(r.Context())

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.JSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		middleware.JSONError(w, http.StatusBadRequest, "name is required")
		return
	}

	kb := models.KnowledgeBase{
		TenantModel: models.TenantModel{
			TenantID: tenantID,
		},
		Name:        req.Name,
		Description: req.Description,
		Status:      "active",
	}

	if err := database.DB.Create(&kb).Error; err != nil {
		middleware.JSONError(w, http.StatusInternalServerError, "failed to create knowledge base: "+err.Error())
		return
	}

	// Create OpenSearch indices for this knowledge base
	if err := h.docService.EnsureKBIndices(kb.ID); err != nil {
		fmt.Printf("WARNING: failed to create OpenSearch indices for KB %s: %v\n", kb.ID, err)
	}

	middleware.JSONResponse(w, http.StatusCreated, kb)
}

func (h *Handler) GetKnowledgeBase(w http.ResponseWriter, r *http.Request) {
	tenantID := middleware.GetTenantID(r.Context())

	kbID, err := parseIDParam(r, "id")
	if err != nil {
		middleware.JSONError(w, http.StatusBadRequest, "invalid knowledge base id")
		return
	}

	var kb models.KnowledgeBase
	if err := database.DB.Where("id = ? AND tenant_id = ?", kbID, tenantID).First(&kb).Error; err != nil {
		middleware.JSONError(w, http.StatusNotFound, "knowledge base not found")
		return
	}

	middleware.JSONResponse(w, http.StatusOK, kb)
}

func (h *Handler) GetKnowledgeBaseObservability(w http.ResponseWriter, r *http.Request) {
	tenantID := middleware.GetTenantID(r.Context())

	kbID, err := parseIDParam(r, "id")
	if err != nil {
		middleware.JSONError(w, http.StatusBadRequest, "invalid knowledge base id")
		return
	}

	limit := 20
	if rawLimit := r.URL.Query().Get("limit"); strings.TrimSpace(rawLimit) != "" {
		parsed, parseErr := strconv.Atoi(rawLimit)
		if parseErr != nil {
			middleware.JSONError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = parsed
	}

	obs, err := h.docService.GetKnowledgeBaseObservability(r.Context(), tenantID, kbID, limit)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			middleware.JSONError(w, http.StatusNotFound, err.Error())
			return
		}
		middleware.JSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	middleware.JSONResponse(w, http.StatusOK, obs)
}

func (h *Handler) UpdateKnowledgeBase(w http.ResponseWriter, r *http.Request) {
	tenantID := middleware.GetTenantID(r.Context())

	kbID, err := parseIDParam(r, "id")
	if err != nil {
		middleware.JSONError(w, http.StatusBadRequest, "invalid knowledge base id")
		return
	}

	var kb models.KnowledgeBase
	if err := database.DB.Where("id = ? AND tenant_id = ?", kbID, tenantID).First(&kb).Error; err != nil {
		middleware.JSONError(w, http.StatusNotFound, "knowledge base not found")
		return
	}

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Status      string `json:"status"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.JSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	updates := make(map[string]interface{})
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.Description != "" {
		updates["description"] = req.Description
	}
	if req.Status != "" {
		updates["status"] = req.Status
	}

	if err := database.DB.Model(&kb).Updates(updates).Error; err != nil {
		middleware.JSONError(w, http.StatusInternalServerError, "failed to update knowledge base: "+err.Error())
		return
	}

	database.DB.Where("id = ? AND tenant_id = ?", kbID, tenantID).First(&kb)
	middleware.JSONResponse(w, http.StatusOK, kb)
}

func (h *Handler) DeleteKnowledgeBase(w http.ResponseWriter, r *http.Request) {
	tenantID := middleware.GetTenantID(r.Context())

	kbID, err := parseIDParam(r, "id")
	if err != nil {
		middleware.JSONError(w, http.StatusBadRequest, "invalid knowledge base id")
		return
	}

	result := database.DB.Where("id = ? AND tenant_id = ?", kbID, tenantID).Delete(&models.KnowledgeBase{})
	if result.Error != nil {
		middleware.JSONError(w, http.StatusInternalServerError, "failed to delete knowledge base: "+result.Error.Error())
		return
	}
	if result.RowsAffected == 0 {
		middleware.JSONError(w, http.StatusNotFound, "knowledge base not found")
		return
	}

	middleware.JSONResponse(w, http.StatusOK, map[string]string{"message": "knowledge base deleted"})
}

// ===========================================
// Document Management
// ===========================================

func (h *Handler) ListDocuments(w http.ResponseWriter, r *http.Request) {
	tenantID := middleware.GetTenantID(r.Context())

	query := r.URL.Query()
	filter := &services.DocumentFilter{
		Keyword:  query.Get("keyword"),
		DocType:  query.Get("doc_type"),
		Status:   query.Get("status"),
		Page:     1,
		PageSize: 20,
	}

	if kbID := query.Get("knowledge_base_id"); kbID != "" {
		filter.KnowledgeBaseID = kbID
	}
	if page := query.Get("page"); page != "" {
		filter.Page, _ = strconv.Atoi(page)
	}
	if pageSize := query.Get("page_size"); pageSize != "" {
		filter.PageSize, _ = strconv.Atoi(pageSize)
	}

	docs, total, err := h.docService.ListDocuments(r.Context(), tenantID, filter)
	if err != nil {
		middleware.JSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	middleware.JSONResponse(w, http.StatusOK, map[string]interface{}{
		"data":      docs,
		"total":     total,
		"page":      filter.Page,
		"page_size": filter.PageSize,
	})
}

func (h *Handler) CreateDocument(w http.ResponseWriter, r *http.Request) {
	tenantID := middleware.GetTenantID(r.Context())

	var req struct {
		Title           string                 `json:"title"`
		Content         string                 `json:"content"`
		KnowledgeBaseID string                 `json:"knowledge_base_id"`
		DocType         string                 `json:"doc_type"`
		Format          string                 `json:"format"`
		FilePath        string                 `json:"file_path"`
		FileSize        int64                  `json:"file_size"`
		Metadata        map[string]interface{} `json:"metadata"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.JSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Title == "" {
		middleware.JSONError(w, http.StatusBadRequest, "title is required")
		return
	}

	docReq := &services.CreateDocumentRequest{
		Title:           req.Title,
		Content:         req.Content,
		KnowledgeBaseID: req.KnowledgeBaseID,
		DocType:         req.DocType,
		Format:          req.Format,
		FilePath:        req.FilePath,
		FileSize:        req.FileSize,
		Metadata:        req.Metadata,
	}

	doc, err := h.docService.CreateDocument(r.Context(), tenantID, docReq)
	if err != nil {
		middleware.JSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	middleware.JSONResponse(w, http.StatusCreated, doc)
}

func (h *Handler) GetDocument(w http.ResponseWriter, r *http.Request) {
	tenantID := middleware.GetTenantID(r.Context())

	docID, err := parseIDParam(r, "id")
	if err != nil {
		middleware.JSONError(w, http.StatusBadRequest, "invalid document id")
		return
	}

	doc, err := h.docService.GetDocument(r.Context(), tenantID, docID)
	if err != nil {
		middleware.JSONError(w, http.StatusNotFound, "document not found")
		return
	}

	middleware.JSONResponse(w, http.StatusOK, doc)
}

func (h *Handler) GetDocumentObservability(w http.ResponseWriter, r *http.Request) {
	tenantID := middleware.GetTenantID(r.Context())

	docID, err := parseIDParam(r, "id")
	if err != nil {
		middleware.JSONError(w, http.StatusBadRequest, "invalid document id")
		return
	}

	obs, err := h.docService.GetDocumentObservability(r.Context(), tenantID, docID)
	if err != nil {
		middleware.JSONError(w, http.StatusNotFound, err.Error())
		return
	}

	middleware.JSONResponse(w, http.StatusOK, obs)
}

func (h *Handler) UpdateDocument(w http.ResponseWriter, r *http.Request) {
	tenantID := middleware.GetTenantID(r.Context())

	docID, err := parseIDParam(r, "id")
	if err != nil {
		middleware.JSONError(w, http.StatusBadRequest, "invalid document id")
		return
	}

	var doc models.Document
	if err := database.DB.Where("id = ? AND tenant_id = ?", docID, tenantID).First(&doc).Error; err != nil {
		middleware.JSONError(w, http.StatusNotFound, "document not found")
		return
	}

	var req struct {
		Title   string `json:"title"`
		Content string `json:"content"`
		DocType string `json:"doc_type"`
		Status  string `json:"status"`
		Summary string `json:"summary"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.JSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	updates := make(map[string]interface{})
	if req.Title != "" {
		updates["title"] = req.Title
	}
	if req.Content != "" {
		updates["content"] = req.Content
	}
	if req.DocType != "" {
		updates["doc_type"] = req.DocType
	}
	if req.Status != "" {
		updates["status"] = req.Status
	}
	if req.Summary != "" {
		updates["summary"] = req.Summary
	}

	if err := database.DB.Model(&doc).Updates(updates).Error; err != nil {
		middleware.JSONError(w, http.StatusInternalServerError, "failed to update document: "+err.Error())
		return
	}

	database.DB.Where("id = ? AND tenant_id = ?", docID, tenantID).First(&doc)
	middleware.JSONResponse(w, http.StatusOK, doc)
}

func (h *Handler) DeleteDocument(w http.ResponseWriter, r *http.Request) {
	tenantID := middleware.GetTenantID(r.Context())

	docID, err := parseIDParam(r, "id")
	if err != nil {
		middleware.JSONError(w, http.StatusBadRequest, "invalid document id")
		return
	}

	if err := h.docService.DeleteDocument(r.Context(), tenantID, docID); err != nil {
		middleware.JSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	middleware.JSONResponse(w, http.StatusOK, map[string]string{"message": "document deleted"})
}

func (h *Handler) ProcessDocument(w http.ResponseWriter, r *http.Request) {
	tenantID := middleware.GetTenantID(r.Context())

	docID, err := parseIDParam(r, "id")
	if err != nil {
		middleware.JSONError(w, http.StatusBadRequest, "invalid document id")
		return
	}

	_, err = h.docService.GetDocument(r.Context(), tenantID, docID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			middleware.JSONError(w, http.StatusNotFound, "document not found")
			return
		}
		middleware.JSONError(w, http.StatusInternalServerError, "failed to query document: "+err.Error())
		return
	}

	go func(documentID string) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("document processing panic: doc_id=%s panic=%v", documentID, rec)
			}
		}()
		if err := h.docService.ProcessDocument(context.Background(), documentID); err != nil {
			log.Printf("document processing failed: doc_id=%s err=%v", documentID, err)
		}
	}(docID)

	middleware.JSONResponse(w, http.StatusAccepted, map[string]interface{}{
		"message":     "document processing started",
		"document_id": docID,
	})
}

func (h *Handler) UploadDocument(w http.ResponseWriter, r *http.Request) {
	tenantID := middleware.GetTenantID(r.Context())

	maxFileSize := h.config.Document.MaxFileSize
	if maxFileSize <= 0 {
		maxFileSize = 10 * 1024 * 1024
	}

	contentType := r.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "multipart/form-data" {
		middleware.JSONError(w, http.StatusUnsupportedMediaType, "content-type must be multipart/form-data")
		return
	}
	if strings.TrimSpace(params["boundary"]) == "" {
		middleware.JSONError(w, http.StatusBadRequest, "missing multipart boundary")
		return
	}

	requestBodyLimit := maxFileSize + (1 << 20)
	r.Body = http.MaxBytesReader(w, r.Body, requestBodyLimit)
	const maxMultipartMemory = 32 << 20
	if err := r.ParseMultipartForm(maxMultipartMemory); err != nil {
		var maxBytesErr *http.MaxBytesError
		switch {
		case errors.As(err, &maxBytesErr), strings.Contains(err.Error(), "http: request body too large"), errors.Is(err, multipart.ErrMessageTooLarge):
			middleware.JSONError(w, http.StatusRequestEntityTooLarge, "file too large")
		default:
			middleware.JSONError(w, http.StatusBadRequest, "invalid multipart form data")
		}
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		if errors.Is(err, http.ErrMissingFile) {
			middleware.JSONError(w, http.StatusBadRequest, "file field is required")
			return
		}
		middleware.JSONError(w, http.StatusBadRequest, "invalid file field")
		return
	}
	defer file.Close()

	if header.Size > maxFileSize {
		middleware.JSONError(w, http.StatusRequestEntityTooLarge, "file too large")
		return
	}

	fileBytes, err := io.ReadAll(file)
	if err != nil {
		middleware.JSONError(w, http.StatusInternalServerError, "failed to read uploaded file")
		return
	}
	if int64(len(fileBytes)) > maxFileSize {
		middleware.JSONError(w, http.StatusRequestEntityTooLarge, "file too large")
		return
	}

	metadata := map[string]interface{}{}
	if metadataRaw := r.FormValue("metadata"); strings.TrimSpace(metadataRaw) != "" {
		if err := json.Unmarshal([]byte(metadataRaw), &metadata); err != nil {
			middleware.JSONError(w, http.StatusBadRequest, "invalid metadata json")
			return
		}
	}

	// Parse knowledge_base_id from metadata — supports both string and number
	var kbID string
	switch v := metadata["knowledge_base_id"].(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			middleware.JSONError(w, http.StatusBadRequest, "metadata.knowledge_base_id is required")
			return
		}
		kbID = v
	case float64:
		kbID = fmt.Sprintf("%.0f", v)
	case nil:
		middleware.JSONError(w, http.StatusBadRequest, "metadata.knowledge_base_id is required")
		return
	default:
		middleware.JSONError(w, http.StatusBadRequest, "invalid metadata.knowledge_base_id type")
		return
	}

	uploadDir := h.config.Document.UploadDir
	if strings.TrimSpace(uploadDir) == "" {
		uploadDir = "uploads"
	}
	tenantKBDir := filepath.Join(uploadDir, tenantID, kbID)
	rawDir := filepath.Join(tenantKBDir, "raw")
	parsedDir := filepath.Join(tenantKBDir, "parsed")
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
		middleware.JSONError(w, http.StatusInternalServerError, "failed to prepare raw upload directory")
		return
	}
	if err := os.MkdirAll(parsedDir, 0o755); err != nil {
		middleware.JSONError(w, http.StatusInternalServerError, "failed to prepare parsed document directory")
		return
	}

	ext := strings.ToLower(filepath.Ext(header.Filename))
	storedName := fmt.Sprintf("%d%s", time.Now().UnixNano(), ext)
	storedPath := filepath.Join(rawDir, storedName)
	if err := os.WriteFile(storedPath, fileBytes, 0o644); err != nil {
		middleware.JSONError(w, http.StatusInternalServerError, "failed to save raw file")
		return
	}

	metadata["original_filename"] = header.Filename
	metadata["original_extension"] = ext
	metadata["upload_content_type"] = header.Header.Get("Content-Type")

	format := detectFormatFromFilename(header.Filename)
	content, parseErr := h.docService.ParseDocument(fileBytes, format)
	if parseErr != nil {
		content = string(fileBytes)
		metadata["parse_warning"] = parseErr.Error()
	}

	parsedName := fmt.Sprintf("%d.md", time.Now().UnixNano())
	parsedPath := filepath.Join(parsedDir, parsedName)
	if err := os.WriteFile(parsedPath, []byte(content), 0o644); err != nil {
		middleware.JSONError(w, http.StatusInternalServerError, "failed to save parsed document")
		return
	}
	metadata["raw_file_path"] = storedPath
	metadata["parsed_doc_path"] = parsedPath

	title := strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))
	if strings.TrimSpace(title) == "" {
		title = header.Filename
	}

	req := &services.CreateDocumentRequest{
		Title:           title,
		Content:         content,
		KnowledgeBaseID: kbID,
		Format:          format,
		FilePath:        storedPath,
		FileSize:        int64(len(fileBytes)),
		Metadata:        models.JSONB(metadata),
	}

	doc, err := h.docService.CreateDocument(r.Context(), tenantID, req)
	if err != nil {
		middleware.JSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	middleware.JSONResponse(w, http.StatusCreated, doc)
}

func (h *Handler) GetDocumentProcessingEvents(w http.ResponseWriter, r *http.Request) {
	tenantID := middleware.GetTenantID(r.Context())

	docID, err := parseIDParam(r, "id")
	if err != nil {
		middleware.JSONError(w, http.StatusBadRequest, "invalid document id")
		return
	}

	events, err := h.docService.ListProcessingEvents(r.Context(), tenantID, docID)
	if err != nil {
		middleware.JSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	middleware.JSONResponse(w, http.StatusOK, map[string]interface{}{
		"data":  events,
		"total": len(events),
	})
}

// ===========================================
// RAG Query
// ===========================================

func (h *Handler) RAGQuery(w http.ResponseWriter, r *http.Request) {
	tenantID := middleware.GetTenantID(r.Context())

	var req services.RAGRequestV2
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.JSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Query == "" {
		middleware.JSONError(w, http.StatusBadRequest, "query is required")
		return
	}

	// Priority: Header > Body (for backward compatibility)
	kbID := middleware.GetKnowledgeBaseID(r.Context())
	if kbID == nil && req.KnowledgeBaseID != nil {
		kbID = req.KnowledgeBaseID
	}
	req.KnowledgeBaseID = kbID

	// Use RAGServiceV2 for enhanced query processing with hybrid intent support
	response, err := h.ragServiceV2.Query(r.Context(), tenantID, &req)
	if err != nil {
		middleware.JSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	middleware.JSONResponse(w, http.StatusOK, response)
}

// ===========================================
// Query Analysis & Document TOC
// ===========================================

// AnalyzeQueryIntent analyzes user query intent (standalone endpoint for debugging)
func (h *Handler) AnalyzeQueryIntent(w http.ResponseWriter, r *http.Request) {
	tenantID := middleware.GetTenantID(r.Context())

	var req struct {
		Query string `json:"query"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.JSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Query == "" {
		middleware.JSONError(w, http.StatusBadRequest, "query is required")
		return
	}

	// Parse tenantID to int64
	tenantIDInt, err := strconv.ParseInt(tenantID, 10, 64)
	if err != nil {
		middleware.JSONError(w, http.StatusBadRequest, "invalid tenant_id")
		return
	}

	// Create QueryAnalyzerService
	analyzerService := services.NewQueryAnalyzerService(database.DB, h.config)

	result, err := analyzerService.AnalyzeIntent(r.Context(), req.Query, tenantIDInt)
	if err != nil {
		middleware.JSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	middleware.JSONResponse(w, http.StatusOK, result)
}

// GetDocumentTOC returns the table of contents for a document
func (h *Handler) GetDocumentTOC(w http.ResponseWriter, r *http.Request) {
	tenantID := middleware.GetTenantID(r.Context())

	docID, err := parseIDParam(r, "id")
	if err != nil {
		middleware.JSONError(w, http.StatusBadRequest, "invalid document id")
		return
	}

	// Get document with TOC
	var doc models.Document
	if err := database.DB.Where("id = ? AND tenant_id = ?", docID, tenantID).First(&doc).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			middleware.JSONError(w, http.StatusNotFound, "document not found")
			return
		}
		middleware.JSONError(w, http.StatusInternalServerError, "failed to query document: "+err.Error())
		return
	}

	// Extract TOC structure
	var tocNodes []models.TOCNode
	if doc.TOCStructure != nil {
		if nodesData, ok := doc.TOCStructure["nodes"]; ok {
			tocJSON, err := json.Marshal(nodesData)
			if err == nil {
				json.Unmarshal(tocJSON, &tocNodes)
			}
		}
	}

	middleware.JSONResponse(w, http.StatusOK, map[string]interface{}{
		"document_id":   doc.ID,
		"title":         doc.Title,
		"toc_structure": tocNodes,
	})
}

func detectFormatFromFilename(filename string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(filename)), ".")
	switch ext {
	case "md", "markdown":
		return "markdown"
	case "txt":
		return "txt"
	case "pdf":
		return "pdf"
	case "doc", "docx":
		return "docx"
	case "xls", "xlsx":
		return "xlsx"
	case "csv":
		return "csv"
	case "ppt", "pptx":
		return "pptx"
	case "html", "htm":
		return "html"
	default:
		return "txt"
	}
}
