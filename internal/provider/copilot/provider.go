package copilot

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kuzerno1/multi-claude-proxy/internal/account"
	"github.com/kuzerno1/multi-claude-proxy/internal/config"
	"github.com/kuzerno1/multi-claude-proxy/internal/utils"
	"github.com/kuzerno1/multi-claude-proxy/pkg/types"
)

const providerName = "copilot"

// Provider implements the GitHub Copilot provider.
type Provider struct {
	accountManager *account.Manager
	models         []Model
	modelIDs       []string
	modelSet       map[string]bool
	modelEndpoints map[string]string // model ID -> preferred endpoint
	modelsMu       sync.RWMutex

	// Token cache: account email -> cached copilot token
	tokenCache   map[string]*cachedToken
	tokenCacheMu sync.RWMutex
}

// cachedToken stores a Copilot token with its expiry.
type cachedToken struct {
	token     string
	expiresAt time.Time
}

// NewProvider creates a new Copilot provider.
func NewProvider(accountManager *account.Manager) *Provider {
	return &Provider{
		accountManager: accountManager,
		models:         []Model{},
		modelIDs:       []string{},
		modelSet:       make(map[string]bool),
		modelEndpoints: make(map[string]string),
		tokenCache:     make(map[string]*cachedToken),
	}
}

// Name returns the provider identifier.
func (p *Provider) Name() string {
	return providerName
}

// Models returns the list of model IDs this provider supports.
func (p *Provider) Models() []string {
	p.modelsMu.RLock()
	defer p.modelsMu.RUnlock()
	result := make([]string, len(p.modelIDs))
	copy(result, p.modelIDs)
	return result
}

// SupportsModel returns true if this provider handles the given model.
func (p *Provider) SupportsModel(model string) bool {
	p.modelsMu.RLock()
	defer p.modelsMu.RUnlock()
	return p.modelSet[model]
}

// GetModelEndpoint returns the preferred API endpoint for the given model.
// Returns DefaultEndpoint if the model is not found.
func (p *Provider) GetModelEndpoint(model string) string {
	p.modelsMu.RLock()
	defer p.modelsMu.RUnlock()
	if endpoint, ok := p.modelEndpoints[model]; ok {
		return endpoint
	}
	return DefaultEndpoint
}

// Initialize performs any setup required by the provider.
func (p *Provider) Initialize(ctx context.Context) error {
	accounts := p.accountManager.GetAllAccountsByProvider(providerName)
	if len(accounts) == 0 {
		utils.Debug("[Copilot] No Copilot accounts configured, skipping initialization")
		return nil
	}

	// Use the first available account to fetch models
	for _, acc := range accounts {
		if acc.IsInvalid {
			continue
		}
		if acc.RefreshToken == "" {
			continue
		}

		// Get Copilot token
		copilotToken, err := p.getCopilotToken(ctx, &acc)
		if err != nil {
			utils.Warn("[Copilot] Failed to get token for account %s: %v", acc.Email, err)
			continue
		}

		// Get account type
		accountType := getAccountType(&acc)
		client := NewClient(accountType)

		// Fetch models
		modelsResp, err := client.GetModels(ctx, copilotToken)
		if err != nil {
			utils.Warn("[Copilot] Failed to fetch models using account %s: %v", acc.Email, err)
			continue
		}

		// Filter to model_picker_enabled models
		p.modelsMu.Lock()
		p.models = []Model{}
		p.modelIDs = []string{}
		p.modelSet = make(map[string]bool)
		p.modelEndpoints = make(map[string]string)

		for _, m := range modelsResp.Data {
			if m.ModelPickerEnabled {
				p.models = append(p.models, m)
				p.modelIDs = append(p.modelIDs, m.ID)
				p.modelSet[m.ID] = true
				p.modelEndpoints[m.ID] = m.PreferredEndpoint()
			}
		}
		p.modelsMu.Unlock()

		utils.Success("[Copilot] Provider initialized with %d models", len(p.modelIDs))
		return nil
	}

	utils.Warn("[Copilot] No valid Copilot accounts available to fetch models")
	return nil
}

