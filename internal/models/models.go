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
	Metadata         JSONB      `gorm:"type:jsonb" json:"metadata"`
	SemanticMetadata JSONB      `gorm:"type:jsonb" json:"semantic_metadata"`
	PublishedAt      *time.Time `json:"published_at"`
	AuthorID         *string    `gorm:"size:12" json:"author_id"`
}

// Chunk represents a text chunk of a document
type Chunk struct {
	TenantModel
	DocumentID    string `gorm:"size:12;not null;index" json:"document_id"`
	Content       string `gorm:"type:text;not null" json:"content"`
	ChunkIndex    int    `gorm:"not null" json:"chunk_index"`
	StartPosition int    `json:"start_position"`
	EndPosition   int    `json:"end_position"`
	ChunkType     string `gorm:"size:20" json:"chunk_type"` // section, paragraph, table, code, list
	Metadata      JSONB  `gorm:"type:jsonb" json:"metadata"`
	VectorID      string `gorm:"size:100" json:"vector_id"` // ID in vector store
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
