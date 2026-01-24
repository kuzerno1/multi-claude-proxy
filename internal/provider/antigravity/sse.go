package antigravity

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"

	"github.com/kuzerno1/multi-claude-proxy/internal/config"
	"github.com/kuzerno1/multi-claude-proxy/internal/utils"
	"github.com/kuzerno1/multi-claude-proxy/pkg/types"
)

// SSEEvent represents a parsed SSE event.
type SSEEvent struct {
	Type string
	Data map[string]interface{}
}

// SSEParser parses SSE streams from the Cloud Code API.
type SSEParser struct {
	reader *bufio.Reader
}

// NewSSEParser creates a new SSE parser.
func NewSSEParser(r io.Reader) *SSEParser {
	return &SSEParser{reader: bufio.NewReader(r)}
}

// ParseThinkingResponse parses an SSE response for thinking models.
// Accumulates all parts and returns a single Anthropic response.
func ParseThinkingResponse(reader io.ReadCloser, originalModel string) (*types.AnthropicResponse, error) {
	defer reader.Close()

	var accumulatedThinkingText string
	var accumulatedThinkingSignature string
	var accumulatedText string
	var finalParts []map[string]interface{}
	var usageMetadata map[string]interface{}
	finishReason := "STOP"

	flushThinking := func() {
		if accumulatedThinkingText != "" {
			finalParts = append(finalParts, map[string]interface{}{
				"thought":          true,
				"text":             accumulatedThinkingText,
				"thoughtSignature": accumulatedThinkingSignature,
			})
			accumulatedThinkingText = ""
			accumulatedThinkingSignature = ""
		}
	}

	flushText := func() {
		if accumulatedText != "" {
			finalParts = append(finalParts, map[string]interface{}{"text": accumulatedText})
			accumulatedText = ""
		}
	}

	scanner := bufio.NewScanner(reader)
	// Increase buffer size to 1MB for large AI responses
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data:") {
			continue
		}

		jsonText := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if jsonText == "" {
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonText), &data); err != nil {
			utils.Debug("[CloudCode] SSE parse warning: %v Raw: %s", err, truncate(jsonText, 100))
			continue
		}

		innerResponse := data
		if resp, ok := data["response"].(map[string]interface{}); ok {
			innerResponse = resp
		}

		if um, ok := innerResponse["usageMetadata"].(map[string]interface{}); ok {
			usageMetadata = um
		}

		candidates, _ := innerResponse["candidates"].([]interface{})
		if len(candidates) == 0 {
			continue
		}

		firstCandidate, _ := candidates[0].(map[string]interface{})
		if firstCandidate == nil {
			continue
		}

		if fr, ok := firstCandidate["finishReason"].(string); ok {
			finishReason = fr
		}

		content, _ := firstCandidate["content"].(map[string]interface{})
		parts, _ := content["parts"].([]interface{})

		for _, p := range parts {
			part, ok := p.(map[string]interface{})
			if !ok {
				continue
			}

			if thought, ok := part["thought"].(bool); ok && thought {
				flushText()
				if text, ok := part["text"].(string); ok {
					accumulatedThinkingText += text
				}
				if sig, ok := part["thoughtSignature"].(string); ok {
					accumulatedThinkingSignature = sig
				}
			} else if _, ok := part["functionCall"]; ok {
				flushThinking()
				flushText()
				finalParts = append(finalParts, part)
			} else if text, ok := part["text"].(string); ok {
				if text == "" {
					continue
				}
				flushThinking()
				accumulatedText += text
			}
		}
	}

	flushThinking()
	flushText()

	// Build accumulated response
	accumulatedResponse := map[string]interface{}{
		"candidates": []interface{}{
			map[string]interface{}{
				"content":      map[string]interface{}{"parts": toInterfaceSlice(finalParts)},
				"finishReason": finishReason,
			},
		},
		"usageMetadata": usageMetadata,
	}

	// Log part types
	partTypes := make([]string, len(finalParts))
	for i, p := range finalParts {
		if _, ok := p["thought"]; ok {
			partTypes[i] = "thought"
		} else if _, ok := p["functionCall"]; ok {
			partTypes[i] = "functionCall"
		} else {
			partTypes[i] = "text"
		}
	}
	utils.Debug("[CloudCode] Response received (SSE), part types: %v", partTypes)

	// Log thinking signature length
	for _, p := range finalParts {
		if _, ok := p["thought"]; ok {
			if sig, ok := p["thoughtSignature"].(string); ok {
				utils.Debug("[CloudCode] Thinking signature length: %d", len(sig))
			}
			break
		}
	}

	return ConvertGoogleToAnthropic(accumulatedResponse, originalModel), nil
}

