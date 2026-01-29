package copilot

import (
	"context"
	"strings"
	"testing"

	"github.com/kuzerno1/multi-claude-proxy/pkg/types"
)

func TestNewStreamState(t *testing.T) {
	state := NewStreamState("gpt-4")

	if state.Model != "gpt-4" {
		t.Errorf("expected model gpt-4, got %s", state.Model)
	}

	if state.MessageStartSent {
		t.Error("expected MessageStartSent to be false")
	}

	if state.ContentBlockOpen {
		t.Error("expected ContentBlockOpen to be false")
	}

	if state.ToolCalls == nil {
		t.Error("expected ToolCalls to be initialized")
	}

	if state.MessageID == "" {
		t.Error("expected MessageID to be set")
	}
}

func TestParseSSEStream_BasicTextContent(t *testing.T) {
	sseData := `data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}

data: [DONE]
`
	ctx := context.Background()
	reader := strings.NewReader(sseData)
	events := ParseSSEStream(ctx, reader, "gpt-4")

	var collectedEvents []types.StreamEvent
	for event := range events {
		collectedEvents = append(collectedEvents, event)
	}

	// Should have: message_start, content_block_start, 2x content_block_delta, content_block_stop, message_delta, message_stop
	if len(collectedEvents) < 5 {
		t.Errorf("expected at least 5 events, got %d", len(collectedEvents))
	}

	// First event should be message_start
	if collectedEvents[0].Type != "message_start" {
		t.Errorf("expected first event type message_start, got %s", collectedEvents[0].Type)
	}

	// Check that message_stop is in there
	hasMessageStop := false
	for _, ev := range collectedEvents {
		if ev.Type == "message_stop" {
			hasMessageStop = true
			break
		}
	}
	if !hasMessageStop {
		t.Error("expected message_stop event")
	}
}

func TestParseSSEStream_ContextCancellation(t *testing.T) {
	// Create a stream that would normally take a while
	sseData := `data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}

`
	ctx, cancel := context.WithCancel(context.Background())

	reader := strings.NewReader(sseData)
	events := ParseSSEStream(ctx, reader, "gpt-4")

	// Cancel immediately
	cancel()

	// Drain channel - should complete quickly due to cancellation
	eventCount := 0
	for range events {
		eventCount++
	}

	// Should have received at least message_start before cancellation
	// But the key is that the channel closed properly
	t.Logf("Received %d events before cancellation", eventCount)
}

func TestParseSSEStream_SkipsInvalidJSON(t *testing.T) {
	sseData := `data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}

data: {invalid json}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]
`
	ctx := context.Background()
	reader := strings.NewReader(sseData)
	events := ParseSSEStream(ctx, reader, "gpt-4")

	var collectedEvents []types.StreamEvent
	for event := range events {
		collectedEvents = append(collectedEvents, event)
	}

	// Should still process valid events
	if len(collectedEvents) < 3 {
		t.Errorf("expected at least 3 events, got %d", len(collectedEvents))
	}
}

func TestParseSSEStream_SkipsComments(t *testing.T) {
	sseData := `: this is a comment
data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"},"finish_reason":null}]}

: another comment
data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]
`
	ctx := context.Background()
	reader := strings.NewReader(sseData)
	events := ParseSSEStream(ctx, reader, "gpt-4")

	var collectedEvents []types.StreamEvent
	for event := range events {
		collectedEvents = append(collectedEvents, event)
	}

	// Should have processed the valid data events
	if len(collectedEvents) < 3 {
		t.Errorf("expected at least 3 events, got %d", len(collectedEvents))
	}
}

func TestCreateErrorEvent(t *testing.T) {
	event := CreateErrorEvent("api_error", "Something went wrong")

	if event.Type != "error" {
		t.Errorf("expected event type error, got %s", event.Type)
	}

	if event.Error == nil {
		t.Fatal("expected error details")
	}

	if event.Error.Type != "api_error" {
		t.Errorf("expected error type api_error, got %s", event.Error.Type)
	}

	if event.Error.Message != "Something went wrong" {
		t.Errorf("expected error message 'Something went wrong', got %s", event.Error.Message)
	}
}

func TestTranslateChunkToAnthropicEvents_EmptyChoices(t *testing.T) {
	state := NewStreamState("gpt-4")
	chunk := &ChatCompletionChunk{
		ID:      "chatcmpl-123",
		Choices: []StreamChoice{},
	}

	events := translateChunkToAnthropicEvents(chunk, state)

	if len(events) != 0 {
		t.Errorf("expected 0 events for empty choices, got %d", len(events))
	}
}

func TestIsToolBlockOpen(t *testing.T) {
	state := NewStreamState("gpt-4")

	// Initially no block open
	if isToolBlockOpen(state) {
		t.Error("expected no tool block open initially")
	}

	// Open a text block
	state.ContentBlockOpen = true
	state.ContentBlockIndex = 0

	// Still not a tool block
	if isToolBlockOpen(state) {
		t.Error("expected text block not to be tool block")
	}

	// Add a tool call at index 0
	state.ToolCalls[0] = &ToolCallState{
		ID:                  "call_123",
		Name:                "test_tool",
		AnthropicBlockIndex: 0,
	}

	// Now it should be a tool block
	if !isToolBlockOpen(state) {
		t.Error("expected tool block to be open")
	}
}
