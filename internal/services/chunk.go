package services

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/jizhuozhi/knowledge/internal/config"
	"github.com/jizhuozhi/knowledge/internal/models"
	"gorm.io/gorm"
)

// ChunkService handles document chunking
type ChunkService struct {
	db     *gorm.DB
	config *config.Config
}

// NewChunkService creates a new chunk service
func NewChunkService(db *gorm.DB, cfg *config.Config) *ChunkService {
	return &ChunkService{
		db:     db,
		config: cfg,
	}
}

// ChunkDocument chunks a document according to strategy.
// It first splits the content into Markdown-aware blocks (tables, code blocks,
// lists, headings, paragraphs) that are never broken mid-structure, then
// applies the chosen chunk strategy on those blocks.
func (s *ChunkService) ChunkDocument(ctx context.Context, doc *models.Document, strategy *IndexStrategy) ([]models.Chunk, error) {
	// Check if chunking is needed
	if strategy.ChunkStrategy == "none" || len(doc.Content) < strategy.ChunkSize {
		chunk := models.Chunk{
			TenantModel: models.TenantModel{
				TenantID: doc.TenantID,
			},
			DocumentID:    doc.ID,
			Content:       doc.Content,
			ChunkIndex:    0,
			StartPosition: 0,
			EndPosition:   len(doc.Content),
			ChunkType:     "full",
		}
		if err := s.db.Create(&chunk).Error; err != nil {
			return nil, err
		}
		return []models.Chunk{chunk}, nil
	}

	// Phase 1: Parse content into Markdown structural blocks
	blocks := s.parseMarkdownBlocks(doc.Content)

	// Phase 2: Apply special processing to blocks (table_aware, code-aware, etc.)
	blocks = s.applySpecialProcessing(blocks, strategy)

	// Phase 3: Apply chunk strategy
	var chunks []models.Chunk
	var err error

	switch strategy.ChunkStrategy {
	case "semantic":
		chunks, err = s.structureAwareSemanticChunk(doc, blocks, strategy)
	case "section":
		chunks, err = s.structureAwareSectionChunk(doc, blocks, strategy)
	case "sliding":
		chunks, err = s.slidingChunk(doc, strategy)
	case "parent_child":
		chunks, err = s.parentChildChunk(doc, strategy)
	default:
		chunks, err = s.structureAwareSemanticChunk(doc, blocks, strategy)
	}

	if err != nil {
		return nil, err
	}

	// Save chunks to database
	if err := s.db.Create(&chunks).Error; err != nil {
		return nil, err
	}

	// ✅ 同步 TOC 索引到 DocumentTOCIndex 表
	if doc.TOCStructure != nil && len(doc.TOCStructure) > 0 {
		if err := s.syncTOCIndex(ctx, doc); err != nil {
			fmt.Printf("[ChunkService] 同步 TOC 索引失败: %v\n", err)
			// 非关键错误，不阻塞 chunking
		}
	}

	return chunks, nil
}

// syncTOCIndex 同步文档的 TOC 索引到 DocumentTOCIndex 表
func (s *ChunkService) syncTOCIndex(ctx context.Context, doc *models.Document) error {
	// 解析 TOC 结构
	tocNodes := []models.TOCNode{}
	if nodesData, ok := doc.TOCStructure["nodes"]; ok {
		tocJSON, err := json.Marshal(nodesData)
		if err != nil {
			return fmt.Errorf("序列化 TOC 失败: %w", err)
		}
		if err := json.Unmarshal(tocJSON, &tocNodes); err != nil {
			return fmt.Errorf("反序列化 TOC 失败: %w", err)
		}
	}

	if len(tocNodes) == 0 {
		return nil
	}

	// 创建 TOC Navigation Service
	tocService := NewTOCNavigationService(s.db, s.config)
	
	// 调用更新方法
	return tocService.UpdateTOCIndex(ctx, doc, tocNodes)
}

// ============================================================================
// Markdown Block Parser
// ============================================================================

// mdBlockType represents the type of a markdown structural block
type mdBlockType string

const (
	mdBlockParagraph  mdBlockType = "paragraph"
	mdBlockHeading    mdBlockType = "heading"
	mdBlockCodeBlock  mdBlockType = "code_block"
	mdBlockTable      mdBlockType = "table"
	mdBlockList       mdBlockType = "list"
	mdBlockBlockquote mdBlockType = "blockquote"
	mdBlockHR         mdBlockType = "hr"
)

// mdBlock represents a single structural block in a Markdown document
type mdBlock struct {
	Type     mdBlockType
	Content  string
	Start    int // byte offset in original content
	End      int
	Metadata map[string]string // e.g. heading level, code language, table headers
}

