// Package antigravity implements the Antigravity Cloud Code provider.
package antigravity

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kuzerno1/multi-claude-proxy/internal/config"
	"github.com/kuzerno1/multi-claude-proxy/internal/utils"
	"github.com/kuzerno1/multi-claude-proxy/pkg/types"
)

// isThinkingBlock checks if a content block is a thinking block.
func isThinkingBlock(block *types.ContentBlock) bool {
	return block.Type == "thinking" || block.Type == "redacted_thinking"
}

// hasValidSignature checks if a thinking block has a valid signature.
func hasValidSignature(block *types.ContentBlock) bool {
	return len(block.Signature) >= config.MinSignatureLength
}

// hasGeminiHistory checks if conversation history contains Gemini-style messages.
// Gemini puts thoughtSignature on tool_use blocks, Claude puts signature on thinking blocks.
func hasGeminiHistory(messages []types.Message) bool {
	for _, msg := range messages {
		if len(msg.Content) == 0 {
			continue
		}

		var blocks []types.ContentBlock
		if err := json.Unmarshal(msg.Content, &blocks); err != nil {
			continue
		}

		for _, block := range blocks {
			if block.Type == "tool_use" && block.ThoughtSignature != "" {
				return true
			}
		}
	}
	return false
}

// ConversationState represents the state of a conversation for thinking recovery.
type ConversationState struct {
	InToolLoop       bool
	InterruptedTool  bool
	TurnHasThinking  bool
	ToolResultCount  int
	LastAssistantIdx int
}

// analyzeConversationState analyzes the conversation to detect corrupted states.
func analyzeConversationState(messages []types.Message) ConversationState {
	state := ConversationState{LastAssistantIdx: -1}

	if len(messages) == 0 {
		return state
	}

	// Find the last assistant message
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			state.LastAssistantIdx = i
			break
		}
	}

	if state.LastAssistantIdx == -1 {
		return state
	}

	lastAssistant := messages[state.LastAssistantIdx]

	// Parse content to check for tool_use and thinking
	var blocks []types.ContentBlock
	if err := json.Unmarshal(lastAssistant.Content, &blocks); err == nil {
		for _, block := range blocks {
			if block.Type == "tool_use" {
				state.InToolLoop = true // Will be refined below
			}
			if isThinkingBlock(&block) && hasValidSignature(&block) {
				state.TurnHasThinking = true
			}
		}
	}

	// Count trailing tool results after the assistant message
	// Node parity: count MESSAGES that have tool_result (not number of blocks)
	hasPlainUserMessageAfter := false
	for i := state.LastAssistantIdx + 1; i < len(messages); i++ {
		msg := messages[i]
		if msg.Role != "user" {
			continue
		}

		var blocks []types.ContentBlock
		if err := json.Unmarshal(msg.Content, &blocks); err != nil {
			// Could be a string message
			hasPlainUserMessageAfter = true
			continue
		}

		hasToolResult := false
		for _, block := range blocks {
			if block.Type == "tool_result" {
				hasToolResult = true
				break // Only count once per message (Node parity)
			}
		}

		if hasToolResult {
			state.ToolResultCount++
		} else {
			hasPlainUserMessageAfter = true
		}
	}

	// Refine state
	hasToolUse := state.InToolLoop
	state.InToolLoop = hasToolUse && state.ToolResultCount > 0
	state.InterruptedTool = hasToolUse && state.ToolResultCount == 0 && hasPlainUserMessageAfter

	return state
}

// needsThinkingRecovery checks if conversation needs thinking recovery.
// Recovery is needed when in a tool loop or interrupted tool and no valid thinking exists.
func needsThinkingRecovery(messages []types.Message) bool {
	state := analyzeConversationState(messages)

	if !state.InToolLoop && !state.InterruptedTool {
		return false
	}

	return !state.TurnHasThinking
}

// removeTrailingThinkingBlocks removes trailing unsigned thinking blocks from content.
func removeTrailingThinkingBlocks(blocks []types.ContentBlock) []types.ContentBlock {
	if len(blocks) == 0 {
		return blocks
	}

	endIndex := len(blocks)
	for i := len(blocks) - 1; i >= 0; i-- {
		block := blocks[i]
		if !isThinkingBlock(&block) {
			break // Stop at first non-thinking block
		}

		if !hasValidSignature(&block) {
			endIndex = i
		} else {
			break // Stop at signed thinking block
		}
	}

	if endIndex < len(blocks) {
		utils.Debug("[ThinkingUtils] Removed %d trailing unsigned thinking blocks", len(blocks)-endIndex)
		return blocks[:endIndex]
	}

	return blocks
}

