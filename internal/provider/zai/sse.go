package zai

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"

	"github.com/kuzerno1/multi-claude-proxy/internal/utils"
	"github.com/kuzerno1/multi-claude-proxy/pkg/types"
)

// StreamingParser parses SSE events from the Z.AI API.
// Z.AI uses Anthropic-compatible SSE format.
type StreamingParser struct {
	reader io.ReadCloser
}

// NewStreamingParser creates a new SSE parser.
func NewStreamingParser(reader io.ReadCloser) *StreamingParser {
	return &StreamingParser{reader: reader}
}

// StreamEvents parses SSE events and returns them on a channel.
// Returns two channels: events and a done channel that receives any error.
func (p *StreamingParser) StreamEvents() (<-chan types.StreamEvent, <-chan error) {
	events := make(chan types.StreamEvent, 100)
	done := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(done)
		defer p.reader.Close()

		scanner := bufio.NewScanner(p.reader)
		// Increase buffer size for large events
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 1024*1024) // 1MB max

		var currentEvent string
		var currentData strings.Builder

		for scanner.Scan() {
			line := scanner.Text()

			if line == "" {
				// Empty line signals end of event
				if currentEvent != "" && currentData.Len() > 0 {
					evt := p.parseEvent(currentEvent, currentData.String())
					if evt != nil {
						events <- *evt
					}
				}
				currentEvent = ""
				currentData.Reset()
				continue
			}

			if strings.HasPrefix(line, "event:") {
				currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			} else if strings.HasPrefix(line, "data:") {
				data := strings.TrimPrefix(line, "data:")
				data = strings.TrimSpace(data)
				if currentData.Len() > 0 {
					currentData.WriteString("\n")
				}
				currentData.WriteString(data)
			}
		}

		// Handle any remaining event
		if currentEvent != "" && currentData.Len() > 0 {
			evt := p.parseEvent(currentEvent, currentData.String())
			if evt != nil {
				events <- *evt
			}
		}

		if err := scanner.Err(); err != nil {
			utils.Debug("[Z.AI SSE] Scanner error: %v", err)
			done <- err
			return
		}

		done <- nil
	}()

	return events, done
}

// parseEvent parses a single SSE event.
func (p *StreamingParser) parseEvent(eventType, data string) *types.StreamEvent {
	if data == "" || data == "[DONE]" {
		return nil
	}

	var rawData map[string]interface{}
	if err := json.Unmarshal([]byte(data), &rawData); err != nil {
		utils.Debug("[Z.AI SSE] Failed to parse event data: %v", err)
		return nil
	}

	return &types.StreamEvent{
		Type: eventType,
		Raw:  rawData,
	}
}
