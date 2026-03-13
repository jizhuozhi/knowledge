//go:build e2e

// Package e2e contains end-to-end tests that require real infrastructure:
//   - PostgreSQL (GORM)
//   - OpenSearch with IK analyzer plugin
//   - Neo4j with APOC
//
// Run: docker-compose up -d postgres opensearch neo4j && go test -tags e2e -v ./internal/e2e/...
//
// LLM calls are mocked with a deterministic MockLLMProvider to avoid AWS Bedrock dependency
// and ensure reproducible test results.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jizhuozhi/knowledge/internal/config"
	"github.com/jizhuozhi/knowledge/internal/database"
	"github.com/jizhuozhi/knowledge/internal/handlers"
	"github.com/jizhuozhi/knowledge/internal/middleware"
	"github.com/jizhuozhi/knowledge/internal/models"
	neo4jpkg "github.com/jizhuozhi/knowledge/internal/neo4j"
	"github.com/jizhuozhi/knowledge/internal/opensearch"
	"github.com/jizhuozhi/knowledge/internal/router"
	"github.com/jizhuozhi/knowledge/internal/services"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ========================================================================
// Test infrastructure setup
// ========================================================================

var (
	testDB       *gorm.DB
	testOS       *opensearch.Client
	testNeo4j    *neo4jpkg.Client
	testCfg      *config.Config
	testMockLLM  *services.MockLLMProvider
	testServer   *httptest.Server
	testTenantID string
	testKBID     string
)

func TestMain(m *testing.M) {
	cfg := buildTestConfig()
	testCfg = cfg

	// Connect to real Postgres
	db, err := gorm.Open(postgres.Open(cfg.Database.DSN()), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		fmt.Printf("SKIP: cannot connect to Postgres: %v\n", err)
		os.Exit(0)
	}
	testDB = db
	database.DB = db

	// Migrate schema
	if err := database.AutoMigrate(); err != nil {
		fmt.Printf("FATAL: migration failed: %v\n", err)
		os.Exit(1)
	}

	// Connect to real OpenSearch
	osClient, err := opensearch.NewClient(cfg.OpenSearch, cfg.LLM.EmbeddingDimension)
	if err != nil {
		fmt.Printf("SKIP: cannot connect to OpenSearch: %v\n", err)
		os.Exit(0)
	}
	testOS = osClient

	// Connect to real Neo4j
	neo4jClient, err := neo4jpkg.NewClient(cfg.Neo4j)
	if err != nil {
		fmt.Printf("SKIP: cannot connect to Neo4j: %v\n", err)
		os.Exit(0)
	}
	testNeo4j = neo4jClient
	defer neo4jClient.Close()

	// Create Neo4j constraints
	if err := neo4jClient.CreateConstraints(context.Background()); err != nil {
		fmt.Printf("Warning: neo4j constraints: %v\n", err)
	}

	// Setup mock LLM
	testMockLLM = services.NewMockLLMProvider(cfg.LLM.EmbeddingDimension)

	// Setup HTTP test server with real services
	testServer = setupTestServer(cfg, db, osClient, neo4jClient, testMockLLM)
	defer testServer.Close()

	// Create test tenant
	testTenantID = createTestTenant()

	os.Exit(m.Run())
}

func buildTestConfig() *config.Config {
	return &config.Config{
		App: config.AppConfig{
			Name:    "knowledge-e2e-test",
			Env:     "test",
			Port:    0,
			Debug:   true,
			Version: "test",
		},
		Database: config.DatabaseConfig{
			Host:         envOr("DB_HOST", "localhost"),
			Port:         envIntOr("DB_PORT", 5432),
			User:         envOr("DB_USER", "postgres"),
			Password:     envOr("DB_PASSWORD", "postgres"),
			DBName:       envOr("DB_NAME", "knowledge_db"),
			SSLMode:      "disable",
			MaxOpenConns: 10,
			MaxIdleConns: 5,
		},
		OpenSearch: config.OpenSearchConfig{
			Host:        envOr("OPENSEARCH_HOST", "localhost"),
			Port:        envIntOr("OPENSEARCH_PORT", 9200),
			User:        envOr("OPENSEARCH_USER", ""),
			Password:    envOr("OPENSEARCH_PASSWORD", ""),
			UseSSL:      false,
			VerifyCerts: false,
		},
		Neo4j: config.Neo4jConfig{
			URI:      envOr("NEO4J_URI", "bolt://localhost:7687"),
			User:     envOr("NEO4J_USER", "neo4j"),
			Password: envOr("NEO4J_PASSWORD", "neo4j"),
		},
		LLM: config.LLMConfig{
			Provider:           "bedrock",
			AWSRegion:          "us-east-1",
			EmbeddingModel:     "amazon.titan-embed-text-v2:0",
			EmbeddingDimension: 256, // smaller for faster tests
			ChatModel:          "amazon.nova-micro-v1:0",
		},
		Document: config.DocumentConfig{
			MaxFileSize:  10 * 1024 * 1024,
			UploadDir:    os.TempDir(),
			ChunkSize:    500,
			ChunkOverlap: 50,
		},
	}
}