// restoreThinkingSignatures filters thinking blocks, keeping only those with valid signatures.
// Blocks without signatures are dropped (API requires signatures).
// redacted_thinking blocks are kept as-is (they have data instead of signature).
func restoreThinkingSignatures(blocks []types.ContentBlock) []types.ContentBlock {
	originalLen := len(blocks)
	filtered := make([]types.ContentBlock, 0, len(blocks))

	for _, block := range blocks {
		if block.Type == "redacted_thinking" {
			// Keep redacted_thinking blocks (Node parity) - they have data instead of signature
			filtered = append(filtered, types.ContentBlock{
				Type: "redacted_thinking",
				Data: block.Data,
			})
			continue
		}

		if block.Type != "thinking" {
			filtered = append(filtered, block)
			continue
		}

		// Keep blocks with valid signatures
		if hasValidSignature(&block) {
			// Sanitize to remove extra fields like cache_control
			filtered = append(filtered, types.ContentBlock{
				Type:      "thinking",
				Thinking:  block.Thinking,
				Signature: block.Signature,
			})
		}
		// Unsigned thinking blocks are dropped
	}

	if len(filtered) < originalLen {
		utils.Debug("[ThinkingUtils] Dropped %d unsigned thinking block(s)", originalLen-len(filtered))
	}

	return filtered
}

// reorderAssistantContent reorders content so that:
// 1. Thinking blocks come first
// 2. Text blocks come in the middle
// 3. Tool_use blocks come at the end
func reorderAssistantContent(blocks []types.ContentBlock) []types.ContentBlock {
	if len(blocks) <= 1 {
		// Even for single element, sanitize if thinking
		if len(blocks) == 1 && isThinkingBlock(&blocks[0]) {
			return []types.ContentBlock{{
				Type:      blocks[0].Type,
				Thinking:  blocks[0].Thinking,
				Signature: blocks[0].Signature,
				Data:      blocks[0].Data, // Preserve redacted_thinking.data (Node parity)
			}}
		}
		return blocks
	}

	var thinking, text, toolUse []types.ContentBlock
	droppedEmpty := 0

	for _, block := range blocks {
		switch block.Type {
		case "thinking", "redacted_thinking":
			thinking = append(thinking, types.ContentBlock{
				Type:      block.Type,
				Thinking:  block.Thinking,
				Signature: block.Signature,
				Data:      block.Data, // Preserve redacted_thinking.data (Node parity)
			})
		case "tool_use":
			toolUse = append(toolUse, block)
		case "text":
			// Node parity: skip blocks with only whitespace content
			if strings.TrimSpace(block.Text) != "" {
				text = append(text, block)
			} else {
				droppedEmpty++
			}
		default:
			text = append(text, block)
		}
	}

	if droppedEmpty > 0 {
		utils.Debug("[ThinkingUtils] Dropped %d empty text block(s)", droppedEmpty)
	}

	result := make([]types.ContentBlock, 0, len(thinking)+len(text)+len(toolUse))
	result = append(result, thinking...)
	result = append(result, text...)
	result = append(result, toolUse...)

	return result
}

// stripInvalidThinkingBlocks removes invalid or incompatible thinking blocks.
func stripInvalidThinkingBlocks(messages []types.Message, targetFamily string) []types.Message {
	sigCache := GetGlobalSignatureCache()
	strippedCount := 0

	result := make([]types.Message, len(messages))
	for i, msg := range messages {
		var blocks []types.ContentBlock
		if err := json.Unmarshal(msg.Content, &blocks); err != nil {
			result[i] = msg
			continue
		}

		filtered := make([]types.ContentBlock, 0, len(blocks))
		for _, block := range blocks {
			if !isThinkingBlock(&block) {
				filtered = append(filtered, block)
				continue
			}

			if !hasValidSignature(&block) {
				strippedCount++
				continue
			}

			// Check family compatibility only for Gemini targets
			if targetFamily == "gemini" {
				signatureFamily := sigCache.GetSignatureFamily(block.Signature)
				if signatureFamily == "" || signatureFamily != targetFamily {
					strippedCount++
					continue
				}
			}

			filtered = append(filtered, block)
		}

		// Ensure at least one content block
		if len(filtered) == 0 {
			filtered = []types.ContentBlock{{Type: "text", Text: "."}}
		}

		newContent, _ := json.Marshal(filtered)
		result[i] = types.Message{
			Role:    msg.Role,
			Content: newContent,
		}
	}

	if strippedCount > 0 {
		utils.Debug("[ThinkingUtils] Stripped %d invalid/incompatible thinking block(s)", strippedCount)
	}

	return result
}

