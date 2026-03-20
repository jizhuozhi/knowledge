# 内容增强与多层索引存储架构设计

## 1. 设计目标

### 1.1 核心问题
- **语义鸿沟**: 原始代码/表格内容与用户自然语言查询之间存在巨大的语义差距
- **检索效率**: 不同检索模式(精确匹配 vs 语义相似度)需要不同的内容表示
- **存储优化**: 避免重复存储,支持灵活的内容版本管理

### 1.2 设计原则
1. **内容分离**: 原始内容、检索内容、语义增强内容分开存储
2. **溯源能力**: 所有增强内容都能追溯到原始来源
3. **可扩展性**: 支持自定义增强 Processor,支持多种 Embedding 策略
4. **成本可控**: LLM 增强结果可缓存,避免重复调用

---

## 2. 数据模型设计

### 2.1 核心存储层

#### 2.1.1 Chunk (原始内容层)
```go
// Chunk 存储原始分块内容,作为真实数据源
type Chunk struct {
    TenantModel
    DocumentID    string  `gorm:"size:12;not null;index" json:"document_id"`
    
    // 原始内容 (用于展示和回源)
    Content       string  `gorm:"type:text;not null" json:"content"`
    
    ChunkIndex    int     `gorm:"not null" json:"chunk_index"`
    StartPosition int     `json:"start_position"`
    EndPosition   int     `json:"end_position"`
    
    // 内容类型标识
    ChunkType     string  `gorm:"size:20" json:"chunk_type"` // section, paragraph, table, code, list
    
    // 结构化元数据 (原始结构信息)
    StructMeta    JSONB   `gorm:"type:jsonb" json:"struct_meta"`
    // 例如:
    // - 表格: {"headers": [...], "row_count": 10, "col_count": 3}
    // - 代码: {"language": "go", "functions": [...], "imports": [...]}
    // - 章节: {"heading_level": 2, "heading_text": "..."}
    
    // 处理状态
    EnrichmentStatus string `gorm:"size:20;default:'pending'" json:"enrichment_status"` // pending, processing, completed, failed
    EnrichmentError  string `gorm:"type:text" json:"enrichment_error"`
}
```

#### 2.1.2 ChunkEnrichment (增强内容层)
```go
// ChunkEnrichment 存储对原始 Chunk 的各种增强结果
type ChunkEnrichment struct {
    TenantModel
    ChunkID       string  `gorm:"size:12;not null;index" json:"chunk_id"`
    
    // 增强类型标识
    EnrichmentType string `gorm:"size:50;not null;index" json:"enrichment_type"` 
    // 预定义类型:
    // - "keyword_extraction"    提取的关键词列表
    // - "semantic_summary"      LLM 生成的语义摘要
    // - "code_explanation"      代码块的自然语言解释
    // - "table_description"     表格的自然语言描述
    // - "entity_extraction"     提取的实体列表
    // - "question_generation"   生成的问题列表(用于问答对训练)
    // - 自定义: "custom_xxx"
    
    // 增强内容 (文本形式)
    EnrichedContent string `gorm:"type:text" json:"enriched_content"`
    
    // 结构化增强数据 (JSON格式)
    EnrichedData   JSONB  `gorm:"type:jsonb" json:"enriched_data"`
    // 例如:
    // - keyword_extraction: {"keywords": ["API", "认证", "JWT"], "weights": [0.9, 0.8, 0.7]}
    // - code_explanation: {"summary": "...", "params": [...], "returns": "..."}
    // - question_generation: {"questions": ["如何实现JWT认证?", "认证流程是什么?"]}
    
    // 增强来源
    EnricherName   string `gorm:"size:100" json:"enricher_name"` // "llm:gpt-4", "rule:keyword", "model:bert-ner"
    EnricherVersion string `gorm:"size:50" json:"enricher_version"`
    
    // 质量指标
    Confidence     float64 `gorm:"default:0" json:"confidence"` // 增强结果的置信度 [0-1]
    
    // LLM 使用记录 (如果使用了 LLM)
    LLMUsageID     *string `gorm:"size:12;index" json:"llm_usage_id,omitempty"`
}
```

