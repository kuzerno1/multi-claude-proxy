package copilot

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/kuzerno1/multi-claude-proxy/pkg/types"
)

// StreamState tracks the state of an ongoing stream translation.
type StreamState struct {
	MessageStartSent  bool
	ContentBlockIndex int
	ContentBlockOpen  bool
	ToolCalls         map[int]*ToolCallState // OpenAI tool index -> state
	Model             string
	MessageID         string
}

// ToolCallState tracks the state of a tool call being streamed.
type ToolCallState struct {
	ID                  string
	Name                string
	AnthropicBlockIndex int
}

// NewStreamState creates a new stream state.
func NewStreamState(model string) *StreamState {
	return &StreamState{
		ToolCalls: make(map[int]*ToolCallState),
		Model:     model,
		MessageID: GenerateMessageID(),
	}
}

// ParseSSEStream reads an SSE stream and converts to Anthropic events.
// The stream parsing will stop when the context is cancelled.
func ParseSSEStream(ctx context.Context, reader io.Reader, model string) <-chan types.StreamEvent {
	return parseSSEStreamWithFormat(ctx, reader, model, false)
}

// ParseSSEStreamResponses reads an SSE stream from the /responses endpoint and converts to Anthropic events.
func ParseSSEStreamResponses(ctx context.Context, reader io.Reader, model string) <-chan types.StreamEvent {
	return parseSSEStreamWithFormat(ctx, reader, model, true)
}

// parseSSEStreamWithFormat handles SSE parsing for both endpoint formats.
func parseSSEStreamWithFormat(ctx context.Context, reader io.Reader, model string, isResponses bool) <-chan types.StreamEvent {
	events := make(chan types.StreamEvent, 100)

	go func() {
		defer close(events)

		state := NewStreamState(model)
		scanner := bufio.NewScanner(reader)

		for scanner.Scan() {
			// Check for context cancellation
			select {
			case <-ctx.Done():
				return
			default:
			}

			line := scanner.Text()

			// Skip empty lines and comments
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}

			// Parse SSE data line
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")

			// Check for stream end
			if data == "[DONE]" {
				break
			}

			// Try to parse based on format
			var anthropicEvents []types.StreamEvent
			if isResponses {
				anthropicEvents = parseResponsesStreamData(data, state)
			} else {
				// Parse the JSON chunk as ChatCompletionChunk
				var chunk ChatCompletionChunk
				if err := json.Unmarshal([]byte(data), &chunk); err != nil {
					continue
				}
				anthropicEvents = translateChunkToAnthropicEvents(&chunk, state)
			}

			for _, event := range anthropicEvents {
				select {
				case events <- event:
				case <-ctx.Done():
					return
				}
			}
		}

		// Ensure we close any open content block
		if state.ContentBlockOpen {
			select {
			case events <- types.StreamEvent{
				Type:  "content_block_stop",
				Index: state.ContentBlockIndex,
			}:
			case <-ctx.Done():
			}
		}
	}()

	return events
}

// parseResponsesStreamData parses a Responses API streaming event.
func parseResponsesStreamData(data string, state *StreamState) []types.StreamEvent {
	var events []types.StreamEvent

	// Try to parse as ResponsesStreamEvent
	var streamEvent ResponsesStreamEvent
	if err := json.Unmarshal([]byte(data), &streamEvent); err != nil {
		// If that fails, try as a generic map to detect the event type
		var genericEvent map[string]interface{}
		if err := json.Unmarshal([]byte(data), &genericEvent); err != nil {
			return events
		}
		// Handle based on detected type
		if eventType, ok := genericEvent["type"].(string); ok {
			return handleResponsesEventByType(eventType, genericEvent, state)
		}
		return events
	}

	return handleResponsesStreamEvent(&streamEvent, state)
}

// handleResponsesStreamEvent processes a typed Responses API stream event.
func handleResponsesStreamEvent(event *ResponsesStreamEvent, state *StreamState) []types.StreamEvent {
	var events []types.StreamEvent

	switch event.Type {
	case "response.created":
		// Send message_start
		if !state.MessageStartSent {
			events = append(events, types.StreamEvent{
				Type: "message_start",
				Message: &types.AnthropicResponse{
					ID:      state.MessageID,
					Type:    "message",
					Role:    "assistant",
					Model:   state.Model,
					Content: []types.ContentBlock{},
				},
			})
			state.MessageStartSent = true
		}

	case "response.output_text.delta":
		// Handle text delta
		if event.Delta != "" {
			// Ensure content block is open
			if !state.ContentBlockOpen {
				events = append(events, types.StreamEvent{
					Type:  "content_block_start",
					Index: state.ContentBlockIndex,
					ContentBlock: &types.ContentBlock{
						Type: "text",
						Text: "",
					},
				})
				state.ContentBlockOpen = true
			}

			events = append(events, types.StreamEvent{
				Type:  "content_block_delta",
				Index: state.ContentBlockIndex,
				Delta: &types.Delta{
					Type: "text_delta",
					Text: event.Delta,
				},
			})
		}

	case "response.output_text.done", "response.done", "response.completed":
		// Close content block if open
		if state.ContentBlockOpen {
			events = append(events, types.StreamEvent{
				Type:  "content_block_stop",
				Index: state.ContentBlockIndex,
			})
			state.ContentBlockOpen = false
		}

		// Send message_delta with stop_reason
		events = append(events, types.StreamEvent{
			Type: "message_delta",
			Delta: &types.Delta{
				StopReason: "end_turn",
			},
		})

		// Send message_stop
		events = append(events, types.StreamEvent{
			Type: "message_stop",
		})
	}

	return events
}

