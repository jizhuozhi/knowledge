package services

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jizhuozhi/knowledge/internal/config"
	"github.com/jizhuozhi/knowledge/internal/models"
	"github.com/jizhuozhi/knowledge/internal/neo4j"
	"gorm.io/gorm"
)

// GraphService handles graph-related operations
type GraphService struct {
	db           *gorm.DB
	config       *config.Config
	neo4jCli     *neo4j.Client
	llm          *EmbeddingService
	usageTracker *LLMUsageTracker
}

// NewGraphService creates a new graph service
func NewGraphService(db *gorm.DB, cfg *config.Config) *GraphService {
	return &GraphService{
		db:           db,
		config:       cfg,
		llm:          NewEmbeddingService(cfg),
		usageTracker: NewLLMUsageTracker(db, cfg),
	}
}

// SetNeo4jClient sets the Neo4j client
func (s *GraphService) SetNeo4jClient(client *neo4j.Client) {
	s.neo4jCli = client
}

// ExtractAndIndex extracts entities and relations from document
func (s *GraphService) ExtractAndIndex(ctx context.Context, doc *models.Document) error {
	if s.neo4jCli == nil {
		return fmt.Errorf("neo4j client not initialized")
	}

	// 1. Extract entities using LLM
	entities, err := s.extractEntities(ctx, doc)
	if err != nil {
		return fmt.Errorf("failed to extract entities: %w", err)
	}

	// 2. Extract relations using LLM — pass entity IDs explicitly so LLM uses matching IDs
	relations, err := s.extractRelations(ctx, doc, entities)
	if err != nil {
		return fmt.Errorf("failed to extract relations: %w", err)
	}

	// 3. Save entities to PostgreSQL and build ID mapping (LLM temp ID -> DB int64 ID string)
	idMapping, err := s.saveEntities(ctx, entities, doc.TenantID, doc.KnowledgeBaseID, doc.ID)
	if err != nil {
		return fmt.Errorf("failed to save entities: %w", err)
	}

	// 4. Also build a name->DB ID mapping as fallback (LLM may use entity names instead of IDs)
	nameMapping := make(map[string]string)
	for i, e := range entities {
		dbID := idMapping[e.ID]
		if dbID != "" {
			nameMapping[e.Name] = dbID
			// Also map by index-based ID patterns the LLM might use
			nameMapping[fmt.Sprintf("entity_%d", i)] = dbID
		}
	}

	// 5. Update relation IDs using the mapping and save to PostgreSQL
	if err := s.saveRelations(ctx, relations, idMapping, nameMapping, doc.TenantID, doc.KnowledgeBaseID); err != nil {
		return fmt.Errorf("failed to save relations: %w", err)
	}

	// 6. Index to Neo4j
	kbIDStr := doc.KnowledgeBaseID
	if err := s.indexToNeo4j(ctx, entities, relations, kbIDStr); err != nil {
		return fmt.Errorf("failed to index to neo4j: %w", err)
	}

	return nil
}

// Entity represents an extracted entity
type Entity struct {
	ID         string                 `json:"id"`
	Name       string                 `json:"name"`
	Type       string                 `json:"type"`
	Properties map[string]interface{} `json:"properties,omitempty"`
}

// Relation represents an extracted relation
type Relation struct {
	ID         string                 `json:"id"`
	SourceID   string                 `json:"source_id"`
	TargetID   string                 `json:"target_id"`
	Type       string                 `json:"type"`
	Weight     float64                `json:"weight"`
	Properties map[string]interface{} `json:"properties,omitempty"`
}

// extractEntities extracts entities from document using LLM
func (s *GraphService) extractEntities(ctx context.Context, doc *models.Document) ([]Entity, error) {
	content := doc.Content
	if len(content) > 5000 {
		content = content[:5000]
	}

	prompt := fmt.Sprintf(`Extract entities from the following document content.

Document Title: %s
Content:
%s

Return a JSON array of entities. IMPORTANT: use "entity_0", "entity_1", etc. as IDs.
[
  {
    "id": "entity_0",
    "name": "entity name",
    "type": "person|concept|service|component|api|product|organization",
    "properties": {}
  }
]

Entity types:
- person: people mentioned
- concept: technical concepts, methodologies
- service: software services, APIs
- component: system components, modules
- api: specific API endpoints
- product: products, tools
- organization: teams, companies

Return only the JSON array.`, doc.Title, content)

	response, usage, err := s.llm.ChatCompletion(ctx, prompt)
	if err != nil {
		return nil, err
	}

	// Record usage
	if s.usageTracker != nil {
		docID := doc.ID
		kbID := doc.KnowledgeBaseID
		s.usageTracker.RecordUsage(ctx, doc.TenantID, &docID, &kbID,
			"graph", "extractEntities", s.config.LLM.ChatModel, "chat",
			usage, 0, "")
	}

	var entities []Entity
	if err := json.Unmarshal([]byte(response), &entities); err != nil {
		return nil, fmt.Errorf("failed to parse entities: %w", err)
	}

	// Ensure IDs are unique and follow entity_N pattern
	seenIDs := make(map[string]bool)
	for i := range entities {
		if entities[i].ID == "" || seenIDs[entities[i].ID] {
			entities[i].ID = fmt.Sprintf("entity_%d", i)
		}
		seenIDs[entities[i].ID] = true
	}

	return entities, nil
}

