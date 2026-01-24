# Multi-Claude-Proxy

A Go proxy server that exposes an Anthropic-compatible Messages API backed by Google Cloud Code (Antigravity) and Z.AI. Use Claude Code CLI with multiple backend providers while benefiting from multi-account load balancing and automatic failover.

![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)

## Features

- **Anthropic-compatible API** - Drop-in replacement for Anthropic's Messages API
- **Multi-account load balancing** - Distribute requests across multiple accounts
- **Automatic failover** - Seamlessly switch accounts on rate limits or errors
- **Sticky account selection** - Maintain cache continuity by preferring the same account
- **Per-model rate limiting** - Track quotas independently per model per account
- **Soft limits** - Prevent accounts from draining to 0% (avoids 7-day reset timer)
- **Model fallback** - Fall back to alternate model families on quota exhaustion
- **OAuth & API key auth** - Support for Google OAuth (Antigravity) and API keys (Z.AI)
- **SSE streaming** - Full support for streaming responses

## Supported Models

### Antigravity Provider (Google Cloud Code)

| Model ID | Display Name |
|----------|--------------|
| `claude-opus-4-5-thinking` | Claude Opus 4.5 (Thinking) |
| `claude-sonnet-4-5-thinking` | Claude Sonnet 4.5 (Thinking) |
| `claude-sonnet-4-5` | Claude Sonnet 4.5 |
| `gemini-3-flash` | Gemini 3 Flash |
| `gemini-3-pro-high` | Gemini 3 Pro (High) |
| `gemini-3-pro-low` | Gemini 3 Pro (Low) |
| `gemini-3-pro-image` | Gemini 3 Pro Image |
| `gemini-2.5-pro` | Gemini 2.5 Pro |
| `gemini-2.5-flash` | Gemini 2.5 Flash |
| `gemini-2.5-flash-thinking` | Gemini 2.5 Flash (Thinking) |
| `gemini-2.5-flash-lite` | Gemini 2.5 Flash Lite |
| `gpt-oss-120b-medium` | GPT-OSS 120B (Medium) |

### Z.AI Provider

| Model ID | Display Name |
|----------|--------------|
| `glm-4.5` | GLM-4.5 |
| `glm-4.5-air` | GLM-4.5-Air |
| `glm-4.6` | GLM-4.6 |
| `glm-4.7` | GLM-4.7 |

### Fallback Mappings

When `--fallback` is enabled, models fall back across families:

| Primary Model | Fallback Model |
|---------------|----------------|
| `gemini-3-pro-high` | `claude-opus-4-5-thinking` |
| `gemini-3-pro-low` | `claude-sonnet-4-5` |
| `gemini-3-flash` | `claude-sonnet-4-5-thinking` |
| `claude-opus-4-5-thinking` | `gemini-3-pro-high` |
| `claude-sonnet-4-5-thinking` | `gemini-3-flash` |
| `claude-sonnet-4-5` | `gemini-3-flash` |

## Getting Started

### Prerequisites

- Go 1.24 or later

### Build

```bash
go build -o multi-claude-proxy .
```

### Add an Account

```bash
# Interactive provider selection
./multi-claude-proxy accounts add

# Add Antigravity account (Google OAuth - manual code entry)
./multi-claude-proxy accounts add --provider antigravity

# Add Z.AI account with API key
./multi-claude-proxy accounts add --provider zai
```

### Set Required Environment Variable

```bash
export PROXY_API_KEY="your-secret-key"
```

### Start the Server

```bash
./multi-claude-proxy serve
```

## CLI Reference

### `serve` Command

Start the proxy server.

```bash
./multi-claude-proxy serve [flags]
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--port` | `-p` | `8080` | Server port |
| `--fallback` | | `false` | Enable model fallback on quota exhaustion |
| `--soft-limit` | | `0.20` | Soft limit threshold (0.0-1.0) |
| `--no-soft-limit` | | `false` | Disable soft limits entirely |
| `--debug` | | `false` | Enable debug logging |

### `accounts` Command

Manage configured accounts.

