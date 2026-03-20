# Query 端智能处理流水线

## 1. 概述

本文档定义 RAG 系统中 Query 端的完整处理流程，包括：
- Query 分析 (意图识别、实体提取)
- Query 改写 (同义词扩展、结构化)
- 多通道召回 (BM25 + Vector + Graph)
- 结果融合 (RRF)
- 智能重排序 (LLM Reranking)

---

## 2. 数据模型

### 2.1 QuerySession (查询会话)
```go
type QuerySession struct {
    TenantModel
    KnowledgeBaseID string `gorm:"size:12;not null;index" json:"knowledge_base_id"`
    
    // 原始查询
    OriginalQuery   string `gorm:"type:text;not null" json:"original_query"`
    
    // 查询分析结果
    QueryAnalysis   JSONB  `gorm:"type:jsonb" json:"query_analysis"`
    // {
    //   "intent": "code_search",
    //   "entities": ["JWT", "认证"],
    //   "query_type": ["code", "concept"],
    //   "language": "zh-CN",
    //   "confidence": 0.9
    // }
    
    // 改写后的查询
    RewrittenQueries JSONB `gorm:"type:jsonb" json:"rewritten_queries"`
    // [
    //   {"query": "JWT 中间件实现代码", "weight": 1.0, "strategy": "structured"},
    //   {"query": "token验证逻辑", "weight": 0.8, "strategy": "synonym"},
    //   {"query": "JWT authentication", "weight": 0.7, "strategy": "translation"}
    // ]
    
    // 召回配置
    RecallConfig    JSONB  `gorm:"type:jsonb" json:"recall_config"`
    // {
    //   "channels": ["bm25", "vector", "graph"],
    //   "top_k_per_channel": 20,
    //   "filters": {"chunk_types": ["code", "section"]}
    // }
    
    // 最终答案
    Answer          string `gorm:"type:text" json:"answer"`
    
    // 性能指标
    DurationMs      int64  `gorm:"default:0" json:"duration_ms"`
    
    // 成本记录
    LLMUsageIDs     JSONB  `gorm:"type:jsonb" json:"llm_usage_ids"` // 关联的所有 LLM 调用
}
```

### 2.2 RecallResult (召回结果)
```go
type RecallResult struct {
    TenantModel
    QuerySessionID string `gorm:"size:12;not null;index" json:"query_session_id"`
    
    // 召回通道
    Channel        string `gorm:"size:20;not null" json:"channel"` // bm25, vector, graph
    
    // 关联的 Chunk
    ChunkID        string  `gorm:"size:12;not null;index" json:"chunk_id"`
    ChunkIndexID   *string `gorm:"size:12;index" json:"chunk_index_id"` // 哪个索引返回的 (可选)
    
    // 原始分数
    RawScore       float64 `gorm:"default:0" json:"raw_score"`
    Rank           int     `gorm:"default:0" json:"rank"` // 在该通道中的排名
    
    // 融合后分数
    RRFScore       float64 `gorm:"default:0" json:"rrf_score"`
    FusedRank      int     `gorm:"default:0" json:"fused_rank"`
    
    // 重排序分数 (LLM Reranking)
    RerankedScore  *float64 `gorm:"default:null" json:"reranked_score"`
    FinalRank      *int     `gorm:"default:null" json:"final_rank"`
    
    // 是否最终返回给用户
    IsReturned     bool    `gorm:"default:false" json:"is_returned"`
    
    // 调试信息
    DebugInfo      JSONB   `gorm:"type:jsonb" json:"debug_info"`
    // {
    //   "matched_fields": ["keywords"],
    //   "highlight": "JWT 认证中间件",
    //   "graph_path": ["JWT" -> "AuthMiddleware"]
    // }
}
```

### 2.3 QueryRewriteCache (查询改写缓存)
```go
type QueryRewriteCache struct {
    TenantModel
    
    // 缓存键 (基于原始查询生成)
    CacheKey       string `gorm:"size:64;not null;unique;index" json:"cache_key"` // SHA256(query + strategy)
    
    // 原始查询
    OriginalQuery  string `gorm:"type:text;not null" json:"original_query"`
    
    // 改写结果
    RewrittenQueries JSONB `gorm:"type:jsonb;not null" json:"rewritten_queries"`
    
    // 改写策略
    Strategy       string `gorm:"size:50;not null" json:"strategy"` // synonym, structured, translation, expansion
    
    // 使用统计
    HitCount       int       `gorm:"default:0" json:"hit_count"`
    LastHitAt      time.Time `json:"last_hit_at"`
    
    // LLM 使用记录
    LLMUsageID     *string   `gorm:"size:12;index" json:"llm_usage_id"`
    
    // 缓存过期时间
    ExpiresAt      *time.Time `gorm:"index" json:"expires_at"`
}
```

---

## 3. 处理流水线

### 3.1 Query Analysis (查询分析)

#### 3.1.1 意图识别（支持混合意图）

**设计理念**: 真实场景中用户查询往往包含多个意图，系统需要识别主要意图和次要意图，并支持混合意图处理。

```go
// 意图类型定义
type QueryIntent string

const (
    // 内容检索类
    IntentCodeSearch      QueryIntent = "code_search"       // 查找代码片段
    IntentConceptQuery    QueryIntent = "concept_query"     // 概念解释
    IntentTroubleshooting QueryIntent = "troubleshooting"   // 问题诊断
    IntentHowTo           QueryIntent = "how_to"            // 操作指南
    IntentComparison      QueryIntent = "comparison"        // 对比分析
    IntentFactual         QueryIntent = "factual"           // 事实查询
    
    // 导航类 (TOC-based)
    IntentNavigation      QueryIntent = "navigation"        // 导航查询: "有哪些模块"
    IntentJump            QueryIntent = "jump"              // 跳转查询: "跳到xxx章节"
    IntentOutline         QueryIntent = "outline"           // 大纲查询: "文档结构"
    
    // 处理类
    IntentSummarization   QueryIntent = "summarization"     // 摘要总结
    IntentQA              QueryIntent = "qa"                // 问答
)

// 查询分析结果（支持混合意图）
type QueryAnalysisResult struct {
    // 主要意图
    PrimaryIntent   QueryIntent   `json:"primary_intent"`
    
    // 次要意图（混合意图场景）
    SecondaryIntents []QueryIntent `json:"secondary_intents"`
    
    // 意图置信度
    Confidence      float64       `json:"confidence"`
    
    // 提取的实体
    Entities        []Entity      `json:"entities"`
    
    // 查询类型
    QueryTypes      []string      `json:"query_types"` // ["code", "section", "table"]
    
    // 关键词
    Keywords        []string      `json:"keywords"`
    
    // 语言
    Language        string        `json:"language"`
    
    // 分析理由
    Reasoning       string        `json:"reasoning"`
}

// 意图识别（完全使用 LLM）
func AnalyzeQueryIntent(query string) (*QueryAnalysisResult, error) {
    prompt := fmt.Sprintf(`你是一个查询意图分析专家。分析以下用户查询,识别意图、提取实体、判断查询类型。

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
  → 主要: navigation (列出组件), 次要: [content_search] (实现细节)
  
