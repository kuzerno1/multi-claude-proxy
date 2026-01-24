// Package provider contains the provider registry for routing models to backends.
package provider

import (
	"fmt"
	"sync"
)

func prefixedModelID(providerName, modelID string) string {
	return fmt.Sprintf("%s/%s", providerName, modelID)
}

// Registry manages registered providers and routes models to the appropriate provider.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider // name -> provider
	modelMap  map[string]Provider // provider/model -> provider
}

// NewRegistry creates a new provider registry.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]Provider),
		modelMap:  make(map[string]Provider),
	}
}

// Register adds a provider to the registry.
// It also maps all models supported by the provider.
func (r *Registry) Register(p Provider) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := p.Name()
	if _, exists := r.providers[name]; exists {
		return fmt.Errorf("provider %q already registered", name)
	}

	r.providers[name] = p

	// Map all models to this provider. Models are registered as "<provider>/<model>" to avoid collisions.
	for _, model := range p.Models() {
		key := prefixedModelID(name, model)
		if existing, exists := r.modelMap[key]; exists {
			return fmt.Errorf("model %q already registered by provider %q", key, existing.Name())
		}
		r.modelMap[key] = p
	}

	return nil
}

// GetByName returns a provider by its name.
func (r *Registry) GetByName(name string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	return p, ok
}

// GetByModel returns the provider that handles the given model.
func (r *Registry) GetByModel(model string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.modelMap[model]
	return p, ok
}

// All returns all registered providers.
func (r *Registry) All() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		result = append(result, p)
	}
	return result
}

// AllModels returns all registered models across all providers.
func (r *Registry) AllModels() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]string, 0, len(r.modelMap))
	for model := range r.modelMap {
		result = append(result, model)
	}
	return result
}
