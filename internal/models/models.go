package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// BaseModel contains common fields for all models
type BaseModel struct {
	ID        string         `gorm:"primaryKey;type:varchar(12)" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// BeforeCreate generates a random ID if not already set
func (b *BaseModel) BeforeCreate(tx *gorm.DB) error {
	if b.ID == "" {
		b.ID = GenerateID()
	}
	return nil
}

// TenantModel adds tenant isolation to base model
type TenantModel struct {
	BaseModel
	TenantID string `gorm:"size:12;not null;index" json:"tenant_id"`
}

// Tenant represents a tenant in the system
type Tenant struct {
	BaseModel
	Name        string `gorm:"size:255;not null;unique" json:"name"`
	Code        string `gorm:"size:50;not null;unique" json:"code"`
	Description string `gorm:"type:text" json:"description"`
	Status      string `gorm:"size:20;default:'active'" json:"status"` // active, suspended, deleted
	Settings    JSONB  `gorm:"type:jsonb" json:"settings"`
}

// KnowledgeBase represents a knowledge base within a tenant
type KnowledgeBase struct {
	TenantModel
	Name        string `gorm:"size:255;not null" json:"name"`
	Description string `gorm:"type:text" json:"description"`
	Settings    JSONB  `gorm:"type:jsonb" json:"settings"`
	Status      string `gorm:"size:20;default:'active'" json:"status"`
	
	// Language distribution (updated on document creation/deletion)
	// Format: {"zh": 0.7, "en": 0.3, "mixed": 0.0}
	LanguageDistribution JSONB `gorm:"type:jsonb" json:"language_distribution"`
}

// Document represents a document in a knowledge base
type Document struct {
	TenantModel
	KnowledgeBaseID  string     `gorm:"size:12;not null;index" json:"knowledge_base_id"`
	Title            string     `gorm:"size:500;not null" json:"title"`
	Content          string     `gorm:"type:text" json:"content"`
	Summary          string     `gorm:"type:text" json:"summary"`
	DocType          string     `gorm:"size:50" json:"doc_type"` // knowledge, process, data, brief, experience
	Format           string     `gorm:"size:20" json:"format"`   // markdown, pdf, docx, xlsx, pptx, txt
	FilePath         string     `gorm:"size:500" json:"file_path"`
	FileSize         int64      `json:"file_size"`
	Status           string     `gorm:"size:20;default:'draft'" json:"status"` // draft, published, archived
	
	// Language detection (detected at index time)
	Language     string  `gorm:"size:10;index" json:"language"`      // "en", "zh", "ja", "mixed", etc.
	LanguageConf float64 `gorm:"default:0" json:"language_conf"`     // 0.0-1.0 confidence
	
	Metadata         JSONB      `gorm:"type:jsonb" json:"metadata"`
	SemanticMetadata JSONB      `gorm:"type:jsonb" json:"semantic_metadata"`
	
	// ✅ 目录导航结构: 存储文档的标题层级树,用于快速导航和定位 chunks
	// 结构: [{"level":1,"title":"...","path":"...","chunk_ids":[1,2,3],"children":[...]}]
	// 价值: 1) 导航类查询 ("核心组件有哪些?") 直接返回子章节
	//      2) 快速跳转 ("跳到缓存层") 直接获取相关 chunk IDs
	//      3) 层级过滤和聚合
	TOCStructure JSONB `gorm:"type:jsonb" json:"toc_structure"`
	
	PublishedAt *time.Time `json:"published_at"`
	AuthorID    *string    `gorm:"size:12" json:"author_id"`
}

// TOCNode 表示目录树的一个节点
type TOCNode struct {
	Level    int       `json:"level"`     // 标题级别 (1-6)
	Title    string    `json:"title"`     // 标题文本
	Path     string    `json:"path"`      // 完整路径 (h1 > h2 > h3)
	ChunkIDs []int     `json:"chunk_ids"` // 该章节关联的所有 chunk 索引
	Children []TOCNode `json:"children"`  // 子章节
	Position int       `json:"position"`  // 在文档中的位置 (block index)
}

// DocumentTOCIndex 文档目录索引表 - 用于高效的 TOC 标题搜索
// 将嵌套的 TOC 结构平铺为可查询的表,支持快速标题匹配和章节定位
type DocumentTOCIndex struct {
	TenantModel
	KnowledgeBaseID string `gorm:"size:12;not null;index" json:"knowledge_base_id"`
	DocumentID      string `gorm:"size:12;not null;index" json:"document_id"`
	
	// 标题信息
	Title string `gorm:"size:500;not null;index" json:"title"` // ✅ 建索引,支持快速 ILIKE 查找
	Level int    `gorm:"not null;index" json:"level"`          // 标题级别 (1-6)
	Path  string `gorm:"size:2000" json:"path"`                // 完整路径 (用于展示)
	
	// ✅ 关联的 chunk IDs (JSONB 数组)
	// 格式: {"ids": [1, 2, 3]}
	ChunkIDs JSONB `gorm:"type:jsonb" json:"chunk_ids"`
	
	// 层级关系
	ParentID *string `gorm:"size:12;index" json:"parent_id"` // 父节点 ID (用于树形查询)
	Position int     `gorm:"not null" json:"position"`       // 在文档中的位置 (block index)
}

// Chunk represents a text chunk of a document
// Chunk represents a text chunk of a document (原始内容层)
type Chunk struct {
	TenantModel
	DocumentID    string `gorm:"size:12;not null;index" json:"document_id"`
	Content       string `gorm:"type:text;not null" json:"content"` // 原始内容,用于展示和回源
	ChunkIndex    int    `gorm:"not null" json:"chunk_index"`
	StartPosition int    `json:"start_position"`
	EndPosition   int    `json:"end_position"`
	ChunkType     string `gorm:"size:20" json:"chunk_type"` // section, paragraph, table, code, list

	// 结构化元数据 (原始结构信息)
	// 例如: 表格 - {"headers": [...], "row_count": 10, "col_count": 3}
	//      代码 - {"language": "go", "functions": [...], "imports": [...]}
	//      章节 - {"heading_level": 2, "heading_text": "..."}
	StructMeta JSONB `gorm:"type:jsonb" json:"struct_meta"`

	// 通用元数据 (向后兼容)
	Metadata JSONB `gorm:"type:jsonb" json:"metadata"`

	// 增强处理状态
	EnrichmentStatus string `gorm:"size:20;default:'pending'" json:"enrichment_status"` // pending, processing, completed, failed
	EnrichmentError  string `gorm:"type:text" json:"enrichment_error"`

	// 向后兼容字段
	VectorID string `gorm:"size:100" json:"vector_id"` // ID in vector store (legacy)
}

// TableData represents structured table data
type TableData struct {
	TenantModel
	DocumentID string `gorm:"size:12;not null;index" json:"document_id"`
	TableIndex int    `gorm:"not null" json:"table_index"`
	Headers    JSONB  `gorm:"type:jsonb" json:"headers"`
	Rows       JSONB  `gorm:"type:jsonb" json:"rows"`
	Summary    string `gorm:"type:text" json:"summary"`
}

// ExperienceCard represents an experience/lesson learned card
type ExperienceCard struct {
	TenantModel
	DocumentID      string  `gorm:"size:12;index" json:"document_id"`
	Title           string  `gorm:"size:500;not null" json:"title"`
	ProblemKeywords string  `gorm:"type:text" json:"problem_keywords"`
	ProblemDesc     string  `gorm:"type:text" json:"problem_desc"`
	Symptoms        string  `gorm:"type:text" json:"symptoms"`
	ErrorInfo       string  `gorm:"type:text" json:"error_info"`
	SolutionDesc    string  `gorm:"type:text" json:"solution_desc"`
	SolutionSteps   JSONB   `gorm:"type:jsonb" json:"solution_steps"`
	CodeSnippets    JSONB   `gorm:"type:jsonb" json:"code_snippets"`
	TechStack       JSONB   `gorm:"type:jsonb" json:"tech_stack"`
	Scenarios       JSONB   `gorm:"type:jsonb" json:"scenarios"`
	Effectiveness   float64 `gorm:"default:0" json:"effectiveness"`
	VerifyCount     int     `gorm:"default:0" json:"verify_count"`
	SuccessRate     float64 `gorm:"default:0" json:"success_rate"`
	VectorID        string  `gorm:"size:100" json:"vector_id"`
}

// GraphEntity represents an entity in the knowledge graph
type GraphEntity struct {
	TenantModel
	KnowledgeBaseID string `gorm:"size:12;index" json:"knowledge_base_id"`
	DocumentID      string `gorm:"size:12;index" json:"document_id"`
	Name            string `gorm:"size:255;not null" json:"name"`
	Type            string `gorm:"size:50;not null" json:"type"` // person, concept, service, component, api
	Properties      JSONB  `gorm:"type:jsonb" json:"properties"`
	Neo4jID         string `gorm:"size:100" json:"neo4j_id"`
}

// GraphRelation represents a relation between entities
// NOTE: No GORM foreign key associations — avoids migration failures
type GraphRelation struct {
	TenantModel
	KnowledgeBaseID string  `gorm:"size:12;index" json:"knowledge_base_id"`
	SourceID        string  `gorm:"size:12;not null;index" json:"source_id"`
	TargetID        string  `gorm:"size:12;not null;index" json:"target_id"`
	Type            string  `gorm:"size:50;not null" json:"type"` // contains, references, depends_on, causes, implements
	Weight          float64 `gorm:"default:1.0" json:"weight"`
	Properties      JSONB   `gorm:"type:jsonb" json:"properties"`
}

// ProcessingEvent records each indexing decision and execution step
// status: started, success, warning, failed
type ProcessingEvent struct {
	TenantModel
	DocumentID string `gorm:"size:12;not null;index" json:"document_id"`
	Step       int    `gorm:"not null" json:"step"`
	Stage      string `gorm:"size:50;not null" json:"stage"`
	Status     string `gorm:"size:20;not null" json:"status"`
	Message    string `gorm:"type:text" json:"message"`
	Details    JSONB  `gorm:"type:jsonb" json:"details"`
}

// LLMUsageRecord records each LLM/Embedding API call with token counts and cost
type LLMUsageRecord struct {
	TenantModel
	DocumentID      *string `gorm:"size:12;index" json:"document_id,omitempty"`
	KnowledgeBaseID *string `gorm:"size:12;index" json:"knowledge_base_id,omitempty"`
	// caller context
	CallerService string `gorm:"size:50;not null;index" json:"caller_service"` // document, rag, graph
	CallerMethod  string `gorm:"size:100;not null" json:"caller_method"`       // e.g. inferDocumentType, extractEntities, generateSummary
	// model info
	ModelID   string `gorm:"size:200;not null" json:"model_id"`
	ModelType string `gorm:"size:20;not null" json:"model_type"` // chat, embedding
	// token counts
	InputTokens  int `gorm:"default:0" json:"input_tokens"`
	OutputTokens int `gorm:"default:0" json:"output_tokens"`
	TotalTokens  int `gorm:"default:0" json:"total_tokens"`
	// cost estimation (USD)
	EstimatedCost float64 `gorm:"default:0" json:"estimated_cost"`
	// execution info
	DurationMs int64  `gorm:"default:0" json:"duration_ms"`
	Status     string `gorm:"size:20;default:'success'" json:"status"` // success, error
	ErrorMsg   string `gorm:"type:text" json:"error_msg,omitempty"`
}

// ============================================================================
// Query Processing Models
// ============================================================================

// QuerySession 查询会话 - 记录完整的查询处理过程
type QuerySession struct {
	TenantModel
	KnowledgeBaseID string `gorm:"size:12;not null;index" json:"knowledge_base_id"`
	
	// 原始查询
	OriginalQuery string `gorm:"type:text;not null" json:"original_query"`
	
	// 查询分析结果 (LLM 输出)
	// 格式: {"primary_intent": "navigation", "secondary_intents": ["content_search"], "entities": [...], "keywords": [...]}
	QueryAnalysis JSONB `gorm:"type:jsonb" json:"query_analysis"`
	
	// 改写后的查询 (如果有)
	// 格式: [{"query": "...", "weight": 1.0, "strategy": "structured"}]
	RewrittenQueries JSONB `gorm:"type:jsonb" json:"rewritten_queries"`
	
	// 召回配置
	// 格式: {"channels": ["bm25", "vector", "toc_navigation"], "top_k": 20}
	RecallConfig JSONB `gorm:"type:jsonb" json:"recall_config"`
	
	// 最终答案
	Answer string `gorm:"type:text" json:"answer"`
	
	// 引用的 chunks (chunk IDs)
	// 格式: {"ids": ["chk_001", "chk_002"]}
	SourceChunkIDs JSONB `gorm:"type:jsonb" json:"source_chunk_ids"`
	
	// 性能指标
	DurationMs int64 `gorm:"default:0" json:"duration_ms"`
	
	// 关联的 LLM 调用记录
	// 格式: {"ids": ["llm_001", "llm_002"]}
	LLMUsageIDs JSONB `gorm:"type:jsonb" json:"llm_usage_ids"`
	
	// 成本
	TotalCost float64 `gorm:"default:0" json:"total_cost"`
	
	// 状态
	Status string `gorm:"size:20;default:'success'" json:"status"` // success, error
	Error  string `gorm:"type:text" json:"error"`
}

// QueryAnalysisResult 查询分析结果 (用于程序内传递,不存库)
type QueryAnalysisResult struct {
	PrimaryIntent     string   `json:"primary_intent"`      // 主要意图
	SecondaryIntents  []string `json:"secondary_intents"`   // 次要意图
	Confidence        float64  `json:"confidence"`          // 置信度
	Entities          []Entity `json:"entities"`            // 提取的实体
	QueryTypes        []string `json:"query_types"`         // 查询类型: code, section, table
	Keywords          []string `json:"keywords"`            // 关键词
	Language          string   `json:"language"`            // zh-CN, en-US
	Reasoning         string   `json:"reasoning"`           // 分析理由
	LLMUsageID        string   `json:"llm_usage_id"`        // 关联的 LLM 调用 ID
}

// Entity 实体
type Entity struct {
	Name string `json:"name"` // 实体名称
	Type string `json:"type"` // concept, service, function, technology, person
}

// RecallResult 召回结果 - 记录单个 chunk 的召回信息
type RecallResult struct {
	TenantModel
	QuerySessionID string `gorm:"size:12;not null;index" json:"query_session_id"`
	
	// 召回通道
	Channel string `gorm:"size:30;not null;index" json:"channel"` // bm25, vector, toc_navigation, graph
	
	// 关联的 Chunk
	ChunkID      string  `gorm:"size:12;not null;index" json:"chunk_id"`
	ChunkIndexID *string `gorm:"size:12;index" json:"chunk_index_id"` // 哪个索引返回的 (可选)
	
	// 原始分数
	RawScore float64 `gorm:"default:0" json:"raw_score"`
	Rank     int     `gorm:"default:0" json:"rank"` // 在该通道中的排名
	
	// 融合后分数 (RRF)
	RRFScore  float64 `gorm:"default:0" json:"rrf_score"`
	FusedRank int     `gorm:"default:0" json:"fused_rank"`
	
	// 重排序分数 (LLM Reranking)
	RerankedScore *float64 `gorm:"default:null" json:"reranked_score"`
	FinalRank     *int     `gorm:"default:null" json:"final_rank"`
	
	// 是否最终返回给用户
	IsReturned bool `gorm:"default:false" json:"is_returned"`
	
	// 调试信息
	// 格式: {"matched_fields": ["keywords"], "highlight": "JWT 认证", "toc_path": "1.1 > 1.1.1"}
	DebugInfo JSONB `gorm:"type:jsonb" json:"debug_info"`
}

// JSONB type for PostgreSQL JSONB
type JSONB map[string]interface{}

// Value implements driver.Valuer
func (j JSONB) Value() (driver.Value, error) {
	if j == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(j)
}

// Scan implements sql.Scanner
func (j *JSONB) Scan(value interface{}) error {
	if j == nil {
		return fmt.Errorf("JSONB: Scan on nil pointer")
	}

	switch v := value.(type) {
	case nil:
		*j = JSONB{}
		return nil
	case []byte:
		if len(v) == 0 {
			*j = JSONB{}
			return nil
		}
		return json.Unmarshal(v, j)
	case string:
		if strings.TrimSpace(v) == "" {
			*j = JSONB{}
			return nil
		}
		return json.Unmarshal([]byte(v), j)
	case map[string]interface{}:
		*j = JSONB(v)
		return nil
	default:
		return fmt.Errorf("JSONB: unsupported Scan type %T", value)
	}
}

// SearchResult represents a search result
type SearchResult struct {
	ID         string                 `json:"id"`
	DocumentID string                 `json:"document_id"`
	Title      string                 `json:"title"`
	Content    string                 `json:"content"`
	Score      float64                `json:"score"`
	Source     string                 `json:"source"` // text, vector, graph
	DocType    string                 `json:"doc_type"`
	Metadata   map[string]interface{} `json:"metadata"`
	Highlights []string               `json:"highlights,omitempty"`
}

// ============================================================================
// Content Enrichment & Multi-Index Models
// ============================================================================

// ChunkEnrichment 存储对原始 Chunk 的各种增强结果
type ChunkEnrichment struct {
	TenantModel
	ChunkID string `gorm:"size:12;not null;index" json:"chunk_id"`

	// 增强类型标识
	// 预定义类型:
	// - "keyword_extraction"    提取的关键词列表
	// - "semantic_summary"      LLM 生成的语义摘要
	// - "code_explanation"      代码块的自然语言解释
	// - "table_description"     表格的自然语言描述
	// - "entity_extraction"     提取的实体列表
	// - "question_generation"   生成的问题列表(用于问答对训练)
	// - 自定义: "custom_xxx"
	EnrichmentType string `gorm:"size:50;not null;index" json:"enrichment_type"`

	// 增强内容 (文本形式)
	EnrichedContent string `gorm:"type:text" json:"enriched_content"`

	// 结构化增强数据 (JSON格式)
	// 例如:
	// - keyword_extraction: {"keywords": ["API", "认证", "JWT"], "weights": [0.9, 0.8, 0.7]}
	// - code_explanation: {"summary": "...", "params": [...], "returns": "..."}
	// - question_generation: {"questions": ["如何实现JWT认证?", "认证流程是什么?"]}
	EnrichedData JSONB `gorm:"type:jsonb" json:"enriched_data"`

	// 增强来源
	EnricherName    string `gorm:"size:100" json:"enricher_name"`       // "llm:gpt-4", "rule:keyword", "model:bert-ner"
	EnricherVersion string `gorm:"size:50" json:"enricher_version"`     // "1.0.0"
	Confidence      float64 `gorm:"default:0" json:"confidence"`        // 增强结果的置信度 [0-1]
	LLMUsageID      *string `gorm:"size:12;index" json:"llm_usage_id"` // 关联的 LLM 使用记录
}

// ChunkIndex 定义如何为 Chunk 创建索引 (支持一个 Chunk 多种索引策略)
type ChunkIndex struct {
	TenantModel
	ChunkID string `gorm:"size:12;not null;index" json:"chunk_id"`

	// 索引类型
	IndexType string `gorm:"size:30;not null" json:"index_type"` // "bm25", "vector", "hybrid"

	// 索引内容来源 (指向具体的内容或增强结果)
	// 取值:
	// - "original"                           使用 Chunk.Content
	// - "enrichment:semantic_summary"        使用 ChunkEnrichment(type=semantic_summary).EnrichedContent
	// - "enrichment:keyword_extraction"      使用关键词列表
	// - "composite:original+keywords"        组合多个来源
	ContentSource string `gorm:"size:50;not null" json:"content_source"`

	// 外部索引 ID (OpenSearch/Milvus 的文档 ID)
	ExternalIndexID string `gorm:"size:200" json:"external_index_id"`

	// 索引配置
	// 例如:
	// - vector: {"embedding_model": "text-embedding-3-small", "dimension": 1536}
	// - bm25: {"analyzer": "standard", "boost": 1.0}
	IndexConfig JSONB `gorm:"type:jsonb" json:"index_config"`

	// 索引状态
	Status    string     `gorm:"size:20;default:'pending'" json:"status"` // pending, indexed, failed
	IndexedAt *time.Time `json:"indexed_at"`
}

// EnrichmentPipeline 定义文档类型对应的增强处理流程
type EnrichmentPipeline struct {
	TenantModel
	Name        string `gorm:"size:255;not null;unique" json:"name"` // "api_doc_pipeline", "code_snippet_pipeline"
	Description string `gorm:"type:text" json:"description"`

	// 适用条件 (PostgreSQL array types)
	ApplicableDocTypes   JSONB `gorm:"type:jsonb" json:"applicable_doc_types"`   // ["knowledge", "experience"]
	ApplicableChunkTypes JSONB `gorm:"type:jsonb" json:"applicable_chunk_types"` // ["code", "table"]

	// 增强处理器链 (按顺序执行)
	// 例如:
	// [
	//   {"name": "keyword_extractor", "type": "rule", "config": {...}},
	//   {"name": "code_summarizer", "type": "llm", "config": {"model": "gpt-4o-mini"}},
	//   {"name": "entity_extractor", "type": "ml", "config": {"model": "bert-ner"}}
	// ]
	Processors JSONB `gorm:"type:jsonb;not null" json:"processors"`

	// 索引策略
	// 例如:
	// [
	//   {"type": "bm25", "source": "original", "boost": 1.0},
	//   {"type": "vector", "source": "enrichment:semantic_summary", "embedding_model": "..."}
	// ]
	IndexStrategies JSONB `gorm:"type:jsonb;not null" json:"index_strategies"`

	Status string `gorm:"size:20;default:'active'" json:"status"` // active, disabled
}

// EnrichmentCache 用于缓存 LLM 增强结果,避免重复调用
type EnrichmentCache struct {
	TenantModel

	// 缓存键 (基于内容和处理器生成)
	CacheKey string `gorm:"size:64;not null;unique;index" json:"cache_key"` // SHA256(content + enricher + version)

	// 输入内容摘要 (用于调试)
	ContentHash    string `gorm:"size:64;not null" json:"content_hash"`
	ContentLength  int    `json:"content_length"`
	ContentPreview string `gorm:"size:500" json:"content_preview"` // 前500字符

	// 增强结果
	EnrichmentType  string `gorm:"size:50;not null" json:"enrichment_type"`
	EnrichedContent string `gorm:"type:text" json:"enriched_content"`
	EnrichedData    JSONB  `gorm:"type:jsonb" json:"enriched_data"`

	// 处理器信息
	EnricherName    string `gorm:"size:100;not null" json:"enricher_name"`
	EnricherVersion string `gorm:"size:50;not null" json:"enricher_version"`

	// 使用统计
	HitCount  int       `gorm:"default:0" json:"hit_count"`
	LastHitAt time.Time `json:"last_hit_at"`

	// LLM 成本记录
	LLMUsageID *string `gorm:"size:12;index" json:"llm_usage_id"`

	// 缓存过期时间
	ExpiresAt *time.Time `gorm:"index" json:"expires_at"`
}

// QueryRewriteCache 缓存查询改写结果
type QueryRewriteCache struct {
	TenantModel

	// 缓存键
	CacheKey string `gorm:"size:64;not null;unique;index" json:"cache_key"` // SHA256(query + strategy)

	// 原始查询
	OriginalQuery string `gorm:"type:text;not null" json:"original_query"`

	// 改写结果
	RewrittenQueries JSONB `gorm:"type:jsonb;not null" json:"rewritten_queries"`

	// 改写策略
	Strategy string `gorm:"size:50;not null" json:"strategy"` // synonym, structured, translation, expansion, analysis

	// 使用统计
	HitCount  int       `gorm:"default:0" json:"hit_count"`
	LastHitAt time.Time `json:"last_hit_at"`

	// LLM 使用记录
	LLMUsageID *string `gorm:"size:12;index" json:"llm_usage_id"`

	// 缓存过期时间
	ExpiresAt *time.Time `gorm:"index" json:"expires_at"`
}
