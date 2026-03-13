package services

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/jizhuozhi/knowledge/internal/config"
)

// MockLLMProvider implements LLMProvider with deterministic responses for testing.
// Embeddings are computed from content hash so semantically similar texts produce similar vectors.
// Chat completions return structured JSON based on prompt detection.
type MockLLMProvider struct {
	Dimension int
	CallLog   []MockLLMCall
}

type MockLLMCall struct {
	Method string
	Prompt string
}

func NewMockLLMProvider(dimension int) *MockLLMProvider {
	return &MockLLMProvider{Dimension: dimension}
}

func (m *MockLLMProvider) GenerateEmbedding(_ context.Context, text string) ([]float32, *LLMUsage, error) {
	m.CallLog = append(m.CallLog, MockLLMCall{Method: "GenerateEmbedding", Prompt: text})
	embedding := m.deterministicEmbedding(text)
	usage := &LLMUsage{InputTokens: len(text) / 4, TotalTokens: len(text) / 4}
	return embedding, usage, nil
}

func (m *MockLLMProvider) GenerateEmbeddings(_ context.Context, texts []string) ([][]float32, *LLMUsage, error) {
	m.CallLog = append(m.CallLog, MockLLMCall{Method: "GenerateEmbeddings", Prompt: fmt.Sprintf("[%d texts]", len(texts))})
	embeddings := make([][]float32, len(texts))
	totalTokens := 0
	for i, text := range texts {
		embeddings[i] = m.deterministicEmbedding(text)
		totalTokens += len(text) / 4
	}
	usage := &LLMUsage{InputTokens: totalTokens, TotalTokens: totalTokens}
	return embeddings, usage, nil
}

func (m *MockLLMProvider) ChatCompletion(_ context.Context, prompt string) (string, *LLMUsage, error) {
	m.CallLog = append(m.CallLog, MockLLMCall{Method: "ChatCompletion", Prompt: prompt})
	response := m.deterministicChatResponse(prompt)
	usage := &LLMUsage{InputTokens: len(prompt) / 4, OutputTokens: len(response) / 4, TotalTokens: (len(prompt) + len(response)) / 4}
	return response, usage, nil
}

func (m *MockLLMProvider) ChatCompletionWithSystem(_ context.Context, systemPrompt, userPrompt string) (string, *LLMUsage, error) {
	m.CallLog = append(m.CallLog, MockLLMCall{Method: "ChatCompletionWithSystem", Prompt: userPrompt})
	response := m.deterministicChatResponse(systemPrompt + "\n" + userPrompt)
	usage := &LLMUsage{InputTokens: (len(systemPrompt) + len(userPrompt)) / 4, OutputTokens: len(response) / 4, TotalTokens: (len(systemPrompt) + len(userPrompt) + len(response)) / 4}
	return response, usage, nil
}

// deterministicEmbedding generates a deterministic embedding vector from text content.
// Uses character frequency distribution to ensure similar texts get similar vectors.
func (m *MockLLMProvider) deterministicEmbedding(text string) []float32 {
	dim := m.Dimension
	if dim <= 0 {
		dim = 1024
	}

	vec := make([]float32, dim)
	lower := strings.ToLower(text)

	// Build a frequency-based seed from the text
	for i, ch := range lower {
		idx := (int(ch) * (i + 1)) % dim
		vec[idx] += float32(ch) / 256.0
	}

	// Add character bigram features for better semantic similarity
	for i := 0; i < len(lower)-1; i++ {
		bigram := int(lower[i])*256 + int(lower[i+1])
		idx := bigram % dim
		vec[idx] += 0.5
	}

	// Normalize to unit vector
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	if norm > 0 {
		norm = math.Sqrt(norm)
		for i := range vec {
			vec[i] = float32(float64(vec[i]) / norm)
		}
	}

	return vec
}

// deterministicChatResponse returns structured JSON based on what the prompt is asking for.
func (m *MockLLMProvider) deterministicChatResponse(prompt string) string {
	lower := strings.ToLower(prompt)

	// Document type inference
	if strings.Contains(lower, "判断文档类型") || strings.Contains(lower, "doc_type") {
		return `{"doc_type": "knowledge", "reason": "技术文档，包含概念和说明"}`
	}

	// Index strategy determination
	if strings.Contains(lower, "indexing strategy") || strings.Contains(lower, "chunk_strategy") {
		return `{"chunk_strategy": "semantic", "chunk_size": 500, "enable_graph_index": true, "enable_ai_summary": true, "special_processing": ""}`
	}

	// Semantic metadata extraction
	if strings.Contains(lower, "extract structured metadata") || strings.Contains(lower, "semantic metadata") {
		return `{"topic": "knowledge management", "domain": "technology", "key_concepts": ["RAG", "vector search", "knowledge graph"]}`
	}

	// Entity extraction
	if strings.Contains(lower, "extract entities") {
		return `[{"id": "entity_0", "name": "OpenSearch", "type": "service", "properties": {"description": "搜索引擎"}}, {"id": "entity_1", "name": "Neo4j", "type": "service", "properties": {"description": "图数据库"}}, {"id": "entity_2", "name": "RAG", "type": "concept", "properties": {"description": "检索增强生成"}}, {"id": "entity_3", "name": "向量检索", "type": "concept", "properties": {"description": "基于语义相似度的搜索"}}]`
	}

	// Relation extraction
	if strings.Contains(lower, "extract relations") {
		return `[{"id": "rel_0", "source_id": "entity_0", "target_id": "entity_2", "type": "implements", "weight": 0.9, "properties": {}}, {"id": "rel_1", "source_id": "entity_1", "target_id": "entity_2", "type": "implements", "weight": 0.8, "properties": {}}, {"id": "rel_2", "source_id": "entity_3", "target_id": "entity_0", "type": "depends_on", "weight": 0.85, "properties": {}}]`
	}

	// Query understanding
	if strings.Contains(lower, "analyze the following search query") {
		return `{"intent": "search", "entities": ["知识库"], "keywords": ["知识库", "搜索"], "routing": "knowledge_base"}`
	}

	// Retrieval strategy
	if strings.Contains(lower, "retrieval strategy") {
		return `{"channels": {"text": 0.5, "vector": 0.5, "graph": 0.3}, "top_k": 10, "hybrid_weight": 0.5}`
	}

	// Reranking
	if strings.Contains(lower, "rerank") {
		return `[1, 2, 3]`
	}

	// Summary generation
	if strings.Contains(lower, "generate a concise summary") || strings.Contains(lower, "summary") {
		return "这是一篇关于知识库系统架构的技术文档，涵盖了全文检索、向量检索和图关系检索的多通道RAG检索方案。"
	}

	// Answer generation
	if strings.Contains(lower, "用户问题") || strings.Contains(lower, "知识库智能助手") {
		return "根据检索到的文档内容，知识库系统支持多通道检索：倒排索引（BM25关键词匹配）、向量检索（语义相似度）和图关系检索（实体关系遍历），通过RRF融合算法综合排序。\n\n来源：文档1"
	}

	// Default: return a simple JSON object
	return `{"result": "ok"}`
}

// NewEmbeddingServiceWithMock creates an EmbeddingService using a mock provider for testing
func NewEmbeddingServiceWithMock(cfg *config.Config, provider LLMProvider) *EmbeddingService {
	return &EmbeddingService{
		config:   cfg,
		provider: provider,
	}
}
