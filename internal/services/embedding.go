package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/jizhuozhi/knowledge/internal/config"
	"github.com/jizhuozhi/knowledge/internal/models"
	"gorm.io/gorm"
)

// LLMUsage represents token usage from a single LLM API call
type LLMUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// LLMProvider defines the interface for LLM providers
type LLMProvider interface {
	ChatCompletion(ctx context.Context, prompt string) (string, *LLMUsage, error)
	ChatCompletionWithSystem(ctx context.Context, systemPrompt, userPrompt string) (string, *LLMUsage, error)
	GenerateEmbedding(ctx context.Context, text string) ([]float32, *LLMUsage, error)
	GenerateEmbeddings(ctx context.Context, texts []string) ([][]float32, *LLMUsage, error)
}

// LLMUsageTracker records LLM usage to the database
type LLMUsageTracker struct {
	db     *gorm.DB
	config *config.Config
}

// NewLLMUsageTracker creates a new usage tracker
func NewLLMUsageTracker(db *gorm.DB, cfg *config.Config) *LLMUsageTracker {
	return &LLMUsageTracker{db: db, config: cfg}
}

// bedrockPricing returns per-token price in USD for a given model
// Prices are approximate as of 2025.
func bedrockPricing(modelID, modelType string) (inputPrice, outputPrice float64) {
	normalizedID := strings.TrimSpace(modelID)
	// Strip cross-region prefixes like "us.", "eu.", "global."
	for _, prefix := range []string{"us.", "eu.", "global."} {
		normalizedID = strings.TrimPrefix(normalizedID, prefix)
	}

	switch {
	// Titan Embeddings
	case strings.Contains(normalizedID, "titan-embed"):
		return 0.0001 / 1000, 0 // $0.0001/1K tokens, no output
	// Nova models
	case normalizedID == "amazon.nova-micro-v1:0":
		return 0.000035 / 1000, 0.00014 / 1000
	case normalizedID == "amazon.nova-lite-v1:0":
		return 0.00006 / 1000, 0.00024 / 1000
	case normalizedID == "amazon.nova-pro-v1:0":
		return 0.0008 / 1000, 0.0032 / 1000
	case normalizedID == "amazon.nova-premier-v1:0":
		return 0.001 / 1000, 0.004 / 1000
	// Titan Text
	case strings.Contains(normalizedID, "titan-text-lite"):
		return 0.0003 / 1000, 0.0004 / 1000
	case strings.Contains(normalizedID, "titan-text-express"):
		return 0.0008 / 1000, 0.0016 / 1000
	case strings.Contains(normalizedID, "titan-text-premier"):
		return 0.0005 / 1000, 0.0015 / 1000
	default:
		return 0.001 / 1000, 0.002 / 1000 // conservative default
	}
}

// RecordUsage records a single LLM usage event
func (t *LLMUsageTracker) RecordUsage(ctx context.Context, tenantID string, documentID, knowledgeBaseID *string,
	callerService, callerMethod, modelID, modelType string,
	usage *LLMUsage, durationMs int64, errMsg string) {

	if t == nil || t.db == nil {
		return
	}

	status := "success"
	if errMsg != "" {
		status = "error"
	}

	inputTokens := 0
	outputTokens := 0
	totalTokens := 0
	if usage != nil {
		inputTokens = usage.InputTokens
		outputTokens = usage.OutputTokens
		totalTokens = usage.TotalTokens
	}

	inputPrice, outputPrice := bedrockPricing(modelID, modelType)
	estimatedCost := float64(inputTokens)*inputPrice + float64(outputTokens)*outputPrice

	record := &models.LLMUsageRecord{
		TenantModel:     models.TenantModel{TenantID: tenantID},
		DocumentID:      documentID,
		KnowledgeBaseID: knowledgeBaseID,
		CallerService:   callerService,
		CallerMethod:    callerMethod,
		ModelID:         modelID,
		ModelType:       modelType,
		InputTokens:     inputTokens,
		OutputTokens:    outputTokens,
		TotalTokens:     totalTokens,
		EstimatedCost:   estimatedCost,
		DurationMs:      durationMs,
		Status:          status,
		ErrorMsg:        errMsg,
	}

	// Fire and forget — don't block the caller
	go func() {
		if err := t.db.Create(record).Error; err != nil {
			fmt.Printf("Warning: failed to record LLM usage: %v\n", err)
		}
	}()
}

