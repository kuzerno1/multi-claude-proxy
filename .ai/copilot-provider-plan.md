# GitHub Copilot Provider Implementation Plan

## Overview

Add GitHub Copilot as a third provider to multi-claude-proxy, enabling users to route Anthropic API requests through their GitHub Copilot subscription.

## Authentication Flow

GitHub Copilot uses a two-stage token system:

1. **GitHub Token** (long-lived): Obtained via GitHub Device OAuth flow
   - User visits `https://github.com/login/device` and enters a code
   - App polls `https://github.com/login/oauth/access_token` until user completes flow
   - Returns a GitHub access token (stored persistently)

2. **Copilot Token** (short-lived, ~30 min): Exchanged from GitHub token
   - Call `https://api.github.com/copilot_internal/v2/token` with GitHub token
   - Returns Copilot JWT token + `refresh_in` seconds
   - Must be refreshed before expiry

## API Details

- **Base URLs** (per account type):
  - Individual: `https://api.githubcopilot.com`
  - Business: `https://api.business.githubcopilot.com`
  - Enterprise: `https://api.enterprise.githubcopilot.com`
- **Endpoint**: `POST /chat/completions` (OpenAI-compatible format)
- **Auth Header**: `Authorization: Bearer <copilot_token>`
- **Format**: OpenAI Chat Completions (not Anthropic) - requires format conversion

## Implementation Steps

### Phase 1: Core Provider Package

Create `internal/provider/copilot/` with:

#### 1.1 `types.go` - OpenAI type definitions
```
- ChatCompletionsPayload (request)
- ChatCompletionResponse (non-streaming response)
- ChatCompletionChunk (streaming response)
- Message, Tool, ToolCall types
- ContentPart (text/image)
```

#### 1.2 `format.go` - Anthropic ↔ OpenAI conversion
```
- translateToOpenAI(AnthropicRequest) → ChatCompletionsPayload
- translateToAnthropic(ChatCompletionResponse) → AnthropicResponse
- Handle: system prompt, messages, tools, tool_choice, images
- Map stop reasons: stop→end_turn, length→max_tokens, tool_calls→tool_use
```

#### 1.3 `sse.go` - OpenAI SSE stream parsing
```
- Parse "data: {json}" format
- Handle "[DONE]" marker
- Accumulate tool call deltas (incremental JSON)
- Convert to Anthropic stream events: message_start, content_block_*, message_delta, message_stop
- State machine for tracking open content blocks
```

#### 1.4 `auth.go` - GitHub Device OAuth + Copilot token exchange
```
- getDeviceCode() - initiate device flow
- pollAccessToken() - poll until user completes auth
- getCopilotToken() - exchange GitHub token for Copilot token
- Token refresh scheduling (refresh_in - 60 seconds)
```

#### 1.5 `client.go` - HTTP client
```
- NewClient() with configurable base URL
- SendMessage(ctx, copilotToken, request) → response
- SendMessageStream(ctx, copilotToken, request) → SSE stream
- VerifyToken(ctx, githubToken) → error (validates by fetching copilot token)
- Required headers: Authorization, user-agent, editor-version, x-github-api-version, etc.
```

#### 1.6 `provider.go` - Provider interface implementation
```
- Name() → "copilot"
- Models() → supported models (gpt-4.1, claude-sonnet-4, etc.)
- Initialize() → setup token refresh goroutine
- SendMessage/SendMessageStream with account selection + retry logic
- Rate limit handling (mark account, failover)
```

### Phase 2: Account Management

#### 2.1 Update `cmd/accounts.go`

Add `addCopilotAccount()`:
```go
1. Prompt for account type (individual/business/enterprise)
2. Initiate GitHub Device OAuth flow (display code + URL)
3. Poll for access token completion
4. Exchange for Copilot token (validates subscription)
5. Store account with:
   - Email: GitHub username
   - Source: "oauth"
   - Provider: "copilot"
   - RefreshToken: GitHub access token (long-lived)
   - AccountType: "individual" | "business" | "enterprise"
```

Update `selectProvider()`:
```go
{"copilot", "GitHub Copilot (GitHub OAuth authentication)"}
```

Update `runAccountsVerify()`:
```go
- For copilot accounts: exchange refresh token for copilot token
- Validate by calling GitHub user API
```

### Phase 3: Provider Registration

#### 3.1 Update `cmd/serve.go`

