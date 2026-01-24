package api

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kuzerno1/multi-claude-proxy/internal/account"
	"github.com/kuzerno1/multi-claude-proxy/internal/config"
	merrors "github.com/kuzerno1/multi-claude-proxy/internal/errors"
	"github.com/kuzerno1/multi-claude-proxy/internal/provider"
	"github.com/kuzerno1/multi-claude-proxy/internal/provider/antigravity"
	"github.com/kuzerno1/multi-claude-proxy/internal/provider/zai"
	"github.com/kuzerno1/multi-claude-proxy/internal/utils"
	"github.com/kuzerno1/multi-claude-proxy/pkg/types"
)

var errMessagesNotArray = stderrors.New("messages_not_array")

// Server holds the HTTP server dependencies.
type Server struct {
	registry       *provider.Registry
	accountManager *account.Manager
	agClient       *antigravity.Client
}

// NewServer creates a new API server with the given provider registry.
func NewServer(registry *provider.Registry, accountManager *account.Manager) *Server {
	return &Server{
		registry:       registry,
		accountManager: accountManager,
		agClient:       antigravity.NewClient(),
	}
}

// Handler returns the main HTTP handler with all routes and middleware.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("/v1/messages", s.handleMessages)
	mux.HandleFunc("/v1/messages/count_tokens", s.handleCountTokens)
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/v1/images/generate", s.handleImageGenerate)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/account-limits", s.handleAccountLimits)
	mux.HandleFunc("/refresh-token", s.handleRefreshToken)

	// Catch-all for unsupported endpoints (Node parity).
	mux.HandleFunc("/", s.handleNotFound)

	// Apply middleware (order matters: outermost first)
	handler := http.Handler(mux)
	handler = Logger(handler)
	handler = Recovery(handler)
	handler = APIKeyAuth(handler) // Auth middleware (skips /health)
	handler = ConfigurableCORS(handler) // CORS middleware (configurable via env)

	return handler
}

// handleMessages handles POST /v1/messages requests.
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.handleNotFound(w, r)
		return
	}

	// Apply request body size limit (Node parity)
	r.Body = http.MaxBytesReader(w, r.Body, config.RequestBodyLimit)

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		// Check if it's a size limit error
		if err.Error() == "http: request body too large" {
			writeError(w, http.StatusRequestEntityTooLarge, "invalid_request_error",
				fmt.Sprintf("Request body too large (max %d bytes)", config.RequestBodyLimit))
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	defer r.Body.Close()

	// Parse request (Node parity: validate messages is an array; default model/max_tokens).
	req, err := parseMessagesRequest(body)
	if err != nil {
		if stderrors.Is(err, errMessagesNotArray) {
			writeError(w, http.StatusBadRequest, "invalid_request_error", "messages is required and must be an array")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Invalid JSON: %v", err))
		return
	}

	// Default model and max_tokens (Node parity).
	if req.Model == "" {
		req.Model = "antigravity/claude-3-5-sonnet-20241022"
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = 4096
	}

	publicModel := req.Model
	prov, rawModel, err := s.resolveProviderForModel(publicModel)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	// Use raw model IDs internally (rate limits, quotas, upstream requests).
	reqForProvider := *req
	reqForProvider.Model = rawModel

	// Optimistic Retry: If ALL provider accounts are rate-limited for this model, reset them to force a fresh check (Node parity).
	providerName := prov.Name()
	if s.accountManager != nil && s.accountManager.IsAllRateLimitedByProvider(providerName, rawModel) {
		utils.Warn("[Server] All %s accounts rate-limited for %s. Resetting state for optimistic retry.", providerName, rawModel)
		s.accountManager.ResetAllRateLimitsByProvider(providerName)
	}

	ctx := r.Context()

	// Handle streaming vs non-streaming (Node parity: centralized error shaping + auth refresh attempt).
	if req.Stream {
		s.handleStreamingMessage(ctx, w, prov, &reqForProvider, publicModel)
		return
	}

	resp, err := prov.SendMessage(ctx, &reqForProvider)
	if err != nil {
		s.writeMessagesError(w, r, err)
		return
	}
	resp.Model = publicModel

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toNodeMessageResponse(resp))
}