// EmbeddingService handles text embedding generation
type EmbeddingService struct {
	config   *config.Config
	provider LLMProvider
}

// NewEmbeddingService creates a new embedding service (Bedrock only)
func NewEmbeddingService(cfg *config.Config) *EmbeddingService {
	return &EmbeddingService{
		config:   cfg,
		provider: NewBedrockProvider(cfg),
	}
}

// --- AWS Bedrock Provider ---

// BedrockProvider implements LLMProvider using AWS Bedrock
type BedrockProvider struct {
	client *bedrockruntime.Client
	config *config.Config
}

// NewBedrockProvider creates a new Bedrock provider
func NewBedrockProvider(cfg *config.Config) *BedrockProvider {
	var opts []func(*awsconfig.LoadOptions) error

	// Use static credentials if provided
	if cfg.LLM.AWSAccessKeyID != "" && cfg.LLM.AWSSecretAccessKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				cfg.LLM.AWSAccessKeyID,
				cfg.LLM.AWSSecretAccessKey,
				cfg.LLM.AWSSessionToken,
			),
		))
	}

	// Set region
	opts = append(opts, awsconfig.WithRegion(cfg.LLM.AWSRegion))

	// Load AWS config
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		panic(fmt.Sprintf("failed to load AWS config: %v", err))
	}

	if creds, credErr := awsCfg.Credentials.Retrieve(context.Background()); credErr != nil {
		fmt.Printf("Bedrock credentials resolve failed (region=%s): %v\n", awsCfg.Region, credErr)
	} else {
		fmt.Printf("Bedrock credentials source=%s region=%s\n", creds.Source, awsCfg.Region)
	}

	return &BedrockProvider{
		client: bedrockruntime.NewFromConfig(awsCfg),
		config: cfg,
	}
}

// --- Titan Embedding ---

// TitanEmbeddingRequest for Titan embedding models
type TitanEmbeddingRequest struct {
	InputText string `json:"inputText"`
}

// TitanEmbeddingResponse for Titan embedding models
type TitanEmbeddingResponse struct {
	Embedding           []float32 `json:"embedding"`
	InputTextTokenCount int       `json:"inputTextTokenCount"`
}

func (p *BedrockProvider) GenerateEmbedding(ctx context.Context, text string) ([]float32, *LLMUsage, error) {
	modelID := strings.TrimSpace(p.config.LLM.EmbeddingModel)

	req := TitanEmbeddingRequest{InputText: text}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal embedding request: %w", err)
	}

	resp, err := p.client.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
		ModelId:     aws.String(modelID),
		ContentType: aws.String("application/json"),
		Body:        body,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to invoke embedding model %s: %w", modelID, err)
	}

	var titanResp TitanEmbeddingResponse
	if err := json.Unmarshal(resp.Body, &titanResp); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal embedding response: %w", err)
	}
	if len(titanResp.Embedding) == 0 {
		return nil, nil, fmt.Errorf("empty embedding from model %s", modelID)
	}

	usage := &LLMUsage{
		InputTokens: titanResp.InputTextTokenCount,
		TotalTokens: titanResp.InputTextTokenCount,
	}

	return titanResp.Embedding, usage, nil
}

func (p *BedrockProvider) GenerateEmbeddings(ctx context.Context, texts []string) ([][]float32, *LLMUsage, error) {
	if len(texts) == 0 {
		return nil, &LLMUsage{}, nil
	}

	totalUsage := &LLMUsage{}
	embeddings := make([][]float32, len(texts))
	for i, text := range texts {
		embedding, usage, err := p.GenerateEmbedding(ctx, text)
		if err != nil {
			return nil, totalUsage, fmt.Errorf("failed to generate embedding for text %d: %w", i, err)
		}
		embeddings[i] = embedding
		if usage != nil {
			totalUsage.InputTokens += usage.InputTokens
			totalUsage.TotalTokens += usage.TotalTokens
		}
	}

	return embeddings, totalUsage, nil
}

