# 内容增强系统使用示例

本文档演示如何使用新的内容增强和多层索引架构。

## 1. 初始化服务

```go
package main

import (
	"github.com/jizhuozhi/knowledge/internal/services"
	"github.com/jizhuozhi/knowledge/internal/services/enrichers"
)

func initServices(db *gorm.DB, embeddingService services.LLMProvider) (
	*services.EnrichmentService,
	*services.IndexService,
) {
	// 1. 创建增强服务
	enrichmentService := services.NewEnrichmentService(db, true) // 启用缓存

	// 2. 注册增强处理器
	keywordExtractor := enrichers.NewKeywordExtractor()
	enrichmentService.RegisterEnricher(keywordExtractor)

	llmSummarizer := enrichers.NewLLMSummarizer(embeddingService, "gpt-4o-mini")
	enrichmentService.RegisterEnricher(llmSummarizer)

	// 3. 创建索引服务
	indexService := services.NewIndexService(db, enrichmentService)

	return enrichmentService, indexService
}
```

## 2. 创建增强管道配置

### 2.1 为代码块创建管道

```go
func createCodePipeline(db *gorm.DB, tenantID string) error {
	pipeline := &models.EnrichmentPipeline{
		TenantModel: models.TenantModel{
			TenantID: tenantID,
		},
		Name:        "code_enrichment_pipeline",
		Description: "代码块的增强处理管道",
		
		// 适用于代码块
		ApplicableDocTypes: models.JSONB{
			"types": []string{"knowledge", "experience"},
		},
		ApplicableChunkTypes: models.JSONB{
			"types": []string{"code"},
		},
		
		// 处理器链
		Processors: models.JSONB{
			"processors": []map[string]interface{}{
				{
					"name":    "rule:keyword_extractor",
					"type":    "rule",
					"enabled": true,
					"config":  map[string]interface{}{},
				},
				{
					"name":    "llm:gpt-4o-mini",
					"type":    "llm",
					"enabled": true,
					"config": map[string]interface{}{
						"model":       "gpt-4o-mini",
						"temperature": 0.3,
					},
				},
			},
		},
		
		// 索引策略
		IndexStrategies: models.JSONB{
			"strategies": []map[string]interface{}{
				{
					"type":   "bm25",
					"source": "composite:original+keywords",
					"analyzer": "standard",
					"boost":   1.5,
				},
				{
					"type":   "vector",
					"source": "enrichment:code_explanation",
					"embedding_model": "text-embedding-3-small",
					"dimension":       1536,
				},
			},
		},
		
		Status: "active",
	}
	
	return db.Create(pipeline).Error
}
```

### 2.2 为表格创建管道

```go
func createTablePipeline(db *gorm.DB, tenantID string) error {
	pipeline := &models.EnrichmentPipeline{
		TenantModel: models.TenantModel{
			TenantID: tenantID,
		},
		Name:        "table_enrichment_pipeline",
		Description: "表格的增强处理管道",
		
		ApplicableDocTypes: models.JSONB{
			"types": []string{"*"}, // 所有文档类型
		},
		ApplicableChunkTypes: models.JSONB{
			"types": []string{"table"},
		},
		
		Processors: models.JSONB{
			"processors": []map[string]interface{}{
				{
					"name":    "rule:keyword_extractor",
					"type":    "rule",
					"enabled": true,
				},
				{
					"name":    "llm:gpt-4o-mini",
					"type":    "llm",
					"enabled": true,
					"config": map[string]interface{}{
						"model": "gpt-4o-mini",
					},
				},
			},
		},
		
		IndexStrategies: models.JSONB{
			"strategies": []map[string]interface{}{
				{
					"type":   "bm25",
					"source": "original", // 保留原始表格用于精确匹配
				},
				{
					"type":   "vector",
					"source": "enrichment:table_description", // 用LLM生成的描述做向量检索
					"embedding_model": "text-embedding-3-small",
				},
			},
		},
		
		Status: "active",
	}
	
	return db.Create(pipeline).Error
}
```

## 3. 文档索引流程

### 3.1 完整的文档处理流程