#### 2.1.3 ChunkIndex (索引配置层)
```go
// ChunkIndex 定义如何为 Chunk 创建索引 (支持一个 Chunk 多种索引策略)
type ChunkIndex struct {
    TenantModel
    ChunkID       string  `gorm:"size:12;not null;index" json:"chunk_id"`
    
    // 索引类型
    IndexType     string  `gorm:"size:30;not null" json:"index_type"` // "bm25", "vector", "hybrid"
    
    // 索引内容来源 (指向具体的内容或增强结果)
    ContentSource string  `gorm:"size:50;not null" json:"content_source"`
    // 取值:
    // - "original"                           使用 Chunk.Content
    // - "enrichment:semantic_summary"        使用 ChunkEnrichment(type=semantic_summary).EnrichedContent
    // - "enrichment:keyword_extraction"      使用关键词列表
    // - "composite:original+keywords"        组合多个来源
    
    // 外部索引 ID (OpenSearch/Milvus 的文档 ID)
    ExternalIndexID string `gorm:"size:200" json:"external_index_id"`
    
    // 索引配置
    IndexConfig   JSONB  `gorm:"type:jsonb" json:"index_config"`
    // 例如:
    // - vector: {"embedding_model": "text-embedding-3-small", "dimension": 1536}
    // - bm25: {"analyzer": "standard", "boost": 1.0}
    
    // 索引状态
    Status        string `gorm:"size:20;default:'pending'" json:"status"` // pending, indexed, failed
    IndexedAt     *time.Time `json:"indexed_at"`
}
```

---

### 2.2 扩展存储层

#### 2.2.1 EnrichmentPipeline (增强管道配置)
```go
// EnrichmentPipeline 定义文档类型对应的增强处理流程
type EnrichmentPipeline struct {
    TenantModel
    Name          string `gorm:"size:255;not null;unique" json:"name"` // "api_doc_pipeline", "code_snippet_pipeline"
    Description   string `gorm:"type:text" json:"description"`
    
    // 适用条件
    ApplicableDocTypes []string `gorm:"type:text[]" json:"applicable_doc_types"` // ["knowledge", "experience"]
    ApplicableChunkTypes []string `gorm:"type:text[]" json:"applicable_chunk_types"` // ["code", "table"]
    
    // 增强处理器链 (按顺序执行)
    Processors    JSONB  `gorm:"type:jsonb;not null" json:"processors"`
    // 例如:
    // [
    //   {"name": "keyword_extractor", "type": "rule", "config": {...}},
    //   {"name": "code_summarizer", "type": "llm", "config": {"model": "gpt-4o-mini"}},
    //   {"name": "entity_extractor", "type": "ml", "config": {"model": "bert-ner"}}
    // ]
    
    // 索引策略
    IndexStrategies JSONB `gorm:"type:jsonb;not null" json:"index_strategies"`
    // 例如:
    // [
    //   {"type": "bm25", "source": "original", "boost": 1.0},
    //   {"type": "vector", "source": "enrichment:semantic_summary", "embedding_model": "..."}
    // ]
    
    Status        string `gorm:"size:20;default:'active'" json:"status"` // active, disabled
}
```

#### 2.2.2 EnrichmentCache (增强结果缓存)
```go
// EnrichmentCache 用于缓存 LLM 增强结果,避免重复调用
type EnrichmentCache struct {
    TenantModel
    
    // 缓存键 (基于内容和处理器生成)
    CacheKey      string `gorm:"size:64;not null;unique;index" json:"cache_key"` // SHA256(content + enricher + version)
    
    // 输入内容摘要 (用于调试)
    ContentHash   string `gorm:"size:64;not null" json:"content_hash"`
    ContentLength int    `json:"content_length"`
    ContentPreview string `gorm:"size:500" json:"content_preview"` // 前500字符
    
    // 增强结果
    EnrichmentType string `gorm:"size:50;not null" json:"enrichment_type"`
    EnrichedContent string `gorm:"type:text" json:"enriched_content"`
    EnrichedData   JSONB  `gorm:"type:jsonb" json:"enriched_data"`
    
    // 处理器信息
    EnricherName   string `gorm:"size:100;not null" json:"enricher_name"`
    EnricherVersion string `gorm:"size:50;not null" json:"enricher_version"`
    
    // 使用统计
    HitCount      int       `gorm:"default:0" json:"hit_count"`
    LastHitAt     time.Time `json:"last_hit_at"`
    
    // LLM 成本记录
    LLMUsageID    *string `gorm:"size:12;index" json:"llm_usage_id,omitempty"`
    
    // 缓存过期时间
    ExpiresAt     *time.Time `gorm:"index" json:"expires_at"`
}
```