// extractRelations extracts relations between entities
func (s *GraphService) extractRelations(ctx context.Context, doc *models.Document, entities []Entity) ([]Relation, error) {
	if len(entities) < 2 {
		return nil, nil
	}

	content := doc.Content
	if len(content) > 5000 {
		content = content[:5000]
	}

	// Build entity list with explicit IDs so LLM uses the same IDs in relations
	entityList := ""
	for _, e := range entities {
		entityList += fmt.Sprintf("- ID: %s, Name: %s (%s)\n", e.ID, e.Name, e.Type)
	}

	prompt := fmt.Sprintf(`Extract relations between the following entities from this document.

Document Title: %s
Content:
%s

Entities (use the exact IDs below for source_id and target_id):
%s

Return a JSON array of relations. IMPORTANT: source_id and target_id MUST be one of the entity IDs listed above (e.g. "entity_0", "entity_1").
[
  {
    "id": "rel_0",
    "source_id": "entity_0",
    "target_id": "entity_1",
    "type": "references|depends_on|causes|implements|contains|related_to",
    "weight": 1.0,
    "properties": {}
  }
]

Return only the JSON array.`, doc.Title, content, entityList)

	response, usage, err := s.llm.ChatCompletion(ctx, prompt)
	if err != nil {
		return nil, err
	}

	// Record usage
	if s.usageTracker != nil {
		docID := doc.ID
		kbID := doc.KnowledgeBaseID
		s.usageTracker.RecordUsage(ctx, doc.TenantID, &docID, &kbID,
			"graph", "extractRelations", s.config.LLM.ChatModel, "chat",
			usage, 0, "")
	}

	var relations []Relation
	if err := json.Unmarshal([]byte(response), &relations); err != nil {
		return nil, fmt.Errorf("failed to parse relations: %w", err)
	}

	return relations, nil
}

// saveEntities saves entities to PostgreSQL and returns ID mapping (LLM ID -> DB ID as string)
func (s *GraphService) saveEntities(ctx context.Context, entities []Entity, tenantID string, knowledgeBaseID string, documentID string) (map[string]string, error) {
	idMapping := make(map[string]string)

	for i := range entities {
		originalID := entities[i].ID

		graphEntity := models.GraphEntity{
			TenantModel: models.TenantModel{
				TenantID: tenantID,
			},
			KnowledgeBaseID: knowledgeBaseID,
			DocumentID:      documentID,
			Name:            entities[i].Name,
			Type:            entities[i].Type,
			Properties:      entities[i].Properties,
		}

		if err := s.db.Create(&graphEntity).Error; err != nil {
			return nil, err
		}

		entities[i].ID = graphEntity.ID
		idMapping[originalID] = graphEntity.ID
	}

	return idMapping, nil
}

// saveRelations saves relations to PostgreSQL using ID mapping
// Uses both idMapping (LLM ID -> DB ID) and nameMapping (entity name -> DB ID) as fallback
func (s *GraphService) saveRelations(ctx context.Context, relations []Relation, idMapping map[string]string, nameMapping map[string]string, tenantID string, knowledgeBaseID string) error {
	savedCount := 0
	for i := range relations {
		// Try to resolve source ID: first by LLM ID mapping, then by name mapping
		sourceDBStr := resolveID(relations[i].SourceID, idMapping, nameMapping)
		if sourceDBStr == "" {
			fmt.Printf("Warning: skipping relation - source_id %s not found\n", relations[i].SourceID)
			continue
		}

		targetDBStr := resolveID(relations[i].TargetID, idMapping, nameMapping)
		if targetDBStr == "" {
			fmt.Printf("Warning: skipping relation - target_id %s not found\n", relations[i].TargetID)
			continue
		}

		graphRelation := models.GraphRelation{
			TenantModel: models.TenantModel{
				TenantID: tenantID,
			},
			KnowledgeBaseID: knowledgeBaseID,
			SourceID:        sourceDBStr,
			TargetID:        targetDBStr,
			Type:            relations[i].Type,
			Weight:          relations[i].Weight,
			Properties:      relations[i].Properties,
		}

		if err := s.db.Create(&graphRelation).Error; err != nil {
			return err
		}
		savedCount++

		// Update relation with DB IDs for Neo4j indexing
		relations[i].ID = graphRelation.ID
		relations[i].SourceID = sourceDBStr
		relations[i].TargetID = targetDBStr
	}

	if len(relations) > 0 {
		fmt.Printf("Graph: saved %d/%d relations\n", savedCount, len(relations))
	}

	return nil
}

