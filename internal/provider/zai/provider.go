package zai

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

const providerName = "zai"

// Provider implements the Z.AI provider for Anthropic-compatible passthrough.
type Provider struct {
	accountManager *account.Manager
	client         *Client
	models         []string           // Model IDs for backwards compatibility
	modelEntries   []ModelEntry       // Full model entries with display_name and created_at
	modelSet       map[string]bool
	modelsMu       sync.RWMutex
}

// NewProvider creates a new Z.AI provider.
func NewProvider(accountManager *account.Manager) *Provider {
	return &Provider{
		accountManager: accountManager,
		client:         NewClient(),
		models:         []string{},
		modelEntries:   []ModelEntry{},
		modelSet:       make(map[string]bool),
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
	result := make([]string, len(p.models))
	copy(result, p.models)
	return result
}

// SupportsModel returns true if this provider handles the given model.
func (p *Provider) SupportsModel(model string) bool {
	p.modelsMu.RLock()
	defer p.modelsMu.RUnlock()
	return p.modelSet[model]
}

// Initialize performs any setup required by the provider.
func (p *Provider) Initialize(ctx context.Context) error {
	accounts := p.accountManager.GetAllAccountsByProvider(providerName)
	if len(accounts) == 0 {
		utils.Debug("[Z.AI] No Z.AI accounts configured, skipping initialization")
		return nil
	}

	// Use the first available account to fetch models
	for _, acc := range accounts {
		if acc.IsInvalid {
			continue
		}
		if acc.APIKey == "" {
			continue
		}

		modelEntries, err := p.client.FetchModels(ctx, acc.APIKey)
		if err != nil {
			utils.Warn("[Z.AI] Failed to fetch models using account %s: %v", acc.Email, err)
			continue
		}

		p.modelsMu.Lock()
		p.modelEntries = modelEntries
		p.models = make([]string, len(modelEntries))
		p.modelSet = make(map[string]bool, len(modelEntries))
		for i, m := range modelEntries {
			p.models[i] = m.ID
			p.modelSet[m.ID] = true
		}
		p.modelsMu.Unlock()

		utils.Success("[Z.AI] Provider initialized with %d models", len(modelEntries))
		return nil
	}

	utils.Warn("[Z.AI] No valid Z.AI accounts available to fetch models")
	return nil
}

// Shutdown performs cleanup when the provider is being stopped.
func (p *Provider) Shutdown(ctx context.Context) error {
	utils.Debug("[Z.AI] Provider shutting down")
	return nil
}

// SendMessage handles non-streaming requests.
func (p *Provider) SendMessage(ctx context.Context, req *types.AnthropicRequest) (*types.AnthropicResponse, error) {
	maxAttempts := config.MaxRetries
	if count := p.accountManager.GetAccountCountByProvider(providerName) + 1; count > maxAttempts {
		maxAttempts = count
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		// Pick next available account using round-robin selection
		acc := p.accountManager.PickNextByProvider(providerName, req.Model)

		// Handle all accounts rate-limited
		if acc == nil && p.accountManager.IsAllRateLimitedByProvider(providerName, req.Model) {
			allWaitMs := p.accountManager.GetMinWaitTimeMsByProvider(providerName, req.Model)
			waitDur := time.Duration(allWaitMs) * time.Millisecond
			resetTime := time.Now().Add(waitDur).UTC().Format("2006-01-02T15:04:05.000Z")

			if waitDur > config.MaxWaitBeforeError {
				return nil, fmt.Errorf(
					"RESOURCE_EXHAUSTED: Rate limited on %s. Quota will reset after %s. Next available: %s",
					req.Model,
					utils.FormatDuration(waitDur),
					resetTime,
				)
			}

			accountCount := p.accountManager.GetAccountCountByProvider(providerName)
			utils.Warn("[Z.AI] All %d account(s) rate-limited. Waiting %s...",
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
			acc = p.accountManager.PickNextByProvider(providerName, req.Model)
		}

		if acc == nil {
			return nil, fmt.Errorf("no Z.AI accounts available")
		}

		// Get API key
		apiKey := acc.APIKey
		if apiKey == "" {
			utils.Warn("[Z.AI] Account %s has no API key, trying next...", acc.Email)
			continue
		}

		// Send request
		resp, err := p.client.SendMessage(ctx, apiKey, req)
		if err != nil {
			// Rate limited - mark and continue
			var rateLimitErr *RateLimitError
			if errors.As(err, &rateLimitErr) {
				p.accountManager.MarkRateLimited(acc.Email, rateLimitErr.ResetMs, req.Model)
				utils.Info("[Z.AI] Account %s rate-limited, trying next...", acc.Email)
				continue
			}

			// Auth error - mark invalid
			var httpErr *HTTPStatusError
			if errors.As(err, &httpErr) {
				if httpErr.StatusCode == 401 || httpErr.StatusCode == 403 {
					p.accountManager.MarkInvalid(acc.Email, "invalid API key")
					utils.Warn("[Z.AI] Account %s has invalid API key, trying next...", acc.Email)
					continue
				}

				// 5xx errors - try next account
				if httpErr.StatusCode >= 500 {
					utils.Warn("[Z.AI] Account %s failed with %d error, trying next...", acc.Email, httpErr.StatusCode)
					continue
				}
			}

			return nil, err
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
		// Pick next available account using round-robin selection
		acc := p.accountManager.PickNextByProvider(providerName, req.Model)

		// Handle all accounts rate-limited
		if acc == nil && p.accountManager.IsAllRateLimitedByProvider(providerName, req.Model) {
			allWaitMs := p.accountManager.GetMinWaitTimeMsByProvider(providerName, req.Model)
			waitDur := time.Duration(allWaitMs) * time.Millisecond
			resetTime := time.Now().Add(waitDur).UTC().Format("2006-01-02T15:04:05.000Z")

			if waitDur > config.MaxWaitBeforeError {
				return nil, fmt.Errorf(
					"RESOURCE_EXHAUSTED: Rate limited on %s. Quota will reset after %s. Next available: %s",
					req.Model,
					utils.FormatDuration(waitDur),
					resetTime,
				)
			}

			accountCount := p.accountManager.GetAccountCountByProvider(providerName)
			utils.Warn("[Z.AI] All %d account(s) rate-limited. Waiting %s...",
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
			acc = p.accountManager.PickNextByProvider(providerName, req.Model)
		}

		if acc == nil {
			return nil, fmt.Errorf("no Z.AI accounts available")
		}

		// Get API key
		apiKey := acc.APIKey
		if apiKey == "" {
			utils.Warn("[Z.AI] Account %s has no API key, trying next...", acc.Email)
			continue
		}

		// Send streaming request
		reader, err := p.client.SendMessageStream(ctx, apiKey, req)
		if err != nil {
			// Rate limited - mark and continue
			var rateLimitErr *RateLimitError
			if errors.As(err, &rateLimitErr) {
				p.accountManager.MarkRateLimited(acc.Email, rateLimitErr.ResetMs, req.Model)
				utils.Info("[Z.AI] Account %s rate-limited, trying next...", acc.Email)
				continue
			}

			// Auth error - mark invalid
			var httpErr *HTTPStatusError
			if errors.As(err, &httpErr) {
				if httpErr.StatusCode == 401 || httpErr.StatusCode == 403 {
					p.accountManager.MarkInvalid(acc.Email, "invalid API key")
					utils.Warn("[Z.AI] Account %s has invalid API key, trying next...", acc.Email)
					continue
				}

				// 5xx errors - try next account
				if httpErr.StatusCode >= 500 {
					utils.Warn("[Z.AI] Account %s failed with %d error, trying next...", acc.Email, httpErr.StatusCode)
					continue
				}
			}

			return nil, err
		}

		// Parse SSE stream
		parser := NewStreamingParser(reader)
		events, done := parser.StreamEvents()

		// Create output channel
		outCh := make(chan types.StreamEvent, 100)

		go func() {
			defer close(outCh)

			for evt := range events {
				select {
				case outCh <- evt:
				case <-ctx.Done():
					return
				}
			}

			// Wait for parser to finish and log any error.
			if err := <-done; err != nil {
				utils.Error("[Z.AI] SSE stream parsing error: %v", err)
				// Emit an error event to the caller so they're aware of truncation.
				select {
				case outCh <- types.StreamEvent{
					Type: "error",
					Raw: map[string]interface{}{
						"type": "error",
						"error": map[string]interface{}{
							"type":    "stream_error",
							"message": err.Error(),
						},
					},
				}:
				case <-ctx.Done():
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
	models := make([]types.Model, len(p.modelEntries))
	for i, m := range p.modelEntries {
		models[i] = types.Model{
			ID:          m.ID,
			DisplayName: m.DisplayName,
			Type:        m.Type,
			CreatedAt:   m.CreatedAt,
		}
		// Fallback if display_name is empty
		if models[i].DisplayName == "" {
			models[i].DisplayName = m.ID
		}
		// Fallback if type is empty
		if models[i].Type == "" {
			models[i].Type = "model"
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

		if acc.APIKey == "" {
			status.Status = "error"
			status.Error = "no API key"
			overallStatus = "degraded"
			accountStatuses[i] = status
			continue
		}

		// Fetch quota info
		quotaInfo, err := p.client.FetchQuota(ctx, acc.APIKey)
		if err != nil {
			utils.Warn("[Z.AI] Failed to fetch quota for %s: %v", acc.Email, err)
			// Mark status as degraded since we can't fetch actual quota.
			status.Status = "degraded"
			status.Error = fmt.Sprintf("quota fetch failed: %v", err)
			// Set unknown quota instead of optimistic 100%.
			p.modelsMu.RLock()
			for _, modelID := range p.models {
				status.Limits[modelID] = types.ModelQuota{
					RemainingFraction:   -1, // Unknown
					RemainingPercentage: -1, // Unknown
				}
			}
			p.modelsMu.RUnlock()
		} else {
			// Apply quota to all models (Z.AI quota is global, not per-model)
			p.modelsMu.RLock()
			for _, modelID := range p.models {
				status.Limits[modelID] = types.ModelQuota{
					RemainingFraction:   quotaInfo.RemainingFraction,
					RemainingPercentage: quotaInfo.RemainingPercentage,
				}

				// Update soft limit status
				p.accountManager.UpdateSoftLimitStatusNoPersist(acc.Email, modelID, quotaInfo.RemainingFraction)

				if quotaInfo.RemainingFraction == 0 {
					status.Status = "rate-limited"
				}
			}
			p.modelsMu.RUnlock()
		}

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
