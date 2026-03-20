package services

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/jizhuozhi/knowledge/internal/config"
	"github.com/jizhuozhi/knowledge/internal/models"
	"github.com/jizhuozhi/knowledge/internal/neo4j"
	"github.com/jizhuozhi/knowledge/internal/opensearch"
	"gorm.io/gorm"
)

// RecallService handles multi-channel retrieval and fusion
type RecallService struct {
	db           *gorm.DB
	config       *config.Config
	opensearch   *opensearch.Client
	neo4j        *neo4j.Client
	embedding    *EmbeddingService
	usageTracker *LLMUsageTracker
}

// NewRecallService creates a new recall service
func NewRecallService(db *gorm.DB, cfg *config.Config) *RecallService {
	return &RecallService{
		db:           db,
		config:       cfg,
		embedding:    NewEmbeddingService(cfg),
		usageTracker: NewLLMUsageTracker(db, cfg),
	}
}

// SetOpenSearchClient sets the OpenSearch client
func (s *RecallService) SetOpenSearchClient(client *opensearch.Client) {
	s.opensearch = client
}

// SetNeo4jClient sets the Neo4j client
func (s *RecallService) SetNeo4jClient(client *neo4j.Client) {
	s.neo4j = client
}

// RecallRequest represents a multi-channel recall request
type RecallRequest struct {
	Query              string
	RewrittenQueries   []RewrittenQuery
	Keywords           []string
	Entities           []Entity
	KnowledgeBaseID    *string
	TopKPerChannel     int
	Channels           []string // bm25, vector, graph
	Filters            map[string]interface{}
	GraphTraversalHops int
}

// RecallResult represents a single recall result from one channel
type RecallResult struct {
	ChunkID        string
	DocumentID     string
	Title          string
	Content        string
	Score          float64
	Rank           int
	Channel        string // bm25, vector, graph
	ChunkIndexID   *string
	Highlights     []string
	Metadata       map[string]interface{}
	GraphPath      string // For graph results
	RRFScore       float64
	FusedRank      int
	RerankedScore  *float64
	FinalRank      *int
}

// RecallResponse represents the complete recall response
type RecallResponse struct {
	BM25Results   []RecallResult
	VectorResults []RecallResult
	GraphResults  []RecallResult
	FusedResults  []RecallResult
	TotalHits     int
	DebugInfo     map[string]interface{}
}

// MultiChannelRecall performs retrieval across all enabled channels
func (s *RecallService) MultiChannelRecall(ctx context.Context, tenantID string, req *RecallRequest) (*RecallResponse, error) {
	if req.TopKPerChannel == 0 {
		req.TopKPerChannel = 20
	}
	if req.GraphTraversalHops == 0 {
		req.GraphTraversalHops = 2
	}
	if len(req.Channels) == 0 {
		req.Channels = []string{"bm25", "vector", "graph"}
	}

	response := &RecallResponse{
		DebugInfo: make(map[string]interface{}),
	}

	// Parallel retrieval
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, channel := range req.Channels {
		switch channel {
		case "bm25":
			wg.Add(1)
			go func() {
				defer wg.Done()
				results, err := s.BM25Recall(ctx, tenantID, req)
				if err != nil {
					fmt.Printf("BM25 recall error: %v\n", err)
					return
				}
				mu.Lock()
				response.BM25Results = results
				response.DebugInfo["bm25_hits"] = len(results)
				mu.Unlock()
			}()

		case "vector":
			wg.Add(1)
			go func() {
				defer wg.Done()
				results, err := s.VectorRecall(ctx, tenantID, req)
				if err != nil {
					fmt.Printf("Vector recall error: %v\n", err)
					return
				}
				mu.Lock()
				response.VectorResults = results
				response.DebugInfo["vector_hits"] = len(results)
				mu.Unlock()
			}()

		case "graph":
			if s.neo4j != nil && req.KnowledgeBaseID != nil {
				wg.Add(1)
				go func() {
					defer wg.Done()
					results, err := s.GraphRecall(ctx, *req.KnowledgeBaseID, req)
					if err != nil {
						fmt.Printf("Graph recall error: %v\n", err)
						return
					}
					mu.Lock()
					response.GraphResults = results
					response.DebugInfo["graph_hits"] = len(results)
					mu.Unlock()
				}()
			}
		}
	}

	wg.Wait()

	// RRF Fusion
	response.FusedResults = s.RRFFusion(response.BM25Results, response.VectorResults, response.GraphResults, req.TopKPerChannel)
	response.DebugInfo["fusion_method"] = "rrf"
	response.DebugInfo["rrf_k"] = 60
	response.TotalHits = len(response.FusedResults)

	return response, nil
}