// --- Titan / Nova Chat ---

// TitanTextRequest for Titan text models
type TitanTextRequest struct {
	InputText            string `json:"inputText"`
	TextGenerationConfig struct {
		Temperature   float64 `json:"temperature"`
		TopP          float64 `json:"topP"`
		MaxTokenCount int     `json:"maxTokenCount"`
	} `json:"textGenerationConfig"`
}

// TitanTextResponse for Titan text models
type TitanTextResponse struct {
	InputTextTokenCount int `json:"inputTextTokenCount"`
	Results             []struct {
		OutputText string `json:"outputText"`
		TokenCount int    `json:"tokenCount"`
	} `json:"results"`
}

// NovaMessage for Amazon Nova models
type NovaMessage struct {
	Role    string        `json:"role"`
	Content []NovaContent `json:"content"`
}

type NovaContent struct {
	Text string `json:"text"`
}

// NovaResponse for Amazon Nova models
type NovaResponse struct {
	Output struct {
		Message struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	} `json:"output"`
	Usage struct {
		InputTokens  int `json:"inputTokens"`
		OutputTokens int `json:"outputTokens"`
		TotalTokens  int `json:"totalTokens"`
	} `json:"usage"`
}

// isNovaModel checks if the model is a Nova model
func isNovaModel(modelID string) bool {
	id := strings.TrimSpace(modelID)
	return id == "amazon.nova-micro-v1:0" ||
		id == "amazon.nova-lite-v1:0" ||
		id == "amazon.nova-pro-v1:0" ||
		id == "amazon.nova-premier-v1:0" ||
		strings.HasPrefix(id, "us.amazon.nova-") ||
		strings.HasPrefix(id, "eu.amazon.nova-") ||
		strings.HasPrefix(id, "global.amazon.nova-")
}

// isTitanTextModel checks if the model is a Titan text model
func isTitanTextModel(modelID string) bool {
	return modelID == "amazon.titan-text-lite-v1" ||
		modelID == "amazon.titan-text-express-v1" ||
		modelID == "amazon.titan-text-premier-v1:0"
}

func (p *BedrockProvider) ChatCompletion(ctx context.Context, prompt string) (string, *LLMUsage, error) {
	modelID := p.config.LLM.ChatModel

	if isNovaModel(modelID) {
		return p.novaChatCompletion(ctx, "", prompt)
	}

	if isTitanTextModel(modelID) {
		return p.titanChatCompletion(ctx, prompt)
	}

	return "", nil, fmt.Errorf("unsupported chat model: %s", modelID)
}

func (p *BedrockProvider) ChatCompletionWithSystem(ctx context.Context, systemPrompt, userPrompt string) (string, *LLMUsage, error) {
	modelID := p.config.LLM.ChatModel

	if isNovaModel(modelID) {
		return p.novaChatCompletion(ctx, systemPrompt, userPrompt)
	}

	if isTitanTextModel(modelID) {
		fullPrompt := userPrompt
		if systemPrompt != "" {
			fullPrompt = systemPrompt + "\n\n" + userPrompt
		}
		return p.titanChatCompletion(ctx, fullPrompt)
	}

	return "", nil, fmt.Errorf("unsupported chat model: %s", modelID)
}

