package antigravity

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/kuzerno1/multi-claude-proxy/internal/config"
	"github.com/kuzerno1/multi-claude-proxy/internal/utils"
	"github.com/kuzerno1/multi-claude-proxy/pkg/types"
)

// ConvertAnthropicToGoogle converts an Anthropic Messages API request to Google format.
func ConvertAnthropicToGoogle(req *types.AnthropicRequest) map[string]interface{} {
	modelName := req.Model
	modelFamily := config.GetModelFamily(modelName)
	isClaudeModel := modelFamily == "claude"
	isGeminiModel := modelFamily == "gemini"
	isThinking := config.IsThinkingModel(modelName)

	googleReq := map[string]interface{}{
		"contents":         []interface{}{},
		"generationConfig": map[string]interface{}{},
	}

	genConfig := googleReq["generationConfig"].(map[string]interface{})

	// Handle system instruction
	if len(req.System) > 0 {
		systemParts := convertSystemToParts(req.System)
		if len(systemParts) > 0 {
			googleReq["systemInstruction"] = map[string]interface{}{
				"parts": systemParts,
			}
		}
	}

	// Add interleaved thinking hint for Claude thinking models with tools
	if isClaudeModel && isThinking && len(req.Tools) > 0 {
		hint := "Interleaved thinking is enabled. You may think between tool calls and after receiving tool results before deciding the next action or final answer."
		appendSystemHint(googleReq, hint)
	}

	// Apply thinking recovery for thinking models when needed (Node parity)
	processedMessages := req.Messages

	if isThinking && needsThinkingRecovery(req.Messages) {
		targetFamily := "gemini"
		if isClaudeModel {
			// For Claude: apply recovery only for cross-model (Gemini→Claude) switch
			if hasGeminiHistory(req.Messages) {
				utils.Debug("[RequestConverter] Applying thinking recovery for Claude (cross-model from Gemini)")
				processedMessages = closeToolLoopForThinking(req.Messages, "claude")
			}
		} else if isGeminiModel {
			utils.Debug("[RequestConverter] Applying thinking recovery for Gemini")
			processedMessages = closeToolLoopForThinking(req.Messages, targetFamily)
		}
	}

	// Convert messages to contents
	contents := make([]interface{}, 0, len(processedMessages))
	for _, msg := range processedMessages {
		// For assistant messages, apply thinking processing (Node parity)
		// Node.js applies this to ANY assistant message with array content, not gated by isThinking
		var parts []interface{}
		if msg.Role == "assistant" || msg.Role == "model" {
			if blocks, ok := processAssistantContentForThinking(msg.Content); ok {
				// Convert processed blocks to parts
				parts = make([]interface{}, 0, len(blocks))
				for _, block := range blocks {
					part := convertBlockToPart(block, isClaudeModel, isGeminiModel)
					if part != nil {
						parts = append(parts, part)
					}
				}
			} else {
				parts = convertContentToParts(msg.Content, isClaudeModel, isGeminiModel)
			}
		} else {
			parts = convertContentToParts(msg.Content, isClaudeModel, isGeminiModel)
		}

		// Ensure at least one part per message
		if len(parts) == 0 {
			utils.Warn("[RequestConverter] Empty parts array after filtering, adding placeholder")
			parts = []interface{}{map[string]interface{}{"text": "."}}
		}

		content := map[string]interface{}{
			"role":  convertRole(msg.Role),
			"parts": parts,
		}
		contents = append(contents, content)
	}

	// Filter unsigned thinking blocks for Claude models (Node parity)
	if isClaudeModel {
		contents = FilterUnsignedThinkingBlocks(contents)
	}

	googleReq["contents"] = contents

	// Generation config
	if req.MaxTokens > 0 {
		genConfig["maxOutputTokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		genConfig["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		genConfig["topP"] = *req.TopP
	}
	if req.TopK != nil {
		genConfig["topK"] = *req.TopK
	}
	if len(req.StopSequences) > 0 {
		genConfig["stopSequences"] = req.StopSequences
	}

	// Enable thinking for thinking models
	if isThinking {
		if isClaudeModel {
			thinkingConfig := map[string]interface{}{
				"include_thoughts": true,
			}
			if req.Thinking != nil && req.Thinking.BudgetTokens > 0 {
				thinkingConfig["thinking_budget"] = req.Thinking.BudgetTokens
				utils.Debug("[RequestConverter] Claude thinking enabled with budget: %d", req.Thinking.BudgetTokens)

				// Validate max_tokens > thinking_budget
				if maxTokens, ok := genConfig["maxOutputTokens"].(int); ok && maxTokens <= req.Thinking.BudgetTokens {
					adjustedMaxTokens := req.Thinking.BudgetTokens + 8192
					utils.Warn("[RequestConverter] max_tokens (%d) <= thinking_budget (%d). Adjusting to %d",
						maxTokens, req.Thinking.BudgetTokens, adjustedMaxTokens)
					genConfig["maxOutputTokens"] = adjustedMaxTokens
				}
			}
			genConfig["thinkingConfig"] = thinkingConfig
		} else if isGeminiModel {
			budget := 16000
			if req.Thinking != nil && req.Thinking.BudgetTokens > 0 {
				budget = req.Thinking.BudgetTokens
			}
			genConfig["thinkingConfig"] = map[string]interface{}{
				"includeThoughts": true,
				"thinkingBudget":  budget,
			}
			utils.Debug("[RequestConverter] Gemini thinking enabled with budget: %d", budget)
		}
	}

	// Convert tools
	if len(req.Tools) > 0 {
		functionDeclarations := make([]interface{}, 0, len(req.Tools))
		for i, tool := range req.Tools {
			name := tool.Name
			if name == "" {
				name = tool.Function.Name
			}
			if name == "" {
				name = generateToolName(i)
			}

			description := tool.Description
			if description == "" {
				description = tool.Function.Description
			}

			schema := tool.InputSchema
			if schema == nil {
				schema = tool.Function.Parameters
			}
			if schema == nil {
				schema = map[string]interface{}{"type": "object"}
			}

			// Sanitize and clean schema
			sanitized := SanitizeSchema(schema)
			cleaned := CleanSchema(sanitized)

			functionDeclarations = append(functionDeclarations, map[string]interface{}{
				"name":        sanitizeFunctionName(name),
				"description": description,
				"parameters":  cleaned,
			})
		}
		googleReq["tools"] = []interface{}{
			map[string]interface{}{"functionDeclarations": functionDeclarations},
		}
	}

	// Cap max tokens for Gemini models
	if isGeminiModel {
		if maxTokens, ok := genConfig["maxOutputTokens"].(int); ok && maxTokens > config.GeminiMaxOutputTokens {
			utils.Debug("[RequestConverter] Capping Gemini max_tokens from %d to %d", maxTokens, config.GeminiMaxOutputTokens)
			genConfig["maxOutputTokens"] = config.GeminiMaxOutputTokens
		}
	}

	return googleReq
}

// ConvertGoogleToAnthropic converts a Google Generative AI response to Anthropic format.
func ConvertGoogleToAnthropic(googleResp map[string]interface{}, model string) *types.AnthropicResponse {
	response := googleResp
	if inner, ok := googleResp["response"].(map[string]interface{}); ok {
		response = inner
	}

	candidates, _ := response["candidates"].([]interface{})
	var firstCandidate map[string]interface{}
	if len(candidates) > 0 {
		firstCandidate, _ = candidates[0].(map[string]interface{})
	}
	if firstCandidate == nil {
		firstCandidate = make(map[string]interface{})
	}

	content, _ := firstCandidate["content"].(map[string]interface{})
	parts, _ := content["parts"].([]interface{})

	// Convert parts to Anthropic content blocks
	anthropicContent := make([]types.ContentBlock, 0)
	hasToolCalls := false
	sigCache := GetGlobalSignatureCache()

	for _, p := range parts {
		part, ok := p.(map[string]interface{})
		if !ok {
			continue
		}

		if text, ok := part["text"].(string); ok {
			if thought, ok := part["thought"].(bool); ok && thought {
				// Thinking block
				signature, _ := part["thoughtSignature"].(string)

				// Cache thinking signature with model family
				if len(signature) >= config.MinSignatureLength {
					modelFamily := config.GetModelFamily(model)
					sigCache.CacheThinkingSignature(signature, string(modelFamily))
				}

				anthropicContent = append(anthropicContent, types.ContentBlock{
					Type:      "thinking",
					Thinking:  text,
					Signature: signature,
				})
			} else {
				// Regular text block
				anthropicContent = append(anthropicContent, types.ContentBlock{
					Type: "text",
					Text: text,
				})
			}
		} else if fc, ok := part["functionCall"].(map[string]interface{}); ok {
			// Tool use block
			toolID := ""
			if id, ok := fc["id"].(string); ok {
				toolID = id
			} else {
				toolID = generateToolID()
			}

			name, _ := fc["name"].(string)
			args, _ := fc["args"].(map[string]interface{})
			if args == nil {
				args = make(map[string]interface{})
			}

			block := types.ContentBlock{
				Type:  "tool_use",
				ID:    toolID,
				Name:  name,
				Input: args,
			}

			// For Gemini, cache thoughtSignature from the part level
			if sig, ok := part["thoughtSignature"].(string); ok && len(sig) >= config.MinSignatureLength {
				block.ThoughtSignature = sig
				sigCache.CacheToolSignature(toolID, sig)
			}

			anthropicContent = append(anthropicContent, block)
			hasToolCalls = true
		}
	}

	// Determine stop reason
	// Priority: max_tokens > tool_use > end_turn
	// MAX_TOKENS always takes precedence (even over tool_use).
	// STOP should not overwrite tool_use.
	finishReason, _ := firstCandidate["finishReason"].(string)
	stopReason := "end_turn"
	switch finishReason {
	case "MAX_TOKENS":
		stopReason = "max_tokens"
	case "TOOL_USE":
		stopReason = "tool_use"
	case "STOP":
		if hasToolCalls {
			stopReason = "tool_use"
		} else {
			stopReason = "end_turn"
		}
	default:
		if hasToolCalls {
			stopReason = "tool_use"
		}
	}

	// Extract usage metadata
	usageMetadata, _ := response["usageMetadata"].(map[string]interface{})
	promptTokens := getInt(usageMetadata, "promptTokenCount")
	cachedTokens := getInt(usageMetadata, "cachedContentTokenCount")
	outputTokens := getInt(usageMetadata, "candidatesTokenCount")

	// Ensure we have at least one content block
	if len(anthropicContent) == 0 {
		anthropicContent = []types.ContentBlock{{Type: "text", Text: ""}}
	}

	return &types.AnthropicResponse{
		ID:           generateMessageID(),
		Type:         "message",
		Role:         "assistant",
		Content:      anthropicContent,
		Model:        model,
		StopReason:   stopReason,
		StopSequence: nil,
		Usage: types.Usage{
			InputTokens:              promptTokens - cachedTokens,
			OutputTokens:             outputTokens,
			CacheReadInputTokens:     cachedTokens,
			CacheCreationInputTokens: 0,
		},
	}
}

// Helper functions

func convertRole(role string) string {
	if role == "assistant" {
		return "model"
	}
	return "user"
}

// convertBlockToPart converts a single types.ContentBlock to a Google part.
func convertBlockToPart(block types.ContentBlock, isClaudeModel, isGeminiModel bool) interface{} {
	sigCache := GetGlobalSignatureCache()

	switch block.Type {
	case "text":
		// Node parity: skip blocks with only whitespace content
		if strings.TrimSpace(block.Text) != "" {
			return map[string]interface{}{"text": block.Text}
		}

	case "image":
		if block.Source != nil {
			if block.Source.Type == "base64" {
				return map[string]interface{}{
					"inlineData": map[string]interface{}{
						"mimeType": block.Source.MediaType,
						"data":     block.Source.Data,
					},
				}
			} else if block.Source.Type == "url" {
				mimeType := block.Source.MediaType
				if mimeType == "" {
					mimeType = "image/jpeg"
				}
				return map[string]interface{}{
					"fileData": map[string]interface{}{
						"mimeType": mimeType,
						"fileUri":  block.Source.URL,
					},
				}
			}
		}

	case "document":
		if block.Source != nil {
			if block.Source.Type == "base64" {
				return map[string]interface{}{
					"inlineData": map[string]interface{}{
						"mimeType": block.Source.MediaType,
						"data":     block.Source.Data,
					},
				}
			} else if block.Source.Type == "url" {
				mimeType := block.Source.MediaType
				if mimeType == "" {
					mimeType = "application/pdf"
				}
				return map[string]interface{}{
					"fileData": map[string]interface{}{
						"mimeType": mimeType,
						"fileUri":  block.Source.URL,
					},
				}
			}
		}

	case "tool_use":
		input := block.Input
		if input == nil {
			input = make(map[string]interface{})
		}

		functionCall := map[string]interface{}{
			"name": block.Name,
			"args": input,
		}

		if isClaudeModel && block.ID != "" {
			functionCall["id"] = block.ID
		}

		part := map[string]interface{}{"functionCall": functionCall}

		if isGeminiModel {
			var signature string
			if block.ThoughtSignature != "" {
				signature = block.ThoughtSignature
			} else if block.ID != "" {
				signature = sigCache.GetToolSignature(block.ID)
				if signature != "" {
					utils.Debug("[ContentConverter] Restored signature from cache for: %s", block.ID)
				}
			}
			if signature == "" {
				signature = config.GeminiSkipSignature
			}
			part["thoughtSignature"] = signature
		}

		return part

	case "tool_result":
		// NOTE: functionResponse.name uses tool_use_id for Node parity.
		// TODO: Explore mapping to actual tool name if Cloud Code accepts it.
		toolUseID := block.ToolUseID
		if toolUseID == "" {
			toolUseID = "unknown"
		}

		responseContent, _ := processToolResultContentTyped(block.Content)

		functionResponse := map[string]interface{}{
			"name":     toolUseID,
			"response": responseContent,
		}

		if isClaudeModel && block.ToolUseID != "" {
			functionResponse["id"] = block.ToolUseID
		}

		return map[string]interface{}{"functionResponse": functionResponse}

	case "thinking":
		if len(block.Signature) >= config.MinSignatureLength {
			if isGeminiModel {
				sigFamily := sigCache.GetSignatureFamily(block.Signature)
				if sigFamily != "" && sigFamily != "gemini" {
					utils.Debug("[ContentConverter] Dropping incompatible %s thinking for gemini model", sigFamily)
					return nil
				}
				if sigFamily == "" {
					utils.Debug("[ContentConverter] Dropping thinking with unknown signature origin")
					return nil
				}
			}

			return map[string]interface{}{
				"text":             block.Thinking,
				"thought":          true,
				"thoughtSignature": block.Signature,
			}
		}
	}

	return nil
}

func convertSystemToParts(system json.RawMessage) []interface{} {
	parts := make([]interface{}, 0)

	if len(system) == 0 {
		return parts
	}

	// Try to parse as string first
	var str string
	if err := json.Unmarshal(system, &str); err == nil {
		if str != "" {
			parts = append(parts, map[string]interface{}{"text": str})
		}
		return parts
	}

	// Parse as array of system blocks
	var blocks []types.SystemBlock
	if err := json.Unmarshal(system, &blocks); err != nil {
		utils.Warn("[RequestConverter] Failed to parse system prompt: %v", err)
		return parts
	}

	for _, block := range blocks {
		if block.Type == "text" && block.Text != "" {
			parts = append(parts, map[string]interface{}{"text": block.Text})
		}
	}

	return parts
}

func appendSystemHint(googleReq map[string]interface{}, hint string) {
	if si, ok := googleReq["systemInstruction"].(map[string]interface{}); ok {
		if parts, ok := si["parts"].([]interface{}); ok && len(parts) > 0 {
			if lastPart, ok := parts[len(parts)-1].(map[string]interface{}); ok {
				if text, ok := lastPart["text"].(string); ok {
					lastPart["text"] = text + "\n\n" + hint
					return
				}
			}
			si["parts"] = append(parts, map[string]interface{}{"text": hint})
		} else {
			si["parts"] = []interface{}{map[string]interface{}{"text": hint}}
		}
	} else {
		googleReq["systemInstruction"] = map[string]interface{}{
			"parts": []interface{}{map[string]interface{}{"text": hint}},
		}
	}
}

func convertContentToParts(content json.RawMessage, isClaudeModel, isGeminiModel bool) []interface{} {
	parts := make([]interface{}, 0)
	sigCache := GetGlobalSignatureCache()

	if len(content) == 0 {
		return parts
	}

	// Try to parse as string first
	var str string
	if err := json.Unmarshal(content, &str); err == nil {
		if str != "" {
			parts = append(parts, map[string]interface{}{"text": str})
		}
		return parts
	}

	// Parse as array of content blocks
	var blocks []types.ContentBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		utils.Warn("[ContentConverter] Failed to parse content: %v", err)
		return parts
	}

	for _, block := range blocks {
		switch block.Type {
		case "text":
			// Node parity: skip blocks with only whitespace content
			if strings.TrimSpace(block.Text) != "" {
				parts = append(parts, map[string]interface{}{"text": block.Text})
			}

		case "image":
			if block.Source != nil {
				if block.Source.Type == "base64" {
					parts = append(parts, map[string]interface{}{
						"inlineData": map[string]interface{}{
							"mimeType": block.Source.MediaType,
							"data":     block.Source.Data,
						},
					})
				} else if block.Source.Type == "url" {
					mimeType := block.Source.MediaType
					if mimeType == "" {
						mimeType = "image/jpeg"
					}
					parts = append(parts, map[string]interface{}{
						"fileData": map[string]interface{}{
							"mimeType": mimeType,
							"fileUri":  block.Source.URL,
						},
					})
				}
			}

		case "document":
			// Handle document content (e.g. PDF) - Node parity
			if block.Source != nil {
				if block.Source.Type == "base64" {
					parts = append(parts, map[string]interface{}{
						"inlineData": map[string]interface{}{
							"mimeType": block.Source.MediaType,
							"data":     block.Source.Data,
						},
					})
				} else if block.Source.Type == "url" {
					mimeType := block.Source.MediaType
					if mimeType == "" {
						mimeType = "application/pdf"
					}
					parts = append(parts, map[string]interface{}{
						"fileData": map[string]interface{}{
							"mimeType": mimeType,
							"fileUri":  block.Source.URL,
						},
					})
				}
			}

		case "tool_use":
			input := block.Input
			if input == nil {
				input = make(map[string]interface{})
			}

			functionCall := map[string]interface{}{
				"name": block.Name,
				"args": input,
			}

			if isClaudeModel && block.ID != "" {
				functionCall["id"] = block.ID
			}

			part := map[string]interface{}{"functionCall": functionCall}

			// For Gemini models, include thoughtSignature
			if isGeminiModel {
				var signature string
				if block.ThoughtSignature != "" {
					signature = block.ThoughtSignature
				} else if block.ID != "" {
					signature = sigCache.GetToolSignature(block.ID)
					if signature != "" {
						utils.Debug("[ContentConverter] Restored signature from cache for: %s", block.ID)
					}
				}
				if signature == "" {
					signature = config.GeminiSkipSignature
				}
				part["thoughtSignature"] = signature
			}

			parts = append(parts, part)

		case "tool_result":
			toolUseID := block.ToolUseID
			if toolUseID == "" {
				toolUseID = "unknown"
			}

			responseContent, imageParts := processToolResultContentTyped(block.Content)

			functionResponse := map[string]interface{}{
				"name":     toolUseID,
				"response": responseContent,
			}

			if isClaudeModel && block.ToolUseID != "" {
				functionResponse["id"] = block.ToolUseID
			}

			parts = append(parts, map[string]interface{}{"functionResponse": functionResponse})

			// Add any images from the tool result as separate parts (Node parity)
			parts = append(parts, imageParts...)

		case "thinking":
			if len(block.Signature) >= config.MinSignatureLength {
				// Check signature compatibility for Gemini
				if isGeminiModel {
					sigFamily := sigCache.GetSignatureFamily(block.Signature)
					if sigFamily != "" && sigFamily != "gemini" {
						utils.Debug("[ContentConverter] Dropping incompatible %s thinking for gemini model", sigFamily)
						continue
					}
					if sigFamily == "" {
						utils.Debug("[ContentConverter] Dropping thinking with unknown signature origin")
						continue
					}
				}

				parts = append(parts, map[string]interface{}{
					"text":             block.Thinking,
					"thought":          true,
					"thoughtSignature": block.Signature,
				})
			}
		}
	}

	return parts
}