// handleResponsesEventByType handles events from generic map parsing.
func handleResponsesEventByType(eventType string, event map[string]interface{}, state *StreamState) []types.StreamEvent {
	var events []types.StreamEvent

	switch eventType {
	case "response.created":
		if !state.MessageStartSent {
			events = append(events, types.StreamEvent{
				Type: "message_start",
				Message: &types.AnthropicResponse{
					ID:      state.MessageID,
					Type:    "message",
					Role:    "assistant",
					Model:   state.Model,
					Content: []types.ContentBlock{},
				},
			})
			state.MessageStartSent = true
		}

	case "response.output_text.delta":
		delta := ""
		if d, ok := event["delta"].(string); ok {
			delta = d
		}
		if delta != "" {
			if !state.ContentBlockOpen {
				events = append(events, types.StreamEvent{
					Type:  "content_block_start",
					Index: state.ContentBlockIndex,
					ContentBlock: &types.ContentBlock{
						Type: "text",
						Text: "",
					},
				})
				state.ContentBlockOpen = true
			}

			events = append(events, types.StreamEvent{
				Type:  "content_block_delta",
				Index: state.ContentBlockIndex,
				Delta: &types.Delta{
					Type: "text_delta",
					Text: delta,
				},
			})
		}

	case "response.output_text.done", "response.done", "response.completed":
		if state.ContentBlockOpen {
			events = append(events, types.StreamEvent{
				Type:  "content_block_stop",
				Index: state.ContentBlockIndex,
			})
			state.ContentBlockOpen = false
		}

		events = append(events, types.StreamEvent{
			Type: "message_delta",
			Delta: &types.Delta{
				StopReason: "end_turn",
			},
		})

		events = append(events, types.StreamEvent{
			Type: "message_stop",
		})
	}

	return events
}

// translateChunkToAnthropicEvents converts an OpenAI chunk to Anthropic stream events.
func translateChunkToAnthropicEvents(chunk *ChatCompletionChunk, state *StreamState) []types.StreamEvent {
	var events []types.StreamEvent

	if len(chunk.Choices) == 0 {
		return events
	}

	choice := chunk.Choices[0]
	delta := choice.Delta

	// Send message_start if not sent yet
	if !state.MessageStartSent {
		events = append(events, createMessageStartEvent(chunk, state))
		state.MessageStartSent = true
	}

	// Handle text content
	if delta.Content != "" {
		events = append(events, handleTextDelta(delta.Content, state)...)
	}

	// Handle tool calls
	for _, toolCall := range delta.ToolCalls {
		events = append(events, handleToolCall(toolCall, state)...)
	}

	// Handle finish reason
	if choice.FinishReason != nil {
		events = append(events, handleFinishReason(chunk, *choice.FinishReason, state)...)
	}

	return events
}

// createMessageStartEvent creates the message_start event.
func createMessageStartEvent(chunk *ChatCompletionChunk, state *StreamState) types.StreamEvent {
	inputTokens := 0
	cacheReadTokens := 0
	if chunk.Usage != nil {
		inputTokens = chunk.Usage.PromptTokens
		if chunk.Usage.PromptTokensDetails != nil {
			cacheReadTokens = chunk.Usage.PromptTokensDetails.CachedTokens
			inputTokens -= cacheReadTokens
		}
	}

	return types.StreamEvent{
		Type: "message_start",
		Message: &types.AnthropicResponse{
			ID:      state.MessageID,
			Type:    "message",
			Role:    "assistant",
			Content: []types.ContentBlock{},
			Model:   state.Model,
			Usage: types.Usage{
				InputTokens:         inputTokens,
				OutputTokens:        0,
				CacheReadInputTokens: cacheReadTokens,
			},
		},
	}
}