---

## 3. 数据流设计

### 3.1 索引阶段数据流

```
Document Content
    ↓
[1] Chunk Service: 结构化分块
    ↓
Chunk (原始内容) 
    ↓
[2] Enrichment Pipeline: 内容增强
    ↓
    ├─→ [2.1] Keyword Extractor → ChunkEnrichment (type: keyword_extraction)
    ├─→ [2.2] LLM Summarizer → ChunkEnrichment (type: semantic_summary)
    ├─→ [2.3] Code Explainer → ChunkEnrichment (type: code_explanation)
    ├─→ [2.4] Entity Extractor → ChunkEnrichment (type: entity_extraction)
    └─→ [2.5] Relation Extractor → ChunkEnrichment (type: relation_extraction)
    ↓
[3] Index Service: 创建多层索引
    ↓
    ├─→ [3.1] BM25 Index (source: original + keywords) 
    │       → ChunkIndex (type: bm25) + OpenSearch
    │
    ├─→ [3.2] Vector Index (source: semantic_summary) 
    │       → ChunkIndex (type: vector) + Milvus
    │
    └─→ [3.3] Graph Index (source: entity_extraction + relation_extraction)
            → GraphEntity + GraphRelation + Neo4j
            (关联到 Chunk 通过 chunk_id)
```

### 3.2 检索阶段数据流

```
User Query: "如何实现JWT认证?"
    ↓
[1] Query Analysis (查询分析)
    ├─→ 意图识别: "代码示例查询"
    ├─→ 实体提取: ["JWT", "认证"]
    └─→ 查询类型: code + concept
    ↓
[2] Query Rewriting (查询改写)
    ├─→ 同义词扩展: "JWT认证" → ["JWT authentication", "token验证", "身份认证"]
    ├─→ 结构化改写: 生成多个子查询
    │   ├─ "JWT 中间件实现代码"
    │   ├─ "认证流程说明"
    │   └─ "token 验证逻辑"
    └─→ 关键词提取: ["JWT", "认证", "中间件", "token"]
    ↓
[3] Multi-Channel Recall (多通道召回)
    ├─→ [3.1] BM25 Search (关键词精确匹配)
    │   ├─ 在 OpenSearch 中搜索 (原始内容 + 关键词增强)
    │   └─ 返回 Top-20 ChunkIndex.ID (source: bm25)
    │
    ├─→ [3.2] Vector Search (语义相似度)
    │   ├─ Embedding Query (使用改写后的查询)
    │   ├─ 在 Milvus 中搜索 (语义摘要向量)
    │   └─ 返回 Top-20 ChunkIndex.ID (source: vector)
    │
    └─→ [3.3] Graph Search (知识图谱)
        ├─ 在 Neo4j 中查找实体 "JWT"
        ├─ 遍历关系: implements, references, depends_on
        ├─ 返回关联的 Document/Chunk
        └─ 返回 Top-10 ChunkIndex.ID (source: graph)
    ↓
[4] RRF Fusion (倒数排名融合)
    ├─ 合并三个通道的结果
    ├─ RRF Score = 1/(k + rank_bm25) + 1/(k + rank_vector) + 1/(k + rank_graph)
    └─ 返回 Top-30 候选
    ↓
[5] LLM Reranking (重排序)
    ├─ 通过 ChunkIndex.ChunkID 获取 Chunk.Content
    ├─ 使用 LLM 对候选结果打分 (query-chunk relevance)
    └─ 返回 Top-10 最相关结果
    ↓
[6] Context Construction (上下文构建)
    ├─ 获取前后相邻 Chunk (上下文窗口)
    ├─ 加载关联的 ChunkEnrichment (摘要/解释)
    ├─ 加载图谱上下文 (相关实体和关系)
    └─ 构建完整上下文传给 LLM
    ↓
[7] Answer Generation (答案生成)
    ↓
[8] 用户看到: 答案 + 原始引用 + 高亮片段 + 图谱可视化
```