// handleStreamingMessage handles streaming message requests.
func (s *Server) handleStreamingMessage(ctx context.Context, w http.ResponseWriter, prov provider.Provider, req *types.AnthropicRequest, publicModel string) {
	utils.Debug("[Messages] Streaming request for model: %s", req.Model)

	sse, err := NewSSEWriter(w)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "api_error", "Streaming not supported")
		return
	}

	// NOTE: Headers are now sent. Any errors from this point must be sent as SSE error events.
	eventsCh, err := prov.SendMessageStream(ctx, req)
	if err != nil {
		s.writeMessagesStreamError(sse, err)
		return
	}

	// Stream events to client
	for event := range eventsCh {
		s.applyPublicModelToStreamEvent(&event, publicModel)

		eventType := event.Type
		if eventType == "" {
			eventType = "message"
		}

		// Check for error events from the provider.
		if event.Error != nil {
			// Provider sent an error event, forward it (Node parity shape).
			if writeErr := sse.WriteEvent("error", event); writeErr != nil {
				utils.Error("[Messages] Failed to write SSE error event: %v", writeErr)
			}
			continue
		}

		if event.Raw != nil {
			if err := sse.WriteEvent(eventType, event.Raw); err != nil {
				utils.Error("[Messages] Failed to write SSE event: %v", err)
				return
			}
			continue
		}

		if err := sse.WriteEvent(eventType, event); err != nil {
			utils.Error("[Messages] Failed to write SSE event: %v", err)
			return
		}
	}
}

func (s *Server) resolveProviderForModel(model string) (provider.Provider, string, error) {
	if s.registry == nil {
		return nil, "", fmt.Errorf("no provider registry configured")
	}

	// Explicit provider selection: "<provider>/<model>".
	// Only treat as explicit provider selection if the prefix is a registered provider.
	if providerName, rawModel, ok := splitModelID(model); ok {
		if prov, found := s.registry.GetByName(providerName); found && prov != nil {
			return prov, rawModel, nil
		}
		// Not a registered provider - treat the full string as a model ID.
	}

	// No explicit provider: try default to Antigravity if that model is registered there.
	if prov, ok := s.registry.GetByModel("antigravity/" + model); ok && prov != nil {
		return prov, model, nil
	}

	// Otherwise, attempt to find a unique provider that supports this model.
	candidates := make([]provider.Provider, 0, 2)
	for _, p := range s.registry.All() {
		if p == nil {
			continue
		}
		if p.SupportsModel(model) {
			candidates = append(candidates, p)
		}
	}
	if len(candidates) == 1 {
		return candidates[0], model, nil
	}

	// Fallback: Antigravity (Node parity: don't reject unknown models).
	prov, _ := s.registry.GetByName("antigravity")
	if prov == nil {
		all := s.registry.All()
		if len(all) == 0 || all[0] == nil {
			return nil, "", fmt.Errorf("no providers registered")
		}
		prov = all[0]
	}
	return prov, model, nil
}

func (s *Server) applyPublicModelToStreamEvent(event *types.StreamEvent, publicModel string) {
	if event == nil || publicModel == "" {
		return
	}

	if event.Message != nil {
		event.Message.Model = publicModel
	}

	raw, ok := event.Raw.(map[string]interface{})
	if !ok || raw == nil {
		return
	}

	// Most providers emit the Anthropic-compatible event shape:
	// { "type": "...", "message": { "model": "..." }, ... } for message_start.
	if msgVal, ok := raw["message"]; ok {
		if msg, ok := msgVal.(map[string]interface{}); ok && msg != nil {
			msg["model"] = publicModel
		}
	}
}

func splitModelID(model string) (providerName, rawModel string, ok bool) {
	providerName, rawModel, ok = strings.Cut(model, "/")
	if !ok || providerName == "" || rawModel == "" {
		return "", "", false
	}
	return providerName, rawModel, true
}

