package services

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jizhuozhi/knowledge/internal/config"
	"github.com/jizhuozhi/knowledge/internal/models"
	"github.com/jizhuozhi/knowledge/internal/neo4j"
	"github.com/jizhuozhi/knowledge/internal/opensearch"
	"gorm.io/gorm"
)

// RAGService handles RAG retrieval operations
type RAGService struct {
	db           *gorm.DB
	config       *config.Config
	opensearch   *opensearch.Client
	neo4j        *neo4j.Client
	embedding    *EmbeddingService
	usageTracker *LLMUsageTracker
}

// NewRAGService creates a new RAG service
func NewRAGService(db *gorm.DB, cfg *config.Config) *RAGService {
	return &RAGService{
		db:           db,
		config:       cfg,
		embedding:    NewEmbeddingService(cfg),
		usageTracker: NewLLMUsageTracker(db, cfg),
	}
}

// SetOpenSearchClient sets the OpenSearch client
func (s *RAGService) SetOpenSearchClient(client *opensearch.Client) {
	s.opensearch = client
}

// SetNeo4jClient sets the Neo4j client
func (s *RAGService) SetNeo4jClient(client *neo4j.Client) {
	s.neo4j = client
}

// RAGRequest represents a RAG query request
type RAGRequest struct {
	Query           string                 `json:"query" binding:"required"`
	KnowledgeBaseID *string                `json:"knowledge_base_id,omitempty"`
	TopK            int                    `json:"top_k,omitempty"`
	Filters         map[string]interface{} `json:"filters,omitempty"`
	HybridWeight    float64                `json:"hybrid_weight,omitempty"` // text vs vector weight
	IncludeGraph    bool                   `json:"include_graph,omitempty"`
}

// RAGResponse represents a RAG query response
type RAGResponse struct {
	Query         string              `json:"query"`
	Answer        string              `json:"answer,omitempty"`         // AI-generated answer based on retrieved results
	Understanding *QueryUnderstanding `json:"understanding,omitempty"`
	Results       []RAGResult         `json:"results"`
	GraphInfo     *GraphInfo          `json:"graph_info,omitempty"`
	Routing       string              `json:"routing"`
}

