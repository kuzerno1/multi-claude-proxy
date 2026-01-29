# Implementation Plan: Image Generation Endpoint

## Overview

Add a `/v1/images/generate` endpoint to the multi-claude-proxy server that uses Antigravity's `gemini-3-pro-image` model for text-to-image generation and image editing.

## User Preferences

- **Response Format**: Base64 JSON response (images returned as base64-encoded data)
- **Sessions**: Implement session-based character consistency
- **Features**: Support both text-to-image generation and image editing

## Critical Files

1. `internal/api/handlers.go` - Add new endpoint handler
2. `pkg/types/types.go` - Add image generation types
3. `internal/provider/antigravity/client.go` - Add image API client method
4. `internal/provider/antigravity/provider.go` - Add GenerateImage method
5. `internal/provider/antigravity/format.go` - Add format conversion for images

## Implementation Steps

### Step 1: Add Types to `pkg/types/types.go`

Add request and response types for image generation:

```go
// ImageGenerationRequest represents an image generation request
type ImageGenerationRequest struct {
    Prompt      string `json:"prompt"`                // Required
    Model       string `json:"model,omitempty"`       // Optional: defaults to gemini-3-pro-image
    AspectRatio string `json:"aspect_ratio,omitempty"` // Optional: 1:1, 16:9, etc.
    Count       int    `json:"count,omitempty"`        // Optional: 1-4, default 1
    InputImage  string `json:"input_image,omitempty"`  // Optional: base64 for editing
    SessionID   string `json:"session_id,omitempty"`   // Optional: for character consistency
}

// ImageGenerationResponse represents an image generation response
type ImageGenerationResponse struct {
    ID    string       `json:"id"`
    Type  string       `json:"type"`
    Model string       `json:"model"`
    Data  []ImageBlock `json:"data"`
}
```

### Step 2: Add Client Method to `internal/provider/antigravity/client.go`

Add `DoImageRequest` method following the existing `DoRequest` pattern:

```go
// DoImageRequest sends an image generation request to the Antigravity API
func (c *Client) DoImageRequest(ctx context.Context, opts RequestOptions) (*Response, error)
```

- Use the existing endpoint pattern (`v1internal:generateContent` or image-specific endpoint)
- Implement retry logic with endpoint fallback
- Handle SSE responses if applicable

### Step 3: Add Format Conversion to `internal/provider/antigravity/format.go`

Add conversion functions:

```go
// ConvertImageRequestToGoogle converts image generation request to Google format
func ConvertImageRequestToGoogle(req *types.ImageGenerationRequest, projectID string) map[string]interface{}

// ConvertGoogleImageResponse converts Google response to image generation response
func ConvertGoogleImageResponse(googleResp map[string]interface{}, model string) *types.ImageGenerationResponse
```

### Step 4: Add Provider Method to `internal/provider/antigravity/provider.go`

Add `GenerateImage` method following the existing `SendMessage` pattern:

```go
// GenerateImage generates images using the Antigravity provider
func (p *Provider) GenerateImage(ctx context.Context, req *types.ImageGenerationRequest) (*types.ImageGenerationResponse, error)
```

- Implement retry loop with account failover (max 3 attempts)
- Use `PickNextByProvider` for account selection
- Handle rate limit errors with `MarkRateLimited`
- Clear token/project caches on 401 errors

### Step 5: Add Handler to `internal/api/handlers.go`

Add the new route and handler:

1. **Register route** in `Handler()` method:
```go
mux.HandleFunc("/v1/images/generate", s.handleImageGenerate)
```

2. **Add handler function**:
```go
func (s *Server) handleImageGenerate(w http.ResponseWriter, r *http.Request)
```

3. **Add request parser**:
```go
func parseImageGenerationRequest(body []byte) (*types.ImageGenerationRequest, error)
```

### Step 6: Add Configuration Constants (if needed)

Add to `internal/config/constants.go`:
- `DefaultImageModel = "gemini-3-pro-image"`
- `MaxImageCount = 4`
- Default aspect ratios validation

## Request/Response Format

### Request (POST `/v1/images/generate`)

```json
{
  "prompt": "A cyberpunk cat in neon-lit Tokyo streets",
  "aspect_ratio": "16:9",
  "count": 1,
  "session_id": "optional-session-id"
}
```

### Response

```json
{
  "id": "img_abc123",
  "type": "image_result",
  "model": "antigravity/gemini-3-pro-image",
  "data": [
    {
      "type": "image",
      "source": {
        "type": "base64",
        "media_type": "image/png",
        "data": "<base64-encoded-image>"
      }
    }
  ]
}
```

## Session-Based Character Consistency

- Accept `session_id` parameter in request
- Include session history in Google format request for character consistency
- Store session state for multi-turn image generation conversations

## Error Handling

Follow existing error patterns:
- **401**: Clear token/project caches, retry with same account
- **429**: Mark account rate-limited for `gemini-3-pro-image`, try next account
- **Network errors**: Try next account with delay
- **Invalid requests**: Return 400 with clear error message

## Verification

### Manual Testing

```bash
# Basic image generation
curl -X POST http://localhost:8080/v1/images/generate \
  -H "Content-Type: application/json" \
  -H "X-API-Key: your-key" \
  -d '{"prompt": "a sunset over mountains"}'

# With aspect ratio
curl -X POST http://localhost:8080/v1/images/generate \
  -H "Content-Type: application/json" \
  -H "X-API-Key: your-key" \
  -d '{"prompt": "a cat", "aspect_ratio": "16:9"}'

# With session for character consistency
curl -X POST http://localhost:8080/v1/images/generate \
  -H "Content-Type: application/json" \
  -H "X-API-Key: your-key" \
  -d '{"prompt": "Create Luna the warrior", "session_id": "luna-character"}'

# Image editing
curl -X POST http://localhost:8080/v1/images/generate \
  -H "Content-Type: application/json" \
  -H "X-API-Key: your-key" \
  -d '{"prompt": "Change sky to sunset", "input_image": "base64..."}'
```

### Test Scenarios

1. Successfully generate an image
2. Generate multiple images (count > 1)
3. Use session-based generation for character consistency
4. Edit an existing image with `input_image`
5. Trigger rate limiting to verify account failover
6. Test all supported aspect ratios
7. Test invalid inputs (missing prompt, invalid aspect ratio, count > 4)

## Unknowns to Resolve During Implementation

1. **Exact Antigravity API endpoint**: May be `v1internal:generateContent` with image-specific parameters or a separate image endpoint
2. **Request format details**: How to specify aspect ratio, count, and session_id in Google format
3. **Response format details**: How images are returned in Google's response (inlineData vs other format)

These will be determined by testing against the actual Antigravity API during implementation.
