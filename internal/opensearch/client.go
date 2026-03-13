package opensearch

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jizhuozhi/knowledge/internal/config"
	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

const (
	TextIndexPrefix   = "knowledge_text_"
	VectorIndexPrefix = "knowledge_vector_"
)

// Client wraps OpenSearch client
type Client struct {
	client      *opensearch.Client
	textIndex   string
	vectorIndex string
}

// IndexConfig represents index configuration
type IndexConfig struct {
	NumberOfShards   int `json:"number_of_shards"`
	NumberOfReplicas int `json:"number_of_replicas"`
}

// VectorMapping represents vector field mapping
type VectorMapping struct {
	Type      string `json:"type"`
	Dimension int    `json:"dimension"`
	Method    Method `json:"method"`
}

// Method represents vector index method
type Method struct {
	Name       string     `json:"name"`
	Engine     string     `json:"engine"`
	SpaceType  string     `json:"space_type"`
	Parameters Parameters `json:"parameters"`
}

// Parameters represents vector index parameters
type Parameters struct {
	EFConstruction int `json:"ef_construction"`
	M              int `json:"m"`
}

// NewClient creates a new OpenSearch client
func NewClient(cfg config.OpenSearchConfig, embeddingDim int) (*Client, error) {
	client, err := opensearch.NewClient(opensearch.Config{
		Addresses: []string{cfg.Address()},
		Username:  cfg.User,
		Password:  cfg.Password,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: !cfg.VerifyCerts,
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create opensearch client: %w", err)
	}

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := opensearchapi.InfoRequest{}
	_, err = req.Do(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to opensearch: %w", err)
	}

	return &Client{
		client:      client,
		textIndex:   TextIndexPrefix + "default",
		vectorIndex: VectorIndexPrefix + "default",
	}, nil
}

// kbIndexName returns the index name for a knowledge base using string ID
func kbIndexName(prefix string, knowledgeBaseID string) string {
	return fmt.Sprintf("%s%s", prefix, knowledgeBaseID)
}

// CreateKBIndices creates text and vector indices for a knowledge base
func (c *Client) CreateKBIndices(knowledgeBaseID string, embeddingDim int) error {
	ctx := context.Background()

	textIndexName := kbIndexName(TextIndexPrefix, knowledgeBaseID)
	if err := c.createTextIndex(ctx, textIndexName); err != nil {
		return fmt.Errorf("failed to create text index: %w", err)
	}

	vectorIndexName := kbIndexName(VectorIndexPrefix, knowledgeBaseID)
	if err := c.createVectorIndex(ctx, vectorIndexName, embeddingDim); err != nil {
		return fmt.Errorf("failed to create vector index: %w", err)
	}

	return nil
}

// createTextIndex creates a text search index
func (c *Client) createTextIndex(ctx context.Context, indexName string) error {
	mapping := map[string]interface{}{
		"settings": IndexConfig{
			NumberOfShards:   3,
			NumberOfReplicas: 1,
		},
		"mappings": map[string]interface{}{
			"properties": map[string]interface{}{
				"tenant_id": map[string]interface{}{
					"type": "keyword",
				},
				"document_id": map[string]interface{}{
					"type": "keyword",
				},
				"chunk_id": map[string]interface{}{
					"type": "keyword",
				},
				"knowledge_base_id": map[string]interface{}{
					"type": "keyword",
				},
				"content": map[string]interface{}{
					"type":     "text",
					"analyzer": "ik_max_word",
					"fields": map[string]interface{}{
						"keyword": map[string]interface{}{
							"type": "keyword",
						},
					},
				},
				"title": map[string]interface{}{
					"type":     "text",
					"analyzer": "ik_max_word",
				},
				"doc_type": map[string]interface{}{
					"type": "keyword",
				},
				"tags": map[string]interface{}{
					"type": "keyword",
				},
				"created_at": map[string]interface{}{
					"type": "date",
				},
				"updated_at": map[string]interface{}{
					"type": "date",
				},
				"metadata": map[string]interface{}{
					"type":    "object",
					"enabled": true,
				},
			},
		},
	}

	body, err := json.Marshal(mapping)
	if err != nil {
		return err
	}

	req := opensearchapi.IndicesCreateRequest{
		Index: indexName,
		Body:  strings.NewReader(string(body)),
	}

	resp, err := req.Do(ctx, c.client)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 400 {
		var errResp struct {
			Error struct {
				Type string `json:"type"`
			} `json:"error"`
		}
		if decodeErr := json.NewDecoder(resp.Body).Decode(&errResp); decodeErr == nil {
			if errResp.Error.Type == "resource_already_exists_exception" {
				return nil
			}
		}
		return fmt.Errorf("failed to create text index (status %d)", resp.StatusCode)
	}

	return nil
}

// createVectorIndex creates a vector search index
func (c *Client) createVectorIndex(ctx context.Context, indexName string, dimension int) error {
	mapping := map[string]interface{}{
		"settings": IndexConfig{
			NumberOfShards:   3,
			NumberOfReplicas: 1,
		},
		"mappings": map[string]interface{}{
			"properties": map[string]interface{}{
				"tenant_id": map[string]interface{}{
					"type": "keyword",
				},
				"document_id": map[string]interface{}{
					"type": "keyword",
				},
				"knowledge_base_id": map[string]interface{}{
					"type": "keyword",
				},
				"chunk_id": map[string]interface{}{
					"type": "keyword",
				},
				"title": map[string]interface{}{
					"type": "text",
				},
				"content": map[string]interface{}{
					"type": "text",
				},
				"embedding": map[string]interface{}{
					"type":      "knn_vector",
					"dimension": dimension,
					"method": map[string]interface{}{
						"name":       "hnsw",
						"engine":     "nmslib",
						"space_type": "cosinesimil",
						"parameters": map[string]interface{}{
							"ef_construction": 256,
							"m":               16,
						},
					},
				},
			},
		},
	}

	body, err := json.Marshal(mapping)
	if err != nil {
		return err
	}

	req := opensearchapi.IndicesCreateRequest{
		Index: indexName,
		Body:  strings.NewReader(string(body)),
	}

	resp, err := req.Do(ctx, c.client)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 400 {
		var errResp struct {
			Error struct {
				Type string `json:"type"`
			} `json:"error"`
		}
		if decodeErr := json.NewDecoder(resp.Body).Decode(&errResp); decodeErr == nil {
			if errResp.Error.Type == "resource_already_exists_exception" {
				return nil
			}
		}
		return fmt.Errorf("failed to create vector index (status %d)", resp.StatusCode)
	}

	return nil
}

// IndexDocument indexes a document
func (c *Client) IndexDocument(ctx context.Context, knowledgeBaseID string, doc *DocumentIndex) error {
	indexName := kbIndexName(TextIndexPrefix, knowledgeBaseID)

	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}

	req := opensearchapi.IndexRequest{
		Index:      indexName,
		DocumentID: doc.ID,
		Body:       strings.NewReader(string(body)),
		Refresh:    "false",
	}

	_, err = req.Do(ctx, c.client)
	return err
}