// BM25Recall performs BM25 keyword-based retrieval
func (s *RecallService) BM25Recall(ctx context.Context, tenantID string, req *RecallRequest) ([]RecallResult, error) {
	if s.opensearch == nil {
		return nil, fmt.Errorf("opensearch client not initialized")
	}

	kbIDs, err := s.resolveKBIDs(ctx, tenantID, req.KnowledgeBaseID)
	if err != nil {
		return nil, err
	}

	var allResults []RecallResult
	seenChunks := make(map[string]bool)

	for _, kbID := range kbIDs {
		searchReq := &opensearch.SearchRequest{
			Query:   req.Query,
			Filters: req.Filters,
			From:    0,
			Size:    req.TopKPerChannel,
		}

		results, err := s.opensearch.Search(ctx, kbID, searchReq)
		if err != nil {
			continue
		}

		for rank, r := range results {
			if seenChunks[r.ID] {
				continue
			}
			seenChunks[r.ID] = true

			allResults = append(allResults, RecallResult{
				ChunkID:    r.ID,
				DocumentID: r.DocumentID,
				Title:      r.Title,
				Content:    r.Content,
				Score:      r.Score,
				Rank:       rank + 1,
				Channel:    "bm25",
				Highlights: r.Highlights,
				Metadata:   r.Metadata,
			})
		}
	}

	// Sort by score and update ranks
	sort.Slice(allResults, func(i, j int) bool {
		return allResults[i].Score > allResults[j].Score
	})
	for i := range allResults {
		allResults[i].Rank = i + 1
	}

	if len(allResults) > req.TopKPerChannel {
		allResults = allResults[:req.TopKPerChannel]
	}

	return allResults, nil
}

// VectorRecall performs vector similarity retrieval
func (s *RecallService) VectorRecall(ctx context.Context, tenantID string, req *RecallRequest) ([]RecallResult, error) {
	if s.opensearch == nil {
		return nil, fmt.Errorf("opensearch client not initialized")
	}

	kbIDs, err := s.resolveKBIDs(ctx, tenantID, req.KnowledgeBaseID)
	if err != nil {
		return nil, err
	}

	// Aggregate results from all rewritten queries
	aggregatedScores := make(map[string]*RecallResult) // chunkID -> result

	// Use rewritten queries if available, otherwise use original
	queries := req.RewrittenQueries
	if len(queries) == 0 {
		queries = []RewrittenQuery{{Query: req.Query, Weight: 1.0, Strategy: "original"}}
	}

	for _, rq := range queries {
		// Generate embedding
		embedding, usage, err := s.embedding.GenerateEmbedding(ctx, rq.Query)
		if err != nil {
			fmt.Printf("Failed to generate embedding for query '%s': %v\n", rq.Query, err)
			continue
		}
		if s.usageTracker != nil {
			s.usageTracker.RecordUsage(ctx, tenantID, nil, nil,
				"recall", "queryEmbedding", s.config.LLM.EmbeddingModel, "embedding",
				usage, 0, "")
		}

		for _, kbID := range kbIDs {
			searchReq := &opensearch.SearchRequest{
				Query:   rq.Query,
				Vector:  embedding,
				Filters: req.Filters,
				From:    0,
				Size:    req.TopKPerChannel,
			}

			results, err := s.opensearch.VectorSearch(ctx, kbID, searchReq)
			if err != nil {
				continue
			}

			for _, r := range results {
				weightedScore := r.Score * rq.Weight

				if existing, ok := aggregatedScores[r.ID]; ok {
					// Take max score across queries
					if weightedScore > existing.Score {
						existing.Score = weightedScore
					}
				} else {
					aggregatedScores[r.ID] = &RecallResult{
						ChunkID:    r.ID,
						DocumentID: r.DocumentID,
						Title:      r.Title,
						Content:    r.Content,
						Score:      weightedScore,
						Channel:    "vector",
						Metadata:   r.Metadata,
					}
				}
			}
		}
	}

	// Convert map to slice and sort
	allResults := make([]RecallResult, 0, len(aggregatedScores))
	for _, result := range aggregatedScores {
		allResults = append(allResults, *result)
	}

	sort.Slice(allResults, func(i, j int) bool {
		return allResults[i].Score > allResults[j].Score
	})

	// Update ranks
	for i := range allResults {
		allResults[i].Rank = i + 1
	}

	if len(allResults) > req.TopKPerChannel {
		allResults = allResults[:req.TopKPerChannel]
	}

	return allResults, nil
}

