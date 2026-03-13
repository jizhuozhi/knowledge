package services

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jizhuozhi/knowledge/internal/config"
	"github.com/jizhuozhi/knowledge/internal/models"
	"github.com/jizhuozhi/knowledge/internal/neo4j"
	"github.com/jizhuozhi/knowledge/internal/opensearch"
	"github.com/xuri/excelize/v2"
	"golang.org/x/net/html"
	"gorm.io/gorm"
)

// DocumentService handles document processing
type DocumentService struct {
	db           *gorm.DB
	config       *config.Config
	chunkSrv     *ChunkService
	embedSrv     *EmbeddingService
	graphSrv     *GraphService
	opensearch   *opensearch.Client
	usageTracker *LLMUsageTracker
}

// NewDocumentService creates a new document service
func NewDocumentService(db *gorm.DB, cfg *config.Config) *DocumentService {
	return &DocumentService{
		db:           db,
		config:       cfg,
		chunkSrv:     NewChunkService(db, cfg),
		embedSrv:     NewEmbeddingService(cfg),
		graphSrv:     NewGraphService(db, cfg),
		usageTracker: NewLLMUsageTracker(db, cfg),
	}
}

// SetOpenSearchClient sets the OpenSearch client
func (s *DocumentService) SetOpenSearchClient(client *opensearch.Client) {
	s.opensearch = client
}

// SetNeo4jClient sets neo4j client for graph extraction
func (s *DocumentService) SetNeo4jClient(client *neo4j.Client) {
	s.graphSrv.SetNeo4jClient(client)
}

// EnsureKBIndices creates OpenSearch text and vector indices for a knowledge base if they don't exist
func (s *DocumentService) EnsureKBIndices(knowledgeBaseID string) error {
	if s.opensearch == nil {
		return fmt.Errorf("opensearch client not initialized")
	}
	return s.opensearch.CreateKBIndices(knowledgeBaseID, s.config.LLM.EmbeddingDimension)
}

// CreateDocument creates a new document
func (s *DocumentService) CreateDocument(ctx context.Context, tenantID string, req *CreateDocumentRequest) (*models.Document, error) {
	if req.Metadata == nil {
		req.Metadata = models.JSONB{}
	}

	if req.Format == "" {
		req.Format = "txt"
	}

	if req.DocType == "" {
		docType, reason := s.inferDocumentType(ctx, req.Title, req.Content, req.Format)
		req.DocType = docType
		req.Metadata["doc_type_inference_reason"] = reason
	}

	if req.KnowledgeBaseID == "" {
		return nil, fmt.Errorf("knowledge_base_id is required")
	}

	var kb models.KnowledgeBase
	if err := s.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ?", req.KnowledgeBaseID, tenantID).
		First(&kb).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("knowledge base not found")
		}
		return nil, fmt.Errorf("failed to verify knowledge base: %w", err)
	}

	doc := &models.Document{
		TenantModel: models.TenantModel{
			TenantID: tenantID,
		},
		KnowledgeBaseID: req.KnowledgeBaseID,
		Title:           req.Title,
		Content:         req.Content,
		DocType:         req.DocType,
		Format:          req.Format,
		FilePath:        req.FilePath,
		FileSize:        req.FileSize,
		Status:          "draft",
		Metadata:        req.Metadata,
	}

	if err := s.db.Create(doc).Error; err != nil {
		return nil, fmt.Errorf("failed to create document: %w", err)
	}

	return doc, nil
}

// ProcessDocument processes a document for indexing
// Uses full replacement strategy: deletes all old indices before re-indexing
func (s *DocumentService) ProcessDocument(ctx context.Context, docID string) (err error) {
	var doc models.Document
	if err = s.db.First(&doc, "id = ?", docID).Error; err != nil {
		return fmt.Errorf("document not found: %w", err)
	}

	step := 1
	logStep := func(stage, status, message string, details models.JSONB) {
		_ = s.recordProcessingEvent(ctx, doc.TenantID, doc.ID, step, stage, status, message, details)
		step++
	}

	_ = s.db.Model(&models.Document{}).Where("id = ?", doc.ID).Update("status", "processing").Error
	logStep("start", "started", "开始构建索引", models.JSONB{"document_title": doc.Title, "format": doc.Format, "doc_type": doc.DocType})

	defer func() {
		if err != nil {
			_ = s.db.Model(&models.Document{}).Where("id = ?", doc.ID).Update("status", "failed").Error
			_ = s.recordProcessingEvent(ctx, doc.TenantID, doc.ID, step, "failed", "failed", err.Error(), nil)
		}
	}()

	// Full replacement: clean up all old indices before re-indexing
	if cleanupErr := s.cleanupDocumentIndices(ctx, &doc); cleanupErr != nil {
		logStep("cleanup", "warning", "清理旧索引失败，继续处理", models.JSONB{"error": cleanupErr.Error()})
	} else {
		logStep("cleanup", "success", "已清理旧索引数据", nil)
	}

	if doc.DocType == "" {
		docType, reason := s.inferDocumentType(ctx, doc.Title, doc.Content, doc.Format)
		doc.DocType = docType
		if doc.Metadata == nil {
			doc.Metadata = models.JSONB{}
		}
		doc.Metadata["doc_type_inference_reason"] = reason
		logStep("doc_type", "success", "自动识别文档类型", models.JSONB{"doc_type": docType, "reason": reason})
	}

	// 1. Extract document features and determine indexing strategy
	features := s.extractDocumentFeatures(&doc)
	logStep("features", "success", "文档特征抽取完成", models.JSONB{
		"content_length":     features.ContentLength,
		"line_count":         strings.Count(doc.Content, "\n") + 1,
		"has_code_blocks":    features.HasCodeBlocks,
		"has_tables":         features.HasTables,
		"has_steps":          features.HasSteps,
		"has_sections":       features.HasSections,
		"section_count_hint": strings.Count(doc.Content, "##"),
	})

	strategy, err := s.determineIndexStrategy(ctx, &doc)
	if err != nil {
		return fmt.Errorf("failed to determine index strategy: %w", err)
	}
	logStep("strategy", "success", "索引策略推理完成", models.JSONB{
		"chunk_strategy":     strategy.ChunkStrategy,
		"chunk_size":         strategy.ChunkSize,
		"enable_graph_index": strategy.EnableGraphIndex,
		"enable_ai_summary":  strategy.EnableAISummary,
		"special_processing": strategy.SpecialProcessing,
	})

	// 2. Generate semantic metadata
	semanticMeta, metaErr := s.extractSemanticMetadata(ctx, &doc)
	if metaErr != nil {
		logStep("semantic_metadata", "warning", "语义元数据提取失败，已降级继续", models.JSONB{"error": metaErr.Error()})
	} else {
		doc.SemanticMetadata = semanticMeta
		logStep("semantic_metadata", "success", "语义元数据提取完成", models.JSONB{
			"field_count":       len(semanticMeta),
			"semantic_metadata": semanticMeta,
		})
	}

	// 3. Chunk document
	chunks, err := s.chunkSrv.ChunkDocument(ctx, &doc, strategy)
	if err != nil {
		return fmt.Errorf("failed to chunk document: %w", err)
	}
	logStep("chunk", "success", "文档分块完成", models.JSONB{
		"chunk_count":    len(chunks),
		"chunk_strategy": strategy.ChunkStrategy,
		"chunk_size":     strategy.ChunkSize,
		"samples":        buildChunkSamples(chunks, 5, 240),
	})

	if s.opensearch == nil {
		return fmt.Errorf("opensearch client not initialized")
	}

	if err := s.opensearch.CreateKBIndices(doc.KnowledgeBaseID, s.config.LLM.EmbeddingDimension); err != nil {
		return fmt.Errorf("failed to ensure KB indices: %w", err)
	}

	texts := make([]string, len(chunks))
	for i := range chunks {
		texts[i] = chunks[i].Content
	}

	embeddings, embeddingUsage, err := s.embedSrv.GenerateEmbeddings(ctx, texts)
	if err != nil {
		return fmt.Errorf("failed to generate chunk embeddings: %w", err)
	}
	if len(embeddings) != len(chunks) {
		return fmt.Errorf("embedding size mismatch: chunks=%d embeddings=%d", len(chunks), len(embeddings))
	}
	embeddingTokens := 0
	if embeddingUsage != nil {
		embeddingTokens = embeddingUsage.InputTokens
	}
	logStep("embedding", "success", "向量生成完成", models.JSONB{"embedding_count": len(embeddings), "embedding_tokens": embeddingTokens})

	// Record embedding usage
	if s.usageTracker != nil {
		kbID := doc.KnowledgeBaseID
		s.usageTracker.RecordUsage(ctx, doc.TenantID, &doc.ID, &kbID,
			"document", "generateEmbeddings", s.config.LLM.EmbeddingModel, "embedding",
			embeddingUsage, 0, "")
	}

	docIDStr := doc.ID
	kbIDStr := doc.KnowledgeBaseID

	baseMeta := map[string]interface{}{
		"doc_type":          doc.DocType,
		"format":            doc.Format,
		"knowledge_base_id": kbIDStr,
	}
	for k, v := range doc.Metadata {
		baseMeta[k] = v
	}
	for k, v := range doc.SemanticMetadata {
		baseMeta["semantic_"+k] = v
	}

	indexedCount := 0
	for i := range chunks {
		chunkID := chunks[i].ID
		vectorID := "vec_" + chunkID

		if err := s.db.Model(&models.Chunk{}).Where("id = ?", chunks[i].ID).Update("vector_id", vectorID).Error; err != nil {
			return fmt.Errorf("failed to update chunk vector id: %w", err)
		}

		chunkMeta := map[string]interface{}{}
		for k, v := range baseMeta {
			chunkMeta[k] = v
		}
		chunkMeta["chunk_index"] = chunks[i].ChunkIndex
		chunkMeta["chunk_type"] = chunks[i].ChunkType

		// Text index: index individual chunk for granular text search
		textDoc := &opensearch.DocumentIndex{
			ID:              chunkID,
			DocumentID:      docIDStr,
			KnowledgeBaseID: kbIDStr,
			Title:           doc.Title,
			Content:         chunks[i].Content,
			DocType:         doc.DocType,
			Tags:            []string{doc.DocType, doc.Format, chunks[i].ChunkType},
			Metadata:        chunkMeta,
			CreatedAt:       doc.CreatedAt,
			UpdatedAt:       doc.UpdatedAt,
		}

		if err := s.opensearch.IndexDocument(ctx, doc.KnowledgeBaseID, textDoc); err != nil {
			return fmt.Errorf("failed to index text chunk: %w", err)
		}

		// Vector index: fine-grained chunk for embedding-based search
		vecDoc := &opensearch.VectorIndex{
			ID:              vectorID,
			DocumentID:      docIDStr,
			KnowledgeBaseID: kbIDStr,
			Title:           doc.Title,
			Content:         chunks[i].Content,
			Embedding:       embeddings[i],
		}

		if err := s.opensearch.IndexVector(ctx, doc.KnowledgeBaseID, vecDoc); err != nil {
			return fmt.Errorf("failed to index vector chunk: %w", err)
		}

		indexedCount++
	}

	// Text index: additionally index section-level large blocks for better BM25 recall.
	sectionTexts := s.buildSectionTexts(&doc)
	sectionIndexed := 0
	for i, section := range sectionTexts {
		sectionID := fmt.Sprintf("section_%s_%d", docIDStr, i)
		sectionMeta := map[string]interface{}{}
		for k, v := range baseMeta {
			sectionMeta[k] = v
		}
		sectionMeta["chunk_type"] = "section_full"
		sectionMeta["chunk_index"] = -1

		sectionDoc := &opensearch.DocumentIndex{
			ID:              sectionID,
			DocumentID:      docIDStr,
			KnowledgeBaseID: kbIDStr,
			Title:           doc.Title,
			Content:         section,
			DocType:         doc.DocType,
			Tags:            []string{doc.DocType, doc.Format, "section_full"},
			Metadata:        sectionMeta,
			CreatedAt:       doc.CreatedAt,
			UpdatedAt:       doc.UpdatedAt,
		}

		if err := s.opensearch.IndexDocument(ctx, doc.KnowledgeBaseID, sectionDoc); err != nil {
			fmt.Printf("Warning: failed to index section %d of doc %s: %v\n", i, doc.ID, err)
			continue
		}
		sectionIndexed++
	}

	logStep("index", "success", "OpenSearch索引写入完成", models.JSONB{
		"indexed_chunks":   indexedCount,
		"indexed_sections": sectionIndexed,
	})

	// 5. Extract entities and relations for graph
	if strategy.EnableGraphIndex {
		if err := s.graphSrv.ExtractAndIndex(ctx, &doc); err != nil {
			logStep("graph", "warning", "图索引构建失败，已降级继续", models.JSONB{"error": err.Error()})
		} else {
			logStep("graph", "success", "图索引构建完成", nil)
		}
	}

	// 6. Generate summary for long documents
	if strategy.EnableAISummary && len(doc.Content) > 5000 {
		summary, sumErr := s.generateSummary(ctx, &doc)
		if sumErr != nil {
			logStep("summary", "warning", "AI摘要生成失败，已降级继续", models.JSONB{"error": sumErr.Error()})
		} else {
			doc.Summary = summary
			logStep("summary", "success", "AI摘要生成完成", models.JSONB{
				"summary_length":  len(summary),
				"summary_preview": truncateForLLM(summary, 1200),
			})
		}
	}

	// Update document status
	doc.Status = "published"
	if err := s.db.Save(&doc).Error; err != nil {
		return fmt.Errorf("failed to update document: %w", err)
	}

	logStep("finish", "success", "索引构建完成", models.JSONB{"status": "published"})
	return nil
}

