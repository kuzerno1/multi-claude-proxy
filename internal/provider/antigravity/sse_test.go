package antigravity

import (
	"io"
	"strings"
	"testing"
)

func TestStreamingParser_EmitsNodeParityEvents(t *testing.T) {
	thinkingSig := strings.Repeat("s", 60) // >= MinSignatureLength
	toolSig := strings.Repeat("t", 60)     // >= MinSignatureLength

	// One SSE data line that contains thinking -> text -> tool_use.
	input := strings.Join([]string{
		`data: {"response":{"usageMetadata":{"promptTokenCount":10,"cachedContentTokenCount":3,"candidatesTokenCount":7},"candidates":[{"content":{"parts":[` +
			`{"thought":true,"text":"a","thoughtSignature":"` + thinkingSig + `"},` +
			`{"text":"hello"},` +
			`{"functionCall":{"id":"toolu_aaaaaaaaaaaaaaaaaaaaaaaa","name":"do","args":{"a":1}},"thoughtSignature":"` + toolSig + `"}` +
			`]}}]}}`,
		"", // scanner expects newline-terminated lines
	}, "\n")

	parser := NewStreamingParser(io.NopCloser(strings.NewReader(input)), "claude-sonnet-4-5-thinking")
	eventsCh, errCh := parser.StreamEvents()

	events := make([]StreamEvent, 0)
	for evt := range eventsCh {
		events = append(events, evt)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if len(events) < 8 {
		t.Fatalf("expected streaming events, got %d", len(events))
	}

	// message_start
	if events[0].Type != "message_start" {
		t.Fatalf("expected first event message_start, got %q", events[0].Type)
	}

	msgStart, _ := events[0].Data.(map[string]interface{})
	message, _ := msgStart["message"].(map[string]interface{})
	usage, _ := message["usage"].(map[string]interface{})
	if got := asInt(usage["input_tokens"]); got != 7 {
		t.Fatalf("expected message_start usage.input_tokens=7, got %#v", usage["input_tokens"])
	}
	if got := asInt(usage["output_tokens"]); got != 0 {
		t.Fatalf("expected message_start usage.output_tokens=0, got %#v", usage["output_tokens"])
	}
	if got := asInt(usage["cache_read_input_tokens"]); got != 3 {
		t.Fatalf("expected message_start usage.cache_read_input_tokens=3, got %#v", usage["cache_read_input_tokens"])
	}
	if got := asInt(usage["cache_creation_input_tokens"]); got != 0 {
		t.Fatalf("expected message_start usage.cache_creation_input_tokens=0, got %#v", usage["cache_creation_input_tokens"])
	}

	// Ensure signature_delta is emitted when leaving thinking.
	foundSignatureDelta := false
	for _, evt := range events {
		if evt.Type != "content_block_delta" {
			continue
		}
		data, _ := evt.Data.(map[string]interface{})
		delta, _ := data["delta"].(map[string]interface{})
		if delta["type"] == "signature_delta" {
			foundSignatureDelta = true
			break
		}
	}
	if !foundSignatureDelta {
		t.Fatalf("expected a signature_delta event, but none was emitted")
	}

	// message_delta should NOT include input_tokens in usage (Node parity).
	foundMessageDelta := false
	for _, evt := range events {
		if evt.Type != "message_delta" {
			continue
		}
		foundMessageDelta = true
		data, _ := evt.Data.(map[string]interface{})
		usage, _ := data["usage"].(map[string]interface{})
		if _, ok := usage["input_tokens"]; ok {
			t.Fatalf("expected message_delta usage to omit input_tokens, got %#v", usage)
		}
		if got := asInt(usage["output_tokens"]); got != 7 {
			t.Fatalf("expected message_delta usage.output_tokens=7, got %#v", usage["output_tokens"])
		}
		break
	}
	if !foundMessageDelta {
		t.Fatalf("expected a message_delta event, but none was emitted")
	}
}

func asInt(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

func TestStreamingParser_EmptyResponseErrors(t *testing.T) {
	input := strings.Join([]string{
		`data: {"response":{"candidates":[{"content":{"parts":[]}}]}}`,
		"",
	}, "\n")

	parser := NewStreamingParser(io.NopCloser(strings.NewReader(input)), "claude-sonnet-4-5-thinking")
	eventsCh, errCh := parser.StreamEvents()

	for range eventsCh {
		t.Fatalf("expected no events for empty response")
	}

	err := <-errCh
	if err == nil {
		t.Fatalf("expected an error for empty response, got nil")
	}
	if _, ok := err.(*EmptyResponseError); !ok {
		t.Fatalf("expected EmptyResponseError, got %T (%v)", err, err)
	}
}
