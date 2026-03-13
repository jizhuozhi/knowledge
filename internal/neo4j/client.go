package neo4j

import (
	"context"
	"fmt"

	"github.com/jizhuozhi/knowledge/internal/config"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// Client wraps Neo4j client
type Client struct {
	driver neo4j.DriverWithContext
}

// EntityType represents entity node types
type EntityType string

const (
	EntityTypeDocument  EntityType = "Document"
	EntityTypeSection   EntityType = "Section"
	EntityTypeEntity    EntityType = "Entity"
	EntityTypeConcept   EntityType = "Concept"
	EntityTypeComponent EntityType = "Component"
	EntityTypeAPI       EntityType = "API"
	EntityTypePerson    EntityType = "Person"
)

// RelationType represents relation types
type RelationType string

const (
	RelationContains    RelationType = "CONTAINS"
	RelationReferences  RelationType = "REFERENCES"
	RelationDependsOn   RelationType = "DEPENDS_ON"
	RelationImplements  RelationType = "IMPLEMENTS"
	RelationCauses      RelationType = "CAUSES"
	RelationRelatedTo   RelationType = "RELATED_TO"
	RelationAuthoredBy  RelationType = "AUTHORED_BY"
)

// Entity represents a graph entity
type Entity struct {
	ID               string                 `json:"id"`
	KnowledgeBaseID  string                 `json:"knowledge_base_id"`
	Type             EntityType             `json:"type"`
	Name             string                 `json:"name"`
	DocumentID       string                 `json:"document_id,omitempty"`
	Properties       map[string]interface{} `json:"properties,omitempty"`
}

// Relation represents a relation between entities
type Relation struct {
	ID               string                 `json:"id"`
	KnowledgeBaseID  string                 `json:"knowledge_base_id"`
	SourceID         string                 `json:"source_id"`
	TargetID         string                 `json:"target_id"`
	Type             RelationType           `json:"type"`
	Weight           float64                `json:"weight"`
	Properties       map[string]interface{} `json:"properties,omitempty"`
}

// NewClient creates a new Neo4j client
func NewClient(cfg config.Neo4jConfig) (*Client, error) {
	driver, err := neo4j.NewDriverWithContext(
		cfg.URI,
		neo4j.BasicAuth(cfg.User, cfg.Password, ""),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create neo4j driver: %w", err)
	}

	// Test connection
	ctx := context.Background()
	if err := driver.VerifyConnectivity(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect to neo4j: %w", err)
	}

	return &Client{driver: driver}, nil
}

// Close closes the Neo4j driver
func (c *Client) Close() error {
	return c.driver.Close(context.Background())
}

// CreateEntity creates an entity node
func (c *Client) CreateEntity(ctx context.Context, entity *Entity) error {
	session := c.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		query := `
		MERGE (n:%s {id: $id})
		SET n.knowledge_base_id = $knowledge_base_id,
		    n.name = $name,
		    n.document_id = $document_id,
		    n.created_at = datetime()
		SET n += $properties
		RETURN n
		`

		result, err := tx.Run(ctx, fmt.Sprintf(query, entity.Type), map[string]interface{}{
			"id":                entity.ID,
			"knowledge_base_id": entity.KnowledgeBaseID,
			"name":              entity.Name,
			"document_id":       entity.DocumentID,
			"properties":        entity.Properties,
		})
		if err != nil {
			return nil, err
		}

		return result.Collect(ctx)
	})

	return err
}

// CreateRelation creates a relation between entities
func (c *Client) CreateRelation(ctx context.Context, relation *Relation) error {
	session := c.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		query := `
		MATCH (source {id: $source_id, knowledge_base_id: $knowledge_base_id})
		MATCH (target {id: $target_id, knowledge_base_id: $knowledge_base_id})
		MERGE (source)-[r:%s]->(target)
		SET r.weight = $weight,
		    r.created_at = datetime()
		SET r += $properties
		RETURN r
		`

		result, err := tx.Run(ctx, fmt.Sprintf(query, relation.Type), map[string]interface{}{
			"knowledge_base_id": relation.KnowledgeBaseID,
			"source_id":         relation.SourceID,
			"target_id":         relation.TargetID,
			"weight":            relation.Weight,
			"properties":        relation.Properties,
		})
		if err != nil {
			return nil, err
		}

		return result.Collect(ctx)
	})

	return err
}

// FindRelatedEntities finds entities related to a given entity
func (c *Client) FindRelatedEntities(ctx context.Context, knowledgeBaseID, entityID string, relationTypes []RelationType, depth int) ([]Entity, error) {
	session := c.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	result, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		query := `
		MATCH (start {id: $entity_id, knowledge_base_id: $knowledge_base_id})
		CALL apoc.path.subgraphAll(start, {
			relationshipFilter: $rel_filter,
			maxLevel: $depth
		})
		YIELD nodes
		UNWIND nodes as node
		RETURN node.id as id, node.knowledge_base_id as knowledge_base_id, 
		       labels(node)[0] as type, node.name as name,
		       node.document_id as document_id, node as properties
		`

		relFilter := ""
		for i, rt := range relationTypes {
			if i > 0 {
				relFilter += "|"
			}
			relFilter += string(rt)
		}
		if relFilter == "" {
			relFilter = "CONTAINS|REFERENCES|DEPENDS_ON|CAUSES|RELATED_TO"
		}

		result, err := tx.Run(ctx, query, map[string]interface{}{
			"knowledge_base_id": knowledgeBaseID,
			"entity_id":         entityID,
			"rel_filter":        relFilter,
			"depth":             depth,
		})
		if err != nil {
			return nil, err
		}

		return result.Collect(ctx)
	})

	if err != nil {
		return nil, err
	}

	records := result.([]*neo4j.Record)
	var entities []Entity
	for _, record := range records {
		props, _ := record.Get("properties")
		propsMap := props.(neo4j.Node).Props
		
		entity := Entity{
			ID:              record.Values[0].(string),
			KnowledgeBaseID: record.Values[1].(string),
			Type:            EntityType(record.Values[2].(string)),
			Name:            record.Values[3].(string),
			DocumentID:      record.Values[4].(string),
			Properties:      propsMap,
		}
		entities = append(entities, entity)
	}

	return entities, nil
}