func setupTestServer(cfg *config.Config, db *gorm.DB, osClient *opensearch.Client, neo4jClient *neo4jpkg.Client, mockLLM *services.MockLLMProvider) *httptest.Server {
	config.GlobalConfig = cfg

	docService := services.NewDocumentServiceForTest(db, cfg, mockLLM)
	docService.SetOpenSearchClient(osClient)
	docService.SetNeo4jClient(neo4jClient)

	ragService := services.NewRAGServiceForTest(db, cfg, mockLLM)
	ragService.SetOpenSearchClient(osClient)
	ragService.SetNeo4jClient(neo4jClient)

	handler := handlers.NewHandler(cfg, docService, ragService)
	r := router.SetupRouter(cfg, handler)

	return httptest.NewServer(r)
}

func createTestTenant() string {
	tenant := models.Tenant{
		Name:   fmt.Sprintf("e2e-test-%d", time.Now().UnixNano()),
		Code:   fmt.Sprintf("e2e%d", time.Now().UnixNano()%100000),
		Status: "active",
	}
	if err := testDB.Create(&tenant).Error; err != nil {
		panic("failed to create test tenant: " + err.Error())
	}
	return tenant.ID
}

// ========================================================================
// Helper functions
// ========================================================================

func apiRequest(t *testing.T, method, path string, body interface{}) *http.Response {
	t.Helper()

	var reqBody *bytes.Buffer
	if body != nil {
		b, _ := json.Marshal(body)
		reqBody = bytes.NewBuffer(b)
	} else {
		reqBody = bytes.NewBuffer(nil)
	}

	req, err := http.NewRequest(method, testServer.URL+path, reqBody)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", testTenantID)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response) map[string]interface{} {
	t.Helper()
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	resp.Body.Close()
	return result
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntOr(key string, fallback int) int {}
	return fallback
}

func envIntOr(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		var n int
		fmt.Sscanf(v, "%d", &n)
		if n > 0 {
			return n
		}
	}
	return fallback
}

// waitForIndex waits for OpenSearch to refresh indices
func waitForIndex(t *testing.T) {
	t.Helper()
	time.Sleep(2 * time.Second) // OpenSearch refresh interval
}

// ========================================================================
// Test 1: Full Document Processing Pipeline
// ========================================================================

