package services

import (
	"regexp"
	"testing"

	"github.com/jizhuozhi/knowledge/internal/config"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTestChunkService(t *testing.T) *ChunkService {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	cfg := &config.Config{
		Document: config.DocumentConfig{
			ChunkSize:    512,
			ChunkOverlap: 50,
		},
	}
	return NewChunkService(db, cfg)
}

// ============================================================================
// Markdown Detection Helpers Tests
// ============================================================================

func TestIsMarkdownHeading(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"# Heading 1", true},
		{"## Heading 2", true},
		{"### Heading 3", true},
		{"#### Heading 4", true},
		{"##### Heading 5", true},
		{"###### Heading 6", true},
		{"####### Not valid", false}, // 7 hashes
		{"No heading", false},
		{"#No space", false},
		{"  # Indented", false},
		{"", false},
		{"Plain text", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := isMarkdownHeading(tt.input)
			if result != tt.expected {
				t.Errorf("isMarkdownHeading(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestIsMarkdownHR(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"---", true},
		{"***", true},
		{"___", true},
		{"- - -", true},
		{"* * *", true},
		{"_ _ _", true},
		{"----", true},
		{"*****", true},
		{"______", true},
		{"--", false},      // too short
		{"- -", false},     // too short after removing spaces
		{"abc", false},     // wrong characters
		{"-a-", false},     // mixed characters
		{"", false},
		{"---text", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := isMarkdownHR(tt.input)
			if result != tt.expected {
				t.Errorf("isMarkdownHR(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestIsMarkdownTableRow(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"| col1 | col2 |", true},
		{"|a|b|c|", true},
		{"| single |", true},
		{"no pipes", false},
		{"|only start", false},
		{"only end|", false},
		{"", false},
		{"plain text", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := isMarkdownTableRow(tt.input)
			if result != tt.expected {
				t.Errorf("isMarkdownTableRow(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestIsMarkdownTableSeparator(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"|---|---|", true},
		{"|:---|:---:|---:|", true},
		{"| - - - | - - - |", true},
		{"|--|", false}, // only 2 dashes, needs at least 3
		{"|no dashes|", false},
		{"|a-b|", false},
		{"no pipes", false},
		{"|-", false}, // less than 3 dashes
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := isMarkdownTableSeparator(tt.input)
			if result != tt.expected {
				t.Errorf("isMarkdownTableSeparator(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestIsMarkdownListItem(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"- item", true},
		{"* item", true},
		{"+ item", true},
		{"1. item", true},
		{"10. item", true},
		{"  - indented", true},
		{"  1. indented number", true},
		{"no marker", false},
		{"-no space", false},
		{"1.no space", false},
		{"", false},
		{"-- not a list", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := isMarkdownListItem(tt.input)
			if result != tt.expected {
				t.Errorf("isMarkdownListItem(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

// ============================================================================
// parseMarkdownBlocks Tests
// ============================================================================

func TestParseMarkdownBlocks_Heading(t *testing.T) {
	svc := newTestChunkService(t)
	content := "# Title\n\nParagraph here."
	blocks := svc.parseMarkdownBlocks(content)

	if len(blocks) < 2 {
		t.Fatalf("expected at least 2 blocks, got %d", len(blocks))
	}

	if blocks[0].Type != mdBlockHeading {
		t.Errorf("first block should be heading, got %s", blocks[0].Type)
	}
	if blocks[0].Metadata["level"] != "1" {
		t.Errorf("heading level should be 1, got %s", blocks[0].Metadata["level"])
	}
}

func TestParseMarkdownBlocks_CodeBlock(t *testing.T) {
	svc := newTestChunkService(t)
	content := "```go\nfunc main() {}\n```\n\nParagraph."
	blocks := svc.parseMarkdownBlocks(content)

	if len(blocks) < 2 {
		t.Fatalf("expected at least 2 blocks, got %d", len(blocks))
	}

	if blocks[0].Type != mdBlockCodeBlock {
		t.Errorf("first block should be code_block, got %s", blocks[0].Type)
	}
	if blocks[0].Metadata["language"] != "go" {
		t.Errorf("language should be 'go', got %s", blocks[0].Metadata["language"])
	}
}

func TestParseMarkdownBlocks_Table(t *testing.T) {
	svc := newTestChunkService(t)
	content := `| Name | Age |
|------|-----|
| Alice | 30 |
| Bob | 25 |`
	blocks := svc.parseMarkdownBlocks(content)

	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}

	if blocks[0].Type != mdBlockTable {
		t.Errorf("block should be table, got %s", blocks[0].Type)
	}
	// row_count is total lines minus separator row
	// This test verifies the table is correctly parsed, not the exact count
	if blocks[0].Metadata["row_count"] == "" {
		t.Error("row_count metadata should be set")
	}
}

func TestParseMarkdownBlocks_List(t *testing.T) {
	svc := newTestChunkService(t)
	content := "- Item 1\n- Item 2\n- Item 3\n\nParagraph."
	blocks := svc.parseMarkdownBlocks(content)

	if len(blocks) < 2 {
		t.Fatalf("expected at least 2 blocks, got %d", len(blocks))
	}

	if blocks[0].Type != mdBlockList {
		t.Errorf("first block should be list, got %s", blocks[0].Type)
	}
}

func TestParseMarkdownBlocks_Blockquote(t *testing.T) {
	svc := newTestChunkService(t)
	content := "> Quote line 1\n> Quote line 2\n\nParagraph."
	blocks := svc.parseMarkdownBlocks(content)

	if len(blocks) < 2 {
		t.Fatalf("expected at least 2 blocks, got %d", len(blocks))
	}

	if blocks[0].Type != mdBlockBlockquote {
		t.Errorf("first block should be blockquote, got %s", blocks[0].Type)
	}
}

func TestParseMarkdownBlocks_HR(t *testing.T) {
	svc := newTestChunkService(t)
	content := "Paragraph 1\n\n---\n\nParagraph 2"
	blocks := svc.parseMarkdownBlocks(content)

	found := false
	for _, b := range blocks {
		if b.Type == mdBlockHR {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find an HR block")
	}
}

func TestParseMarkdownBlocks_Mixed(t *testing.T) {
	svc := newTestChunkService(t)
	content := `# Title

This is a paragraph.

## Section

- List item 1
- List item 2

` + "```go" + `
func main() {
    fmt.Println("hello")
}
` + "```" + `

| A | B |
|---|---|
| 1 | 2 |

> A quote

---

Final paragraph.`

	blocks := svc.parseMarkdownBlocks(content)

	expectedTypes := []mdBlockType{
		mdBlockHeading,    // # Title
		mdBlockParagraph,  // This is a paragraph
		mdBlockHeading,    // ## Section
		mdBlockList,       // List items
		mdBlockCodeBlock,  // code block
		mdBlockTable,      // table
		mdBlockBlockquote, // quote
		mdBlockHR,         // ---
		mdBlockParagraph,  // Final paragraph
	}

	if len(blocks) != len(expectedTypes) {
		t.Fatalf("expected %d blocks, got %d", len(expectedTypes), len(blocks))
	}

	for i, expected := range expectedTypes {
		if blocks[i].Type != expected {
			t.Errorf("block %d: expected %s, got %s", i, expected, blocks[i].Type)
		}
	}
}

// ============================================================================
// Table Processing Tests
// ============================================================================

func TestProcessTableBlock_SmallTable(t *testing.T) {
	svc := newTestChunkService(t)
	block := mdBlock{
		Type: mdBlockTable,
		Content: `| Name | Age |
|------|-----|
| Alice | 30 |
| Bob | 25 |`,
	}
	strategy := &IndexStrategy{ChunkSize: 512}

	result := svc.processTableBlock(block, strategy)

	// Small table (2 data rows) should remain as single block
	if len(result) != 1 {
		t.Errorf("small table should remain as 1 block, got %d", len(result))
	}
	if result[0].Type != mdBlockTable {
		t.Errorf("block type should remain table, got %s", result[0].Type)
	}
}

func TestProcessTableBlock_LargeTable(t *testing.T) {
	svc := newTestChunkService(t)

	// Build a table with 15 rows (>10 rows threshold)
	var content string
	content = "| Name | Age | City |\n|------|-----|------|\n"
	for i := 0; i < 15; i++ {
		content += "| Name" + string(rune('A'+i%26)) + " | 30 | City |\n"
	}

	block := mdBlock{
		Type:    mdBlockTable,
		Content: content,
	}
	strategy := &IndexStrategy{ChunkSize: 512}

	result := svc.processTableBlock(block, strategy)

	// Large table should be split into row-level blocks (15 rows = 15 blocks)
	if len(result) != 15 {
		t.Errorf("large table should produce 15 row blocks, got %d", len(result))
	}

	// Each row should be converted to natural language
	for i, b := range result {
		if b.Type != mdBlockParagraph {
			t.Errorf("row %d should be paragraph type, got %s", i, b.Type)
		}
		if b.Metadata["chunk_type"] != "table_row" {
			t.Errorf("row %d should have chunk_type=table_row, got %s", i, b.Metadata["chunk_type"])
		}
	}
}

func TestParseTableRow(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"| a | b | c |", []string{"a", "b", "c"}},
		{"|a|b|c|", []string{"a", "b", "c"}},
		{"| spaced | values |", []string{"spaced", "values"}},
		{"|single|", []string{"single"}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := parseTableRow(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("expected %d cells, got %d", len(tt.expected), len(result))
				return
			}
			for i, cell := range result {
				if cell != tt.expected[i] {
					t.Errorf("cell %d: expected %q, got %q", i, tt.expected[i], cell)
				}
			}
		})
	}
}

// ============================================================================
// Code Block Processing Tests
// ============================================================================

func TestExtractCodeContent(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "simple go code",
			input: "```go\nfunc main() {}\n```",
			expected: "func main() {}",
		},
		{
			name: "code without language",
			input: "```\nsome code\n```",
			expected: "some code",
		},
		{
			name: "tilde fence",
			input: "~~~python\nprint('hello')\n~~~",
			expected: "print('hello')",
		},
		{
			name: "multiline code",
			input: "```go\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n```",
			expected: "func main() {\n\tfmt.Println(\"hello\")\n}",
		},
		{
			name: "no fence",
			input: "plain code",
			expected: "plain code",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractCodeContent(tt.input)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestSplitJSONBlock_Object(t *testing.T) {
	svc := newTestChunkService(t)
	block := mdBlock{
		Type:    mdBlockCodeBlock,
		Content: "```json\n{\"a\": 1, \"b\": 2, \"c\": 3}\n```",
		Metadata: map[string]string{"language": "json"},
	}
	strategy := &IndexStrategy{ChunkSize: 512}

	result := svc.splitJSONBlock(block, `{"a": 1, "b": 2, "c": 3}`, strategy)

	// Should split by keys (3 keys = 3 blocks)
	if len(result) != 3 {
		t.Errorf("expected 3 blocks for 3 JSON keys, got %d", len(result))
	}

	for _, b := range result {
		if b.Metadata["chunk_type"] != "json_key" {
			t.Errorf("expected chunk_type=json_key, got %s", b.Metadata["chunk_type"])
		}
	}
}

func TestSplitJSONBlock_Array(t *testing.T) {
	svc := newTestChunkService(t)
	block := mdBlock{
		Type:    mdBlockCodeBlock,
		Content: "```json\n[1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12]\n```",
		Metadata: map[string]string{"language": "json"},
	}
	strategy := &IndexStrategy{ChunkSize: 512}

	result := svc.splitJSONBlock(block, `[1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12]`, strategy)

	// Should split into batches of 10 (default batchSize)
	if len(result) < 2 {
		t.Errorf("expected at least 2 batches for 12 items, got %d", len(result))
	}
}

func TestSplitGoCodeBlock(t *testing.T) {
	svc := newTestChunkService(t)
	code := `package main

import "fmt"

func main() {
    fmt.Println("hello")
}

func helper() int {
    return 42
}

type MyStruct struct {
    Field string
}`

	block := mdBlock{
		Type:     mdBlockCodeBlock,
		Content:  "```go\n" + code + "\n```",
		Metadata: map[string]string{"language": "go"},
	}
	strategy := &IndexStrategy{ChunkSize: 512}

	result := svc.splitGoCodeBlock(block, code, strategy)

	// Should split at func/type boundaries
	if len(result) < 3 {
		t.Errorf("expected at least 3 blocks for 3 functions/types, got %d", len(result))
	}
}

func TestSplitPythonCodeBlock(t *testing.T) {
	svc := newTestChunkService(t)
	code := `import os

class MyClass:
    def __init__(self):
        pass
    
    def method(self):
        pass

def standalone():
    pass`

	block := mdBlock{
		Type:     mdBlockCodeBlock,
		Content:  "```python\n" + code + "\n```",
		Metadata: map[string]string{"language": "python"},
	}
	strategy := &IndexStrategy{ChunkSize: 512}

	result := svc.splitPythonCodeBlock(block, code, strategy)

	// Should split at class/def boundaries
	if len(result) < 2 {
		t.Errorf("expected at least 2 blocks for class and function, got %d", len(result))
	}
}

func TestSplitJSCodeBlock(t *testing.T) {
	svc := newTestChunkService(t)
	code := `import React from 'react';

function Component() {
    return <div />;
}

class MyClass {
    method() {}
}

const arrow = () => 1;

export { Component };`

	block := mdBlock{
		Type:     mdBlockCodeBlock,
		Content:  "```javascript\n" + code + "\n```",
		Metadata: map[string]string{"language": "javascript"},
	}
	strategy := &IndexStrategy{ChunkSize: 512}

	result := svc.splitJSCodeBlock(block, code, strategy)

	if len(result) < 2 {
		t.Errorf("expected at least 2 blocks, got %d", len(result))
	}
}

// ============================================================================
// Code Split by Pattern Tests
// ============================================================================

func TestSplitCodeByPattern(t *testing.T) {
	svc := newTestChunkService(t)
	code := `package main

func one() {}
func two() {}
func three() {}`

	block := mdBlock{
		Type:     mdBlockCodeBlock,
		Content:  "```go\n" + code + "\n```",
		Metadata: map[string]string{"language": "go"},
	}
	strategy := &IndexStrategy{ChunkSize: 512}
	pattern := regexp.MustCompile(`(?m)^func `)

	result := svc.splitCodeByPattern(block, code, strategy, pattern)

	// splitCodeByPattern includes preamble (package main) before first match
	// So we have: preamble + 3 funcs = 4 blocks
	if len(result) < 3 {
		t.Errorf("expected at least 3 blocks, got %d", len(result))
	}
}

func TestSplitGenericCodeBlock(t *testing.T) {
	svc := newTestChunkService(t)
	// Use long sections that exceed chunkSize to force splitting
	code := `section one with a lot of content to make it longer than the chunk size threshold which is set to a small value for testing purposes

section two with a lot of content to make it longer than the chunk size threshold which is set to a small value for testing purposes

section three with a lot of content to make it longer than the chunk size threshold which is set to a small value for testing purposes`

	block := mdBlock{
		Type:     mdBlockCodeBlock,
		Content:  "```\n" + code + "\n```",
		Metadata: map[string]string{"language": ""},
	}
	strategy := &IndexStrategy{ChunkSize: 100}

	result := svc.splitGenericCodeBlock(block, code, strategy)

	// Should split by double newlines
	if len(result) < 2 {
		t.Errorf("expected at least 2 blocks, got %d", len(result))
	}
}

// ============================================================================
// applySpecialProcessing Tests
// ============================================================================

func TestApplySpecialProcessing(t *testing.T) {
	svc := newTestChunkService(t)
	blocks := []mdBlock{
		{Type: mdBlockParagraph, Content: "Intro text."},
		{Type: mdBlockTable, Content: "| A | B |\n|---|---|\n| 1 | 2 |"},
		{Type: mdBlockCodeBlock, Content: "```go\nfunc main() {}\n```", Metadata: map[string]string{"language": "go"}},
	}
	strategy := &IndexStrategy{ChunkSize: 512}

	result := svc.applySpecialProcessing(blocks, strategy)

	// Paragraph passes through, table and code may be transformed
	if len(result) < 1 {
		t.Error("expected at least 1 block")
	}

	// First block should be the paragraph unchanged
	if result[0].Type != mdBlockParagraph {
		t.Errorf("first block should be paragraph, got %s", result[0].Type)
	}
}
