package yarouter

import (
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
)

const (
	listenAddressEnv = "YA_ROUTER_LISTEN_ADDRESS"
	inboundAPIKeyEnv = "YA_ROUTER_API_KEY"
	corsOriginsEnv   = "YA_ROUTER_CORS_ALLOWED_ORIGINS"
)

func configuredListenAddress(port int) (string, error) {
	host := strings.TrimSpace(os.Getenv(listenAddressEnv))
	if host == "" {
		host = "127.0.0.1"
	}
	hostForCheck := strings.Trim(host, "[]")
	loopback := strings.EqualFold(hostForCheck, "localhost")
	if ip := net.ParseIP(hostForCheck); ip != nil {
		loopback = ip.IsLoopback()
	}
	if !loopback && strings.TrimSpace(os.Getenv(inboundAPIKeyEnv)) == "" {
		return "", fmt.Errorf("%s must be set when %s is non-loopback", inboundAPIKeyEnv, listenAddressEnv)
	}
	return net.JoinHostPort(hostForCheck, strconv.Itoa(port)), nil
}

func secureHandler(next http.Handler) http.Handler {
	apiKey := strings.TrimSpace(os.Getenv(inboundAPIKeyEnv))
	allowedOrigins := make(map[string]struct{})
	for _, origin := range strings.Split(os.Getenv(corsOriginsEnv), ",") {
		if normalized := strings.TrimSpace(origin); normalized != "" {
			allowedOrigins[normalized] = struct{}{}
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			if _, ok := allowedOrigins[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-API-Key")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			}
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/health") {
			next.ServeHTTP(w, r)
			return
		}
		if apiKey != "" && !validInboundCredential(r, apiKey) {
			if strings.HasPrefix(r.URL.Path, "/v1/messages") {
				writeAnthropicError(w, http.StatusUnauthorized, newProviderError("", ProviderErrorAuthRequired, http.StatusUnauthorized, false, "invalid or missing proxy credential"))
				return
			}
			writeOpenAIError(w, http.StatusUnauthorized, newProviderError("", ProviderErrorAuthRequired, http.StatusUnauthorized, false, "invalid or missing proxy credential"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func validInboundCredential(r *http.Request, expected string) bool {
	supplied := strings.TrimSpace(r.Header.Get("X-API-Key"))
	if supplied == "" {
		authorization := strings.TrimSpace(r.Header.Get("Authorization"))
		if len(authorization) > len("Bearer ") && strings.EqualFold(authorization[:len("Bearer ")], "Bearer ") {
			supplied = strings.TrimSpace(authorization[len("Bearer "):])
		}
	}
	if len(supplied) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(supplied), []byte(expected)) == 1
}