- "跳转到缓存层设计,并总结核心要点"
  → 主要: jump (定位章节), 次要: [summarization] (总结)
  
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
  "reasoning": "简要说明识别理由"
}
`, query)
    
    // 调用 LLM (使用 GPT-4o-mini)
    response := llm.Call(prompt, LLMOptions{
        Model:       "gpt-4o-mini",
        Temperature: 0.3, // 较低温度保证稳定性
    })
    
    // 记录 LLMUsageRecord
    usageID := recordLLMUsage(response)
    
    // 解析结果
    result := parseAnalysisResult(response)
    result.LLMUsageID = usageID
    
    return result, nil
}
```

#### 3.1.2 实体提取
支持多种实体类型：
- **Concept**: 技术概念 (JWT, RESTful, Docker)
- **Service**: 服务名称 (user-api, order-service)
- **Function**: 函数/方法名 (AuthMiddleware, Parse)
- **Technology**: 技术栈 (Go, Python, Redis)
- **Person**: 人员 (负责人、作者)

### 3.2 Query Rewriting (查询改写)

#### 3.2.1 同义词扩展
```go
type SynonymExpander struct {
    dictionary map[string][]string // 同义词词典
    llm        LLMClient
}

func (e *SynonymExpander) Expand(query string, entities []Entity) []RewrittenQuery {
    // 1. 查询本地同义词词典
    localSynonyms := e.lookupDictionary(entities)
    
    // 2. 使用 LLM 生成同义词 (缓存结果)
    llmSynonyms := e.generateSynonyms(query, entities)
    
    // 3. 合并去重
    return mergeAndWeight(localSynonyms, llmSynonyms)
}

// 示例输出:
// 原始: "如何实现JWT认证"
// 扩展:
//   - "JWT authentication 实现" (weight: 1.0)
//   - "token 验证机制" (weight: 0.9)
//   - "用户身份认证" (weight: 0.7)
//   - "JWT 中间件开发" (weight: 0.8)
```

#### 3.2.2 结构化改写
将复杂查询分解为多个子查询：
```go
// 原始: "JWT认证的原理和代码实现"
// 改写:
//   1. "JWT 认证原理解释" (concept_query, weight: 1.0)
//   2. "JWT 认证代码示例" (code_search, weight: 1.0)
//   3. "JWT token 结构说明" (concept_query, weight: 0.8)
```

#### 3.2.3 跨语言查询
```go
// 原始: "如何使用 Docker 部署"
// 扩展:
//   - "How to deploy with Docker" (en, weight: 0.8)
//   - "Docker deployment tutorial" (en, weight: 0.7)
```

#### 3.2.4 关键词提取
```go
// 提取核心关键词用于 BM25 检索
// 原始: "在 Go 项目中如何实现 JWT 认证中间件"
// 关键词: ["Go", "JWT", "认证", "中间件", "实现"]
```

### 3.3 Multi-Channel Recall (多通道召回)

#### 3.3.1 BM25 全文检索
```go
func (s *BM25SearchService) Search(ctx context.Context, query string, keywords []string, filters map[string]interface{}) ([]*RecallResult, error) {
    // 构建 OpenSearch 查询
    osQuery := map[string]interface{}{
        "query": map[string]interface{}{
            "bool": map[string]interface{}{
                "should": []map[string]interface{}{
                    // 原始内容匹配
                    {"match": map[string]interface{}{
                        "original_content": map[string]interface{}{
                            "query": query,
                            "boost": 1.0,
                        },
                    }},
                    // 关键词匹配 (boost 更高)
                    {"terms": map[string]interface{}{
                        "keywords": map[string]interface{}{
                            "value": keywords,
                            "boost": 1.5,
                        },
                    }},
                },
                "filter": buildFilters(filters), // chunk_type, doc_type, tenant_id
            },
        },
        "size": 20, // Top-20
    }
    
    // 执行搜索
    hits := s.opensearch.Search(osQuery)
    
    // 转换为 RecallResult
    results := make([]*RecallResult, len(hits))
    for i, hit := range hits {
        results[i] = &RecallResult{
            Channel:      "bm25",
            ChunkID:      hit.ChunkID,
            ChunkIndexID: &hit.IndexID,
            RawScore:     hit.Score,
            Rank:         i + 1,
            DebugInfo:    JSONB{"matched_fields": hit.MatchedFields},
        }
    }
    
    return results, nil
}
```

#### 3.3.2 Vector 语义检索
```go
func (s *VectorSearchService) Search(ctx context.Context, queries []RewrittenQuery, filters map[string]interface{}) ([]*RecallResult, error) {
    // 1. 对所有改写查询生成 Embedding
    embeddings := make([][]float64, len(queries))
    for i, q := range queries {
        embeddings[i] = s.embeddingService.Embed(q.Query) // 使用 text-embedding-3-small
    }
    
    // 2. 在 Milvus 中搜索 (支持多向量查询)
    searchParams := map[string]interface{}{
        "metric_type": "COSINE",
        "params":      map[string]interface{}{"nprobe": 10},
    }
    
    // 3. 合并多个查询的结果 (加权)
    allResults := make(map[string]*RecallResult) // chunk_id -> result
    
    for i, emb := range embeddings {
        hits := s.milvus.Search(
            "knowledge_chunks_vector", // collection
            [][]float64{emb},
            "embedding",
            searchParams,
            20, // top_k
            buildMilvusFilters(filters),
        )
        
        for rank, hit := range hits {
            chunkID := hit.ChunkID
            weightedScore := hit.Score * queries[i].Weight
            
            if existing, ok := allResults[chunkID]; ok {
                // 已存在,取最高分
                if weightedScore > existing.RawScore {
                    existing.RawScore = weightedScore
                }
            } else {
                allResults[chunkID] = &RecallResult{
                    Channel:      "vector",
                    ChunkID:      chunkID,
                    ChunkIndexID: &hit.IndexID,
                    RawScore:     weightedScore,
                    Rank:         rank + 1,
                }
            }
        }
    }
    
    // 4. 排序
    results := sortByScore(allResults)
    return results[:min(20, len(results))], nil
}
```

#### 3.3.3 TOC 导航检索（新增通道）

**适用场景**: 导航类意图 (navigation, jump, outline)

