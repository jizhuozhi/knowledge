package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jizhuozhi/knowledge/internal/config"
	"github.com/jizhuozhi/knowledge/internal/models"
	"github.com/jizhuozhi/knowledge/internal/neo4j"
	"github.com/jizhuozhi/knowledge/internal/opensearch"
	"gorm.io/gorm"
)

// RAGServiceV2 is the enhanced RAG service with full query processing pipeline
type RAGServiceV2 struct {
	db           *gorm.DB
	config       *config.Config
	query        *QueryService
	recall       *RecallService
	reranker     *RerankerService
	embedding    *EmbeddingService
	usageTracker *LLMUsageTracker
}

// NewRAGServiceV2 creates a new enhanced RAG service
func NewRAGServiceV2(db *gorm.DB, cfg *config.Config) *RAGServiceV2 {
	return &RAGServiceV2{
		db:           db,
		config:       cfg,
		query:        NewQueryService(db, cfg),
		recall:       NewRecallService(db, cfg),
		reranker:     NewRerankerService(db, cfg),
		embedding:    NewEmbeddingService(cfg),
		usageTracker: NewLLMUsageTracker(db, cfg),
	}
}

// SetOpenSearchClient sets the OpenSearch client
func (s *RAGServiceV2) SetOpenSearchClient(client *opensearch.Client) {
	s.recall.SetOpenSearchClient(client)
}

// SetNeo4jClient sets the Neo4j client
func (s *RAGServiceV2) SetNeo4jClient(client *neo4j.Client) {
	s.recall.SetNeo4jClient(client)
}

// RAGRequestV2 represents an enhanced RAG query request
type RAGRequestV2 struct {
	Query              string                 `json:"query" binding:"required"`
	KnowledgeBaseID    *string                `json:"knowledge_base_id,omitempty"`
	TopK               int                    `json:"top_k,omitempty"`
	TopKPerChannel     int                    `json:"top_k_per_channel,omitempty"`
	Channels           []string               `json:"channels,omitempty"` // bm25, vector, graph
	Filters            map[string]interface{} `json:"filters,omitempty"`
	IncludeGraph       bool                   `json:"include_graph,omitempty"`
	EnableReranking    bool                   `json:"enable_reranking,omitempty"`
	EnableQueryRewrite bool                   `json:"enable_query_rewrite,omitempty"`
	GraphTraversalHops int                    `json:"graph_traversal_hops,omitempty"`
}

// RAGResponseV2 represents an enhanced RAG query response
type RAGResponseV2 struct {
	Query            string                `json:"query"`
	Answer           string                `json:"answer,omitempty"`
	QueryAnalysis    *QueryAnalysisResult  `json:"query_analysis,omitempty"`
	RewrittenQueries []RewrittenQuery      `json:"rewritten_queries,omitempty"`
	Results          []EnhancedRAGResult   `json:"results"`
	RecallDebug      map[string]interface{} `json:"recall_debug,omitempty"`
	DurationMs       int64                 `json:"duration_ms"`
	TotalTokens      int                   `json:"total_tokens"`
	EstimatedCost    float64               `json:"estimated_cost"`
}