// StreamEvent represents an event to send in SSE streaming.
type StreamEvent struct {
	Type string
	Data interface{}
}

// EmptyResponseError is returned when the SSE stream contains no content parts.
// This is used to trigger retry logic in the streaming handler (Node parity).
type EmptyResponseError struct {
	Message string
}

func (e *EmptyResponseError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return "No content received from API"
}

// StreamingParser handles streaming SSE responses (Node parity).
type StreamingParser struct {
	reader        io.ReadCloser
	originalModel string
	messageID     string

	hasEmittedStart          bool
	blockIndex               int
	currentBlockType         string // "", "thinking", "text", "tool_use"
	currentThinkingSignature string
	stopReason               string

	inputTokens     int
	outputTokens    int
	cacheReadTokens int

	sigCache *SignatureCache
}

// NewStreamingParser creates a new streaming parser.
func NewStreamingParser(reader io.ReadCloser, originalModel string) *StreamingParser {
	return &StreamingParser{
		reader:        reader,
		originalModel: originalModel,
		messageID:     generateMessageID(),
		stopReason:    "end_turn",
		sigCache:      GetGlobalSignatureCache(),
	}
}

// StreamEvents yields streaming events to be sent to the client.
// Returns a channel of StreamEvent and a channel for the final error (nil on success).
func (p *StreamingParser) StreamEvents() (<-chan StreamEvent, <-chan error) {
	eventsCh := make(chan StreamEvent, 100)
	errCh := make(chan error, 1)

	go func() {
		defer close(eventsCh)
		defer close(errCh)
		defer p.reader.Close()

		scanner := bufio.NewScanner(p.reader)
		// Increase buffer size to 1MB for large AI responses
		buf := make([]byte, 64*1024)
		scanner.Buffer(buf, 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()

			if !strings.HasPrefix(line, "data:") {
				continue
			}

			jsonText := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if jsonText == "" {
				continue
			}

			var data map[string]interface{}
			if err := json.Unmarshal([]byte(jsonText), &data); err != nil {
				continue
			}

			innerResponse := data
			if resp, ok := data["response"].(map[string]interface{}); ok {
				innerResponse = resp
			}

			// Extract usage metadata (including cache tokens).
			if usage, ok := innerResponse["usageMetadata"].(map[string]interface{}); ok {
				if prompt := getInt(usage, "promptTokenCount"); prompt != 0 {
					p.inputTokens = prompt
				}
				if out := getInt(usage, "candidatesTokenCount"); out != 0 {
					p.outputTokens = out
				}
				if cached := getInt(usage, "cachedContentTokenCount"); cached != 0 {
					p.cacheReadTokens = cached
				}
			}

			candidates, _ := innerResponse["candidates"].([]interface{})
			if len(candidates) == 0 {
				continue
			}

			firstCandidate, _ := candidates[0].(map[string]interface{})
			if firstCandidate == nil {
				continue
			}

			content, _ := firstCandidate["content"].(map[string]interface{})
			parts, _ := content["parts"].([]interface{})

			// Emit message_start on first data that includes parts.
			if !p.hasEmittedStart && len(parts) > 0 {
				p.hasEmittedStart = true
				eventsCh <- StreamEvent{
					Type: "message_start",
					Data: map[string]interface{}{
						"type": "message_start",
						"message": map[string]interface{}{
							"id":            p.messageID,
							"type":          "message",
							"role":          "assistant",
							"content":       []interface{}{},
							"model":         p.originalModel,
							"stop_reason":   nil,
							"stop_sequence": nil,
							"usage": map[string]interface{}{
								"input_tokens":                p.inputTokens - p.cacheReadTokens,
								"output_tokens":               0,
								"cache_read_input_tokens":     p.cacheReadTokens,
								"cache_creation_input_tokens": 0,
							},
						},
					},
				}
			}

			// Process each part.
			for _, part := range parts {
				partMap, ok := part.(map[string]interface{})
				if !ok {
					continue
				}

				for _, evt := range p.processPart(partMap) {
					eventsCh <- evt
				}
			}

			// Check finish reason.
			// Priority: max_tokens > tool_use > end_turn
			// MAX_TOKENS always takes precedence (even over tool_use).
			// STOP should not overwrite tool_use.
			if fr, ok := firstCandidate["finishReason"].(string); ok && fr != "" {
				switch fr {
				case "MAX_TOKENS":
					p.stopReason = "max_tokens"
				case "STOP":
					if p.stopReason != "tool_use" {
						p.stopReason = "end_turn"
					}
				}
			}
		}

		if err := scanner.Err(); err != nil {
			errCh <- err
			return
		}

		// Handle empty response (Node parity: throw to trigger retry in streaming handler).
		if !p.hasEmittedStart {
			errCh <- &EmptyResponseError{Message: "No content parts received from API"}
			return
		}

		// Close any open block.
		if p.currentBlockType != "" {
			if p.currentBlockType == "thinking" && p.currentThinkingSignature != "" {
				eventsCh <- p.signatureDeltaEvent(p.currentThinkingSignature)
				p.currentThinkingSignature = ""
			}

			eventsCh <- StreamEvent{
				Type: "content_block_stop",
				Data: map[string]interface{}{
					"type":  "content_block_stop",
					"index": p.blockIndex,
				},
			}
		}

		// Emit message_delta and message_stop.
		eventsCh <- StreamEvent{
			Type: "message_delta",
			Data: map[string]interface{}{
				"type": "message_delta",
				"delta": map[string]interface{}{
					"stop_reason":   p.stopReason,
					"stop_sequence": nil,
				},
				"usage": map[string]interface{}{
					"output_tokens":               p.outputTokens,
					"cache_read_input_tokens":     p.cacheReadTokens,
					"cache_creation_input_tokens": 0,
				},
			},
		}

		eventsCh <- StreamEvent{
			Type: "message_stop",
			Data: map[string]interface{}{
				"type": "message_stop",
			},
		}

		errCh <- nil
	}()

	return eventsCh, errCh
}