---

## 4. 典型场景示例

### 4.1 场景: 代码块索引

#### 原始 Chunk
```go
// chunk_id: "chk_abc123"
// chunk_type: "code"
// content: (原始代码)
func AuthMiddleware(secret string) gin.HandlerFunc {
    return func(c *gin.Context) {
        token := c.GetHeader("Authorization")
        if token == "" {
            c.AbortWithStatus(401)
            return
        }
        claims, err := jwt.Parse(token, secret)
        if err != nil {
            c.AbortWithStatus(401)
            return
        }
        c.Set("user_id", claims.UserID)
        c.Next()
    }
}

// struct_meta: 
{
    "language": "go",
    "type": "function",
    "name": "AuthMiddleware",
    "params": [{"name": "secret", "type": "string"}],
    "returns": "gin.HandlerFunc",
    "imports": ["github.com/gin-gonic/gin", "jwt-go"]
}
```

#### 增强结果 1: 关键词提取
```go
// enrichment_type: "keyword_extraction"
// enricher_name: "rule:go_ast_parser"
// enriched_data:
{
    "keywords": ["AuthMiddleware", "JWT", "token", "Authorization", "gin.HandlerFunc"],
    "weights": [1.0, 0.9, 0.8, 0.8, 0.7],
    "identifiers": ["AuthMiddleware", "token", "claims", "secret"],
    "imported_packages": ["gin", "jwt"]
}
```

#### 增强结果 2: 代码语义摘要 (LLM)
```go
// enrichment_type: "code_explanation"
// enricher_name: "llm:gpt-4o-mini"
// enriched_content:
"这是一个 Gin 框架的 JWT 认证中间件。
它从请求头中提取 Authorization token,验证 JWT 签名,
并将解析出的用户 ID 存入 Gin 上下文中供后续处理器使用。
认证失败时返回 401 状态码。"

// enriched_data:
{
    "summary": "JWT认证中间件",
    "purpose": "验证HTTP请求中的JWT token",
    "input": "JWT secret密钥字符串",
    "output": "Gin中间件处理函数",
    "error_handling": "认证失败返回401状态码",
    "tech_stack": ["Gin", "JWT"]
}
```

#### 增强结果 3: 实体和关系提取 (Graph)
```go
// enrichment_type: "entity_extraction"
// enricher_name: "llm:gpt-4o-mini"
// enriched_data:
{
    "entities": [
        {"name": "JWT", "type": "concept", "properties": {"category": "认证技术"}},
        {"name": "AuthMiddleware", "type": "function", "properties": {"language": "Go"}},
        {"name": "Gin", "type": "framework", "properties": {"domain": "Web"}},
        {"name": "Authorization Header", "type": "component", "properties": {"protocol": "HTTP"}}
    ]
}

// enrichment_type: "relation_extraction"
// enricher_name: "llm:gpt-4o-mini"
// enriched_data:
{
    "relations": [
        {"source": "AuthMiddleware", "target": "JWT", "type": "implements", "weight": 0.9},
        {"source": "AuthMiddleware", "target": "Gin", "type": "depends_on", "weight": 0.8},
        {"source": "JWT", "target": "Authorization Header", "type": "uses", "weight": 0.7}
    ]
}

// 这些会同步写入 GraphEntity 和 GraphRelation 表
```

#### 索引策略 (三层索引)
```go
// 索引 1: BM25 全文索引
ChunkIndex{
    index_type: "bm25",
    content_source: "composite:original+keywords",
    external_index_id: "opensearch_doc_001",
    index_config: {
        "analyzer": "standard",
        "fields": {
            "code": {"boost": 1.0, "source": "original"},
            "keywords": {"boost": 1.5, "source": "enrichment:keyword_extraction"}
        }
    }
}

// 索引 2: 向量语义索引
ChunkIndex{
    index_type: "vector",
    content_source: "enrichment:code_explanation",
    external_index_id: "milvus_vec_001",
    index_config: {
        "embedding_model": "text-embedding-3-small",
        "dimension": 1536,
        "metric": "cosine"
    }
}

// 索引 3: 图谱索引 (存储在 GraphEntity/GraphRelation)
// 注意: 图谱索引不创建 ChunkIndex 记录,直接通过 GraphEntity.DocumentID/ChunkID 关联
// Neo4j中创建节点和关系:
// - (:Function {name: "AuthMiddleware", chunk_id: "chk_abc123"})
// - (:Concept {name: "JWT"})
// - (AuthMiddleware)-[:IMPLEMENTS]->(JWT)
```

