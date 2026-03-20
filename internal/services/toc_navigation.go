package services

import (
	"context"
	"fmt"
	"strings"

	"github.com/jizhuozhi/knowledge/internal/config"
	"github.com/jizhuozhi/knowledge/internal/models"

	"gorm.io/gorm"
)

// TOCNavigationService TOC 导航服务 - 通过文档目录结构快速定位章节
type TOCNavigationService struct {
	db  *gorm.DB
	cfg *config.Config
}

// NewTOCNavigationService 创建 TOC 导航服务
func NewTOCNavigationService(db *gorm.DB, cfg *config.Config) *TOCNavigationService {
	return &TOCNavigationService{
		db:  db,
		cfg: cfg,
	}
}

// DocumentMatch 文档匹配结果
type DocumentMatch struct {
	DocumentID string          `json:"document_id"`
	Title      string          `json:"title"`
	DocType    string          `json:"doc_type"`
	Sections   []SectionMatch  `json:"sections"`
}

// SectionMatch 章节匹配结果
type SectionMatch struct {
	Title    string `json:"title"`
	Path     string `json:"path"`     // 完整路径 (h1 > h2 > h3)
	Level    int    `json:"level"`
	ChunkIDs []int  `json:"chunk_ids"` // 该章节及其子章节的所有 chunks
}

// DiscoverDocuments 文档发现 - 通过 TOC 标题索引查找包含相关章节的文档
func (s *TOCNavigationService) DiscoverDocuments(
	ctx context.Context,
	query string,
	keywords []string,
	kbID string,
	tenantID int64,
) ([]DocumentMatch, error) {
	if len(keywords) == 0 {
		return []DocumentMatch{}, nil
	}
	
	// 使用第一个关键词作为主要搜索词
	searchKeyword := keywords[0]
	
	// 在 DocumentTOCIndex 表中搜索匹配的标题
	var tocMatches []models.DocumentTOCIndex
	err := s.db.Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID).
		Where("title ILIKE ?", "%"+searchKeyword+"%"). // PostgreSQL 不区分大小写
		Order("level ASC, position ASC").               // 优先返回高级别标题
		Limit(50).
		Find(&tocMatches).Error
	
	if err != nil {
		return nil, fmt.Errorf("查询 TOC 索引失败: %w", err)
	}
	
	if len(tocMatches) == 0 {
		return []DocumentMatch{}, nil
	}
	
	// 按文档分组
	docMap := make(map[string]*DocumentMatch)
	for _, match := range tocMatches {
		if _, exists := docMap[match.DocumentID]; !exists {
			// 加载文档基本信息
			var doc models.Document
			err := s.db.Select("id, title, doc_type").
				Where("id = ?", match.DocumentID).
				First(&doc).Error
			
			if err != nil {
				continue // 跳过无效文档
			}
			
			docMap[match.DocumentID] = &DocumentMatch{
				DocumentID: match.DocumentID,
				Title:      doc.Title,
				DocType:    doc.DocType,
				Sections:   []SectionMatch{},
			}
		}
		
		// 解析 chunk_ids
		chunkIDs := s.parseChunkIDs(match.ChunkIDs)
		
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
	
	fmt.Printf("[TOCNavigation] 找到 %d 个匹配文档, %d 个章节\n", 
		len(results), len(tocMatches))
	
	return results, nil
}

// getChildrenChunkIDs 递归获取所有子章节的 chunk IDs
func (s *TOCNavigationService) getChildrenChunkIDs(parentID string) []int {
	var children []models.DocumentTOCIndex
	s.db.Where("parent_id = ?", parentID).Find(&children)
	
	var allChunkIDs []int
	for _, child := range children {
		// 当前子节点的 chunks
		chunkIDs := s.parseChunkIDs(child.ChunkIDs)
		allChunkIDs = append(allChunkIDs, chunkIDs...)
		
		// 递归获取孙节点
		grandChildIDs := s.getChildrenChunkIDs(child.ID)
		allChunkIDs = append(allChunkIDs, grandChildIDs...)
	}
	
	return allChunkIDs
}

// parseChunkIDs 解析 JSONB 中的 chunk IDs
func (s *TOCNavigationService) parseChunkIDs(chunkIDsJSON models.JSONB) []int {
	var chunkIDs []int
	
	if chunkIDsJSON == nil {
		return chunkIDs
	}
	
	if ids, ok := chunkIDsJSON["ids"].([]interface{}); ok {
		for _, id := range ids {
			switch v := id.(type) {
			case float64:
				chunkIDs = append(chunkIDs, int(v))
			case int:
				chunkIDs = append(chunkIDs, v)
			case int64:
				chunkIDs = append(chunkIDs, int(v))
			}
		}
	}
	
	return chunkIDs
}

