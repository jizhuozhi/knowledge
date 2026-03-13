package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jizhuozhi/knowledge/internal/config"
	"github.com/jizhuozhi/knowledge/internal/database"
	"github.com/jizhuozhi/knowledge/internal/handlers"
	"github.com/jizhuozhi/knowledge/internal/neo4j"
	"github.com/jizhuozhi/knowledge/internal/opensearch"
	"github.com/jizhuozhi/knowledge/internal/router"
	"github.com/jizhuozhi/knowledge/internal/services"
)

func main() {
	if err := config.Load(); err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	cfg := config.GlobalConfig

	if err := database.InitDB(cfg.Database); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer database.Close()

	if err := database.AutoMigrate(); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}
	log.Println("Database migrations completed")

	osClient, err := opensearch.NewClient(cfg.OpenSearch, cfg.LLM.EmbeddingDimension)
	if err != nil {
		log.Printf("Warning: Failed to initialize OpenSearch: %v", err)
	} else {
		log.Println("OpenSearch client initialized")
	}

	neo4jClient, err := neo4j.NewClient(cfg.Neo4j)
	if err != nil {
		log.Printf("Warning: Failed to initialize Neo4j: %v", err)
	} else {
		log.Println("Neo4j client initialized")
		defer neo4jClient.Close()
	}

	docService := services.NewDocumentService(database.GetDB(), cfg)
	ragService := services.NewRAGService(database.GetDB(), cfg)

	if osClient != nil {
		ragService.SetOpenSearchClient(osClient)
		docService.SetOpenSearchClient(osClient)
	}
	if neo4jClient != nil {
		ragService.SetNeo4jClient(neo4jClient)
		docService.SetNeo4jClient(neo4jClient)
	}

	handler := handlers.NewHandler(cfg, docService, ragService)
	r := router.SetupRouter(cfg, handler)

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.App.Port),
		Handler: r,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	log.Printf("Server started on port %d", cfg.App.Port)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited")
}
