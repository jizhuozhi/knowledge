package services

import (
	"context"
	"fmt"
	"sort"

	"github.com/jizhuozhi/knowledge/internal/config"
	"gorm.io/gorm"
)

// RerankerService handles result reranking using LLM
type RerankerService struct {
	db           *gorm.DB
	config       *config.Config
	embedding    *EmbeddingService
	usageTracker *LLMUsageTracker
}

// NewRerankerService creates a new reranker service
func NewRerankerService(db *gorm.DB, cfg *config.Config) *RerankerService {
	return &RerankerService{
		db:           db,
		config:       cfg,
		embedding:    NewEmbeddingService(cfg),
		usageTracker: NewLLMUsageTracker(db, cfg),
	}
}

// RerankingScore represents a reranking score from LLM
type RerankingScore struct {
	Index  int     `json:"index"`
	Score  float64 `json:"score"`
	Reason string  `json:"reason"`
}

// Rerank performs LLM-based reranking on recall results
func (s *RerankerService) Rerank(ctx context.Context, tenantID, query string, results []RecallResult, topK int) ([]RecallResult, *LLMUsage, error) {
	if len(results) == 0 {
		return results, nil, nil
	}

	// Only rerank top N candidates to control cost
	maxCandidates := 30
	if len(results) > maxCandidates {
		results = results[:maxCandidates]
	}

	// Build prompt
	resultText := ""
	for i, r := range results {
		preview := r.Content
		if len(preview) > 500 {
			preview = preview[:500] + "..."
		}
		resultText += fmt.Sprintf("\n[%d] 标题: %s\n内容预览: %s\n来源通道: %s\nRRF分数: %.4f\n",
			i+1, r.Title, preview, r.Channel, r.RRFScore)
	}

	prompt := fmt.Sprintf(`你是一个搜索相关性评分专家。请对以下候选文档与用户查询的相关性进行打分。

用户查询: "%s"

候选文档:
%s

请对每个文档打分 (0-100分),分数越高表示与查询越相关。

返回 JSON 格式:
[
  {"index": 1, "score": 95, "reason": "完全匹配用户查询的核心需求..."},
  {"index": 2, "score": 70, "reason": "部分相关,提供了背景知识..."},
  ...
]

要求:
1. 分数范围 0-100,必须提供打分理由
2. 按照相关性从高到低排序
3. 考虑内容的准确性、完整性、实用性
4. 来源通道(bm25/vector/graph)和RRF分数仅供参考,不要过度依赖`, query, resultText)

	response, usage, err := s.embedding.ChatCompletion(ctx, prompt)
	if err != nil {
		// Fallback: return original order on error
		return results, usage, fmt.Errorf("reranking failed: %w", err)
	}

	// Parse scores
	var scores []RerankingScore
	if err := parseJSON(response, &scores); err != nil {
		// Fallback: return original order
		return results, usage, fmt.Errorf("failed to parse reranking scores: %w", err)
	}

	// Apply scores to results
	scoreMap := make(map[int]RerankingScore)
	for _, sc := range scores {
		scoreMap[sc.Index] = sc
	}

	for i := range results {
		if sc, ok := scoreMap[i+1]; ok {
			results[i].RerankedScore = &sc.Score
			if results[i].Metadata == nil {
				results[i].Metadata = make(map[string]interface{})
			}
			results[i].Metadata["rerank_reason"] = sc.Reason
		}
	}

	// Sort by reranked score
	sort.Slice(results, func(i, j int) bool {
		si := results[i].RerankedScore
		sj := results[j].RerankedScore

		if si == nil && sj == nil {
			return results[i].RRFScore > results[j].RRFScore
		}
		if si == nil {
			return false
		}
		if sj == nil {
			return true
		}
		return *si > *sj
	})

	// Update final ranks
	for i := range results {
		rank := i + 1
		results[i].FinalRank = &rank
	}

	// Return top K
	if len(results) > topK {
		results = results[:topK]
	}

	return results, usage, nil
}