```go
func processDocument(
	ctx context.Context,
	db *gorm.DB,
	enrichmentService *services.EnrichmentService,
	indexService *services.IndexService,
	chunkService *services.ChunkService,
	doc *models.Document,
) error {
	// 1. 分块 (使用现有的 ChunkService)
	strategy := &services.IndexStrategy{
		ChunkStrategy: "semantic",
		ChunkSize:     800,
		SpecialProcessing: "table_aware,code_aware",
	}
	
	chunks, err := chunkService.ChunkDocument(ctx, doc, strategy)
	if err != nil {
		return fmt.Errorf("chunking failed: %w", err)
	}
	
	// 2. 对每个 Chunk 应用增强和索引
	for _, chunk := range chunks {
		// 2.1 确定适用的管道
		pipelineName := determinePipeline(chunk.ChunkType)
		
		// 2.2 增强处理
		enrichments, err := enrichmentService.EnrichChunkWithPipeline(ctx, &chunk, pipelineName)
		if err != nil {
			// 记录错误但继续处理
			log.Printf("enrichment failed for chunk %s: %v", chunk.ID, err)
			continue
		}
		
		log.Printf("chunk %s enriched with %d results", chunk.ID, len(enrichments))
		
		// 2.3 创建索引
		if err := indexService.CreateIndicesForChunk(ctx, &chunk, pipelineName); err != nil {
			log.Printf("indexing failed for chunk %s: %v", chunk.ID, err)
			continue
		}
	}
	
	return nil
}

func determinePipeline(chunkType string) string {
	switch chunkType {
	case "code":
		return "code_enrichment_pipeline"
	case "table":
		return "table_enrichment_pipeline"
	default:
		return "default_pipeline"
	}
}
```

### 3.2 单独处理某个 Chunk

```go
func enrichSingleChunk(
	ctx context.Context,
	enrichmentService *services.EnrichmentService,
	indexService *services.IndexService,
	chunk *models.Chunk,
) error {
	// 1. 手动应用单个增强器
	enrichment, err := enrichmentService.EnrichChunk(ctx, chunk, "llm:gpt-4o-mini", false)
	if err != nil {
		return err
	}
	
	fmt.Printf("Enrichment result:\n")
	fmt.Printf("  Type: %s\n", enrichment.EnrichmentType)
	fmt.Printf("  Content: %s\n", enrichment.EnrichedContent)
	fmt.Printf("  Confidence: %.2f\n", enrichment.Confidence)
	
	// 2. 查看缓存效果
	// 再次调用相同内容,应该命中缓存
	enrichment2, err := enrichmentService.EnrichChunk(ctx, chunk, "llm:gpt-4o-mini", false)
	if err != nil {
		return err
	}
	
	fmt.Printf("Second call (should hit cache): %v\n", enrichment2.LLMUsageID == nil)
	
	return nil
}
```

## 4. 查询增强结果

### 4.1 查看 Chunk 的所有增强结果

```go
func inspectChunkEnrichments(
	ctx context.Context,
	enrichmentService *services.EnrichmentService,
	indexService *services.IndexService,
	chunkID string,
) {
	// 1. 查询所有增强结果
	enrichments, err := enrichmentService.GetEnrichments(ctx, chunkID)
	if err != nil {
		log.Fatal(err)
	}
	
	fmt.Printf("Chunk %s has %d enrichments:\n", chunkID, len(enrichments))
	for _, e := range enrichments {
		fmt.Printf("  - %s (%s v%s)\n", e.EnrichmentType, e.EnricherName, e.EnricherVersion)
		fmt.Printf("    Confidence: %.2f\n", e.Confidence)
		if e.LLMUsageID != nil {
			fmt.Printf("    LLM Usage ID: %s\n", *e.LLMUsageID)
		}
	}
	
	// 2. 查询索引情况
	indices, err := indexService.GetChunkIndices(ctx, chunkID)
	if err != nil {
		log.Fatal(err)
	}
	
	fmt.Printf("\nChunk %s has %d indices:\n", chunkID, len(indices))
	for _, idx := range indices {
		fmt.Printf("  - %s (source: %s)\n", idx.IndexType, idx.ContentSource)
		fmt.Printf("    External ID: %s\n", idx.ExternalIndexID)
		fmt.Printf("    Status: %s\n", idx.Status)
	}
}
```