func (s *DocumentService) recordProcessingEvent(ctx context.Context, tenantID, docID string, step int, stage, status, message string, details models.JSONB) error {
	event := &models.ProcessingEvent{
		TenantModel: models.TenantModel{TenantID: tenantID},
		DocumentID:  docID,
		Step:        step,
		Stage:       stage,
		Status:      status,
		Message:     message,
		Details:     details,
	}
	return s.db.WithContext(ctx).Create(event).Error
}

// cleanupDocumentIndices removes all existing indices for a document before re-indexing
func (s *DocumentService) cleanupDocumentIndices(ctx context.Context, doc *models.Document) error {
	var errs []string

	// 1. Delete chunks from database
	if err := s.db.WithContext(ctx).Where("document_id = ? AND tenant_id = ?", doc.ID, doc.TenantID).Delete(&models.Chunk{}).Error; err != nil {
		errs = append(errs, "chunks: "+err.Error())
	}

	// 2. Delete from OpenSearch (text and vector indices)
	if s.opensearch != nil {
		if err := s.opensearch.DeleteDocument(ctx, doc.KnowledgeBaseID, doc.ID); err != nil {
			errs = append(errs, "opensearch: "+err.Error())
		}
	}

	// 3. Delete from Neo4j graph
	if s.graphSrv != nil && s.graphSrv.neo4jCli != nil {
		if err := s.graphSrv.neo4jCli.DeleteDocumentEntities(ctx, doc.KnowledgeBaseID, doc.ID); err != nil {
			errs = append(errs, "neo4j: "+err.Error())
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("partial cleanup errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

// ListProcessingEvents lists all indexing events for a document
func (s *DocumentService) ListProcessingEvents(ctx context.Context, tenantID, docID string) ([]models.ProcessingEvent, error) {
	var events []models.ProcessingEvent
	err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND document_id = ?", tenantID, docID).
		Order("step ASC, created_at ASC").
		Find(&events).Error
	return events, err
}

// buildSectionTexts splits a document into section-level text blocks for BM25 indexing.
// Unlike fine-grained chunks (500 chars) used for vector search, these sections are
// larger natural units (split by markdown headers) that give BM25 more context to match.
// For non-markdown or short documents, returns the full document as a single section.
func (s *DocumentService) buildSectionTexts(doc *models.Document) []string {
	content := doc.Content

	// Short documents: index as a single block
	const maxSingleSection = 8000
	if len(content) <= maxSingleSection {
		return []string{content}
	}

	// Split by markdown headers (##, ###)
	sectionRegex := regexp.MustCompile(`(?m)^(#{1,3}\s+.+)$`)
	headerLocs := sectionRegex.FindAllStringIndex(content, -1)

	if len(headerLocs) == 0 {
		// No markdown structure — split into ~3000 char blocks at paragraph boundaries
		return s.splitAtParagraphs(content, 3000)
	}

	var sections []string
	for i, loc := range headerLocs {
		start := loc[0]
		end := len(content)
		if i+1 < len(headerLocs) {
			end = headerLocs[i+1][0]
		}

		section := strings.TrimSpace(content[start:end])
		if len(section) < 50 {
			continue // Skip tiny sections
		}

		// If a section is extremely large, split at paragraph boundaries
		if len(section) > maxSingleSection {
			subSections := s.splitAtParagraphs(section, 3000)
			sections = append(sections, subSections...)
		} else {
			sections = append(sections, section)
		}
	}

	// If there's content before the first header, include it
	if len(headerLocs) > 0 && headerLocs[0][0] > 100 {
		preamble := strings.TrimSpace(content[:headerLocs[0][0]])
		if len(preamble) > 50 {
			sections = append([]string{preamble}, sections...)
		}
	}

	return sections
}

// splitAtParagraphs splits text into blocks of approximately targetSize chars,
// preferring to break at paragraph boundaries (double newlines).
func (s *DocumentService) splitAtParagraphs(text string, targetSize int) []string {
	paragraphs := strings.Split(text, "\n\n")
	var sections []string
	var current strings.Builder

	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if current.Len()+len(p) > targetSize && current.Len() > 0 {
			sections = append(sections, current.String())
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteString("\n\n")
		}
		current.WriteString(p)
	}
	if current.Len() > 0 {
		sections = append(sections, current.String())
	}
	return sections
}

func (s *DocumentService) inferDocumentType(ctx context.Context, title, content, format string) (string, string) {
	if len(strings.TrimSpace(content)) < 300 {
		return "brief", "内容较短，默认归类为 brief"
	}

	prompt := fmt.Sprintf(`请判断文档类型，候选值仅限：knowledge, process, data, brief, experience。

标题：%s
格式：%s
内容（前2000字符）：
%s

输出JSON：
{"doc_type":"knowledge|process|data|brief|experience","reason":"简短理由"}
只返回JSON。`, title, format, truncateForLLM(content, 2000))

	start := time.Now()
	resp, usage, err := s.embedSrv.ChatCompletion(ctx, prompt)
	durationMs := time.Since(start).Milliseconds()

	// Record usage regardless of success
	if s.usageTracker != nil {
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		}
		s.usageTracker.RecordUsage(ctx, "", nil, nil,
			"document", "inferDocumentType", s.config.LLM.ChatModel, "chat",
			usage, durationMs, errMsg)
	}

	if err == nil {
		var out struct {
			DocType string `json:"doc_type"`
			Reason  string `json:"reason"`
		}
		if json.Unmarshal([]byte(resp), &out) == nil {
			if isValidDocType(out.DocType) {
				reason := strings.TrimSpace(out.Reason)
				if reason == "" {
					reason = "LLM 自动识别"
				}
				return out.DocType, reason
			}
		}
	}

	lower := strings.ToLower(title + "\n" + content)
	switch {
	case strings.Contains(lower, "步骤") || strings.Contains(lower, "操作") || strings.Contains(lower, "排查"):
		return "process", "命中流程/步骤关键词"
	case strings.Contains(lower, "故障") || strings.Contains(lower, "复盘") || strings.Contains(lower, "经验"):
		return "experience", "命中经验/故障关键词"
	case strings.Contains(lower, "报表") || strings.Contains(lower, "指标") || strings.Contains(lower, "csv") || strings.Contains(lower, "xlsx"):
		return "data", "命中数据型关键词"
	default:
		return "knowledge", "回退默认类型 knowledge"
	}
}

func isValidDocType(v string) bool {
	switch strings.TrimSpace(v) {
	case "knowledge", "process", "data", "brief", "experience":
		return true
	default:
		return false
	}
}

func truncateForLLM(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func buildChunkSamples(chunks []models.Chunk, limit, previewLen int) []map[string]interface{} {
	if limit <= 0 || len(chunks) == 0 {
		return nil
	}
	if previewLen <= 0 {
		previewLen = 200
	}

	n := limit
	if len(chunks) < n {
		n = len(chunks)
	}

	samples := make([]map[string]interface{}, 0, n)
	for i := 0; i < n; i++ {
		chunk := chunks[i]
		samples = append(samples, map[string]interface{}{
			"chunk_index": chunk.ChunkIndex,
			"chunk_type":  chunk.ChunkType,
			"length":      len(chunk.Content),
			"preview":     truncateForLLM(chunk.Content, previewLen),
		})
	}
	return samples
}

// IndexStrategy represents the indexing strategy for a document
type IndexStrategy struct {
	ChunkStrategy     string `json:"chunk_strategy"` // semantic, section, sliding, parent_child
	ChunkSize         int    `json:"chunk_size"`
	EnableGraphIndex  bool   `json:"enable_graph_index"`
	EnableAISummary   bool   `json:"enable_ai_summary"`
	SpecialProcessing string `json:"special_processing"` // table_aware, brief_mode, experience_card
}

// determineIndexStrategy determines the indexing strategy using LLM
func (s *DocumentService) determineIndexStrategy(ctx context.Context, doc *models.Document) (*IndexStrategy, error) {
	// Extract document features
	features := s.extractDocumentFeatures(doc)

	// Use LLM to determine strategy
	prompt := fmt.Sprintf(`Analyze the following document and determine the best indexing strategy.

Document Features:
- Title: %s
- Content Length: %d characters
- Document Type: %s
- Format: %s
- Has Code Blocks: %v
- Has Tables: %v
- Has Steps: %v
- Has Multiple Sections: %v

Return a JSON object with:
{
  "chunk_strategy": "semantic" | "section" | "sliding" | "parent_child",
  "chunk_size": 200-800,
  "enable_graph_index": true | false,
  "enable_ai_summary": true | false,
  "special_processing": "" | "table_aware" | "brief_mode" | "experience_card"
}

Consider:
1. For technical docs with clear sections: use "section" chunking
2. For continuous content: use "semantic" chunking
3. For long docs (>5000 chars): enable AI summary
4. For docs with entities/relations: enable graph index
5. For docs with tables: use "table_aware"
6. For docs with experience/troubleshooting: use "experience_card"`,
		doc.Title, len(doc.Content), doc.DocType, doc.Format,
		features.HasCodeBlocks, features.HasTables, features.HasSteps, features.HasSections)

	response, usage, err := s.embedSrv.ChatCompletion(ctx, prompt)
	if err != nil {
		return s.getDefaultStrategy(doc), nil
	}

	// Record usage
	if s.usageTracker != nil {
		kbID := doc.KnowledgeBaseID
		s.usageTracker.RecordUsage(ctx, doc.TenantID, &doc.ID, &kbID,
			"document", "determineIndexStrategy", s.config.LLM.ChatModel, "chat",
			usage, 0, "")
	}

	var strategy IndexStrategy
	if err := json.Unmarshal([]byte(response), &strategy); err != nil {
		return s.getDefaultStrategy(doc), nil
	}

	if strategy.ChunkSize <= 0 {
		strategy.ChunkSize = s.config.Document.ChunkSize
	}

	return &strategy, nil
}

// DocumentFeatures represents extracted document features
type DocumentFeatures struct {
	ContentLength int
	HasCodeBlocks bool
	HasTables     bool
	HasSteps      bool
	HasSections   bool
}

// extractDocumentFeatures extracts features from document
func (s *DocumentService) extractDocumentFeatures(doc *models.Document) *DocumentFeatures {
	content := doc.Content

	return &DocumentFeatures{
		ContentLength: len(content),
		HasCodeBlocks: strings.Contains(content, "```") || strings.Contains(content, "func ") || strings.Contains(content, "class "),
		HasTables:     strings.Contains(content, "|") && strings.Count(content, "|") > 5,
		HasSteps:      strings.Contains(content, "1.") || strings.Contains(content, "- ") || strings.Contains(content, "* "),
		HasSections:   strings.Count(content, "##") > 1,
	}
}

// getDefaultStrategy returns default indexing strategy
func (s *DocumentService) getDefaultStrategy(doc *models.Document) *IndexStrategy {
	strategy := &IndexStrategy{
		ChunkStrategy:    "semantic",
		ChunkSize:        s.config.Document.ChunkSize,
		EnableGraphIndex: false,
		EnableAISummary:  false,
	}

	switch doc.DocType {
	case "knowledge":
		strategy.ChunkStrategy = "section"
		strategy.EnableGraphIndex = true
	case "process":
		strategy.ChunkStrategy = "section"
		strategy.EnableGraphIndex = true
	case "brief":
		strategy.ChunkStrategy = "none"
		strategy.SpecialProcessing = "brief_mode"
	case "experience":
		strategy.SpecialProcessing = "experience_card"
		strategy.EnableGraphIndex = true
	}

	if len(doc.Content) > 5000 {
		strategy.EnableAISummary = true
	}

	return strategy
}

// extractSemanticMetadata extracts semantic metadata using LLM
func (s *DocumentService) extractSemanticMetadata(ctx context.Context, doc *models.Document) (models.JSONB, error) {
	content := doc.Content
	if len(content) > 3000 {
		content = content[:3000]
	}

	prompt := fmt.Sprintf(`Extract structured metadata from the following document.
Return a JSON object with relevant fields. Do NOT predefine fields - extract what is meaningful from the content.

Document Title: %s
Document Type: %s
Content (first 3000 chars):
%s

Return only the JSON object, no other text.`, doc.Title, doc.DocType, content)

	response, usage, err := s.embedSrv.ChatCompletion(ctx, prompt)
	if err != nil {
		return nil, err
	}

	// Record usage
	if s.usageTracker != nil {
		kbID := doc.KnowledgeBaseID
		s.usageTracker.RecordUsage(ctx, doc.TenantID, &doc.ID, &kbID,
			"document", "extractSemanticMetadata", s.config.LLM.ChatModel, "chat",
			usage, 0, "")
	}

	var metadata models.JSONB
	if err := json.Unmarshal([]byte(response), &metadata); err != nil {
		return nil, err
	}

	return metadata, nil
}

// generateSummary generates a summary for long documents
func (s *DocumentService) generateSummary(ctx context.Context, doc *models.Document) (string, error) {
	content := doc.Content
	if len(content) > 10000 {
		content = content[:10000]
	}

	prompt := fmt.Sprintf(`Generate a concise summary (about 200 words) for the following document.

Title: %s
Content:
%s

Return only the summary text.`, doc.Title, content)

	result, usage, err := s.embedSrv.ChatCompletion(ctx, prompt)

	// Record usage
	if s.usageTracker != nil {
		kbID := doc.KnowledgeBaseID
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		}
		s.usageTracker.RecordUsage(ctx, doc.TenantID, &doc.ID, &kbID,
			"document", "generateSummary", s.config.LLM.ChatModel, "chat",
			usage, 0, errMsg)
	}

	return result, err
}

// GetDocument retrieves a document by ID
func (s *DocumentService) GetDocument(ctx context.Context, tenantID, docID string) (*models.Document, error) {
	var doc models.Document
	if err := s.db.Where("id = ? AND tenant_id = ?", docID, tenantID).First(&doc).Error; err != nil {
		return nil, err
	}
	return &doc, nil
}

// ListDocuments lists documents with filtering
func (s *DocumentService) ListDocuments(ctx context.Context, tenantID string, filter *DocumentFilter) ([]models.Document, int64, error) {
	var docs []models.Document
	var total int64

	query := s.db.Model(&models.Document{}).Where("tenant_id = ?", tenantID)

	if filter.KnowledgeBaseID != "" {
		query = query.Where("knowledge_base_id = ?", filter.KnowledgeBaseID)
	}
	if filter.DocType != "" {
		query = query.Where("doc_type = ?", filter.DocType)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.Keyword != "" {
		query = query.Where("title ILIKE ? OR content ILIKE ?", "%"+filter.Keyword+"%", "%"+filter.Keyword+"%")
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (filter.Page - 1) * filter.PageSize
	if err := query.Order("created_at DESC").Offset(offset).Limit(filter.PageSize).Find(&docs).Error; err != nil {
		return nil, 0, err
	}

	return docs, total, nil
}

// KnowledgeBaseObservability aggregates retrievable index/graph payloads for one knowledge base.
type KnowledgeBaseObservability struct {
	KnowledgeBaseID string                         `json:"knowledge_base_id"`
	Stats           map[string]int64               `json:"stats"`
	ChunkSamples    []ChunkSample                  `json:"chunk_samples"`
	TextIndex       []opensearch.TextIndexSample   `json:"text_index_samples"`
	VectorIndex     []opensearch.VectorIndexSample `json:"vector_index_samples"`
	GraphEntities   []GraphEntitySample            `json:"graph_entity_samples"`
	GraphRelations  []GraphRelationSample          `json:"graph_relation_samples"`
	LLMUsage        *KBLLMUsageSummary             `json:"llm_usage"`
	Warnings        []string                       `json:"warnings,omitempty"`
}

// KBLLMUsageSummary represents KB-level LLM usage summary
type KBLLMUsageSummary struct {
	TotalCalls        int64              `json:"total_calls"`
	TotalInputTokens  int64              `json:"total_input_tokens"`
	TotalOutputTokens int64              `json:"total_output_tokens"`
	TotalTokens       int64              `json:"total_tokens"`
	EstimatedCostUSD  float64            `json:"estimated_cost_usd"`
	ByService         []ServiceUsageStat `json:"by_service"`
	ByModelType       []ModelTypeUsage   `json:"by_model_type"`
	TopDocuments      []DocUsageStat     `json:"top_documents"`
}

// ServiceUsageStat represents usage grouped by service
type ServiceUsageStat struct {
	CallerService string  `json:"caller_service"`
	Calls         int64   `json:"calls"`
	InputTokens   int64   `json:"input_tokens"`
	OutputTokens  int64   `json:"output_tokens"`
	TotalTokens   int64   `json:"total_tokens"`
	CostUSD       float64 `json:"cost_usd"`
}

// DocUsageStat represents per-document usage
type DocUsageStat struct {
	DocumentID    string  `json:"document_id"`
	DocumentTitle string  `json:"document_title"`
	Calls         int64   `json:"calls"`
	TotalTokens   int64   `json:"total_tokens"`
	CostUSD       float64 `json:"cost_usd"`
}

type ChunkSample struct {
	ChunkID    string `json:"chunk_id"`
	DocumentID string `json:"document_id"`
	ChunkIndex int    `json:"chunk_index"`
	ChunkType  string `json:"chunk_type"`
	VectorID   string `json:"vector_id"`
	Content    string `json:"content"`
}

type GraphEntitySample struct {
	EntityID   string `json:"entity_id"`
	DocumentID string `json:"document_id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
}

type GraphRelationSample struct {
	RelationID       string  `json:"relation_id"`
	SourceEntityID   string  `json:"source_entity_id"`
	SourceName       string  `json:"source_name"`
	SourceType       string  `json:"source_type"`
	TargetEntityID   string  `json:"target_entity_id"`
	TargetName       string  `json:"target_name"`
	TargetType       string  `json:"target_type"`
	RelationType     string  `json:"relation_type"`
	Weight           float64 `json:"weight"`
	SourceDocumentID string  `json:"source_document_id"`
	TargetDocumentID string  `json:"target_document_id"`
}

// GetKnowledgeBaseObservability returns KB-scoped chunks/fulltext/vector/graph samples.
func (s *DocumentService) GetKnowledgeBaseObservability(ctx context.Context, tenantID, knowledgeBaseID string, limit int) (*KnowledgeBaseObservability, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	var kb models.KnowledgeBase
	if err := s.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", knowledgeBaseID, tenantID).First(&kb).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("knowledge base not found")
		}
		return nil, err
	}

	result := &KnowledgeBaseObservability{
		KnowledgeBaseID: knowledgeBaseID,
		Stats:           map[string]int64{},
	}

	var documentsCount int64
	if err := s.db.WithContext(ctx).Model(&models.Document{}).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, knowledgeBaseID).
		Count(&documentsCount).Error; err != nil {
		return nil, err
	}
	result.Stats["documents"] = documentsCount

	var chunksCount int64
	if err := s.db.WithContext(ctx).Model(&models.Chunk{}).
		Joins("JOIN documents d ON d.id = chunks.document_id").
		Where("chunks.tenant_id = ? AND d.tenant_id = ? AND d.knowledge_base_id = ?", tenantID, tenantID, knowledgeBaseID).
		Count(&chunksCount).Error; err != nil {
		return nil, err
	}
	result.Stats["chunks"] = chunksCount

	var graphEntitiesCount int64
	if err := s.db.WithContext(ctx).Model(&models.GraphEntity{}).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, knowledgeBaseID).
		Count(&graphEntitiesCount).Error; err != nil {
		return nil, err
	}
	result.Stats["graph_entities"] = graphEntitiesCount

	var graphRelationsCount int64
	if err := s.db.WithContext(ctx).Model(&models.GraphRelation{}).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, knowledgeBaseID).
		Count(&graphRelationsCount).Error; err != nil {
		return nil, err
	}
	result.Stats["graph_relations"] = graphRelationsCount

	if err := s.db.WithContext(ctx).
		Table("chunks c").
		Select("c.id AS chunk_id, c.document_id AS document_id, c.chunk_index, c.chunk_type, c.vector_id, LEFT(c.content, 400) AS content").
		Joins("JOIN documents d ON d.id = c.document_id").
		Where("c.tenant_id = ? AND d.tenant_id = ? AND d.knowledge_base_id = ?", tenantID, tenantID, knowledgeBaseID).
		Order("c.created_at DESC").
		Limit(limit).
		Scan(&result.ChunkSamples).Error; err != nil {
		return nil, err
	}

	if err := s.db.WithContext(ctx).
		Table("graph_entities ge").
		Select("ge.id AS entity_id, ge.document_id AS document_id, ge.name, ge.type").
		Where("ge.tenant_id = ? AND ge.knowledge_base_id = ?", tenantID, knowledgeBaseID).
		Order("ge.created_at DESC").
		Limit(limit).
		Scan(&result.GraphEntities).Error; err != nil {
		return nil, err
	}

	if err := s.db.WithContext(ctx).
		Table("graph_relations gr").
		Select("gr.id AS relation_id, gr.source_id AS source_entity_id, se.name AS source_name, se.type AS source_type, gr.target_id AS target_entity_id, te.name AS target_name, te.type AS target_type, gr.type AS relation_type, gr.weight, se.document_id AS source_document_id, te.document_id AS target_document_id").
		Joins("JOIN graph_entities se ON se.id = gr.source_id").
		Joins("JOIN graph_entities te ON te.id = gr.target_id").
		Where("gr.tenant_id = ? AND gr.knowledge_base_id = ?", tenantID, knowledgeBaseID).
		Order("gr.created_at DESC").
		Limit(limit).
		Scan(&result.GraphRelations).Error; err != nil {
		return nil, err
	}
	if len(result.GraphRelations) > 1 {
		sort.Slice(result.GraphRelations, func(i, j int) bool {
			return result.GraphRelations[i].Weight > result.GraphRelations[j].Weight
		})
	}

	if s.opensearch != nil {
		textSamples, err := s.opensearch.ListTextSamplesByKnowledgeBase(ctx, knowledgeBaseID, limit)
		if err != nil {
			result.Warnings = append(result.Warnings, "text index query failed: "+err.Error())
		} else {
			result.TextIndex = textSamples
			result.Stats["text_index_samples"] = int64(len(textSamples))
		}

		vectorSamples, err := s.opensearch.ListVectorSamplesByKnowledgeBase(ctx, knowledgeBaseID, limit)
		if err != nil {
			result.Warnings = append(result.Warnings, "vector index query failed: "+err.Error())
		} else {
			result.VectorIndex = vectorSamples
			result.Stats["vector_index_samples"] = int64(len(vectorSamples))
		}
	} else {
		result.Warnings = append(result.Warnings, "opensearch client not initialized")
	}

	// Get LLM usage for this knowledge base
	result.LLMUsage = s.getKBLLMUsage(ctx, tenantID, knowledgeBaseID)

	return result, nil
}

// getKBLLMUsage queries LLM usage for a knowledge base
func (s *DocumentService) getKBLLMUsage(ctx context.Context, tenantID, knowledgeBaseID string) *KBLLMUsageSummary {
	summary := &KBLLMUsageSummary{}

	// Aggregate totals
	var totalResult struct {
		Calls        int64   `json:"calls"`
		InputTokens  int64   `json:"input_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		TotalTokens  int64   `json:"total_tokens"`
		TotalCost    float64 `json:"total_cost"`
	}
	if err := s.db.WithContext(ctx).
		Model(&models.LLMUsageRecord{}).
		Select("COUNT(*) as calls, COALESCE(SUM(input_tokens),0) as input_tokens, COALESCE(SUM(output_tokens),0) as output_tokens, COALESCE(SUM(total_tokens),0) as total_tokens, COALESCE(SUM(estimated_cost),0) as total_cost").
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, knowledgeBaseID).
		Scan(&totalResult).Error; err == nil {
		summary.TotalCalls = totalResult.Calls
		summary.TotalInputTokens = totalResult.InputTokens
		summary.TotalOutputTokens = totalResult.OutputTokens
		summary.TotalTokens = totalResult.TotalTokens
		summary.EstimatedCostUSD = totalResult.TotalCost
	}

	// By service
	var serviceStats []struct {
		CallerService string  `json:"caller_service"`
		Calls         int64   `json:"calls"`
		InputTokens   int64   `json:"input_tokens"`
		OutputTokens  int64   `json:"output_tokens"`
		TotalTokens   int64   `json:"total_tokens"`
		CostUSD       float64 `json:"cost_usd"`
	}
	if err := s.db.WithContext(ctx).
		Model(&models.LLMUsageRecord{}).
		Select("caller_service, COUNT(*) as calls, COALESCE(SUM(input_tokens),0) as input_tokens, COALESCE(SUM(output_tokens),0) as output_tokens, COALESCE(SUM(total_tokens),0) as total_tokens, COALESCE(SUM(estimated_cost),0) as cost_usd").
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, knowledgeBaseID).
		Group("caller_service").
		Order("total_tokens DESC").
		Scan(&serviceStats).Error; err == nil {
		summary.ByService = make([]ServiceUsageStat, len(serviceStats))
		for i, s := range serviceStats {
			summary.ByService[i] = ServiceUsageStat{
				CallerService: s.CallerService,
				Calls:         s.Calls,
				InputTokens:   s.InputTokens,
				OutputTokens:  s.OutputTokens,
				TotalTokens:   s.TotalTokens,
				CostUSD:       s.CostUSD,
			}
		}
	}

	// By model type
	var modelStats []struct {
		ModelType    string  `json:"model_type"`
		ModelID      string  `json:"model_id"`
		Calls        int64   `json:"calls"`
		InputTokens  int64   `json:"input_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		TotalTokens  int64   `json:"total_tokens"`
		CostUSD      float64 `json:"cost_usd"`
	}
	if err := s.db.WithContext(ctx).
		Model(&models.LLMUsageRecord{}).
		Select("model_type, model_id, COUNT(*) as calls, COALESCE(SUM(input_tokens),0) as input_tokens, COALESCE(SUM(output_tokens),0) as output_tokens, COALESCE(SUM(total_tokens),0) as total_tokens, COALESCE(SUM(estimated_cost),0) as cost_usd").
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, knowledgeBaseID).
		Group("model_type, model_id").
		Order("total_tokens DESC").
		Scan(&modelStats).Error; err == nil {
		summary.ByModelType = make([]ModelTypeUsage, len(modelStats))
		for i, m := range modelStats {
			summary.ByModelType[i] = ModelTypeUsage{
				ModelType:    m.ModelType,
				ModelID:      m.ModelID,
				Calls:        m.Calls,
				InputTokens:  m.InputTokens,
				OutputTokens: m.OutputTokens,
				TotalTokens:  m.TotalTokens,
				CostUSD:      m.CostUSD,
			}
		}
	}

	// Top documents by usage
	var topDocs []struct {
		DocumentID  string  `json:"document_id"`
		Calls       int64   `json:"calls"`
		TotalTokens int64   `json:"total_tokens"`
		CostUSD     float64 `json:"cost_usd"`
	}
	if err := s.db.WithContext(ctx).
		Model(&models.LLMUsageRecord{}).
		Select("document_id as document_id, COUNT(*) as calls, COALESCE(SUM(total_tokens),0) as total_tokens, COALESCE(SUM(estimated_cost),0) as cost_usd").
		Where("tenant_id = ? AND knowledge_base_id = ? AND document_id IS NOT NULL", tenantID, knowledgeBaseID).
		Group("document_id").
		Order("total_tokens DESC").
		Limit(10).
		Scan(&topDocs).Error; err == nil {

		// Get document titles
		docTitles := make(map[string]string)
		var docIDs []string
		for _, d := range topDocs {
			docIDs = append(docIDs, d.DocumentID)
		}
		if len(docIDs) > 0 {
			var docs []models.Document
			s.db.WithContext(ctx).
				Select("id, title").
				Where("id IN ?", docIDs).
				Find(&docs)
			for _, d := range docs {
				docTitles[d.ID] = d.Title
			}
		}

		summary.TopDocuments = make([]DocUsageStat, len(topDocs))
		for i, d := range topDocs {
			summary.TopDocuments[i] = DocUsageStat{
				DocumentID:    d.DocumentID,
				DocumentTitle: docTitles[d.DocumentID],
				Calls:         d.Calls,
				TotalTokens:   d.TotalTokens,
				CostUSD:       d.CostUSD,
			}
		}
	}

	return summary
}

// DocumentObservability represents observability data for a single document
type DocumentObservability struct {
	DocumentID     string                            `json:"document_id"`
	Title          string                            `json:"title"`
	Content        string                            `json:"content"`
	DocType        string                            `json:"doc_type"`
	Status         string                            `json:"status"`
	IndexStatus    *opensearch.DocumentIndexStatus   `json:"index_status"`
	Chunks         []ChunkDetail                     `json:"chunks"`
	TokenAnalysis  *TokenAnalysisResult              `json:"token_analysis"`
	VectorSamples  []opensearch.DocumentVectorSample `json:"vector_samples"`
	GraphEntities  []GraphEntitySample               `json:"graph_entities"`
	GraphRelations []GraphRelationSample             `json:"graph_relations"`
	LLMUsage       *DocumentLLMUsageSummary          `json:"llm_usage"`
	Warnings       []string                          `json:"warnings,omitempty"`
}

// DocumentLLMUsageSummary represents LLM usage stats for a document
type DocumentLLMUsageSummary struct {
	TotalCalls        int64             `json:"total_calls"`
	TotalInputTokens  int64             `json:"total_input_tokens"`
	TotalOutputTokens int64             `json:"total_output_tokens"`
	TotalTokens       int64             `json:"total_tokens"`
	EstimatedCostUSD  float64           `json:"estimated_cost_usd"`
	ByMethod          []MethodUsageStat `json:"by_method"`
	ByModelType       []ModelTypeUsage  `json:"by_model_type"`
	Records           []LLMUsageItem    `json:"records"`
}

// MethodUsageStat represents usage grouped by caller method
type MethodUsageStat struct {
	CallerService string  `json:"caller_service"`
	CallerMethod  string  `json:"caller_method"`
	Calls         int64   `json:"calls"`
	InputTokens   int64   `json:"input_tokens"`
	OutputTokens  int64   `json:"output_tokens"`
	TotalTokens   int64   `json:"total_tokens"`
	CostUSD       float64 `json:"cost_usd"`
}

// ModelTypeUsage represents usage grouped by model type
type ModelTypeUsage struct {
	ModelType    string  `json:"model_type"`
	ModelID      string  `json:"model_id"`
	Calls        int64   `json:"calls"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	TotalTokens  int64   `json:"total_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

// LLMUsageItem represents a single LLM usage record for display
type LLMUsageItem struct {
	ID            string    `json:"id"`
	CallerService string    `json:"caller_service"`
	CallerMethod  string    `json:"caller_method"`
	ModelID       string    `json:"model_id"`
	ModelType     string    `json:"model_type"`
	InputTokens   int       `json:"input_tokens"`
	OutputTokens  int       `json:"output_tokens"`
	TotalTokens   int       `json:"total_tokens"`
	CostUSD       float64   `json:"cost_usd"`
	DurationMs    int64     `json:"duration_ms"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
}

// ChunkDetail represents detailed chunk information
type ChunkDetail struct {
	ChunkID    string `json:"chunk_id"`
	ChunkIndex int    `json:"chunk_index"`
	ChunkType  string `json:"chunk_type"`
	VectorID   string `json:"vector_id"`
	Content    string `json:"content"`
	WordCount  int    `json:"word_count"`
}

// TokenAnalysisResult represents text tokenization result
type TokenAnalysisResult struct {
	Analyzer string             `json:"analyzer"`
	Tokens   []opensearch.Token `json:"tokens"`
	Stats    TokenAnalysisStats `json:"stats"`
}

// TokenAnalysisStats represents token statistics
type TokenAnalysisStats struct {
	TotalTokens  int            `json:"total_tokens"`
	UniqueTokens int            `json:"unique_tokens"`
	TokenTypes   map[string]int `json:"token_types"`
	AvgTokenLen  float64        `json:"avg_token_len"`
}

// GetDocumentObservability returns detailed observability data for a single document
func (s *DocumentService) GetDocumentObservability(ctx context.Context, tenantID, docID string) (*DocumentObservability, error) {
	// Get document
	doc, err := s.GetDocument(ctx, tenantID, docID)
	if err != nil {
		return nil, err
	}

	result := &DocumentObservability{
		DocumentID: docID,
		Title:      doc.Title,
		Content:    doc.Content,
		DocType:    doc.DocType,
		Status:     doc.Status,
		Warnings:   []string{},
	}

	// Get chunks
	var chunks []models.Chunk
	docIDStr := docID
	if err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND document_id = ?", tenantID, docID).
		Order("chunk_index ASC").
		Find(&chunks).Error; err == nil {
		result.Chunks = make([]ChunkDetail, len(chunks))
		for i, c := range chunks {
			result.Chunks[i] = ChunkDetail{
				ChunkID:    c.ID,
				ChunkIndex: c.ChunkIndex,
				ChunkType:  c.ChunkType,
				VectorID:   c.VectorID,
				Content:    c.Content,
				WordCount:  len(strings.Fields(c.Content)),
			}
		}
	}

	// Get index status
	if s.opensearch != nil {
		status, err := s.opensearch.GetDocumentIndexStatus(ctx, doc.KnowledgeBaseID, docIDStr)
		if err != nil {
			result.Warnings = append(result.Warnings, "failed to get index status: "+err.Error())
		} else {
			result.IndexStatus = status
		}

		// Get token analysis
		contentToAnalyze := doc.Content
		if len(contentToAnalyze) > 5000 {
			contentToAnalyze = contentToAnalyze[:5000]
		}
		analyzeResult, err := s.opensearch.Analyze(ctx, doc.KnowledgeBaseID, contentToAnalyze, "ik_max_word")
		if err != nil {
			result.Warnings = append(result.Warnings, "failed to analyze tokens: "+err.Error())
		} else {
			stats := computeTokenStats(analyzeResult.Tokens)
			result.TokenAnalysis = &TokenAnalysisResult{
				Analyzer: "ik_max_word",
				Tokens:   analyzeResult.Tokens,
				Stats:    stats,
			}
		}

		// Get vector samples
		vectors, err := s.opensearch.GetDocumentVectors(ctx, doc.KnowledgeBaseID, docIDStr, 20)
		if err != nil {
			result.Warnings = append(result.Warnings, "failed to get vectors: "+err.Error())
		} else {
			result.VectorSamples = vectors
		}
	}

	// Get graph entities
	var entities []models.GraphEntity
	if err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND document_id = ?", tenantID, docID).
		Order("created_at DESC").
		Limit(50).
		Find(&entities).Error; err == nil {
		result.GraphEntities = make([]GraphEntitySample, len(entities))
		for i, e := range entities {
			result.GraphEntities[i] = GraphEntitySample{
				EntityID:   e.ID,
				DocumentID: e.DocumentID,
				Name:       e.Name,
				Type:       e.Type,
			}
		}
	}

	// Get graph relations — use raw SQL join since we removed GORM foreign key associations
	if err := s.db.WithContext(ctx).
		Table("graph_relations gr").
		Select("gr.id AS relation_id, gr.source_id AS source_entity_id, se.name AS source_name, se.type AS source_type, gr.target_id AS target_entity_id, te.name AS target_name, te.type AS target_type, gr.type AS relation_type, gr.weight, se.document_id AS source_document_id, te.document_id AS target_document_id").
		Joins("JOIN graph_entities se ON se.id = gr.source_id").
		Joins("JOIN graph_entities te ON te.id = gr.target_id").
		Where("gr.tenant_id = ? AND (se.document_id = ? OR te.document_id = ?)", tenantID, docID, docID).
		Order("gr.weight DESC").
		Limit(50).
		Scan(&result.GraphRelations).Error; err != nil {
		result.Warnings = append(result.Warnings, "failed to get graph relations: "+err.Error())
	}

	// Get LLM usage for this document
	result.LLMUsage = s.getDocumentLLMUsage(ctx, tenantID, docID)

	return result, nil
}

// getDocumentLLMUsage queries LLM usage for a document
func (s *DocumentService) getDocumentLLMUsage(ctx context.Context, tenantID string, docID string) *DocumentLLMUsageSummary {
	summary := &DocumentLLMUsageSummary{}

	// Get all records for this document
	var records []models.LLMUsageRecord
	if err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND document_id = ?", tenantID, docID).
		Order("created_at ASC").
		Find(&records).Error; err != nil {
		return summary
	}

	summary.TotalCalls = int64(len(records))
	summary.Records = make([]LLMUsageItem, len(records))

	methodMap := make(map[string]*MethodUsageStat)
	modelMap := make(map[string]*ModelTypeUsage)

	for i, r := range records {
		summary.TotalInputTokens += int64(r.InputTokens)
		summary.TotalOutputTokens += int64(r.OutputTokens)
		summary.TotalTokens += int64(r.TotalTokens)
		summary.EstimatedCostUSD += r.EstimatedCost

		summary.Records[i] = LLMUsageItem{
			ID:            r.ID,
			CallerService: r.CallerService,
			CallerMethod:  r.CallerMethod,
			ModelID:       r.ModelID,
			ModelType:     r.ModelType,
			InputTokens:   r.InputTokens,
			OutputTokens:  r.OutputTokens,
			TotalTokens:   r.TotalTokens,
			CostUSD:       r.EstimatedCost,
			DurationMs:    r.DurationMs,
			Status:        r.Status,
			CreatedAt:     r.CreatedAt,
		}

		// Aggregate by method
		methodKey := r.CallerService + "." + r.CallerMethod
		if m, ok := methodMap[methodKey]; ok {
			m.Calls++
			m.InputTokens += int64(r.InputTokens)
			m.OutputTokens += int64(r.OutputTokens)
			m.TotalTokens += int64(r.TotalTokens)
			m.CostUSD += r.EstimatedCost
		} else {
			methodMap[methodKey] = &MethodUsageStat{
				CallerService: r.CallerService,
				CallerMethod:  r.CallerMethod,
				Calls:         1,
				InputTokens:   int64(r.InputTokens),
				OutputTokens:  int64(r.OutputTokens),
				TotalTokens:   int64(r.TotalTokens),
				CostUSD:       r.EstimatedCost,
			}
		}

		// Aggregate by model type
		modelKey := r.ModelType + ":" + r.ModelID
		if m, ok := modelMap[modelKey]; ok {
			m.Calls++
			m.InputTokens += int64(r.InputTokens)
			m.OutputTokens += int64(r.OutputTokens)
			m.TotalTokens += int64(r.TotalTokens)
			m.CostUSD += r.EstimatedCost
		} else {
			modelMap[modelKey] = &ModelTypeUsage{
				ModelType:    r.ModelType,
				ModelID:      r.ModelID,
				Calls:        1,
				InputTokens:  int64(r.InputTokens),
				OutputTokens: int64(r.OutputTokens),
				TotalTokens:  int64(r.TotalTokens),
				CostUSD:      r.EstimatedCost,
			}
		}
	}

	summary.ByMethod = make([]MethodUsageStat, 0, len(methodMap))
	for _, v := range methodMap {
		summary.ByMethod = append(summary.ByMethod, *v)
	}
	summary.ByModelType = make([]ModelTypeUsage, 0, len(modelMap))
	for _, v := range modelMap {
		summary.ByModelType = append(summary.ByModelType, *v)
	}

	return summary
}

func computeTokenStats(tokens []opensearch.Token) TokenAnalysisStats {
	stats := TokenAnalysisStats{
		TotalTokens: len(tokens),
		TokenTypes:  make(map[string]int),
	}
	uniqueTokens := make(map[string]bool)
	totalLen := 0

	for _, t := range tokens {
		uniqueTokens[t.Token] = true
		stats.TokenTypes[t.Type]++
		totalLen += len(t.Token)
	}

	stats.UniqueTokens = len(uniqueTokens)
	if stats.TotalTokens > 0 {
		stats.AvgTokenLen = float64(totalLen) / float64(stats.TotalTokens)
	}

	return stats
}

// DeleteDocument deletes a document and its related data
func (s *DocumentService) DeleteDocument(ctx context.Context, tenantID, docID string) error {
	// Look up the document to get its KnowledgeBaseID for index deletion
	var doc models.Document
	if err := s.db.Where("id = ? AND tenant_id = ?", docID, tenantID).First(&doc).Error; err == nil {
		if s.opensearch != nil {
			_ = s.opensearch.DeleteDocument(ctx, doc.KnowledgeBaseID, docID)
		}

		// Delete Neo4j entities for this document
		if s.graphSrv != nil && s.graphSrv.neo4jCli != nil {
			_ = s.graphSrv.neo4jCli.DeleteDocumentEntities(ctx, doc.KnowledgeBaseID, docID)
		}
	}

	result := s.db.Where("id = ? AND tenant_id = ?", docID, tenantID).Delete(&models.Document{})
	if result.Error != nil {
		return result.Error
	}
	return nil
}

// CreateDocumentRequest represents a document creation request
type CreateDocumentRequest struct {
	Title           string       `json:"title" binding:"required"`
	Content         string       `json:"content"`
	KnowledgeBaseID string       `json:"knowledge_base_id"`
	DocType         string       `json:"doc_type"`
	Format          string       `json:"format"`
	FilePath        string       `json:"file_path"`
	FileSize        int64        `json:"file_size"`
	Metadata        models.JSONB `json:"metadata"`
}

// DocumentFilter represents document filter options
type DocumentFilter struct {
	KnowledgeBaseID string `form:"knowledge_base_id"`
	DocType         string `form:"doc_type"`
	Status          string `form:"status"`
	Keyword         string `form:"keyword"`
	Page            int    `form:"page"`
	PageSize        int    `form:"page_size"`
}

// ParseDocument parses different document formats
func (s *DocumentService) ParseDocument(content []byte, format string) (string, error) {
	switch strings.ToLower(format) {
	case "txt", "md", "markdown":
		return string(content), nil
	case "html":
		return s.parseHTML(content)
	case "pdf":
		return s.parsePDF(content)
	case "docx":
		return s.parseDocx(content)
	case "xlsx", "csv":
		return s.parseXlsx(content, format)
	default:
		return string(content), nil
	}
}

func (s *DocumentService) parseHTML(content []byte) (string, error) {
	if len(content) == 0 {
		return "", nil
	}

	doc, err := html.Parse(bytes.NewReader(content))
	if err != nil {
		// Fallback: simple tag stripping
		re := regexp.MustCompile(`<[^>]*>`)
		text := re.ReplaceAllString(string(content), " ")
		return strings.TrimSpace(strings.Join(strings.Fields(text), " ")), nil
	}

	var result strings.Builder
	s.htmlNodeToMarkdown(doc, &result, &htmlContext{})
	text := strings.TrimSpace(result.String())
	// Clean up excessive blank lines
	multiBlankLine := regexp.MustCompile(`\n{3,}`)
	text = multiBlankLine.ReplaceAllString(text, "\n\n")
	if text == "" {
		return "[html] 文档内容为空或无法提取文本", nil
	}
	return text, nil
}

// htmlContext tracks state during HTML→Markdown conversion
type htmlContext struct {
	inPre       bool
	inCode      bool
	listDepth   int
	orderedList bool
	listCounter int
	inTable     bool
	tableRows   [][]string
	currentRow  []string
	inLink      bool
	linkHref    string
}

// htmlNodeToMarkdown recursively converts an HTML node tree to Markdown
func (s *DocumentService) htmlNodeToMarkdown(n *html.Node, w *strings.Builder, ctx *htmlContext) {
	if n == nil {
		return
	}

	switch n.Type {
	case html.TextNode:
		text := n.Data
		if ctx.inPre || ctx.inCode {
			w.WriteString(text)
		} else if ctx.inTable {
			// Accumulate table cell text
		} else {
			// Collapse whitespace for normal text
			text = strings.Join(strings.Fields(text), " ")
			if text != "" {
				w.WriteString(text)
			}
		}
		return

	case html.ElementNode:
		tag := strings.ToLower(n.Data)

		switch tag {
		// Headings
		case "h1", "h2", "h3", "h4", "h5", "h6":
			level := int(tag[1] - '0')
			w.WriteString("\n\n")
			w.WriteString(strings.Repeat("#", level))
			w.WriteString(" ")
			s.htmlChildrenText(n, w, ctx)
			w.WriteString("\n\n")
			return

		// Paragraphs
		case "p":
			w.WriteString("\n\n")
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				s.htmlNodeToMarkdown(child, w, ctx)
			}
			w.WriteString("\n\n")
			return

		// Line breaks
		case "br":
			w.WriteString("\n")
			return

		// Horizontal rule
		case "hr":
			w.WriteString("\n\n---\n\n")
			return

		// Bold
		case "b", "strong":
			w.WriteString("**")
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				s.htmlNodeToMarkdown(child, w, ctx)
			}
			w.WriteString("**")
			return

		// Italic
		case "i", "em":
			w.WriteString("*")
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				s.htmlNodeToMarkdown(child, w, ctx)
			}
			w.WriteString("*")
			return

		// Inline code
		case "code":
			if !ctx.inPre {
				w.WriteString("`")
				for child := n.FirstChild; child != nil; child = child.NextSibling {
					s.htmlNodeToMarkdown(child, w, ctx)
				}
				w.WriteString("`")
				return
			}
			// Inside <pre>, handled by pre block
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				s.htmlNodeToMarkdown(child, w, ctx)
			}
			return

		// Preformatted / code blocks
		case "pre":
			lang := s.htmlDetectCodeLang(n)
			w.WriteString("\n\n```")
			w.WriteString(lang)
			w.WriteString("\n")
			oldInPre := ctx.inPre
			ctx.inPre = true
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				s.htmlNodeToMarkdown(child, w, ctx)
			}
			ctx.inPre = oldInPre
			w.WriteString("\n```\n\n")
			return

		// Links
		case "a":
			href := s.htmlGetAttr(n, "href")
			w.WriteString("[")
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				s.htmlNodeToMarkdown(child, w, ctx)
			}
			w.WriteString("](")
			w.WriteString(href)
			w.WriteString(")")
			return

		// Images
		case "img":
			alt := s.htmlGetAttr(n, "alt")
			src := s.htmlGetAttr(n, "src")
			w.WriteString("![")
			w.WriteString(alt)
			w.WriteString("](")
			w.WriteString(src)
			w.WriteString(")")
			return

		// Unordered lists
		case "ul":
			w.WriteString("\n")
			oldOrdered := ctx.orderedList
			ctx.orderedList = false
			ctx.listDepth++
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				s.htmlNodeToMarkdown(child, w, ctx)
			}
			ctx.listDepth--
			ctx.orderedList = oldOrdered
			w.WriteString("\n")
			return

		// Ordered lists
		case "ol":
			w.WriteString("\n")
			oldOrdered := ctx.orderedList
			oldCounter := ctx.listCounter
			ctx.orderedList = true
			ctx.listCounter = 0
			ctx.listDepth++
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				s.htmlNodeToMarkdown(child, w, ctx)
			}
			ctx.listDepth--
			ctx.orderedList = oldOrdered
			ctx.listCounter = oldCounter
			w.WriteString("\n")
			return

		// List items
		case "li":
			indent := strings.Repeat("  ", ctx.listDepth-1)
			if ctx.orderedList {
				ctx.listCounter++
				w.WriteString(fmt.Sprintf("%s%d. ", indent, ctx.listCounter))
			} else {
				w.WriteString(indent + "- ")
			}
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				s.htmlNodeToMarkdown(child, w, ctx)
			}
			w.WriteString("\n")
			return

		// Tables
		case "table":
			w.WriteString("\n\n")
			oldInTable := ctx.inTable
			oldRows := ctx.tableRows
			ctx.inTable = true
			ctx.tableRows = nil
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				s.htmlNodeToMarkdown(child, w, ctx)
			}
			// Render accumulated table rows as Markdown
			if len(ctx.tableRows) > 0 {
				maxCols := 0
				for _, row := range ctx.tableRows {
					if len(row) > maxCols {
						maxCols = len(row)
					}
				}
				for i, row := range ctx.tableRows {
					for len(row) < maxCols {
						row = append(row, "")
					}
					w.WriteString("| " + strings.Join(row, " | ") + " |\n")
					if i == 0 {
						sep := make([]string, maxCols)
						for j := range sep {
							sep[j] = "---"
						}
						w.WriteString("| " + strings.Join(sep, " | ") + " |\n")
					}
				}
			}
			ctx.inTable = oldInTable
			ctx.tableRows = oldRows
			w.WriteString("\n")
			return

		case "thead", "tbody", "tfoot":
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				s.htmlNodeToMarkdown(child, w, ctx)
			}
			return

		case "tr":
			oldRow := ctx.currentRow
			ctx.currentRow = nil
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				s.htmlNodeToMarkdown(child, w, ctx)
			}
			if len(ctx.currentRow) > 0 {
				ctx.tableRows = append(ctx.tableRows, ctx.currentRow)
			}
			ctx.currentRow = oldRow
			return

		case "td", "th":
			var cellBuf strings.Builder
			cellCtx := &htmlContext{inPre: ctx.inPre, inCode: ctx.inCode}
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				s.htmlNodeToMarkdown(child, &cellBuf, cellCtx)
			}
			cellText := strings.TrimSpace(cellBuf.String())
			cellText = strings.ReplaceAll(cellText, "|", "\\|")
			cellText = strings.ReplaceAll(cellText, "\n", " ")
			ctx.currentRow = append(ctx.currentRow, cellText)
			return

		// Blockquote
		case "blockquote":
			var quoteBuf strings.Builder
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				s.htmlNodeToMarkdown(child, &quoteBuf, ctx)
			}
			lines := strings.Split(strings.TrimSpace(quoteBuf.String()), "\n")
			w.WriteString("\n")
			for _, line := range lines {
				w.WriteString("> " + line + "\n")
			}
			w.WriteString("\n")
			return

		// Skip non-visible elements
		case "script", "style", "noscript", "head", "meta", "link":
			return

		// Division / section - just recurse
		case "div", "section", "article", "main", "header", "footer", "nav", "aside",
			"span", "body", "html", "form", "label", "input", "button", "select",
			"textarea", "fieldset", "legend", "details", "summary", "figure",
			"figcaption", "mark", "del", "ins", "sub", "sup", "abbr", "time",
			"small", "cite", "dfn", "var", "samp", "kbd", "ruby", "rt", "rp",
			"bdi", "bdo", "wbr", "data", "output", "progress", "meter",
			"dialog", "slot", "template", "caption", "colgroup", "col":
			// Just recurse into children
		}
	}

	// Default: recurse into children
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		s.htmlNodeToMarkdown(child, w, ctx)
	}
}