// Shutdown performs cleanup when the provider is being stopped.
func (p *Provider) Shutdown(ctx context.Context) error {
	utils.Debug("[Copilot] Provider shutting down")
	return nil
}

// SendMessage handles non-streaming requests.
func (p *Provider) SendMessage(ctx context.Context, req *types.AnthropicRequest) (*types.AnthropicResponse, error) {
	maxAttempts := config.MaxRetries
	if count := p.accountManager.GetAccountCountByProvider(providerName) + 1; count > maxAttempts {
		maxAttempts = count
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		acc := p.accountManager.PickNextByProvider(providerName, req.Model)

		// Handle all accounts rate-limited
		if acc == nil && p.accountManager.IsAllRateLimitedByProvider(providerName, req.Model) {
			var err error
			acc, err = p.waitForRateLimitReset(ctx, req.Model)
			if err != nil {
				return nil, err
			}
		}

		if acc == nil {
			return nil, fmt.Errorf("no Copilot accounts available")
		}

		// Get Copilot token
		copilotToken, err := p.getCopilotToken(ctx, acc)
		if err != nil {
			utils.Warn("[Copilot] Failed to get token for %s: %v, trying next...", acc.Email, err)
			p.accountManager.MarkInvalid(acc.Email, err.Error())
			continue
		}

		// Get endpoint for this model
		endpoint := p.GetModelEndpoint(req.Model)

		// Convert request to correct OpenAI format based on endpoint
		var payload interface{}
		if endpoint == "/responses" {
			payload, err = TranslateToOpenAIResponses(req)
		} else {
			payload, err = TranslateToOpenAI(req)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to convert request: %w", err)
		}

		// Create client with correct account type
		accountType := getAccountType(acc)
		client := NewClient(accountType)

		// Send request
		openAIResp, err := client.SendMessage(ctx, copilotToken, payload, endpoint)
		if err != nil {
			if p.handleRequestError(err, acc, req.Model) == retryActionContinue {
				continue
			}
			return nil, err
		}

		// Convert response to Anthropic format based on response type
		var resp *types.AnthropicResponse
		switch r := openAIResp.(type) {
		case *ChatCompletionResponse:
			resp = TranslateToAnthropic(r, req.Model)
		case *ResponsesAPIResponse:
			resp = TranslateResponsesAPIToAnthropic(r, req.Model)
		default:
			return nil, fmt.Errorf("unexpected response type: %T", openAIResp)
		}
		return resp, nil
	}

	return nil, fmt.Errorf("max retries exceeded")
}

// SendMessageStream handles streaming requests.
func (p *Provider) SendMessageStream(ctx context.Context, req *types.AnthropicRequest) (<-chan types.StreamEvent, error) {
	maxAttempts := config.MaxRetries
	if count := p.accountManager.GetAccountCountByProvider(providerName) + 1; count > maxAttempts {
		maxAttempts = count
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		acc := p.accountManager.PickNextByProvider(providerName, req.Model)

		// Handle all accounts rate-limited
		if acc == nil && p.accountManager.IsAllRateLimitedByProvider(providerName, req.Model) {
			var err error
			acc, err = p.waitForRateLimitReset(ctx, req.Model)
			if err != nil {
				return nil, err
			}
		}

		if acc == nil {
			return nil, fmt.Errorf("no Copilot accounts available")
		}

		// Get Copilot token
		copilotToken, err := p.getCopilotToken(ctx, acc)
		if err != nil {
			utils.Warn("[Copilot] Failed to get token for %s: %v, trying next...", acc.Email, err)
			p.accountManager.MarkInvalid(acc.Email, err.Error())
			continue
		}

		// Get endpoint for this model
		endpoint := p.GetModelEndpoint(req.Model)

		// Convert request to correct OpenAI format based on endpoint
		var payload interface{}
		if endpoint == "/responses" {
			payload, err = TranslateToOpenAIResponses(req)
		} else {
			payload, err = TranslateToOpenAI(req)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to convert request: %w", err)
		}

		// Create client with correct account type
		accountType := getAccountType(acc)
		client := NewClient(accountType)

		// Send streaming request
		reader, err := client.SendMessageStream(ctx, copilotToken, payload, endpoint)
		if err != nil {
			if p.handleRequestError(err, acc, req.Model) == retryActionContinue {
				continue
			}
			return nil, err
		}

		// Parse SSE stream and convert to Anthropic format
		// Use the correct parser based on endpoint
		var events <-chan types.StreamEvent
		if endpoint == "/responses" {
			events = ParseSSEStreamResponses(ctx, reader, req.Model)
		} else {
			events = ParseSSEStream(ctx, reader, req.Model)
		}

		// Create output channel that will close reader when done
		outCh := make(chan types.StreamEvent, 100)
		go func() {
			defer close(outCh)
			defer reader.Close()

			for evt := range events {
				select {
				case outCh <- evt:
				case <-ctx.Done():
					return
				}
			}
		}()

		return outCh, nil
	}

	return nil, fmt.Errorf("max retries exceeded")
}