// GraphRecall performs graph-based retrieval
func (s *RecallService) GraphRecall(ctx context.Context, knowledgeBaseID string, req *RecallRequest) ([]RecallResult, error) {
	if s.neo4j == nil {
		return nil, fmt.Errorf("neo4j client not initialized")
	}

	var allResults []RecallResult
	seenChunks := make(map[string]float64) // chunkID -> score

	// Search for entities matching the query
	entities, err := s.neo4j.SearchByEntityName(ctx, knowledgeBaseID, req.Query, 10)
	if err != nil {
		return nil, err
	}

	// For each entity, traverse the graph to find related chunks
	for _, entity := range entities {
		// Find related entities within N hops
		related, err := s.neo4j.FindRelatedEntities(ctx, knowledgeBaseID, entity.ID, nil, req.GraphTraversalHops)
		if err != nil {
			continue
		}

		for distance, relatedEntity := range related {
			// Calculate score based on graph distance (closer = higher score)
			score := 1.0 / float64(distance+1)

			// Get chunk associated with this entity
			var chunks []models.Chunk
			query := s.db.WithContext(ctx).Table("chunks")

			if relatedEntity.DocumentID != "" {
				query = query.Where("document_id = ?", relatedEntity.DocumentID)
			}

			if err := query.Limit(1).Find(&chunks).Error; err != nil || len(chunks) == 0 {
				continue
			}

			chunk := chunks[0]

			// Aggregate scores for the same chunk
			if existingScore, ok := seenChunks[chunk.ID]; ok {
				seenChunks[chunk.ID] = existingScore + score
			} else {
				seenChunks[chunk.ID] = score
			}
		}
	}

	// Fetch chunk details and build results
	if len(seenChunks) > 0 {
		chunkIDs := make([]string, 0, len(seenChunks))
		for id := range seenChunks {
			chunkIDs = append(chunkIDs, id)
		}

		var chunks []models.Chunk
		var documents []models.Document
		
		// Fetch chunks
		if err := s.db.WithContext(ctx).
			Where("id IN ?", chunkIDs).
			Find(&chunks).Error; err == nil {

			// Fetch related documents
			docIDs := make([]string, len(chunks))
			for i, chunk := range chunks {
				docIDs[i] = chunk.DocumentID
			}
			
			s.db.WithContext(ctx).
				Where("id IN ?", docIDs).
				Find(&documents)
			
			docMap := make(map[string]*models.Document)
			for i := range documents {
				docMap[documents[i].ID] = &documents[i]
			}

			for _, chunk := range chunks {
				title := ""
				if doc, ok := docMap[chunk.DocumentID]; ok && doc.Title != "" {
					title = doc.Title
				}

				allResults = append(allResults, RecallResult{
					ChunkID:    chunk.ID,
					DocumentID: chunk.DocumentID,
					Title:      title,
					Content:    chunk.Content,
					Score:      seenChunks[chunk.ID],
					Channel:    "graph",
					GraphPath:  fmt.Sprintf("Entity -> %dhops -> Chunk", req.GraphTraversalHops),
				})
			}
		}
	}

	// Sort by score
	sort.Slice(allResults, func(i, j int) bool {
		return allResults[i].Score > allResults[j].Score
	})

	// Update ranks
	for i := range allResults {
		allResults[i].Rank = i + 1
	}

	if len(allResults) > req.TopKPerChannel {
		allResults = allResults[:req.TopKPerChannel]
	}

	return allResults, nil
}

// RRFFusion performs Reciprocal Rank Fusion across multiple channels
func (s *RecallService) RRFFusion(bm25Results, vectorResults, graphResults []RecallResult, topK int) []RecallResult {
	const rrfK = 60

	// Aggregate scores by chunk ID
	aggregated := make(map[string]*RecallResult)

	addResults := func(results []RecallResult) {
		for _, r := range results {
			rrfScore := 1.0 / float64(rrfK+r.Rank)

			if existing, ok := aggregated[r.ChunkID]; ok {
				// Accumulate RRF score
				existing.RRFScore += rrfScore
				// Keep the highest original score across channels
				if r.Score > existing.Score {
					existing.Score = r.Score
					existing.Channel = r.Channel // Update channel to the one with highest score
				}
			} else {
				r.RRFScore = rrfScore
				aggregated[r.ChunkID] = &r
			}
		}
	}

	addResults(bm25Results)
	addResults(vectorResults)
	addResults(graphResults)

	// Convert to slice
	fused := make([]RecallResult, 0, len(aggregated))
	for _, result := range aggregated {
		fused = append(fused, *result)
	}

	// Sort by RRF score
	sort.Slice(fused, func(i, j int) bool {
		return fused[i].RRFScore > fused[j].RRFScore
	})

	// Update fused ranks
	for i := range fused {
		fused[i].FusedRank = i + 1
	}

	// Debug: Log top 3 results
	if len(fused) > 0 {
		fmt.Printf("[RRF Fusion] Top 3 results:\n")
		for i := 0; i < min(3, len(fused)); i++ {
			fmt.Printf("  [%d] ChunkID=%s Channel=%s OrigScore=%.4f RRFScore=%.4f\n",
				i+1, fused[i].ChunkID, fused[i].Channel, fused[i].Score, fused[i].RRFScore)
		}
	}

	if len(fused) > topK {
		fused = fused[:topK]
	}

	return fused
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// resolveKBIDs resolves knowledge base IDs to search
func (s *RecallService) resolveKBIDs(ctx context.Context, tenantID string, kbID *string) ([]string, error) {
	if kbID != nil && *kbID != "" {
		return []string{*kbID}, nil
	}

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