**核心价值**:
- ✅ 通过文档目录结构快速定位章节
- ✅ 100% 精确匹配,无相似度误差
- ✅ 完整章节召回,保证内容完整性
- ✅ 延迟 < 10ms,性能优异

```go
// TOC 导航服务
type TOCNavigationService struct {
    db *gorm.DB
}

// 文档发现: 通过 TOC 标题索引查找包含相关章节的文档
func (s *TOCNavigationService) DiscoverDocuments(
    ctx context.Context,
    query string,
    keywords []string,
    kbID string,
) ([]DocumentMatch, error) {
    // 提取核心关键词 (通常来自 QueryAnalysisResult.Keywords)
    searchKeyword := keywords[0]
    
    // 在 DocumentTOCIndex 表中搜索匹配的标题
    var tocMatches []models.DocumentTOCIndex
    err := s.db.Where("tenant_id = ? AND knowledge_base_id = ?", ctx.TenantID, kbID).
        Where("title ILIKE ?", "%"+searchKeyword+"%"). // PostgreSQL 不区分大小写
        Order("level ASC, position ASC"). // 优先返回高级别标题
        Limit(50).
        Find(&tocMatches).Error
    
    if err != nil {
        return nil, err
    }
    
    // 按文档分组
    docMap := make(map[string]*DocumentMatch)
    for _, match := range tocMatches {
        if _, exists := docMap[match.DocumentID]; !exists {
            // 加载文档基本信息
            var doc models.Document
            s.db.Select("id, title, doc_type").
                Where("id = ?", match.DocumentID).
                First(&doc)
            
            docMap[match.DocumentID] = &DocumentMatch{
                DocumentID: match.DocumentID,
                Title:      doc.Title,
                DocType:    doc.DocType,
                Sections:   []SectionMatch{},
            }
        }
        
        // 解析 chunk_ids
        var chunkIDs []int
        if ids, ok := match.ChunkIDs["ids"].([]interface{}); ok {
            for _, id := range ids {
                chunkIDs = append(chunkIDs, int(id.(float64)))
            }
        }
        
        // 获取子章节的 chunk_ids (递归)
        childChunkIDs := s.getChildrenChunkIDs(match.ID)
        chunkIDs = append(chunkIDs, childChunkIDs...)
        
        docMap[match.DocumentID].Sections = append(
            docMap[match.DocumentID].Sections,
            SectionMatch{
                Title:    match.Title,
                Path:     match.Path,
                Level:    match.Level,
                ChunkIDs: chunkIDs,
            },
        )
    }
    
    // 转为列表
    results := make([]DocumentMatch, 0, len(docMap))
    for _, doc := range docMap {
        results = append(results, *doc)
    }
    
    return results, nil
}

// 递归获取所有子章节的 chunk IDs
func (s *TOCNavigationService) getChildrenChunkIDs(parentID uint) []int {
    var children []models.DocumentTOCIndex
    s.db.Where("parent_id = ?", parentID).Find(&children)
    
    var allChunkIDs []int
    for _, child := range children {
        // 当前子节点的 chunks
        if ids, ok := child.ChunkIDs["ids"].([]interface{}); ok {
            for _, id := range ids {
                allChunkIDs = append(allChunkIDs, int(id.(float64)))
            }
        }
        
        // 递归获取孙节点
        grandChildIDs := s.getChildrenChunkIDs(child.ID)
        allChunkIDs = append(allChunkIDs, grandChildIDs...)
    }
    
    return allChunkIDs
}

// 数据结构
type DocumentMatch struct {
    DocumentID string         `json:"document_id"`
    Title      string         `json:"title"`
    DocType    string         `json:"doc_type"`
    Sections   []SectionMatch `json:"sections"`
}

type SectionMatch struct {
    Title    string `json:"title"`
    Path     string `json:"path"`     // 完整路径 (h1 > h2 > h3)
    Level    int    `json:"level"`
    ChunkIDs []int  `json:"chunk_ids"` // 该章节及其子章节的所有 chunks
}

// 转换为 RecallResult
func (s *TOCNavigationService) ToRecallResults(matches []DocumentMatch) []*RecallResult {
    results := []*RecallResult{}
    
    for _, doc := range matches {
        for _, section := range doc.Sections {
            // 为该章节的每个 chunk 创建 RecallResult
            for rank, chunkIndex := range section.ChunkIDs {
                results = append(results, &RecallResult{
                    Channel:  "toc_navigation",
                    ChunkID:  fmt.Sprintf("%s_%d", doc.DocumentID, chunkIndex),
                    RawScore: 1.0, // TOC 召回的分数统一为 1.0 (精确匹配)
                    Rank:     rank + 1,
                    DebugInfo: JSONB{
                        "toc_title": section.Title,
                        "toc_path":  section.Path,
                        "toc_level": section.Level,
                    },
                })
            }
        }
    }
    
    return results
}
```

**使用示例**:

查询: "核心组件有哪些部分？"
1. 意图识别: `IntentNavigation`
2. 关键词提取: `["核心组件"]`
3. TOC 查找:
   ```sql
   SELECT * FROM document_toc_index
   WHERE title ILIKE '%核心组件%'
   ORDER BY level ASC;
   ```
4. 返回匹配的章节及其所有子章节的 chunk IDs
5. 直接召回这些 chunks,无需 BM25/Vector 检索

---

#### 3.3.4 Graph 图谱检索
```go
func (s *GraphSearchService) Search(ctx context.Context, entities []Entity, hops int) ([]*RecallResult, error) {
    // 1. 在 Neo4j 中查找实体节点
    cypher := `
    MATCH (e:Entity)
    WHERE e.name IN $entity_names
    RETURN e
    `
    entityNodes := s.neo4j.Run(cypher, map[string]interface{}{
        "entity_names": extractNames(entities),
    })
    
    // 2. 图遍历 (N-hop 邻居)
    cypher = `
    MATCH (start:Entity)-[r*1..%d]-(related)
    WHERE start.id IN $start_ids
    AND type(r) IN ['IMPLEMENTS', 'REFERENCES', 'DEPENDS_ON', 'CONTAINS']
    RETURN DISTINCT related, r, length(r) as distance
    ORDER BY distance ASC
    LIMIT 50
    `
    
    paths := s.neo4j.Run(fmt.Sprintf(cypher, hops), map[string]interface{}{
        "start_ids": extractIDs(entityNodes),
    })
    
    // 3. 提取关联的 Chunk
    chunkIDs := make(map[string]*GraphPath)
    for _, path := range paths {
        if chunkID := path.Related.ChunkID; chunkID != "" {
            // 计算图谱分数 (距离越近分数越高)
            score := 1.0 / float64(path.Distance+1)
            
            if existing, ok := chunkIDs[chunkID]; ok {
                // 多条路径指向同一个 Chunk,累加分数
                existing.Score += score
            } else {
                chunkIDs[chunkID] = &GraphPath{
                    ChunkID: chunkID,
                    Score:   score,
                    Path:    path,
                }
            }
        }
    }
    
    // 4. 转换为 RecallResult
    results := make([]*RecallResult, 0, len(chunkIDs))
    for _, gp := range chunkIDs {
        results = append(results, &RecallResult{
            Channel:   "graph",
            ChunkID:   gp.ChunkID,
            RawScore:  gp.Score,
            DebugInfo: JSONB{"graph_path": gp.Path.String()},
        })
    }
    
    // 5. 排序
    sort.Slice(results, func(i, j int) bool {
        return results[i].RawScore > results[j].RawScore
    })
    
    // 6. 更新 Rank
    for i := range results {
        results[i].Rank = i + 1
    }
    
    return results[:min(10, len(results))], nil
}
```