### 4.2 查看特定类型的增强结果

```go
func getCodeExplanation(
	ctx context.Context,
	enrichmentService *services.EnrichmentService,
	chunkID string,
) (string, error) {
	enrichment, err := enrichmentService.GetEnrichmentByType(ctx, chunkID, "code_explanation")
	if err != nil {
		return "", err
	}
	
	return enrichment.EnrichedContent, nil
}
```

## 5. 检索场景

### 5.1 混合检索 (BM25 + Vector)

```go
func hybridSearch(
	ctx context.Context,
	db *gorm.DB,
	query string,
	knowledgeBaseID string,
) ([]*models.SearchResult, error) {
	// TODO: 实际实现需要集成 OpenSearch 和 Milvus 客户端
	
	// 1. BM25 全文检索 (基于原始内容 + 关键词)
	bm25Results := searchBM25(query, knowledgeBaseID)
	
	// 2. 向量语义检索 (基于 LLM 生成的摘要)
	vectorResults := searchVector(query, knowledgeBaseID)
	
	// 3. 融合结果 (Reciprocal Rank Fusion)
	fusedResults := fuseResults(bm25Results, vectorResults)
	
	// 4. 通过 ChunkIndex 回溯到原始 Chunk
	var results []*models.SearchResult
	for _, item := range fusedResults {
		chunk, err := getChunkFromIndexID(db, item.IndexID)
		if err != nil {
			continue
		}
		
		// 返回原始内容给用户
		results = append(results, &models.SearchResult{
			ID:         chunk.ID,
			DocumentID: chunk.DocumentID,
			Content:    chunk.Content,  // 原始内容
			Score:      item.Score,
			Source:     item.Source, // "bm25" or "vector"
		})
	}
	
	return results, nil
}
```

## 6. 成本分析

### 6.1 查看 LLM 使用统计

```go
func analyzeLLMCost(db *gorm.DB, documentID string) {
	var usages []models.LLMUsageRecord
	db.Where("document_id = ? AND caller_service = ?", documentID, "enrichment").
		Order("created_at DESC").
		Find(&usages)
	
	totalCost := 0.0
	totalTokens := 0
	
	for _, usage := range usages {
		totalCost += usage.EstimatedCost
		totalTokens += usage.TotalTokens
		
		fmt.Printf("%s: %s - %d tokens - $%.4f\n",
			usage.CreatedAt.Format("2006-01-02 15:04:05"),
			usage.CallerMethod,
			usage.TotalTokens,
			usage.EstimatedCost,
		)
	}
	
	fmt.Printf("\nTotal: %d tokens - $%.4f\n", totalTokens, totalCost)
}
```

### 6.2 查看缓存命中率

```go
func analyzeCachePerformance(db *gorm.DB) {
	var caches []models.EnrichmentCache
	db.Order("hit_count DESC").Limit(10).Find(&caches)
	
	fmt.Println("Top 10 most hit caches:")
	for i, cache := range caches {
		fmt.Printf("%d. %s (%s)\n", i+1, cache.EnrichmentType, cache.EnricherName)
		fmt.Printf("   Hit count: %d\n", cache.HitCount)
		fmt.Printf("   Last hit: %s\n", cache.LastHitAt.Format("2006-01-02 15:04:05"))
		if cache.LLMUsageID != nil {
			var usage models.LLMUsageRecord
			db.First(&usage, "id = ?", *cache.LLMUsageID)
			fmt.Printf("   Saved cost: $%.4f x %d = $%.4f\n",
				usage.EstimatedCost, cache.HitCount, usage.EstimatedCost*float64(cache.HitCount))
		}
	}
}
```

## 7. 维护操作

### 7.1 重新索引某个文档