func (s *Server) writeMessagesError(w http.ResponseWriter, r *http.Request, err error) {
	ae := merrors.FromError(err)
	errorType := string(ae.Detail.Type)
	statusCode := ae.StatusCode()
	errorMessage := ae.Detail.Message

	// For auth errors, clear caches so next request will refresh tokens.
	if errorType == "authentication_error" {
		utils.Warn("[API] Token might be expired, clearing caches...")
		if s.accountManager != nil {
			s.accountManager.ClearProjectCache("")
			s.accountManager.ClearTokenCache("")
		}
		errorMessage = "Token was expired. Caches cleared - please retry your request."
	}

	// If headers have already been sent, write error as SSE (Node parity).
	if w.Header().Get("Content-Type") == "text/event-stream" {
		sse, sseErr := NewSSEWriter(w)
		if sseErr == nil {
			_ = sse.WriteError(errorType, errorMessage)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(types.AnthropicError{
		Type: "error",
		Error: types.ErrorDetail{
			Type:    errorType,
			Message: errorMessage,
		},
	})
}

func (s *Server) writeMessagesStreamError(sse *SSEWriter, err error) {
	ae := merrors.FromError(err)
	errorType := string(ae.Detail.Type)
	errorMessage := ae.Detail.Message

	// For auth errors, clear caches so next request will refresh tokens.
	if errorType == "authentication_error" {
		if s.accountManager != nil {
			s.accountManager.ClearProjectCache("")
			s.accountManager.ClearTokenCache("")
		}
		errorMessage = "Token was expired. Caches cleared - please retry your request."
	}

	if writeErr := sse.WriteError(errorType, errorMessage); writeErr != nil {
		utils.Error("[Messages] Failed to write SSE error event: %v", writeErr)
	}
}

func parseMessagesRequest(body []byte) (*types.AnthropicRequest, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}

	// Validate messages is an array (Node parity).
	messagesRaw, ok := raw["messages"]
	if !ok || len(bytes.TrimSpace(messagesRaw)) == 0 || !looksLikeJSONArray(messagesRaw) {
		return nil, errMessagesNotArray
	}

	var req types.AnthropicRequest

	_ = json.Unmarshal(raw["model"], &req.Model)
	_ = json.Unmarshal(messagesRaw, &req.Messages)
	_ = json.Unmarshal(raw["max_tokens"], &req.MaxTokens)
	_ = json.Unmarshal(raw["stream"], &req.Stream)

	// Preserve raw system prompt content.
	if sys, ok := raw["system"]; ok {
		req.System = sys
	}

	_ = json.Unmarshal(raw["tools"], &req.Tools)
	_ = json.Unmarshal(raw["tool_choice"], &req.ToolChoice)
	_ = json.Unmarshal(raw["thinking"], &req.Thinking)
	_ = json.Unmarshal(raw["temperature"], &req.Temperature)
	_ = json.Unmarshal(raw["top_p"], &req.TopP)
	_ = json.Unmarshal(raw["top_k"], &req.TopK)
	_ = json.Unmarshal(raw["stop_sequences"], &req.StopSequences)

	return &req, nil
}

func looksLikeJSONArray(b []byte) bool {
	trimmed := bytes.TrimSpace(b)
	return len(trimmed) > 0 && trimmed[0] == '['
}

func toNodeMessageResponse(resp *types.AnthropicResponse) map[string]interface{} {
	content := make([]interface{}, 0, len(resp.Content))
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			content = append(content, map[string]interface{}{
				"type": "text",
				"text": block.Text,
			})
		case "thinking":
			content = append(content, map[string]interface{}{
				"type":      "thinking",
				"thinking":  block.Thinking,
				"signature": block.Signature,
			})
		case "tool_use":
			input := block.Input
			if input == nil {
				input = map[string]interface{}{}
			}
			tool := map[string]interface{}{
				"type":  "tool_use",
				"id":    block.ID,
				"name":  block.Name,
				"input": input,
			}
			if block.ThoughtSignature != "" {
				tool["thoughtSignature"] = block.ThoughtSignature
			}
			content = append(content, tool)
		default:
			// Preserve unknown blocks best-effort.
			content = append(content, map[string]interface{}{
				"type": block.Type,
			})
		}
	}

	// Node parity: ensure at least one content block.
	if len(content) == 0 {
		content = []interface{}{map[string]interface{}{"type": "text", "text": ""}}
	}

	return map[string]interface{}{
		"id":            resp.ID,
		"type":          resp.Type,
		"role":          resp.Role,
		"content":       content,
		"model":         resp.Model,
		"stop_reason":   resp.StopReason,
		"stop_sequence": nil,
		"usage": map[string]interface{}{
			"input_tokens":                resp.Usage.InputTokens,
			"output_tokens":               resp.Usage.OutputTokens,
			"cache_read_input_tokens":     resp.Usage.CacheReadInputTokens,
			"cache_creation_input_tokens": resp.Usage.CacheCreationInputTokens,
		},
	}
}

// handleCountTokens handles POST /v1/messages/count_tokens requests (Node parity: 501 not implemented).
func (s *Server) handleCountTokens(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.handleNotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"type": "error",
		"error": map[string]interface{}{
			"type":    "not_implemented",
			"message": "Token counting is not implemented. Use /v1/messages with max_tokens or configure your client to skip token counting.",
		},
	})
}