### 3.4 RRF Fusion (倒数排名融合)

RRF (Reciprocal Rank Fusion) 是一种简单但有效的多通道融合算法：

```go
func FuseResults(bm25Results, vectorResults, graphResults []*RecallResult, k int) []*RecallResult {
    // k 是平滑参数,通常取 60
    if k == 0 {
        k = 60
    }
    
    // 1. 按 chunk_id 聚合所有结果
    aggregated := make(map[string]*RecallResult)
    
    for _, r := range bm25Results {
        if existing, ok := aggregated[r.ChunkID]; ok {
            existing.RRFScore += 1.0 / float64(k+r.Rank)
        } else {
            r.RRFScore = 1.0 / float64(k+r.Rank)
            aggregated[r.ChunkID] = r
        }
    }
    
    for _, r := range vectorResults {
        if existing, ok := aggregated[r.ChunkID]; ok {
            existing.RRFScore += 1.0 / float64(k+r.Rank)
        } else {
            r.RRFScore = 1.0 / float64(k+r.Rank)
            aggregated[r.ChunkID] = r
        }
    }
    
    for _, r := range graphResults {
        if existing, ok := aggregated[r.ChunkID]; ok {
            existing.RRFScore += 1.0 / float64(k+r.Rank)
        } else {
            r.RRFScore = 1.0 / float64(k+r.Rank)
            aggregated[r.ChunkID] = r
        }
    }
    
    // 2. 按 RRF 分数排序
    fused := make([]*RecallResult, 0, len(aggregated))
    for _, r := range aggregated {
        fused = append(fused, r)
    }
    
    sort.Slice(fused, func(i, j int) bool {
        return fused[i].RRFScore > fused[j].RRFScore
    })
    
    // 3. 更新 FusedRank
    for i := range fused {
        fused[i].FusedRank = i + 1
    }
    
    return fused
}

// 示例:
// BM25: chunk_1 (rank=1), chunk_2 (rank=3), chunk_3 (rank=5)
// Vector: chunk_2 (rank=1), chunk_3 (rank=2), chunk_4 (rank=10)
// Graph: chunk_1 (rank=2), chunk_3 (rank=1)
//
// RRF Score (k=60):
// - chunk_1: 1/(60+1) + 1/(60+2) = 0.0164 + 0.0161 = 0.0325
// - chunk_2: 1/(60+3) + 1/(60+1) = 0.0159 + 0.0164 = 0.0323
// - chunk_3: 1/(60+5) + 1/(60+2) + 1/(60+1) = 0.0154 + 0.0161 + 0.0164 = 0.0479 (最高)
// - chunk_4: 1/(60+10) = 0.0143
//
// 融合结果排序: chunk_3 > chunk_1 > chunk_2 > chunk_4
```

### 3.5 LLM Reranking (智能重排序)

使用 LLM 对融合后的候选结果进行重排序，提升相关性：

```go
func (s *RerankerService) Rerank(ctx context.Context, query string, candidates []*RecallResult, topK int) ([]*RecallResult, error) {
    // 1. 获取 Chunk 内容
    chunks := make([]*models.Chunk, len(candidates))
    for i, c := range candidates {
        chunk, err := s.chunkService.GetChunk(ctx, c.ChunkID)
        if err != nil {
            return nil, err
        }
        chunks[i] = chunk
    }
    
    // 2. 构建 Reranking Prompt
    prompt := fmt.Sprintf(`
你是一个搜索相关性评分专家。请对以下候选文档与用户查询的相关性进行打分 (0-100分)。

用户查询: "%s"

候选文档:
`, query)
    
    for i, chunk := range chunks {
        preview := truncate(chunk.Content, 500) // 截取前500字符
        prompt += fmt.Sprintf("\n[%d] %s\n", i+1, preview)
    }
    
    prompt += `
请返回 JSON 格式的打分结果:
[
  {"index": 1, "score": 95, "reason": "完全匹配用户查询..."},
  {"index": 2, "score": 70, "reason": "部分相关..."},
  ...
]

要求:
1. 分数范围 0-100,越高越相关
2. 必须提供打分理由
3. 按照相关性从高到低排序
`
    
    // 3. 调用 LLM (使用 GPT-4o-mini)
    response := s.llm.Call(prompt)
    // 记录 LLMUsageRecord
    
    scores := parseRerankingScores(response)
    
    // 4. 更新 RecallResult
    for _, score := range scores {
        idx := score.Index - 1
        if idx >= 0 && idx < len(candidates) {
            candidates[idx].RerankedScore = &score.Score
        }
    }
    
    // 5. 排序
    sort.Slice(candidates, func(i, j int) bool {
        si := candidates[i].RerankedScore
        sj := candidates[j].RerankedScore
        if si == nil {
            return false
        }
        if sj == nil {
            return true
        }
        return *si > *sj
    })
    
    // 6. 更新 FinalRank
    for i := range candidates {
        rank := i + 1
        candidates[i].FinalRank = &rank
    }
    
    return candidates[:min(topK, len(candidates))], nil
}
```

### 3.6 Context Construction (上下文构建)

获取最终候选结果后，需要构建完整上下文传给 LLM 生成答案：