// FindPath finds the shortest path between two entities
func (c *Client) FindPath(ctx context.Context, knowledgeBaseID, sourceID, targetID string) ([]Entity, error) {
	session := c.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	result, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		query := `
		MATCH (source {id: $source_id, knowledge_base_id: $knowledge_base_id})
		MATCH (target {id: $target_id, knowledge_base_id: $knowledge_base_id})
		MATCH path = shortestPath((source)-[*]-(target))
		UNWIND nodes(path) as node
		RETURN node.id as id, node.knowledge_base_id as knowledge_base_id,
		       labels(node)[0] as type, node.name as name,
		       node.document_id as document_id
		`

		result, err := tx.Run(ctx, query, map[string]interface{}{
			"knowledge_base_id": knowledgeBaseID,
			"source_id":         sourceID,
			"target_id":         targetID,
		})
		if err != nil {
			return nil, err
		}

		return result.Collect(ctx)
	})

	if err != nil {
		return nil, err
	}

	records := result.([]*neo4j.Record)
	var entities []Entity
	for _, record := range records {
		entity := Entity{
			ID:              record.Values[0].(string),
			KnowledgeBaseID: record.Values[1].(string),
			Type:            EntityType(record.Values[2].(string)),
			Name:            record.Values[3].(string),
			DocumentID:      record.Values[4].(string),
		}
		entities = append(entities, entity)
	}

	return entities, nil
}

// SearchByEntityName searches entities by name within a knowledge base
func (c *Client) SearchByEntityName(ctx context.Context, knowledgeBaseID, name string, limit int) ([]Entity, error) {
	session := c.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	result, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		query := `
		MATCH (n {knowledge_base_id: $knowledge_base_id})
		WHERE n.name CONTAINS $name
		RETURN n.id as id, n.knowledge_base_id as knowledge_base_id,
		       labels(n)[0] as type, n.name as name,
		       n.document_id as document_id
		LIMIT $limit
		`

		result, err := tx.Run(ctx, query, map[string]interface{}{
			"knowledge_base_id": knowledgeBaseID,
			"name":              name,
			"limit":             limit,
		})
		if err != nil {
			return nil, err
		}

		return result.Collect(ctx)
	})

	if err != nil {
		return nil, err
	}

	records := result.([]*neo4j.Record)
	var entities []Entity
	for _, record := range records {
		entity := Entity{
			ID:              record.Values[0].(string),
			KnowledgeBaseID: record.Values[1].(string),
			Type:            EntityType(record.Values[2].(string)),
			Name:            record.Values[3].(string),
			DocumentID:      record.Values[4].(string),
		}
		entities = append(entities, entity)
	}

	return entities, nil
}

// DeleteDocumentEntities deletes all entities associated with a document in a knowledge base
func (c *Client) DeleteDocumentEntities(ctx context.Context, knowledgeBaseID, documentID string) error {
	session := c.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		query := `
		MATCH (n {knowledge_base_id: $knowledge_base_id, document_id: $document_id})
		DETACH DELETE n
		`

		result, err := tx.Run(ctx, query, map[string]interface{}{
			"knowledge_base_id": knowledgeBaseID,
			"document_id":       documentID,
		})
		if err != nil {
			return nil, err
		}

		return result.Collect(ctx)
	})

	return err
}

// GetEntityCount returns the count of entities for a knowledge base
func (c *Client) GetEntityCount(ctx context.Context, knowledgeBaseID string) (int64, error) {
	session := c.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	result, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		query := `
		MATCH (n {knowledge_base_id: $knowledge_base_id})
		RETURN count(n) as count
		`

		result, err := tx.Run(ctx, query, map[string]interface{}{
			"knowledge_base_id": knowledgeBaseID,
		})
		if err != nil {
			return nil, err
		}

		record, err := result.Single(ctx)
		if err != nil {
			return nil, err
		}

		return record.Values[0].(int64), nil
	})

	if err != nil {
		return 0, err
	}

	return result.(int64), nil
}

// CreateConstraints creates unique constraints for entity types
func (c *Client) CreateConstraints(ctx context.Context) error {
	session := c.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		// Create unique constraint on id for each entity type
		entityTypes := []EntityType{
			EntityTypeDocument, EntityTypeSection, EntityTypeEntity,
			EntityTypeConcept, EntityTypeComponent, EntityTypeAPI, EntityTypePerson,
		}

		for _, et := range entityTypes {
			query := fmt.Sprintf(`
			CREATE CONSTRAINT IF NOT EXISTS FOR (n:%s)
			REQUIRE n.id IS UNIQUE
			`, et)
			
			if _, err := tx.Run(ctx, query, nil); err != nil {
				return nil, err
			}
		}

		return nil, nil
	})

	return err
}
