package yarouter

import (
	"encoding/json"
	"fmt"
	"sort"
)

func (stream *anthropicSSEWriter) emitDelta(index int, kind, delta string) error {
	payload := struct {
		Type  string `json:"type"`
		Index int    `json:"index"`
		Delta struct {
			Type        string `json:"type"`
			Text        string `json:"text,omitempty"`
			PartialJSON string `json:"partial_json,omitempty"`
		} `json:"delta"`
	}{Type: "content_block_delta", Index: index}
	payload.Delta.Type = kind
	if kind == "text_delta" {
		payload.Delta.Text = delta
	} else {
		payload.Delta.PartialJSON = delta
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return stream.emit("content_block_delta", body)
}

func (stream *anthropicSSEWriter) stopOutputItem(data []byte) error {
	var event struct {
		Item struct {
			ID     string `json:"id"`
			CallID string `json:"call_id"`
		} `json:"item"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("invalid response.output_item.done event")
	}
	key := event.Item.ID
	if key == "" {
		key = event.Item.CallID
	}
	index, ok := stream.indices[key]
	if !ok {
		return fmt.Errorf("output item stop references an unknown item")
	}
	return stream.stopBlock(index)
}

func (stream *anthropicSSEWriter) completeMessage(data []byte) error {
	if err := stream.startMessage(); err != nil {
		return err
	}
	var event struct {
		Response struct {
			Usage *struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		} `json:"response"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("invalid response.completed event")
	}
	indices := make([]int, 0, len(stream.active))
	for index := range stream.active {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	for _, index := range indices {
		if err := stream.stopBlock(index); err != nil {
			return err
		}
	}
	stopReason := "end_turn"
	if stream.hadToolUse {
		stopReason = "tool_use"
	}
	payload := struct {
		Type  string `json:"type"`
		Delta struct {
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
		Usage anthropicUsage `json:"usage"`
	}{Type: "message_delta"}
	payload.Delta.StopReason = stopReason
	if event.Response.Usage != nil {
		payload.Usage.OutputTokens = event.Response.Usage.OutputTokens
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := stream.emit("message_delta", body); err != nil {
		return err
	}
	stream.ended = true
	body, err = json.Marshal(struct {
		Type string `json:"type"`
	}{Type: "message_stop"})
	if err != nil {
		return err
	}
	return stream.emit("message_stop", body)
}

func (stream *anthropicSSEWriter) stopBlock(index int) error {
	if _, ok := stream.active[index]; !ok {
		return nil
	}
	delete(stream.active, index)
	body, err := json.Marshal(struct {
		Type  string `json:"type"`
		Index int    `json:"index"`
	}{Type: "content_block_stop", Index: index})
	if err != nil {
		return err
	}
	return stream.emit("content_block_stop", body)
}

func (stream *anthropicSSEWriter) writeErrorEvent(message string) {
	payload := struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}{Type: "error"}
	payload.Error.Type, payload.Error.Message = "api_error", message
	if body, err := json.Marshal(payload); err == nil {
		_ = stream.emit("error", body)
	}
}