// IndexVector indexes a document with vector embedding
func (c *Client) IndexVector(ctx context.Context, knowledgeBaseID string, vec *VectorIndex) error {
	indexName := kbIndexName(VectorIndexPrefix, knowledgeBaseID)

	body, err := json.Marshal(vec)
	if err != nil {
		return err
	}

	req := opensearchapi.IndexRequest{
		Index:      indexName,
		DocumentID: vec.ID,
		Body:       strings.NewReader(string(body)),
		Refresh:    "false",
	}

	_, err = req.Do(ctx, c.client)
	return err
}

// SearchRequest represents a search request
type SearchRequest struct {
	Query        string                 `json:"query"`
	Vector       []float32              `json:"vector,omitempty"`
	Filters      map[string]interface{} `json:"filters,omitempty"`
	From         int                    `json:"from"`
	Size         int                    `json:"size"`
	HybridWeight float64                `json:"hybrid_weight,omitempty"`
}

// SearchResult represents a search result
type SearchResult struct {
	ID         string                 `json:"id"`
	DocumentID string                 `json:"document_id"`
	Title      string                 `json:"title"`
	Content    string                 `json:"content"`
	Score      float64                `json:"score"`
	Source     string                 `json:"source"`
	Highlights []string               `json:"highlights,omitempty"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

// DocumentIndex represents a document to index
type DocumentIndex struct {
	ID              string                 `json:"id"`
	TenantID        string                 `json:"tenant_id"`
	DocumentID      string                 `json:"document_id"`
	KnowledgeBaseID string                 `json:"knowledge_base_id"`
	Title           string                 `json:"title"`
	Content         string                 `json:"content"`
	DocType         string                 `json:"doc_type"`
	Tags            []string               `json:"tags"`
	Metadata        map[string]interface{} `json:"metadata"`
	CreatedAt       time.Time              `json:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at"`
}