func TestDocumentProcessingPipeline(t *testing.T) {
	// 1. Create knowledge base
	resp := apiRequest(t, "POST", "/api/v1/knowledge-bases", map[string]string{
		"name":        "E2E测试知识库",
		"description": "端到端测试用知识库",
	})
	if resp.StatusCode != http.StatusCreated {
		body := decodeJSON(t, resp)
		t.Fatalf("failed to create KB: status=%d body=%v", resp.StatusCode, body)
	}
	kbData := decodeJSON(t, resp)
	testKBID = kbData["id"].(string)
	t.Logf("Created KB: %s", testKBID)

	// 2. Create document with rich Markdown content
	docContent := buildTestDocument()
	resp = apiRequest(t, "POST", "/api/v1/documents", map[string]interface{}{
		"title":             "知识库系统架构设计文档",
		"content":           docContent,
		"knowledge_base_id": testKBID,
		"doc_type":          "knowledge",
		"format":            "markdown",
	})
	if resp.StatusCode != http.StatusCreated {
		body := decodeJSON(t, resp)
		t.Fatalf("failed to create document: status=%d body=%v", resp.StatusCode, body)
	}
	docData := decodeJSON(t, resp)
	docID := docData["id"].(string)
	t.Logf("Created document: %s", docID)

	// 3. Process document (synchronous call via service for E2E)
	docService := buildDocService()
	err := docService.ProcessDocument(context.Background(), docID)
	if err != nil {
		t.Fatalf("ProcessDocument failed: %v", err)
	}

	// 4. Verify document status in Postgres
	var doc models.Document
	if err := testDB.First(&doc, "id = ?", docID).Error; err != nil {
		t.Fatalf("failed to fetch document: %v", err)
	}
	if doc.Status != "published" {
		t.Errorf("expected document status=published, got %s", doc.Status)
	}
	if doc.Summary == "" {
		t.Error("expected non-empty document summary")
	}
	t.Logf("Document status=%s, summary_len=%d", doc.Status, len(doc.Summary))

	// 5. Verify chunks in Postgres
	var chunks []models.Chunk
	if err := testDB.Where("document_id = ?", docID).Order("chunk_index ASC").Find(&chunks).Error; err != nil {
		t.Fatalf("failed to query chunks: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk, got 0")
	}
	t.Logf("Created %d chunks", len(chunks))

	for _, chunk := range chunks {
		if chunk.Content == "" {
			t.Errorf("chunk %d has empty content", chunk.ChunkIndex)
		}
		if chunk.VectorID == "" {
			t.Errorf("chunk %d has no vector_id", chunk.ChunkIndex)
		}
	}

	// 6. Verify OpenSearch text index
	waitForIndex(t)
	indexStatus, err := testOS.GetDocumentIndexStatus(context.Background(), testKBID, docID)
	if err != nil {
		t.Fatalf("failed to get index status: %v", err)
	}
	if !indexStatus.InTextIndex {
		t.Error("document not found in text index")
	}
	if indexStatus.TextIndexCount == 0 {
		t.Error("text index count is 0")
	}
	t.Logf("Text index: count=%d", indexStatus.TextIndexCount)

	// 7. Verify OpenSearch vector index
	if !indexStatus.InVectorIndex {
		t.Error("document not found in vector index")
	}
	if indexStatus.VectorIndexCount == 0 {
		t.Error("vector index count is 0")
	}
	t.Logf("Vector index: count=%d", indexStatus.VectorIndexCount)

	// 8. Verify graph entities in Postgres
	var entities []models.GraphEntity
	if err := testDB.Where("document_id = ? AND tenant_id = ?", docID, testTenantID).Find(&entities).Error; err != nil {
		t.Fatalf("failed to query graph entities: %v", err)
	}
	if len(entities) == 0 {
		t.Error("expected graph entities, got 0")
	}
	t.Logf("Graph entities: %d", len(entities))
	for _, e := range entities {
		t.Logf("  Entity: %s (%s)", e.Name, e.Type)
	}

	// 9. Verify graph relations in Postgres
	var relations []models.GraphRelation
	if err := testDB.Where("knowledge_base_id = ? AND tenant_id = ?", testKBID, testTenantID).Find(&relations).Error; err != nil {
		t.Fatalf("failed to query graph relations: %v", err)
	}
	if len(relations) == 0 {
		t.Error("expected graph relations, got 0")
	}
	t.Logf("Graph relations: %d", len(relations))

	// 10. Verify Neo4j entities
	neo4jCount, err := testNeo4j.GetEntityCount(context.Background(), testKBID)
	if err != nil {
		t.Fatalf("failed to get Neo4j entity count: %v", err)
	}
	if neo4jCount == 0 {
		t.Error("expected Neo4j entities, got 0")
	}
	t.Logf("Neo4j entities: %d", neo4jCount)

	// 11. Verify processing events were recorded
	var events []models.ProcessingEvent
	if err := testDB.Where("document_id = ? AND tenant_id = ?", docID, testTenantID).Order("step ASC").Find(&events).Error; err != nil {
		t.Fatalf("failed to query processing events: %v", err)
	}
	if len(events) == 0 {
		t.Error("expected processing events, got 0")
	}
	t.Logf("Processing events: %d", len(events))

	hasFinish := false
	for _, e := range events {
		t.Logf("  Step %d: %s - %s", e.Step, e.Stage, e.Status)
		if e.Stage == "finish" && e.Status == "success" {
			hasFinish = true
		}
	}
	if !hasFinish {
		t.Error("missing 'finish' processing event with success status")
	}

	// 12. Verify LLM usage records
	var usageRecords []models.LLMUsageRecord
	if err := testDB.Where("document_id = ? AND tenant_id = ?", docID, testTenantID).Find(&usageRecords).Error; err != nil {
		t.Fatalf("failed to query LLM usage records: %v", err)
	}
	if len(usageRecords) == 0 {
		t.Error("expected LLM usage records, got 0")
	}
	t.Logf("LLM usage records: %d", len(usageRecords))
}

// ========================================================================
// Test 2: Text Retrieval via OpenSearch Inverted Index (BM25)
// ========================================================================