### 4.2 场景: 表格索引

#### 原始 Chunk
```markdown
| 服务名称 | 端口 | 协议 | 负责人 |
|---------|------|------|--------|
| user-api | 8080 | HTTP | 张三 |
| order-service | 8081 | gRPC | 李四 |
| payment-gateway | 8082 | HTTPS | 王五 |

// struct_meta:
{
    "headers": ["服务名称", "端口", "协议", "负责人"],
    "row_count": 3,
    "col_count": 4,
    "column_types": ["string", "number", "string", "string"]
}
```

#### 增强结果 1: 关键词提取
```go
// enrichment_type: "keyword_extraction"
// enriched_data:
{
    "keywords": ["user-api", "order-service", "payment-gateway", "8080", "8081", "8082", "HTTP", "gRPC", "HTTPS"],
    "entities": {
        "services": ["user-api", "order-service", "payment-gateway"],
        "persons": ["张三", "李四", "王五"],
        "ports": [8080, 8081, 8082]
    }
}
```

#### 增强结果 2: 表格语义描述 (LLM)
```go
// enrichment_type: "table_description"
// enricher_name: "llm:gpt-4o-mini"
// enriched_content:
"这是一份微服务架构的服务清单,包含3个核心服务。
user-api 负责用户相关接口,使用 HTTP 协议;
order-service 处理订单业务,使用 gRPC 高性能通信;
payment-gateway 是支付网关,使用 HTTPS 安全协议。
每个服务都有明确的负责人和独立的端口号。"

// enriched_data:
{
    "summary": "微服务架构服务清单",
    "domain": "后端服务",
    "key_info": {
        "service_count": 3,
        "protocols": ["HTTP", "gRPC", "HTTPS"],
        "port_range": "8080-8082"
    },
    "semantic_tags": ["微服务", "服务清单", "端口配置", "团队分工"]
}
```

#### 索引策略
```go
// 索引 1: BM25 (用于精确查找服务名/端口/负责人)
ChunkIndex{
    index_type: "bm25",
    content_source: "original",
    external_index_id: "opensearch_table_001"
}

// 索引 2: 向量索引 (用于语义查询 "支付相关的服务有哪些")
ChunkIndex{
    index_type: "vector",
    content_source: "enrichment:table_description",
    external_index_id: "milvus_table_001"
}
```

---

## 5. OpenSearch/Milvus 索引结构

### 5.1 OpenSearch 文档结构

```json
{
    "_index": "knowledge_chunks_bm25",
    "_id": "opensearch_doc_001",  // 对应 ChunkIndex.external_index_id
    "_source": {
        "chunk_id": "chk_abc123",
        "document_id": "doc_xyz789",
        "tenant_id": "tenant_001",
        
        // 内容字段 (根据 content_source 动态填充)
        "original_content": "func AuthMiddleware...",  // Chunk.Content
        "keywords": ["AuthMiddleware", "JWT", "token"],  // ChunkEnrichment(keyword_extraction)
        
        // 元数据
        "chunk_type": "code",
        "struct_meta": {
            "language": "go",
            "type": "function",
            "name": "AuthMiddleware"
        },
        
        // 文档上下文
        "doc_title": "API认证指南",
        "doc_type": "knowledge",
        
        // 索引时间
        "indexed_at": "2026-03-17T10:00:00Z"
    }
}
```

### 5.2 Milvus Collection 结构