```go
func (s *ContextBuilder) Build(ctx context.Context, chunks []*models.Chunk) (*RAGContext, error) {
    ragContext := &RAGContext{
        MainChunks: chunks,
    }
    
    // 1. 获取相邻 Chunk (上下文窗口)
    for _, chunk := range chunks {
        neighbors := s.getNeighborChunks(ctx, chunk, 1) // 前后各1个
        ragContext.NeighborChunks = append(ragContext.NeighborChunks, neighbors...)
    }
    
    // 2. 加载 ChunkEnrichment (语义摘要、代码解释)
    for _, chunk := range chunks {
        enrichments := s.enrichmentService.GetEnrichments(ctx, chunk.ID)
        ragContext.Enrichments = append(ragContext.Enrichments, enrichments...)
    }
    
    // 3. 加载图谱上下文
    for _, chunk := range chunks {
        // 查找该 Chunk 关联的实体
        entities := s.graphService.GetEntitiesByChunk(ctx, chunk.ID)
        
        // 查找实体的1-hop关系
        for _, entity := range entities {
            relations := s.graphService.GetRelations(ctx, entity.ID, 1)
            ragContext.GraphContext = append(ragContext.GraphContext, relations...)
        }
    }
    
    // 4. 去重
    ragContext.Deduplicate()
    
    return ragContext, nil
}

type RAGContext struct {
    MainChunks      []*models.Chunk
    NeighborChunks  []*models.Chunk
    Enrichments     []*models.ChunkEnrichment
    GraphContext    []*models.GraphRelation
}

func (c *RAGContext) ToPrompt() string {
    var builder strings.Builder
    
    builder.WriteString("# 相关知识片段\n\n")
    
    for i, chunk := range c.MainChunks {
        builder.WriteString(fmt.Sprintf("## 片段 %d\n", i+1))
        builder.WriteString(chunk.Content)
        builder.WriteString("\n\n")
        
        // 添加语义摘要 (如果有)
        for _, enrich := range c.Enrichments {
            if enrich.ChunkID == chunk.ID && enrich.EnrichmentType == "semantic_summary" {
                builder.WriteString(fmt.Sprintf("**摘要**: %s\n\n", enrich.EnrichedContent))
            }
        }
    }
    
    // 添加图谱关系
    if len(c.GraphContext) > 0 {
        builder.WriteString("\n## 相关知识图谱\n\n")
        for _, rel := range c.GraphContext {
            builder.WriteString(fmt.Sprintf("- %s -> [%s] -> %s\n", 
                rel.SourceEntity.Name, rel.Type, rel.TargetEntity.Name))
        }
    }
    
    return builder.String()
}
```

---

## 4. API 设计

### 4.1 统一查询接口

**端点**: `POST /api/v1/rag/query`

**说明**: V1 API 现已集成混合意图处理能力,自动识别用户查询意图并选择最优检索策略。

**请求**:
```json
{
  "query": "核心组件有哪些？分别是如何实现的？",
  "knowledge_base_id": "kb_001",
  "top_k": 5,
  "enable_rerank": true
}
```

**响应**:
```json
{
  "answer": "## 核心组件\n\n根据系统架构设计...",
  "sources": [...],
  "toc_structure": {...},
  "query_analysis": {
    "primary_intent": "navigation",
    "secondary_intents": ["content_search"],
    "confidence": 0.92
  },
  "query_type": "navigation+content_search",
  "duration_ms": 850,
  "cost": 0.0028
}
```

**特性**:
- ✅ 自动识别混合意图
- ✅ 智能选择检索策略(TOC + BM25 + Vector + Graph)
- ✅ 统一API,无需手动指定处理模式
- ✅ 向后兼容传统单意图查询

### 4.2 辅助端点

#### 意图分析(调试用)
`POST /api/v1/query/analyze`
```json
{"query": "核心组件有哪些？"}
```

返回详细的意图分析结果(主要意图、次要意图、关键词等)

#### 获取文档目录
`GET /api/v1/documents/{id}/toc`

返回文档的完整目录结构

---

## 5. 混合意图处理（Hybrid Intent Processing）

### 4.1 设计理念

**真实场景**: 用户查询往往包含多个诉求,需要组合多种检索策略。

**核心思路**: 意图分解 → 并行执行 → 结果融合

### 4.2 常见混合意图模式

| 模式 | 意图组合 | 执行策略 | 示例查询 |
|------|---------|---------|---------|
| **导航+搜索** | Navigation + ContentSearch | TOC 章节定位 → 章节内深度检索 | "核心组件有哪些？如何实现？" |
| **跳转+摘要** | Jump + Summarization | TOC 精确定位 → LLM 摘要 | "跳到缓存层,总结要点" |
| **大纲+摘要** | Outline + Summarization | TOC 树形结构 → 每节摘要 | "文档结构,每节概述" |
| **对比+搜索** | Comparison + ContentSearch | 多关键词检索 → 结构化对比 | "A和B有什么区别？" |
| **问答+验证** | QA + Verification | 内容搜索 → 多源验证 | "xxx是对的吗？" |

### 4.3 混合意图路由策略

```go
// 混合意图处理入口
func (s *QueryService) ProcessHybridQuery(
    ctx context.Context,
    query string,
    analysis *QueryAnalysisResult,
    kbID string,
) (*RAGResponse, error) {
    // 根据意图组合,选择执行策略
    primaryIntent := analysis.PrimaryIntent
    secondaryIntents := analysis.SecondaryIntents
    
    switch {
    // ========================================================================
    // 模式 1: 导航 + 内容搜索 (最常见)
    // ========================================================================
    case primaryIntent == IntentNavigation && 
         contains(secondaryIntents, IntentContentSearch):
        
        return s.navigationPlusSearch(ctx, query, analysis, kbID)
    
    // ========================================================================
    // 模式 2: 跳转 + 摘要
    // ========================================================================
    case primaryIntent == IntentJump && 
         contains(secondaryIntents, IntentSummarization):
        
        return s.jumpPlusSummarize(ctx, query, analysis, kbID)
    
    // ========================================================================
    // 模式 3: 大纲 + 摘要
    // ========================================================================
    case primaryIntent == IntentOutline && 
         contains(secondaryIntents, IntentSummarization):
        
        return s.outlinePlusSummarize(ctx, query, analysis, kbID)
    
    // ========================================================================
    // 模式 4: 纯导航 (单意图)
    // ========================================================================
    case primaryIntent == IntentNavigation:
        return s.pureNavigationQuery(ctx, query, analysis, kbID)
    
    // ========================================================================
    // 模式 5: 纯跳转 (单意图)
    // ========================================================================
    case primaryIntent == IntentJump:
        return s.pureJumpQuery(ctx, query, analysis, kbID)
    
    // ========================================================================
    // 默认: 传统内容搜索 (BM25 + Vector + Graph)
    // ========================================================================
    default:
        return s.traditionalContentSearch(ctx, query, analysis, kbID)
    }
}
```

### 4.4 模式 1 实现: 导航 + 内容搜索

**场景**: "核心组件有哪些？分别是如何实现的？"

**策略**: 
1. TOC 导航找到章节
2. 在章节范围内深度检索实现细节
3. 融合结果,生成结构化答案