// ListModels returns available models with metadata.
func (p *Provider) ListModels(ctx context.Context) (*types.ModelsResponse, error) {
	p.modelsMu.RLock()
	models := make([]types.Model, len(p.models))
	for i, m := range p.models {
		models[i] = types.Model{
			ID:          m.ID,
			DisplayName: m.Name,
			Type:        "model",
		}
		if models[i].DisplayName == "" {
			models[i].DisplayName = m.ID
		}
	}
	p.modelsMu.RUnlock()

	return &types.ModelsResponse{
		Data: models,
	}, nil
}

// GetStatus returns provider health and quota information.
func (p *Provider) GetStatus(ctx context.Context) (*types.ProviderStatus, error) {
	accounts := p.accountManager.GetAllAccountsByProvider(providerName)
	accountStatuses := make([]types.AccountStatus, len(accounts))

	overallStatus := "ok"

	for i, acc := range accounts {
		status := types.AccountStatus{
			Email:    acc.Email,
			Status:   "ok",
			LastUsed: acc.LastUsed,
			Limits:   make(map[string]types.ModelQuota),
		}

		if acc.IsInvalid {
			status.Status = "invalid"
			status.Error = string(acc.InvalidReason)
			overallStatus = "degraded"
			accountStatuses[i] = status
			continue
		}

		if acc.RefreshToken == "" {
			status.Status = "error"
			status.Error = "no refresh token"
			overallStatus = "degraded"
			accountStatuses[i] = status
			continue
		}

		// Check if we can get a copilot token
		_, err := p.getCopilotToken(ctx, &acc)
		if err != nil {
			status.Status = "error"
			status.Error = fmt.Sprintf("token fetch failed: %v", err)
			overallStatus = "degraded"
		}

		// Set default quota (Copilot doesn't expose per-model quota)
		p.modelsMu.RLock()
		for _, modelID := range p.modelIDs {
			// Check rate limit status from account manager
			if limit, ok := acc.ModelRateLimits[modelID]; ok && limit.IsRateLimited {
				status.Limits[modelID] = types.ModelQuota{
					RemainingFraction:   0,
					RemainingPercentage: 0,
				}
				status.Status = "rate-limited"
			} else {
				status.Limits[modelID] = types.ModelQuota{
					RemainingFraction:   1.0,
					RemainingPercentage: 100,
				}
			}
		}
		p.modelsMu.RUnlock()

		if status.Status != "ok" {
			overallStatus = "degraded"
		}

		accountStatuses[i] = status
	}

	return &types.ProviderStatus{
		Name:      providerName,
		Status:    overallStatus,
		Accounts:  accountStatuses,
		Timestamp: time.Now(),
	}, nil
}

// GenerateImage generates images from text prompts.
// GitHub Copilot does not support image generation.
func (p *Provider) GenerateImage(ctx context.Context, req *types.ImageGenerationRequest) (*types.ImageGenerationResponse, error) {
	return nil, fmt.Errorf("image generation is not supported by GitHub Copilot provider")
}

