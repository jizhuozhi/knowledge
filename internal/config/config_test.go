package config

import (
	"testing"
)

func TestDatabaseConfig_DSN(t *testing.T) {
	tests := []struct {
		name     string
		config   DatabaseConfig
		expected string
	}{
		{
			name: "standard postgres connection",
			config: DatabaseConfig{
				Host:     "localhost",
				Port:     5432,
				User:     "postgres",
				Password: "secret",
				DBName:   "testdb",
				SSLMode:  "disable",
			},
			expected: "host=localhost port=5432 user=postgres password=secret dbname=testdb sslmode=disable",
		},
		{
			name: "with ssl mode require",
			config: DatabaseConfig{
				Host:     "db.example.com",
				Port:     5433,
				User:     "admin",
				Password: "pass123",
				DBName:   "production",
				SSLMode:  "require",
			},
			expected: "host=db.example.com port=5433 user=admin password=pass123 dbname=production sslmode=require",
		},
		{
			name: "empty password",
			config: DatabaseConfig{
				Host:     "localhost",
				Port:     5432,
				User:     "user",
				Password: "",
				DBName:   "db",
				SSLMode:  "disable",
			},
			expected: "host=localhost port=5432 user=user password= dbname=db sslmode=disable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.DSN()
			if result != tt.expected {
				t.Errorf("DSN() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestOpenSearchConfig_Address(t *testing.T) {
	tests := []struct {
		name     string
		config   OpenSearchConfig
		expected string
	}{
		{
			name: "http connection",
			config: OpenSearchConfig{
				Host:   "localhost",
				Port:   9200,
				UseSSL: false,
			},
			expected: "http://localhost:9200",
		},
		{
			name: "https connection",
			config: OpenSearchConfig{
				Host:   "search.example.com",
				Port:   443,
				UseSSL: true,
			},
			expected: "https://search.example.com:443",
		},
		{
			name: "custom port with ssl",
			config: OpenSearchConfig{
				Host:   "opensearch.internal",
				Port:   9250,
				UseSSL: true,
			},
			expected: "https://opensearch.internal:9250",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.Address()
			if result != tt.expected {
				t.Errorf("Address() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestRedisConfig_Address(t *testing.T) {
	tests := []struct {
		name     string
		config   RedisConfig
		expected string
	}{
		{
			name: "standard redis connection",
			config: RedisConfig{
				Host: "localhost",
				Port: 6379,
			},
			expected: "localhost:6379",
		},
		{
			name: "custom host and port",
			config: RedisConfig{
				Host: "redis.example.com",
				Port: 6380,
			},
			expected: "redis.example.com:6380",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.Address()
			if result != tt.expected {
				t.Errorf("Address() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestGetBedrockModelIDs(t *testing.T) {
	ids := GetBedrockModelIDs()

	tests := []struct {
		field    string
		expected string
	}{
		{"TitanEmbedTextV1", "amazon.titan-embed-text-v1"},
		{"TitanEmbedTextV2", "amazon.titan-embed-text-v2:0"},
		{"TitanTextLiteV1", "amazon.titan-text-lite-v1"},
		{"TitanTextExpressV1", "amazon.titan-text-express-v1"},
		{"NovaMicro", "amazon.nova-micro-v1:0"},
		{"NovaLite", "amazon.nova-lite-v1:0"},
		{"NovaPro", "amazon.nova-pro-v1:0"},
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			var actual string
			switch tt.field {
			case "TitanEmbedTextV1":
				actual = ids.TitanEmbedTextV1
			case "TitanEmbedTextV2":
				actual = ids.TitanEmbedTextV2
			case "TitanTextLiteV1":
				actual = ids.TitanTextLiteV1
			case "TitanTextExpressV1":
				actual = ids.TitanTextExpressV1
			case "NovaMicro":
				actual = ids.NovaMicro
			case "NovaLite":
				actual = ids.NovaLite
			case "NovaPro":
				actual = ids.NovaPro
			}
			if actual != tt.expected {
				t.Errorf("%s = %q, want %q", tt.field, actual, tt.expected)
			}
		})
	}
}

func TestConfig_Defaults(t *testing.T) {
	// Test that default values are correctly set
	cfg := &Config{
		LLM: LLMConfig{},
	}

	// Simulate default setting logic
	if cfg.LLM.AWSRegion == "" {
		cfg.LLM.AWSRegion = "us-east-1"
	}
	if cfg.LLM.EmbeddingModel == "" {
		cfg.LLM.EmbeddingModel = "amazon.titan-embed-text-v2:0"
	}
	if cfg.LLM.EmbeddingDimension == 0 {
		cfg.LLM.EmbeddingDimension = 1024
	}
	if cfg.LLM.ChatModel == "" {
		cfg.LLM.ChatModel = "amazon.nova-micro-v1:0"
	}

	if cfg.LLM.AWSRegion != "us-east-1" {
		t.Errorf("default AWSRegion should be 'us-east-1', got %s", cfg.LLM.AWSRegion)
	}
	if cfg.LLM.EmbeddingModel != "amazon.titan-embed-text-v2:0" {
		t.Errorf("default EmbeddingModel should be 'amazon.titan-embed-text-v2:0', got %s", cfg.LLM.EmbeddingModel)
	}
	if cfg.LLM.EmbeddingDimension != 1024 {
		t.Errorf("default EmbeddingDimension should be 1024, got %d", cfg.LLM.EmbeddingDimension)
	}
	if cfg.LLM.ChatModel != "amazon.nova-micro-v1:0" {
		t.Errorf("default ChatModel should be 'amazon.nova-micro-v1:0', got %s", cfg.LLM.ChatModel)
	}
}

func TestConfig_PreserveExplicitValues(t *testing.T) {
	// Test that explicit values are preserved
	cfg := &Config{
		LLM: LLMConfig{
			AWSRegion:          "eu-west-1",
			EmbeddingModel:     "custom-embedding-model",
			EmbeddingDimension: 512,
			ChatModel:          "custom-chat-model",
		},
	}

	// Simulate default setting logic (should NOT override)
	if cfg.LLM.AWSRegion == "" {
		cfg.LLM.AWSRegion = "us-east-1"
	}
	if cfg.LLM.EmbeddingModel == "" {
		cfg.LLM.EmbeddingModel = "amazon.titan-embed-text-v2:0"
	}
	if cfg.LLM.EmbeddingDimension == 0 {
		cfg.LLM.EmbeddingDimension = 1024
	}
	if cfg.LLM.ChatModel == "" {
		cfg.LLM.ChatModel = "amazon.nova-micro-v1:0"
	}

	if cfg.LLM.AWSRegion != "eu-west-1" {
		t.Errorf("AWSRegion should be preserved as 'eu-west-1', got %s", cfg.LLM.AWSRegion)
	}
	if cfg.LLM.EmbeddingModel != "custom-embedding-model" {
		t.Errorf("EmbeddingModel should be preserved, got %s", cfg.LLM.EmbeddingModel)
	}
	if cfg.LLM.EmbeddingDimension != 512 {
		t.Errorf("EmbeddingDimension should be preserved as 512, got %d", cfg.LLM.EmbeddingDimension)
	}
	if cfg.LLM.ChatModel != "custom-chat-model" {
		t.Errorf("ChatModel should be preserved, got %s", cfg.LLM.ChatModel)
	}
}

func TestConfig_StructFields(t *testing.T) {
	cfg := &Config{
		App: AppConfig{
			Name:    "knowledge-platform",
			Env:     "production",
			Port:    8080,
			Debug:   false,
			Version: "1.0.0",
		},
		Database: DatabaseConfig{
			Host:     "db.local",
			Port:     5432,
			User:     "app",
			Password: "secret",
			DBName:   "knowledge",
			SSLMode:  "require",
		},
		OpenSearch: OpenSearchConfig{
			Host:   "search.local",
			Port:   9200,
			UseSSL: true,
		},
		Neo4j: Neo4jConfig{
			URI:      "bolt://neo4j.local:7687",
			User:     "neo4j",
			Password: "password",
		},
		Redis: RedisConfig{
			Host: "redis.local",
			Port: 6379,
		},
		Tenant: TenantConfig{
			IsolationMode: "row",
		},
		Document: DocumentConfig{
			MaxFileSize:  10485760,
			UploadDir:    "/uploads",
			ChunkSize:    512,
			ChunkOverlap: 50,
		},
	}

	if cfg.App.Name != "knowledge-platform" {
		t.Errorf("App.Name should be 'knowledge-platform', got %s", cfg.App.Name)
	}
	if cfg.Database.Host != "db.local" {
		t.Errorf("Database.Host should be 'db.local', got %s", cfg.Database.Host)
	}
	if cfg.OpenSearch.Host != "search.local" {
		t.Errorf("OpenSearch.Host should be 'search.local', got %s", cfg.OpenSearch.Host)
	}
	if cfg.Neo4j.URI != "bolt://neo4j.local:7687" {
		t.Errorf("Neo4j.URI should be 'bolt://neo4j.local:7687', got %s", cfg.Neo4j.URI)
	}
	if cfg.Redis.Host != "redis.local" {
		t.Errorf("Redis.Host should be 'redis.local', got %s", cfg.Redis.Host)
	}
	if cfg.Tenant.IsolationMode != "row" {
		t.Errorf("Tenant.IsolationMode should be 'row', got %s", cfg.Tenant.IsolationMode)
	}
	if cfg.Document.ChunkSize != 512 {
		t.Errorf("Document.ChunkSize should be 512, got %d", cfg.Document.ChunkSize)
	}
}
