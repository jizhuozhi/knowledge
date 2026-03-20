package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jizhuozhi/knowledge/internal/models"
	"gorm.io/gorm"
)

// ============================================================================
// IndexService - 多索引管理
// ============================================================================

// IndexService 管理 Chunk 的多索引创建和维护
type IndexService struct {
	db                *gorm.DB
	enrichmentService *EnrichmentService
	// TODO: OpenSearch/Milvus 客户端
}

// NewIndexService 创建索引服务
func NewIndexService(db *gorm.DB, enrichmentService *EnrichmentService) *IndexService {
	return &IndexService{
		db:                db,
		enrichmentService: enrichmentService,
	}
}

// CreateIndicesForChunk 为 Chunk 创建索引 (根据 Pipeline 配置)
func (s *IndexService) CreateIndicesForChunk(ctx context.Context, chunk *models.Chunk, pipelineName string) error {
	// 加载 Pipeline
	var pipeline models.EnrichmentPipeline
	if err := s.db.Where("name = ? AND status = ?", pipelineName, "active").First(&pipeline).Error; err != nil {
		return fmt.Errorf("pipeline not found: %w", err)
	}

	// 解析索引策略
	strategies := s.parseIndexStrategies(pipeline.IndexStrategies)

	// 为每个策略创建索引
	for _, strategy := range strategies {
		if err := s.createIndex(ctx, chunk, strategy); err != nil {
			return fmt.Errorf("failed to create index (type=%s): %w", strategy.Type, err)
		}
	}

	return nil
}

// createIndex 创建单个索引
func (s *IndexService) createIndex(ctx context.Context, chunk *models.Chunk, strategy IndexConfig) error {
	// 1. 解析索引内容
	content, err := s.resolveIndexContent(ctx, chunk, strategy.Source)
	if err != nil {
		return fmt.Errorf("failed to resolve content: %w", err)
	}

	// 2. 根据索引类型创建索引
	var externalIndexID string
	switch strategy.Type {
	case "bm25":
		externalIndexID, err = s.createBM25Index(ctx, chunk, content, strategy)
	case "vector":
		externalIndexID, err = s.createVectorIndex(ctx, chunk, content, strategy)
	default:
		return fmt.Errorf("unsupported index type: %s", strategy.Type)
	}

	if err != nil {
		return err
	}

	// 3. 保存 ChunkIndex 记录
	now := time.Now()
	chunkIndex := &models.ChunkIndex{
		TenantModel: models.TenantModel{
			TenantID: chunk.TenantID,
		},
		ChunkID:         chunk.ID,
		IndexType:       strategy.Type,
		ContentSource:   strategy.Source,
		ExternalIndexID: externalIndexID,
		IndexConfig:     strategy.Config,
		Status:          "indexed",
		IndexedAt:       &now,
	}

	if err := s.db.Create(chunkIndex).Error; err != nil {
		return fmt.Errorf("failed to save chunk index: %w", err)
	}

	return nil
}

// ============================================================================
// Content Resolution
// ============================================================================

// resolveIndexContent 根据 content_source 解析出用于索引的内容
func (s *IndexService) resolveIndexContent(ctx context.Context, chunk *models.Chunk, source string) (string, error) {
	parts := strings.SplitN(source, ":", 2)
	sourceType := parts[0]

	switch sourceType {
	case "original":
		// 使用 Chunk 的原始内容
		return chunk.Content, nil

	case "enrichment":
		// 使用指定类型的增强结果
		if len(parts) < 2 {
			return "", fmt.Errorf("enrichment source requires type: enrichment:<type>")
		}
		enrichmentType := parts[1]
		enrichment, err := s.enrichmentService.GetEnrichmentByType(ctx, chunk.ID, enrichmentType)
		if err != nil {
			return "", fmt.Errorf("enrichment not found (type=%s): %w", enrichmentType, err)
		}
		return enrichment.EnrichedContent, nil

	case "composite":
		// 组合多个来源
		if len(parts) < 2 {
			return "", fmt.Errorf("composite source requires sources: composite:<source1>+<source2>")
		}
		sources := strings.Split(parts[1], "+")
		var contents []string
		for _, src := range sources {
			content, err := s.resolveIndexContent(ctx, chunk, src)
			if err != nil {
				// 跳过失败的来源,继续处理其他
				continue
			}
			contents = append(contents, content)
		}
		if len(contents) == 0 {
			return "", fmt.Errorf("no content resolved from composite sources")
		}
		return strings.Join(contents, "\n\n"), nil

	default:
		return "", fmt.Errorf("unknown content source type: %s", sourceType)
	}
}

// ============================================================================
// Index Creation (Stub Implementation)
// ============================================================================