```go
func (s *QueryService) navigationPlusSearch(
    ctx context.Context,
    query string,
    analysis *QueryAnalysisResult,
    kbID string,
) (*RAGResponse, error) {
    // ========================================================================
    // Phase 1: TOC 导航 - 找到相关章节
    // ========================================================================
    tocMatches, err := s.tocService.DiscoverDocuments(
        ctx,
        query,
        analysis.Keywords,
        kbID,
    )
    if err != nil {
        return nil, err
    }
    
    // 收集章节对应的 chunk IDs
    chunkIDSet := make(map[string][]int) // document_id -> chunk_ids
    var sections []SectionMatch
    
    for _, doc := range tocMatches {
        for _, section := range doc.Sections {
            chunkIDSet[doc.DocumentID] = append(
                chunkIDSet[doc.DocumentID],
                section.ChunkIDs...,
            )
            sections = append(sections, section)
        }
    }
    
    // ========================================================================
    // Phase 2: 内容深度搜索 - 在召回的章节内搜索实现细节
    // ========================================================================
    // 提取第二个子问题 (通过 LLM 分解查询)
    subQueries := s.decomposeQuery(query) // ["核心组件有哪些", "如何实现"]
    searchQuery := subQueries[1]          // "如何实现"
    
    // 构建文档过滤条件
    docIDs := make([]string, 0, len(chunkIDSet))
    for docID := range chunkIDSet {
        docIDs = append(docIDs, docID)
    }
    
    // 并行执行 BM25 + Vector 检索 (限定在相关文档中)
    var wg sync.WaitGroup
    var bm25Results, vectorResults []*RecallResult
    
    wg.Add(2)
    
    go func() {
        defer wg.Done()
        bm25Results, _ = s.bm25Service.Search(ctx, searchQuery, HybridSearchOptions{
            DocumentIDs: docIDs,  // ✅ 只在相关文档中搜索
            TopK:        20,
        })
    }()
    
    go func() {
        defer wg.Done()
        vectorResults, _ = s.vectorService.Search(ctx, searchQuery, HybridSearchOptions{
            DocumentIDs: docIDs,
            TopK:        20,
        })
    }()
    
    wg.Wait()
    
    // ========================================================================
    // Phase 3: 结果融合
    // ========================================================================
    // 策略: TOC chunks (结构化) + 高分 search results (细节)
    tocResults := s.tocService.ToRecallResults(tocMatches)
    
    // RRF 融合
    allResults := append(tocResults, bm25Results...)
    allResults = append(allResults, vectorResults...)
    fusedResults := s.fuseResults(allResults)
    
    // LLM Reranking
    rerankedResults, err := s.rerankerService.Rerank(ctx, query, fusedResults, 10)
    if err != nil {
        return nil, err
    }
    
    // ========================================================================
    // Phase 4: 生成答案
    // ========================================================================
    // 构建上下文
    ragContext, err := s.buildContext(ctx, rerankedResults)
    if err != nil {
        return nil, err
    }
    
    // 生成结构化答案 (强调既要列出组件,又要说明实现)
    prompt := fmt.Sprintf(`基于以下内容回答用户问题:

问题: %s

文档章节:
%s

详细内容:
%s

要求:
1. 先列出核心组件列表 (来自文档结构)
2. 然后详细说明每个组件的实现方式 (来自详细内容)
3. 保持结构化,使用标题和列表
4. 如果实现细节不足,明确指出`,
        query,
        formatSections(sections),
        ragContext.ToPrompt(),
    )
    
    answer := s.llm.Call(prompt, LLMOptions{
        Model:       "gpt-4o",
        Temperature: 0.7,
    })
    
    return &RAGResponse{
        Answer:      answer,
        Sources:     rerankedResults,
        Sections:    sections,
        QueryType:   "navigation+search",
        DurationMs:  time.Since(startTime).Milliseconds(),
    }, nil
}

// 查询分解 (使用 LLM)
func (s *QueryService) decomposeQuery(query string) []string {
    prompt := fmt.Sprintf(`将以下查询分解为多个子问题:

查询: "%s"

返回 JSON 数组: ["子问题1", "子问题2"]`, query)
    
    response := s.llm.Call(prompt, LLMOptions{Model: "gpt-4o-mini"})
    
    var subQueries []string
    json.Unmarshal([]byte(response), &subQueries)
    return subQueries
}
```

### 4.5 模式 2 实现: 跳转 + 摘要

**场景**: "跳转到缓存层设计,并总结核心要点"

```go
func (s *QueryService) jumpPlusSummarize(
    ctx context.Context,
    query string,
    analysis *QueryAnalysisResult,
    kbID string,
) (*RAGResponse, error) {
    // Phase 1: TOC 精确跳转
    tocMatches, _ := s.tocService.DiscoverDocuments(ctx, query, analysis.Keywords, kbID)
    if len(tocMatches) == 0 || len(tocMatches[0].Sections) == 0 {
        return nil, errors.New("未找到匹配的章节")
    }
    
    // 取第一个匹配的章节
    targetSection := tocMatches[0].Sections[0]
    
    // Phase 2: 获取该章节的所有内容
    var chunks []models.Chunk
    s.db.Where("document_id = ? AND chunk_index IN ?", 
        tocMatches[0].DocumentID, 
        targetSection.ChunkIDs,
    ).Find(&chunks)
    
    // Phase 3: LLM 摘要
    content := ""
    for _, chunk := range chunks {
        content += chunk.Content + "\n\n"
    }
    
    summaryPrompt := fmt.Sprintf(`总结以下内容的核心要点:

章节: %s

内容:
%s

要求:
- 提取 3-5 个核心要点
- 每个要点用一句话概括
- 保持结构化`, targetSection.Title, content)
    
    summary := s.llm.Call(summaryPrompt, LLMOptions{Model: "gpt-4o"})
    
    return &RAGResponse{
        Answer:     fmt.Sprintf("**%s**\n\n%s", targetSection.Title, summary),
        Sources:    convertChunksToResults(chunks),
        QueryType:  "jump+summary",
    }, nil
}
```

### 4.6 性能优化: 并行执行意图

```go
// 并行执行多个意图,提升性能
func (s *QueryService) parallelIntentExecution(
    ctx context.Context,
    query string,
    analysis *QueryAnalysisResult,
    kbID string,
) (*RAGResponse, error) {
    resultChan := make(chan intentResult, 2)
    
    // Goroutine 1: TOC 导航
    go func() {
        tocResults, err := s.tocService.DiscoverDocuments(ctx, query, analysis.Keywords, kbID)
        resultChan <- intentResult{"toc", tocResults, err}
    }()
    
    // Goroutine 2: 内容搜索
    go func() {
        searchResults, err := s.hybridSearch(ctx, query, analysis, kbID)
        resultChan <- intentResult{"search", searchResults, err}
    }()
    
    // 收集结果
    var tocResults []DocumentMatch
    var searchResults []*RecallResult
    
    for i := 0; i < 2; i++ {
        res := <-resultChan
        if res.err != nil {
            log.Printf("Error in %s: %v", res.name, res.err)
            continue
        }
        
        switch res.name {
        case "toc":
            tocResults = res.data.([]DocumentMatch)
        case "search":
            searchResults = res.data.([]*RecallResult)
        }
    }
    
    // 融合结果
    return s.mergeIntentResults(tocResults, searchResults, query)
}

type intentResult struct {
    name string
    data interface{}
    err  error
}
```