func (p *BedrockProvider) novaChatCompletion(ctx context.Context, systemPrompt, userPrompt string) (string, *LLMUsage, error) {
	messages := []NovaMessage{
		{
			Role:    "user",
			Content: []NovaContent{{Text: userPrompt}},
		},
	}

	reqBody := map[string]interface{}{
		"messages": messages,
		"inferenceConfig": map[string]interface{}{
			"maxTokens":   4096,
			"temperature": 0.3,
			"topP":        0.9,
		},
	}

	if systemPrompt != "" {
		reqBody["system"] = []map[string]string{
			{"text": systemPrompt},
		}
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := p.client.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
		ModelId:     aws.String(p.config.LLM.ChatModel),
		ContentType: aws.String("application/json"),
		Body:        body,
	})
	if err != nil {
		return "", nil, fmt.Errorf("failed to invoke Nova model: %w", err)
	}

	var novaResp NovaResponse
	if err := json.Unmarshal(resp.Body, &novaResp); err != nil {
		return "", nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(novaResp.Output.Message.Content) == 0 {
		return "", nil, fmt.Errorf("no response from Nova")
	}

	usage := &LLMUsage{
		InputTokens:  novaResp.Usage.InputTokens,
		OutputTokens: novaResp.Usage.OutputTokens,
		TotalTokens:  novaResp.Usage.TotalTokens,
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}

	return novaResp.Output.Message.Content[0].Text, usage, nil
}

func (p *BedrockProvider) titanChatCompletion(ctx context.Context, prompt string) (string, *LLMUsage, error) {
	req := TitanTextRequest{
		InputText: prompt,
	}
	req.TextGenerationConfig.Temperature = 0.3
	req.TextGenerationConfig.TopP = 0.9
	req.TextGenerationConfig.MaxTokenCount = 4096

	body, err := json.Marshal(req)
	if err != nil {
		return "", nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := p.client.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
		ModelId:     aws.String(p.config.LLM.ChatModel),
		ContentType: aws.String("application/json"),
		Body:        body,
	})
	if err != nil {
		return "", nil, fmt.Errorf("failed to invoke model: %w", err)
	}

	var titanResp TitanTextResponse
	if err := json.Unmarshal(resp.Body, &titanResp); err != nil {
		return "", nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(titanResp.Results) == 0 {
		return "", nil, fmt.Errorf("no response from Titan")
	}

	outputTokens := 0
	if len(titanResp.Results) > 0 {
		outputTokens = titanResp.Results[0].TokenCount
	}
	usage := &LLMUsage{
		InputTokens:  titanResp.InputTextTokenCount,
		OutputTokens: outputTokens,
		TotalTokens:  titanResp.InputTextTokenCount + outputTokens,
	}

	return titanResp.Results[0].OutputText, usage, nil
}

// --- EmbeddingService methods ---

// GenerateEmbedding generates embedding for a single text
func (s *EmbeddingService) GenerateEmbedding(ctx context.Context, text string) ([]float32, *LLMUsage, error) {
	return s.provider.GenerateEmbedding(ctx, text)
}

// GenerateEmbeddings generates embeddings for multiple texts
func (s *EmbeddingService) GenerateEmbeddings(ctx context.Context, texts []string) ([][]float32, *LLMUsage, error) {
	return s.provider.GenerateEmbeddings(ctx, texts)
}

// ChatCompletion generates chat completion
func (s *EmbeddingService) ChatCompletion(ctx context.Context, prompt string) (string, *LLMUsage, error) {
	return s.provider.ChatCompletion(ctx, prompt)
}

// ChatCompletionWithSystem generates chat completion with system prompt
func (s *EmbeddingService) ChatCompletionWithSystem(ctx context.Context, systemPrompt, userPrompt string) (string, *LLMUsage, error) {
	return s.provider.ChatCompletionWithSystem(ctx, systemPrompt, userPrompt)
}

// EmbedChunks generates embeddings for document chunks
func (s *EmbeddingService) EmbedChunks(ctx context.Context, chunks []models.Chunk) (*LLMUsage, error) {
	totalUsage := &LLMUsage{}
	batchSize := 100
	for i := 0; i < len(chunks); i += batchSize {
		end := i + batchSize
		if end > len(chunks) {
			end = len(chunks)
		}

		batch := chunks[i:end]
		texts := make([]string, len(batch))
		for j, chunk := range batch {
			texts[j] = chunk.Content
		}

		embeddings, usage, err := s.GenerateEmbeddings(ctx, texts)
		if err != nil {
			return totalUsage, err
		}
		if usage != nil {
			totalUsage.InputTokens += usage.InputTokens
			totalUsage.OutputTokens += usage.OutputTokens
			totalUsage.TotalTokens += usage.TotalTokens
		}

		for j := range embeddings {
			batch[j].VectorID = batch[j].ID
		}
	}

	return totalUsage, nil
}