// parseMarkdownBlocks splits Markdown content into structural blocks.
// Blocks are: headings, code fences, tables, lists, blockquotes, HRs, paragraphs.
// The key guarantee: tables, code blocks, and list items are never split mid-structure.
func (s *ChunkService) parseMarkdownBlocks(content string) []mdBlock {
	var blocks []mdBlock
	lines := strings.Split(content, "\n")
	pos := 0 // byte position tracking

	i := 0
	for i < len(lines) {
		line := lines[i]
		lineStart := pos
		lineLen := len(line)
		if i < len(lines)-1 {
			lineLen++ // account for \n
		}

		trimmed := strings.TrimSpace(line)

		// --- Blank line: skip (advances pos) ---
		if trimmed == "" {
			pos += lineLen
			i++
			continue
		}

		// --- Heading: # ... ---
		if isMarkdownHeading(trimmed) {
			level := "1"
			for j := 0; j < len(trimmed) && trimmed[j] == '#'; j++ {
				level = fmt.Sprintf("%d", j+1)
			}
			blocks = append(blocks, mdBlock{
				Type:     mdBlockHeading,
				Content:  line,
				Start:    lineStart,
				End:      lineStart + lineLen,
				Metadata: map[string]string{"level": level},
			})
			pos += lineLen
			i++
			continue
		}

		// --- HR: ---, ***, ___ ---
		if isMarkdownHR(trimmed) {
			blocks = append(blocks, mdBlock{
				Type:    mdBlockHR,
				Content: line,
				Start:   lineStart,
				End:     lineStart + lineLen,
			})
			pos += lineLen
			i++
			continue
		}

		// --- Fenced code block: ``` or ~~~ ---
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fence := trimmed[:3]
			lang := strings.TrimSpace(trimmed[3:])
			var codeLines []string
			codeLines = append(codeLines, line)
			pos += lineLen
			i++
			closed := false
			for i < len(lines) {
				cl := lines[i]
				clLen := len(cl)
				if i < len(lines)-1 {
					clLen++
				}
				codeLines = append(codeLines, cl)
				pos += clLen
				i++
				if strings.HasPrefix(strings.TrimSpace(cl), fence) && len(codeLines) > 1 {
					closed = true
					break
				}
			}
			_ = closed
			blocks = append(blocks, mdBlock{
				Type:     mdBlockCodeBlock,
				Content:  strings.Join(codeLines, "\n"),
				Start:    lineStart,
				End:      pos,
				Metadata: map[string]string{"language": lang},
			})
			continue
		}

		// --- Table: starts with | ---
		if isMarkdownTableRow(trimmed) {
			var tableLines []string
			tableStart := lineStart
			for i < len(lines) {
				tl := strings.TrimSpace(lines[i])
				if !isMarkdownTableRow(tl) && tl != "" {
					break
				}
				if tl == "" {
					// blank line ends table
					break
				}
				tableLines = append(tableLines, lines[i])
				tlLen := len(lines[i])
				if i < len(lines)-1 {
					tlLen++
				}
				pos += tlLen
				i++
			}
			// Extract headers from first row
			headers := ""
			if len(tableLines) > 0 {
				headers = strings.TrimSpace(tableLines[0])
			}
			rowCount := len(tableLines)
			// Subtract separator row
			if rowCount > 1 && isMarkdownTableSeparator(strings.TrimSpace(tableLines[1])) {
				rowCount -= 1 // don't count separator
			}
			blocks = append(blocks, mdBlock{
				Type:    mdBlockTable,
				Content: strings.Join(tableLines, "\n"),
				Start:   tableStart,
				End:     pos,
				Metadata: map[string]string{
					"headers":   headers,
					"row_count": fmt.Sprintf("%d", rowCount),
				},
			})
			continue
		}

		// --- List: starts with -, *, +, or digit. ---
		if isMarkdownListItem(trimmed) {
			var listLines []string
			listStart := lineStart
			for i < len(lines) {
				ll := lines[i]
				llTrimmed := strings.TrimSpace(ll)
				// Continue list if: it's a list item, or indented continuation, or blank line followed by list item
				if isMarkdownListItem(llTrimmed) || (len(ll) > 0 && (ll[0] == ' ' || ll[0] == '\t') && llTrimmed != "") {
					listLines = append(listLines, ll)
					llLen := len(ll)
					if i < len(lines)-1 {
						llLen++
					}
					pos += llLen
					i++
				} else if llTrimmed == "" {
					// Check if next non-blank line is still a list item
					nextNonBlank := i + 1
					for nextNonBlank < len(lines) && strings.TrimSpace(lines[nextNonBlank]) == "" {
						nextNonBlank++
					}
					if nextNonBlank < len(lines) && isMarkdownListItem(strings.TrimSpace(lines[nextNonBlank])) {
						// Include blank line as part of list
						listLines = append(listLines, ll)
						llLen := len(ll)
						if i < len(lines)-1 {
							llLen++
						}
						pos += llLen
						i++
					} else {
						break
					}
				} else {
					break
				}
			}
			blocks = append(blocks, mdBlock{
				Type:    mdBlockList,
				Content: strings.Join(listLines, "\n"),
				Start:   listStart,
				End:     pos,
			})
			continue
		}

		// --- Blockquote: starts with > ---
		if strings.HasPrefix(trimmed, ">") {
			var bqLines []string
			bqStart := lineStart
			for i < len(lines) {
				bl := strings.TrimSpace(lines[i])
				if strings.HasPrefix(bl, ">") || (bl != "" && len(bqLines) > 0) {
					bqLines = append(bqLines, lines[i])
					blLen := len(lines[i])
					if i < len(lines)-1 {
						blLen++
					}
					pos += blLen
					i++
				} else {
					break
				}
			}
			blocks = append(blocks, mdBlock{
				Type:    mdBlockBlockquote,
				Content: strings.Join(bqLines, "\n"),
				Start:   bqStart,
				End:     pos,
			})
			continue
		}

		// --- Paragraph: consecutive non-blank, non-special lines ---
		var paraLines []string
		paraStart := lineStart
		for i < len(lines) {
			pl := lines[i]
			plTrimmed := strings.TrimSpace(pl)
			if plTrimmed == "" {
				break
			}
			// Stop if we hit a special structure
			if isMarkdownHeading(plTrimmed) || isMarkdownHR(plTrimmed) ||
				strings.HasPrefix(plTrimmed, "```") || strings.HasPrefix(plTrimmed, "~~~") ||
				isMarkdownTableRow(plTrimmed) || isMarkdownListItem(plTrimmed) ||
				strings.HasPrefix(plTrimmed, ">") {
				break
			}
			paraLines = append(paraLines, pl)
			plLen := len(pl)
			if i < len(lines)-1 {
				plLen++
			}
			pos += plLen
			i++
		}
		if len(paraLines) > 0 {
			blocks = append(blocks, mdBlock{
				Type:    mdBlockParagraph,
				Content: strings.Join(paraLines, "\n"),
				Start:   paraStart,
				End:     pos,
			})
		}
	}

	return blocks
}