// handleRefreshToken handles POST /refresh-token - clears caches and refreshes OAuth tokens.
func (s *Server) handleRefreshToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.handleNotFound(w, r)
		return
	}

	if err := s.ensureInitialized(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "error",
			"error":  err.Error(),
		})
		return
	}

	if s.accountManager == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "error",
			"error":  "No account manager configured",
		})
		return
	}

	// Clear all caches
	s.accountManager.ClearTokenCache("")
	s.accountManager.ClearProjectCache("")

	// Attempt to refresh tokens for all accounts
	allAccounts := s.accountManager.GetAllAccounts()
	refreshed := 0
	var lastError error

	for i := range allAccounts {
		acc := allAccounts[i]
		if acc.Source != "oauth" {
			continue // Manual accounts don't need refresh
		}
		if _, err := s.accountManager.GetTokenForAccount(&acc); err != nil {
			lastError = err
			utils.Warn("[API] Failed to refresh token for %s: %v", acc.Email, err)
		} else {
			refreshed++
		}
	}

	if refreshed == 0 && lastError != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "error",
			"error":  lastError.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":            "ok",
		"message":           "Token caches cleared",
		"accountsRefreshed": refreshed,
	})
}

// handleModels handles GET /v1/models requests (Anthropic-compatible).
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.handleNotFound(w, r)
		return
	}

	ctx := r.Context()

	if err := s.ensureInitialized(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"type": "error",
			"error": map[string]interface{}{
				"type":    "api_error",
				"message": err.Error(),
			},
		})
		return
	}

	if s.registry == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"type": "error",
			"error": map[string]interface{}{
				"type":    "api_error",
				"message": "No providers registered",
			},
		})
		return
	}

	providers := s.registry.All()
	if len(providers) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"type": "error",
			"error": map[string]interface{}{
				"type":    "api_error",
				"message": "No providers registered",
			},
		})
		return
	}

	// Parse pagination parameters
	afterID := r.URL.Query().Get("after_id")
	beforeID := r.URL.Query().Get("before_id")
	limitStr := r.URL.Query().Get("limit")

	limit := 20 // Default limit
	if limitStr != "" {
		parsed, err := strconv.Atoi(limitStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request_error",
				fmt.Sprintf("Invalid limit parameter: %s", limitStr))
			return
		}
		limit = parsed
	}
	// Enforce limit bounds (1-1000)
	if limit < 1 {
		limit = 1
	}
	if limit > 1000 {
		limit = 1000
	}

	// Collect all models from all providers
	merged := make([]types.Model, 0, 64)
	for _, p := range providers {
		if p == nil {
			continue
		}

		resp, err := p.ListModels(ctx)
		if err != nil || resp == nil {
			// Fallback: create models from provider's model list
			for _, modelID := range p.Models() {
				merged = append(merged, types.Model{
					ID:          fmt.Sprintf("%s/%s", p.Name(), modelID),
					DisplayName: modelID,
					Type:        "model",
					CreatedAt:   nil, // Unknown when provider doesn't provide it
				})
			}
			continue
		}

		for _, m := range resp.Data {
			model := m
			model.ID = fmt.Sprintf("%s/%s", p.Name(), m.ID)
			if model.DisplayName == "" {
				model.DisplayName = m.ID
			}
			if model.Type == "" {
				model.Type = "model"
			}
			merged = append(merged, model)
		}
	}

	// Sort by ID for consistent ordering
	sort.Slice(merged, func(i, j int) bool { return merged[i].ID < merged[j].ID })

	// Apply pagination
	startIdx := 0
	endIdx := len(merged)

	// Handle after_id: return models after the specified ID
	if afterID != "" {
		found := false
		for i, m := range merged {
			if m.ID == afterID {
				startIdx = i + 1
				found = true
				break
			}
		}
		// If after_id not found, return empty (nothing after non-existent ID)
		if !found {
			startIdx = len(merged) // Empty result
		}
	}

	// Handle before_id: return models before the specified ID
	if beforeID != "" {
		for i, m := range merged {
			if m.ID == beforeID {
				endIdx = i
				break
			}
		}
	}

	// Slice to get the range
	if startIdx > len(merged) {
		startIdx = len(merged)
	}
	if endIdx > len(merged) {
		endIdx = len(merged)
	}
	if startIdx > endIdx {
		startIdx = endIdx
	}

	result := merged[startIdx:endIdx]

	// Apply limit
	hasMore := false
	if len(result) > limit {
		result = result[:limit]
		hasMore = true
	}

	// Build response
	firstID := ""
	lastID := ""
	if len(result) > 0 {
		firstID = result[0].ID
		lastID = result[len(result)-1].ID
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(types.ModelsResponse{
		Data:    result,
		FirstID: firstID,
		HasMore: hasMore,
		LastID:  lastID,
	}); err != nil {
		utils.Debug("[API] Failed to encode models response: %v", err)
	}
}