// resolveID tries to resolve an LLM-generated ID to a DB ID string
func resolveID(llmID string, idMapping, nameMapping map[string]string) string {
	// Direct match in ID mapping
	if dbID, ok := idMapping[llmID]; ok {
		return dbID
	}
	// Fallback: try name mapping (LLM may use entity names as IDs)
	if dbID, ok := nameMapping[llmID]; ok {
		return dbID
	}
	return ""
}

// indexToNeo4j indexes entities and relations to Neo4j
func (s *GraphService) indexToNeo4j(ctx context.Context, entities []Entity, relations []Relation, knowledgeBaseID string) error {
	for _, entity := range entities {
		neo4jEntity := &neo4j.Entity{
			ID:              entity.ID,
			KnowledgeBaseID: knowledgeBaseID,
			Type:            neo4j.EntityType(entity.Type),
			Name:            entity.Name,
			Properties:      entity.Properties,
		}

		if err := s.neo4jCli.CreateEntity(ctx, neo4jEntity); err != nil {
			fmt.Printf("Warning: failed to create entity in Neo4j: %v\n", err)
		}
	}

	for _, relation := range relations {
		if relation.SourceID == "" || relation.TargetID == "" {
			continue
		}
		neo4jRelation := &neo4j.Relation{
			ID:              relation.ID,
			KnowledgeBaseID: knowledgeBaseID,
			SourceID:        relation.SourceID,
			TargetID:        relation.TargetID,
			Type:            neo4j.RelationType(relation.Type),
			Weight:          relation.Weight,
			Properties:      relation.Properties,
		}

		if err := s.neo4jCli.CreateRelation(ctx, neo4jRelation); err != nil {
			fmt.Printf("Warning: failed to create relation in Neo4j: %v\n", err)
		}
	}

	return nil
}

// SearchRelatedEntities searches for related entities within a knowledge base
func (s *GraphService) SearchRelatedEntities(ctx context.Context, knowledgeBaseID string, entityName string, depth int) ([]models.GraphEntity, error) {
	entities, err := s.neo4jCli.SearchByEntityName(ctx, knowledgeBaseID, entityName, 10)
	if err != nil {
		return nil, err
	}

	var result []models.GraphEntity
	for _, e := range entities {
		entity := models.GraphEntity{
			Name: e.Name,
			Type: string(e.Type),
		}

		related, err := s.neo4jCli.FindRelatedEntities(ctx, knowledgeBaseID, e.ID, nil, depth)
		if err == nil {
			entity.Properties = models.JSONB{
				"related_count": len(related),
			}
		}

		result = append(result, entity)
	}

	return result, nil
}

// FindKnowledgePath finds the knowledge path between two concepts
func (s *GraphService) FindKnowledgePath(ctx context.Context, knowledgeBaseID string, source, target string) ([]models.GraphEntity, error) {
	sourceEntities, err := s.neo4jCli.SearchByEntityName(ctx, knowledgeBaseID, source, 1)
	if err != nil || len(sourceEntities) == 0 {
		return nil, fmt.Errorf("source entity not found")
	}

	targetEntities, err := s.neo4jCli.SearchByEntityName(ctx, knowledgeBaseID, target, 1)
	if err != nil || len(targetEntities) == 0 {
		return nil, fmt.Errorf("target entity not found")
	}

	path, err := s.neo4jCli.FindPath(ctx, knowledgeBaseID, sourceEntities[0].ID, targetEntities[0].ID)
	if err != nil {
		return nil, err
	}

	var result []models.GraphEntity
	for _, e := range path {
		result = append(result, models.GraphEntity{
			Name: e.Name,
			Type: string(e.Type),
		})
	}

	return result, nil
}