func (p *StreamingParser) processPart(part map[string]interface{}) []StreamEvent {
	events := make([]StreamEvent, 0, 2)

	// Thinking block
	if thought, ok := part["thought"].(bool); ok && thought {
		text := ""
		if t, ok := part["text"].(string); ok {
			text = t
		}

		signature := ""
		if sig, ok := part["thoughtSignature"].(string); ok {
			signature = sig
		}

		if p.currentBlockType != "thinking" {
			if p.currentBlockType != "" {
				events = append(events, StreamEvent{
					Type: "content_block_stop",
					Data: map[string]interface{}{
						"type":  "content_block_stop",
						"index": p.blockIndex,
					},
				})
				p.blockIndex++
			}

			p.currentBlockType = "thinking"
			p.currentThinkingSignature = ""
			events = append(events, StreamEvent{
				Type: "content_block_start",
				Data: map[string]interface{}{
					"type":  "content_block_start",
					"index": p.blockIndex,
					"content_block": map[string]interface{}{
						"type":     "thinking",
						"thinking": "",
					},
				},
			})
		}

		if signature != "" && len(signature) >= config.MinSignatureLength {
			p.currentThinkingSignature = signature
			modelFamily := config.GetModelFamily(p.originalModel)
			p.sigCache.CacheThinkingSignature(signature, string(modelFamily))
		}

		events = append(events, StreamEvent{
			Type: "content_block_delta",
			Data: map[string]interface{}{
				"type":  "content_block_delta",
				"index": p.blockIndex,
				"delta": map[string]interface{}{
					"type":     "thinking_delta",
					"thinking": text,
				},
			},
		})
		return events
	}

	// Text block: match Node's "part.text !== undefined" behavior.
	if _, ok := part["text"]; ok {
		text, _ := part["text"].(string)
		if strings.TrimSpace(text) == "" {
			return events
		}

		if p.currentBlockType != "text" {
			if p.currentBlockType == "thinking" && p.currentThinkingSignature != "" {
				events = append(events, p.signatureDeltaEvent(p.currentThinkingSignature))
				p.currentThinkingSignature = ""
			}
			if p.currentBlockType != "" {
				events = append(events, StreamEvent{
					Type: "content_block_stop",
					Data: map[string]interface{}{
						"type":  "content_block_stop",
						"index": p.blockIndex,
					},
				})
				p.blockIndex++
			}

			p.currentBlockType = "text"
			events = append(events, StreamEvent{
				Type: "content_block_start",
				Data: map[string]interface{}{
					"type":  "content_block_start",
					"index": p.blockIndex,
					"content_block": map[string]interface{}{
						"type": "text",
						"text": "",
					},
				},
			})
		}

		events = append(events, StreamEvent{
			Type: "content_block_delta",
			Data: map[string]interface{}{
				"type":  "content_block_delta",
				"index": p.blockIndex,
				"delta": map[string]interface{}{
					"type": "text_delta",
					"text": text,
				},
			},
		})
		return events
	}

	// Tool use (functionCall)
	if fc, ok := part["functionCall"].(map[string]interface{}); ok {
		functionCallSignature := ""
		if sig, ok := part["thoughtSignature"].(string); ok {
			functionCallSignature = sig
		}

		if p.currentBlockType == "thinking" && p.currentThinkingSignature != "" {
			events = append(events, p.signatureDeltaEvent(p.currentThinkingSignature))
			p.currentThinkingSignature = ""
		}
		if p.currentBlockType != "" {
			events = append(events, StreamEvent{
				Type: "content_block_stop",
				Data: map[string]interface{}{
					"type":  "content_block_stop",
					"index": p.blockIndex,
				},
			})
			p.blockIndex++
		}

		p.currentBlockType = "tool_use"
		p.stopReason = "tool_use"

		toolID := ""
		if id, ok := fc["id"].(string); ok && id != "" {
			toolID = id
		} else {
			toolID = generateToolID()
		}

		name, _ := fc["name"].(string)
		args, _ := fc["args"].(map[string]interface{})
		argsJSON := "{}"
		if args != nil {
			if b, err := json.Marshal(args); err == nil {
				argsJSON = string(b)
			}
		}

		toolUseBlock := map[string]interface{}{
			"type":  "tool_use",
			"id":    toolID,
			"name":  name,
			"input": map[string]interface{}{},
		}

		if functionCallSignature != "" && len(functionCallSignature) >= config.MinSignatureLength {
			toolUseBlock["thoughtSignature"] = functionCallSignature
			p.sigCache.CacheToolSignature(toolID, functionCallSignature)
		}

		events = append(events, StreamEvent{
			Type: "content_block_start",
			Data: map[string]interface{}{
				"type":          "content_block_start",
				"index":         p.blockIndex,
				"content_block": toolUseBlock,
			},
		})

		events = append(events, StreamEvent{
			Type: "content_block_delta",
			Data: map[string]interface{}{
				"type":  "content_block_delta",
				"index": p.blockIndex,
				"delta": map[string]interface{}{
					"type":         "input_json_delta",
					"partial_json": argsJSON,
				},
			},
		})
		return events
	}

	return events
}