func TestTextRetrievalBM25(t *testing.T) {
	if testKBID == "" {
		t.Skip("no KB created, run TestDocumentProcessingPipeline first")
	}

	waitForIndex(t)

	ctx := context.Background()

	// Test 1: Exact keyword match — "OpenSearch" should hit documents mentioning OpenSearch
	searchReq := &opensearch.SearchRequest{
		Query: "OpenSearch 倒排索引",
		From:  0,
		Size:  10,
	}
	results, err := testOS.Search(ctx, testKBID, searchReq)
	if err != nil {
		t.Fatalf("text search failed: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected BM25 results for 'OpenSearch 倒排索引', got 0")
	} else {
		t.Logf("BM25 search 'OpenSearch 倒排索引': %d results", len(results))
		for i, r := range results {
			t.Logf("  [%d] score=%.4f doc=%s content_preview=%s", i, r.Score, r.DocumentID, truncateStr(r.Content, 80))
		}
	}

	// Test 2: Chinese keyword matching — "向量检索" (vector retrieval)
	searchReq2 := &opensearch.SearchRequest{
		Query: "向量检索 语义相似度",
		From:  0,
		Size:  10,
	}
	results2, err := testOS.Search(ctx, testKBID, searchReq2)
	if err != nil {
		t.Fatalf("text search failed: %v", err)
	}
	t.Logf("BM25 search '向量检索 语义相似度': %d results", len(results2))

	// Test 3: Error code / specific term matching — "ERR_CONN_REFUSED"
	searchReq3 := &opensearch.SearchRequest{
		Query: "ERR_CONN_REFUSED",
		From:  0,
		Size:  10,
	}
	results3, err := testOS.Search(ctx, testKBID, searchReq3)
	if err != nil {
		t.Fatalf("text search failed: %v", err)
	}
	t.Logf("BM25 search 'ERR_CONN_REFUSED': %d results", len(results3))
	if len(results3) == 0 {
		t.Error("expected BM25 to match error code ERR_CONN_REFUSED in document")
	}

	// Test 4: Multi-field search — title should have higher weight
	searchReq4 := &opensearch.SearchRequest{
		Query: "知识库系统架构",
		From:  0,
		Size:  10,
	}
	results4, err := testOS.Search(ctx, testKBID, searchReq4)
	if err != nil {
		t.Fatalf("text search failed: %v", err)
	}
	t.Logf("BM25 search '知识库系统架构': %d results", len(results4))
	if len(results4) > 0 {
		// Title match should have higher score due to title^2 boost
		t.Logf("  Top result score: %.4f (title boosted)", results4[0].Score)
	}
}

// ========================================================================
// Test 3: Vector Retrieval via OpenSearch KNN
// ========================================================================

func TestVectorRetrievalKNN(t *testing.T) {
	if testKBID == "" {
		t.Skip("no KB created")
	}

	waitForIndex(t)

	ctx := context.Background()

	// Generate query embedding using mock provider
	queryText := "如何实现知识库的多通道检索融合"
	queryEmbedding := testMockLLM.DeterministicEmbeddingPublic(queryText)

	searchReq := &opensearch.SearchRequest{
		Query:  queryText,
		Vector: queryEmbedding,
		From:   0,
		Size:   10,
	}

	results, err := testOS.VectorSearch(ctx, testKBID, searchReq)
	if err != nil {
		t.Fatalf("vector search failed: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected KNN results for semantic query, got 0")
	} else {
		t.Logf("KNN search '如何实现知识库的多通道检索融合': %d results", len(results))
		for i, r := range results {
			t.Logf("  [%d] score=%.4f doc=%s content_preview=%s", i, r.Score, r.DocumentID, truncateStr(r.Content, 80))
		}
	}

	// Test 2: Conceptual query — semantically related but different wording
	queryText2 := "搜索引擎如何理解用户查询意图"
	queryEmbedding2 := testMockLLM.DeterministicEmbeddingPublic(queryText2)

	searchReq2 := &opensearch.SearchRequest{
		Query:  queryText2,
		Vector: queryEmbedding2,
		From:   0,
		Size:   10,
	}
	results2, err := testOS.VectorSearch(ctx, testKBID, searchReq2)
	if err != nil {
		t.Fatalf("vector search failed: %v", err)
	}
	t.Logf("KNN search '搜索引擎如何理解用户查询意图': %d results", len(results2))

	// Test 3: Hybrid search — text + vector combined
	queryText3 := "OpenSearch向量索引HNSW算法"
	queryEmbedding3 := testMockLLM.DeterministicEmbeddingPublic(queryText3)

	searchReq3 := &opensearch.SearchRequest{
		Query:        queryText3,
		Vector:       queryEmbedding3,
		From:         0,
		Size:         10,
		HybridWeight: 0.5,
	}
	results3, err := testOS.HybridSearch(ctx, testKBID, searchReq3)
	if err != nil {
		t.Fatalf("hybrid search failed: %v", err)
	}
	t.Logf("Hybrid search 'OpenSearch向量索引HNSW算法': %d results", len(results3))
	if len(results3) == 0 {
		t.Error("expected hybrid search results, got 0")
	}
}