// UnderstandQuery uses LLM to understand user query
type QueryUnderstanding struct {
	Intent     string                 `json:"intent"`
	Entities   []string               `json:"entities"`
	TimeFilter map[string]interface{} `json:"time_filter,omitempty"`
	TypeFilter string                 `json:"type_filter,omitempty"`
	Keywords   []string               `json:"keywords"`
	Routing    string                 `json:"routing"`
	Rewritten  string                 `json:"rewritten,omitempty"`
}

func (s *EmbeddingService) UnderstandQuery(ctx context.Context, query string) (*QueryUnderstanding, *LLMUsage, error) {
	prompt := fmt.Sprintf(`Analyze the following search query and extract structured information.

Query: %s

Return a JSON object with:
{
  "intent": "what the user wants to do",
  "entities": ["important concepts mentioned"],
  "time_filter": {"field": "created_at", "range": "last_week"} or null,
  "type_filter": "document type if mentioned" or null,
  "keywords": ["key search terms"],
  "routing": "knowledge_base" | "external" | "hybrid",
  "rewritten": "improved query if needed" or null
}

Routing decision:
- Use "knowledge_base" for internal knowledge, technical docs, historical data
- Use "external" for real-time info, industry news, public knowledge
- Use "hybrid" for comparisons, combining internal and external info

Return only the JSON object.`, query)

	response, usage, err := s.provider.ChatCompletion(ctx, prompt)
	if err != nil {
		return nil, usage, err
	}

	var understanding QueryUnderstanding
	if err := json.Unmarshal([]byte(response), &understanding); err != nil {
		return nil, usage, fmt.Errorf("failed to parse query understanding: %w", err)
	}

	return &understanding, usage, nil
}

// RetrievalStrategy defines the retrieval strategy
type RetrievalStrategy struct {
	Channels     map[string]float64     `json:"channels"`
	TopK         int                    `json:"top_k"`
	Filters      map[string]interface{} `json:"filters,omitempty"`
	HybridWeight float64                `json:"hybrid_weight"`
}

func (s *EmbeddingService) DetermineRetrievalStrategy(ctx context.Context, understanding *QueryUnderstanding) (*RetrievalStrategy, *LLMUsage, error) {
	prompt := fmt.Sprintf(`Determine the best retrieval strategy based on the query understanding.

Query Understanding:
%s

Return a JSON object with:
{
  "channels": {
    "text": 0.0-1.0,
    "vector": 0.0-1.0,
    "graph": 0.0-1.0
  },
  "top_k": 10-100,
  "filters": {} or null,
  "hybrid_weight": 0.3-0.7
}

Guidelines:
- For precise queries (error codes, specific terms): higher text weight
- For conceptual queries: higher vector weight
- For relationship queries: higher graph weight
- For exploration: balanced weights

Return only the JSON object.`, mustMarshal(understanding))

	response, usage, err := s.provider.ChatCompletion(ctx, prompt)
	if err != nil {
		return &RetrievalStrategy{
			Channels: map[string]float64{
				"text":   0.5,
				"vector": 0.5,
				"graph":  0.3,
			},
			TopK:         20,
			HybridWeight: 0.5,
		}, usage, nil
	}

	var strategy RetrievalStrategy
	if err := json.Unmarshal([]byte(response), &strategy); err != nil {
		return &RetrievalStrategy{
			Channels: map[string]float64{
				"text":   0.5,
				"vector": 0.5,
				"graph":  0.3,
			},
			TopK:         20,
			HybridWeight: 0.5,
		}, usage, nil
	}

	return &strategy, usage, nil
}

func mustMarshal(v interface{}) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}