// htmlChildrenText extracts text from all children of a node
func (s *DocumentService) htmlChildrenText(n *html.Node, w *strings.Builder, ctx *htmlContext) {
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		s.htmlNodeToMarkdown(child, w, ctx)
	}
}

// htmlGetAttr gets an attribute value from an HTML node
func (s *DocumentService) htmlGetAttr(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

// htmlDetectCodeLang tries to detect the programming language from a <pre> block
func (s *DocumentService) htmlDetectCodeLang(n *html.Node) string {
	// Check class attribute on <pre> or child <code>
	for _, attr := range n.Attr {
		if attr.Key == "class" {
			if lang := s.htmlExtractLangFromClass(attr.Val); lang != "" {
				return lang
			}
		}
	}
	// Check child <code> element
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.Data == "code" {
			for _, attr := range child.Attr {
				if attr.Key == "class" {
					if lang := s.htmlExtractLangFromClass(attr.Val); lang != "" {
						return lang
					}
				}
			}
		}
	}
	return ""
}

// htmlExtractLangFromClass extracts language from class names like "language-go" or "lang-python"
func (s *DocumentService) htmlExtractLangFromClass(class string) string {
	for _, cls := range strings.Fields(class) {
		if strings.HasPrefix(cls, "language-") {
			return strings.TrimPrefix(cls, "language-")
		}
		if strings.HasPrefix(cls, "lang-") {
			return strings.TrimPrefix(cls, "lang-")
		}
	}
	return ""
}

