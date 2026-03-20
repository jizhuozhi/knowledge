package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jizhuozhi/knowledge/internal/models"
	"gorm.io/gorm"
)

// ============================================================================
// Enricher Interface
// ============================================================================

// Enricher 内容增强处理器接口
type Enricher interface {
	// Name 返回处理器名称 (例如: "llm:gpt-4o-mini", "rule:keyword")
	Name() string

	// Version 返回处理器版本
	Version() string

	// SupportedChunkTypes 返回支持的 Chunk 类型列表
	SupportedChunkTypes() []string

	// Enrich 对 Chunk 进行增强处理
	Enrich(ctx context.Context, chunk *models.Chunk) (*EnrichmentResult, error)
}

// EnrichmentResult 增强处理结果
type EnrichmentResult struct {
	EnrichmentType  string                 // 增强类型 (例如: "semantic_summary", "keyword_extraction")
	EnrichedContent string                 // 增强后的文本内容
	EnrichedData    map[string]interface{} // 结构化增强数据
	Confidence      float64                // 置信度 [0-1]
	LLMUsage        *models.LLMUsageRecord // LLM 使用记录 (如果使用了 LLM)
}

// ============================================================================
// EnrichmentService
// ============================================================================

// EnrichmentService 管理增强处理流程
type EnrichmentService struct {
	db         *gorm.DB
	enrichers  map[string]Enricher // 注册的处理器: name -> enricher
	cacheEnabled bool
}

// NewEnrichmentService 创建增强服务
func NewEnrichmentService(db *gorm.DB, cacheEnabled bool) *EnrichmentService {
	return &EnrichmentService{
		db:           db,
		enrichers:    make(map[string]Enricher),
		cacheEnabled: cacheEnabled,
	}
}

// RegisterEnricher 注册增强处理器
func (s *EnrichmentService) RegisterEnricher(enricher Enricher) {
	s.enrichers[enricher.Name()] = enricher
}

// GetEnricher 获取注册的处理器
func (s *EnrichmentService) GetEnricher(name string) (Enricher, bool) {
	enricher, ok := s.enrichers[name]
	return enricher, ok
}

// EnrichChunk 对单个 Chunk 应用增强处理器
func (s *EnrichmentService) EnrichChunk(ctx context.Context, chunk *models.Chunk, enricherName string, force bool) (*models.ChunkEnrichment, error) {
	enricher, ok := s.enrichers[enricherName]
	if !ok {
		return nil, fmt.Errorf("enricher not found: %s", enricherName)
	}

	// 检查是否支持该 Chunk 类型
	supported := false
	for _, ct := range enricher.SupportedChunkTypes() {
		if ct == chunk.ChunkType || ct == "*" {
			supported = true
			break
		}
	}
	if !supported {
		return nil, fmt.Errorf("enricher %s does not support chunk type: %s", enricherName, chunk.ChunkType)
	}

	// 生成缓存键
	cacheKey := generateCacheKey(chunk.Content, enricher.Name(), enricher.Version())

	// 尝试从缓存读取 (如果启用且非强制模式)
	if s.cacheEnabled && !force {
		cached, err := s.getFromCache(ctx, cacheKey)
		if err == nil && cached != nil {
			// 缓存命中
			return s.createEnrichmentFromCache(chunk, cached)
		}
	}

	// 执行增强处理
	result, err := enricher.Enrich(ctx, chunk)
	if err != nil {
		return nil, fmt.Errorf("enrichment failed: %w", err)
	}

	// 保存 LLM 使用记录 (如果有)
	var llmUsageID *string
	if result.LLMUsage != nil {
		result.LLMUsage.TenantID = chunk.TenantID
		result.LLMUsage.DocumentID = &chunk.DocumentID
		if err := s.db.Create(result.LLMUsage).Error; err != nil {
			return nil, fmt.Errorf("failed to save LLM usage: %w", err)
		}
		llmUsageID = &result.LLMUsage.ID
	}

	// 创建 ChunkEnrichment 记录
	enrichment := &models.ChunkEnrichment{
		TenantModel: models.TenantModel{
			TenantID: chunk.TenantID,
		},
		ChunkID:         chunk.ID,
		EnrichmentType:  result.EnrichmentType,
		EnrichedContent: result.EnrichedContent,
		EnrichedData:    result.EnrichedData,
		EnricherName:    enricher.Name(),
		EnricherVersion: enricher.Version(),
		Confidence:      result.Confidence,
		LLMUsageID:      llmUsageID,
	}

	if err := s.db.Create(enrichment).Error; err != nil {
		return nil, fmt.Errorf("failed to save enrichment: %w", err)
	}

	// 保存到缓存
	if s.cacheEnabled {
		_ = s.saveToCache(ctx, cacheKey, chunk.Content, enrichment, llmUsageID)
	}

	return enrichment, nil
}