func (p *StreamingParser) signatureDeltaEvent(signature string) StreamEvent {
	return StreamEvent{
		Type: "content_block_delta",
		Data: map[string]interface{}{
			"type":  "content_block_delta",
			"index": p.blockIndex,
			"delta": map[string]interface{}{
				"type":      "signature_delta",
				"signature": signature,
			},
		},
	}
}

func emitEmptyResponseFallback(model string) []StreamEvent {
	messageID := generateMessageID()
	return []StreamEvent{
		{
			Type: "message_start",
			Data: map[string]interface{}{
				"type": "message_start",
				"message": map[string]interface{}{
					"id":            messageID,
					"type":          "message",
					"role":          "assistant",
					"content":       []interface{}{},
					"model":         model,
					"stop_reason":   nil,
					"stop_sequence": nil,
					"usage": map[string]interface{}{
						"input_tokens":  0,
						"output_tokens": 0,
					},
				},
			},
		},
		{
			Type: "content_block_start",
			Data: map[string]interface{}{
				"type":  "content_block_start",
				"index": 0,
				"content_block": map[string]interface{}{
					"type": "text",
					"text": "",
				},
			},
		},
		{
			Type: "content_block_delta",
			Data: map[string]interface{}{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]interface{}{
					"type": "text_delta",
					"text": "[No response after retries - please try again]",
				},
			},
		},
		{
			Type: "content_block_stop",
			Data: map[string]interface{}{
				"type":  "content_block_stop",
				"index": 0,
			},
		},
		{
			Type: "message_delta",
			Data: map[string]interface{}{
				"type": "message_delta",
				"delta": map[string]interface{}{
					"stop_reason":   "end_turn",
					"stop_sequence": nil,
				},
				"usage": map[string]interface{}{
					"output_tokens": 0,
				},
			},
		},
		{
			Type: "message_stop",
			Data: map[string]interface{}{
				"type": "message_stop",
			},
		},
	}
}

func toInterfaceSlice(maps []map[string]interface{}) []interface{} {
	result := make([]interface{}, len(maps))
	for i, m := range maps {
		result[i] = m
	}
	return result
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