func (s *DocumentService) parsePDF(content []byte) (string, error) {
	if len(content) == 0 {
		return "", nil
	}
	return "[pdf] 当前版本未接入PDF结构化解析，已保留原始文件并等待后续解析增强。", nil
}

func (s *DocumentService) parseDocx(content []byte) (string, error) {
	if len(content) == 0 {
		return "", nil
	}

	// DOCX is a ZIP archive containing word/document.xml
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", fmt.Errorf("failed to open docx as zip: %w", err)
	}

	// Find word/document.xml
	var docFile *zip.File
	for _, f := range reader.File {
		if f.Name == "word/document.xml" {
			docFile = f
			break
		}
	}
	if docFile == nil {
		return "", fmt.Errorf("word/document.xml not found in docx")
	}

	rc, err := docFile.Open()
	if err != nil {
		return "", fmt.Errorf("failed to open document.xml: %w", err)
	}
	defer rc.Close()

	xmlContent, err := io.ReadAll(rc)
	if err != nil {
		return "", fmt.Errorf("failed to read document.xml: %w", err)
	}

	// Parse XML to extract paragraph text
	// DOCX XML structure: <w:document> -> <w:body> -> <w:p> -> <w:r> -> <w:t>
	type Text struct {
		Content string `xml:",chardata"`
	}
	type Run struct {
		Texts []Text `xml:"t"`
	}
	type ParagraphProperties struct {
		PStyle struct {
			Val string `xml:"val,attr"`
		} `xml:"pStyle"`
	}
	type Paragraph struct {
		Properties *ParagraphProperties `xml:"pPr"`
		Runs       []Run                `xml:"r"`
		Hyperlinks []struct {
			Runs []Run `xml:"r"`
		} `xml:"hyperlink"`
	}
	type Table struct {
		Rows []struct {
			Cells []struct {
				Paragraphs []Paragraph `xml:"p"`
			} `xml:"tc"`
		} `xml:"tr"`
	}
	type Body struct {
		Paragraphs []Paragraph `xml:"p"`
		Tables     []Table     `xml:"tbl"`
		// We need to preserve order, so use raw XML tokens approach below
	}
	type Document struct {
		Body Body `xml:"body"`
	}

	// Use token-based parsing to preserve paragraph/table order
	var result strings.Builder
	decoder := xml.NewDecoder(bytes.NewReader(xmlContent))

	var inParagraph, inRun, inText, inTable, inTableRow, inTableCell bool
	var currentParagraph strings.Builder
	var tableRows [][]string
	var currentRow []string
	var cellText strings.Builder
	var pStyle string
	depth := 0

	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}

		switch t := token.(type) {
		case xml.StartElement:
			localName := t.Name.Local
			switch localName {
			case "p":
				inParagraph = true
				currentParagraph.Reset()
				pStyle = ""
			case "pStyle":
				if inParagraph {
					for _, attr := range t.Attr {
						if attr.Name.Local == "val" {
							pStyle = attr.Value
						}
					}
				}
			case "r":
				if inParagraph {
					inRun = true
				}
			case "t":
				if inRun {
					inText = true
				}
			case "tbl":
				inTable = true
				tableRows = nil
				depth = 0
			case "tr":
				if inTable {
					inTableRow = true
					currentRow = nil
				}
			case "tc":
				if inTableRow {
					inTableCell = true
					cellText.Reset()
				}
			case "tab":
				if inParagraph {
					currentParagraph.WriteString("\t")
				}
			case "br":
				if inParagraph {
					currentParagraph.WriteString("\n")
				}
			}
			if inTable && localName != "tbl" {
				depth++
			}

		case xml.CharData:
			if inText && inRun && inParagraph {
				text := string(t)
				if inTableCell {
					cellText.WriteString(text)
				} else {
					currentParagraph.WriteString(text)
				}
			}

		case xml.EndElement:
			localName := t.Name.Local
			switch localName {
			case "t":
				inText = false
			case "r":
				inRun = false
			case "p":
				if inTableCell {
					// Table cell paragraph - append to cell text
					if cellText.Len() > 0 && currentParagraph.Len() > 0 {
						cellText.WriteString(" ")
					}
					cellText.WriteString(currentParagraph.String())
				} else if inParagraph {
					text := strings.TrimSpace(currentParagraph.String())
					if text != "" {
						// Convert heading styles to Markdown headers
						switch {
						case strings.Contains(pStyle, "Heading1") || pStyle == "1":
							result.WriteString("# " + text + "\n\n")
						case strings.Contains(pStyle, "Heading2") || pStyle == "2":
							result.WriteString("## " + text + "\n\n")
						case strings.Contains(pStyle, "Heading3") || pStyle == "3":
							result.WriteString("### " + text + "\n\n")
						case strings.Contains(pStyle, "Heading4") || pStyle == "4":
							result.WriteString("#### " + text + "\n\n")
						default:
							result.WriteString(text + "\n\n")
						}
					}
				}
				inParagraph = false
			case "tc":
				if inTableCell {
					currentRow = append(currentRow, strings.TrimSpace(cellText.String()))
					inTableCell = false
				}
			case "tr":
				if inTableRow && len(currentRow) > 0 {
					tableRows = append(tableRows, currentRow)
					inTableRow = false
				}
			case "tbl":
				// Convert table to Markdown table
				if len(tableRows) > 0 {
					// Determine max columns
					maxCols := 0
					for _, row := range tableRows {
						if len(row) > maxCols {
							maxCols = len(row)
						}
					}

					for i, row := range tableRows {
						// Pad row to maxCols
						for len(row) < maxCols {
							row = append(row, "")
						}
						result.WriteString("| " + strings.Join(row, " | ") + " |\n")
						// Add header separator after first row
						if i == 0 {
							sep := make([]string, maxCols)
							for j := range sep {
								sep[j] = "---"
							}
							result.WriteString("| " + strings.Join(sep, " | ") + " |\n")
						}
					}
					result.WriteString("\n")
				}
				inTable = false
			}
		}
	}

	text := strings.TrimSpace(result.String())
	if text == "" {
		return "[docx] 文档内容为空或无法提取文本", nil
	}
	return text, nil
}