// handleImageGenerate handles POST /v1/images/generate requests.
func (s *Server) handleImageGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.handleNotFound(w, r)
		return
	}

	// Apply request body size limit
	r.Body = http.MaxBytesReader(w, r.Body, config.RequestBodyLimit)

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		if err.Error() == "http: request body too large" {
			writeError(w, http.StatusRequestEntityTooLarge, "invalid_request_error",
				fmt.Sprintf("Request body too large (max %d bytes)", config.RequestBodyLimit))
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	defer r.Body.Close()

	// Parse request
	req, err := parseImageGenerationRequest(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Invalid JSON: %v", err))
		return
	}

	// Validate prompt
	if req.Prompt == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "prompt is required")
		return
	}

	// Get antigravity provider
	if s.registry == nil {
		writeError(w, http.StatusInternalServerError, "api_error", "Image generation provider not available")
		return
	}
	prov, ok := s.registry.GetByName("antigravity")
	if !ok || prov == nil {
		writeError(w, http.StatusInternalServerError, "api_error", "Image generation provider not available")
		return
	}

	// Type assert to access GenerateImage method
	agProvider, ok := prov.(*antigravity.Provider)
	if !ok {
		writeError(w, http.StatusInternalServerError, "api_error", "Image generation provider not available")
		return
	}

	ctx := r.Context()
	resp, err := agProvider.GenerateImage(ctx, req)
	if err != nil {
		ae := merrors.FromError(err)
		writeError(w, ae.StatusCode(), string(ae.Detail.Type), ae.Detail.Message)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func parseImageGenerationRequest(body []byte) (*types.ImageGenerationRequest, error) {
	var req types.ImageGenerationRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}

	// Apply defaults
	if req.Model == "" {
		req.Model = config.DefaultImageModel
	}
	if req.Count <= 0 {
		req.Count = config.DefaultImageCount
	}
	if req.Count > config.MaxImageCount {
		req.Count = config.MaxImageCount
	}

	return &req, nil
}

