package zai

import (
	"bytes"
	"io"
	"testing"
)

func TestStreamingParser(t *testing.T) {
	t.Run("parse simple events", func(t *testing.T) {
		input := "event: message_start\ndata: {\"type\": \"message_start\", \"message\": {\"id\": \"msg_123\"}}\n\n" +
			"event: content_block_delta\ndata: {\"type\": \"content_block_delta\", \"delta\": {\"type\": \"text_delta\", \"text\": \"Hello\"}}\n\n" +
			"event: message_stop\ndata: {\"type\": \"message_stop\"}\n\n"

		reader := io.NopCloser(bytes.NewReader([]byte(input)))
		parser := NewStreamingParser(reader)
		events, done := parser.StreamEvents()

		// Receive message_start
		evt := <-events
		if evt.Type != "message_start" {
			t.Errorf("expected event type message_start, got %s", evt.Type)
		}
		raw := evt.Raw.(map[string]interface{})
		if raw["type"] != "message_start" {
			t.Errorf("expected raw type message_start, got %v", raw["type"])
		}

		// Receive content_block_delta
		evt = <-events
		if evt.Type != "content_block_delta" {
			t.Errorf("expected event type content_block_delta, got %s", evt.Type)
		}
		raw = evt.Raw.(map[string]interface{})
		if raw["type"] != "content_block_delta" {
			t.Errorf("expected raw type content_block_delta, got %v", raw["type"])
		}
		delta := raw["delta"].(map[string]interface{})
		if delta["text"] != "Hello" {
			t.Errorf("expected text Hello, got %v", delta["text"])
		}

		// Receive message_stop
		evt = <-events
		if evt.Type != "message_stop" {
			t.Errorf("expected event type message_stop, got %s", evt.Type)
		}

		err := <-done
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		// Channel should be closed
		_, ok := <-events
		if ok {
			t.Error("expected events channel to be closed")
		}
	})

	t.Run("handles multiline data", func(t *testing.T) {
		input := "event: message_start\n" +
			"data: {\"type\": \"message_start\",\n" +
			"data:  \"message\": {\"id\": \"msg_123\"}}\n\n"

		reader := io.NopCloser(bytes.NewReader([]byte(input)))
		parser := NewStreamingParser(reader)
		events, _ := parser.StreamEvents()

		evt := <-events
		if evt.Type != "message_start" {
			t.Errorf("expected event type message_start, got %s", evt.Type)
		}
		raw := evt.Raw.(map[string]interface{})
		msg := raw["message"].(map[string]interface{})
		if msg["id"] != "msg_123" {
			t.Errorf("expected id msg_123, got %v", msg["id"])
		}
	})

	t.Run("handles [DONE] marker", func(t *testing.T) {
		input := "event: message_stop\ndata: {\"type\": \"message_stop\"}\n\ndata: [DONE]\n\n"

		reader := io.NopCloser(bytes.NewReader([]byte(input)))
		parser := NewStreamingParser(reader)
		events, done := parser.StreamEvents()

		evt := <-events
		if evt.Type != "message_stop" {
			t.Errorf("expected event type message_stop, got %s", evt.Type)
		}

		err := <-done
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		_, ok := <-events
		if ok {
			t.Error("expected events channel to be closed")
		}
	})
}