// EnhancedRAGResult represents a single enhanced result
type EnhancedRAGResult struct {
	ChunkID        string                 `json:"chunk_id"`
	DocumentID     string                 `json:"document_id"`
	Title          string                 `json:"title"`
	Content        string                 `json:"content"`
	Score          float64                `json:"score"` // Final score after reranking
	Sources        []string               `json:"sources"` // Channels that found this
	RRFScore       float64                `json:"rrf_score,omitempty"`
	RerankedScore  *float64               `json:"reranked_score,omitempty"`
	RerankReason   string                 `json:"rerank_reason,omitempty"`
	Highlights     []string               `json:"highlights,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
	GraphPath      string                 `json:"graph_path,omitempty"`
	ChunkContext   *ChunkContext          `json:"chunk_context,omitempty"`
}

// Query performs enhanced RAG query with full pipeline
func (s *RAGServiceV2) Query(ctx context.Context, tenantID string, req *RAGRequestV2) (*RAGResponseV2, error) {
	startTime := time.Now()

	// Set defaults
	if req.TopK == 0 {
		req.TopK = 10
	}
	if req.TopKPerChannel == 0 {
		req.TopKPerChannel = 20
	}
	if req.GraphTraversalHops == 0 {
		req.GraphTraversalHops = 2
	}
	if len(req.Channels) == 0 {
		req.Channels = []string{"bm25", "vector", "graph"}
	}
	if !req.IncludeGraph {
		// Remove graph from channels if not requested
		channels := []string{}
		for _, ch := range req.Channels {
			if ch != "graph" {
				channels = append(channels, ch)
			}
		}
		req.Channels = channels
	}

	response := &RAGResponseV2{
		Query: req.Query,
	}

	totalTokens := 0

	// ========================================================================
	// Stage 1: Query Analysis
	// ========================================================================
	fmt.Println("[RAG V2] Stage 1: Query Analysis")
	analysis, analysisUsage, err := s.query.AnalyzeQuery(ctx, tenantID, req.Query)
	if err != nil {
		fmt.Printf("Query analysis error: %v\n", err)
		// Use default analysis
		analysis = &QueryAnalysisResult{
			Intent:     IntentFactual,
			QueryTypes: []string{"section"},
			Keywords:   []string{req.Query},
			Language:   "zh-CN",
			Confidence: 0.5,
		}
	} else {
		response.QueryAnalysis = analysis
		if analysisUsage != nil {
			totalTokens += analysisUsage.TotalTokens
			s.recordLLMUsage(ctx, tenantID, "query_analysis", analysisUsage, "")
		}
	}

	// ========================================================================
	// Stage 2: Query Rewriting (Optional)
	// ========================================================================
	var rewrittenQueries []RewrittenQuery
	if req.EnableQueryRewrite {
		fmt.Println("[RAG V2] Stage 2: Query Rewriting")
		rewrites, rewriteUsage, err := s.query.RewriteQuery(ctx, tenantID, req.Query, analysis)
		if err != nil {
			fmt.Printf("Query rewriting error: %v\n", err)
			// Use original query only
			rewrittenQueries = []RewrittenQuery{{Query: req.Query, Weight: 1.0, Strategy: "original"}}
		} else {
			rewrittenQueries = rewrites
			response.RewrittenQueries = rewrites
			if rewriteUsage != nil {
				totalTokens += rewriteUsage.TotalTokens
				s.recordLLMUsage(ctx, tenantID, "query_rewriting", rewriteUsage, "")
			}
		}
	} else {
		rewrittenQueries = []RewrittenQuery{{Query: req.Query, Weight: 1.0, Strategy: "original"}}
	}

	// Extract keywords
	keywords := s.query.ExtractKeywords(req.Query, analysis.Entities)

	// ========================================================================
	// Stage 3: Multi-Channel Recall
	// ========================================================================
	fmt.Println("[RAG V2] Stage 3: Multi-Channel Recall")
	recallReq := &RecallRequest{
		Query:              req.Query,
		RewrittenQueries:   rewrittenQueries,
		Keywords:           keywords,
		Entities:           analysis.Entities,
		KnowledgeBaseID:    req.KnowledgeBaseID,
		TopKPerChannel:     req.TopKPerChannel,
		Channels:           req.Channels,
		Filters:            req.Filters,
		GraphTraversalHops: req.GraphTraversalHops,
	}

	recallResp, err := s.recall.MultiChannelRecall(ctx, tenantID, recallReq)
	if err != nil {
		return nil, fmt.Errorf("multi-channel recall failed: %w", err)
	}

	response.RecallDebug = recallResp.DebugInfo

	// ========================================================================
	// Stage 4: LLM Reranking (Optional)
	// ========================================================================
	results := recallResp.FusedResults
	if req.EnableReranking && len(results) > 0 {
		fmt.Println("[RAG V2] Stage 4: LLM Reranking")
		reranked, rerankUsage, err := s.reranker.Rerank(ctx, tenantID, req.Query, results, req.TopK)
		if err != nil {
			fmt.Printf("Reranking error: %v\n", err)
			// Use fused results
		} else {
			results = reranked
			if rerankUsage != nil {
				totalTokens += rerankUsage.TotalTokens
				s.recordLLMUsage(ctx, tenantID, "reranking", rerankUsage, "")
			}
		}
	} else {
		// Just take top K from fused results
		if len(results) > req.TopK {
			results = results[:req.TopK]
		}
	}

	// ========================================================================
	// Stage 5: Context Construction
	// ========================================================================
	fmt.Println("[RAG V2] Stage 5: Context Construction")
	enhancedResults := s.buildEnhancedResults(ctx, results)
	response.Results = enhancedResults

	// ========================================================================
	// Stage 6: Answer Generation
	// ========================================================================
	if len(enhancedResults) > 0 {
		fmt.Println("[RAG V2] Stage 6: Answer Generation")
		answer, answerUsage, err := s.generateAnswer(ctx, req.Query, enhancedResults)
		if err != nil {
			fmt.Printf("Answer generation error: %v\n", err)
		} else {
			response.Answer = answer
			if answerUsage != nil {
				totalTokens += answerUsage.TotalTokens
				s.recordLLMUsage(ctx, tenantID, "answer_generation", answerUsage, "")
			}
		}
	}

	// ========================================================================
	// Finalize Response
	// ========================================================================
	duration := time.Since(startTime)
	response.DurationMs = duration.Milliseconds()
	response.TotalTokens = totalTokens
	response.EstimatedCost = s.estimateCost(totalTokens)

	fmt.Printf("[RAG V2] Query completed in %v, tokens: %d, cost: $%.4f\n",
		duration, totalTokens, response.EstimatedCost)

	return response, nil
}

// buildEnhancedResults builds enhanced results with context
func (s *RAGServiceV2) buildEnhancedResults(ctx context.Context, recallResults []RecallResult) []EnhancedRAGResult {
	var enhanced []EnhancedRAGResult

	// Collect chunk IDs
	chunkIDs := make([]string, len(recallResults))
	for i, r := range recallResults {
		chunkIDs[i] = r.ChunkID
	}

	// Batch fetch chunk metadata
	var chunks []models.Chunk
	s.db.WithContext(ctx).
		Where("id IN ?", chunkIDs).
		Find(&chunks)

	chunkMap := make(map[string]*models.Chunk)
	for i := range chunks {
		chunkMap[chunks[i].ID] = &chunks[i]
	}

	// Build enhanced results
	for _, r := range recallResults {
		result := EnhancedRAGResult{
			ChunkID:       r.ChunkID,
			DocumentID:    r.DocumentID,
			Title:         r.Title,
			Content:       r.Content,
			Sources:       []string{r.Channel},
			RRFScore:      r.RRFScore,
			RerankedScore: r.RerankedScore,
			Highlights:    r.Highlights,
			Metadata:      r.Metadata,
			GraphPath:     r.GraphPath,
		}

		// Score priority: Reranked > Original > RRF normalized
		if r.RerankedScore != nil {
			// LLM reranking score (0-100)
			result.Score = *r.RerankedScore
			if reason, ok := r.Metadata["rerank_reason"].(string); ok {
				result.RerankReason = reason
			}
		} else if r.Score > 0 {
			// Use original OpenSearch/Graph score
			result.Score = r.Score
		} else {
			// Fallback: normalize RRF to 0-100 range
			// RRF scores are typically 0.001-0.03, normalize to 0-100
			result.Score = r.RRFScore * 100
		}

		// Add chunk context (prev/next chunks)
		if chunk, ok := chunkMap[r.ChunkID]; ok {
			result.ChunkContext = s.getChunkContext(ctx, chunk)
		}

		enhanced = append(enhanced, result)
	}

	return enhanced
}

// getChunkContext fetches previous and next chunks for context
func (s *RAGServiceV2) getChunkContext(ctx context.Context, chunk *models.Chunk) *ChunkContext {
	context := &ChunkContext{}

	// Get previous chunk
	if chunk.ChunkIndex > 0 {
		var prev models.Chunk
		if err := s.db.WithContext(ctx).
			Where("document_id = ? AND chunk_index = ?", chunk.DocumentID, chunk.ChunkIndex-1).
			Select("content").
			First(&prev).Error; err == nil {
			preview := prev.Content
			if len(preview) > 300 {
				preview = preview[:300] + "..."
			}
			context.PrevContent = preview
		}
	}

	// Get next chunk
	var next models.Chunk
	if err := s.db.WithContext(ctx).
		Where("document_id = ? AND chunk_index = ?", chunk.DocumentID, chunk.ChunkIndex+1).
		Select("content").
		First(&next).Error; err == nil {
		preview := next.Content
		if len(preview) > 300 {
			preview = preview[:300] + "..."
		}
		context.NextContent = preview
	}

	return context
}

// generateAnswer generates AI answer based on retrieved results
func (s *RAGServiceV2) generateAnswer(ctx context.Context, query string, results []EnhancedRAGResult) (string, *LLMUsage, error) {
	// Build context from top results (limit to top 3 to control tokens)
	maxResults := 3
	if len(results) < maxResults {
		maxResults = len(results)
	}

	var contextParts []string
	for i := 0; i < maxResults; i++ {
		r := results[i]
		part := fmt.Sprintf("【文档%d】%s\n内容:\n%s", i+1, r.Title, truncateContent(r.Content, 1500))

		// Add chunk context if available
		if r.ChunkContext != nil {
			if r.ChunkContext.PrevContent != "" {
				part += fmt.Sprintf("\n\n上文: %s", r.ChunkContext.PrevContent)
			}
			if r.ChunkContext.NextContent != "" {
				part += fmt.Sprintf("\n\n下文: %s", r.ChunkContext.NextContent)
			}
		}

		contextParts = append(contextParts, part)
	}

	contextText := strings.Join(contextParts, "\n\n---\n\n")

	systemPrompt := `你是一个知识库智能助手。请根据用户的问题和检索到的文档内容，给出准确、清晰、有条理的回答。

要求：
1. 只基于提供的文档内容回答，不要编造信息
2. 如果文档内容不能完全回答问题，如实说明
3. 使用中文回答（除非问题明确要求其他语言）
4. 回答要有条理，重点突出，适当使用要点列表
5. 在回答末尾简要标注信息来源（引用了哪些文档）
6. 回答要简洁精炼，控制在500字以内`

	userPrompt := fmt.Sprintf("用户问题：%s\n\n检索到的相关文档：\n\n%s", query, contextText)

	return s.embedding.ChatCompletionWithSystem(ctx, systemPrompt, userPrompt)
}

// recordLLMUsage records LLM usage to database
func (s *RAGServiceV2) recordLLMUsage(ctx context.Context, tenantID, method string, usage *LLMUsage, errMsg string) {
	if s.usageTracker == nil || usage == nil {
		return
	}

	s.usageTracker.RecordUsage(ctx, tenantID, nil, nil,
		"rag_v2", method, s.config.LLM.ChatModel, "chat",
		usage, 0, errMsg)
}

// estimateCost estimates total cost based on total tokens
func (s *RAGServiceV2) estimateCost(totalTokens int) float64 {
	// Rough estimate: $0.01 per 1000 tokens (adjust based on actual pricing)
	return float64(totalTokens) * 0.01 / 1000.0
}

func truncateContent(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// ============================================================================
// Hybrid Intent Processing
// ============================================================================

// ProcessHybridQuery handles queries with multiple intents (primary + secondary)
// It executes different strategies based on intent combinations
func (s *RAGServiceV2) ProcessHybridQuery(
	ctx context.Context,
	tenantID string,
	req *RAGRequestV2,
	analysis *models.QueryAnalysisResult,
) (*RAGResponseV2, error) {
	// Determine processing strategy based on intent combination
	primaryIntent := analysis.PrimaryIntent
	hasSecondary := len(analysis.SecondaryIntents) > 0

	switch {
	case primaryIntent == "navigation" && hasIntentType(analysis.SecondaryIntents, "content_search"):
		return s.navigationPlusSearch(ctx, tenantID, req, analysis)
	
	case primaryIntent == "jump" && hasIntentType(analysis.SecondaryIntents, "summarize"):
		return s.jumpPlusSummarize(ctx, tenantID, req, analysis)
	
	case primaryIntent == "outline" && hasIntentType(analysis.SecondaryIntents, "summarize"):
		return s.outlinePlusSummarize(ctx, tenantID, req, analysis)
	
	case primaryIntent == "navigation" && !hasSecondary:
		// Pure navigation - use TOC navigation only
		return s.pureNavigation(ctx, tenantID, req, analysis)
	
	default:
		// Fallback: use default Query method (parallel channel recall)
		return s.Query(ctx, tenantID, req)
	}
}

// navigationPlusSearch: User wants to explore a section AND search for specific content
// Strategy: 
//   1. Use TOC navigation to find relevant sections
//   2. Within those sections, perform content search (BM25 + Vector)
//   3. Combine results with section context
func (s *RAGServiceV2) navigationPlusSearch(
	ctx context.Context,
	tenantID string,
	req *RAGRequestV2,
	analysis *models.QueryAnalysisResult,
) (*RAGResponseV2, error) {
	startTime := time.Now()
	
	// Parse tenantID to int64
	tenantIDInt, err := parseIDToInt64(tenantID)
	if err != nil {
		return nil, fmt.Errorf("invalid tenant_id: %w", err)
	}
	
	// Step 1: TOC Navigation to find target sections
	tocService := NewTOCNavigationService(s.db, s.config)
	
	// Extract navigation keywords from entities and keywords
	navKeywords := analysis.Keywords
	if len(navKeywords) == 0 {
		for _, entity := range analysis.Entities {
			navKeywords = append(navKeywords, entity.Name)
		}
	}
	
	if len(navKeywords) == 0 {
		navKeywords = []string{req.Query}
	}
	
	kbID := ""
	if req.KnowledgeBaseID != nil {
		kbID = *req.KnowledgeBaseID
	}
	
	docMatches, err := tocService.DiscoverDocuments(ctx, req.Query, navKeywords, kbID, tenantIDInt)
	if err != nil {
		fmt.Printf("[navigationPlusSearch] TOC discovery failed: %v\n", err)
		// Fallback to regular query
		return s.Query(ctx, tenantID, req)
	}
	
	// Step 2: Collect chunk IDs from TOC results
	targetChunkIDs := []string{}
	for _, docMatch := range docMatches {
		for _, section := range docMatch.Sections {
			for _, chunkID := range section.ChunkIDs {
				targetChunkIDs = append(targetChunkIDs, fmt.Sprintf("%d", chunkID))
			}
		}
	}
	
	if len(targetChunkIDs) == 0 {
		// No sections found, fallback to regular query
		return s.Query(ctx, tenantID, req)
	}
	
	// Step 3: Within target chunks, perform content search
	contentReq := *req
	if contentReq.Filters == nil {
		contentReq.Filters = make(map[string]interface{})
	}
	contentReq.Filters["chunk_ids"] = targetChunkIDs
	contentReq.Channels = []string{"bm25", "vector"}
	
	contentResponse, err := s.Query(ctx, tenantID, &contentReq)
	if err != nil {
		return nil, fmt.Errorf("content search failed: %w", err)
	}
	
	// Add TOC context to results
	for i := range contentResponse.Results {
		if contentResponse.Results[i].Metadata == nil {
			contentResponse.Results[i].Metadata = make(map[string]interface{})
		}
		contentResponse.Results[i].Metadata["navigation_context"] = "Found via TOC navigation"
	}
	
	contentResponse.QueryAnalysis = convertQueryAnalysisResult(analysis)
	contentResponse.DurationMs = time.Since(startTime).Milliseconds()
	return contentResponse, nil
}

// jumpPlusSummarize: User wants to jump to a specific section AND get a summary
// Strategy:
//   1. Use TOC to locate exact section
//   2. Collect all chunks in that section
//   3. Generate summary with LLM
func (s *RAGServiceV2) jumpPlusSummarize(
	ctx context.Context,
	tenantID string,
	req *RAGRequestV2,
	analysis *models.QueryAnalysisResult,
) (*RAGResponseV2, error) {
	startTime := time.Now()
	
	// Parse tenantID to int64
	tenantIDInt, err := parseIDToInt64(tenantID)
	if err != nil {
		return nil, fmt.Errorf("invalid tenant_id: %w", err)
	}
	
	// Step 1: Locate target section via TOC
	tocService := NewTOCNavigationService(s.db, s.config)
	
	// Use keywords as jump target
	jumpKeywords := analysis.Keywords
	if len(jumpKeywords) == 0 {
		jumpKeywords = []string{req.Query}
	}
	
	kbID := ""
	if req.KnowledgeBaseID != nil {
		kbID = *req.KnowledgeBaseID
	}
	
	docMatches, err := tocService.DiscoverDocuments(ctx, req.Query, jumpKeywords, kbID, tenantIDInt)
	if err != nil || len(docMatches) == 0 {
		return nil, fmt.Errorf("section not found: %v", err)
	}
	
	// Get first matching section
	if len(docMatches[0].Sections) == 0 {
		return nil, fmt.Errorf("no sections in matched document")
	}
	
	firstSection := docMatches[0].Sections[0]
	
	// Step 2: Collect all chunks in the section
	chunkContents := []string{}
	for _, chunkID := range firstSection.ChunkIDs {
		var chunk models.Chunk
		if err := s.db.Where("id = ?", chunkID).First(&chunk).Error; err != nil {
			continue
		}
		chunkContents = append(chunkContents, chunk.Content)
	}
	
	if len(chunkContents) == 0 {
		return nil, fmt.Errorf("no content found in section")
	}
	
	// Step 3: Generate summary
	combinedContent := strings.Join(chunkContents, "\n\n")
	prompt := fmt.Sprintf(
		"请总结以下章节的内容：\n\n%s\n\n请用简洁的语言概括核心要点（200字以内）。",
		truncateContent(combinedContent, 4000),
	)
	
	summary, usage, err := s.embedding.ChatCompletion(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("summary generation failed: %w", err)
	}
	
	// Build response
	results := []EnhancedRAGResult{}
	for _, chunkID := range firstSection.ChunkIDs {
		var chunk models.Chunk
		if err := s.db.Where("id = ?", chunkID).First(&chunk).Error; err != nil {
			continue
		}
		results = append(results, EnhancedRAGResult{
			ChunkID:    chunk.ID,
			DocumentID: chunk.DocumentID,
			Content:    chunk.Content,
			Score:      1.0,
			Sources:    []string{"toc_navigation"},
		})
	}
	
	response := &RAGResponseV2{
		Query:         req.Query,
		Answer:        summary,
		QueryAnalysis: convertQueryAnalysisResult(analysis),
		Results:       results,
		DurationMs:    time.Since(startTime).Milliseconds(),
		TotalTokens:   usage.InputTokens + usage.OutputTokens,
		EstimatedCost: s.estimateCost(usage.InputTokens + usage.OutputTokens),
	}
	
	return response, nil
}

// outlinePlusSummarize: User wants to see document outline AND summaries of each section
// Strategy:
//   1. Get complete TOC structure
//   2. For each section, generate brief summary
//   3. Return hierarchical outline with summaries
func (s *RAGServiceV2) outlinePlusSummarize(
	ctx context.Context,
	tenantID string,
	req *RAGRequestV2,
	analysis *models.QueryAnalysisResult,
) (*RAGResponseV2, error) {
	startTime := time.Now()
	
	// Step 1: Get document ID from query context or KB
	var docID string
	if req.KnowledgeBaseID != nil {
		// Find first document in KB with TOC structure
		var doc models.Document
		if err := s.db.Where("knowledge_base_id = ? AND toc_structure IS NOT NULL", *req.KnowledgeBaseID).
			First(&doc).Error; err != nil {
			return nil, fmt.Errorf("no document with TOC found in KB")
		}
		docID = doc.ID
	} else {
		return nil, fmt.Errorf("knowledge_base_id required for outline query")
	}
	
	// Step 2: Get TOC structure
	var doc models.Document
	if err := s.db.Where("id = ?", docID).First(&doc).Error; err != nil {
		return nil, fmt.Errorf("document not found: %w", err)
	}
	
	var tocNodes []models.TOCNode
	if doc.TOCStructure != nil {
		if nodesData, ok := doc.TOCStructure["nodes"]; ok {
			if nodes, ok := nodesData.([]interface{}); ok {
				for _, node := range nodes {
					if nodeMap, ok := node.(map[string]interface{}); ok {
						tocNode := models.TOCNode{}
						if level, ok := nodeMap["level"].(float64); ok {
							tocNode.Level = int(level)
						}
						if title, ok := nodeMap["title"].(string); ok {
							tocNode.Title = title
						}
						if path, ok := nodeMap["path"].(string); ok {
							tocNode.Path = path
						}
						tocNodes = append(tocNodes, tocNode)
					}
				}
			}
		}
	}
	
	if len(tocNodes) == 0 {
		return nil, fmt.Errorf("document has no TOC structure")
	}
	
	// Step 3: Build outline response
	outlineText := s.buildOutlineText(tocNodes, 0)
	
	response := &RAGResponseV2{
		Query:         req.Query,
		Answer:        fmt.Sprintf("文档大纲：\n\n%s", outlineText),
		QueryAnalysis: convertQueryAnalysisResult(analysis),
		Results:       []EnhancedRAGResult{},
		DurationMs:    time.Since(startTime).Milliseconds(),
	}
	
	return response, nil
}

// pureNavigation: User only wants to navigate/explore structure
// Strategy: Return TOC matches without content search
func (s *RAGServiceV2) pureNavigation(
	ctx context.Context,
	tenantID string,
	req *RAGRequestV2,
	analysis *models.QueryAnalysisResult,
) (*RAGResponseV2, error) {
	startTime := time.Now()
	
	// Parse tenantID to int64
	tenantIDInt, err := parseIDToInt64(tenantID)
	if err != nil {
		return nil, fmt.Errorf("invalid tenant_id: %w", err)
	}
	
	tocService := NewTOCNavigationService(s.db, s.config)
	
	// Use keywords for navigation
	navKeywords := analysis.Keywords
	if len(navKeywords) == 0 {
		navKeywords = []string{req.Query}
	}
	
	kbID := ""
	if req.KnowledgeBaseID != nil {
		kbID = *req.KnowledgeBaseID
	}
	
	docMatches, err := tocService.DiscoverDocuments(ctx, req.Query, navKeywords, kbID, tenantIDInt)
	if err != nil {
		return nil, fmt.Errorf("TOC navigation failed: %w", err)
	}
	
	// Convert to standard format
	results := []EnhancedRAGResult{}
	for _, docMatch := range docMatches {
		for _, section := range docMatch.Sections {
			results = append(results, EnhancedRAGResult{
				DocumentID: docMatch.DocumentID,
				Title:      docMatch.Title,
				Content:    fmt.Sprintf("章节: %s\n路径: %s", section.Title, section.Path),
				Score:      1.0,
				Sources:    []string{"toc_navigation"},
				Metadata: map[string]interface{}{
					"toc_level":  section.Level,
					"toc_path":   section.Path,
					"chunk_ids":  section.ChunkIDs,
					"doc_type":   docMatch.DocType,
				},
			})
		}
	}
	
	response := &RAGResponseV2{
		Query:         req.Query,
		QueryAnalysis: convertQueryAnalysisResult(analysis),
		Results:       results,
		DurationMs:    time.Since(startTime).Milliseconds(),
	}
	
	return response, nil
}

// Helper: Check if a specific intent type exists in secondary intents
func hasIntentType(intents []string, target string) bool {
	for _, intent := range intents {
		if intent == target {
			return true
		}
	}
	return false
}

// Helper: Parse string ID to int64
func parseIDToInt64(id string) (int64, error) {
	var result int64
	_, err := fmt.Sscanf(id, "%d", &result)
	return result, err
}

// Helper: Convert models.QueryAnalysisResult to services.QueryAnalysisResult
func convertQueryAnalysisResult(m *models.QueryAnalysisResult) *QueryAnalysisResult {
	if m == nil {
		return nil
	}
	
	// Convert entities
	entities := make([]Entity, len(m.Entities))
	for i, e := range m.Entities {
		entities[i] = Entity{
			Name: e.Name,
			Type: e.Type,
		}
	}
	
	// Map primary intent to QueryIntent type
	var intent QueryIntent
	switch m.PrimaryIntent {
	case "navigation":
		intent = IntentConceptQuery // Use concept query for navigation
	case "content_search":
		intent = IntentCodeSearch
	case "factual":
		intent = IntentFactual
	default:
		intent = IntentConceptQuery // fallback
	}
	
	return &QueryAnalysisResult{
		Intent:     intent,
		Entities:   entities,
		QueryTypes: m.QueryTypes,
		Keywords:   m.Keywords,
		Language:   m.Language,
		Confidence: m.Confidence,
	}
}

// Helper: Build hierarchical outline text
func (s *RAGServiceV2) buildOutlineText(nodes []models.TOCNode, depth int) string {
	var lines []string
	indent := strings.Repeat("  ", depth)
	
	for _, node := range nodes {
		lines = append(lines, fmt.Sprintf("%s%s. %s", indent, repeatString("·", node.Level), node.Title))
		if len(node.Children) > 0 {
			lines = append(lines, s.buildOutlineText(node.Children, depth+1))
		}
	}
	
	return strings.Join(lines, "\n")
}

func repeatString(s string, count int) string {
	return strings.Repeat(s, count)
}