// handleHealth handles GET /health requests.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.handleNotFound(w, r)
		return
	}

	start := time.Now()
	if err := s.ensureInitialized(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "error",
			"error":     err.Error(),
			"timestamp": formatISOTimeUTC(time.Now()),
		})
		return
	}

	allAccounts := []account.Account{}
	if s.accountManager != nil {
		allAccounts = s.accountManager.GetAllAccounts()
	}

	// Get soft limit settings
	softLimitEnabled := false
	softLimitThreshold := 0.0
	if s.accountManager != nil {
		softLimitEnabled = s.accountManager.IsSoftLimitEnabled()
		softLimitThreshold = s.accountManager.GetSoftLimitThreshold()
	}

	// Account pool summary (Node parity + soft limits).
	total := len(allAccounts)
	invalid := 0
	rateLimited := 0
	softLimited := 0
	nowMs := time.Now().UnixMilli()

	for _, acc := range allAccounts {
		if acc.IsInvalid {
			invalid++
			continue
		}

		isLimited := false
		isSoftLimited := false
		for _, limit := range acc.ModelRateLimits {
			if limit.IsRateLimited && limit.ResetTime > nowMs {
				isLimited = true
			}
			if limit.IsSoftLimited {
				isSoftLimited = true
			}
		}
		if isLimited {
			rateLimited++
		}
		if isSoftLimited && !isLimited {
			softLimited++
		}
	}

	var available int
	var summary string

	// Detailed per-account model quotas (Node parity).
	type accountDetail struct {
		idx int
		val map[string]interface{}
	}

	var (
		wg      sync.WaitGroup
		results = make([]accountDetail, 0, len(allAccounts))
		mu      sync.Mutex
	)

	zaiClient := zai.NewClient()
	zaiModels := []string{}
	if s.registry != nil {
		if p, ok := s.registry.GetByName("zai"); ok && p != nil {
			zaiModels = p.Models()
		}
	}

	for i := range allAccounts {
		acc := allAccounts[i]
		wg.Add(1)
		go func(idx int, a account.Account) {
			defer wg.Done()

			providerName := a.Provider
			if providerName == "" {
				providerName = "antigravity"
			}

			// Check if this account is soft-limited for any model
			accIsSoftLimited := false
			for _, limit := range a.ModelRateLimits {
				if limit.IsSoftLimited {
					accIsSoftLimited = true
					break
				}
			}

			baseInfo := map[string]interface{}{
				"email":                      a.Email,
				"provider":                   providerName,
				"lastUsed":                   nil,
				"rateLimitCooldownRemaining": int64(0),
				"isSoftLimited":              accIsSoftLimited,
			}

			if a.LastUsed != nil {
				baseInfo["lastUsed"] = formatISOTimeUTC(*a.LastUsed)
			}

			// Compute soonest reset among active model-specific limits.
			var (
				isLimited    bool
				soonestReset int64
			)
			for _, limit := range a.ModelRateLimits {
				if limit.IsRateLimited && limit.ResetTime > nowMs {
					if !isLimited || limit.ResetTime < soonestReset {
						soonestReset = limit.ResetTime
					}
					isLimited = true
				}
			}
			if isLimited {
				remaining := soonestReset - nowMs
				if remaining < 0 {
					remaining = 0
				}
				baseInfo["rateLimitCooldownRemaining"] = remaining
			}

			// Invalid accounts: skip quota fetch.
			if a.IsInvalid {
				baseInfo["status"] = "invalid"
				baseInfo["error"] = a.InvalidReason
				baseInfo["models"] = map[string]interface{}{}
				mu.Lock()
				results = append(results, accountDetail{idx: idx, val: baseInfo})
				mu.Unlock()
				return
			}

			quotas := map[string]interface{}{}

			// Use a shorter timeout for quota fetches in health checks.
			quotaCtx, quotaCancel := context.WithTimeout(r.Context(), config.QuotaFetchTimeout)

			switch providerName {
			case "zai":
				if a.APIKey == "" {
					quotaCancel()
					baseInfo["status"] = "error"
					baseInfo["error"] = "no API key"
					baseInfo["models"] = map[string]interface{}{}
					mu.Lock()
					results = append(results, accountDetail{idx: idx, val: baseInfo})
					mu.Unlock()
					return
				}

				quotaInfo, err := zaiClient.FetchQuota(quotaCtx, a.APIKey)
				quotaCancel()
				if err != nil {
					baseInfo["status"] = "error"
					baseInfo["error"] = err.Error()
					baseInfo["models"] = map[string]interface{}{}
					mu.Lock()
					results = append(results, accountDetail{idx: idx, val: baseInfo})
					mu.Unlock()
					return
				}

				for _, modelID := range zaiModels {
					quotas[modelID] = map[string]interface{}{
						"remainingFraction": quotaInfo.RemainingFraction,
						"resetTime":         nil,
					}
				}

			default:
				// Antigravity-style quotas (per model) from Cloud Code.
				token, err := s.accountManager.GetTokenForAccount(&a)
				if err != nil {
					quotaCancel()
					baseInfo["status"] = "error"
					baseInfo["error"] = err.Error()
					baseInfo["models"] = map[string]interface{}{}
					mu.Lock()
					results = append(results, accountDetail{idx: idx, val: baseInfo})
					mu.Unlock()
					return
				}

				quotas, err = s.getModelQuotas(quotaCtx, token)
				quotaCancel()
				if err != nil {
					baseInfo["status"] = "error"
					baseInfo["error"] = err.Error()
					baseInfo["models"] = map[string]interface{}{}
					mu.Lock()
					results = append(results, accountDetail{idx: idx, val: baseInfo})
					mu.Unlock()
					return
				}
			}

			// Update soft limit status based on fetched quotas (no persist for health checks)
			for modelID, infoVal := range quotas {
				info, _ := infoVal.(map[string]interface{})
				if info == nil {
					continue
				}
				if rf, ok := info["remainingFraction"].(float64); ok {
					s.accountManager.UpdateSoftLimitStatusNoPersist(a.Email, modelID, rf)
				}
			}

			// Re-check soft limit status after updating
			accIsSoftLimited = false
			for _, infoVal := range quotas {
				info, _ := infoVal.(map[string]interface{})
				if info == nil {
					continue
				}
				if rf, ok := info["remainingFraction"].(float64); ok {
					// Treat 0% (exhausted) as soft-limited too - use <= for explicit 0% handling
					if softLimitEnabled && (rf <= 0 || rf < softLimitThreshold) {
						accIsSoftLimited = true
						break
					}
				}
			}
			baseInfo["isSoftLimited"] = accIsSoftLimited

			formatted := make(map[string]interface{}, len(quotas))
			for modelID, infoVal := range quotas {
				info, _ := infoVal.(map[string]interface{})
				if info == nil {
					continue
				}
				rf := info["remainingFraction"]
				resetTime := info["resetTime"]
				remaining := "N/A"
				modelIsSoftLimited := false
				if rf != nil {
					if f, ok := rf.(float64); ok {
						remaining = fmt.Sprintf("%d%%", int64(f*100+0.5))
						// Treat 0% (exhausted) as soft-limited too - use <= for explicit 0% handling
						if softLimitEnabled && (f <= 0 || f < softLimitThreshold) {
							modelIsSoftLimited = true
						}
					}
				}

				formatted[fmt.Sprintf("%s/%s", providerName, modelID)] = map[string]interface{}{
					"remaining":         remaining,
					"remainingFraction": rf,
					"resetTime":         resetTime,
					"isSoftLimited":     modelIsSoftLimited,
				}
			}

			if isLimited {
				baseInfo["status"] = "rate-limited"
			} else if accIsSoftLimited {
				baseInfo["status"] = "soft-limited"
			} else {
				baseInfo["status"] = "ok"
			}
			baseInfo["models"] = formatted

			mu.Lock()
			results = append(results, accountDetail{idx: idx, val: baseInfo})
			mu.Unlock()
		}(i, acc)
	}

	wg.Wait()
	sort.Slice(results, func(i, j int) bool { return results[i].idx < results[j].idx })

	detailed := make([]map[string]interface{}, 0, len(results))
	for _, r := range results {
		detailed = append(detailed, r.val)
	}

	// Recompute counts from fresh quota data (after fetch)
	invalid = 0
	rateLimited = 0
	softLimited = 0
	errorCount := 0
	for _, r := range results {
		status, _ := r.val["status"].(string)
		switch status {
		case "invalid":
			invalid++
		case "rate-limited":
			rateLimited++
		case "soft-limited":
			softLimited++
		case "error":
			errorCount++ // Track errors separately from invalid
		}
	}
	// Unavailable = invalid + error (accounts we can't use right now)
	unavailable := invalid + errorCount
	available = total - unavailable
	if softLimitEnabled {
		summary = fmt.Sprintf("%d total, %d available, %d soft-limited, %d rate-limited, %d invalid", total, available, softLimited, rateLimited, invalid)
	} else {
		summary = fmt.Sprintf("%d total, %d available, %d rate-limited, %d invalid", total, available, rateLimited, invalid)
	}

	w.Header().Set("Content-Type", "application/json")
	response := map[string]interface{}{
		"status":    "ok",
		"timestamp": formatISOTimeUTC(time.Now()),
		"latencyMs": time.Since(start).Milliseconds(),
		"summary":   summary,
		"counts": map[string]interface{}{
			"total":       total,
			"available":   available,
			"rateLimited": rateLimited,
			"softLimited": softLimited,
			"invalid":     invalid,
			"error":       errorCount,
		},
		"accounts": detailed,
	}

	// Add soft limit settings to response
	if softLimitEnabled {
		response["softLimit"] = map[string]interface{}{
			"enabled":   softLimitEnabled,
			"threshold": softLimitThreshold,
		}
	}

	_ = json.NewEncoder(w).Encode(response)
}

