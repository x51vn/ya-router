package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/x51vn/github-copilot-svcs/internal/config"
	"github.com/x51vn/github-copilot-svcs/internal/httputil"
	"github.com/x51vn/github-copilot-svcs/internal/provider"
)

func capabilityFromPath(path string) (provider.Capability, error) {
	switch {
	case strings.Contains(path, "/chat/completions"):
		return provider.CapabilityChat, nil
	case strings.Contains(path, "/responses"):
		return provider.CapabilityResponses, nil
	case strings.Contains(path, "/embeddings"):
		return provider.CapabilityEmbeddings, nil
	default:
		return "", fmt.Errorf("unsupported path: %s", path)
	}
}

func ProxyHandler(registry *provider.ProviderRegistry, router *ModelRouter, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(cfg.Timeouts.ProxyContext)*time.Second)
		defer cancel()

		r.Body = http.MaxBytesReader(w, r.Body, 5*1024*1024)
		rw := &httputil.ResponseWrapper{ResponseWriter: w}
		done := make(chan error, 1)

		httputil.GlobalWorkerPool.Submit(func() {
			defer func() {
				if rec := recover(); rec != nil {
					log.Printf("Worker panic: %v", rec)
					done <- fmt.Errorf("internal server error")
				}
			}()
			done <- processProxyRequest(registry, router, cfg, rw, r, ctx)
		})

		select {
		case err := <-done:
			if err != nil && !rw.HeadersSent {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		case <-ctx.Done():
			if !rw.HeadersSent {
				http.Error(w, "Request timeout", http.StatusRequestTimeout)
			}
		}
	}
}

func processProxyRequest(
	registry *provider.ProviderRegistry,
	router *ModelRouter,
	cfg *config.Config,
	w http.ResponseWriter,
	r *http.Request,
	ctx context.Context,
) error {
	reqStart := time.Now()
	cap, err := capabilityFromPath(r.URL.Path)
	if err != nil {
		log.Printf("[REQ] %s %s → unsupported path", r.Method, r.URL.Path)
		return err
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("[REQ] %s %s → body read error: %v", r.Method, r.URL.Path, err)
		return fmt.Errorf("reading request body: %w", err)
	}
	defer r.Body.Close()

	requestedModel := provider.ExtractModelFromBody(body)
	log.Printf("[REQ] %s %s model=%q capability=%s body_size=%d from=%s",
		r.Method, r.URL.Path, requestedModel, cap, len(body), r.RemoteAddr)

	if cap == provider.CapabilityResponses {
		return processResponsesRequest(registry, router, cfg, w, r, ctx, body, requestedModel, reqStart)
	}

	if cap == provider.CapabilityChat {
		_, prefixProvider, hasPrefix := provider.StripModelPrefix(requestedModel)
		if !hasPrefix || prefixProvider == provider.ProviderCopilot {
			if copilot, err := registry.Get(provider.ProviderCopilot); err == nil {
				if freeChatProvider, ok := copilot.(provider.FreeChatProxyProvider); ok {
					log.Printf("[REQ] Chat path ignoring client model=%q and delegating selection to Copilot free-model rotation", requestedModel)
					proxyErr := freeChatProvider.ProxyFreeChatRequest(ctx, w, r, body, requestedModel)
					elapsed := time.Since(reqStart)
					if proxyErr != nil {
						log.Printf("[REQ] COMPLETED %s %s model=%q provider=%s elapsed=%s ERROR: %v",
							r.Method, r.URL.Path, requestedModel, copilot.ID(), elapsed, proxyErr)
					} else {
						log.Printf("[REQ] COMPLETED %s %s model=%q provider=%s elapsed=%s OK",
							r.Method, r.URL.Path, requestedModel, copilot.ID(), elapsed)
					}
					return proxyErr
				}
			}
		}
	}

	route, err := router.Resolve(ctx, requestedModel, cap)
	if err != nil {
		log.Printf("[REQ] %s %s model=%q → routing FAILED: %v", r.Method, r.URL.Path, requestedModel, err)
		return fmt.Errorf("routing: %w", err)
	}

	if route.ResolvedModel != requestedModel {
		log.Printf("[REQ] model rewritten: %q → %q", requestedModel, route.ResolvedModel)
		body = provider.PatchBodyModel(body, route.ResolvedModel)
	}

	log.Printf("[REQ] Routing %s %s model=%q → provider=%s upstream_model=%q",
		r.Method, r.URL.Path, requestedModel, route.Provider.ID(), route.ResolvedModel)

	proxyErr := route.Provider.ProxyRequest(ctx, w, r, body, cap)
	elapsed := time.Since(reqStart)
	if proxyErr != nil {
		log.Printf("[REQ] COMPLETED %s %s model=%q provider=%s elapsed=%s ERROR: %v",
			r.Method, r.URL.Path, requestedModel, route.Provider.ID(), elapsed, proxyErr)
	} else {
		log.Printf("[REQ] COMPLETED %s %s model=%q provider=%s elapsed=%s OK",
			r.Method, r.URL.Path, requestedModel, route.Provider.ID(), elapsed)
	}
	return proxyErr
}

