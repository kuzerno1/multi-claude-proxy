# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Multi-Claude-Proxy is a Go proxy server that exposes an Anthropic-compatible Messages API backed by Google Cloud Code (Antigravity). It enables using Claude Code CLI with the Cloud Code backend while supporting multiple accounts for load balancing and failover.

## Common Commands

```bash
# Build
go build -o multi-claude-proxy .

# Run server
./multi-claude-proxy serve                    # Start on default port 8080
./multi-claude-proxy serve --port 9000        # Custom port
./multi-claude-proxy serve --fallback         # Enable model fallback on quota exhaustion
./multi-claude-proxy serve --debug            # Debug logging

# Account management
./multi-claude-proxy accounts list            # List configured accounts
./multi-claude-proxy accounts add             # Add account via OAuth (opens browser)
./multi-claude-proxy accounts add --no-browser # Add account without browser (manual code)
./multi-claude-proxy accounts remove          # Remove an account
./multi-claude-proxy accounts verify          # Verify all account tokens

# Run tests
go test ./...
go test -v ./internal/provider/antigravity/  # Verbose tests for a package
go test -run TestFunctionName ./path/to/pkg  # Run specific test
```

## Architecture

### Request Flow
1. Client sends Anthropic Messages API request to `/v1/messages`
2. `api.Server` parses request and selects provider (currently Antigravity-only)
3. `account.Manager` picks available account using sticky selection for cache continuity
4. `antigravity.Provider` converts Anthropic format → Google format, sends to Cloud Code API
5. Response is converted back to Anthropic format and streamed via SSE

### Core Packages

- **`cmd/`** - Cobra CLI commands (`serve`, `accounts`)
- **`internal/api/`** - HTTP handlers, middleware, SSE streaming
- **`internal/account/`** - Multi-account manager with per-model rate limiting, sticky selection, failover
- **`internal/provider/`** - Provider interface and registry for multi-backend support
- **`internal/provider/antigravity/`** - Cloud Code provider: format conversion, SSE parsing, thinking model handling
- **`internal/auth/`** - OAuth flow, token refresh
- **`internal/config/`** - Constants, model mappings, OAuth config
- **`pkg/types/`** - Anthropic API types (canonical internal format)

### Key Design Patterns

**Provider Interface**: All backends implement `provider.Provider` (currently only Antigravity). Providers handle format translation between Anthropic API and their native format.

**Account Selection**: The `account.Manager` uses "sticky" selection - it prefers keeping the same account for cache continuity, but fails over on rate limits. Model-specific rate limits are tracked per account.

**Format Conversion**: `internal/provider/antigravity/format.go` converts between Anthropic and Google formats. Thinking models require special handling for signatures.

**SSE Streaming**: `internal/provider/antigravity/sse.go` parses Google's SSE format and emits Anthropic-compatible events. Empty response retries are handled with exponential backoff.

### API Endpoints

- `POST /v1/messages` - Anthropic Messages API (streaming and non-streaming)
- `GET /v1/models` - List available models with quota info
- `GET /health` - Server health with per-account quota details
- `GET /account-limits` - Detailed quota information (JSON or table format)
- `POST /refresh-token` - Force token refresh

### Configuration

Account config stored at `~/.config/multi-claude-proxy/accounts.json`. Accounts can be:
- `oauth` - Added via OAuth flow, uses refresh tokens
- `manual` - Direct API key

### Model Support

Supported models (mapped 1:1 to Cloud Code):
- Claude: `claude-sonnet-4-5-thinking`, `claude-opus-4-5-thinking`, `claude-sonnet-4-5`
- Gemini: `gemini-3-flash`, `gemini-3-pro-low`, `gemini-3-pro-high`

Thinking models (Claude with `-thinking` suffix, Gemini 3+) return extended thinking with signatures.

### Rate Limit Handling

When quota exhausted:
1. Account marked as rate-limited for that specific model
2. Next available account is selected
3. If all accounts exhausted and wait < 2 minutes, proxy waits
4. If wait > 2 minutes, returns `RESOURCE_EXHAUSTED` error
5. With `--fallback` flag, can fallback to alternate model family
