package yarouter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnthropicHandler_returnsMessageFromResponsesProvider(t *testing.T) {
	// Given
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCodex,
		name:   "Codex",
		caps:   []Capability{CapabilityResponses},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-5.4", Object: "model"}}},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, body []byte, capability Capability) error {
			if capability != CapabilityResponses || extractModelFromBody(body) != "gpt-5.4" {
				return newProviderError(ProviderCodex, ProviderErrorInvalidRequest, http.StatusBadRequest, false, "unexpected native Responses request")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"resp_1","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"x"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`))
			return nil
		},
	})
	cfg := defaultConfig()
	handler := anthropicHandler(registry, NewModelRouter(registry, cfg.Routing), cfg)
	request := httptest.NewRequest(http.MethodPost, "/v1/messages?beta=true", strings.NewReader(`{"model":"codex/gpt-5.4","messages":[{"role":"user","content":"x"}],"max_tokens":8}`))
	response := httptest.NewRecorder()

	// When
	handler.ServeHTTP(response, request)

	// Then
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var message struct {
		Type  string `json:"type"`
		Model string `json:"model"`
		Usage struct {
			InputTokens int `json:"input_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(response.Body).Decode(&message); err != nil {
		t.Fatalf("decode message: %v", err)
	}
	if message.Type != "message" || message.Model != "codex/gpt-5.4" || message.Usage.InputTokens != 1 {
		t.Fatalf("message=%+v", message)
	}
}

func TestAnthropicHandler_returnsAnthropicErrorEnvelope(t *testing.T) {
	// Given
	handler := anthropicHandler(NewProviderRegistry(), NewModelRouter(NewProviderRegistry(), defaultConfig().Routing), defaultConfig())
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"codex/gpt-5.4","messages":[],"max_tokens":8}`))
	response := httptest.NewRecorder()

	// When
	handler.ServeHTTP(response, request)

	// Then
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope struct {
		Type  string `json:"type"`
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode Anthropic error: %v", err)
	}
	if envelope.Type != "error" || envelope.Error.Type != "invalid_request_error" {
		t.Fatalf("envelope=%+v", envelope)
	}
}