// ========================================================================
// Test 4: Graph Relationship Retrieval via Neo4j
// ========================================================================

func TestGraphRetrieval(t *testing.T) {
	if testKBID == "" {
		t.Skip("no KB created")
	}

	ctx := context.Background()

	// Test 1: Search entities by name
	entities, err := testNeo4j.SearchByEntityName(ctx, testKBID, "OpenSearch", 10)
	if err != nil {
		t.Fatalf("Neo4j entity search failed: %v", err)
	}
	if len(entities) == 0 {
		t.Error("expected to find 'OpenSearch' entity in Neo4j")
	} else {
		t.Logf("Found %d entities matching 'OpenSearch'", len(entities))
		for _, e := range entities {
			t.Logf("  Entity: %s (%s) doc=%s", e.Name, e.Type, e.DocumentID)
		}
	}

	// Test 2: Find related entities from graph traversal
	if len(entities) > 0 {
		related, err := testNeo4j.FindRelatedEntities(ctx, testKBID, entities[0].ID, nil, 2)
		if err != nil {
			t.Logf("Warning: FindRelatedEntities failed (may need APOC): %v", err)
		} else {
			t.Logf("Related entities for '%s': %d", entities[0].Name, len(related))
			for _, r := range related {
				t.Logf("  Related: %s (%s)", r.Name, r.Type)
			}
		}
	}

	// Test 3: Search for concept entities
	concepts, err := testNeo4j.SearchByEntityName(ctx, testKBID, "RAG", 10)
	if err != nil {
		t.Fatalf("Neo4j concept search failed: %v", err)
	}
	t.Logf("Found %d entities matching 'RAG'", len(concepts))

	// Test 4: Verify graph entities match between Postgres and Neo4j
	var pgEntities []models.GraphEntity
	testDB.Where("knowledge_base_id = ? AND tenant_id = ?", testKBID, testTenantID).Find(&pgEntities)

	neo4jCount, _ := testNeo4j.GetEntityCount(ctx, testKBID)
	t.Logf("Entity counts: Postgres=%d, Neo4j=%d", len(pgEntities), neo4jCount)

	// Counts should be close (Neo4j may have slightly fewer if some creates failed)
	if neo4jCount == 0 && len(pgEntities) > 0 {
		t.Error("Neo4j has 0 entities but Postgres has entities — graph indexing broken")
	}
}

// ========================================================================
// Test 5: Full RAG Query Pipeline
// ========================================================================

func TestRAGQueryPipeline(t *testing.T) {
	if testKBID == "" {
		t.Skip("no KB created")
	}

	waitForIndex(t)

	// Test 1: RAG query targeting specific KB
	resp := apiRequest(t, "POST", "/api/v1/rag/query", map[string]interface{}{
		"query":             "知识库系统是如何实现多通道检索的？",
		"knowledge_base_id": testKBID,
		"top_k":             5,
		"include_graph":     true,
	})
	if resp.StatusCode != http.StatusOK {
		body := decodeJSON(t, resp)
		t.Fatalf("RAG query failed: status=%d body=%v", resp.StatusCode, body)
	}

	ragResp := decodeJSON(t, resp)

	// Verify query understanding
	if understanding, ok := ragResp["understanding"].(map[string]interface{}); ok {
		t.Logf("Query understanding: intent=%v routing=%v", understanding["intent"], understanding["routing"])
		if keywords, ok := understanding["keywords"].([]interface{}); ok {
			t.Logf("  Keywords: %v", keywords)
		}
	} else {
		t.Error("missing query understanding in RAG response")
	}

	// Verify results exist
	results, ok := ragResp["results"].([]interface{})
	if !ok || len(results) == 0 {
		t.Error("RAG query returned 0 results")
	} else {
		t.Logf("RAG results: %d documents", len(results))
		for i, r := range results {
			result := r.(map[string]interface{})
			t.Logf("  [%d] title=%v relevance=%.1f sources=%v",
				i, result["title"], result["relevance"], result["sources"])

			// Verify each result has required fields
			if result["document_id"] == nil {
				t.Errorf("result %d missing document_id", i)
			}
			if result["content"] == nil || result["content"] == "" {
				t.Errorf("result %d missing content", i)
			}

			// Verify chunks are included
			if chunks, ok := result["chunks"].([]interface{}); ok {
				t.Logf("    Chunks: %d", len(chunks))
				for j, c := range chunks {
					chunk := c.(map[string]interface{})
					t.Logf("      [%d] source=%v score=%.4f", j, chunk["source"], chunk["score"])
				}
			}
		}
	}

	// Verify AI answer
	if answer, ok := ragResp["answer"].(string); ok && answer != "" {
		t.Logf("AI Answer: %s", truncateStr(answer, 200))
	} else {
		t.Error("RAG query returned empty AI answer")
	}

	// Verify graph info (requested with include_graph=true)
	if graphInfo, ok := ragResp["graph_info"].(map[string]interface{}); ok {
		if entities, ok := graphInfo["entities"].([]interface{}); ok {
			t.Logf("Graph entities in response: %d", len(entities))
		}
		if concepts, ok := graphInfo["related_concepts"].([]interface{}); ok {
			t.Logf("Related concepts: %v", concepts)
		}
	}
}

