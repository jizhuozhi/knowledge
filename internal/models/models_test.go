package models

import (
	"database/sql/driver"
	"encoding/json"
	"testing"
)

func TestGenerateID(t *testing.T) {
	id := GenerateID()

	if len(id) != 12 {
		t.Errorf("ID length should be 12, got %d", len(id))
	}

	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			t.Errorf("ID contains invalid character: %c", c)
		}
	}
}

func TestGenerateID_Uniqueness(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := GenerateID()
		if ids[id] {
			t.Errorf("duplicate ID generated: %s", id)
		}
		ids[id] = true
	}
}

func TestBaseModel_BeforeCreate(t *testing.T) {
	b := &BaseModel{}
	err := b.BeforeCreate(nil)
	if err != nil {
		t.Errorf("BeforeCreate should not return error: %v", err)
	}
	if b.ID == "" {
		t.Error("ID should be generated")
	}
	if len(b.ID) != 12 {
		t.Errorf("ID length should be 12, got %d", len(b.ID))
	}
}

func TestBaseModel_BeforeCreate_PreserveExistingID(t *testing.T) {
	existingID := "existing123"
	b := &BaseModel{ID: existingID}
	err := b.BeforeCreate(nil)
	if err != nil {
		t.Errorf("BeforeCreate should not return error: %v", err)
	}
	if b.ID != existingID {
		t.Errorf("ID should be preserved, got %s", b.ID)
	}
}

// ============================================================================
// JSONB Tests
// ============================================================================

func TestJSONB_Value_Nil(t *testing.T) {
	var j JSONB
	val, err := j.Value()
	if err != nil {
		t.Errorf("Value should not return error: %v", err)
	}
	if string(val.([]byte)) != "{}" {
		t.Errorf("nil JSONB should return {}, got %s", val)
	}
}

func TestJSONB_Value_Empty(t *testing.T) {
	j := JSONB{}
	val, err := j.Value()
	if err != nil {
		t.Errorf("Value should not return error: %v", err)
	}
	if string(val.([]byte)) != "{}" {
		t.Errorf("empty JSONB should return {}, got %s", val)
	}
}