// ============================================================================
// Markdown detection helpers
// ============================================================================

var headingRegex = regexp.MustCompile(`^#{1,6}\s+`)

func isMarkdownHeading(line string) bool {
	return headingRegex.MatchString(line)
}

func isMarkdownHR(line string) bool {
	if len(line) < 3 {
		return false
	}
	// ---, ***, ___
	clean := strings.ReplaceAll(line, " ", "")
	if len(clean) < 3 {
		return false
	}
	allSame := true
	ch := clean[0]
	if ch != '-' && ch != '*' && ch != '_' {
		return false
	}
	for _, c := range clean {
		if byte(c) != ch {
			allSame = false
			break
		}
	}
	return allSame
}

func isMarkdownTableRow(line string) bool {
	return strings.HasPrefix(line, "|") && strings.Contains(line[1:], "|")
}

func isMarkdownTableSeparator(line string) bool {
	if !strings.HasPrefix(line, "|") {
		return false
	}
	// Should contain only |, -, :, and spaces
	for _, c := range line {
		if c != '|' && c != '-' && c != ':' && c != ' ' {
			return false
		}
	}
	return strings.Count(line, "-") >= 3
}

var listItemRegex = regexp.MustCompile(`^(\s*)([-*+]|\d+\.)\s+`)

func isMarkdownListItem(line string) bool {
	return listItemRegex.MatchString(line)
}

// ============================================================================
// Special Processing: Table-Aware + Code-Aware
// ============================================================================

// applySpecialProcessing transforms blocks based on the strategy's SpecialProcessing field.
// For table_aware: small tables stay whole, large tables get row-level semantic expansion.
// Code blocks and JSON blocks get syntax-aware sub-chunking.
func (s *ChunkService) applySpecialProcessing(blocks []mdBlock, strategy *IndexStrategy) []mdBlock {
	var result []mdBlock

	for _, block := range blocks {
		switch block.Type {
		case mdBlockTable:
			result = append(result, s.processTableBlock(block, strategy)...)
		case mdBlockCodeBlock:
			result = append(result, s.processCodeBlock(block, strategy)...)
		default:
			result = append(result, block)
		}
	}

	return result
}

// processTableBlock handles table blocks with row-level semantic expansion.
//
// Strategy based on row count:
//   - ≤10 rows: keep table as a single block (small table)
//   - 11-100 rows: convert each row to a natural language description
//   - >100 rows: same as 11-100 but with a summary block prepended
func (s *ChunkService) processTableBlock(block mdBlock, _ *IndexStrategy) []mdBlock {
	lines := strings.Split(block.Content, "\n")
	if len(lines) < 2 {
		return []mdBlock{block}
	}

	// Parse headers
	headerLine := strings.TrimSpace(lines[0])
	headers := parseTableRow(headerLine)

	// Find data rows (skip header and separator)
	dataStart := 1
	if dataStart < len(lines) && isMarkdownTableSeparator(strings.TrimSpace(lines[dataStart])) {
		dataStart = 2
	}

	var dataRows [][]string
	for i := dataStart; i < len(lines); i++ {
		row := strings.TrimSpace(lines[i])
		if row == "" {
			continue
		}
		cells := parseTableRow(row)
		if len(cells) > 0 {
			dataRows = append(dataRows, cells)
		}
	}

	rowCount := len(dataRows)

	// Small table (≤10 rows): keep as-is
	if rowCount <= 10 {
		return []mdBlock{block}
	}

	var result []mdBlock

	// For large tables (>100 rows), add a summary block
	if rowCount > 100 {
		summary := fmt.Sprintf("表格概览：共%d行，%d列。列名：%s",
			rowCount, len(headers), strings.Join(headers, "、"))
		result = append(result, mdBlock{
			Type:    mdBlockParagraph,
			Content: summary,
			Start:   block.Start,
			End:     block.Start,
			Metadata: map[string]string{
				"chunk_type":      "table_summary",
				"original_table":  "true",
				"table_row_count": fmt.Sprintf("%d", rowCount),
			},
		})
	}

	// Row-level semantic expansion: convert each row to natural language
	for i, row := range dataRows {
		var parts []string
		for j, cell := range row {
			cell = strings.TrimSpace(cell)
			if cell == "" {
				continue
			}
			header := ""
			if j < len(headers) {
				header = headers[j]
			}
			if header != "" {
				parts = append(parts, fmt.Sprintf("%s为%s", header, cell))
			} else {
				parts = append(parts, cell)
			}
		}
		if len(parts) == 0 {
			continue
		}

		naturalLang := strings.Join(parts, "，")
		result = append(result, mdBlock{
			Type:    mdBlockParagraph,
			Content: naturalLang,
			Start:   block.Start,
			End:     block.End,
			Metadata: map[string]string{
				"chunk_type":     "table_row",
				"original_table": "true",
				"row_index":      fmt.Sprintf("%d", i),
			},
		})
	}

	return result
}