// VectorIndex represents a vector to index
type VectorIndex struct {
	ID              string    `json:"id"`
	TenantID        string    `json:"tenant_id"`
	DocumentID      string    `json:"document_id"`
	KnowledgeBaseID string    `json:"knowledge_base_id"`
	Title           string    `json:"title"`
	Content         string    `json:"content"`
	Embedding       []float32 `json:"embedding"`
}

// TextIndexSample represents text index debug payload
type TextIndexSample struct {
	ID              string                 `json:"id"`
	DocumentID      string                 `json:"document_id"`
	KnowledgeBaseID string                 `json:"knowledge_base_id"`
	Title           string                 `json:"title"`
	Content         string                 `json:"content"`
	DocType         string                 `json:"doc_type"`
	UpdatedAt       time.Time              `json:"updated_at"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
}

// VectorIndexSample represents vector index debug payload
type VectorIndexSample struct {
	ID               string    `json:"id"`
	DocumentID       string    `json:"document_id"`
	KnowledgeBaseID  string    `json:"knowledge_base_id"`
	Title            string    `json:"title"`
	Content          string    `json:"content"`
	EmbeddingDim     int       `json:"embedding_dim"`
	EmbeddingPreview []float32 `json:"embedding_preview"`
}

// Search performs text search — consistently uses kbIndexName
func (c *Client) Search(ctx context.Context, knowledgeBaseID string, req *SearchRequest) ([]SearchResult, error) {
	indexName := kbIndexName(TextIndexPrefix, knowledgeBaseID)

	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{
						"multi_match": map[string]interface{}{
							"query":  req.Query,
							"fields": []string{"title^2", "content"},
						},
					},
				},
				"filter": c.buildFilters(req.Filters),
			},
		},
		"from": req.From,
		"size": req.Size,
		"highlight": map[string]interface{}{
			"fields": map[string]interface{}{
				"content": map[string]interface{}{},
			},
		},
	}

	body, err := json.Marshal(query)
	if err != nil {
		return nil, err
	}

	searchReq := opensearchapi.SearchRequest{
		Index: []string{indexName},
		Body:  strings.NewReader(string(body)),
	}

	resp, err := searchReq.Do(ctx, c.client)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return c.parseSearchResult(result), nil
}

// VectorSearch performs vector similarity search — consistently uses kbIndexName
func (c *Client) VectorSearch(ctx context.Context, knowledgeBaseID string, req *SearchRequest) ([]SearchResult, error) {
	indexName := kbIndexName(VectorIndexPrefix, knowledgeBaseID)

	knnQuery := map[string]interface{}{
		"vector": req.Vector,
		"k":      req.Size,
	}
	if filterClauses := c.buildFilters(req.Filters); len(filterClauses) > 0 {
		knnQuery["filter"] = map[string]interface{}{
			"bool": map[string]interface{}{
				"filter": filterClauses,
			},
		}
	}

	query := map[string]interface{}{
		"size": req.Size,
		"query": map[string]interface{}{
			"knn": map[string]interface{}{
				"embedding": knnQuery,
			},
		},
	}

	body, err := json.Marshal(query)
	if err != nil {
		return nil, err
	}

	searchReq := opensearchapi.SearchRequest{
		Index: []string{indexName},
		Body:  strings.NewReader(string(body)),
	}

	resp, err := searchReq.Do(ctx, c.client)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return c.parseSearchResult(result), nil
}

// HybridSearch performs hybrid text + vector search
func (c *Client) HybridSearch(ctx context.Context, knowledgeBaseID string, req *SearchRequest) ([]SearchResult, error) {
	textCh := make(chan []SearchResult, 1)
	vectorCh := make(chan []SearchResult, 1)
	errCh := make(chan error, 2)

	go func() {
		results, err := c.Search(ctx, knowledgeBaseID, req)
		if err != nil {
			errCh <- err
			return
		}
		textCh <- results
	}()

	go func() {
		if req.Vector == nil {
			vectorCh <- []SearchResult{}
			return
		}
		results, err := c.VectorSearch(ctx, knowledgeBaseID, req)
		if err != nil {
			errCh <- err
			return
		}
		vectorCh <- results
	}()

	select {
	case err := <-errCh:
		return nil, err
	case textResults := <-textCh:
		vectorResults := <-vectorCh
		return c.mergeResults(textResults, vectorResults, req.HybridWeight), nil
	}
}

// buildFilters builds filter conditions
func (c *Client) buildFilters(filters map[string]interface{}) []map[string]interface{} {
	if filters == nil {
		return nil
	}

	var result []map[string]interface{}
	for key, value := range filters {
		result = append(result, map[string]interface{}{
			"term": map[string]interface{}{
				key: value,
			},
		})
	}
	return result
}

// parseSearchResult parses search response
func (c *Client) parseSearchResult(result map[string]interface{}) []SearchResult {
	var results []SearchResult

	hits, ok := result["hits"].(map[string]interface{})
	if !ok {
		return results
	}

	hitsArray, ok := hits["hits"].([]interface{})
	if !ok {
		return results
	}

	for _, hit := range hitsArray {
		hitMap, ok := hit.(map[string]interface{})
		if !ok {
			continue
		}

		source, ok := hitMap["_source"].(map[string]interface{})
		if !ok {
			continue
		}

		score, _ := hitMap["_score"].(float64)

		result := SearchResult{
			ID:         getString(hitMap, "_id"),
			DocumentID: getString(source, "document_id"),
			Title:      getString(source, "title"),
			Content:    getString(source, "content"),
			Score:      score,
			Source:     "text",
		}
		if docType, ok := source["doc_type"].(string); ok {
			result.Metadata = map[string]interface{}{"doc_type": docType}
		}
		if metadata, ok := source["metadata"].(map[string]interface{}); ok {
			if result.Metadata == nil {
				result.Metadata = map[string]interface{}{}
			}
			for k, v := range metadata {
				result.Metadata[k] = v
			}
		}

		if highlight, ok := hitMap["highlight"].(map[string]interface{}); ok {
			if fragments, ok := highlight["content"].([]interface{}); ok {
				for _, f := range fragments {
					result.Highlights = append(result.Highlights, f.(string))
				}
			}
		}

		results = append(results, result)
	}

	return results
}

// mergeResults merges text and vector search results using RRF
func (c *Client) mergeResults(textResults, vectorResults []SearchResult, weight float64) []SearchResult {
	merged := make(map[string]*SearchResult)
	scores := make(map[string]float64)

	for i, r := range textResults {
		merged[r.ID] = &r
		scores[r.ID] += 1.0 / float64(60+i+1) * weight
	}

	vectorWeight := 1.0 - weight
	for i, r := range vectorResults {
		if existing, ok := merged[r.ID]; ok {
			existing.Score = r.Score
		} else {
			merged[r.ID] = &r
			merged[r.ID].Source = "vector"
		}
		scores[r.ID] += 1.0 / float64(60+i+1) * vectorWeight
	}

	var results []SearchResult
	for _, r := range merged {
		r.Score = scores[r.ID]
		results = append(results, *r)
	}

	sortResults(results)
	return results
}

func sortResults(results []SearchResult) {
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Score > results[i].Score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
}

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// ListTextSamplesByKnowledgeBase lists text index docs for a knowledge base.
func (c *Client) ListTextSamplesByKnowledgeBase(ctx context.Context, knowledgeBaseID string, limit int) ([]TextIndexSample, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	indexName := kbIndexName(TextIndexPrefix, knowledgeBaseID)
	query := map[string]interface{}{
		"size": limit,
		"query": map[string]interface{}{
			"match_all": map[string]interface{}{},
		},
		"sort": []map[string]interface{}{
			{"updated_at": map[string]interface{}{"order": "desc"}},
		},
	}

	body, err := json.Marshal(query)
	if err != nil {
		return nil, err
	}

	searchReq := opensearchapi.SearchRequest{
		Index: []string{indexName},
		Body:  strings.NewReader(string(body)),
	}

	resp, err := searchReq.Do(ctx, c.client)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}

	return parseTextIndexSamples(raw), nil
}

// ListVectorSamplesByKnowledgeBase lists vector index docs for a knowledge base — consistently uses kbIndexName
func (c *Client) ListVectorSamplesByKnowledgeBase(ctx context.Context, knowledgeBaseID string, limit int) ([]VectorIndexSample, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	indexName := kbIndexName(VectorIndexPrefix, knowledgeBaseID)
	query := map[string]interface{}{
		"size": limit,
		"query": map[string]interface{}{
			"match_all": map[string]interface{}{},
		},
	}

	body, err := json.Marshal(query)
	if err != nil {
		return nil, err
	}

	searchReq := opensearchapi.SearchRequest{
		Index: []string{indexName},
		Body:  strings.NewReader(string(body)),
	}

	resp, err := searchReq.Do(ctx, c.client)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}

	return parseVectorIndexSamples(raw), nil
}

func parseTextIndexSamples(raw map[string]interface{}) []TextIndexSample {
	hitsMap, ok := raw["hits"].(map[string]interface{})
	if !ok {
		return nil
	}
	hits, ok := hitsMap["hits"].([]interface{})
	if !ok {
		return nil
	}

	out := make([]TextIndexSample, 0, len(hits))
	for _, item := range hits {
		hit, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		source, ok := hit["_source"].(map[string]interface{})
		if !ok {
			continue
		}
		s := TextIndexSample{
			ID:              getString(hit, "_id"),
			DocumentID:      getString(source, "document_id"),
			KnowledgeBaseID: getString(source, "knowledge_base_id"),
			Title:           getString(source, "title"),
			Content:         getString(source, "content"),
			DocType:         getString(source, "doc_type"),
		}
		if metadata, ok := source["metadata"].(map[string]interface{}); ok {
			s.Metadata = metadata
		}
		if updatedAtRaw, ok := source["updated_at"].(string); ok {
			if t, err := time.Parse(time.RFC3339, updatedAtRaw); err == nil {
				s.UpdatedAt = t
			}
		}
		out = append(out, s)
	}
	return out
}

func parseVectorIndexSamples(raw map[string]interface{}) []VectorIndexSample {
	hitsMap, ok := raw["hits"].(map[string]interface{})
	if !ok {
		return nil
	}
	hits, ok := hitsMap["hits"].([]interface{})
	if !ok {
		return nil
	}

	out := make([]VectorIndexSample, 0, len(hits))
	for _, item := range hits {
		hit, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		source, ok := hit["_source"].(map[string]interface{})
		if !ok {
			continue
		}
		embeddingPreview := make([]float32, 0, 8)
		embeddingDim := 0
		if embedding, ok := source["embedding"].([]interface{}); ok {
			embeddingDim = len(embedding)
			for i := 0; i < len(embedding) && i < 8; i++ {
				switch v := embedding[i].(type) {
				case float64:
					embeddingPreview = append(embeddingPreview, float32(v))
				case float32:
					embeddingPreview = append(embeddingPreview, v)
				case int:
					embeddingPreview = append(embeddingPreview, float32(v))
				}
			}
		}

		out = append(out, VectorIndexSample{
			ID:               getString(hit, "_id"),
			DocumentID:       getString(source, "document_id"),
			KnowledgeBaseID:  getString(source, "knowledge_base_id"),
			Title:            getString(source, "title"),
			Content:          getString(source, "content"),
			EmbeddingDim:     embeddingDim,
			EmbeddingPreview: embeddingPreview,
		})
	}
	return out
}

// AnalyzeRequest represents a text analysis request
type AnalyzeRequest struct {
	Text     string `json:"text"`
	Analyzer string `json:"analyzer,omitempty"`
}

// AnalyzeResult represents text analysis result
type AnalyzeResult struct {
	Tokens []Token `json:"tokens"`
}

// Token represents a single analyzed token
type Token struct {
	Token       string `json:"token"`
	StartOffset int    `json:"start_offset"`
	EndOffset   int    `json:"end_offset"`
	Type        string `json:"type"`
	Position    int    `json:"position"`
}

// Analyze performs text analysis — consistently uses kbIndexName
func (c *Client) Analyze(ctx context.Context, knowledgeBaseID string, text, analyzer string) (*AnalyzeResult, error) {
	indexName := kbIndexName(TextIndexPrefix, knowledgeBaseID)

	if analyzer == "" {
		analyzer = "ik_max_word"
	}

	reqBody := map[string]interface{}{
		"analyzer": analyzer,
		"text":     text,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req := opensearchapi.IndicesAnalyzeRequest{
		Index: indexName,
		Body:  strings.NewReader(string(body)),
	}

	resp, err := req.Do(ctx, c.client)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("analyze failed (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var result struct {
		Tokens []Token `json:"tokens"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &AnalyzeResult{Tokens: result.Tokens}, nil
}