func TestAnthropicHandler_streamsResponsesAsAnthropicEvents(t *testing.T) {
	// Given
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCodex,
		name:   "Codex",
		caps:   []Capability{CapabilityResponses},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-5.4", Object: "model"}}},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, capability Capability) error {
			if capability != CapabilityResponses {
				return newProviderError(ProviderCodex, ProviderErrorInvalidRequest, http.StatusBadRequest, false, "wrong capability")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("event: response.created\ndata: {\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-5.4\"}}\n\n"))
			w.(http.Flusher).Flush()
			_, _ = w.Write([]byte("event: response.output_item.added\ndata: {\"item\":{\"id\":\"msg_1\",\"type\":\"message\"}}\n\n"))
			_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"item_id\":\"msg_1\",\"delta\":\"x\"}\n\n"))
			_, _ = w.Write([]byte("event: response.completed\ndata: {\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"))
			return nil
		},
	})
	cfg := defaultConfig()
	handler := anthropicHandler(registry, NewModelRouter(registry, cfg.Routing), cfg)
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"codex/gpt-5.4","messages":[{"role":"user","content":"x"}],"max_tokens":8,"stream":true}`))
	response := httptest.NewRecorder()

	// When
	handler.ServeHTTP(response, request)

	// Then
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status=%d content-type=%q body=%s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	for _, event := range []string{"event: message_start", "event: content_block_start", "event: content_block_delta", "event: content_block_stop", "event: message_delta", "event: message_stop"} {
		if !strings.Contains(response.Body.String(), event) {
			t.Fatalf("missing %s in %s", event, response.Body.String())
		}
	}
}

func TestAnthropicHandler_resolvesConfiguredClaudeAlias(t *testing.T) {
	// Given
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCodex,
		name:   "Codex",
		caps:   []Capability{CapabilityResponses},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-5.4", Object: "model"}}},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, body []byte, _ Capability) error {
			if extractModelFromBody(body) != "gpt-5.4" {
				return newProviderError(ProviderCodex, ProviderErrorInvalidRequest, http.StatusBadRequest, false, "alias did not resolve")
			}
			_, _ = w.Write([]byte(`{"id":"resp_1","output":[]}`))
			return nil
		},
	})
	cfg := defaultConfig()
	cfg.Routing.ClaudeAliases = map[string]string{"claude-ya-codex-gpt-5-4": "codex/gpt-5.4"}
	handler := anthropicHandler(registry, NewModelRouter(registry, cfg.Routing), cfg)
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-ya-codex-gpt-5-4","messages":[{"role":"user","content":"x"}],"max_tokens":8}`))
	response := httptest.NewRecorder()

	// When
	handler.ServeHTTP(response, request)

	// Then
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAnthropicHandler_preservesRateLimitStatusAndRetryAfter(t *testing.T) {
	// Given
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCodex,
		name:   "Codex",
		caps:   []Capability{CapabilityResponses},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-5.4", Object: "model"}}},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			w.Header().Set("Retry-After", "7")
			return newProviderError(ProviderCodex, ProviderErrorRateLimit, http.StatusTooManyRequests, true, "upstream rate limit")
		},
	})
	cfg := defaultConfig()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"codex/gpt-5.4","messages":[{"role":"user","content":"x"}],"max_tokens":8}`))

	// When
	anthropicHandler(registry, NewModelRouter(registry, cfg.Routing), cfg).ServeHTTP(response, request)

	// Then
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "7" {
		t.Fatalf("status=%d retry-after=%q", response.Code, response.Header().Get("Retry-After"))
	}
}

func TestSecureHandler_returnsAnthropicAuthenticationErrorForMessages(t *testing.T) {
	// Given
	t.Setenv(inboundAPIKeyEnv, "gateway-secret")
	handler := secureHandler(http.NotFoundHandler())
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	response := httptest.NewRecorder()

	// When
	handler.ServeHTTP(response, request)

	// Then
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", response.Code)
	}
	var envelope struct {
		Type  string `json:"type"`
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if envelope.Type != "error" || envelope.Error.Type != "authentication_error" {
		t.Fatalf("envelope=%+v", envelope)
	}
}

func TestAnthropicHandler_doesNotForwardGatewayOrClaudeHeaders(t *testing.T) {
	// Given
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCodex,
		name:   "Codex",
		caps:   []Capability{CapabilityResponses},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-5.4", Object: "model"}}},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, request *http.Request, _ []byte, _ Capability) error {
			if request.Header.Get("Authorization") != "" || request.Header.Get("Anthropic-Beta") != "" || request.Header.Get("X-Claude-Code-Version") != "" {
				return newProviderError(ProviderCodex, ProviderErrorTransport, http.StatusBadGateway, false, "gateway header leaked")
			}
			if request.Header.Get("Idempotency-Key") != "request-key" {
				return newProviderError(ProviderCodex, ProviderErrorInvalidRequest, http.StatusBadRequest, false, "idempotency key missing")
			}
			_, _ = w.Write([]byte(`{"id":"resp_1","output":[]}`))
			return nil
		},
	})
	cfg := defaultConfig()
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"codex/gpt-5.4","messages":[{"role":"user","content":"x"}],"max_tokens":8}`))
	request.Header.Set("Authorization", "Bearer client-token")
	request.Header.Set("Anthropic-Beta", "tool-uses-2024-01-01")
	request.Header.Set("X-Claude-Code-Version", "1.0")
	request.Header.Set("Idempotency-Key", "request-key")
	response := httptest.NewRecorder()

	// When
	anthropicHandler(registry, NewModelRouter(registry, cfg.Routing), cfg).ServeHTTP(response, request)

	// Then
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAnthropicHandler_rejectsUnimplementedTokenCounting(t *testing.T) {
	// Given
	request := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(`{"model":"codex/gpt-5.4","messages":[{"role":"user","content":"x"}]}`))
	response := httptest.NewRecorder()

	// When
	anthropicHandler(NewProviderRegistry(), NewModelRouter(NewProviderRegistry(), defaultConfig().Routing), defaultConfig()).ServeHTTP(response, request)

	// Then
	if response.Code != http.StatusNotImplemented {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