### 4.7 混合意图的优势

| 对比项 | 单意图处理 | 混合意图处理 | 提升 |
|--------|-----------|-------------|------|
| **答案完整性** | ⚠️ 只回答一个方面 | ✅ 完整回答多个诉求 | **+100%** |
| **交互轮次** | ⚠️ 需要多轮对话 | ✅ 一次性完成 | **-50%** |
| **用户体验** | ⚠️ 需要追问 | ✅ 直接满足需求 | **+80%** |
| **召回质量** | ⚠️ 可能遗漏内容 | ✅ TOC + 深度检索 | **+40%** |
| **性能** | ✅ 快 | ✅ 并行执行,总延迟 ≈ max(intent1, intent2) | **相当** |

### 4.8 降级策略

```go
// 混合意图失败时,降级到单意图处理
func (s *QueryService) processWithFallback(
    ctx context.Context,
    query string,
    analysis *QueryAnalysisResult,
    kbID string,
) (*RAGResponse, error) {
    // 尝试混合意图处理
    response, err := s.ProcessHybridQuery(ctx, query, analysis, kbID)
    
    if err != nil || response == nil {
        // 降级: 只处理主要意图
        log.Printf("Hybrid intent failed, fallback to primary intent: %s", analysis.PrimaryIntent)
        
        switch analysis.PrimaryIntent {
        case IntentNavigation:
            return s.pureNavigationQuery(ctx, query, analysis, kbID)
        case IntentJump:
            return s.pureJumpQuery(ctx, query, analysis, kbID)
        default:
            return s.traditionalContentSearch(ctx, query, analysis, kbID)
        }
    }
    
    return response, nil
}
```

---

## 5. 完整流程示例（混合意图）

### 输入
```json
{
  "query": "核心组件有哪些？分别是如何实现的？",
  "knowledge_base_id": "kb_001",
  "top_k": 5
}
```

### 处理步骤

#### Step 1: Query Analysis (混合意图识别)
```json
{
  "primary_intent": "navigation",
  "secondary_intents": ["content_search"],
  "confidence": 0.92,
  "entities": [
    {"name": "核心组件", "type": "concept"}
  ],
  "query_types": ["section", "code"],
  "keywords": ["核心组件", "实现"],
  "language": "zh-CN",
  "reasoning": "用户首先想知道有哪些组件(导航),然后想了解实现细节(内容搜索)"
}
```

#### Step 2: Query Decomposition (查询分解)
```json
{
  "sub_queries": [
    "核心组件有哪些？",
    "核心组件如何实现的？"
  ]
}
```

#### Step 3a: TOC Navigation (第一子查询)
```json
{
  "toc_matches": [
    {
      "document_id": "doc_12345",
      "title": "系统架构设计.md",
      "sections": [
        {
          "title": "1.1 核心组件详细说明",
          "path": "1. 系统架构 > 1.1 核心组件详细说明",
          "level": 2,
          "chunk_ids": [2, 3, 4],
          "children_chunk_ids": [5, 6, 7, 8, 9, 10, 11, 12]
        }
      ]
    }
  ]
}
```

#### Step 3b: Content Search (第二子查询, 限定在相关文档)
**BM25 Results (限定 doc_12345)**
```json
[
  {"chunk_id": "chk_005", "score": 11.2, "rank": 1, "matched": "存储层实现"},
  {"chunk_id": "chk_009", "score": 10.5, "rank": 2, "matched": "缓存实现"},
  {"chunk_id": "chk_011", "score": 9.8, "rank": 3, "matched": "API网关实现"}
]
```

**Vector Results (限定 doc_12345)**
```json
[
  {"chunk_id": "chk_009", "score": 0.91, "rank": 1},
  {"chunk_id": "chk_005", "score": 0.88, "rank": 2},
  {"chunk_id": "chk_011", "score": 0.85, "rank": 3}
]
```

#### Step 4: RRF Fusion (k=60, 包含 TOC 结果)
```json
[
  {"chunk_id": "chk_005", "rrf_score": 0.065, "fused_rank": 1},  // TOC + BM25 + Vector
  {"chunk_id": "chk_009", "rrf_score": 0.063, "fused_rank": 2},  // TOC + BM25 + Vector
  {"chunk_id": "chk_011", "rrf_score": 0.058, "fused_rank": 3},  // TOC + BM25 + Vector
  {"chunk_id": "chk_003", "rrf_score": 0.032, "fused_rank": 4},  // TOC only
  {"chunk_id": "chk_007", "rrf_score": 0.028, "fused_rank": 5}   // TOC + BM25
]
```

#### Step 5: LLM Reranking
```json
[
  {"chunk_id": "chk_005", "reranked_score": 96, "final_rank": 1, "reason": "存储层完整实现说明"},
  {"chunk_id": "chk_009", "reranked_score": 94, "final_rank": 2, "reason": "缓存层详细设计"},
  {"chunk_id": "chk_011", "reranked_score": 92, "final_rank": 3, "reason": "API网关实现"},
  {"chunk_id": "chk_003", "reranked_score": 75, "final_rank": 4, "reason": "核心组件概述"},
  {"chunk_id": "chk_007", "reranked_score": 70, "final_rank": 5, "reason": "组件关系图"}
]
```

#### Step 6: Context Construction
```go
// 获取 Top-5 chunks 的完整内容
// + 文档结构信息 (TOC 章节)
// + 相邻 Chunk (上下文)
// + Enrichment (语义摘要)
```

#### Step 7: Answer Generation (结构化答案)
```
Prompt:
基于以下内容回答用户问题:

问题: 核心组件有哪些？分别是如何实现的？

文档结构:
- 1.1 核心组件详细说明
  - 1.1.1 存储层架构 (chunks: 5-6)
  - 1.1.2 缓存层设计 (chunks: 9-10)
  - 1.1.3 API 网关 (chunks: 11-12)

详细内容:
[chunk 内容...]

要求:
1. 先列出核心组件列表
2. 然后说明每个组件的实现方式
3. 保持结构化
```

