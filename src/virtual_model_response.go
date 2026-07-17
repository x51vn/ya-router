package yarouter

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
)

type virtualModelResponseWriter struct {
	http.ResponseWriter
	model         string
	statusCode    int
	headersSent   bool
	streaming     bool
	body          bytes.Buffer
	streamPending bytes.Buffer
}

func newVirtualModelResponseWriter(writer http.ResponseWriter, model string) *virtualModelResponseWriter {
	return &virtualModelResponseWriter{ResponseWriter: writer, model: model}
}

func (writer *virtualModelResponseWriter) WriteHeader(statusCode int) {
	if writer.headersSent {
		return
	}
	writer.headersSent = true
	writer.statusCode = statusCode
}

func (writer *virtualModelResponseWriter) Write(body []byte) (int, error) {
	if writer.streaming {
		return len(body), writer.writeStream(body)
	}
	return writer.body.Write(body)
}

func (writer *virtualModelResponseWriter) Flush() {
	if !writer.streaming {
		writer.streaming = true
		writer.commitHeaders()
		if writer.body.Len() > 0 {
			_ = writer.writeStream(writer.body.Bytes())
			writer.body.Reset()
		}
	}
	if flusher, ok := writer.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (writer *virtualModelResponseWriter) StatusCode() int {
	if writer.statusCode == 0 {
		return http.StatusOK
	}
	return writer.statusCode
}

func (writer *virtualModelResponseWriter) Commit() error {
	if writer.streaming {
		if writer.streamPending.Len() > 0 {
			if _, err := writer.ResponseWriter.Write(rewriteSSEModelLine(writer.streamPending.Bytes(), writer.model)); err != nil {
				return err
			}
			writer.streamPending.Reset()
		}
		return nil
	}
	writer.commitHeaders()
	_, err := writer.ResponseWriter.Write(rewriteModelJSON(writer.body.Bytes(), writer.model))
	return err
}

func (writer *virtualModelResponseWriter) commitHeaders() {
	writer.Header().Del("Content-Length")
	writer.ResponseWriter.WriteHeader(writer.StatusCode())
}

func (writer *virtualModelResponseWriter) writeStream(body []byte) error {
	writer.streamPending.Write(body)
	for {
		pending := writer.streamPending.Bytes()
		index := bytes.IndexByte(pending, '\n')
		if index < 0 {
			return nil
		}
		line := append([]byte(nil), pending[:index+1]...)
		writer.streamPending.Next(index + 1)
		if _, err := writer.ResponseWriter.Write(rewriteSSEModelLine(line, writer.model)); err != nil {
			return err
		}
	}
}

func rewriteSSEModelLine(line []byte, model string) []byte {
	trimmed := strings.TrimSuffix(string(line), "\n")
	if !strings.HasPrefix(trimmed, "data: ") {
		return line
	}
	payload := rewriteModelJSON([]byte(strings.TrimPrefix(trimmed, "data: ")), model)
	return append(append([]byte("data: "), payload...), '\n')
}

func rewriteModelJSON(body []byte, model string) []byte {
	var payload any
	if json.Unmarshal(body, &payload) != nil {
		return body
	}
	rewriteModelField(payload, model)
	rewritten, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return rewritten
}

func rewriteModelField(payload any, model string) {
	switch value := payload.(type) {
	case map[string]any:
		for key, nested := range value {
			if key == "model" {
				if _, ok := nested.(string); ok {
					value[key] = model
				}
				continue
			}
			rewriteModelField(nested, model)
		}
	case []any:
		for _, nested := range value {
			rewriteModelField(nested, model)
		}
	}
}
