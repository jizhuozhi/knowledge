package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jizhuozhi/knowledge/internal/config"
	"github.com/jizhuozhi/knowledge/internal/models"

	"gorm.io/gorm"
)

// QueryAnalyzerService 查询分析服务 - 使用 LLM 识别混合意图
type QueryAnalyzerService struct {
	db         *gorm.DB
	cfg        *config.Config
	embService *EmbeddingService
}

// NewQueryAnalyzerService 创建查询分析服务
func NewQueryAnalyzerService(db *gorm.DB, cfg *config.Config) *QueryAnalyzerService {
	return &QueryAnalyzerService{
		db:         db,
		cfg:        cfg,
		embService: NewEmbeddingService(cfg),
	}
}

// AnalyzeIntent 分析查询意图（完全使用 LLM，支持混合意图）
// TODO: 实现完整的 LLM 调用逻辑
func (s *QueryAnalyzerService) AnalyzeIntent(
	ctx context.Context,
	query string,
	tenantID int64,
) (*models.QueryAnalysisResult, error) {
	startTime := time.Now()
	
	// TODO: 构建 Prompt 并调用 LLM
	// prompt := s.buildIntentAnalysisPrompt(query)
	
	// 暂时返回默认分析结果（占位实现）
	result := &models.QueryAnalysisResult{
		PrimaryIntent:    "content_search",  // 默认为内容搜索
		SecondaryIntents: []string{},
		Confidence:       0.8,
		Entities:         []models.Entity{},
		QueryTypes:       []string{"section"},
		Keywords:         s.extractSimpleKeywords(query),
		Language:         "zh-CN",
		Reasoning:        "占位实现，待完善",
		LLMUsageID:       "",
	}
	
	duration := time.Since(startTime).Milliseconds()
	fmt.Printf("[QueryAnalyzer] 意图分析完成（占位） - 耗时: %dms, 主要意图: %s\n", 
		duration, result.PrimaryIntent)
	
	return result, nil
}

// extractSimpleKeywords 简单关键词提取（占位实现）
func (s *QueryAnalyzerService) extractSimpleKeywords(query string) []string {
	// 简单分词：按空格分割
	words := strings.Fields(query)
	if len(words) == 0 {
		return []string{query}
	}
	if len(words) > 5 {
		return words[:5]
	}
	return words
}

// buildIntentAnalysisPrompt 构建意图分析 Prompt
func (s *QueryAnalyzerService) buildIntentAnalysisPrompt(query string) string {
	return fmt.Sprintf(`你是一个查询意图分析专家。分析以下用户查询,识别意图、提取实体、判断查询类型。

用户查询: "%s"

**意图类型说明**:

内容检索类:
- code_search: 查找代码片段、函数实现
- concept_query: 概念解释、原理说明
- troubleshooting: 问题诊断、错误排查
- how_to: 操作指南、使用教程
- comparison: 对比分析、差异比较
- factual: 事实查询、配置查询

导航类 (基于文档目录结构):
- navigation: 导航查询 - "核心组件有哪些部分？"、"包含什么模块？"
- jump: 跳转查询 - "跳转到缓存层设计"、"定位到API文档"
- outline: 大纲查询 - "文档结构"、"章节目录"

处理类:
- summarization: 摘要总结 - "总结要点"、"概括内容"
- qa: 问答 - "为什么"、"是否正确"

**混合意图识别**:
很多查询包含多个意图,需要识别主要意图和次要意图。

示例:
- "核心组件有哪些？分别是如何实现的？"
  → 主要: navigation (列出组件), 次要: ["content_search"] (实现细节)
  
- "跳转到缓存层设计,并总结核心要点"
  → 主要: jump (定位章节), 次要: ["summarization"] (总结)
  
- "API网关和Nginx有什么区别？"
  → 主要: comparison (对比), 次要: [] (单意图)

请返回 JSON 格式:
{
  "primary_intent": "主要意图类型",
  "secondary_intents": ["次要意图1", "次要意图2"],
  "confidence": 0.95,
  "entities": [
    {"name": "实体名称", "type": "concept|service|function|technology|person"}
  ],
  "query_types": ["code", "section", "table"],
  "keywords": ["关键词1", "关键词2"],
  "language": "zh-CN|en-US",
  "reasoning": "简要说明识别理由（一句话）"
}

注意:
1. primary_intent 必须选择一个最主要的意图
2. secondary_intents 是数组,可以为空（单意图）或包含多个次要意图
3. entities 提取查询中的关键实体（技术名词、服务名、概念等）
4. keywords 提取用于检索的关键词（2-5个）
5. 只返回 JSON,不要有其他文字`, query)
}

// parseAnalysisResult 解析 LLM 返回的分析结果
func (s *QueryAnalyzerService) parseAnalysisResult(response string) (*models.QueryAnalysisResult, error) {
	var result models.QueryAnalysisResult
	
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return nil, fmt.Errorf("JSON 解析失败: %w, 响应: %s", err, response)
	}
	
	// 验证必填字段
	if result.PrimaryIntent == "" {
		return nil, fmt.Errorf("primary_intent 不能为空")
	}
	
	if result.Confidence == 0 {
		result.Confidence = 0.8 // 默认置信度
	}
	
	if len(result.Keywords) == 0 {
		result.Keywords = []string{} // 防止 nil
	}
	
	if result.SecondaryIntents == nil {
		result.SecondaryIntents = []string{} // 防止 nil
	}
	
	if result.Entities == nil {
		result.Entities = []models.Entity{} // 防止 nil
	}
	
	if result.QueryTypes == nil {
		result.QueryTypes = []string{} // 防止 nil
	}
	
	return &result, nil
}

// DecomposeQuery 查询分解 - 将复杂查询分解为多个子查询
// TODO: 使用 LLM 实现智能分解
func (s *QueryAnalyzerService) DecomposeQuery(
	ctx context.Context,
	query string,
	tenantID int64,
) ([]string, error) {
	// 暂时返回原查询（占位实现）
	return []string{query}, nil
}