// closeToolLoopForThinking closes tool loop by injecting synthetic messages.
// This allows the model to start a fresh turn when thinking is corrupted.
func closeToolLoopForThinking(messages []types.Message, targetFamily string) []types.Message {
	state := analyzeConversationState(messages)

	if !state.InToolLoop && !state.InterruptedTool {
		return messages
	}

	// Strip invalid/incompatible thinking blocks
	modified := stripInvalidThinkingBlocks(messages, targetFamily)

	if state.InterruptedTool {
		// For interrupted tools: add synthetic assistant message before user's new message
		insertIdx := state.LastAssistantIdx + 1
		syntheticContent, _ := json.Marshal([]types.ContentBlock{{
			Type: "text",
			Text: "[Tool call was interrupted.]",
		}})

		syntheticMsg := types.Message{
			Role:    "assistant",
			Content: syntheticContent,
		}

		// Insert at the correct position
		result := make([]types.Message, 0, len(modified)+1)
		result = append(result, modified[:insertIdx]...)
		result = append(result, syntheticMsg)
		result = append(result, modified[insertIdx:]...)
		modified = result

		utils.Debug("[ThinkingUtils] Applied thinking recovery for interrupted tool")
	} else if state.InToolLoop {
		// For tool loops: add synthetic messages to close the loop
		// Node parity: use fmt.Sprintf for multi-digit support
		syntheticText := "[Tool execution completed.]"
		if state.ToolResultCount > 1 {
			syntheticText = fmt.Sprintf("[%d tool executions completed.]", state.ToolResultCount)
		}

		assistantContent, _ := json.Marshal([]types.ContentBlock{{
			Type: "text",
			Text: syntheticText,
		}})
		userContent, _ := json.Marshal([]types.ContentBlock{{
			Type: "text",
			Text: "[Continue]",
		}})

		modified = append(modified, types.Message{
			Role:    "assistant",
			Content: assistantContent,
		})
		modified = append(modified, types.Message{
			Role:    "user",
			Content: userContent,
		})

		utils.Debug("[ThinkingUtils] Applied thinking recovery for tool loop")
	}

	return modified
}

// processAssistantContentForThinking applies thinking processing to assistant message content.
// This includes restoring signatures, removing trailing unsigned blocks, and reordering.
func processAssistantContentForThinking(content json.RawMessage) ([]types.ContentBlock, bool) {
	var blocks []types.ContentBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return nil, false
	}

	// Apply thinking processing
	blocks = restoreThinkingSignatures(blocks)
	blocks = removeTrailingThinkingBlocks(blocks)
	blocks = reorderAssistantContent(blocks)

	return blocks, true
}

// isThinkingPart checks if a part (map) represents a thinking block.
func isThinkingPart(part map[string]interface{}) bool {
	if thought, ok := part["thought"].(bool); ok && thought {
		return true
	}
	if text, ok := part["text"]; ok {
		if _, hasThought := part["thought"]; hasThought {
			_ = text
			return true
		}
	}
	return false
}

// hasValidSignaturePart checks if a part has a valid signature.
func hasValidSignaturePart(part map[string]interface{}) bool {
	if sig, ok := part["thoughtSignature"].(string); ok && len(sig) >= config.MinSignatureLength {
		return true
	}
	return false
}

// sanitizeThinkingPart sanitizes a thinking part to keep only allowed fields.
func sanitizeThinkingPart(part map[string]interface{}) map[string]interface{} {
	sanitized := map[string]interface{}{
		"thought": true,
	}
	if text, ok := part["text"].(string); ok {
		sanitized["text"] = text
	}
	if sig, ok := part["thoughtSignature"].(string); ok {
		sanitized["thoughtSignature"] = sig
	}
	return sanitized
}

// filterPartsArray filters parts, keeping only thinking blocks with valid signatures.
func filterPartsArray(parts []interface{}) []interface{} {
	filtered := make([]interface{}, 0, len(parts))

	for _, p := range parts {
		part, ok := p.(map[string]interface{})
		if !ok {
			filtered = append(filtered, p)
			continue
		}

		if !isThinkingPart(part) {
			filtered = append(filtered, p)
			continue
		}

		// Keep items with valid signatures
		if hasValidSignaturePart(part) {
			filtered = append(filtered, sanitizeThinkingPart(part))
			continue
		}

		// Drop unsigned thinking blocks
		utils.Debug("[ThinkingUtils] Dropping unsigned thinking block")
	}

	return filtered
}

// FilterUnsignedThinkingBlocks filters unsigned thinking blocks from contents (Google/Gemini format).
// This is applied to Claude models after building googleReq["contents"].
func FilterUnsignedThinkingBlocks(contents []interface{}) []interface{} {
	result := make([]interface{}, 0, len(contents))

	for _, c := range contents {
		content, ok := c.(map[string]interface{})
		if !ok {
			result = append(result, c)
			continue
		}

		parts, ok := content["parts"].([]interface{})
		if !ok {
			result = append(result, c)
			continue
		}

		filteredParts := filterPartsArray(parts)

		newContent := make(map[string]interface{})
		for k, v := range content {
			newContent[k] = v
		}
		newContent["parts"] = filteredParts

		result = append(result, newContent)
	}

	return result
}
