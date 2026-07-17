package yarouter

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func setupGracefulShutdown(server *http.Server) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signals
		fmt.Println("\nGracefully shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("Server shutdown error: %v", err)
		}
	}()
}

func healthHandler(registry *ProviderRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimSuffix(r.URL.Path, "/")
		switch path {
		case "/health/ready":
			writeReadiness(w, r, registry)
		case "/health/providers":
			writeProviderHealth(w, r, registry)
		default:
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"status":    "ok",
				"service":   "ya-router",
				"timestamp": time.Now().Unix(),
			})
		}
	}
}

func writeReadiness(w http.ResponseWriter, r *http.Request, registry *ProviderRegistry) {
	if registry == nil || len(registry.All()) == 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"status": "not_ready",
			"reason": "no providers registered",
		})
		return
	}
	ready := 0
	for _, provider := range registry.All() {
		if provider.Health(r.Context()).Authenticated {
			ready++
		}
	}
	status := http.StatusOK
	state := "ready"
	if ready == 0 {
		status = http.StatusServiceUnavailable
		state = "not_ready"
	}
	writeJSON(w, status, map[string]interface{}{
		"status":          state,
		"ready_providers": ready,
		"total_providers": len(registry.All()),
	})
}

func writeProviderHealth(w http.ResponseWriter, r *http.Request, registry *ProviderRegistry) {
	providers := make([]map[string]interface{}, 0)
	if registry != nil {
		for _, provider := range registry.All() {
			health := provider.Health(r.Context())
			providers = append(providers, map[string]interface{}{
				"id":            provider.ID(),
				"name":          provider.Name(),
				"authenticated": health.Authenticated,
				"can_refresh":   health.CanRefresh,
				"capabilities":  provider.Capabilities(),
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":    "ok",
		"providers": providers,
	})
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("health response encoding failed: %v", err)
	}
}
