package enrichers

import (
	"context"
	"fmt"
	"time"

	"github.com/jizhuozhi/knowledge/internal/models"
	"github.com/jizhuozhi/knowledge/internal/services"
)

// LLMSummarizer 基于 LLM 的语义摘要生成器
type LLMSummarizer struct {
	embeddingService services.LLMProvider // 使用 LLMProvider 接口
	version          string
	modelName        string
}

// NewLLMSummarizer 创建 LLM 摘要器
func NewLLMSummarizer(embeddingService services.LLMProvider, modelName string) *LLMSummarizer {
	if modelName == "" {
		modelName = "gpt-4o-mini"
	}
	return &LLMSummarizer{
		embeddingService: embeddingService,
		version:          "1.0.0",
		modelName:        modelName,
	}
}

func (e *LLMSummarizer) Name() string {
	return fmt.Sprintf("llm:%s", e.modelName)
}

func (e *LLMSummarizer) Version() string {
	return e.version
}

func (e *LLMSummarizer) SupportedChunkTypes() []string {
	return []string{"*"} // 支持所有类型
}

func (e *LLMSummarizer) Enrich(ctx context.Context, chunk *models.Chunk) (*services.EnrichmentResult, error) {
	startTime := time.Now()

	// 根据 ChunkType 选择合适的 Prompt
	var prompt string
	var enrichmentType string

	switch chunk.ChunkType {
	case "code":
		enrichmentType = "code_explanation"
		prompt = e.buildCodePrompt(chunk)
	case "table":
		enrichmentType = "table_description"
		prompt = e.buildTablePrompt(chunk)
	default:
		enrichmentType = "semantic_summary"
		prompt = e.buildGeneralPrompt(chunk)
	}

	// 调用 LLM
	summary, usage, err := e.embeddingService.ChatCompletion(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM call failed: %w", err)
	}

	duration := time.Since(startTime)

	// 创建 LLM 使用记录
	llmUsage := &models.LLMUsageRecord{
		CallerService: "enrichment",
		CallerMethod:  enrichmentType,
		ModelID:       e.modelName,
		ModelType:     "chat",
		InputTokens:   usage.InputTokens,
		OutputTokens:  usage.OutputTokens,
		TotalTokens:   usage.TotalTokens,
		EstimatedCost: e.calculateCost(usage),
		DurationMs:    duration.Milliseconds(),
		Status:        "success",
	}

	return &services.EnrichmentResult{
		EnrichmentType:  enrichmentType,
		EnrichedContent: summary,
		EnrichedData: map[string]interface{}{
			"model":         e.modelName,
			"prompt_tokens": usage.InputTokens,
			"total_tokens":  usage.TotalTokens,
		},
		Confidence: 0.9, // LLM 结果置信度设为 0.9
		LLMUsage:   llmUsage,
	}, nil
}

func (e *LLMSummarizer) buildCodePrompt(chunk *models.Chunk) string {
	language := "unknown"
	if chunk.StructMeta != nil {
		if lang, ok := chunk.StructMeta["language"].(string); ok {
			language = lang
		}
	}

	return fmt.Sprintf(`请用简洁的自然语言描述以下 %s 代码的功能和用途,重点说明:
1. 代码的核心功能
2. 输入参数和返回值
3. 使用场景

代码:
%s

要求:
- 用 2-3 句话总结
- 使用通俗易懂的语言,避免过多技术细节
- 便于后续语义检索`, language, chunk.Content)
}

func (e *LLMSummarizer) buildTablePrompt(chunk *models.Chunk) string {
	return fmt.Sprintf(`请用简洁的自然语言描述以下表格的内容和用途,重点说明:
1. 表格记录的数据类型和领域
2. 数据的关键维度和数量级
3. 表格的使用场景

表格:
%s

要求:
- 用 2-3 句话总结
- 突出表格的业务含义
- 便于后续语义检索`, chunk.Content)
}

func (e *LLMSummarizer) buildGeneralPrompt(chunk *models.Chunk) string {
	return fmt.Sprintf(`请用简洁的自然语言总结以下文本的核心内容:

%s

要求:
- 用 2-3 句话概括主要内容
- 保留关键信息和概念
- 便于后续语义检索`, chunk.Content)
}

// calculateCost 估算 LLM 调用成本 (USD)
func (e *LLMSummarizer) calculateCost(usage *services.LLMUsage) float64 {
	// GPT-4o-mini 定价 (示例,实际需根据最新定价调整)
	// Input: $0.15 / 1M tokens
	// Output: $0.60 / 1M tokens
	inputCost := float64(usage.InputTokens) / 1000000.0 * 0.15
	outputCost := float64(usage.OutputTokens) / 1000000.0 * 0.60
	return inputCost + outputCost
}
