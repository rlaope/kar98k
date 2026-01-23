# Project Instructions for Claude

## Git Operations

**IMPORTANT: Always ask user for confirmation before running `git commit` or `git push` commands. Never commit or push without explicit user approval.**

## Project Overview

kar98k is a high-intensity irregular traffic simulation tool written in Go. It generates realistic, irregular traffic patterns for load testing HTTP/1.1, HTTP/2, and gRPC services.

## Build Commands

```bash
make build       # Build kar CLI
make server      # Build demo server
make run-server  # Run demo server
make test        # Run tests
```

## Key Directories

- `cmd/kar98k/` - Main entry point
- `internal/cli/` - CLI commands (Cobra)
- `internal/tui/` - Terminal UI (Bubble Tea)
- `internal/pattern/` - Traffic pattern engine
- `examples/echoserver/` - Demo HTTP server
- `docs/` - Documentation (en/kr)