```go
// Initialize Copilot provider
copilotAccountCount := accountManager.GetAccountCountByProvider("copilot")
if copilotAccountCount > 0 {
    copilotProvider := copilot.NewProvider(accountManager)
    if err := copilotProvider.Initialize(ctx); err != nil {
        utils.Warn("[Server] Copilot provider init: %v", err)
    } else {
        registry.Register(copilotProvider)
        utils.Info("[Server] Copilot provider registered")
    }
}
```

### Phase 4: Configuration

#### 4.1 Add to `internal/config/constants.go`

```go
const (
    CopilotIndividualBaseURL  = "https://api.githubcopilot.com"
    CopilotBusinessBaseURL    = "https://api.business.githubcopilot.com"
    CopilotEnterpriseBaseURL  = "https://api.enterprise.githubcopilot.com"
    CopilotTokenURL           = "https://api.github.com/copilot_internal/v2/token"
    GitHubDeviceCodeURL       = "https://github.com/login/device/code"
    GitHubAccessTokenURL      = "https://github.com/login/oauth/access_token"
    GitHubUserURL             = "https://api.github.com/user"
    GitHubClientID            = "Iv1.b507a08c87ecfe98"  // VS Code Copilot client ID
)
```

## Files to Create

| File | Purpose |
|------|---------|
| `internal/provider/copilot/types.go` | OpenAI request/response types |
| `internal/provider/copilot/format.go` | Anthropic ↔ OpenAI format conversion |
| `internal/provider/copilot/sse.go` | SSE stream parsing + event translation |
| `internal/provider/copilot/auth.go` | GitHub OAuth + Copilot token management |
| `internal/provider/copilot/client.go` | HTTP client for Copilot API |
| `internal/provider/copilot/provider.go` | Provider interface implementation |

## Files to Modify

| File | Changes |
|------|---------|
| `cmd/accounts.go` | Add `addCopilotAccount()`, update provider selection, account type prompt |
| `cmd/serve.go` | Register Copilot provider |
| `internal/config/constants.go` | Add Copilot/GitHub constants |
| `internal/account/storage.go` | Add `AccountType` field to Account struct |

## Model Support

**Dynamic Model Fetching**: Use `GET {baseUrl}/models` endpoint to fetch available models at runtime.

Response includes rich metadata per model:
- `id`: Model identifier (e.g., `gpt-4.1`, `claude-sonnet-4`)
- `name`: Display name
- `vendor`: Provider (openai, anthropic, google)
- `capabilities.limits`: Token limits (context, output, prompt)
- `capabilities.supports`: Features (tool_calls, parallel_tool_calls)
- `model_picker_enabled`: Whether model is selectable
- `preview`: Whether model is in preview

Models are fetched during `Initialize()` and cached. Use `model_picker_enabled: true` to filter available models.

## Key Differences from Existing Providers

| Aspect | Antigravity | Z.AI | Copilot |
|--------|-------------|------|---------|
| Auth | Google OAuth | API Key | GitHub Device OAuth |
| Token refresh | Yes (OAuth) | No | Yes (Copilot token) |
| API Format | Google | Anthropic | OpenAI |
| Format conversion | Complex | Minimal | Medium (OpenAI ↔ Anthropic) |

## Rate Limit Handling

- HTTP 429 → Parse `retry-after` header, mark account rate-limited
- HTTP 401/403 → Mark account invalid (token expired/revoked)
- HTTP 5xx → Retry with next account

## Verification

After implementation:

1. **Add account**: `./multi-claude-proxy accounts add --provider copilot`
   - Should open browser for GitHub auth
   - Should display device code
   - Should confirm account added

2. **Verify account**: `./multi-claude-proxy accounts verify`
   - Should show Copilot account as valid

3. **List accounts**: `./multi-claude-proxy accounts list`
   - Should display Copilot account with provider="copilot"

4. **Start server**: `./multi-claude-proxy serve`
   - Should show "Copilot provider registered"

5. **Test API call**:
   ```bash
   curl -X POST http://localhost:8080/v1/messages \
     -H "Content-Type: application/json" \
     -d '{
       "model": "copilot/gpt-4.1",
       "max_tokens": 100,
       "messages": [{"role": "user", "content": "Hello"}]
     }'
   ```

6. **Run tests**: `go test ./internal/provider/copilot/...`