// handleTextDelta processes text content delta and returns events.
func handleTextDelta(content string, state *StreamState) []types.StreamEvent {
	var events []types.StreamEvent

	// Close tool block if open
	if isToolBlockOpen(state) {
		events = append(events, types.StreamEvent{
			Type:  "content_block_stop",
			Index: state.ContentBlockIndex,
		})
		state.ContentBlockIndex++
		state.ContentBlockOpen = false
	}

	// Start text block if not open
	if !state.ContentBlockOpen {
		events = append(events, types.StreamEvent{
			Type:  "content_block_start",
			Index: state.ContentBlockIndex,
			ContentBlock: &types.ContentBlock{
				Type: "text",
				Text: "",
			},
		})
		state.ContentBlockOpen = true
	}

	// Send text delta
	events = append(events, types.StreamEvent{
		Type:  "content_block_delta",
		Index: state.ContentBlockIndex,
		Delta: &types.Delta{
			Type: "text_delta",
			Text: content,
		},
	})

	return events
}

// handleToolCall processes a tool call delta and returns events.
func handleToolCall(toolCall ToolCallDelta, state *StreamState) []types.StreamEvent {
	var events []types.StreamEvent

	// New tool call starting
	if toolCall.ID != "" && toolCall.Function != nil && toolCall.Function.Name != "" {
		// Close any previously open block
		if state.ContentBlockOpen {
			events = append(events, types.StreamEvent{
				Type:  "content_block_stop",
				Index: state.ContentBlockIndex,
			})
			state.ContentBlockIndex++
			state.ContentBlockOpen = false
		}

		// Track this tool call
		anthropicBlockIndex := state.ContentBlockIndex
		state.ToolCalls[toolCall.Index] = &ToolCallState{
			ID:                  toolCall.ID,
			Name:                toolCall.Function.Name,
			AnthropicBlockIndex: anthropicBlockIndex,
		}

		// Start tool_use block
		events = append(events, types.StreamEvent{
			Type:  "content_block_start",
			Index: anthropicBlockIndex,
			ContentBlock: &types.ContentBlock{
				Type:  "tool_use",
				ID:    toolCall.ID,
				Name:  toolCall.Function.Name,
				Input: map[string]interface{}{},
			},
		})
		state.ContentBlockOpen = true
	}

	// Tool call arguments delta
	if toolCall.Function != nil && toolCall.Function.Arguments != "" {
		toolCallInfo := state.ToolCalls[toolCall.Index]
		if toolCallInfo != nil {
			events = append(events, types.StreamEvent{
				Type:  "content_block_delta",
				Index: toolCallInfo.AnthropicBlockIndex,
				Delta: &types.Delta{
					Type:        "input_json_delta",
					PartialJSON: toolCall.Function.Arguments,
				},
			})
		}
	}

	return events
}

// handleFinishReason processes the finish reason and returns events.
func handleFinishReason(chunk *ChatCompletionChunk, finishReason string, state *StreamState) []types.StreamEvent {
	var events []types.StreamEvent

	// Close any open content block
	if state.ContentBlockOpen {
		events = append(events, types.StreamEvent{
			Type:  "content_block_stop",
			Index: state.ContentBlockIndex,
		})
		state.ContentBlockOpen = false
	}

	// Calculate usage tokens
	outputTokens := 0
	inputTokens := 0
	cacheReadTokens := 0
	if chunk.Usage != nil {
		outputTokens = chunk.Usage.CompletionTokens
		inputTokens = chunk.Usage.PromptTokens
		if chunk.Usage.PromptTokensDetails != nil {
			cacheReadTokens = chunk.Usage.PromptTokensDetails.CachedTokens
			inputTokens -= cacheReadTokens
		}
	}

	// Send message_delta with stop reason
	events = append(events, types.StreamEvent{
		Type: "message_delta",
		Delta: &types.Delta{
			StopReason: translateStopReason(finishReason),
		},
		Usage: &types.Usage{
			InputTokens:         inputTokens,
			OutputTokens:        outputTokens,
			CacheReadInputTokens: cacheReadTokens,
		},
	})

	// Send message_stop
	events = append(events, types.StreamEvent{
		Type: "message_stop",
	})

	return events
}

// isToolBlockOpen checks if the current open block is a tool block.
func isToolBlockOpen(state *StreamState) bool {
	if !state.ContentBlockOpen {
		return false
	}
	for _, tc := range state.ToolCalls {
		if tc.AnthropicBlockIndex == state.ContentBlockIndex {
			return true
		}
	}
	return false
}

// CreateErrorEvent creates an Anthropic error stream event.
func CreateErrorEvent(errType, message string) types.StreamEvent {
	return types.StreamEvent{
		Type: "error",
		Error: &types.ErrorDetail{
			Type:    errType,
			Message: message,
		},
	}
}