func processResponsesRequest(
	registry *provider.ProviderRegistry,
	router *ModelRouter,
	_ *config.Config,
	w http.ResponseWriter,
	r *http.Request,
	ctx context.Context,
	body []byte,
	requestedModel string,
	reqStart time.Time,
) error {
	if err := provider.ValidateResponsesRequest(body); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"error":{"message":%q,"type":"invalid_request_error","code":"unsupported_responses_feature"}}`, err.Error())))
		log.Printf("[REQ] %s %s model=%q → invalid responses request: %v", r.Method, r.URL.Path, requestedModel, err)
		return nil
	}

	route, err := router.Resolve(ctx, requestedModel, provider.CapabilityChat)
	if err != nil {
		log.Printf("[REQ] %s %s model=%q → responses routing FAILED: %v", r.Method, r.URL.Path, requestedModel, err)
		return fmt.Errorf("routing: %w", err)
	}

	if route.ResolvedModel != requestedModel {
		log.Printf("[REQ] responses model rewritten: %q → %q", requestedModel, route.ResolvedModel)
		body = provider.PatchBodyModel(body, route.ResolvedModel)
	}

	log.Printf("[REQ] Routing %s %s model=%q → provider=%s upstream_model=%q (responses)",
		r.Method, r.URL.Path, requestedModel, route.Provider.ID(), route.ResolvedModel)

	body = provider.SanitizeResponsesBodyForProvider(body, route.Provider.ID())

	if responsesProvider, ok := route.Provider.(provider.ResponsesProxyProvider); ok {
		proxyErr := responsesProvider.ProxyResponsesRequest(ctx, w, r, body, requestedModel)
		elapsed := time.Since(reqStart)
		if proxyErr != nil {
			log.Printf("[REQ] COMPLETED %s %s model=%q provider=%s elapsed=%s ERROR: %v",
				r.Method, r.URL.Path, requestedModel, route.Provider.ID(), elapsed, proxyErr)
		} else {
			log.Printf("[REQ] COMPLETED %s %s model=%q provider=%s elapsed=%s OK",
				r.Method, r.URL.Path, requestedModel, route.Provider.ID(), elapsed)
		}
		return proxyErr
	}

	chatBody, streaming, err := provider.BuildChatCompletionsRequestFromResponses(body)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"error":{"message":%q,"type":"invalid_request_error","code":"unsupported_responses_feature"}}`, err.Error())))
		return nil
	}
	chatBody = provider.SanitizeChatBodyForProvider(chatBody, route.Provider.ID())

	compatReq := r.Clone(ctx)
	compatReq.URL.Path = "/v1/chat/completions"
	compatReq.Body = io.NopCloser(bytes.NewReader(chatBody))

	rr := &httptestResponseRecorder{header: make(http.Header)}
	proxyErr := route.Provider.ProxyRequest(ctx, rr, compatReq, chatBody, provider.CapabilityChat)
	if proxyErr != nil {
		elapsed := time.Since(reqStart)
		log.Printf("[REQ] COMPLETED %s %s model=%q provider=%s elapsed=%s ERROR: %v",
			r.Method, r.URL.Path, requestedModel, route.Provider.ID(), elapsed, proxyErr)
		return proxyErr
	}

	return writeResponsesCompatibilityResult(w, rr, streaming)
}

type httptestResponseRecorder struct {
	header http.Header
	body   bytes.Buffer
	code   int
}

func (r *httptestResponseRecorder) Header() http.Header { return r.header }
func (r *httptestResponseRecorder) Write(data []byte) (int, error) {
	if r.code == 0 {
		r.code = http.StatusOK
	}
	return r.body.Write(data)
}
func (r *httptestResponseRecorder) WriteHeader(statusCode int) { r.code = statusCode }

func writeResponsesCompatibilityResult(w http.ResponseWriter, rr *httptestResponseRecorder, streaming bool) error {
	if rr.code == 0 {
		rr.code = http.StatusOK
	}
	if streaming {
		return provider.WriteResponsesSSEFromChat(w, rr.code, rr.header, rr.body.Bytes())
	}
	return provider.WriteResponsesJSONFromChat(w, rr.code, rr.header, rr.body.Bytes())
}

// ProcessProxyRequest is an exported wrapper for test access.
func ProcessProxyRequest(
	registry *provider.ProviderRegistry,
	router *ModelRouter,
	cfg *config.Config,
	w http.ResponseWriter,
	r *http.Request,
	ctx context.Context,
) error {
	return processProxyRequest(registry, router, cfg, w, r, ctx)
}

// CapabilityFromPath is an exported wrapper for test access.
func CapabilityFromPath(path string) (provider.Capability, error) {
	return capabilityFromPath(path)
}
