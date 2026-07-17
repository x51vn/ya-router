package yarouter

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	routingpkg "github.com/duvu/ya-router/internal/routing"
)

func anthropicHandler(registry *ProviderRegistry, router *ModelRouter, cfg *Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAnthropicError(w, http.StatusMethodNotAllowed, newProviderError("", ProviderErrorInvalidRequest, http.StatusMethodNotAllowed, false, "method is not supported"))
			return
		}
		if r.URL.Path == "/v1/messages/count_tokens" {
			writeAnthropicError(w, http.StatusNotImplemented, newProviderError("", ProviderErrorUnsupported, http.StatusNotImplemented, false, "token counting is not implemented"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(cfg.Timeouts.ProxyContext)*time.Second)
		defer cancel()
		r.Body = http.MaxBytesReader(w, r.Body, anthropicRequestLimit)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeAnthropicError(w, http.StatusRequestEntityTooLarge, newProviderError("", ProviderErrorInvalidRequest, http.StatusRequestEntityTooLarge, false, "request body is too large"))
			return
		}
		defer r.Body.Close()
		translated, err := translateAnthropicRequest(body, cfg.Routing.ClaudeAliases)
		if err != nil {
			writeAnthropicError(w, http.StatusBadRequest, newProviderError("", ProviderErrorInvalidRequest, http.StatusBadRequest, false, "%v", err))
			return
		}
		route, err := router.Resolve(ctx, translated.Model, CapabilityResponses)
		if err != nil {
			writeAnthropicRouteError(w, err)
			return
		}
		if route.Selection != nil {
			logUmbrellaSelection(route.Selection, route.Provider.ID())
		}
		nativeBody := patchBodyModel(translated.Body, route.ResolvedModel)
		nativeRequest := nativeResponsesRequest(r, ctx, nativeBody)
		if translated.Stream {
			stream := newAnthropicSSEWriter(w, translated.Model)
			proxyErr := route.Provider.ProxyRequest(ctx, stream, nativeRequest, nativeBody, CapabilityResponses)
			if proxyErr == nil {
				proxyErr = stream.Finish()
			}
			status := stream.StatusCode()
			if route.Selection != nil {
				router.RecordOutcome(route.Selection, status, proxyErr, retryAfterDuration(stream.Header().Get("Retry-After")))
			}
			if proxyErr != nil && !stream.Started() {
				writeAnthropicError(w, providerErrorStatus(proxyErr), proxyErr)
			}
			return
		}
		capture := newAnthropicCapture()
		proxyErr := route.Provider.ProxyRequest(ctx, capture, nativeRequest, nativeBody, CapabilityResponses)
		status := capture.StatusCode()
		if proxyErr != nil {
			if route.Selection != nil {
				router.RecordOutcome(route.Selection, providerErrorStatus(proxyErr), proxyErr, retryAfterDuration(capture.Header().Get("Retry-After")))
			}
			copyAnthropicSafeHeaders(w.Header(), capture.Header())
			writeAnthropicError(w, providerErrorStatus(proxyErr), proxyErr)
			return
		}
		if status >= http.StatusBadRequest {
			copyAnthropicSafeHeaders(w.Header(), capture.Header())
			writeAnthropicError(w, status, newProviderError(route.Provider.ID(), classifyUpstreamStatus(status), status, status >= 500, "upstream request failed"))
			return
		}
		if route.Selection != nil {
			router.RecordOutcome(route.Selection, status, nil, retryAfterDuration(capture.Header().Get("Retry-After")))
		}
		message, err := responsesToAnthropicMessage(capture.Body(), translated.Model)
		if err != nil {
			writeAnthropicError(w, http.StatusBadGateway, newProviderError(route.Provider.ID(), ProviderErrorTransport, http.StatusBadGateway, true, "%v", err))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(message)
	}
}

func writeAnthropicRouteError(w http.ResponseWriter, err error) {
	var noTarget *routingpkg.NoActiveTargetError
	if errors.As(err, &noTarget) {
		writeAnthropicError(w, http.StatusServiceUnavailable, newProviderError("", ProviderErrorModelUnavailable, http.StatusServiceUnavailable, false, "no active target is available"))
		return
	}
	writeAnthropicError(w, http.StatusBadRequest, newProviderError("", ProviderErrorInvalidRequest, http.StatusBadRequest, false, "model is unavailable for Responses"))
}

type anthropicCapture struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newAnthropicCapture() *anthropicCapture {
	return &anthropicCapture{header: make(http.Header)}
}

func (capture *anthropicCapture) Header() http.Header { return capture.header }

func (capture *anthropicCapture) WriteHeader(status int) {
	if capture.status == 0 {
		capture.status = status
	}
}

func (capture *anthropicCapture) Write(body []byte) (int, error) {
	if capture.status == 0 {
		capture.status = http.StatusOK
	}
	return capture.body.Write(body)
}

func (capture *anthropicCapture) Flush() {}

func (capture *anthropicCapture) StatusCode() int {
	if capture.status == 0 {
		return http.StatusOK
	}
	return capture.status
}

func (capture *anthropicCapture) Body() []byte { return capture.body.Bytes() }

func copyAnthropicSafeHeaders(destination, source http.Header) {
	for _, key := range []string{"Retry-After", "Request-Id", "X-Request-Id"} {
		if value := source.Get(key); value != "" {
			destination.Set(key, value)
		}
	}
}

func nativeResponsesRequest(request *http.Request, ctx context.Context, body []byte) *http.Request {
	native := request.Clone(ctx)
	native.URL.Path = "/v1/responses"
	native.Body = io.NopCloser(bytes.NewReader(body))
	native.ContentLength = int64(len(body))
	native.Header = make(http.Header)
	for header, values := range request.Header {
		if classifyAnthropicHeader(header) != anthropicHeaderForwarded {
			continue
		}
		for _, value := range values {
			native.Header.Add(header, value)
		}
	}
	return native
}