// handleAccountLimits handles GET /account-limits requests.
func (s *Server) handleAccountLimits(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.handleNotFound(w, r)
		return
	}

	if err := s.ensureInitialized(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "error",
			"error":  err.Error(),
		})
		return
	}

	allAccounts := []account.Account{}
	if s.accountManager != nil {
		allAccounts = s.accountManager.GetAllAccounts()
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}

	accountLimits := make([]map[string]interface{}, 0, len(allAccounts))

	zaiClient := zai.NewClient()
	zaiModels := []string{}
	if s.registry != nil {
		if p, ok := s.registry.GetByName("zai"); ok && p != nil {
			zaiModels = p.Models()
		}
	}

	for i := range allAccounts {
		acc := allAccounts[i]
		providerName := acc.Provider
		if providerName == "" {
			providerName = "antigravity"
		}

		if acc.IsInvalid {
			accountLimits = append(accountLimits, map[string]interface{}{
				"email":    acc.Email,
				"provider": providerName,
				"status":   "invalid",
				"error":    acc.InvalidReason,
				"models":   map[string]interface{}{},
			})
			continue
		}

		// Use a shorter timeout for quota fetches.
		quotaCtx, quotaCancel := context.WithTimeout(r.Context(), config.QuotaFetchTimeout)

		switch providerName {
		case "zai":
			if acc.APIKey == "" {
				quotaCancel()
				accountLimits = append(accountLimits, map[string]interface{}{
					"email":    acc.Email,
					"provider": providerName,
					"status":   "error",
					"error":    "no API key",
					"models":   map[string]interface{}{},
				})
				continue
			}

			quotaInfo, err := zaiClient.FetchQuota(quotaCtx, acc.APIKey)
			quotaCancel()
			if err != nil {
				accountLimits = append(accountLimits, map[string]interface{}{
					"email":    acc.Email,
					"provider": providerName,
					"status":   "error",
					"error":    err.Error(),
					"models":   map[string]interface{}{},
				})
				continue
			}

			quotas := make(map[string]interface{}, len(zaiModels))
			for _, modelID := range zaiModels {
				quotas[fmt.Sprintf("%s/%s", providerName, modelID)] = map[string]interface{}{
					"remainingFraction": quotaInfo.RemainingFraction,
					"resetTime":         quotaInfo.ResetTime,
				}
			}

			accountLimits = append(accountLimits, map[string]interface{}{
				"email":    acc.Email,
				"provider": providerName,
				"status":   "ok",
				"models":   quotas,
			})

		default:
			token, err := s.accountManager.GetTokenForAccount(&acc)
			if err != nil {
				quotaCancel()
				accountLimits = append(accountLimits, map[string]interface{}{
					"email":    acc.Email,
					"provider": providerName,
					"status":   "error",
					"error":    err.Error(),
					"models":   map[string]interface{}{},
				})
				continue
			}

			rawQuotas, err := s.getModelQuotas(quotaCtx, token)
			quotaCancel()
			if err != nil {
				accountLimits = append(accountLimits, map[string]interface{}{
					"email":    acc.Email,
					"provider": providerName,
					"status":   "error",
					"error":    err.Error(),
					"models":   map[string]interface{}{},
				})
				continue
			}

			quotas := make(map[string]interface{}, len(rawQuotas))
			for modelID, info := range rawQuotas {
				quotas[fmt.Sprintf("%s/%s", providerName, modelID)] = info
			}

			accountLimits = append(accountLimits, map[string]interface{}{
				"email":    acc.Email,
				"provider": providerName,
				"status":   "ok",
				"models":   quotas,
			})
		}
	}

	// Collect all unique model IDs (Node parity).
	modelSet := make(map[string]struct{})
	for _, acc := range accountLimits {
		models, _ := acc["models"].(map[string]interface{})
		for modelID := range models {
			modelSet[modelID] = struct{}{}
		}
	}

	sortedModels := make([]string, 0, len(modelSet))
	for modelID := range modelSet {
		sortedModels = append(sortedModels, modelID)
	}
	sort.Strings(sortedModels)

	if format == "table" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(renderAccountLimitsTable(time.Now(), allAccounts, accountLimits, sortedModels)))
		return
	}

	// Default: JSON format (Node parity).
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"timestamp":     time.Now().In(time.Local).Format("1/2/2006, 3:04:05 PM"),
		"totalAccounts": len(allAccounts),
		"models":        sortedModels,
		"accounts":      renderAccountLimitsJSON(sortedModels, accountLimits),
	})
}