// parseTableRow extracts cells from a Markdown table row
func parseTableRow(row string) []string {
	row = strings.TrimSpace(row)
	row = strings.TrimPrefix(row, "|")
	row = strings.TrimSuffix(row, "|")
	parts := strings.Split(row, "|")
	var cells []string
	for _, p := range parts {
		cells = append(cells, strings.TrimSpace(p))
	}
	return cells
}

// processCodeBlock handles code block sub-chunking with syntax awareness.
//
// For JSON: split by top-level keys.
// For general code: split by function/class boundaries or logical sections.
// Small code blocks (≤ chunkSize) are kept as-is.
func (s *ChunkService) processCodeBlock(block mdBlock, strategy *IndexStrategy) []mdBlock {
	if len(block.Content) <= strategy.ChunkSize {
		return []mdBlock{block}
	}

	lang := ""
	if block.Metadata != nil {
		lang = strings.ToLower(block.Metadata["language"])
	}

	// Extract the code content (strip fences)
	code := extractCodeContent(block.Content)

	switch lang {
	case "json":
		return s.splitJSONBlock(block, code, strategy)
	case "go", "golang":
		return s.splitGoCodeBlock(block, code, strategy)
	case "python", "py":
		return s.splitPythonCodeBlock(block, code, strategy)
	case "javascript", "js", "typescript", "ts":
		return s.splitJSCodeBlock(block, code, strategy)
	default:
		// For unknown languages, try JSON detection first, then generic split
		trimmed := strings.TrimSpace(code)
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			return s.splitJSONBlock(block, code, strategy)
		}
		return s.splitGenericCodeBlock(block, code, strategy)
	}
}

// extractCodeContent strips the fence markers from a fenced code block
func extractCodeContent(block string) string {
	lines := strings.Split(block, "\n")
	if len(lines) < 2 {
		return block
	}
	// Remove first and last lines if they are fences
	start := 0
	end := len(lines)
	if strings.HasPrefix(strings.TrimSpace(lines[0]), "```") || strings.HasPrefix(strings.TrimSpace(lines[0]), "~~~") {
		start = 1
	}
	if end > start && (strings.HasPrefix(strings.TrimSpace(lines[end-1]), "```") || strings.HasPrefix(strings.TrimSpace(lines[end-1]), "~~~")) {
		end--
	}
	return strings.Join(lines[start:end], "\n")
}

// splitJSONBlock splits a large JSON block by top-level keys
func (s *ChunkService) splitJSONBlock(block mdBlock, code string, strategy *IndexStrategy) []mdBlock {
	trimmed := strings.TrimSpace(code)

	// Try to parse as JSON object
	if strings.HasPrefix(trimmed, "{") {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &obj); err == nil && len(obj) > 1 {
			var result []mdBlock
			for key, value := range obj {
				fragment := fmt.Sprintf("```json\n{\"%s\": %s}\n```", key, string(value))
				if len(fragment) <= strategy.ChunkSize*2 {
					result = append(result, mdBlock{
						Type:    mdBlockCodeBlock,
						Content: fragment,
						Start:   block.Start,
						End:     block.End,
						Metadata: map[string]string{
							"language":   "json",
							"chunk_type": "json_key",
							"json_key":   key,
						},
					})
				} else {
					// If a single key's value is still too large, keep it as one block
					result = append(result, mdBlock{
						Type:    mdBlockCodeBlock,
						Content: fragment,
						Start:   block.Start,
						End:     block.End,
						Metadata: map[string]string{
							"language":   "json",
							"chunk_type": "json_key_large",
							"json_key":   key,
						},
					})
				}
			}
			if len(result) > 0 {
				return result
			}
		}
	}

	// Try to parse as JSON array
	if strings.HasPrefix(trimmed, "[") {
		var arr []json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &arr); err == nil && len(arr) > 1 {
			var result []mdBlock
			batchSize := 10
			for i := 0; i < len(arr); i += batchSize {
				end := i + batchSize
				if end > len(arr) {
					end = len(arr)
				}
				batch := arr[i:end]
				batchJSON, _ := json.MarshalIndent(batch, "", "  ")
				fragment := fmt.Sprintf("```json\n%s\n```", string(batchJSON))
				result = append(result, mdBlock{
					Type:    mdBlockCodeBlock,
					Content: fragment,
					Start:   block.Start,
					End:     block.End,
					Metadata: map[string]string{
						"language":    "json",
						"chunk_type":  "json_array_batch",
						"batch_start": fmt.Sprintf("%d", i),
						"batch_end":   fmt.Sprintf("%d", end),
					},
				})
			}
			if len(result) > 0 {
				return result
			}
		}
	}

	// Fallback: generic split
	return s.splitGenericCodeBlock(block, code, strategy)
}

// splitGoCodeBlock splits Go code by function/type boundaries
func (s *ChunkService) splitGoCodeBlock(block mdBlock, code string, strategy *IndexStrategy) []mdBlock {
	return s.splitCodeByPattern(block, code, strategy, regexp.MustCompile(`(?m)^(func |type |var |const )`))
}

// splitPythonCodeBlock splits Python code by class/function boundaries
func (s *ChunkService) splitPythonCodeBlock(block mdBlock, code string, strategy *IndexStrategy) []mdBlock {
	return s.splitCodeByPattern(block, code, strategy, regexp.MustCompile(`(?m)^(class |def |async def )`))
}

// splitJSCodeBlock splits JavaScript/TypeScript code by function/class boundaries
func (s *ChunkService) splitJSCodeBlock(block mdBlock, code string, strategy *IndexStrategy) []mdBlock {
	return s.splitCodeByPattern(block, code, strategy, regexp.MustCompile(`(?m)^(function |class |const |export |async function )`))
}

