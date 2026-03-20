package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jizhuozhi/knowledge/internal/config"
	"github.com/jizhuozhi/knowledge/internal/models"
	"gorm.io/gorm"
)

// QueryService handles query analysis and rewriting
type QueryService struct {
	db           *gorm.DB
	config       *config.Config
	embedding    *EmbeddingService
	usageTracker *LLMUsageTracker
}

// NewQueryService creates a new query service
func NewQueryService(db *gorm.DB, cfg *config.Config) *QueryService {
	return &QueryService{
		db:           db,
		config:       cfg,
		embedding:    NewEmbeddingService(cfg),
		usageTracker: NewLLMUsageTracker(db, cfg),
	}
}

// QueryIntent represents the user's intent
type QueryIntent string

const (
	IntentCodeSearch      QueryIntent = "code_search"
	IntentConceptQuery    QueryIntent = "concept_query"
	IntentTroubleshooting QueryIntent = "troubleshooting"
	IntentHowTo           QueryIntent = "how_to"
	IntentComparison      QueryIntent = "comparison"
	IntentFactual         QueryIntent = "factual"
)

// QueryAnalysisResult represents the result of query analysis
type QueryAnalysisResult struct {
	Intent     QueryIntent `json:"intent"`
	Entities   []Entity    `json:"entities"`
	QueryTypes []string    `json:"query_types"` // code, section, table
	Keywords   []string    `json:"keywords"`
	Language   string      `json:"language"`
	Confidence float64     `json:"confidence"`
}

// RewrittenQuery represents a rewritten query variant
type RewrittenQuery struct {
	Query    string  `json:"query"`
	Weight   float64 `json:"weight"`
	Strategy string  `json:"strategy"` // synonym, structured, translation, expansion
}

// AnalyzeQuery performs comprehensive query analysis
func (s *QueryService) AnalyzeQuery(ctx context.Context, tenantID, query string) (*QueryAnalysisResult, *LLMUsage, error) {
	// Check cache first
	cacheKey := s.generateCacheKey(query, "analysis", "v1")
	if cached, err := s.getCachedAnalysis(ctx, cacheKey); err == nil && cached != nil {
		return cached, nil, nil
	}

	prompt := fmt.Sprintf(`分析以下用户查询,识别意图、提取实体、判断查询类型。

用户查询: "%s"

请返回 JSON 格式:
{
  "intent": "code_search|concept_query|troubleshooting|how_to|comparison|factual",
  "entities": [{"name": "...", "type": "concept|service|function|technology|person"}],
  "query_types": ["code", "section", "table"],
  "keywords": ["关键词1", "关键词2"],
  "language": "zh-CN|en-US",
  "confidence": 0.9
}

规则:
1. intent 必须是预定义类型之一
2. entities 提取查询中的核心概念、技术栈、服务名、函数名等
3. query_types 标记用户想查找的内容类型
4. keywords 提取用于 BM25 检索的关键词
5. confidence 表示分析结果的置信度 [0-1]`, query)

	response, usage, err := s.embedding.ChatCompletion(ctx, prompt)
	if err != nil {
		return nil, usage, fmt.Errorf("failed to analyze query: %w", err)
	}

	// Parse response
	var result QueryAnalysisResult
	if err := parseJSON(response, &result); err != nil {
		return nil, usage, fmt.Errorf("failed to parse analysis result: %w", err)
	}

	// Default values
	if result.Language == "" {
		result.Language = "zh-CN"
	}
	if result.Confidence == 0 {
		result.Confidence = 0.8
	}
	if len(result.QueryTypes) == 0 {
		result.QueryTypes = []string{"section"}
	}

	// Cache the result
	s.cacheAnalysis(ctx, cacheKey, &result, usage.TotalTokens)

	return &result, usage, nil
}

// RewriteQuery generates multiple query variants for improved recall
func (s *QueryService) RewriteQuery(ctx context.Context, tenantID, query string, analysis *QueryAnalysisResult) ([]RewrittenQuery, *LLMUsage, error) {
	// Check cache first
	cacheKey := s.generateCacheKey(query, "rewrite", "v1")
	if cached, err := s.getCachedRewrittenQueries(ctx, cacheKey); err == nil && cached != nil {
		return cached, nil, nil
	}

	entityNames := make([]string, len(analysis.Entities))
	for i, e := range analysis.Entities {
		entityNames[i] = e.Name
	}

	prompt := fmt.Sprintf(`对以下查询进行改写,生成多个查询变体以提高召回率。

原始查询: "%s"
意图: %s
实体: %v
关键词: %v

请生成以下类型的查询变体:
1. 同义词扩展 (使用同义词替换)
2. 结构化改写 (将复杂查询分解为多个子查询)
3. 跨语言查询 (中英文互译)
4. 关键词提取 (提取核心关键词)

返回 JSON 格式:
[
  {"query": "改写后的查询1", "weight": 1.0, "strategy": "synonym"},
  {"query": "改写后的查询2", "weight": 0.9, "strategy": "structured"},
  ...
]

要求:
1. 至少生成 3 个变体,最多 6 个
2. weight 表示该变体的权重 [0.5-1.0],原始语义越接近权重越高
3. strategy 必须是: synonym, structured, translation, expansion 之一
4. 保持查询的原始意图`, query, analysis.Intent, entityNames, analysis.Keywords)

	response, usage, err := s.embedding.ChatCompletion(ctx, prompt)
	if err != nil {
		return nil, usage, fmt.Errorf("failed to rewrite query: %w", err)
	}

	// Parse response
	var rewrites []RewrittenQuery
	if err := parseJSON(response, &rewrites); err != nil {
		return nil, usage, fmt.Errorf("failed to parse rewritten queries: %w", err)
	}

	// Add original query as highest weight
	rewrites = append([]RewrittenQuery{
		{Query: query, Weight: 1.0, Strategy: "original"},
	}, rewrites...)

	// Validate and normalize weights
	for i := range rewrites {
		if rewrites[i].Weight < 0.5 {
			rewrites[i].Weight = 0.5
		}
		if rewrites[i].Weight > 1.0 {
			rewrites[i].Weight = 1.0
		}
	}

	// Cache the result
	s.cacheRewrittenQueries(ctx, cacheKey, rewrites, usage.TotalTokens)

	return rewrites, usage, nil
}