func (s *DocumentService) parseXlsx(content []byte, format string) (string, error) {
	if len(content) == 0 {
		return "", nil
	}

	// CSV uses standard library
	if strings.ToLower(format) == "csv" {
		return s.parseCSV(content)
	}

	// XLSX uses excelize
	f, err := excelize.OpenReader(bytes.NewReader(content))
	if err != nil {
		return "", fmt.Errorf("failed to open xlsx: %w", err)
	}
	defer f.Close()

	var result strings.Builder
	sheets := f.GetSheetList()

	for sheetIdx, sheetName := range sheets {
		rows, err := f.GetRows(sheetName)
		if err != nil {
			continue
		}
		if len(rows) == 0 {
			continue
		}

		// Add sheet name as heading (skip if only one sheet with default name)
		if len(sheets) > 1 || (sheetName != "Sheet1" && sheetName != "Sheet 1") {
			result.WriteString("## " + sheetName + "\n\n")
		}

		// Determine max columns across all rows
		maxCols := 0
		for _, row := range rows {
			if len(row) > maxCols {
				maxCols = len(row)
			}
		}
		if maxCols == 0 {
			continue
		}

		// Skip entirely empty rows, but track which rows have data
		var dataRows [][]string
		for _, row := range rows {
			// Check if row has any non-empty cell
			hasData := false
			for _, cell := range row {
				if strings.TrimSpace(cell) != "" {
					hasData = true
					break
				}
			}
			if hasData {
				// Pad to maxCols
				for len(row) < maxCols {
					row = append(row, "")
				}
				// Escape pipe characters in cell content
				escaped := make([]string, len(row))
				for i, cell := range row {
					escaped[i] = strings.ReplaceAll(strings.TrimSpace(cell), "|", "\\|")
				}
				dataRows = append(dataRows, escaped)
			}
		}

		if len(dataRows) == 0 {
			continue
		}

		// Render as Markdown table
		// First row as header
		result.WriteString("| " + strings.Join(dataRows[0], " | ") + " |\n")
		sep := make([]string, maxCols)
		for i := range sep {
			sep[i] = "---"
		}
		result.WriteString("| " + strings.Join(sep, " | ") + " |\n")

		// Remaining rows
		for _, row := range dataRows[1:] {
			result.WriteString("| " + strings.Join(row, " | ") + " |\n")
		}
		result.WriteString("\n")

		// Limit to reasonable number of rows per sheet to avoid token explosion
		if sheetIdx < len(sheets)-1 {
			result.WriteString("---\n\n")
		}
	}

	text := strings.TrimSpace(result.String())
	if text == "" {
		return "[xlsx] 工作簿为空或无法提取内容", nil
	}
	return text, nil
}