// splitCodeByPattern splits code at regex-matched boundaries (function/class definitions)
func (s *ChunkService) splitCodeByPattern(block mdBlock, code string, strategy *IndexStrategy, pattern *regexp.Regexp) []mdBlock {
	locs := pattern.FindAllStringIndex(code, -1)
	if len(locs) <= 1 {
		return s.splitGenericCodeBlock(block, code, strategy)
	}

	lang := ""
	if block.Metadata != nil {
		lang = block.Metadata["language"]
	}

	var result []mdBlock
	for i, loc := range locs {
		start := loc[0]
		end := len(code)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		fragment := strings.TrimSpace(code[start:end])
		if fragment == "" {
			continue
		}
		wrapped := fmt.Sprintf("```%s\n%s\n```", lang, fragment)
		result = append(result, mdBlock{
			Type:    mdBlockCodeBlock,
			Content: wrapped,
			Start:   block.Start,
			End:     block.End,
			Metadata: map[string]string{
				"language":   lang,
				"chunk_type": "code_function",
			},
		})
	}

	// Include any preamble (imports, etc.) before the first match
	if len(locs) > 0 && locs[0][0] > 0 {
		preamble := strings.TrimSpace(code[:locs[0][0]])
		if preamble != "" {
			wrapped := fmt.Sprintf("```%s\n%s\n```", lang, preamble)
			result = append([]mdBlock{{
				Type:    mdBlockCodeBlock,
				Content: wrapped,
				Start:   block.Start,
				End:     block.End,
				Metadata: map[string]string{
					"language":   lang,
					"chunk_type": "code_preamble",
				},
			}}, result...)
		}
	}

	if len(result) > 0 {
		return result
	}
	return s.splitGenericCodeBlock(block, code, strategy)
}

// splitGenericCodeBlock splits code by blank lines as a last resort
func (s *ChunkService) splitGenericCodeBlock(block mdBlock, code string, strategy *IndexStrategy) []mdBlock {
	// Split by double blank lines or logical sections
	sections := regexp.MustCompile(`\n\n+`).Split(code, -1)
	if len(sections) <= 1 {
		// Can't split meaningfully, return as-is
		return []mdBlock{block}
	}

	lang := ""
	if block.Metadata != nil {
		lang = block.Metadata["language"]
	}

	var result []mdBlock
	var currentBuf strings.Builder
	chunkSize := strategy.ChunkSize

	for _, section := range sections {
		section = strings.TrimSpace(section)
		if section == "" {
			continue
		}
		if currentBuf.Len()+len(section) > chunkSize && currentBuf.Len() > 0 {
			wrapped := fmt.Sprintf("```%s\n%s\n```", lang, currentBuf.String())
			result = append(result, mdBlock{
				Type:    mdBlockCodeBlock,
				Content: wrapped,
				Start:   block.Start,
				End:     block.End,
				Metadata: map[string]string{
					"language":   lang,
					"chunk_type": "code_section",
				},
			})
			currentBuf.Reset()
		}
		if currentBuf.Len() > 0 {
			currentBuf.WriteString("\n\n")
		}
		currentBuf.WriteString(section)
	}
	if currentBuf.Len() > 0 {
		wrapped := fmt.Sprintf("```%s\n%s\n```", lang, currentBuf.String())
		result = append(result, mdBlock{
			Type:    mdBlockCodeBlock,
			Content: wrapped,
			Start:   block.Start,
			End:     block.End,
			Metadata: map[string]string{
				"language":   lang,
				"chunk_type": "code_section",
			},
		})
	}

	if len(result) > 0 {
		return result
	}
	return []mdBlock{block}
}

// ============================================================================
// Structure-Aware Chunking Strategies
// ============================================================================

// structureAwareSemanticChunk groups Markdown blocks into chunks respecting structural boundaries.
// Blocks are never split mid-structure. If a single block exceeds chunk size, it becomes its own chunk.
func (s *ChunkService) structureAwareSemanticChunk(doc *models.Document, blocks []mdBlock, strategy *IndexStrategy) ([]models.Chunk, error) {
	chunkSize := strategy.ChunkSize
	var chunks []models.Chunk
	var currentBlocks []mdBlock
	currentLength := 0
	chunkIndex := 0

	flushCurrent := func() {
		if len(currentBlocks) == 0 {
			return
		}
		var parts []string
		startPos := currentBlocks[0].Start
		endPos := currentBlocks[len(currentBlocks)-1].End
		meta := models.JSONB{}

		for _, b := range currentBlocks {
			parts = append(parts, b.Content)
			// Propagate metadata from specialized blocks
			if b.Metadata != nil {
				if ct, ok := b.Metadata["chunk_type"]; ok {
					meta["contains_"+ct] = true
				}
			}
		}
		chunkContent := strings.TrimSpace(strings.Join(parts, "\n\n"))
		if chunkContent == "" {
			return
		}

		chunkType := "semantic"
		// If this chunk contains only a single special block, use that type
		if len(currentBlocks) == 1 {
			switch currentBlocks[0].Type {
			case mdBlockTable:
				chunkType = "table"
			case mdBlockCodeBlock:
				chunkType = "code"
			case mdBlockList:
				chunkType = "list"
			}
			if currentBlocks[0].Metadata != nil {
				if ct, ok := currentBlocks[0].Metadata["chunk_type"]; ok {
					chunkType = ct
				}
				for k, v := range currentBlocks[0].Metadata {
					meta[k] = v
				}
			}
		}

		chunks = append(chunks, models.Chunk{
			TenantModel: models.TenantModel{
				TenantID: doc.TenantID,
			},
			DocumentID:    doc.ID,
			Content:       chunkContent,
			ChunkIndex:    chunkIndex,
			StartPosition: startPos,
			EndPosition:   endPos,
			ChunkType:     chunkType,
			Metadata:      meta,
		})
		chunkIndex++
		currentBlocks = nil
		currentLength = 0
	}

	for _, block := range blocks {
		blockLen := len(block.Content)

		// If this single block exceeds chunk size, flush current and add it solo
		if blockLen > chunkSize {
			flushCurrent()
			currentBlocks = []mdBlock{block}
			flushCurrent()
			continue
		}

		// If adding this block would exceed chunk size, flush first
		if currentLength+blockLen > chunkSize && len(currentBlocks) > 0 {
			flushCurrent()
		}

		currentBlocks = append(currentBlocks, block)
		currentLength += blockLen + 2 // +2 for \n\n separator
	}
	flushCurrent()

	return chunks, nil
}

