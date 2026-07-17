package yarouter

import (
	"net/http/httptest"
	"testing"
)

func FuzzTranslateAnthropicRequest_neverPanicsOnMalformedInput(f *testing.F) {
	f.Add([]byte(`{"model":"codex/gpt-5.4","messages":[{"role":"user","content":"x"}],"max_tokens":8}`))
	f.Add([]byte(`{"model":`))

	f.Fuzz(func(t *testing.T, body []byte) {
		// Given
		aliases := map[string]string{"claude-ya-codex-gpt-5-4": "codex/gpt-5.4"}

		// When
		_, _ = translateAnthropicRequest(body, aliases)

		// Then
	})
}

func FuzzAnthropicSSEWriter_neverPanicsOnMalformedFrames(f *testing.F) {
	f.Add([]byte("event: response.created\ndata: {}\n\n"))
	f.Add([]byte("event: response.completed\ndata: {}\n\n"))

	f.Fuzz(func(t *testing.T, frame []byte) {
		// Given
		writer := newAnthropicSSEWriter(httptest.NewRecorder(), "claude-ya-codex-gpt-5-4")

		// When
		_, _ = writer.Write(frame)

		// Then
	})
}