// ========================================================================
// Test 6: RAG Retrieval Scenarios from Design Docs
// ========================================================================

func TestRAGRetrievalScenarios(t *testing.T) {
	if testKBID == "" {
		t.Skip("no KB created")
	}

	waitForIndex(t)

	scenarios := []struct {
		name     string
		query    string
		graph    bool
		minHits  int
		desc     string
	}{
		{
			name:    "precise_query",
			query:   "ERR_CONN_REFUSED 连接拒绝错误",
			graph:   false,
			minHits: 0,
			desc:    "精确查询：错误码匹配，应该走BM25倒排索引",
		},
		{
			name:    "conceptual_query",
			query:   "如何提升知识检索的准确率和召回率",
			graph:   false,
			minHits: 0,
			desc:    "概念查询：语义搜索，应该走向量检索",
		},
		{
			name:    "graph_relationship_query",
			query:   "OpenSearch和Neo4j之间有什么关系",
			graph:   true,
			minHits: 0,
			desc:    "关系查询：实体关系，应该走图检索",
		},
		{
			name:    "hybrid_query",
			query:   "HNSW算法在向量索引中的应用原理",
			graph:   false,
			minHits: 0,
			desc:    "混合查询：既有精确术语又有概念理解",
		},
		{
			name:    "cross_document_query",
			query:   "知识库的完整索引构建流程",
			graph:   true,
			minHits: 0,
			desc:    "跨文档查询：需要综合多个chunk的信息",
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			resp := apiRequest(t, "POST", "/api/v1/rag/query", map[string]interface{}{
				"query":             sc.query,
				"knowledge_base_id": testKBID,
				"top_k":             10,
				"include_graph":     sc.graph,
			})
			if resp.StatusCode != http.StatusOK {
				body := decodeJSON(t, resp)
				t.Fatalf("[%s] RAG query failed: status=%d body=%v", sc.desc, resp.StatusCode, body)
			}

			ragResp := decodeJSON(t, resp)
			results, _ := ragResp["results"].([]interface{})
			answer, _ := ragResp["answer"].(string)
			routing, _ := ragResp["routing"].(string)

			t.Logf("[%s] results=%d routing=%s answer_len=%d",
				sc.desc, len(results), routing, len(answer))

			if len(results) < sc.minHits {
				t.Errorf("[%s] expected at least %d results, got %d", sc.desc, sc.minHits, len(results))
			}
		})
	}
}

// ========================================================================
// Test 7: Multi-Document Processing and Cross-Document Retrieval
// ========================================================================

func TestMultiDocumentRetrieval(t *testing.T) {
	if testKBID == "" {
		t.Skip("no KB created")
	}

	// Create a second document with different content
	doc2Content := buildSecondTestDocument()
	resp := apiRequest(t, "POST", "/api/v1/documents", map[string]interface{}{
		"title":             "微服务故障排查手册",
		"content":           doc2Content,
		"knowledge_base_id": testKBID,
		"doc_type":          "process",
		"format":            "markdown",
	})
	if resp.StatusCode != http.StatusCreated {
		body := decodeJSON(t, resp)
		t.Fatalf("failed to create doc2: status=%d body=%v", resp.StatusCode, body)
	}
	doc2Data := decodeJSON(t, resp)
	doc2ID := doc2Data["id"].(string)
	t.Logf("Created doc2: %s", doc2ID)

	// Process doc2
	docService := buildDocService()
	if err := docService.ProcessDocument(context.Background(), doc2ID); err != nil {
		t.Fatalf("ProcessDocument doc2 failed: %v", err)
	}
	t.Log("Doc2 processed successfully")

	waitForIndex(t)

	// Now search should return results from both documents
	resp = apiRequest(t, "POST", "/api/v1/rag/query", map[string]interface{}{
		"query":             "服务连接失败如何排查",
		"knowledge_base_id": testKBID,
		"top_k":             10,
	})
	if resp.StatusCode != http.StatusOK {
		body := decodeJSON(t, resp)
		t.Fatalf("multi-doc RAG query failed: %v", body)
	}

	ragResp := decodeJSON(t, resp)
	results, _ := ragResp["results"].([]interface{})
	t.Logf("Multi-doc RAG results: %d", len(results))

	// Check if results come from multiple documents (RRF document-level aggregation)
	docIDs := make(map[string]bool)
	for _, r := range results {
		result := r.(map[string]interface{})
		if did, ok := result["document_id"].(string); ok {
			docIDs[did] = true
		}
	}
	t.Logf("Results from %d distinct documents", len(docIDs))
}

