# Knowledge Platform

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.24-00ADD8.svg)](https://go.dev/)
[![React](https://img.shields.io/badge/React-18-61DAFB.svg)](https://react.dev/)

A multi-tenant knowledge management platform with RAG (Retrieval-Augmented Generation) capabilities, powered by PostgreSQL, OpenSearch, and Neo4j.

基于 PostgreSQL + OpenSearch + Neo4j 的多租户知识管理平台，提供 RAG 检索增强生成服务。

## ✨ Features

- **Multi-tenant Isolation** — Row-level data isolation with automatic tenant context injection
- **Multi-format Document Processing** — Markdown, HTML, Word, Excel, CSV, PDF, PPT
- **Intelligent Chunking** — Markdown structure-aware chunking (tables, code blocks, lists preserved), semantic chunking, section-based chunking, sliding window, parent-child chunking
- **Syntax-aware Code/JSON Splitting** — Splits code by function/class boundaries, JSON by top-level keys
- **Table Row-level Semanticization** — Converts table rows to natural language descriptions for better retrieval
- **RAG Retrieval** — Multi-channel recall (text + vector + graph), RRF fusion, LLM reranking
- **Knowledge Graph** — Automatic entity/relation extraction with Neo4j
- **LLM-driven Strategy** — Index strategy inference via LLM (AWS Bedrock)

## 🏗 Architecture

```
┌─────────────────────────────────────────────────┐
│                   Frontend                       │
│            React 18 + Ant Design 5               │
└──────────────────────┬──────────────────────────┘
                       │ REST API
┌──────────────────────▼──────────────────────────┐
│          Backend (Go + net/http Handler)          │
│  ┌──────────┐ ┌──────────┐ ┌──────────────────┐ │
│  │ Document  │ │   RAG    │ │  Knowledge Graph │ │
│  │ Service   │ │ Service  │ │    Service       │ │
│  └────┬─────┘ └────┬─────┘ └───────┬──────────┘ │
│       │             │               │            │
│  ┌────▼─────┐ ┌────▼─────┐ ┌──────▼───────┐    │
│  │ Chunking │ │ Embedding│ │   Graph      │    │
│  │ Service  │ │ Service  │ │   Builder    │    │
│  └──────────┘ └──────────┘ └──────────────┘    │
└──────┬──────────────┬───────────────┬───────────┘
       │              │               │
┌──────▼──┐    ┌──────▼──┐    ┌──────▼──┐
│PostgreSQL│    │OpenSearch│    │  Neo4j  │
│  (RDBMS) │    │ (Search) │    │ (Graph) │
└─────────┘    └─────────┘    └─────────┘
```

## 🛠 Tech Stack

| Layer | Technology |
|-------|-----------|
| **Language** | Go 1.24, TypeScript |
| **Backend Framework** | Go 标准库 net/http（ServeMux + Handler） |
| **Frontend** | React 18 + Ant Design 5 + Vite 5 + Zustand |
| **Database** | PostgreSQL 15 |
| **Search Engine** | OpenSearch 2.11 (with IK Chinese Analyzer) |
| **Graph Database** | Neo4j 5.17 |
| **LLM Provider** | AWS Bedrock (Titan Embedding + Nova Chat) |
| **Containerization** | Docker + Docker Compose |

## 🚀 Quick Start

### Prerequisites

- Docker & Docker Compose
- Go 1.24+ (for local development)
- Node.js 18+ (for frontend development)

### Using Docker Compose

```bash
# Clone the repository
git clone https://github.com/georgeji/knowledge-platform.git
cd knowledge-platform

# Copy and configure environment
cp .env.example .env
# Edit .env — set your AWS credentials and database passwords

# Start all services
make docker-up

# Wait for services to be healthy, then initialize indices
make init-all
```

**Access:**
- Frontend: http://localhost
- API: http://localhost:8080/api/v1
- OpenSearch: http://localhost:9200
- Neo4j Browser: http://localhost:7474

### Local Development

```bash
# Install dependencies
make install-deps

# Start infrastructure services only
docker compose up -d postgres opensearch neo4j

# Initialize indices
make init-all

# Run backend (terminal 1)
make dev-backend

# Run frontend (terminal 2)
make dev-frontend
```

## 📖 API Reference

### Tenants

```bash
# Create a tenant
POST /api/v1/admin/tenants
{
  "name": "Engineering Team",
  "code": "eng-team"
}
```

### Knowledge Bases

```bash
# Create a knowledge base
POST /api/v1/knowledge-bases
Headers: X-Tenant-ID: <tenant_id>
{
  "name": "Technical Docs"
}

# List knowledge bases
GET /api/v1/knowledge-bases
```

### Documents

```bash
# Create a document
POST /api/v1/documents
Headers: X-Tenant-ID: <tenant_id>
{
  "title": "API Design Guide",
  "content": "...",
  "doc_type": "knowledge",
  "format": "markdown"
}

# Upload a file
POST /api/v1/documents/upload
Headers: X-Tenant-ID: <tenant_id>
Content-Type: multipart/form-data

# Process (index) a document
POST /api/v1/documents/{id}/process

# List documents
GET /api/v1/documents?keyword=API&page=1&page_size=20

# Get document details
GET /api/v1/documents/{id}

# Delete a document
DELETE /api/v1/documents/{id}
```

### RAG Query

```bash
POST /api/v1/rag/query
Headers: X-Tenant-ID: <tenant_id>
{
  "query": "How to design a microservice architecture?",
  "top_k": 10,
  "hybrid_weight": 0.5,
  "include_graph": true
}
```

## 📁 Project Structure

```
knowledge-platform/
├── cmd/
│   └── server/              # Application entrypoint
│       └── main.go
├── internal/
│   ├── config/              # Configuration management
│   ├── database/            # Database connection
│   ├── handlers/            # HTTP handlers
│   ├── middleware/           # Middleware (multi-tenancy, auth)
│   ├── models/              # Data models
│   ├── neo4j/               # Neo4j client
│   ├── opensearch/          # OpenSearch client
│   ├── router/              # Route definitions
│   └── services/
│       ├── chunk.go         # Document chunking (structure-aware)
│       ├── document.go      # Document processing (multi-format)
│       ├── embedding.go     # Embedding & LLM integration
│       ├── graph.go         # Knowledge graph operations
│       └── rag.go           # RAG retrieval pipeline
├── frontend/                # React frontend
│   ├── src/
│   │   ├── api/             # API client
│   │   ├── components/      # UI components
│   │   ├── layouts/         # Page layouts
│   │   ├── pages/           # Pages
│   │   ├── stores/          # State management (Zustand)
│   │   └── types/           # TypeScript types
│   └── package.json
├── docker-compose.yml       # Container orchestration
├── Dockerfile       # Backend container
├── Dockerfile.opensearch    # OpenSearch with IK analyzer
├── Makefile                 # Build & dev commands
├── .env.example             # Configuration template
├── rag-index-design.md      # Index design document
└── rag-retrieval-design.md  # Retrieval design document
```

## 📐 Design Documents

For detailed system design, see:
- [Index Design](rag-index-design.md) — Document processing, chunking strategies, index types
- [Retrieval Design](rag-retrieval-design.md) — Query understanding, multi-channel recall, fusion ranking

## ⚙️ Configuration

All configuration is managed via environment variables. See [`.env.example`](.env.example) for the full list.

Key variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `APP_PORT` | Application port | `8080` |
| `DB_HOST` | PostgreSQL host | `localhost` |
| `OPENSEARCH_HOST` | OpenSearch host | `localhost` |
| `NEO4J_URI` | Neo4j connection URI | `bolt://localhost:7687` |
| `AWS_REGION` | AWS region for Bedrock | `us-east-1` |
| `EMBEDDING_MODEL` | Embedding model ID | `amazon.titan-embed-text-v2:0` |
| `CHAT_MODEL` | Chat model ID | `amazon.nova-micro-v1:0` |
| `CHUNK_SIZE` | Default chunk size (tokens) | `500` |

## 🤝 Contributing

Contributions are welcome! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## 📄 License

This project is licensed under the MIT License — see the [LICENSE](LICENSE) file for details.