// ExtractKeywords extracts keywords from query for BM25 retrieval
func (s *QueryService) ExtractKeywords(query string, entities []Entity) []string {
	keywords := make(map[string]bool)

	// Add entity names
	for _, e := range entities {
		keywords[e.Name] = true
	}

	// Simple tokenization (split by space and punctuation)
	words := strings.FieldsFunc(query, func(r rune) bool {
		return r == ' ' || r == ',' || r == '。' || r == '，' || r == '、' ||
			r == '!' || r == '?' || r == '；' || r == '：'
	})

	for _, word := range words {
		word = strings.TrimSpace(word)
		if len(word) > 1 { // Filter out single characters
			keywords[word] = true
		}
	}

	result := make([]string, 0, len(keywords))
	for kw := range keywords {
		result = append(result, kw)
	}

	return result
}

// ============================================================================
// Cache Management
// ============================================================================

func (s *QueryService) generateCacheKey(query, operation, version string) string {
	data := fmt.Sprintf("%s|%s|%s", query, operation, version)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

func (s *QueryService) getCachedAnalysis(ctx context.Context, cacheKey string) (*QueryAnalysisResult, error) {
	var cache models.QueryRewriteCache
	if err := s.db.WithContext(ctx).
		Where("cache_key = ? AND expires_at > ?", cacheKey, time.Now()).
		First(&cache).Error; err != nil {
		return nil, err
	}

	// Update hit count
	s.db.Model(&cache).
		UpdateColumn("hit_count", gorm.Expr("hit_count + 1")).
		UpdateColumn("last_hit_at", time.Now())

	// Parse rewritten queries
	var result QueryAnalysisResult
	if data, ok := cache.RewrittenQueries["analysis"]; ok {
		jsonBytes, _ := json.Marshal(data)
		json.Unmarshal(jsonBytes, &result)
		return &result, nil
	}

	return nil, fmt.Errorf("analysis not found in cache")
}

func (s *QueryService) cacheAnalysis(ctx context.Context, cacheKey string, result *QueryAnalysisResult, tokens int) error {
	cache := models.QueryRewriteCache{
		TenantModel: models.TenantModel{
			BaseModel: models.BaseModel{
				ID: models.GenerateID(),
			},
		},
		CacheKey:      cacheKey,
		OriginalQuery: "",
		RewrittenQueries: models.JSONB{
			"analysis": result,
		},
		Strategy:  "analysis",
		HitCount:  0,
		LastHitAt: time.Now(),
		ExpiresAt: timePtr(time.Now().Add(24 * time.Hour)),
	}

	return s.db.WithContext(ctx).Create(&cache).Error
}

func (s *QueryService) getCachedRewrittenQueries(ctx context.Context, cacheKey string) ([]RewrittenQuery, error) {
	var cache models.QueryRewriteCache
	if err := s.db.WithContext(ctx).
		Where("cache_key = ? AND expires_at > ?", cacheKey, time.Now()).
		First(&cache).Error; err != nil {
		return nil, err
	}

	// Update hit count
	s.db.Model(&cache).
		UpdateColumn("hit_count", gorm.Expr("hit_count + 1")).
		UpdateColumn("last_hit_at", time.Now())

	// Parse rewritten queries
	if data, ok := cache.RewrittenQueries["queries"]; ok {
		jsonBytes, _ := json.Marshal(data)
		var queries []RewrittenQuery
		if err := json.Unmarshal(jsonBytes, &queries); err == nil {
			return queries, nil
		}
	}

	return nil, fmt.Errorf("rewritten queries not found in cache")
}

func (s *QueryService) cacheRewrittenQueries(ctx context.Context, cacheKey string, queries []RewrittenQuery, tokens int) error {
	cache := models.QueryRewriteCache{
		TenantModel: models.TenantModel{
			BaseModel: models.BaseModel{
				ID: models.GenerateID(),
			},
		},
		CacheKey:      cacheKey,
		OriginalQuery: "",
		RewrittenQueries: models.JSONB{
			"queries": queries,
		},
		Strategy:  "rewrite",
		HitCount:  0,
		LastHitAt: time.Now(),
		ExpiresAt: timePtr(time.Now().Add(24 * time.Hour)),
	}

	return s.db.WithContext(ctx).Create(&cache).Error
}

// Helper function
func parseJSON(s string, v interface{}) error {
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

func timePtr(t time.Time) *time.Time {
	return &t
}
