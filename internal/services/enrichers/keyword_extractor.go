package enrichers

import (
	"context"
	"regexp"
	"strings"

	"github.com/jizhuozhi/knowledge/internal/models"
	"github.com/jizhuozhi/knowledge/internal/services"
)

// KeywordExtractor 基于规则的关键词提取器
type KeywordExtractor struct {
	version string
}

// NewKeywordExtractor 创建关键词提取器
func NewKeywordExtractor() *KeywordExtractor {
	return &KeywordExtractor{
		version: "1.0.0",
	}
}

func (e *KeywordExtractor) Name() string {
	return "rule:keyword_extractor"
}

func (e *KeywordExtractor) Version() string {
	return e.version
}

func (e *KeywordExtractor) SupportedChunkTypes() []string {
	return []string{"*"} // 支持所有类型
}

func (e *KeywordExtractor) Enrich(ctx context.Context, chunk *models.Chunk) (*services.EnrichmentResult, error) {
	keywords := e.extractKeywords(chunk)

	return &services.EnrichmentResult{
		EnrichmentType:  "keyword_extraction",
		EnrichedContent: strings.Join(keywords, ", "),
		EnrichedData: map[string]interface{}{
			"keywords": keywords,
			"count":    len(keywords),
		},
		Confidence: 1.0, // 规则提取,置信度为 1
		LLMUsage:   nil,
	}, nil
}

func (e *KeywordExtractor) extractKeywords(chunk *models.Chunk) []string {
	content := chunk.Content

	switch chunk.ChunkType {
	case "code":
		return e.extractCodeKeywords(content, chunk.StructMeta)
	case "table":
		return e.extractTableKeywords(content, chunk.StructMeta)
	default:
		return e.extractGeneralKeywords(content)
	}
}

// extractCodeKeywords 提取代码块关键词
func (e *KeywordExtractor) extractCodeKeywords(content string, structMeta models.JSONB) []string {
	keywords := make(map[string]bool)

	// 从 StructMeta 提取
	if structMeta != nil {
		if language, ok := structMeta["language"].(string); ok {
			keywords[language] = true
		}
		if name, ok := structMeta["name"].(string); ok && name != "" {
			keywords[name] = true
		}
		if imports, ok := structMeta["imports"].([]interface{}); ok {
			for _, imp := range imports {
				if impStr, ok := imp.(string); ok {
					// 提取包名 (最后一段)
					parts := strings.Split(impStr, "/")
					if len(parts) > 0 {
						pkgName := strings.Trim(parts[len(parts)-1], "\"")
						keywords[pkgName] = true
					}
				}
			}
		}
	}

	// 正则提取标识符 (驼峰命名、下划线命名)
	identifierRegex := regexp.MustCompile(`\b[A-Z][a-zA-Z0-9]+\b|\b[a-z_][a-z0-9_]+\b`)
	matches := identifierRegex.FindAllString(content, -1)
	for _, match := range matches {
		// 过滤常见词
		if !isCommonWord(match) && len(match) > 2 {
			keywords[match] = true
		}
	}

	// 转为列表
	result := make([]string, 0, len(keywords))
	for kw := range keywords {
		result = append(result, kw)
	}
	return result
}

// extractTableKeywords 提取表格关键词
func (e *KeywordExtractor) extractTableKeywords(content string, structMeta models.JSONB) []string {
	keywords := make(map[string]bool)

	// 从 StructMeta 提取表头
	if structMeta != nil {
		if headers, ok := structMeta["headers"].([]interface{}); ok {
			for _, h := range headers {
				if hStr, ok := h.(string); ok {
					keywords[hStr] = true
				}
			}
		}
	}

	// 提取表格单元格中的关键词 (简单分词)
	// 分割表格行
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if strings.Contains(line, "|") {
			cells := strings.Split(line, "|")
			for _, cell := range cells {
				cell = strings.TrimSpace(cell)
				if cell != "" && cell != "---" && !strings.HasPrefix(cell, "-") {
					// 提取中英文词汇
					words := regexp.MustCompile(`[\w\u4e00-\u9fa5]+`).FindAllString(cell, -1)
					for _, word := range words {
						if len(word) > 1 && !isCommonWord(word) {
							keywords[word] = true
						}
					}
				}
			}
		}
	}

	result := make([]string, 0, len(keywords))
	for kw := range keywords {
		result = append(result, kw)
	}
	return result
}

// extractGeneralKeywords 提取通用关键词
func (e *KeywordExtractor) extractGeneralKeywords(content string) []string {
	keywords := make(map[string]int)

	// 简单分词: 中英文词汇
	wordRegex := regexp.MustCompile(`[\w\u4e00-\u9fa5]{2,}`)
	words := wordRegex.FindAllString(content, -1)

	for _, word := range words {
		word = strings.ToLower(word)
		if !isCommonWord(word) {
			keywords[word]++
		}
	}

	// 按频率排序,取 Top 20
	type kwFreq struct {
		word string
		freq int
	}
	var sorted []kwFreq
	for w, f := range keywords {
		sorted = append(sorted, kwFreq{w, f})
	}

	// 简单冒泡排序
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].freq > sorted[i].freq {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	// 取 Top 20
	limit := 20
	if len(sorted) < limit {
		limit = len(sorted)
	}

	result := make([]string, limit)
	for i := 0; i < limit; i++ {
		result[i] = sorted[i].word
	}

	return result
}

// isCommonWord 检查是否为常见停用词
func isCommonWord(word string) bool {
	word = strings.ToLower(word)
	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true,
		"but": true, "in": true, "on": true, "at": true, "to": true,
		"for": true, "of": true, "with": true, "by": true, "from": true,
		"is": true, "are": true, "was": true, "were": true, "be": true,
		"have": true, "has": true, "had": true, "do": true, "does": true,
		"did": true, "will": true, "would": true, "should": true, "could": true,
		"may": true, "might": true, "can": true, "this": true, "that": true,
		"these": true, "those": true, "i": true, "you": true, "he": true,
		"she": true, "it": true, "we": true, "they": true, "var": true,
		"func": true, "return": true, "if": true, "else": true, "while": true,
		"true": true, "false": true, "null": true, "nil": true,
		"的": true, "了": true, "在": true, "是": true, "我": true,
		"有": true, "和": true, "就": true, "不": true, "人": true,
		"都": true, "一": true, "一个": true, "上": true, "也": true,
		"很": true, "到": true, "说": true, "要": true, "去": true,
	}

	return stopWords[word]
}