// getCopilotToken gets a valid Copilot token for the account.
// Uses caching to avoid unnecessary token exchanges.
func (p *Provider) getCopilotToken(ctx context.Context, acc *account.Account) (string, error) {
	// Check cache first
	p.tokenCacheMu.RLock()
	cached, ok := p.tokenCache[acc.Email]
	p.tokenCacheMu.RUnlock()

	if ok && time.Now().Before(cached.expiresAt.Add(-60*time.Second)) {
		return cached.token, nil
	}

	// Get GitHub token (stored as RefreshToken)
	githubToken := acc.RefreshToken
	if githubToken == "" {
		return "", fmt.Errorf("no GitHub token for account %s", acc.Email)
	}

	// Exchange for Copilot token
	accountType := getAccountType(acc)
	tokenResp, err := GetCopilotToken(ctx, githubToken, accountType)
	if err != nil {
		return "", err
	}

	// Cache the token
	p.tokenCacheMu.Lock()
	p.tokenCache[acc.Email] = &cachedToken{
		token:     tokenResp.Token,
		expiresAt: time.Unix(tokenResp.ExpiresAt, 0),
	}
	p.tokenCacheMu.Unlock()

	return tokenResp.Token, nil
}

// invalidateToken removes a cached token.
func (p *Provider) invalidateToken(email string) {
	p.tokenCacheMu.Lock()
	delete(p.tokenCache, email)
	p.tokenCacheMu.Unlock()
}

// getAccountType returns the AccountType for an account.
func getAccountType(acc *account.Account) AccountType {
	// AccountType is stored in a custom field
	// Default to individual if not set
	switch acc.AccountType {
	case "business":
		return AccountTypeBusiness
	case "enterprise":
		return AccountTypeEnterprise
	default:
		return AccountTypeIndividual
	}
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// waitForRateLimitReset waits for rate limit to reset or returns an error if wait is too long.
func (p *Provider) waitForRateLimitReset(ctx context.Context, modelID string) (*account.Account, error) {
	allWaitMs := p.accountManager.GetMinWaitTimeMsByProvider(providerName, modelID)
	waitDur := time.Duration(allWaitMs) * time.Millisecond
	resetTime := time.Now().Add(waitDur).UTC().Format("2006-01-02T15:04:05.000Z")

	if waitDur > config.MaxWaitBeforeError {
		return nil, fmt.Errorf(
			"RESOURCE_EXHAUSTED: Rate limited on %s. Quota will reset after %s. Next available: %s",
			modelID,
			utils.FormatDuration(waitDur),
			resetTime,
		)
	}

	accountCount := p.accountManager.GetAccountCountByProvider(providerName)
	utils.Warn("[Copilot] All %d account(s) rate-limited. Waiting %s...",
		accountCount,
		utils.FormatDuration(waitDur),
	)

	if err := sleepWithContext(ctx, waitDur); err != nil {
		return nil, err
	}
	if err := sleepWithContext(ctx, config.PostRateLimitBuffer); err != nil {
		return nil, err
	}
	p.accountManager.ResetAllRateLimitsByProvider(providerName)

	return p.accountManager.PickNextByProvider(providerName, modelID), nil
}

// retryAction indicates what action to take after handling an error.
type retryAction int

const (
	retryActionContinue retryAction = iota // Retry with next account
	retryActionFail                        // Return the error
)

// handleRequestError processes an error and returns whether to retry.
func (p *Provider) handleRequestError(err error, acc *account.Account, modelID string) retryAction {
	// Rate limited
	var rateLimitErr *RateLimitError
	if errors.As(err, &rateLimitErr) {
		p.accountManager.MarkRateLimited(acc.Email, rateLimitErr.RetryAfterMs(), modelID)
		utils.Info("[Copilot] Account %s rate-limited, trying next...", acc.Email)
		return retryActionContinue
	}

	// Auth error
	var authErr *AuthError
	if errors.As(err, &authErr) {
		p.invalidateToken(acc.Email)
		p.accountManager.MarkInvalid(acc.Email, "authentication failed")
		utils.Warn("[Copilot] Account %s auth failed, trying next...", acc.Email)
		return retryActionContinue
	}

	// HTTP error - retry on 5xx
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		if httpErr.StatusCode >= 500 {
			utils.Warn("[Copilot] Account %s failed with %d error, trying next...", acc.Email, httpErr.StatusCode)
			return retryActionContinue
		}
	}

	return retryActionFail
}