// EnrichChunkWithPipeline 使用 Pipeline 对 Chunk 进行增强
func (s *EnrichmentService) EnrichChunkWithPipeline(ctx context.Context, chunk *models.Chunk, pipelineName string) ([]*models.ChunkEnrichment, error) {
	// 加载 Pipeline 配置
	pipeline, err := s.loadPipeline(pipelineName)
	if err != nil {
		return nil, err
	}

	// 检查适用性
	if !s.isPipelineApplicable(pipeline, chunk) {
		return nil, fmt.Errorf("pipeline %s is not applicable to chunk type %s", pipelineName, chunk.ChunkType)
	}

	// 更新 Chunk 状态
	if err := s.db.Model(chunk).Updates(map[string]interface{}{
		"enrichment_status": "processing",
		"enrichment_error":  "",
	}).Error; err != nil {
		return nil, err
	}

	// 执行处理器链
	var enrichments []*models.ChunkEnrichment
	processors := s.parseProcessors(pipeline.Processors)

	for _, proc := range processors {
		if !proc.Enabled {
			continue
		}

		enrichment, err := s.EnrichChunk(ctx, chunk, proc.Name, false)
		if err != nil {
			// 记录错误但继续处理其他处理器
			_ = s.db.Model(chunk).Update("enrichment_error", err.Error()).Error
			continue
		}
		enrichments = append(enrichments, enrichment)
	}

	// 更新完成状态
	status := "completed"
	if len(enrichments) == 0 {
		status = "failed"
	}
	if err := s.db.Model(chunk).Update("enrichment_status", status).Error; err != nil {
		return nil, err
	}

	return enrichments, nil
}

// GetEnrichments 获取 Chunk 的所有增强结果
func (s *EnrichmentService) GetEnrichments(ctx context.Context, chunkID string) ([]*models.ChunkEnrichment, error) {
	var enrichments []*models.ChunkEnrichment
	err := s.db.Where("chunk_id = ?", chunkID).Order("created_at ASC").Find(&enrichments).Error
	return enrichments, err
}

// GetEnrichmentByType 获取指定类型的增强结果
func (s *EnrichmentService) GetEnrichmentByType(ctx context.Context, chunkID string, enrichmentType string) (*models.ChunkEnrichment, error) {
	var enrichment models.ChunkEnrichment
	err := s.db.Where("chunk_id = ? AND enrichment_type = ?", chunkID, enrichmentType).
		Order("created_at DESC").
		First(&enrichment).Error
	if err != nil {
		return nil, err
	}
	return &enrichment, nil
}

// ============================================================================
// Cache Operations
// ============================================================================

func (s *EnrichmentService) getFromCache(ctx context.Context, cacheKey string) (*models.EnrichmentCache, error) {
	var cache models.EnrichmentCache
	err := s.db.Where("cache_key = ?", cacheKey).First(&cache).Error
	if err != nil {
		return nil, err
	}

	// 检查是否过期
	if cache.ExpiresAt != nil && cache.ExpiresAt.Before(time.Now()) {
		return nil, gorm.ErrRecordNotFound
	}

	// 更新命中统计
	now := time.Now()
	_ = s.db.Model(&cache).Updates(map[string]interface{}{
		"hit_count":   cache.HitCount + 1,
		"last_hit_at": now,
	}).Error

	return &cache, nil
}