func (s *DocumentService) parseCSV(content []byte) (string, error) {
	reader := csv.NewReader(bytes.NewReader(content))
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true
	// Auto-detect delimiter: try common delimiters
	reader.FieldsPerRecord = -1 // Allow variable fields

	records, err := reader.ReadAll()
	if err != nil {
		// Try with tab delimiter
		reader = csv.NewReader(bytes.NewReader(content))
		reader.Comma = '\t'
		reader.LazyQuotes = true
		reader.FieldsPerRecord = -1
		records, err = reader.ReadAll()
		if err != nil {
			return "", fmt.Errorf("failed to parse CSV: %w", err)
		}
	}

	if len(records) == 0 {
		return "", nil
	}

	// Determine max columns
	maxCols := 0
	for _, row := range records {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}

	var result strings.Builder

	// Render as Markdown table
	for i, row := range records {
		// Pad to maxCols
		for len(row) < maxCols {
			row = append(row, "")
		}
		escaped := make([]string, len(row))
		for j, cell := range row {
			escaped[j] = strings.ReplaceAll(strings.TrimSpace(cell), "|", "\\|")
		}
		result.WriteString("| " + strings.Join(escaped, " | ") + " |\n")
		if i == 0 {
			sep := make([]string, maxCols)
			for j := range sep {
				sep[j] = "---"
			}
			result.WriteString("| " + strings.Join(sep, " | ") + " |\n")
		}
	}

	return strings.TrimSpace(result.String()), nil
}
