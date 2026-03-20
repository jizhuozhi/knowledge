# Contributing to Knowledge Platform

Thank you for your interest in contributing! This document provides guidelines and information about contributing to this project.

## Development Setup

### Prerequisites

- Go 1.24+
- Node.js 18+
- Docker & Docker Compose

### Getting Started

```bash
# Clone the repo
git clone https://github.com/georgeji/knowledge-platform.git
cd knowledge-platform

# Install dependencies
make install-deps

# Copy and configure environment
cp .env.example .env
# Edit .env with your settings

# Start infrastructure
docker compose up -d postgres opensearch neo4j

# Initialize indices
make init-all

# Run in development mode
make dev
```

## Project Structure

- `cmd/server/` — Application entrypoint
- `internal/` — Core application code (not importable by external packages)
  - `config/` — Configuration management
  - `database/` — Database connection and migrations
  - `handlers/` — HTTP request handlers
  - `middleware/` — HTTP middleware
  - `models/` — Data models and types
  - `services/` — Business logic
  - `router/` — Route definitions
  - `opensearch/` — OpenSearch client
  - `neo4j/` — Neo4j client
- `frontend/` — React frontend application

## How to Contribute

### Reporting Bugs

- Use the [GitHub Issues](../../issues) page
- Include steps to reproduce, expected vs actual behavior
- Include Go version, OS, and relevant configuration

### Suggesting Features

- Open a GitHub Issue with the `enhancement` label
- Describe the use case and expected behavior

### Submitting Changes

1. **Fork** the repository
2. **Create a branch** from `main`:
   ```bash
   git checkout -b feature/your-feature
   ```
3. **Make your changes** following the coding guidelines below
4. **Test** your changes:
   ```bash
   make test
   ```
5. **Commit** with a clear message:
   ```bash
   git commit -m "feat: add support for PDF table extraction"
   ```
6. **Push** and create a **Pull Request**

### Commit Message Convention

We follow [Conventional Commits](https://www.conventionalcommits.org/):

| Prefix | Usage |
|--------|-------|
| `feat:` | New feature |
| `fix:` | Bug fix |
| `docs:` | Documentation only |
| `refactor:` | Code refactoring (no feature change) |
| `test:` | Adding or updating tests |
| `chore:` | Build process, tooling, dependencies |
| `perf:` | Performance improvement |

### Coding Guidelines

**Go:**
- Follow standard Go conventions (`gofmt`, `go vet`)
- Use meaningful variable and function names
- Add comments for exported functions and types
- Keep functions focused and reasonably sized
- Handle errors explicitly — don't ignore them

**TypeScript/React:**
- Follow the existing ESLint configuration
- Use TypeScript types — avoid `any`
- Keep components focused and composable

**General:**
- Keep PRs focused on a single concern
- Update documentation when changing behavior
- Add tests for new features when applicable

## Code of Conduct

Please be respectful and constructive in all interactions. We are committed to providing a welcoming and inclusive experience for everyone.

## Questions?

Feel free to open a GitHub Issue for any questions about contributing.