// structureAwareSectionChunk groups blocks by heading sections, respecting structural boundaries.
// Key improvement: prevents pure-heading chunks by ensuring each section includes content.
func (s *ChunkService) structureAwareSectionChunk(doc *models.Document, blocks []mdBlock, strategy *IndexStrategy) ([]models.Chunk, error) {
	chunkSize := strategy.ChunkSize
	var chunks []models.Chunk
	chunkIndex := 0

	// Track heading hierarchy (h1 → h2 → h3)
	var headingPath []string
	
	// Track heading blocks for smart inclusion
	type headingBlock struct {
		level int
		block mdBlock
	}
	var headingStack []headingBlock
	
	var currentBlocks []mdBlock
	currentLength := 0

	flushSection := func() {
		if len(currentBlocks) == 0 {
			return
		}

		// Skip if only headings with no content
		hasContent := false
		contentStartIdx := 0
		for i, b := range currentBlocks {
			if b.Type != mdBlockHeading {
				hasContent = true
				contentStartIdx = i
				break
			}
		}
		if !hasContent {
			// Don't flush pure-heading chunks
			return
		}

		// ✅ Smart heading selection: only include CLOSEST parent headings
		// Strategy: Keep at most 2 levels of headings before content
		var finalBlocks []mdBlock
		headingsBeforeContent := currentBlocks[:contentStartIdx]
		
		// Only keep the last 1-2 heading levels (most relevant context)
		maxHeadingsToKeep := 2
		if len(headingsBeforeContent) > maxHeadingsToKeep {
			headingsBeforeContent = headingsBeforeContent[len(headingsBeforeContent)-maxHeadingsToKeep:]
		}
		
		// Check total heading length - if too long, keep only 1 level
		totalHeadingLen := 0
		for _, h := range headingsBeforeContent {
			totalHeadingLen += len(h.Content)
		}
		if totalHeadingLen > 100 {
			// Heading too long, only keep the most immediate one
			headingsBeforeContent = headingsBeforeContent[len(headingsBeforeContent)-1:]
		}
		
		finalBlocks = append(finalBlocks, headingsBeforeContent...)
		finalBlocks = append(finalBlocks, currentBlocks[contentStartIdx:]...)

		var parts []string
		startPos := finalBlocks[0].Start
		endPos := finalBlocks[len(finalBlocks)-1].End

		for _, b := range finalBlocks {
			parts = append(parts, b.Content)
		}
		chunkContent := strings.TrimSpace(strings.Join(parts, "\n\n"))
		if chunkContent == "" {
			return
		}

		// ✅ Store FULL heading path in metadata (for filtering and display)
		meta := models.JSONB{
			"section_title": strings.Join(headingPath, " > "),
			"heading_levels": len(headingPath),
		}

		chunks = append(chunks, models.Chunk{
			TenantModel: models.TenantModel{
				TenantID: doc.TenantID,
			},
			DocumentID:    doc.ID,
			Content:       chunkContent,
			ChunkIndex:    chunkIndex,
			StartPosition: startPos,
			EndPosition:   endPos,
			ChunkType:     "section",
			Metadata:      meta,
		})
		chunkIndex++
		currentBlocks = nil
		currentLength = 0
	}

	for _, block := range blocks {
		if block.Type == mdBlockHeading {
			level := 1
			if block.Metadata != nil {
				if levelStr, ok := block.Metadata["level"]; ok {
					fmt.Sscanf(levelStr, "%d", &level)
				}
			}

			// Update heading path: replace at current level and trim deeper levels
			if level <= len(headingPath) {
				headingPath = headingPath[:level-1]
				// Trim heading stack to match
				for len(headingStack) > 0 && headingStack[len(headingStack)-1].level >= level {
					headingStack = headingStack[:len(headingStack)-1]
				}
			}
			if level > len(headingPath) {
				// Extend path if needed
				for len(headingPath) < level-1 {
					headingPath = append(headingPath, "")
				}
			}
			headingPath = append(headingPath, block.Content)
			headingStack = append(headingStack, headingBlock{level: level, block: block})

			// If we already have content blocks, flush before starting new section
			hasContent := false
			for _, b := range currentBlocks {
				if b.Type != mdBlockHeading {
					hasContent = true
					break
				}
			}
			if hasContent {
				flushSection()
				// After flush, restore limited parent headings (smart strategy)
				// Only restore headings that are ancestors of current heading
				var ancestorHeadings []mdBlock
				for _, h := range headingStack {
					if h.level < level {
						ancestorHeadings = append(ancestorHeadings, h.block)
					}
				}
				// Keep at most 2 ancestor levels
				if len(ancestorHeadings) > 2 {
					ancestorHeadings = ancestorHeadings[len(ancestorHeadings)-2:]
				}
				currentBlocks = ancestorHeadings
				currentLength = 0
				for _, h := range ancestorHeadings {
					currentLength += len(h.Content) + 2
				}
			}
			
			// Add current heading to section
			currentBlocks = append(currentBlocks, block)
			currentLength += len(block.Content) + 2
			continue
		}

		blockLen := len(block.Content)

		// If adding this block would make section too large, flush first
		// But never split a structural block (table/code/list)
		if currentLength+blockLen > chunkSize*2 && len(currentBlocks) > 1 {
			flushSection()
			// Re-add limited ancestor headings
			var ancestorHeadings []mdBlock
			for _, h := range headingStack {
				ancestorHeadings = append(ancestorHeadings, h.block)
			}
			// Keep at most 2 ancestor levels
			if len(ancestorHeadings) > 2 {
				ancestorHeadings = ancestorHeadings[len(ancestorHeadings)-2:]
			}
			currentBlocks = ancestorHeadings
			currentLength = 0
			for _, h := range ancestorHeadings {
				currentLength += len(h.Content) + 2
			}
		}

		currentBlocks = append(currentBlocks, block)
		currentLength += blockLen + 2
	}
	flushSection()

	// ============================================================================
	// Build TOC Structure: 构建目录导航图谱，关联 chunk IDs 到每个章节
	// ============================================================================
	toc := s.buildTOCFromChunks(blocks, chunks)
	if toc != nil && len(toc) > 0 {
		// Store TOC in document metadata (will be saved later)
		if doc.TOCStructure == nil {
			doc.TOCStructure = make(models.JSONB)
		}
		tocData, err := json.Marshal(toc)
		if err == nil {
			var tocNodes []models.TOCNode
			json.Unmarshal(tocData, &tocNodes)
			doc.TOCStructure = models.JSONB{"nodes": tocNodes}
		}
	}

	return chunks, nil
}