### 输出
```json
{
  "answer": "## 核心组件\n\n根据系统架构设计,核心组件包括以下三个部分:\n\n### 1. 存储层架构\n\n**实现方式**:\n- 使用 PostgreSQL 作为主数据库\n- 采用主从复制架构保证高可用\n- 配置读写分离: 主库负责写入,从库负责读取\n- 使用 Patroni + etcd 实现自动故障转移\n- 数据备份策略: 每日全量备份 + 实时 WAL 归档\n\n### 2. 缓存层设计\n\n**实现方式**:\n- 使用 Redis Cluster (3主3从配置)\n- 三级缓存策略:\n  * L1: 本地内存缓存 (LRU 算法, 1GB)\n  * L2: Redis 分布式缓存 (TTL 1小时)\n  * L3: 数据库\n- 缓存更新: 使用 Canal 监听 MySQL binlog,实时失效缓存\n- 缓存穿透防护: 布隆过滤器 + 空值缓存\n\n### 3. API 网关\n\n**实现方式**:\n- 基于 Kong + OpenResty 实现\n- 路由: 支持 RESTful API 和 GraphQL\n- 认证: JWT + OAuth2 双重认证\n- 限流: 令牌桶算法 (100 req/s per user)\n- 监控: 集成 Prometheus + Grafana\n\n---\n\n以上三个核心组件共同构成了系统的基础架构层。",
  
  "sources": [
    {
      "chunk_id": "chk_005",
      "document_title": "系统架构设计.md",
      "section_title": "1.1.1 存储层架构",
      "content": "...",
      "score": 96,
      "channel": "toc_navigation+bm25+vector"
    },
    {
      "chunk_id": "chk_009",
      "document_title": "系统架构设计.md",
      "section_title": "1.1.2 缓存层设计",
      "content": "...",
      "score": 94,
      "channel": "toc_navigation+bm25+vector"
    },
    {
      "chunk_id": "chk_011",
      "document_title": "系统架构设计.md",
      "section_title": "1.1.3 API 网关",
      "content": "...",
      "score": 92,
      "channel": "toc_navigation+bm25+vector"
    }
  ],
  
  "toc_structure": {
    "matched_section": "1.1 核心组件详细说明",
    "children": [
      {"title": "1.1.1 存储层架构", "chunk_ids": [5, 6]},
      {"title": "1.1.2 缓存层设计", "chunk_ids": [9, 10]},
      {"title": "1.1.3 API 网关", "chunk_ids": [11, 12]}
    ]
  },
  
  "query_type": "navigation+content_search",
  "duration_ms": 850,
  "cost": 0.0028
}
```

**关键改进**:
- ✅ 答案既有**列表** (来自 TOC),又有**实现细节** (来自深度检索)
- ✅ 结构化清晰,逻辑完整
- ✅ 一次查询完整回答,无需追问
- ✅ TOC 保证召回完整性,深度检索保证细节丰富度

---

## 5. 性能优化

### 5.1 缓存策略
- **Query Rewriting Cache**: 缓存常见查询的改写结果 (命中率 ~40%)
- **Embedding Cache**: 缓存查询 Embedding (命中率 ~30%)
- **Graph Traversal Cache**: 缓存常见实体的 N-hop 邻居

### 5.2 并行执行
```go
func (s *RAGService) Search(ctx context.Context, query string) (*SearchResult, error) {
    // 1. 并行执行 3 个通道的检索
    var wg sync.WaitGroup
    var bm25Results, vectorResults, graphResults []*RecallResult
    
    wg.Add(3)
    
    go func() {
        defer wg.Done()
        bm25Results, _ = s.bm25Service.Search(ctx, query, ...)
    }()
    
    go func() {
        defer wg.Done()
        vectorResults, _ = s.vectorService.Search(ctx, query, ...)
    }()
    
    go func() {
        defer wg.Done()
        graphResults, _ = s.graphService.Search(ctx, query, ...)
    }()
    
    wg.Wait()
    
    // 2. RRF 融合
    fused := FuseResults(bm25Results, vectorResults, graphResults, 60)
    
    // 3. Reranking
    reranked := s.rerankerService.Rerank(ctx, query, fused, 10)
    
    return reranked, nil
}
```

### 5.3 成本控制
- **LLM Reranking**: 只对 Top-30 候选进行重排序
- **Query Analysis**: 使用 GPT-4o-mini (更便宜)
- **Answer Generation**: 使用 GPT-4o (质量更高)

---

## 6. 监控指标

### 6.1 查询性能
- `query_duration_p50/p95/p99`: 查询延迟分位数
- `recall_count_by_channel`: 各通道召回数量
- `rrf_fusion_time`: RRF 融合耗时
- `llm_reranking_time`: LLM 重排序耗时

### 6.2 召回质量
- `precision@k`: 前 K 个结果的精确率
- `recall@k`: 前 K 个结果的召回率
- `mrr`: Mean Reciprocal Rank (平均倒数排名)
- `ndcg@k`: Normalized Discounted Cumulative Gain

### 6.3 成本
- `llm_cost_per_query`: 每次查询的 LLM 成本
- `cache_hit_rate`: 缓存命中率
- `embedding_cost`: Embedding 成本

---

## 7. 未来优化

### 7.1 Query Understanding
- 多轮对话上下文记忆
- 用户意图学习 (基于历史查询)
- 个性化查询改写 (基于用户偏好)

### 7.2 Recall Optimization
- 学习排序 (Learning to Rank)
- 动态通道权重调整
- 负样本挖掘 (Hard Negative Mining)

### 7.3 Reranking
- 使用专用 Reranker 模型 (Cohere Rerank, BGE-Reranker)
- 训练自定义 Reranker (基于用户反馈)

---

**总结**: 完整的 Query 处理流水线包括:
1. **Query Analysis**: LLM 识别混合意图 (主要+次要)
2. **Query Rewriting**: 查询改写和分解
3. **Multi-Channel Recall**: BM25 + Vector + TOC Navigation + Graph
4. **RRF Fusion**: 倒数排名融合
5. **LLM Reranking**: 智能重排序
6. **Hybrid Intent Processing**: 并行执行多个意图策略
7. **Context Construction**: 构建完整上下文
8. **Answer Generation**: 生成结构化答案

**核心创新**:
- ✅ **TOC 导航检索**: 通过文档目录结构快速定位章节,100% 精确匹配
- ✅ **混合意图处理**: 识别和并行处理多个意图,一次查询完整回答
- ✅ **结果融合优化**: TOC 保证完整性,深度检索保证细节
- ✅ **全LLM意图识别**: 去除规则匹配,完全依赖 LLM 准确识别意图

**性能指标**:
- TOC 导航延迟: < 10ms
- 混合意图并行执行: 总延迟 ≈ max(intent1, intent2)
- 端到端延迟: P95 < 1000ms
- 答案质量: 结构化 + 完整性 + 细节丰富度