```python
# Collection: knowledge_chunks_vector
{
    "id": "milvus_vec_001",  # 对应 ChunkIndex.external_index_id
    
    # 向量字段
    "embedding": [0.123, -0.456, ...],  # 1536维
    
    # Scalar 字段 (用于过滤)
    "chunk_id": "chk_abc123",
    "document_id": "doc_xyz789",
    "tenant_id": "tenant_001",
    "chunk_type": "code",
    "doc_type": "knowledge",
    
    # 元数据 (JSON)
    "metadata": {
        "enrichment_type": "code_explanation",  # 标记这是用什么内容生成的 embedding
        "embedding_model": "text-embedding-3-small",
        "content_preview": "这是一个 Gin 框架的 JWT 认证中间件..."  # 前200字符
    },
    
    "indexed_at": 1710662400
}
```

---

## 6. API 接口设计

### 6.1 增强管道管理

```go
// 创建增强管道
POST /api/v1/enrichment-pipelines
{
    "name": "code_pipeline",
    "applicable_doc_types": ["knowledge", "experience"],
    "applicable_chunk_types": ["code"],
    "processors": [
        {
            "name": "keyword_extractor",
            "type": "rule",
            "enabled": true
        },
        {
            "name": "code_explainer",
            "type": "llm",
            "enabled": true,
            "config": {
                "model": "gpt-4o-mini",
                "temperature": 0.3,
                "max_tokens": 500
            }
        }
    ],
    "index_strategies": [
        {"type": "bm25", "source": "composite:original+keywords"},
        {"type": "vector", "source": "enrichment:code_explanation"}
    ]
}

// 触发增强处理 (手动)
POST /api/v1/chunks/{chunk_id}/enrich
{
    "pipeline_name": "code_pipeline",
    "force": false  // 是否忽略缓存
}

// 查询增强结果
GET /api/v1/chunks/{chunk_id}/enrichments
Response:
[
    {
        "enrichment_type": "keyword_extraction",
        "enriched_content": "",
        "enriched_data": {...},
        "confidence": 1.0,
        "created_at": "..."
    },
    {
        "enrichment_type": "code_explanation",
        "enriched_content": "这是一个JWT认证中间件...",
        "confidence": 0.95,
        "llm_usage": {
            "model": "gpt-4o-mini",
            "tokens": 850,
            "cost": 0.0012
        }
    }
]
```

### 6.2 检索增强 (三层索引)

```go
// 混合检索 (支持三层索引)
POST /api/v1/search
{
    "query": "如何实现JWT认证",
    "knowledge_base_id": "kb_001",
    "index_types": ["bm25", "vector", "graph"],  // 可选: 默认全部
    "filters": {
        "chunk_types": ["code", "section"]
    },
    "include_graph": true,  // 是否包含图谱上下文
    "limit": 10
}

Response:
{
    "results": [
        {
            "chunk_id": "chk_abc123",
            "score": 0.92,
            "source": "vector",  // 哪个索引返回的 (bm25, vector, graph)
            "content": "func AuthMiddleware...",  // 始终返回 Chunk.Content (原始内容)
            "enrichments": {  // 可选: 返回增强信息用于调试
                "semantic_summary": "这是一个JWT认证中间件...",
                "keywords": ["JWT", "认证", "中间件"]
            },
            "highlights": ["JWT", "Authorization"],
            "metadata": {
                "doc_title": "API认证指南",
                "chunk_type": "code"
            },
            "graph_context": {  // 图谱上下文 (如果 include_graph=true)
                "entities": [
                    {"name": "JWT", "type": "concept"},
                    {"name": "AuthMiddleware", "type": "function"}
                ],
                "relations": [
                    {"source": "AuthMiddleware", "target": "JWT", "type": "implements"}
                ]
            }
        }
    ],
    "debug": {
        "bm25_hits": 5,
        "vector_hits": 8,
        "graph_hits": 3,
        "fusion_method": "rrf",
        "query_analysis": {
            "intent": "code_search",
            "entities": ["JWT", "认证"],
            "rewritten_queries": ["JWT middleware", "token validation"]
        }
    }
}
```

---

## 7. 实施优先级

### Phase 1: 核心模型 (Week 1-2)
- [ ] 实现 `ChunkEnrichment` 模型
- [ ] 实现 `ChunkIndex` 模型
- [ ] 实现 `EnrichmentCache` 模型
- [ ] 数据库 Migration

### Phase 2: 增强处理器 (Week 3-4)
- [ ] 实现 `Enricher` 接口和基础处理器
  - [ ] `KeywordExtractor` (基于规则)
  - [ ] `LLMSummarizer` (调用 LLM)
  - [ ] `CodeExplainer` (针对代码块)
  - [ ] `TableDescriber` (针对表格)