// createBM25Index 创建 BM25 全文索引 (OpenSearch)
func (s *IndexService) createBM25Index(ctx context.Context, chunk *models.Chunk, content string, strategy IndexConfig) (string, error) {
	// TODO: 实现 OpenSearch 索引创建
	// 1. 构建索引文档
	// doc := map[string]interface{}{
	//     "chunk_id": chunk.ID,
	//     "document_id": chunk.DocumentID,
	//     "tenant_id": chunk.TenantID,
	//     "content": content,
	//     "chunk_type": chunk.ChunkType,
	//     "struct_meta": chunk.StructMeta,
	//     "indexed_at": time.Now(),
	// }
	//
	// 2. 发送到 OpenSearch
	// resp, err := s.opensearchClient.Index("knowledge_chunks_bm25", doc)
	// if err != nil {
	//     return "", err
	// }
	//
	// return resp.ID, nil

	// 临时返回模拟 ID
	return fmt.Sprintf("os_%s", chunk.ID), nil
}

// createVectorIndex 创建向量索引 (Milvus)
func (s *IndexService) createVectorIndex(ctx context.Context, chunk *models.Chunk, content string, strategy IndexConfig) (string, error) {
	// TODO: 实现 Milvus 向量索引创建
	// 1. 生成 Embedding
	// embeddingModel := strategy.Config["embedding_model"].(string)
	// embedding, err := s.embeddingClient.Embed(ctx, content, embeddingModel)
	// if err != nil {
	//     return "", err
	// }
	//
	// 2. 插入 Milvus
	// entity := map[string]interface{}{
	//     "chunk_id": chunk.ID,
	//     "document_id": chunk.DocumentID,
	//     "tenant_id": chunk.TenantID,
	//     "chunk_type": chunk.ChunkType,
	//     "embedding": embedding,
	//     "metadata": map[string]interface{}{
	//         "enrichment_type": extractEnrichmentType(strategy.Source),
	//         "embedding_model": embeddingModel,
	//         "content_preview": truncate(content, 200),
	//     },
	// }
	// id, err := s.milvusClient.Insert("knowledge_chunks_vector", entity)
	// if err != nil {
	//     return "", err
	// }
	//
	// return id, nil

	// 临时返回模拟 ID
	return fmt.Sprintf("mv_%s", chunk.ID), nil
}

// ============================================================================
// Query & Deletion
// ============================================================================

// GetChunkIndices 获取 Chunk 的所有索引
func (s *IndexService) GetChunkIndices(ctx context.Context, chunkID string) ([]*models.ChunkIndex, error) {
	var indices []*models.ChunkIndex
	err := s.db.Where("chunk_id = ?", chunkID).Order("created_at ASC").Find(&indices).Error
	return indices, err
}

// DeleteChunkIndices 删除 Chunk 的所有索引
func (s *IndexService) DeleteChunkIndices(ctx context.Context, chunkID string) error {
	// 1. 查询所有索引
	indices, err := s.GetChunkIndices(ctx, chunkID)
	if err != nil {
		return err
	}

	// 2. 从外部索引系统删除
	for _, index := range indices {
		switch index.IndexType {
		case "bm25":
			// TODO: 从 OpenSearch 删除
			// s.opensearchClient.Delete("knowledge_chunks_bm25", index.ExternalIndexID)
		case "vector":
			// TODO: 从 Milvus 删除
			// s.milvusClient.Delete("knowledge_chunks_vector", index.ExternalIndexID)
		}
	}

	// 3. 删除 ChunkIndex 记录
	return s.db.Where("chunk_id = ?", chunkID).Delete(&models.ChunkIndex{}).Error
}

// ============================================================================
// Helper Types
// ============================================================================

// IndexConfig 索引策略配置 (重命名避免与 document.go 的 IndexStrategy 冲突)
type IndexConfig struct {
	Type   string       // "bm25", "vector"
	Source string       // "original", "enrichment:semantic_summary", "composite:..."
	Config models.JSONB // 额外配置
}

func (s *IndexService) parseIndexStrategies(data models.JSONB) []IndexConfig {
	var strategies []IndexConfig

	// JSONB 直接就是 map[string]interface{}
	if stratList, ok := data["strategies"].([]interface{}); ok {
		for _, item := range stratList {
			if strat, ok := item.(map[string]interface{}); ok {
				strategy := IndexConfig{
					Type:   getStringOrDefault(strat, "type", ""),
					Source: getStringOrDefault(strat, "source", "original"),
				}
				// 复制其他字段作为 Config
				config := make(models.JSONB)
				for k, v := range strat {
					if k != "type" && k != "source" {
						config[k] = v
					}
				}
				strategy.Config = config
				strategies = append(strategies, strategy)
			}
		}
	}

	return strategies
}
