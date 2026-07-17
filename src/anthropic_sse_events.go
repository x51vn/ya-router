package yarouter

import (
	"encoding/json"
	"fmt"
	"net/http"
)

func (stream *anthropicSSEWriter) handleEvent(raw []byte) error {
	eventType, data := parseResponsesSSEEvent(raw)
	switch eventType {
	case "response.created":
		var event struct {
			Response struct {
				ID string `json:"id"`
			} `json:"response"`
		}
		if err := json.Unmarshal(data, &event); err != nil || event.Response.ID == "" {
			return fmt.Errorf("invalid response.created event")
		}
		stream.messageID = event.Response.ID
		return stream.startMessage()
	case "response.output_item.added":
		return stream.startOutputItem(data)
	case "response.output_text.delta":
		return stream.writeTextDelta(data)
	case "response.function_call_arguments.delta":
		return stream.writeToolDelta(data)
	case "response.output_item.done":
		return stream.stopOutputItem(data)
	case "response.completed":
		return stream.completeMessage(data)
	case "response.in_progress", "response.queued":
		return nil
	case "response.failed", "response.incomplete", "error":
		stream.writeErrorEvent("upstream request failed")
		return fmt.Errorf("upstream Responses stream returned %s", eventType)
	default:
		stream.writeErrorEvent("unsupported upstream stream capability")
		return fmt.Errorf("unsupported Responses stream event %q", eventType)
	}
}

func (stream *anthropicSSEWriter) startMessage() error {
	if stream.started {
		return nil
	}
	stream.started = true
	stream.writer.Header().Set("Content-Type", "text/event-stream")
	stream.writer.Header().Set("Cache-Control", "no-cache")
	stream.writer.Header().Set("Connection", "keep-alive")
	stream.writer.Header().Set("X-Accel-Buffering", "no")
	stream.writer.WriteHeader(http.StatusOK)
	payload := struct {
		Type    string `json:"type"`
		Message struct {
			ID           string         `json:"id"`
			Type         string         `json:"type"`
			Role         string         `json:"role"`
			Content      []string       `json:"content"`
			Model        string         `json:"model"`
			StopReason   *string        `json:"stop_reason"`
			StopSequence *string        `json:"stop_sequence"`
			Usage        anthropicUsage `json:"usage"`
		} `json:"message"`
	}{Type: "message_start"}
	payload.Message.ID, payload.Message.Type, payload.Message.Role, payload.Message.Content, payload.Message.Model = stream.messageID, "message", "assistant", []string{}, stream.publicModel
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return stream.emit("message_start", body)
}

func (stream *anthropicSSEWriter) startOutputItem(data []byte) error {
	var event struct {
		Item struct {
			ID     string `json:"id"`
			Type   string `json:"type"`
			CallID string `json:"call_id"`
			Name   string `json:"name"`
		} `json:"item"`
	}
	if err := json.Unmarshal(data, &event); err != nil || event.Item.ID == "" {
		return fmt.Errorf("invalid response.output_item.added event")
	}
	if err := stream.startMessage(); err != nil {
		return err
	}
	index := len(stream.indices)
	stream.indices[event.Item.ID] = index
	content := anthropicResponseContent{Type: "text"}
	if event.Item.Type == "function_call" {
		if event.Item.CallID == "" || event.Item.Name == "" {
			return fmt.Errorf("function_call stream item is incomplete")
		}
		content = anthropicResponseContent{Type: "tool_use", ID: event.Item.CallID, Name: event.Item.Name, Input: json.RawMessage(`{}`)}
		stream.indices[event.Item.CallID], stream.hadToolUse = index, true
	} else if event.Item.Type != "message" {
		return fmt.Errorf("unsupported Responses output item %q", event.Item.Type)
	}
	stream.active[index] = struct{}{}
	body, err := json.Marshal(struct {
		Type         string                   `json:"type"`
		Index        int                      `json:"index"`
		ContentBlock anthropicResponseContent `json:"content_block"`
	}{Type: "content_block_start", Index: index, ContentBlock: content})
	if err != nil {
		return err
	}
	return stream.emit("content_block_start", body)
}

func (stream *anthropicSSEWriter) writeTextDelta(data []byte) error {
	var event struct {
		ItemID string `json:"item_id"`
		Delta  string `json:"delta"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("invalid response.output_text.delta event")
	}
	index, ok := stream.indices[event.ItemID]
	if !ok {
		return fmt.Errorf("text delta references an unknown output item")
	}
	return stream.emitDelta(index, "text_delta", event.Delta)
}

func (stream *anthropicSSEWriter) writeToolDelta(data []byte) error {
	var event struct {
		ItemID string `json:"item_id"`
		CallID string `json:"call_id"`
		Delta  string `json:"delta"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("invalid response.function_call_arguments.delta event")
	}
	key := event.ItemID
	if key == "" {
		key = event.CallID
	}
	index, ok := stream.indices[key]
	if !ok {
		return fmt.Errorf("tool delta references an unknown output item")
	}
	return stream.emitDelta(index, "input_json_delta", event.Delta)
}