- [ ] 实现 `EnrichmentService`
- [ ] 实现缓存机制

### Phase 3: 索引管道 (Week 5-6)
- [ ] 实现 `EnrichmentPipeline` 配置
- [ ] 修改 `ChunkService` 集成增强流程
- [ ] 实现多索引策略 (BM25 + Vector)
- [ ] 更新 OpenSearch/Milvus 索引结构

### Phase 4: 检索优化 (Week 7-8)
- [ ] 修改 `RAGService` 支持多索引检索
- [ ] 实现混合检索融合算法
- [ ] 添加调试接口

---

## 8. 关键技术点

### 8.1 缓存键生成
```go
func GenerateCacheKey(content string, enricherName string, enricherVersion string) string {
    data := fmt.Sprintf("%s|%s|%s", content, enricherName, enricherVersion)
    hash := sha256.Sum256([]byte(data))
    return hex.EncodeToString(hash[:])
}
```

### 8.2 增强处理器接口
```go
type Enricher interface {
    Name() string
    Version() string
    SupportedChunkTypes() []string
    
    Enrich(ctx context.Context, chunk *models.Chunk) (*EnrichmentResult, error)
}

type EnrichmentResult struct {
    EnrichmentType  string
    EnrichedContent string
    EnrichedData    map[string]interface{}
    Confidence      float64
    LLMUsage        *models.LLMUsageRecord  // 如果使用了 LLM
}
```

### 8.3 索引内容解析
```go
func (s *IndexService) resolveIndexContent(chunk *models.Chunk, source string) (string, error) {
    parts := strings.SplitN(source, ":", 2)
    
    switch parts[0] {
    case "original":
        return chunk.Content, nil
        
    case "enrichment":
        enrichmentType := parts[1]
        enrichment, err := s.getEnrichment(chunk.ID, enrichmentType)
        if err != nil {
            return "", err
        }
        return enrichment.EnrichedContent, nil
        
    case "composite":
        // 组合多个来源
        sources := strings.Split(parts[1], "+")
        var contents []string
        for _, src := range sources {
            content, err := s.resolveIndexContent(chunk, src)
            if err != nil {
                continue
            }
            contents = append(contents, content)
        }
        return strings.Join(contents, "\n\n"), nil
        
    default:
        return "", fmt.Errorf("unknown content source: %s", source)
    }
}
```

---

## 9. 预期效果

### 9.1 检索质量提升
- **代码块**: 用户搜索 "如何实现JWT认证" → 命中语义摘要 → 返回原始代码
- **表格**: 用户搜索 "支付相关的服务" → 命中表格描述 → 返回原始表格
- **精确匹配**: 用户搜索 "user-api" → 命中关键词索引 → 快速定位

### 9.2 成本优化
- **缓存命中率**: 相似内容共享增强结果,预计节省 60% LLM 调用
- **增量更新**: 只对新增/修改的 Chunk 进行增强处理

### 9.3 可观测性
- 每个 Chunk 可查看所有增强结果
- 每次检索可追溯到具体的索引类型
- LLM 使用成本可追踪到具体文档/Chunk

---

## 10. 未来扩展

### 10.1 自定义增强处理器
允许用户通过 Plugin 方式注册自定义 Enricher:
```go
// 例如: 法律文档的条款编号提取器
type LegalClauseExtractor struct{}

func (e *LegalClauseExtractor) Enrich(ctx context.Context, chunk *models.Chunk) (*EnrichmentResult, error) {
    // 提取 "第一条"、"第二条" 等结构
}
```

### 10.2 多模态增强
支持图片、音频等内容的增强:
```go
// 图片描述生成
type ImageCaptionEnricher struct{}

// 音频转文字 + 摘要
type AudioTranscriptEnricher struct{}
```

### 10.3 增强结果反馈
允许用户对增强结果进行评分,用于优化 Prompt 或训练微调模型。

---

**总结**: 这个架构将原始内容、检索内容、语义增强内容清晰分离,支持灵活的增强处理器和多索引策略,同时保证成本可控和结果可追溯。