// slidingChunk performs sliding window chunking (preserved from original)
func (s *ChunkService) slidingChunk(doc *models.Document, strategy *IndexStrategy) ([]models.Chunk, error) {
	content := doc.Content
	chunkSize := strategy.ChunkSize
	overlap := s.config.Document.ChunkOverlap

	var chunks []models.Chunk
	start := 0
	chunkIndex := 0

	for start < len(content) {
		end := start + chunkSize
		if end > len(content) {
			end = len(content)
		}

		// Try to end at sentence boundary
		if end < len(content) {
			lastPeriod := strings.LastIndex(content[start:end], "。")
			lastNewline := strings.LastIndex(content[start:end], "\n")
			lastBoundary := lastPeriod
			if lastNewline > lastBoundary {
				lastBoundary = lastNewline
			}
			if lastBoundary > chunkSize/2 {
				end = start + lastBoundary + 1
			}
		}

		chunk := models.Chunk{
			TenantModel: models.TenantModel{
				TenantID: doc.TenantID,
			},
			DocumentID:    doc.ID,
			Content:       strings.TrimSpace(content[start:end]),
			ChunkIndex:    chunkIndex,
			StartPosition: start,
			EndPosition:   end,
			ChunkType:     "sliding",
		}
		chunks = append(chunks, chunk)

		start = end - overlap
		if start < 0 {
			start = 0
		}
		chunkIndex++
	}

	return chunks, nil
}

// parentChildChunk creates parent-child chunks (preserved from original, uses structure-aware for parents)
func (s *ChunkService) parentChildChunk(doc *models.Document, strategy *IndexStrategy) ([]models.Chunk, error) {
	blocks := s.parseMarkdownBlocks(doc.Content)
	blocks = s.applySpecialProcessing(blocks, strategy)

	smallSize := strategy.ChunkSize / 2
	largeSize := strategy.ChunkSize

	var chunks []models.Chunk
	chunkIndex := 0

	// Create large chunks (parents) using structure-aware chunking
	largeChunks, err := s.structureAwareSemanticChunk(doc, blocks, &IndexStrategy{ChunkSize: largeSize})
	if err != nil {
		return nil, err
	}

	for _, largeChunk := range largeChunks {
		largeChunk.ChunkIndex = chunkIndex
		largeChunk.ChunkType = "parent"
		chunks = append(chunks, largeChunk)

		// Create small chunks (children) within each large chunk
		childBlocks := s.parseMarkdownBlocks(largeChunk.Content)
		childChunks, err := s.structureAwareSemanticChunk(&models.Document{
			TenantModel: doc.TenantModel,
			Content:     largeChunk.Content,
		}, childBlocks, &IndexStrategy{ChunkSize: smallSize})
		if err != nil {
			return nil, err
		}

		for _, smallChunk := range childChunks {
			chunkIndex++
			smallChunk.ChunkIndex = chunkIndex
			smallChunk.ChunkType = "child"
			smallChunk.StartPosition += largeChunk.StartPosition
			smallChunk.EndPosition += largeChunk.StartPosition
			if smallChunk.Metadata == nil {
				smallChunk.Metadata = models.JSONB{}
			}
			smallChunk.Metadata["parent_id"] = largeChunk.ID
			smallChunk.Metadata["parent_start"] = largeChunk.StartPosition
			chunks = append(chunks, smallChunk)
		}

		chunkIndex++
	}

	return chunks, nil
}

// ============================================================================
// Legacy helpers (kept for compatibility)
// ============================================================================

