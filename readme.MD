# The personal website of Jacob Henning

A [personal blog](https://jake-henning.com) built with Go, featuring automated deployment and observability.

## Features

- **Go HTTP Server** 
- **Git-based Content** - Markdown files auto-converted to HTML
- **Real-time Updates** via HTMX
- **OpenTelemetry** tracing and metrics
- **AWS Lightsail** cheap and predicatable costing
- **Automated CI/CD** with GitHub Actions
- **HTTPS/TLS** with Let's Encrypt
- **Load Testing** with K6


## Quick Start

### Local Development
```bash
export BLOG_REPO_URL=dummy-url        # Dummy value can be anything
export BLOG_REPO_PRIV_KEY=dummy-key   # Dummy value can be anything
export BLOG_SERVER_PORT=8080
export BLOG_LOCAL_ONLY=true           # Do not clone a repo
export BLOG_CONTENT_DIR=./content     # Folder with markdown files i.e posts
go run cmd/jakeserver.go
```

## Building

```bash
# Binary
go build -o jake-blog ./cmd

# Docker image
docker build --build-arg GO_VERSION=$(cat .go-version) -f build/package/Dockerfile .
```

## Project Structure

```
├── cmd/                  # Application entry points
├── internal/blog/        # Core blog logic
├── deployments/          # Infrastructure (Terraform/Ansible)
├── integration_test/     # Integration and fuzzing tests
├── scripts/k6/           # Load testing
├── web/                  # Static assets
└── .github/workflows/    # CI/CD
```

## Testing

```bash
# Integration tests  
go test ./integration_test

# Load testing
k6 run -e TEST_PROFILE=gentle scripts/k6/load.js

# Security scanning
govulncheck ./internal/blog
gosec ./internal/blog
```

## Bumping the Go Version

Update `.go-version` at the repo root, then run:

```bash
go mod edit -go=$(cat .go-version)
go mod tidy
```

The Dockerfile and CI workflow read `.go-version` automatically.

## Deployment

Push to `main` triggers:
1. **Staging** deployment and testing
2. **Production** deployment (if staging passes)


## Monitoring

- **Metrics**: `/telemetry/metric` - uptime, request latency, memory usage
- **Tracing**: `/telemetry/trace` - distributed request tracing
- **RSS Feed**: `/feed/`