// DocumentIndexStatus represents where a document is indexed
type DocumentIndexStatus struct {
	DocumentID       string `json:"document_id"`
	InTextIndex      bool   `json:"in_text_index"`
	InVectorIndex    bool   `json:"in_vector_index"`
	TextIndexCount   int64  `json:"text_index_count"`
	VectorIndexCount int64  `json:"vector_index_count"`
}

// GetDocumentIndexStatus checks where a document is indexed — consistently uses kbIndexName
func (c *Client) GetDocumentIndexStatus(ctx context.Context, knowledgeBaseID string, documentID string) (*DocumentIndexStatus, error) {
	status := &DocumentIndexStatus{DocumentID: documentID}

	textIndex := kbIndexName(TextIndexPrefix, knowledgeBaseID)
	vectorIndex := kbIndexName(VectorIndexPrefix, knowledgeBaseID)

	// Check text index
	textQuery := map[string]interface{}{
		"size": 0,
		"query": map[string]interface{}{
			"term": map[string]interface{}{
				"document_id": documentID,
			},
		},
	}
	textBody, _ := json.Marshal(textQuery)
	textReq := opensearchapi.SearchRequest{
		Index: []string{textIndex},
		Body:  strings.NewReader(string(textBody)),
	}
	if resp, err := textReq.Do(ctx, c.client); err == nil {
		defer resp.Body.Close()
		var result map[string]interface{}
		if json.NewDecoder(resp.Body).Decode(&result) == nil {
			if hits, ok := result["hits"].(map[string]interface{}); ok {
				if total, ok := hits["total"].(map[string]interface{}); ok {
					if count, ok := total["value"].(float64); ok {
						status.TextIndexCount = int64(count)
						status.InTextIndex = count > 0
					}
				}
			}
		}
	}

	// Check vector index
	vectorQuery := map[string]interface{}{
		"size": 0,
		"query": map[string]interface{}{
			"term": map[string]interface{}{
				"document_id": documentID,
			},
		},
	}
	vectorBody, _ := json.Marshal(vectorQuery)
	vectorReq := opensearchapi.SearchRequest{
		Index: []string{vectorIndex},
		Body:  strings.NewReader(string(vectorBody)),
	}
	if resp, err := vectorReq.Do(ctx, c.client); err == nil {
		defer resp.Body.Close()
		var result map[string]interface{}
		if json.NewDecoder(resp.Body).Decode(&result) == nil {
			if hits, ok := result["hits"].(map[string]interface{}); ok {
				if total, ok := hits["total"].(map[string]interface{}); ok {
					if count, ok := total["value"].(float64); ok {
						status.VectorIndexCount = int64(count)
						status.InVectorIndex = count > 0
					}
				}
			}
		}
	}

	return status, nil
}