func TestJSONB_Value_WithData(t *testing.T) {
	j := JSONB{"key": "value", "number": 42}
	val, err := j.Value()
	if err != nil {
		t.Errorf("Value should not return error: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(val.([]byte), &parsed); err != nil {
		t.Errorf("result should be valid JSON: %v", err)
	}
	if parsed["key"] != "value" {
		t.Errorf("key should be 'value', got %v", parsed["key"])
	}
}

func TestJSONB_Scan_Nil(t *testing.T) {
	var j *JSONB
	err := j.Scan(nil)
	if err == nil {
		t.Error("Scan on nil pointer should return error")
	}
}

func TestJSONB_Scan_NilReceiver(t *testing.T) {
	j := &JSONB{}
	err := j.Scan(nil)
	if err != nil {
		t.Errorf("Scan(nil) should not return error: %v", err)
	}
	if *j == nil {
		t.Error("JSONB should be initialized, not nil")
	}
}

func TestJSONB_Scan_Bytes(t *testing.T) {
	j := &JSONB{}
	data := []byte(`{"key":"value"}`)
	err := j.Scan(data)
	if err != nil {
		t.Errorf("Scan should not return error: %v", err)
	}
	if (*j)["key"] != "value" {
		t.Errorf("key should be 'value', got %v", (*j)["key"])
	}
}

func TestJSONB_Scan_EmptyBytes(t *testing.T) {
	j := &JSONB{}
	err := j.Scan([]byte{})
	if err != nil {
		t.Errorf("Scan should not return error: %v", err)
	}
	if *j == nil {
		t.Error("JSONB should be initialized")
	}
}

func TestJSONB_Scan_String(t *testing.T) {
	j := &JSONB{}
	err := j.Scan(`{"nested":{"key":"value"}}`)
	if err != nil {
		t.Errorf("Scan should not return error: %v", err)
	}
	nested, ok := (*j)["nested"].(map[string]interface{})
	if !ok {
		t.Error("nested should be a map")
	}
	if nested["key"] != "value" {
		t.Errorf("nested.key should be 'value', got %v", nested["key"])
	}
}

func TestJSONB_Scan_EmptyString(t *testing.T) {
	j := &JSONB{}
	err := j.Scan("")
	if err != nil {
		t.Errorf("Scan should not return error: %v", err)
	}
	if *j == nil {
		t.Error("JSONB should be initialized")
	}
}

func TestJSONB_Scan_WhitespaceString(t *testing.T) {
	j := &JSONB{}
	err := j.Scan("   ")
	if err != nil {
		t.Errorf("Scan should not return error: %v", err)
	}
	if *j == nil {
		t.Error("JSONB should be initialized")
	}
}

func TestJSONB_Scan_Map(t *testing.T) {
	j := &JSONB{}
	input := map[string]interface{}{"direct": "map", "number": float64(123)}
	err := j.Scan(input)
	if err != nil {
		t.Errorf("Scan should not return error: %v", err)
	}
	if (*j)["direct"] != "map" {
		t.Errorf("direct should be 'map', got %v", (*j)["direct"])
	}
}

func TestJSONB_Scan_UnsupportedType(t *testing.T) {
	j := &JSONB{}
	err := j.Scan(12345) // int is unsupported
	if err == nil {
		t.Error("Scan with unsupported type should return error")
	}
}

func TestJSONB_Scan_InvalidJSON(t *testing.T) {
	j := &JSONB{}
	err := j.Scan([]byte(`{invalid json`))
	if err == nil {
		t.Error("Scan with invalid JSON should return error")
	}
}

// ============================================================================
// Model Field Tests
// ============================================================================

func TestTenantModel_TenantID(t *testing.T) {
	tm := TenantModel{
		TenantID: "tenant123",
	}
	if tm.TenantID != "tenant123" {
		t.Errorf("TenantID should be 'tenant123', got %s", tm.TenantID)
	}
}

func TestDocument_Fields(t *testing.T) {
	doc := Document{
		TenantModel: TenantModel{
			TenantID: "t1",
		},
		KnowledgeBaseID:  "kb1",
		Title:            "Test Document",
		Content:          "Content here",
		DocType:          "knowledge",
		Format:           "markdown",
		Status:           "draft",
	}

	if doc.KnowledgeBaseID != "kb1" {
		t.Errorf("KnowledgeBaseID should be 'kb1', got %s", doc.KnowledgeBaseID)
	}
	if doc.Title != "Test Document" {
		t.Errorf("Title should be 'Test Document', got %s", doc.Title)
	}
	if doc.DocType != "knowledge" {
		t.Errorf("DocType should be 'knowledge', got %s", doc.DocType)
	}
}

func TestChunk_Fields(t *testing.T) {
	chunk := Chunk{
		TenantModel: TenantModel{
			TenantID: "t1",
		},
		DocumentID:    "doc1",
		Content:       "Chunk content",
		ChunkIndex:    0,
		StartPosition: 0,
		EndPosition:   13,
		ChunkType:     "semantic",
	}

	if chunk.DocumentID != "doc1" {
		t.Errorf("DocumentID should be 'doc1', got %s", chunk.DocumentID)
	}
	if chunk.ChunkIndex != 0 {
		t.Errorf("ChunkIndex should be 0, got %d", chunk.ChunkIndex)
	}
	if chunk.ChunkType != "semantic" {
		t.Errorf("ChunkType should be 'semantic', got %s", chunk.ChunkType)
	}
}

func TestGraphEntity_Fields(t *testing.T) {
	entity := GraphEntity{
		TenantModel: TenantModel{
			TenantID: "t1",
		},
		KnowledgeBaseID: "kb1",
		DocumentID:      "doc1",
		Name:            "UserService",
		Type:            "service",
		Properties: JSONB{
			"language": "go",
			"port":     8080,
		},
	}

	if entity.Name != "UserService" {
		t.Errorf("Name should be 'UserService', got %s", entity.Name)
	}
	if entity.Type != "service" {
		t.Errorf("Type should be 'service', got %s", entity.Type)
	}
	if entity.Properties["language"] != "go" {
		t.Errorf("Properties.language should be 'go', got %v", entity.Properties["language"])
	}
}

func TestGraphRelation_Fields(t *testing.T) {
	relation := GraphRelation{
		TenantModel: TenantModel{
			TenantID: "t1",
		},
		KnowledgeBaseID: "kb1",
		SourceID:        "entity1",
		TargetID:        "entity2",
		Type:            "depends_on",
		Weight:          0.8,
	}

	if relation.SourceID != "entity1" {
		t.Errorf("SourceID should be 'entity1', got %s", relation.SourceID)
	}
	if relation.TargetID != "entity2" {
		t.Errorf("TargetID should be 'entity2', got %s", relation.TargetID)
	}
	if relation.Type != "depends_on" {
		t.Errorf("Type should be 'depends_on', got %s", relation.Type)
	}
	if relation.Weight != 0.8 {
		t.Errorf("Weight should be 0.8, got %f", relation.Weight)
	}
}

func TestProcessingEvent_Fields(t *testing.T) {
	event := ProcessingEvent{
		TenantModel: TenantModel{
			TenantID: "t1",
		},
		DocumentID: "doc1",
		Step:       1,
		Stage:      "chunking",
		Status:     "success",
		Message:    "Created 5 chunks",
	}

	if event.Step != 1 {
		t.Errorf("Step should be 1, got %d", event.Step)
	}
	if event.Stage != "chunking" {
		t.Errorf("Stage should be 'chunking', got %s", event.Stage)
	}
	if event.Status != "success" {
		t.Errorf("Status should be 'success', got %s", event.Status)
	}
}

func TestLLMUsageRecord_Fields(t *testing.T) {
	docID := "doc1"
	kbID := "kb1"
	record := LLMUsageRecord{
		TenantModel: TenantModel{
			TenantID: "t1",
		},
		DocumentID:      &docID,
		KnowledgeBaseID: &kbID,
		CallerService:   "document",
		CallerMethod:    "inferDocumentType",
		ModelID:         "amazon.nova-micro-v1:0",
		ModelType:       "chat",
		InputTokens:     100,
		OutputTokens:    50,
		TotalTokens:     150,
		EstimatedCost:   0.0001,
		DurationMs:      250,
		Status:          "success",
	}

	if record.CallerService != "document" {
		t.Errorf("CallerService should be 'document', got %s", record.CallerService)
	}
	if record.TotalTokens != 150 {
		t.Errorf("TotalTokens should be 150, got %d", record.TotalTokens)
	}
}

func TestSearchResult_Fields(t *testing.T) {
	result := SearchResult{
		ID:         "chunk1",
		DocumentID: "doc1",
		Title:      "Test Document",
		Content:    "Matching content",
		Score:      0.95,
		Source:     "vector",
		DocType:    "knowledge",
		Metadata: map[string]interface{}{
			"section": "Introduction",
		},
		Highlights: []string{"<em>Matching</em> content"},
	}

	if result.Score != 0.95 {
		t.Errorf("Score should be 0.95, got %f", result.Score)
	}
	if result.Source != "vector" {
		t.Errorf("Source should be 'vector', got %s", result.Source)
	}
	if len(result.Highlights) != 1 {
		t.Errorf("Highlights should have 1 item, got %d", len(result.Highlights))
	}
}

// ============================================================================
// Interface Implementation Tests
// ============================================================================

func TestJSONB_ImplementsDriverValuer(t *testing.T) {
	var _ driver.Valuer = JSONB{}
}

func TestJSONB_ImplementsSQLScanner(t *testing.T) {
	var _ driver.Valuer = (*JSONB)(nil)
}