// Helper functions

func writeError(w http.ResponseWriter, statusCode int, errorType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(types.AnthropicError{
		Type: "error",
		Error: types.ErrorDetail{
			Type:    errorType,
			Message: message,
		},
	})
}

func (s *Server) ensureInitialized() error {
	if s.accountManager == nil {
		return nil
	}
	return s.accountManager.Initialize()
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	if utils.IsDebugEnabled() {
		utils.Debug("[API] 404 Not Found: %s %s", r.Method, r.URL.RequestURI())
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"type": "error",
		"error": map[string]interface{}{
			"type":    "not_found_error",
			"message": fmt.Sprintf("Endpoint %s %s not found", r.Method, r.URL.RequestURI()),
		},
	})
}

func (s *Server) getModelQuotas(ctx context.Context, token string) (map[string]interface{}, error) {
	data, err := s.agClient.FetchAvailableModels(ctx, token)
	if err != nil {
		return nil, err
	}
	if data == nil || data.Models == nil {
		return map[string]interface{}{}, nil
	}

	quotas := make(map[string]interface{})
	for modelID, modelData := range data.Models {
		family := config.GetModelFamily(modelID)
		if family != config.ModelFamilyClaude && family != config.ModelFamilyGemini {
			continue
		}
		if modelData.QuotaInfo == nil {
			continue
		}

		var rf any = nil
		if modelData.QuotaInfo.RemainingFraction != nil {
			rf = *modelData.QuotaInfo.RemainingFraction
		}
		var rt any = nil
		if modelData.QuotaInfo.ResetTime != nil && *modelData.QuotaInfo.ResetTime != "" {
			rt = *modelData.QuotaInfo.ResetTime
		}

		quotas[modelID] = map[string]interface{}{
			"remainingFraction": rf,
			"resetTime":         rt,
		}
	}

	return quotas, nil
}