| Subcommand | Description |
|------------|-------------|
| `accounts add` | Add new account via OAuth or API key |
| `accounts list` | List all configured accounts with status |
| `accounts remove` | Remove an account |
| `accounts verify` | Verify all account tokens are valid |

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `PROXY_API_KEY` | **Required** - API key for proxy authentication | (none) |
| `PORT` | Server port | `8080` |
| `BIND_ADDRESS` | Server bind address | `0.0.0.0` |
| `DEBUG` | Enable debug logging | `false` |
| `ENABLE_FALLBACK` | Enable model fallback on quota exhaustion | `false` |
| `SOFT_LIMIT_THRESHOLD` | Soft limit threshold (0.0-1.0) | `0.20` |
| `READ_TIMEOUT_SEC` | HTTP read timeout (seconds) | `30` |
| `WRITE_TIMEOUT_SEC` | HTTP write timeout (seconds) | `300` |
| `IDLE_TIMEOUT_SEC` | HTTP idle timeout (seconds) | `120` |
| `CORS_ENABLED` | Enable CORS | `true` |
| `CORS_ALLOW_ORIGIN` | CORS allowed origins | `*` |
| `CORS_ALLOW_METHODS` | CORS allowed methods | `GET, POST, PUT, DELETE, OPTIONS` |
| `CORS_ALLOW_HEADERS` | CORS allowed headers | `Content-Type, Authorization, X-API-Key, anthropic-version, x-session-id` |
| `CORS_MAX_AGE` | CORS max age (seconds) | `86400` |
| `GOOGLE_CLIENT_ID` | Google OAuth client ID | (built-in) |
| `GOOGLE_CLIENT_SECRET` | Google OAuth client secret | (built-in) |
| `ACCOUNTS_CONFIG_PATH` | Account config file path | `~/.config/multi-claude-proxy/accounts.json` |

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/v1/messages` | POST | Anthropic Messages API (streaming and non-streaming) |
| `/v1/models` | GET | List available models with quota info |
| `/health` | GET | Health check with per-account quota details |
| `/account-limits` | GET | Detailed quota info (JSON or `?format=table`) |
| `/refresh-token` | POST | Force token refresh |

### Authentication

Include your API key in requests:

```bash
# Using X-API-Key header
curl -H "X-API-Key: your-secret-key" http://localhost:8080/v1/models

# Using Authorization header
curl -H "Authorization: Bearer your-secret-key" http://localhost:8080/v1/models
```

### Example: Send a Message

```bash
curl -X POST http://localhost:8080/v1/messages \
  -H "Content-Type: application/json" \
  -H "X-API-Key: your-secret-key" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "claude-sonnet-4-5",
    "max_tokens": 1024,
    "messages": [
      {"role": "user", "content": "Hello, Claude!"}
    ]
  }'
```

## Rate Limiting & Quota

The proxy implements intelligent rate limit handling:

1. **Per-model tracking** - Rate limits are tracked independently per model per account
2. **Soft limits** - Accounts at or below the threshold (default 20%) are deprioritized to avoid the 7-day reset timer
3. **Automatic failover** - When an account hits a rate limit, the next available account is selected
4. **Wait or error** - If all accounts are exhausted:
   - Wait < 2 minutes: proxy waits for reset
   - Wait > 2 minutes: returns `RESOURCE_EXHAUSTED` error
5. **Model fallback** - With `--fallback` flag, falls back to alternate model family

## Docker

### Quick Start with Docker Compose

1. First, add accounts locally (OAuth requires interactive terminal):
   ```bash
   go build -o multi-claude-proxy .
   ./multi-claude-proxy accounts add
   ```

2. Create a `.env` file with your configuration:
   ```bash
   PROXY_API_KEY=your-secret-key
   DEBUG=false
   ENABLE_FALLBACK=false
   SOFT_LIMIT_THRESHOLD=0.20
   ```

3. Start the proxy:
   ```bash
   docker-compose up -d
   ```

4. View logs:
   ```bash
   docker-compose logs -f
   ```

5. Stop the proxy:
   ```bash
   docker-compose down
   ```

### Docker Compose Configuration

The included `docker-compose.yml` mounts your local `accounts.json` and supports all environment variables:

```yaml
services:
  multi-claude-proxy:
    image: ghcr.io/kuzerno1/multi-claude-proxy:latest
    ports:
      - "8080:8080"
    volumes:
      - ./accounts.json:/config/accounts.json:ro
    environment:
      - PROXY_API_KEY=${PROXY_API_KEY:?PROXY_API_KEY is required}
      - ACCOUNTS_CONFIG_PATH=/config/accounts.json
      - DEBUG=${DEBUG:-false}
      - ENABLE_FALLBACK=${ENABLE_FALLBACK:-false}
      - SOFT_LIMIT_THRESHOLD=${SOFT_LIMIT_THRESHOLD:-0.20}
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "--spider", "http://localhost:8080/health"]
      interval: 30s
      timeout: 3s
      retries: 3
```

### Build from Source

```bash
docker build -t multi-claude-proxy .
```

### Run Container Directly

```bash
docker run -d \
  -p 8080:8080 \
  -v ~/.config/multi-claude-proxy/accounts.json:/config/accounts.json:ro \
  -e PROXY_API_KEY="your-secret-key" \
  -e ACCOUNTS_CONFIG_PATH=/config/accounts.json \
  multi-claude-proxy
```

### Pre-built Image

```bash
docker pull ghcr.io/kuzerno1/multi-claude-proxy:latest
```

### Docker Notes

- Mount your `accounts.json` to `/config/accounts.json`
- Container runs as non-root user for security
- Health check configured on `/health` endpoint
- Graceful shutdown supported

## Claude Code Integration

Configure Claude Code CLI to use the proxy:

```bash
export ANTHROPIC_BASE_URL="http://localhost:8080"
export ANTHROPIC_API_KEY="your-secret-key"  # Must match PROXY_API_KEY
```

Then use Claude Code normally:

```bash
claude
```

## License

MIT
