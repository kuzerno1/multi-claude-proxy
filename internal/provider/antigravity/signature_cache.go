// Package antigravity implements the Antigravity Cloud Code provider.
package antigravity

import (
	"sync"
	"time"

	"github.com/kuzerno1/multi-claude-proxy/internal/config"
)

// signatureEntry stores a cached signature with timestamp.
type signatureEntry struct {
	signature string
	timestamp time.Time
}

// thinkingSignatureEntry stores a thinking signature with model family.
type thinkingSignatureEntry struct {
	modelFamily string
	timestamp   time.Time
}

// SignatureCache caches Gemini thoughtSignatures for tool_use blocks.
// Claude Code strips non-standard fields, so we cache them for restoration.
type SignatureCache struct {
	mu              sync.RWMutex
	toolSignatures  map[string]signatureEntry         // tool_use_id -> signature
	thinkingCache   map[string]thinkingSignatureEntry // signature -> model family
	ttl             time.Duration
	minSignatureLen int
}

// NewSignatureCache creates a new SignatureCache with default settings.
func NewSignatureCache() *SignatureCache {
	return &SignatureCache{
		toolSignatures:  make(map[string]signatureEntry),
		thinkingCache:   make(map[string]thinkingSignatureEntry),
		ttl:             config.GeminiSignatureCacheTTL,
		minSignatureLen: config.MinSignatureLength,
	}
}

// CacheToolSignature stores a signature for a tool_use_id.
func (c *SignatureCache) CacheToolSignature(toolUseID, signature string) {
	if toolUseID == "" || signature == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.toolSignatures[toolUseID] = signatureEntry{
		signature: signature,
		timestamp: time.Now(),
	}
}

// GetToolSignature retrieves a cached signature for a tool_use_id.
// Returns empty string if not found or expired.
func (c *SignatureCache) GetToolSignature(toolUseID string) string {
	if toolUseID == "" {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.toolSignatures[toolUseID]
	if !ok {
		return ""
	}
	if time.Since(entry.timestamp) > c.ttl {
		return ""
	}
	return entry.signature
}

// CacheThinkingSignature stores a thinking signature with its model family.
func (c *SignatureCache) CacheThinkingSignature(signature, modelFamily string) {
	if signature == "" || len(signature) < c.minSignatureLen {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.thinkingCache[signature] = thinkingSignatureEntry{
		modelFamily: modelFamily,
		timestamp:   time.Now(),
	}
}

// GetSignatureFamily retrieves the model family for a cached thinking signature.
// Returns empty string if not found or expired.
func (c *SignatureCache) GetSignatureFamily(signature string) string {
	if signature == "" {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.thinkingCache[signature]
	if !ok {
		return ""
	}
	if time.Since(entry.timestamp) > c.ttl {
		return ""
	}
	return entry.modelFamily
}

// Cleanup removes expired entries from both caches.
func (c *SignatureCache) Cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()

	for id, entry := range c.toolSignatures {
		if now.Sub(entry.timestamp) > c.ttl {
			delete(c.toolSignatures, id)
		}
	}

	for sig, entry := range c.thinkingCache {
		if now.Sub(entry.timestamp) > c.ttl {
			delete(c.thinkingCache, sig)
		}
	}
}

// Size returns the current number of entries in the tool signature cache.
func (c *SignatureCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.toolSignatures)
}

// ThinkingCacheSize returns the current number of entries in the thinking cache.
func (c *SignatureCache) ThinkingCacheSize() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.thinkingCache)
}

// Global signature cache instance
var globalSignatureCache = NewSignatureCache()

// GetGlobalSignatureCache returns the global signature cache instance.
func GetGlobalSignatureCache() *SignatureCache {
	return globalSignatureCache
}
