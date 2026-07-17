package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"
)

const (
	RequestIDHeader     = "X-Request-ID"
	ClientVersionHeader = "X-YA-Client-Version"
)

var safeRequestID = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,128}$`)

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestID := strings.TrimSpace(request.Header.Get(RequestIDHeader))
		if !safeRequestID.MatchString(requestID) {
			requestID = newRequestID()
		}
		writer.Header().Set(RequestIDHeader, requestID)
		ctx := context.WithValue(request.Context(), requestIDContextKey, requestID)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func RequestIDFromContext(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDContextKey).(string)
	return requestID
}

func newRequestID() string {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "req_unavailable"
	}
	return "req_" + hex.EncodeToString(random[:])
}

func suppliedCredential(request *http.Request) string {
	if token := bearerToken(request.Header.Get("Authorization")); token != "" {
		return token
	}
	return strings.TrimSpace(request.Header.Get("X-API-Key"))
}