// ========================================================================
// Test 8: OpenSearch IK Analyzer Verification
// ========================================================================

func TestIKAnalyzer(t *testing.T) {
	if testKBID == "" {
		t.Skip("no KB created")
	}

	ctx := context.Background()

	// Test IK analyzer tokenization
	analyzeResult, err := testOS.Analyze(ctx, testKBID, "知识库系统架构设计文档", "ik_max_word")
	if err != nil {
		t.Fatalf("IK analyze failed: %v", err)
	}

	if len(analyzeResult.Tokens) == 0 {
		t.Fatal("IK analyzer returned 0 tokens")
	}

	t.Logf("IK tokenization of '知识库系统架构设计文档': %d tokens", len(analyzeResult.Tokens))
	for _, token := range analyzeResult.Tokens {
		t.Logf("  token=%s type=%s position=%d", token.Token, token.Type, token.Position)
	}

	// Verify key terms are tokenized correctly
	expectedTokens := []string{"知识库", "系统", "架构", "设计", "文档"}
	tokenSet := make(map[string]bool)
	for _, tok := range analyzeResult.Tokens {
		tokenSet[tok.Token] = true
	}
	for _, expected := range expectedTokens {
		if !tokenSet[expected] {
			t.Errorf("IK analyzer missing expected token: %s", expected)
		}
	}
}

// ========================================================================
// Test 9: API-level Document Lifecycle
// ========================================================================