// ToRecallResults 转换为 RecallResult (用于与其他通道融合)
func (s *TOCNavigationService) ToRecallResults(matches []DocumentMatch) []*RecallResult {
	results := []*RecallResult{}
	
	for _, doc := range matches {
		for _, section := range doc.Sections {
			// 为该章节的每个 chunk 创建 RecallResult
			for rank, chunkIndex := range section.ChunkIDs {
				results = append(results, &RecallResult{
					ChunkID:    fmt.Sprintf("%s_%d", doc.DocumentID, chunkIndex),
					DocumentID: doc.DocumentID,
					Title:      section.Title,
					Score:      1.0, // TOC 召回的分数统一为 1.0 (精确匹配)
					Rank:       rank + 1,
					Channel:    "toc_navigation",
					Metadata: map[string]interface{}{
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

// UpdateTOCIndex 更新文档的 TOC 索引（在 Chunk 创建完成后调用）
func (s *TOCNavigationService) UpdateTOCIndex(
	ctx context.Context,
	doc *models.Document,
	tocNodes []models.TOCNode,
) error {
	// 1. 删除旧的 TOC 索引
	if err := s.db.Where("document_id = ?", doc.ID).
		Delete(&models.DocumentTOCIndex{}).Error; err != nil {
		return fmt.Errorf("删除旧 TOC 索引失败: %w", err)
	}
	
	// 2. 递归插入新的 TOC 索引
	if err := s.insertTOCNodes(doc, tocNodes, nil); err != nil {
		return fmt.Errorf("插入 TOC 索引失败: %w", err)
	}
	
	fmt.Printf("[TOCNavigation] 更新 TOC 索引成功 - 文档: %s, 节点数: %d\n", 
		doc.ID, s.countNodes(tocNodes))
	
	return nil
}

// insertTOCNodes 递归插入 TOC 节点
func (s *TOCNavigationService) insertTOCNodes(
	doc *models.Document,
	nodes []models.TOCNode,
	parentID *string,
) error {
	for _, node := range nodes {
		// 创建索引记录
		record := &models.DocumentTOCIndex{
			TenantModel:     doc.TenantModel,
			KnowledgeBaseID: doc.KnowledgeBaseID,
			DocumentID:      doc.ID,
			Title:           node.Title,
			Level:           node.Level,
			Path:            node.Path,
			ChunkIDs:        models.JSONB{"ids": node.ChunkIDs},
			ParentID:        parentID,
			Position:        node.Position,
		}
		
		if err := s.db.Create(record).Error; err != nil {
			return err
		}
		
		// 递归插入子节点
		if len(node.Children) > 0 {
			recordID := record.ID
			if err := s.insertTOCNodes(doc, node.Children, &recordID); err != nil {
				return err
			}
		}
	}
	
	return nil
}

// countNodes 递归计数节点数
func (s *TOCNavigationService) countNodes(nodes []models.TOCNode) int {
	count := len(nodes)
	for _, node := range nodes {
		count += s.countNodes(node.Children)
	}
	return count
}

// GetDocumentTOC 获取文档的完整 TOC 树（用于前端展示）
func (s *TOCNavigationService) GetDocumentTOC(
	ctx context.Context,
	docID string,
	tenantID int64,
) ([]models.TOCNode, error) {
	var doc models.Document
	if err := s.db.Where("id = ? AND tenant_id = ?", docID, tenantID).
		First(&doc).Error; err != nil {
		return nil, fmt.Errorf("文档不存在: %w", err)
	}
	
	// 从 TOCStructure 中解析
	if doc.TOCStructure == nil {
		return []models.TOCNode{}, nil
	}
	
	if nodesData, ok := doc.TOCStructure["nodes"].([]interface{}); ok {
		// 转换为 TOCNode 数组
		nodes := make([]models.TOCNode, 0, len(nodesData))
		// TODO: 实现类型转换逻辑
		_ = nodesData
		return nodes, nil
	}
	
	return []models.TOCNode{}, nil
}

// FormatSections 格式化章节列表（用于 LLM Prompt）
func FormatSections(sections []SectionMatch) string {
	var builder strings.Builder
	
	for _, section := range sections {
		builder.WriteString(fmt.Sprintf("- %s (Level %d)\n", section.Title, section.Level))
		if section.Path != "" {
			builder.WriteString(fmt.Sprintf("  路径: %s\n", section.Path))
		}
		builder.WriteString(fmt.Sprintf("  Chunks: %v\n", section.ChunkIDs))
	}
	
	return builder.String()
}