// processToolResultContentTyped handles tool_result content from json.RawMessage
// and extracts both text and images (Node parity).
func processToolResultContentTyped(content json.RawMessage) (map[string]interface{}, []interface{}) {
	imageParts := make([]interface{}, 0)

	if len(content) == 0 {
		return map[string]interface{}{"result": ""}, imageParts
	}

	// Try to parse as string first
	var str string
	if err := json.Unmarshal(content, &str); err == nil {
		return map[string]interface{}{"result": str}, imageParts
	}

	// Parse as array of content blocks
	var blocks []types.ContentBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return map[string]interface{}{"result": ""}, imageParts
	}

	// Extract images and text
	var texts []string
	for _, block := range blocks {
		if block.Type == "image" && block.Source != nil && block.Source.Type == "base64" {
			imageParts = append(imageParts, map[string]interface{}{
				"inlineData": map[string]interface{}{
					"mimeType": block.Source.MediaType,
					"data":     block.Source.Data,
				},
			})
		} else if block.Type == "text" && block.Text != "" {
			texts = append(texts, block.Text)
		}
	}

	// Build result
	result := ""
	if len(texts) > 0 {
		result = joinStrings(texts, "\n")
	} else if len(imageParts) > 0 {
		result = "Image attached"
	}

	return map[string]interface{}{"result": result}, imageParts
}

func sanitizeFunctionName(name string) string {
	result := make([]byte, 0, len(name))
	for i := 0; i < len(name) && len(result) < 64; i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			result = append(result, c)
		} else {
			result = append(result, '_')
		}
	}
	return string(result)
}

func generateToolName(idx int) string {
	bytes := make([]byte, 4)
	rand.Read(bytes)
	return "tool_" + hex.EncodeToString(bytes)
}

func generateToolID() string {
	bytes := make([]byte, 12)
	rand.Read(bytes)
	return "toolu_" + hex.EncodeToString(bytes)
}

func generateMessageID() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return "msg_" + hex.EncodeToString(bytes)
}

func getInt(m map[string]interface{}, key string) int {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}