```go
func reindexDocument(
	ctx context.Context,
	db *gorm.DB,
	indexService *services.IndexService,
	documentID string,
) error {
	// 1. 查询文档的所有 Chunk
	var chunks []models.Chunk
	if err := db.Where("document_id = ?", documentID).Find(&chunks).Error; err != nil {
		return err
	}
	
	// 2. 删除旧索引
	for _, chunk := range chunks {
		if err := indexService.DeleteChunkIndices(ctx, chunk.ID); err != nil {
			log.Printf("failed to delete indices for chunk %s: %v", chunk.ID, err)
		}
	}
	
	// 3. 重新创建索引 (增强结果已存在,无需重复调用 LLM)
	for _, chunk := range chunks {
		pipelineName := determinePipeline(chunk.ChunkType)
		if err := indexService.CreateIndicesForChunk(ctx, &chunk, pipelineName); err != nil {
			log.Printf("failed to reindex chunk %s: %v", chunk.ID, err)
		}
	}
	
	return nil
}
```

### 7.2 清理过期缓存

```go
func cleanExpiredCaches(db *gorm.DB) error {
	now := time.Now()
	result := db.Where("expires_at IS NOT NULL AND expires_at < ?", now).
		Delete(&models.EnrichmentCache{})
	
	fmt.Printf("Deleted %d expired caches\n", result.RowsAffected)
	return result.Error
}
```

## 8. 数据库 Migration

```go
// migrations/add_enrichment_tables.go
package migrations

func AddEnrichmentTables(db *gorm.DB) error {
	// 1. 添加新字段到 Chunk 表
	if err := db.Migrator().AddColumn(&models.Chunk{}, "struct_meta"); err != nil {
		return err
	}
	if err := db.Migrator().AddColumn(&models.Chunk{}, "enrichment_status"); err != nil {
		return err
	}
	if err := db.Migrator().AddColumn(&models.Chunk{}, "enrichment_error"); err != nil {
		return err
	}
	
	// 2. 创建新表
	tables := []interface{}{
		&models.ChunkEnrichment{},
		&models.ChunkIndex{},
		&models.EnrichmentPipeline{},
		&models.EnrichmentCache{},
	}
	
	for _, table := range tables {
		if err := db.AutoMigrate(table); err != nil {
			return err
		}
	}
	
	// 3. 创建索引
	if err := db.Exec(`
		CREATE INDEX idx_chunk_enrichment_chunk_type ON chunk_enrichments(chunk_id, enrichment_type);
		CREATE INDEX idx_chunk_index_chunk_type ON chunk_indices(chunk_id, index_type);
		CREATE INDEX idx_enrichment_cache_expires ON enrichment_caches(expires_at);
	`).Error; err != nil {
		return err
	}
	
	return nil
}
```

## 9. 预期效果

### 9.1 代码块示例

**原始内容**:
```go
func AuthMiddleware(secret string) gin.HandlerFunc {
    return func(c *gin.Context) {
        token := c.GetHeader("Authorization")
        // ... JWT validation
    }
}
```

**增强结果**:
- **关键词**: `["AuthMiddleware", "JWT", "token", "Authorization", "gin.HandlerFunc"]`
- **语义摘要**: "这是一个 Gin 框架的 JWT 认证中间件,从请求头中提取 Authorization token,验证 JWT 签名,并将解析出的用户 ID 存入上下文。"

**索引策略**:
- BM25 索引: 原始代码 + 关键词 → 支持精确搜索 "AuthMiddleware"
- 向量索引: 语义摘要 → 支持语义搜索 "如何实现JWT认证"

### 9.2 表格示例

**原始内容**:
```markdown
| 服务名称 | 端口 | 协议 |
|---------|------|------|
| user-api | 8080 | HTTP |
| order-service | 8081 | gRPC |
```

**增强结果**:
- **关键词**: `["user-api", "order-service", "8080", "8081", "HTTP", "gRPC"]`
- **语义描述**: "这是一份微服务架构的服务清单,包含用户API服务和订单服务,分别使用HTTP和gRPC协议。"

**索引策略**:
- BM25 索引: 原始表格 → 支持精确搜索 "user-api"
- 向量索引: 语义描述 → 支持语义搜索 "订单相关的服务"

---

**总结**: 这个架构实现了原始内容、检索内容、语义增强内容的清晰分离,支持灵活的增强处理器和多索引策略,同时保证成本可控和结果可追溯。