// splitSentences splits text into sentences
func (s *ChunkService) splitSentences(text string) []string {
	re := regexp.MustCompile(`[。！？.!?]\s*`)
	sentences := re.Split(text, -1)

	var result []string
	for _, sentence := range sentences {
		sentence = strings.TrimSpace(sentence)
		if sentence != "" {
			result = append(result, sentence)
		}
	}

	return result
}

// getOverlapSentences gets sentences for overlap
func (s *ChunkService) getOverlapSentences(sentences []string, targetLen int) []string {
	var result []string
	totalLen := 0

	for i := len(sentences) - 1; i >= 0; i-- {
		if totalLen+len(sentences[i]) > targetLen {
			break
		}
		result = append([]string{sentences[i]}, result...)
		totalLen += len(sentences[i])
	}

	return result
}

// GetDocumentChunks retrieves all chunks for a document
func (s *ChunkService) GetDocumentChunks(ctx context.Context, tenantID, docID int64) ([]models.Chunk, error) {
	var chunks []models.Chunk
	if err := s.db.Where("document_id = ? AND tenant_id = ?", docID, tenantID).
		Order("chunk_index").Find(&chunks).Error; err != nil {
		return nil, err
	}
	return chunks, nil
}

// DeleteDocumentChunks deletes all chunks for a document
func (s *ChunkService) DeleteDocumentChunks(ctx context.Context, tenantID, docID int64) error {
	return s.db.Where("document_id = ? AND tenant_id = ?", docID, tenantID).
		Delete(&models.Chunk{}).Error
}

// ============================================================================
// TOC (Table of Contents) Builder: 构建目录导航图谱
// ============================================================================

// buildTOCFromChunks 从 blocks 和 chunks 构建目录树，将每个章节关联到其 chunk IDs
// 返回的是根级别的 TOC nodes (level 1 headings 和它们的子树)
func (s *ChunkService) buildTOCFromChunks(blocks []mdBlock, chunks []models.Chunk) []models.TOCNode {
	// Step 1: 提取所有标题 blocks
	type headingInfo struct {
		level    int
		title    string
		position int // block index
	}
	var headings []headingInfo
	for i, block := range blocks {
		if block.Type == mdBlockHeading {
			level := 1
			if block.Metadata != nil {
				if levelStr, ok := block.Metadata["level"]; ok {
					fmt.Sscanf(levelStr, "%d", &level)
				}
			}
			// Extract title text (remove leading #)
			title := strings.TrimSpace(strings.TrimLeft(block.Content, "#"))
			headings = append(headings, headingInfo{
				level:    level,
				title:    title,
				position: i,
			})
		}
	}

	if len(headings) == 0 {
		return nil
	}

	// Step 2: 为每个标题查找关联的 chunk IDs
	// 策略: 找到 metadata.section_title 包含该标题的所有 chunks
	headingToChunkIDs := make(map[string][]int)
	for _, chunk := range chunks {
		if chunk.Metadata == nil {
			continue
		}
		sectionTitle, ok := chunk.Metadata["section_title"].(string)
		if !ok {
			continue
		}
		// 为每个在路径中出现的标题记录此 chunk
		for _, h := range headings {
			if strings.Contains(sectionTitle, h.title) {
				key := fmt.Sprintf("%d:%s", h.level, h.title)
				headingToChunkIDs[key] = append(headingToChunkIDs[key], chunk.ChunkIndex)
			}
		}
	}

	// Step 3: 构建层级树
	// 使用栈来维护当前的父节点路径
	var rootNodes []models.TOCNode
	var stack [](*models.TOCNode) // 栈中存放指向节点的指针
	var pathStack []string        // 维护完整路径

	for _, h := range headings {
		key := fmt.Sprintf("%d:%s", h.level, h.title)
		
		// 构建完整路径
		// 1. 弹出栈直到找到父级 (level < current level)
		for len(stack) > 0 {
			lastIdx := len(stack) - 1
			if stack[lastIdx] == nil || (len(pathStack) > 0 && h.level <= len(pathStack)) {
				// 回退到合适的父级
				targetLevel := h.level - 1
				for len(stack) > targetLevel {
					stack = stack[:len(stack)-1]
					if len(pathStack) > 0 {
						pathStack = pathStack[:len(pathStack)-1]
					}
				}
				break
			}
			break
		}

		// 构建路径
		currentPath := append([]string{}, pathStack...)
		currentPath = append(currentPath, h.title)
		fullPath := strings.Join(currentPath, " > ")

		// 创建新节点
		node := models.TOCNode{
			Level:    h.level,
			Title:    h.title,
			Path:     fullPath,
			ChunkIDs: headingToChunkIDs[key],
			Children: []models.TOCNode{},
			Position: h.position,
		}

		// 2. 将节点添加到父级或根级别
		if h.level == 1 {
			// 一级标题：添加到根
			rootNodes = append(rootNodes, node)
			stack = [](*models.TOCNode){&rootNodes[len(rootNodes)-1]}
			pathStack = []string{h.title}
		} else {
			// 子级标题：添加到栈顶的 children
			if len(stack) > 0 && stack[len(stack)-1] != nil {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, node)
				// 更新栈：指向新添加的子节点
				childIdx := len(parent.Children) - 1
				stack = append(stack, &parent.Children[childIdx])
				pathStack = append(pathStack, h.title)
			} else {
				// 没有父级（文档不是从 h1 开始）：作为根节点
				rootNodes = append(rootNodes, node)
				stack = [](*models.TOCNode){&rootNodes[len(rootNodes)-1]}
				pathStack = []string{h.title}
			}
		}
	}

	return rootNodes
}
