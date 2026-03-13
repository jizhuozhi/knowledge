package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	App       AppConfig
	Database  DatabaseConfig
	OpenSearch OpenSearchConfig
	Neo4j     Neo4jConfig
	Redis     RedisConfig
	LLM       LLMConfig
	Tenant    TenantConfig
	Document  DocumentConfig
}

type AppConfig struct {
	Name      string
	Env       string
	Port      int
	Debug     bool
	Version   string
	StaticDir string // Frontend static files directory
}

type DatabaseConfig struct {
	Host         string
	Port         int
	User         string
	Password     string
	DBName       string
	SSLMode      string
	MaxOpenConns int
	MaxIdleConns int
}

func (c DatabaseConfig) DSN() string {
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.DBName, c.SSLMode)
}

type OpenSearchConfig struct {
	Host        string
	Port        int
	User        string
	Password    string
	UseSSL      bool
	VerifyCerts bool
}

func (c OpenSearchConfig) Address() string {
	protocol := "http"
	if c.UseSSL {
		protocol = "https"
	}
	return fmt.Sprintf("%s://%s:%d", protocol, c.Host, c.Port)
}

type Neo4jConfig struct {
	URI      string
	User     string
	Password string
}

type RedisConfig struct {
	Host     string
	Port     int
	Password string
	DB       int
}

func (c RedisConfig) Address() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// LLMConfig configures AWS Bedrock as the sole LLM provider
type LLMConfig struct {
	// Provider is always "bedrock" (kept for config compatibility)
	Provider string

	// AWS Bedrock
	AWSRegion          string
	AWSAccessKeyID     string
	AWSSecretAccessKey string
	AWSSessionToken    string // Optional, for temporary credentials

	// Model configuration
	EmbeddingModel     string
	EmbeddingDimension int
	ChatModel          string
}

// BedrockModelIDs defines available models in AWS Bedrock
type BedrockModelIDs struct {
	// Titan Embedding models
	TitanEmbedTextV1 string // amazon.titan-embed-text-v1
	TitanEmbedTextV2 string // amazon.titan-embed-text-v2:0
	
	// Titan Text models
	TitanTextLiteV1    string // amazon.titan-text-lite-v1
	TitanTextExpressV1 string // amazon.titan-text-express-v1
	
	// Amazon Nova models (next-gen foundation models)
	NovaMicro string // amazon.nova-micro-v1:0 - fast, text only
	NovaLite  string // amazon.nova-lite-v1:0 - multimodal, cost-effective
	NovaPro   string // amazon.nova-pro-v1:0 - multimodal, most capable
}

// GetBedrockModelIDs returns model IDs for Bedrock
func GetBedrockModelIDs() BedrockModelIDs {
	return BedrockModelIDs{
		TitanEmbedTextV1:    "amazon.titan-embed-text-v1",
		TitanEmbedTextV2:    "amazon.titan-embed-text-v2:0",
		TitanTextLiteV1:     "amazon.titan-text-lite-v1",
		TitanTextExpressV1:  "amazon.titan-text-express-v1",
		NovaMicro:           "amazon.nova-micro-v1:0",
		NovaLite:            "amazon.nova-lite-v1:0",
		NovaPro:             "amazon.nova-pro-v1:0",
	}
}

type TenantConfig struct {
	IsolationMode string // row, schema, database
}

type DocumentConfig struct {
	MaxFileSize  int64
	UploadDir    string
	ChunkSize    int
	ChunkOverlap int
}

var GlobalConfig *Config

func Load() error {
	viper.SetConfigName(".env")
	viper.SetConfigType("env")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./..")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return err
		}
	}

	GlobalConfig = &Config{
		App: AppConfig{
			Name:      viper.GetString("APP_NAME"),
			Env:       viper.GetString("APP_ENV"),
			Port:      viper.GetInt("APP_PORT"),
			Debug:     viper.GetBool("DEBUG"),
			Version:   viper.GetString("APP_VERSION"),
			StaticDir: viper.GetString("STATIC_DIR"),
		},
		Database: DatabaseConfig{
			Host:         viper.GetString("DB_HOST"),
			Port:         viper.GetInt("DB_PORT"),
			User:         viper.GetString("DB_USER"),
			Password:     viper.GetString("DB_PASSWORD"),
			DBName:       viper.GetString("DB_NAME"),
			SSLMode:      viper.GetString("DB_SSL_MODE"),
			MaxOpenConns: viper.GetInt("DB_MAX_OPEN_CONNS"),
			MaxIdleConns: viper.GetInt("DB_MAX_IDLE_CONNS"),
		},
		OpenSearch: OpenSearchConfig{
			Host:        viper.GetString("OPENSEARCH_HOST"),
			Port:        viper.GetInt("OPENSEARCH_PORT"),
			User:        viper.GetString("OPENSEARCH_USER"),
			Password:    viper.GetString("OPENSEARCH_PASSWORD"),
			UseSSL:      viper.GetBool("OPENSEARCH_USE_SSL"),
			VerifyCerts: viper.GetBool("OPENSEARCH_VERIFY_CERTS"),
		},
		Neo4j: Neo4jConfig{
			URI:      viper.GetString("NEO4J_URI"),
			User:     viper.GetString("NEO4J_USER"),
			Password: viper.GetString("NEO4J_PASSWORD"),
		},
		Redis: RedisConfig{
			Host:     viper.GetString("REDIS_HOST"),
			Port:     viper.GetInt("REDIS_PORT"),
			Password: viper.GetString("REDIS_PASSWORD"),
			DB:       viper.GetInt("REDIS_DB"),
		},
		LLM: LLMConfig{
			Provider:           "bedrock",
			AWSRegion:          viper.GetString("AWS_REGION"),
			AWSAccessKeyID:     viper.GetString("AWS_ACCESS_KEY_ID"),
			AWSSecretAccessKey: viper.GetString("AWS_SECRET_ACCESS_KEY"),
			AWSSessionToken:    viper.GetString("AWS_SESSION_TOKEN"),
			EmbeddingModel:     viper.GetString("EMBEDDING_MODEL"),
			EmbeddingDimension: viper.GetInt("EMBEDDING_DIMENSION"),
			ChatModel:          viper.GetString("CHAT_MODEL"),
		},
		Tenant: TenantConfig{
			IsolationMode: viper.GetString("TENANT_ISOLATION_MODE"),
		},
		Document: DocumentConfig{
			MaxFileSize:  viper.GetInt64("MAX_FILE_SIZE"),
			UploadDir:    viper.GetString("UPLOAD_DIR"),
			ChunkSize:    viper.GetInt("CHUNK_SIZE"),
			ChunkOverlap: viper.GetInt("CHUNK_OVERLAP"),
		},
	}

	// Set defaults
	if GlobalConfig.LLM.AWSRegion == "" {
		GlobalConfig.LLM.AWSRegion = "us-east-1"
	}
	if GlobalConfig.LLM.EmbeddingModel == "" {
		GlobalConfig.LLM.EmbeddingModel = "amazon.titan-embed-text-v2:0"
	}
	if GlobalConfig.LLM.EmbeddingDimension == 0 {
		GlobalConfig.LLM.EmbeddingDimension = 1024 // Titan Embed V2 dimension
	}
	if GlobalConfig.LLM.ChatModel == "" {
		GlobalConfig.LLM.ChatModel = "amazon.nova-micro-v1:0"
	}

	return nil
}

func GetDuration(key string, defaultValue time.Duration) time.Duration {
	if !viper.IsSet(key) {
		return defaultValue
	}
	return viper.GetDuration(key)
}