// RAGResult represents a single RAG result — document-level after aggregation
type RAGResult struct {
	DocumentID      string                 `json:"document_id"`
	Title           string                 `json:"title"`
	Content         string                 `json:"content"`          // primary/best chunk content
	DocumentSummary string                 `json:"document_summary,omitempty"`
	Chunks          []ResultChunk          `json:"chunks,omitempty"` // all matched chunks from this doc
	Relevance       float64                `json:"relevance"`        // 0-100 human-readable relevance %
	Score           float64                `json:"score"`            // raw RRF score (for debugging)
	Sources         []string               `json:"sources"`          // all channels: text, vector, graph
	DocType         string                 `json:"doc_type"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
	Highlights      []string               `json:"highlights,omitempty"`
	RelatedDocs     []string               `json:"related_docs,omitempty"`
}

// ResultChunk represents a single matched chunk within a document result
type ResultChunk struct {
	ChunkID     string        `json:"chunk_id"`
	Content     string        `json:"content"`
	ChunkIndex  int           `json:"chunk_index"`
	TotalChunks int           `json:"total_chunks"`
	Score       float64       `json:"score"`
	Source      string        `json:"source"` // which channel found this chunk
	Highlights  []string      `json:"highlights,omitempty"`
	Context     *ChunkContext `json:"context,omitempty"`
}

// ChunkContext provides surrounding context for a chunk
type ChunkContext struct {
	PrevContent string `json:"prev_content,omitempty"`
	NextContent string `json:"next_content,omitempty"`
}

// internalChunkResult is used during retrieval before document-level aggregation
type internalChunkResult struct {
	DocumentID string
	ChunkID    string
	Title      string
	Content    string
	Score      float64
	Source     string
	DocType    string
	Metadata   map[string]interface{}
	Highlights []string
}

// GraphInfo contains graph-based information
type GraphInfo struct {
	Entities        []EntityInfo `json:"entities,omitempty"`
	RelatedPaths    []PathInfo   `json:"related_paths,omitempty"`
	RelatedConcepts []string     `json:"related_concepts,omitempty"`
}

// EntityInfo represents entity information
type EntityInfo struct {
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	RelatedTo []string `json:"related_to,omitempty"`
}

// PathInfo represents a knowledge path
type PathInfo struct {
	Source string   `json:"source"`
	Target string   `json:"target"`
	Path   []string `json:"path"`
}

// Query performs RAG query
func (s *RAGService) Query(ctx context.Context, tenantID string, req *RAGRequest) (*RAGResponse, error) {
	startTime := time.Now()

	// Set defaults
	if req.TopK == 0 {
		req.TopK = 10
	}
	if req.HybridWeight == 0 {
		req.HybridWeight = 0.5
	}

	// 1. Query Understanding
	understanding, understandUsage, err := s.embedding.UnderstandQuery(ctx, req.Query)
	if err != nil {
		// Log error but continue with default understanding
		understanding = &QueryUnderstanding{
			Intent:   "search",
			Routing:  "knowledge_base",
			Keywords: []string{req.Query},
		}
	}
	if s.usageTracker != nil {
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		}
		s.usageTracker.RecordUsage(ctx, tenantID, nil, nil,
			"rag", "understandQuery", s.config.LLM.ChatModel, "chat",
			understandUsage, 0, errMsg)
	}

	// 2. Determine retrieval strategy
	strategy, strategyUsage, err := s.embedding.DetermineRetrievalStrategy(ctx, understanding)
	if err != nil {
		strategy = &RetrievalStrategy{
			Channels: map[string]float64{
				"text":   0.5,
				"vector": 0.5,
			},
			TopK:         req.TopK,
			HybridWeight: req.HybridWeight,
		}
	}
	if s.usageTracker != nil {
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		}
		s.usageTracker.RecordUsage(ctx, tenantID, nil, nil,
			"rag", "determineRetrievalStrategy", s.config.LLM.ChatModel, "chat",
			strategyUsage, 0, errMsg)
	}

	// 2.5. Cross-language query translation & HyDE rewriting
	queryVariants := []string{req.Query}
	
	// Language detection and translation
	queryLang := s.detectQueryLanguage(req.Query)
	translatedQuery, needsTranslation := s.translateQueryIfNeeded(ctx, tenantID, queryLang, req)
	if needsTranslation && translatedQuery != "" {
		fmt.Printf("Query translated: %q (%s) -> %q\n", req.Query, queryLang, translatedQuery)
		req.Query = translatedQuery // Use translated query as primary
		queryVariants = append(queryVariants, translatedQuery)
	}
	
	// HyDE: Generate hypothetical answer for better retrieval
	hydeQuery, hydeUsage, hydeErr := s.generateHyDE(ctx, tenantID, req.Query)
	if hydeErr == nil && hydeQuery != "" && hydeQuery != req.Query {
		fmt.Printf("HyDE generated: %q\n", truncate(hydeQuery, 100))
		queryVariants = append(queryVariants, hydeQuery)
	}
	if s.usageTracker != nil && hydeUsage != nil {
		errMsg := ""
		if hydeErr != nil {
			errMsg = hydeErr.Error()
		}
		s.usageTracker.RecordUsage(ctx, tenantID, nil, nil,
			"rag", "generateHyDE", s.config.LLM.ChatModel, "chat",
			hydeUsage, 0, errMsg)
	}

	// 3. Multi-channel retrieval — collect ranked lists per channel
	channelResults := make(map[string][]internalChunkResult)

	// Text retrieval
	if weight, ok := strategy.Channels["text"]; ok && weight > 0 {
		textResults, err := s.textRetrieval(ctx, tenantID, req, strategy)
		if err == nil && len(textResults) > 0 {
			channelResults["text"] = textResults
		}
	}

	// Vector retrieval
	if weight, ok := strategy.Channels["vector"]; ok && weight > 0 {
		vectorResults, err := s.vectorRetrieval(ctx, tenantID, req, strategy)
		if err == nil && len(vectorResults) > 0 {
			channelResults["vector"] = vectorResults
		}
	}

	// Graph retrieval
	var graphInfo *GraphInfo
	if req.IncludeGraph && s.neo4j != nil && req.KnowledgeBaseID != nil {
		if weight, ok := strategy.Channels["graph"]; ok && weight > 0 {
			graphResults, info, err := s.graphRetrieval(ctx, *req.KnowledgeBaseID, req.Query, req.TopK)
			if err == nil && len(graphResults) > 0 {
				channelResults["graph"] = graphResults
				graphInfo = info
			} else if info != nil {
				graphInfo = info
			}
		}
	}

	// 4. RRF merge → document-level aggregation → relevance scoring
	mergedResults := s.rrfMergeAndAggregate(ctx, channelResults, strategy.Channels, req.TopK)
	if mergedResults == nil {
		mergedResults = make([]RAGResult, 0)
	}

	// 5. Rerank (optional, now at document level)
	if len(mergedResults) > 0 {
		mergedResults = s.rerankResults(ctx, req.Query, mergedResults)
	}

	// 6. Log search query
	s.logSearchQuery(ctx, tenantID, req, understanding, len(mergedResults))

	// 7. Generate AI answer based on retrieved results
	answer := ""
	if len(mergedResults) > 0 {
		aiAnswer, answerUsage, err := s.generateAnswer(ctx, req.Query, mergedResults)
		if err != nil {
			fmt.Printf("Warning: failed to generate AI answer: %v\n", err)
		} else {
			answer = aiAnswer
		}
		if s.usageTracker != nil {
			errMsg := ""
			if err != nil {
				errMsg = err.Error()
			}
			s.usageTracker.RecordUsage(ctx, tenantID, nil, nil,
				"rag", "generateAnswer", s.config.LLM.ChatModel, "chat",
				answerUsage, 0, errMsg)
		}
	}

	// Build response
	response := &RAGResponse{
		Query:         req.Query,
		Answer:        answer,
		Understanding: understanding,
		Results:       mergedResults,
		Routing:       understanding.Routing,
		GraphInfo:     graphInfo,
	}

	// Log timing
	elapsed := time.Since(startTime)
	fmt.Printf("RAG query completed in %v\n", elapsed)

	return response, nil
}

// textRetrieval performs text-based retrieval
func (s *RAGService) textRetrieval(ctx context.Context, tenantID string, req *RAGRequest, strategy *RetrievalStrategy) ([]internalChunkResult, error) {
	if s.opensearch == nil {
		return nil, fmt.Errorf("opensearch client not initialized")
	}

	kbIDs, err := s.resolveKBIDs(ctx, tenantID, req.KnowledgeBaseID)
	if err != nil {
		return nil, err
	}

	var allResults []internalChunkResult
	for _, kbID := range kbIDs {
		searchReq := &opensearch.SearchRequest{
			Query:   req.Query,
			Filters: cloneFilters(req.Filters),
			From:    0,
			Size:    strategy.TopK,
		}

		results, err := s.opensearch.Search(ctx, kbID, searchReq)
		if err != nil {
			continue
		}

		for _, r := range results {
			allResults = append(allResults, internalChunkResult{
				DocumentID: r.DocumentID,
				ChunkID:    r.ID,
				Title:      r.Title,
				Content:    r.Content,
				Score:      r.Score,
				Source:     "text",
				Highlights: r.Highlights,
				Metadata:   r.Metadata,
			})
		}
	}

	return allResults, nil
}

// vectorRetrieval performs vector similarity retrieval
func (s *RAGService) vectorRetrieval(ctx context.Context, tenantID string, req *RAGRequest, strategy *RetrievalStrategy) ([]internalChunkResult, error) {
	if s.opensearch == nil {
		return nil, fmt.Errorf("opensearch client not initialized")
	}

	embedding, embUsage, err := s.embedding.GenerateEmbedding(ctx, req.Query)
	if err != nil {
		return nil, fmt.Errorf("failed to generate query embedding: %w", err)
	}
	if s.usageTracker != nil {
		s.usageTracker.RecordUsage(ctx, tenantID, nil, nil,
			"rag", "queryEmbedding", s.config.LLM.EmbeddingModel, "embedding",
			embUsage, 0, "")
	}

	kbIDs, err := s.resolveKBIDs(ctx, tenantID, req.KnowledgeBaseID)
	if err != nil {
		return nil, err
	}

	var allResults []internalChunkResult
	for _, kbID := range kbIDs {
		searchReq := &opensearch.SearchRequest{
			Query:   req.Query,
			Vector:  embedding,
			Filters: req.Filters,
			From:    0,
			Size:    strategy.TopK,
		}

		results, err := s.opensearch.VectorSearch(ctx, kbID, searchReq)
		if err != nil {
			continue
		}

		for _, r := range results {
			allResults = append(allResults, internalChunkResult{
				DocumentID: r.DocumentID,
				ChunkID:    r.ID,
				Title:      r.Title,
				Content:    r.Content,
				Score:      r.Score,
				Source:     "vector",
			})
		}
	}

	return allResults, nil
}

// resolveKBIDs resolves the list of knowledge base IDs to search.
// If a specific KB is requested, returns just that one.
// Otherwise, returns all active KBs for the tenant.
func (s *RAGService) resolveKBIDs(ctx context.Context, tenantID string, kbID *string) ([]string, error) {
	if kbID != nil && *kbID != "" {
		return []string{*kbID}, nil
	}
	// Query all active knowledge bases for this tenant
	var kbs []models.KnowledgeBase
	if err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND status = ?", tenantID, "active").
		Select("id").
		Find(&kbs).Error; err != nil {
		return nil, fmt.Errorf("failed to list knowledge bases: %w", err)
	}
	ids := make([]string, len(kbs))
	for i, kb := range kbs {
		ids[i] = kb.ID
	}
	return ids, nil
}

// graphRetrieval performs graph-based retrieval
func (s *RAGService) graphRetrieval(ctx context.Context, knowledgeBaseID string, query string, topK int) ([]internalChunkResult, *GraphInfo, error) {
	if s.neo4j == nil {
		return nil, nil, fmt.Errorf("neo4j client not initialized")
	}

	entities, err := s.neo4j.SearchByEntityName(ctx, knowledgeBaseID, query, topK)
	if err != nil {
		return nil, nil, err
	}

	var results []internalChunkResult
	graphInfo := &GraphInfo{
		Entities:        []EntityInfo{},
		RelatedPaths:    []PathInfo{},
		RelatedConcepts: []string{},
	}

	seenDocs := make(map[string]bool)

	for _, entity := range entities {
		graphInfo.Entities = append(graphInfo.Entities, EntityInfo{
			Name: entity.Name,
			Type: string(entity.Type),
		})

		related, err := s.neo4j.FindRelatedEntities(ctx, knowledgeBaseID, entity.ID, nil, 2)
		if err == nil {
			for _, r := range related {
				graphInfo.RelatedConcepts = append(graphInfo.RelatedConcepts, r.Name)

				if r.DocumentID != "" && !seenDocs[r.DocumentID] {
					seenDocs[r.DocumentID] = true
					results = append(results, internalChunkResult{
						DocumentID: r.DocumentID,
						Title:      r.Name,
						Content:    fmt.Sprintf("Related entity: %s (%s)", r.Name, r.Type),
						Score:      0.7,
						Source:     "graph",
					})
				}
			}
		}
	}

	graphInfo.RelatedConcepts = uniqueStrings(graphInfo.RelatedConcepts)

	return results, graphInfo, nil
}

// rrfMergeAndAggregate performs RRF fusion across channels, then aggregates
// chunks by document. Returns document-level results with embedded chunk details.
//
// Flow:
//  1. RRF score per chunk across channels
//  2. Group chunks by DocumentID
//  3. Document score = sum of its chunks' RRF scores (multi-chunk = more relevant)
//  4. Convert to 0-100 relevance percentage
//  5. Fetch chunk context and document summaries from DB
func (s *RAGService) rrfMergeAndAggregate(ctx context.Context, channelResults map[string][]internalChunkResult, channelWeights map[string]float64, topK int) []RAGResult {
	const rrfK = 60

	// --- Step 1: RRF scoring per chunk ---
	type chunkEntry struct {
		chunk   internalChunkResult
		rrfSum  float64
		sources []string
	}
	chunkMap := make(map[string]*chunkEntry)

	chunkKey := func(r *internalChunkResult) string {
		// Always use ChunkID if available (it's unique per chunk)
		if r.ChunkID != "" {
			return r.ChunkID
		}
		// Fallback: use hash of full content to avoid false duplicates
		// (using first 64 chars can cause different chunks to collide)
		h := sha256.Sum256([]byte(r.DocumentID + "|" + r.Content))
		return fmt.Sprintf("%x", h[:16]) // 32-char hex string
	}

	for channel, results := range channelResults {
		weight := 1.0
		if w, ok := channelWeights[channel]; ok && w > 0 {
			weight = w
		}
		for rank, r := range results {
			key := chunkKey(&r)
			rrfScore := weight / float64(rrfK+rank+1)

			if existing, ok := chunkMap[key]; ok {
				existing.rrfSum += rrfScore
				existing.sources = append(existing.sources, channel)
				if len(r.Highlights) > len(existing.chunk.Highlights) {
					existing.chunk = r
				}
			} else {
				chunkMap[key] = &chunkEntry{
					chunk:   r,
					rrfSum:  rrfScore,
					sources: []string{channel},
				}
			}
		}
	}

	// --- Step 2: Group by DocumentID ---
	type docGroup struct {
		documentID string
		title      string
		docType    string
		metadata   map[string]interface{}
		chunks     []chunkEntry
		totalRRF   float64
		allSources map[string]bool
	}
	docMap := make(map[string]*docGroup)

	for _, entry := range chunkMap {
		docID := entry.chunk.DocumentID
		group, ok := docMap[docID]
		if !ok {
			group = &docGroup{
				documentID: docID,
				title:      entry.chunk.Title,
				docType:    entry.chunk.DocType,
				metadata:   entry.chunk.Metadata,
				allSources: make(map[string]bool),
			}
			docMap[docID] = group
		}
		group.chunks = append(group.chunks, *entry)
		group.totalRRF += entry.rrfSum
		for _, src := range entry.sources {
			group.allSources[src] = true
		}
	}

	// --- Step 2.5: Deduplicate chunks within each document by content similarity ---
	for _, group := range docMap {
		if len(group.chunks) <= 1 {
			continue
		}
		
		// Build content signature map (first 200 chars)
		type chunkSig struct {
			sig   string
			entry chunkEntry
		}
		var sigs []chunkSig
		for _, c := range group.chunks {
			content := c.chunk.Content
			if len(content) > 200 {
				content = content[:200]
			}
			sigs = append(sigs, chunkSig{sig: content, entry: c})
		}
		
		// Keep unique chunks (first occurrence wins)
		seen := make(map[string]bool)
		var uniqueChunks []chunkEntry
		for _, s := range sigs {
			if !seen[s.sig] {
				seen[s.sig] = true
				uniqueChunks = append(uniqueChunks, s.entry)
			}
		}
		
		group.chunks = uniqueChunks
	}

	// --- Step 3: Sort documents by total RRF score ---
	var docGroups []*docGroup
	for _, g := range docMap {
		// Sort chunks within each document by RRF score desc
		sort.Slice(g.chunks, func(i, j int) bool {
			return g.chunks[i].rrfSum > g.chunks[j].rrfSum
		})
		docGroups = append(docGroups, g)
	}
	sort.Slice(docGroups, func(i, j int) bool {
		return docGroups[i].totalRRF > docGroups[j].totalRRF
	})

	if len(docGroups) > topK {
		docGroups = docGroups[:topK]
	}

	// --- Step 4: Convert RRF to 0-100 relevance ---
	// Max possible RRF for a single chunk appearing in all channels at rank 0:
	// Σ(weight / 61). We use the actual max as the reference.
	maxRRF := 0.0
	for _, g := range docGroups {
		if g.totalRRF > maxRRF {
			maxRRF = g.totalRRF
		}
	}

	// --- Step 5: Fetch chunk context & document summaries ---
	// Collect all chunk IDs and doc IDs
	allChunkIDs := make([]string, 0)
	allDocIDs := make(map[string]bool)
	for _, g := range docGroups {
		allDocIDs[g.documentID] = true
		for _, c := range g.chunks {
			if c.chunk.ChunkID != "" {
				allChunkIDs = append(allChunkIDs, c.chunk.ChunkID)
			}
		}
	}

	// Batch fetch chunk metadata (chunk_index, document_id, total_chunks)
	type chunkMeta struct {
		ID         string `gorm:"column:id"`
		DocID      string `gorm:"column:document_id"`
		ChunkIndex int    `gorm:"column:chunk_index"`
	}
	chunkMetaMap := make(map[string]chunkMeta)
	if len(allChunkIDs) > 0 {
		var metas []chunkMeta
		if err := s.db.WithContext(ctx).
			Table("chunks").
			Select("id, document_id, chunk_index").
			Where("id IN ?", allChunkIDs).
			Find(&metas).Error; err == nil {
			for _, m := range metas {
				chunkMetaMap[m.ID] = m
			}
		}
	}

	// Batch fetch total chunk counts per document
	type docChunkCount struct {
		DocID string `gorm:"column:document_id"`
		Count int    `gorm:"column:cnt"`
	}
	docChunkCounts := make(map[string]int)
	if len(allDocIDs) > 0 {
		docIDList := make([]string, 0, len(allDocIDs))
		for id := range allDocIDs {
			docIDList = append(docIDList, id)
		}
		var counts []docChunkCount
		if err := s.db.WithContext(ctx).
			Table("chunks").
			Select("document_id, COUNT(*) AS cnt").
			Where("document_id IN ? AND deleted_at IS NULL", docIDList).
			Group("document_id").
			Find(&counts).Error; err == nil {
			for _, c := range counts {
				docChunkCounts[c.DocID] = c.Count
			}
		}
	}

	// Batch fetch document summaries
	docSummaries := make(map[string]string)
	if len(allDocIDs) > 0 {
		docIDList := make([]string, 0, len(allDocIDs))
		for id := range allDocIDs {
			docIDList = append(docIDList, id)
		}
		var docs []struct {
			ID      string `gorm:"column:id"`
			Summary string `gorm:"column:summary"`
		}
		if err := s.db.WithContext(ctx).
			Table("documents").
			Select("id, summary").
			Where("id IN ?", docIDList).
			Find(&docs).Error; err == nil {
			for _, d := range docs {
				if d.Summary != "" {
					docSummaries[d.ID] = d.Summary
				}
			}
		}
	}

	// Batch fetch adjacent chunk content for context
	// We need prev and next chunks for each matched chunk
	type adjQuery struct {
		docID      string
		chunkIndex int
	}
	adjContentMap := make(map[string]string) // "docID|chunkIndex" -> content
	var adjQueries []adjQuery
	for _, g := range docGroups {
		for _, c := range g.chunks {
			if meta, ok := chunkMetaMap[c.chunk.ChunkID]; ok {
				if meta.ChunkIndex > 0 {
					adjQueries = append(adjQueries, adjQuery{meta.DocID, meta.ChunkIndex - 1})
				}
				adjQueries = append(adjQueries, adjQuery{meta.DocID, meta.ChunkIndex + 1})
			}
		}
	}
	if len(adjQueries) > 0 {
		// Deduplicate
		seen := make(map[string]bool)
		var unique []adjQuery
		for _, q := range adjQueries {
			key := fmt.Sprintf("%s|%d", q.docID, q.chunkIndex)
			if !seen[key] {
				seen[key] = true
				unique = append(unique, q)
			}
		}
		// Fetch in batches (build OR conditions)
		for _, q := range unique {
			var adj struct {
				Content string `gorm:"column:content"`
			}
			if err := s.db.WithContext(ctx).
				Table("chunks").
				Select("LEFT(content, 300) AS content").
				Where("document_id = ? AND chunk_index = ? AND deleted_at IS NULL",
					q.docID, q.chunkIndex).
				First(&adj).Error; err == nil {
				key := fmt.Sprintf("%s|%d", q.docID, q.chunkIndex)
				adjContentMap[key] = adj.Content
			}
		}
	}

	// --- Step 6: Build final results ---
	var results []RAGResult
	for _, g := range docGroups {
		// Calculate relevance: normalize against max, scale to 0-100
		relevance := 0.0
		if maxRRF > 0 {
			relevance = (g.totalRRF / maxRRF) * 100
		}
		// Ensure minimum 1% for any returned result
		if relevance < 1.0 {
			relevance = 1.0
		}

		sources := make([]string, 0, len(g.allSources))
		for src := range g.allSources {
			sources = append(sources, src)
		}
		sort.Strings(sources)

		// Build chunk details (keep top 3 per document to avoid noise)
		maxChunks := 3
		if len(g.chunks) < maxChunks {
			maxChunks = len(g.chunks)
		}
		var chunkDetails []ResultChunk
		var bestHighlights []string
		for i := 0; i < maxChunks; i++ {
			c := g.chunks[i]
			detail := ResultChunk{
				ChunkID:    c.chunk.ChunkID,
				Content:    c.chunk.Content,
				Score:      c.rrfSum,
				Source:     c.sources[0],
				Highlights: c.chunk.Highlights,
			}

			// Add chunk index info
			if meta, ok := chunkMetaMap[c.chunk.ChunkID]; ok {
				detail.ChunkIndex = meta.ChunkIndex
				detail.TotalChunks = docChunkCounts[meta.DocID]

				// Add adjacent chunk context
				chunkCtx := &ChunkContext{}
				hasContext := false
				prevKey := fmt.Sprintf("%s|%d", meta.DocID, meta.ChunkIndex-1)
				if prev, ok := adjContentMap[prevKey]; ok {
					chunkCtx.PrevContent = prev
					hasContext = true
				}
				nextKey := fmt.Sprintf("%s|%d", meta.DocID, meta.ChunkIndex+1)
				if next, ok := adjContentMap[nextKey]; ok {
					chunkCtx.NextContent = next
					hasContext = true
				}
				if hasContext {
					detail.Context = chunkCtx
				}
			}

			chunkDetails = append(chunkDetails, detail)
			if len(c.chunk.Highlights) > 0 && len(bestHighlights) == 0 {
				bestHighlights = c.chunk.Highlights
			}
		}

		result := RAGResult{
			DocumentID:      g.documentID,
			Title:           g.title,
			Content:         g.chunks[0].chunk.Content, // best chunk as primary content
			DocumentSummary: docSummaries[g.documentID],
			Chunks:          chunkDetails,
			Relevance:       math.Round(relevance*10) / 10, // 1 decimal place
			Score:           g.totalRRF,
			Sources:         sources,
			DocType:         g.docType,
			Metadata:        g.metadata,
			Highlights:      bestHighlights,
		}
		results = append(results, result)
	}

	return results
}

// rerankResults reranks results using LLM
func (s *RAGService) rerankResults(ctx context.Context, query string, results []RAGResult) []RAGResult {
	// For small result sets, use LLM reranking
	if len(results) <= 5 {
		return s.llmRerank(ctx, query, results)
	}

	// For larger sets, use simple recency boost
	return s.recencyBoost(results)
}

// llmRerank uses LLM to rerank results
func (s *RAGService) llmRerank(ctx context.Context, query string, results []RAGResult) []RAGResult {
	// Build prompt with results
	resultText := ""
	for i, r := range results {
		resultText += fmt.Sprintf("\n[%d] Title: %s\nContent: %s\n", i+1, r.Title, truncate(r.Content, 500))
	}

	prompt := fmt.Sprintf(`Rerank the following search results by relevance to the query.

Query: %s

Results:
%s

Return a JSON array of result indices sorted by relevance (most relevant first):
[1, 2, 3, ...]

Return only the JSON array.`, query, resultText)

	response, usage, err := s.embedding.ChatCompletion(ctx, prompt)
	if err != nil {
		return results // Return original on error
	}
	if s.usageTracker != nil {
		s.usageTracker.RecordUsage(ctx, "", nil, nil,
			"rag", "llmRerank", s.config.LLM.ChatModel, "chat",
			usage, 0, "")
	}

	var indices []int
	if err := parseJSONArray(response, &indices); err != nil {
		return results
	}

	// Reorder results
	reranked := make([]RAGResult, len(results))
	for i, idx := range indices {
		if idx > 0 && idx <= len(results) {
			reranked[i] = results[idx-1]
		}
	}

	return reranked
}

// recencyBoost applies recency boost to results
func (s *RAGService) recencyBoost(results []RAGResult) []RAGResult {
	now := time.Now()

	for i := range results {
		if createdAt, ok := results[i].Metadata["created_at"].(string); ok {
			t, err := time.Parse(time.RFC3339, createdAt)
			if err == nil {
				// Decay factor
				ageDays := now.Sub(t).Hours() / 24
				decayFactor := 1.0 / (1.0 + ageDays/30.0) // Half-life of 30 days
				results[i].Score *= decayFactor
			}
		}
	}

	// Re-sort
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results
}

// generateAnswer uses LLM to synthesize an answer from retrieved results
func (s *RAGService) generateAnswer(ctx context.Context, query string, results []RAGResult) (string, *LLMUsage, error) {
	// Build context from top results (limit to top 3 to control token usage)
	maxResults := 3
	if len(results) < maxResults {
		maxResults = len(results)
	}

	var contextParts []string
	for i := 0; i < maxResults; i++ {
		r := results[i]
		part := fmt.Sprintf("【文档%d】%s\n", i+1, r.Title)
		if r.DocumentSummary != "" {
			part += fmt.Sprintf("摘要: %s\n", r.DocumentSummary)
		}
		// Use best chunk content, truncate to avoid token overflow
		content := r.Content
		if len(content) > 1500 {
			content = content[:1500] + "..."
		}
		part += fmt.Sprintf("内容:\n%s", content)

		// Include additional chunks if available
		if len(r.Chunks) > 1 {
			for j := 1; j < len(r.Chunks) && j <= 2; j++ {
				chunkContent := r.Chunks[j].Content
				if len(chunkContent) > 800 {
					chunkContent = chunkContent[:800] + "..."
				}
				part += fmt.Sprintf("\n\n补充片段:\n%s", chunkContent)
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

// detectQueryLanguage detects query language using simple heuristics (fast, no LLM)
func (s *RAGService) detectQueryLanguage(query string) string {
	if query == "" {
		return "unknown"
	}

	var cjkCount, latinCount int
	for _, r := range query {
		switch {
		case (r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified Ideographs
			(r >= 0x3400 && r <= 0x4DBF):    // CJK Extension A
			cjkCount++
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			latinCount++
		}
	}

	if cjkCount > latinCount {
		return "zh"
	} else if latinCount > 0 {
		return "en"
	}
	return "unknown"
}

// translateQueryIfNeeded checks KB language distribution and decides if translation is needed
// Returns (translatedQuery, needsTranslation)
func (s *RAGService) translateQueryIfNeeded(ctx context.Context, tenantID, queryLang string, req *RAGRequest) (string, bool) {
	// Get KB language distribution (pre-computed at index time)
	kbIDs, err := s.resolveKBIDs(ctx, tenantID, req.KnowledgeBaseID)
	if err != nil || len(kbIDs) == 0 {
		return req.Query, false
	}

	var kb struct {
		LanguageDistribution map[string]interface{} `gorm:"column:language_distribution"`
	}
	if err := s.db.WithContext(ctx).
		Table("knowledge_bases").
		Select("language_distribution").
		Where("id = ? AND tenant_id = ?", kbIDs[0], tenantID).
		First(&kb).Error; err != nil || kb.LanguageDistribution == nil {
		return req.Query, false // No distribution data, skip translation
	}

	// Parse distribution
	zhRatio := 0.0
	enRatio := 0.0
	if val, ok := kb.LanguageDistribution["zh"]; ok {
		if f, ok := val.(float64); ok {
			zhRatio = f
		}
	}
	if val, ok := kb.LanguageDistribution["en"]; ok {
		if f, ok := val.(float64); ok {
			enRatio = f
		}
	}

	// Decision rules (fast, no LLM!)
	const threshold = 0.7
	if queryLang == "zh" && enRatio >= threshold {
		// Chinese query, English KB -> translate to English
		translated, _, err := s.translateQuery(ctx, req.Query, "en")
		if err == nil {
			return translated, true
		}
	} else if queryLang == "en" && zhRatio >= threshold {
		// English query, Chinese KB -> translate to Chinese
		translated, _, err := s.translateQuery(ctx, req.Query, "zh")
		if err == nil {
			return translated, true
		}
	}

	return req.Query, false
}

// translateQuery translates query to target language using LLM
func (s *RAGService) translateQuery(ctx context.Context, query, targetLang string) (string, *LLMUsage, error) {
	langName := "English"
	if targetLang == "zh" {
		langName = "Chinese"
	}

	prompt := fmt.Sprintf(`Translate the following query to %s. Return ONLY the translated query, no explanations.

Query: %s

Translation:`, langName, query)

	response, usage, err := s.embedding.ChatCompletion(ctx, prompt)
	if err != nil {
		return query, usage, err
	}

	translated := strings.TrimSpace(response)
	if translated == "" {
		return query, usage, fmt.Errorf("empty translation")
	}

	return translated, usage, nil
}

// generateHyDE generates a hypothetical answer to improve retrieval (HyDE technique)
func (s *RAGService) generateHyDE(ctx context.Context, tenantID, query string) (string, *LLMUsage, error) {
	prompt := fmt.Sprintf(`Generate a brief, direct answer (2-3 sentences) to this question as if you were retrieving from a knowledge base. This will be used for semantic search.

Question: %s

Hypothetical Answer:`, query)

	response, usage, err := s.embedding.ChatCompletion(ctx, prompt)
	if err != nil {
		return "", usage, err
	}

	hypothetical := strings.TrimSpace(response)
	return hypothetical, usage, nil
}

// logSearchQuery logs the search query for analytics (log only, no DB persistence)
func (s *RAGService) logSearchQuery(ctx context.Context, tenantID string, req *RAGRequest, understanding *QueryUnderstanding, resultCount int) {
	fmt.Printf("RAG query: tenant=%s query=%q intent=%s routing=%s results=%d\n",
		tenantID, truncate(req.Query, 80), understanding.Intent, understanding.Routing, resultCount)
}

// Helper functions
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func uniqueStrings(s []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}
	return result
}

func parseJSONArray(s string, v interface{}) error {
	trimmed := strings.TrimSpace(s)
	if strings.HasPrefix(trimmed, "```") {
		trimmed = strings.TrimPrefix(trimmed, "```")
		trimmed = strings.TrimPrefix(trimmed, "json")
		trimmed = strings.TrimSpace(trimmed)
		trimmed = strings.TrimSuffix(trimmed, "```")
		trimmed = strings.TrimSpace(trimmed)
	}
	return json.Unmarshal([]byte(trimmed), v)
}

func cloneFilters(src map[string]interface{}) map[string]interface{} {
	if src == nil {
		return map[string]interface{}{}
	}
	dst := make(map[string]interface{}, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