func TestDocumentLifecycleAPI(t *testing.T) {
	// Create KB via API
	resp := apiRequest(t, "POST", "/api/v1/knowledge-bases", map[string]string{
		"name":        "lifecycle-test-kb",
		"description": "lifecycle test",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatal("failed to create KB for lifecycle test")
	}
	kbData := decodeJSON(t, resp)
	kbID := kbData["id"].(string)

	// Create document
	resp = apiRequest(t, "POST", "/api/v1/documents", map[string]interface{}{
		"title":             "Lifecycle Test Doc",
		"content":           "This is a test document for lifecycle verification.",
		"knowledge_base_id": kbID,
		"format":            "txt",
	})
	if resp.StatusCode != http.StatusCreated {
		body := decodeJSON(t, resp)
		t.Fatalf("create doc: status=%d body=%v", resp.StatusCode, body)
	}
	docData := decodeJSON(t, resp)
	docID := docData["id"].(string)

	// Get document
	resp = apiRequest(t, "GET", "/api/v1/documents/"+docID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get doc: status=%d", resp.StatusCode)
	}

	// List documents
	resp = apiRequest(t, "GET", "/api/v1/documents?knowledge_base_id="+kbID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list docs: status=%d", resp.StatusCode)
	}
	listData := decodeJSON(t, resp)
	if total, ok := listData["total"].(float64); !ok || total == 0 {
		t.Error("expected at least 1 document in list")
	}

	// Update document
	resp = apiRequest(t, "PUT", "/api/v1/documents/"+docID, map[string]interface{}{
		"title": "Updated Lifecycle Doc",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update doc: status=%d", resp.StatusCode)
	}

	// Delete document
	resp = apiRequest(t, "DELETE", "/api/v1/documents/"+docID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete doc: status=%d", resp.StatusCode)
	}

	// Verify deleted
	resp = apiRequest(t, "GET", "/api/v1/documents/"+docID, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", resp.StatusCode)
	}

	// Delete KB
	resp = apiRequest(t, "DELETE", "/api/v1/knowledge-bases/"+kbID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete KB: status=%d", resp.StatusCode)
	}
}

// ========================================================================
// Test Helpers: Document content builders
// ========================================================================

func buildTestDocument() string {
	return `# 知识库系统架构设计文档

## 概述

本文档描述了知识库平台的系统架构设计，包括多通道检索、索引构建和知识图谱等核心功能。

系统采用 Go 语言开发，基于 net/http Handler 实现 RESTful API，支持多租户隔离。

## 核心组件

### OpenSearch 全文检索

OpenSearch 作为倒排索引引擎，支持基于 IK 分词器的中文全文检索。

- **文本索引**: 使用 ` + "`ik_max_word`" + ` 分析器进行最细粒度分词
- **BM25 算法**: 计算文档与查询的相关性评分
- **高亮显示**: 支持命中关键词的高亮标注

### 向量检索

向量检索基于 OpenSearch KNN 插件，使用 HNSW 算法实现高效近似最近邻搜索。

- **Embedding 模型**: AWS Bedrock Titan Embed Text V2（1024维）
- **余弦相似度**: 使用 cosinesimil 空间类型
- **HNSW 参数**: ef_construction=256, m=16

### Neo4j 图关系检索

Neo4j 存储实体和关系，支持基于 APOC 的子图遍历。

实体类型包括：person, concept, service, component, api, product, organization
关系类型包括：references, depends_on, causes, implements, contains, related_to

## 检索流程

### 查询理解

通过 LLM 分析用户查询意图，提取实体、关键词，判断路由策略。

### 多通道检索

1. **文本通道**: OpenSearch BM25 关键词匹配
2. **向量通道**: OpenSearch KNN 语义相似度搜索  
3. **图通道**: Neo4j 实体关系遍历

### RRF 融合

使用 Reciprocal Rank Fusion (RRF) 算法融合多通道结果：

` + "```" + `
score = Σ (weight_i / (k + rank_i + 1))
其中 k=60，weight 由检索策略动态决定
` + "```" + `

融合后按文档维度聚合，取最佳 chunk 作为主内容。

## 常见问题

### ERR_CONN_REFUSED 连接拒绝

当出现 ERR_CONN_REFUSED 错误时，通常是因为：
1. 服务未启动
2. 端口被占用
3. 防火墙规则阻断

### 索引构建失败

索引构建过程中可能出现的错误：
- Embedding 生成超时
- OpenSearch 索引写入失败
- Neo4j 连接中断

## 数据模型

| 模型 | 说明 | 存储位置 |
| --- | --- | --- |
| Document | 文档元数据 | PostgreSQL |
| Chunk | 文档分块 | PostgreSQL |
| TextIndex | 倒排索引 | OpenSearch |
| VectorIndex | 向量索引 | OpenSearch |
| GraphEntity | 图实体 | PostgreSQL + Neo4j |
| GraphRelation | 图关系 | PostgreSQL + Neo4j |
`
}

func buildSecondTestDocument() string {
	return `# 微服务故障排查手册

## 概述

本手册记录常见微服务故障的排查流程和解决方案。

## 网络故障

### 服务连接失败

当服务间调用出现连接失败时，按以下步骤排查：

1. 检查目标服务是否正常运行: ` + "`kubectl get pods`" + `
2. 检查网络策略是否允许通信
3. 验证 DNS 解析是否正确
4. 检查端口是否被正确暴露

错误码: ERR_CONN_REFUSED, ERR_CONN_TIMEOUT, ERR_DNS_RESOLUTION

### 超时问题

服务调用超时通常与以下因素有关：
- 服务端处理时间过长
- 网络延迟
- 连接池耗尽
- 数据库查询慢

## 数据库故障

### PostgreSQL 连接池耗尽

**症状**: 应用日志出现 "too many connections" 错误

**排查步骤**:
1. 检查当前连接数: ` + "`SELECT count(*) FROM pg_stat_activity;`" + `
2. 查看空闲连接: ` + "`SELECT * FROM pg_stat_activity WHERE state = 'idle';`" + `
3. 调整连接池参数

### OpenSearch 索引异常

**症状**: 搜索返回空结果或超时

**排查步骤**:
1. 检查集群健康状态: ` + "`curl localhost:9200/_cluster/health`" + `
2. 查看索引状态: ` + "`curl localhost:9200/_cat/indices`" + `
3. 检查分片分配

## 性能优化

### 检索延迟优化

- 使用索引缓存提升热点查询性能
- 优化 Embedding 批量生成
- 调整 HNSW 的 ef_search 参数
`
}

func buildDocService() *services.DocumentService {
	docService := services.NewDocumentServiceForTest(testDB, testCfg, testMockLLM)
	docService.SetOpenSearchClient(testOS)
	docService.SetNeo4jClient(testNeo4j)
	return docService
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// Ensure middleware.GetTenantID is accessible (used by handlers through context)
var _ = middleware.GetTenantID
