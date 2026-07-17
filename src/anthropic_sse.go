package yarouter

import (
	"bytes"
	"fmt"
	"net/http"
)

const anthropicSSEBufferLimit = 4 * 1024 * 1024

type anthropicSSEWriter struct {
	writer      http.ResponseWriter
	publicModel string
	status      int
	pending     bytes.Buffer
	messageID   string
	indices     map[string]int
	active      map[int]struct{}
	started     bool
	ended       bool
	hadToolUse  bool
	err         error
}

func newAnthropicSSEWriter(writer http.ResponseWriter, publicModel string) *anthropicSSEWriter {
	return &anthropicSSEWriter{writer: writer, publicModel: publicModel, indices: make(map[string]int), active: make(map[int]struct{})}
}

func (stream *anthropicSSEWriter) Header() http.Header { return stream.writer.Header() }

func (stream *anthropicSSEWriter) WriteHeader(status int) {
	if stream.status == 0 {
		stream.status = status
	}
}

func (stream *anthropicSSEWriter) Write(data []byte) (int, error) {
	if stream.err != nil {
		return 0, stream.err
	}
	normalized := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	stream.pending.Write(normalized)
	if stream.pending.Len() > anthropicSSEBufferLimit {
		stream.err = fmt.Errorf("upstream SSE event exceeds the configured limit")
		return 0, stream.err
	}
	for {
		pending := stream.pending.Bytes()
		boundary := bytes.Index(pending, []byte("\n\n"))
		if boundary < 0 {
			return len(data), nil
		}
		event := append([]byte(nil), pending[:boundary]...)
		stream.pending.Next(boundary + 2)
		if err := stream.handleEvent(event); err != nil {
			stream.err = err
			return 0, err
		}
	}
}

func (stream *anthropicSSEWriter) Flush() {
	if flusher, ok := stream.writer.(http.Flusher); ok && stream.started {
		flusher.Flush()
	}
}

func (stream *anthropicSSEWriter) StatusCode() int {
	if stream.status == 0 {
		return http.StatusOK
	}
	return stream.status
}

func (stream *anthropicSSEWriter) Finish() error {
	if stream.err != nil {
		return stream.err
	}
	if !stream.ended {
		return fmt.Errorf("Responses stream ended before response.completed")
	}
	return nil
}

func (stream *anthropicSSEWriter) Started() bool { return stream.started }

func parseResponsesSSEEvent(raw []byte) (string, []byte) {
	var eventType string
	var data [][]byte
	for _, line := range bytes.Split(raw, []byte("\n")) {
		switch {
		case bytes.HasPrefix(line, []byte("event: ")):
			eventType = string(bytes.TrimPrefix(line, []byte("event: ")))
		case bytes.HasPrefix(line, []byte("data: ")):
			data = append(data, bytes.TrimPrefix(line, []byte("data: ")))
		}
	}
	return eventType, bytes.Join(data, []byte("\n"))
}
func (stream *anthropicSSEWriter) emit(event string, body []byte) error {
	if _, err := fmt.Fprintf(stream.writer, "event: %s\ndata: %s\n\n", event, body); err != nil {
		return err
	}
	stream.Flush()
	return nil
}