func (s *EnrichmentService) saveToCache(ctx context.Context, cacheKey string, originalContent string, enrichment *models.ChunkEnrichment, llmUsageID *string) error {
	contentHash := sha256.Sum256([]byte(originalContent))
	preview := originalContent
	if len(preview) > 500 {
		preview = preview[:500]
	}

	cache := &models.EnrichmentCache{
		TenantModel: models.TenantModel{
			TenantID: enrichment.TenantID,
		},
		CacheKey:        cacheKey,
		ContentHash:     hex.EncodeToString(contentHash[:]),
		ContentLength:   len(originalContent),
		ContentPreview:  preview,
		EnrichmentType:  enrichment.EnrichmentType,
		EnrichedContent: enrichment.EnrichedContent,
		EnrichedData:    enrichment.EnrichedData,
		EnricherName:    enrichment.EnricherName,
		EnricherVersion: enrichment.EnricherVersion,
		HitCount:        0,
		LastHitAt:       time.Now(),
		LLMUsageID:      llmUsageID,
		ExpiresAt:       nil, // 永久缓存,可根据需要设置过期时间
	}

	return s.db.Create(cache).Error
}

func (s *EnrichmentService) createEnrichmentFromCache(chunk *models.Chunk, cache *models.EnrichmentCache) (*models.ChunkEnrichment, error) {
	enrichment := &models.ChunkEnrichment{
		TenantModel: models.TenantModel{
			TenantID: chunk.TenantID,
		},
		ChunkID:         chunk.ID,
		EnrichmentType:  cache.EnrichmentType,
		EnrichedContent: cache.EnrichedContent,
		EnrichedData:    cache.EnrichedData,
		EnricherName:    cache.EnricherName,
		EnricherVersion: cache.EnricherVersion,
		Confidence:      1.0, // 缓存结果认为是高置信度
		LLMUsageID:      cache.LLMUsageID,
	}

	if err := s.db.Create(enrichment).Error; err != nil {
		return nil, err
	}

	return enrichment, nil
}

// ============================================================================
// Helper Functions
// ============================================================================

func generateCacheKey(content string, enricherName string, enricherVersion string) string {
	data := fmt.Sprintf("%s|%s|%s", content, enricherName, enricherVersion)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

func (s *EnrichmentService) loadPipeline(name string) (*models.EnrichmentPipeline, error) {
	var pipeline models.EnrichmentPipeline
	err := s.db.Where("name = ? AND status = ?", name, "active").First(&pipeline).Error
	return &pipeline, err
}

func (s *EnrichmentService) isPipelineApplicable(pipeline *models.EnrichmentPipeline, chunk *models.Chunk) bool {
	// ApplicableChunkTypes 是 JSONB,可能包含数组
	if pipeline.ApplicableChunkTypes == nil {
		return false
	}

	// 尝试从 JSONB 中提取数组
	var typeList []string
	if types, ok := pipeline.ApplicableChunkTypes["types"]; ok {
		if typesArray, ok := types.([]interface{}); ok {
			for _, t := range typesArray {
				if tStr, ok := t.(string); ok {
					typeList = append(typeList, tStr)
				}
			}
		}
	}

	// 检查 chunk.ChunkType 是否在列表中
	for _, ct := range typeList {
		if ct == chunk.ChunkType || ct == "*" {
			return true
		}
	}

	return false
}

// ProcessorConfig 处理器配置
type ProcessorConfig struct {
	Name    string                 `json:"name"`
	Type    string                 `json:"type"`    // "rule", "llm", "ml"
	Enabled bool                   `json:"enabled"` // 默认 true
	Config  map[string]interface{} `json:"config"`
}

func (s *EnrichmentService) parseProcessors(data models.JSONB) []ProcessorConfig {
	var processors []ProcessorConfig

	// JSONB 直接就是 map[string]interface{}
	if procList, ok := data["processors"].([]interface{}); ok {
		for _, item := range procList {
			if proc, ok := item.(map[string]interface{}); ok {
				config := ProcessorConfig{
					Name:    getStringOrDefault(proc, "name", ""),
					Type:    getStringOrDefault(proc, "type", ""),
					Enabled: getBoolOrDefault(proc, "enabled", true),
				}
				if cfg, ok := proc["config"].(map[string]interface{}); ok {
					config.Config = cfg
				}
				processors = append(processors, config)
			}
		}
	}

	return processors
}

func getStringOrDefault(m map[string]interface{}, key string, defaultValue string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return defaultValue
}

func getBoolOrDefault(m map[string]interface{}, key string, defaultValue bool) bool {
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return defaultValue
}
