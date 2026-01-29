// Package provider defines the Provider interface for multi-backend support.
package provider

import (
	"context"

	"github.com/kuzerno1/multi-claude-proxy/pkg/types"
)

// Provider defines the interface that all backend providers must implement.
// Each provider handles the translation between Anthropic API format and
// its native format (e.g., Google Cloud Code, OpenAI, etc.).
type Provider interface {
	// Name returns the provider identifier (e.g., "antigravity", "openai").
	Name() string

	// Models returns the list of model IDs this provider supports.
	Models() []string

	// SupportsModel returns true if this provider handles the given model.
	SupportsModel(model string) bool

	// SendMessage handles non-streaming requests.
	// It converts the Anthropic request to the provider's format, makes the API call,
	// and converts the response back to Anthropic format.
	SendMessage(ctx context.Context, req *types.AnthropicRequest) (*types.AnthropicResponse, error)

	// SendMessageStream handles streaming requests.
	// Returns a channel that yields Anthropic-format SSE events.
	// The channel is closed when the stream ends or an error occurs.
	SendMessageStream(ctx context.Context, req *types.AnthropicRequest) (<-chan types.StreamEvent, error)

	// ListModels returns available models with metadata.
	ListModels(ctx context.Context) (*types.ModelsResponse, error)

	// GetStatus returns provider health and quota information.
	GetStatus(ctx context.Context) (*types.ProviderStatus, error)

	// GenerateImage generates images from text prompts.
	// Returns generated images in base64 format.
	GenerateImage(ctx context.Context, req *types.ImageGenerationRequest) (*types.ImageGenerationResponse, error)

	// Initialize performs any setup required by the provider.
	// Called once at startup.
	Initialize(ctx context.Context) error

	// Shutdown performs cleanup when the provider is being stopped.
	Shutdown(ctx context.Context) error
}