// DocumentVectorSample represents a document's vector with full embedding
type DocumentVectorSample struct {
	ID              string    `json:"id"`
	ChunkID         string    `json:"chunk_id"`
	Content         string    `json:"content"`
	EmbeddingDim    int       `json:"embedding_dim"`
	EmbeddingFull   []float32 `json:"embedding_full"`
	EmbeddingUmap2D []float32 `json:"embedding_umap_2d,omitempty"`
}

// GetDocumentVectors gets all vectors for a document — consistently uses kbIndexName
func (c *Client) GetDocumentVectors(ctx context.Context, knowledgeBaseID string, documentID string, limit int) ([]DocumentVectorSample, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	indexName := kbIndexName(VectorIndexPrefix, knowledgeBaseID)
	query := map[string]interface{}{
		"size": limit,
		"query": map[string]interface{}{
			"term": map[string]interface{}{
				"document_id": documentID,
			},
		},
	}

	body, err := json.Marshal(query)
	if err != nil {
		return nil, err
	}

	searchReq := opensearchapi.SearchRequest{
		Index: []string{indexName},
		Body:  strings.NewReader(string(body)),
	}

	resp, err := searchReq.Do(ctx, c.client)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}

	return parseDocumentVectorSamples(raw), nil
}

func parseDocumentVectorSamples(raw map[string]interface{}) []DocumentVectorSample {
	hitsMap, ok := raw["hits"].(map[string]interface{})
	if !ok {
		return nil
	}
	hits, ok := hitsMap["hits"].([]interface{})
	if !ok {
		return nil
	}

	out := make([]DocumentVectorSample, 0, len(hits))
	for _, item := range hits {
		hit, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		source, ok := hit["_source"].(map[string]interface{})
		if !ok {
			continue
		}

		embeddingFull := make([]float32, 0)
		embeddingDim := 0
		if embedding, ok := source["embedding"].([]interface{}); ok {
			embeddingDim = len(embedding)
			for _, v := range embedding {
				switch val := v.(type) {
				case float64:
					embeddingFull = append(embeddingFull, float32(val))
				case float32:
					embeddingFull = append(embeddingFull, val)
				case int:
					embeddingFull = append(embeddingFull, float32(val))
				}
			}
		}

		out = append(out, DocumentVectorSample{
			ID:            getString(hit, "_id"),
			ChunkID:       getString(source, "chunk_id"),
			Content:       getString(source, "content"),
			EmbeddingDim:  embeddingDim,
			EmbeddingFull: embeddingFull,
		})
	}
	return out
}

// DeleteDocument deletes a document from KB-specific indices
func (c *Client) DeleteDocument(ctx context.Context, knowledgeBaseID string, documentID string) error {
	textIndex := kbIndexName(TextIndexPrefix, knowledgeBaseID)
	vectorIndex := kbIndexName(VectorIndexPrefix, knowledgeBaseID)

	query := map[string]interface{}{
		"query": map[string]interface{}{
			"term": map[string]interface{}{
				"document_id": documentID,
			},
		},
	}

	body, _ := json.Marshal(query)

	deleteReq := opensearchapi.DeleteByQueryRequest{
		Index: []string{textIndex, vectorIndex},
		Body:  strings.NewReader(string(body)),
	}

	_, err := deleteReq.Do(ctx, c.client)
	return err
}
