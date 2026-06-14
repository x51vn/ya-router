package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func init() {
	if sharedHTTPClient == nil {
		sharedHTTPClient = &http.Client{}
	}
}

func TestIsUnrecoverableRefreshError(t *testing.T) {
	tests := []struct {
		name string
		body []byte
		want bool
	}{
		{
			name: "refresh_token_reused",
			body: []byte(`{"error":{"message":"Your refresh token has already been used.","type":"invalid_request_error","param":null,"code":"refresh_token_reused"}}`),
			want: true,
		},
		{
			name: "invalid_grant",
			body: []byte(`{"error":{"message":"Invalid grant.","type":"invalid_request_error","param":null,"code":"invalid_grant"}}`),
			want: true,
		},
		{
			name: "refresh_token_expired",
			body: []byte(`{"error":{"message":"Refresh token has expired.","type":"invalid_request_error","code":"refresh_token_expired"}}`),
			want: true,
		},
		{
			name: "refresh_token_invalidated",
			body: []byte(`{"error":{"message":"Refresh token has been invalidated.","type":"invalid_request_error","code":"refresh_token_invalidated"}}`),
			want: true,
		},
		{
			name: "transient server error body",
			body: []byte(`{"error":{"message":"internal server error","type":"server_error","code":"internal_server_error"}}`),
			want: false,
		},
		{
			name: "empty body",
			body: []byte(``),
			want: false,
		},
		{
			name: "malformed JSON",
			body: []byte(`not json at all`),
			want: false,
		},
		{
			name: "no code field",
			body: []byte(`{"error":{"message":"something went wrong"}}`),
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isUnrecoverableRefreshError(tc.body)
			if got != tc.want {
				t.Errorf("isUnrecoverableRefreshError(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

func TestCodexRefreshTokenSkipsRetriesOnPermanentError(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"refresh token reused","type":"invalid_request_error","code":"refresh_token_reused"}}`))
	}))
	defer srv.Close()

	oldIssuer := codexAuthIssuer
	codexAuthIssuer = srv.URL
	defer func() { codexAuthIssuer = oldIssuer }()

	auth := &CodexAuthState{
		RefreshToken: "test-refresh-token",
	}
	err := codexRefreshToken(auth, func() error { return nil })
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, errRefreshUnrecoverable) {
		t.Errorf("expected errRefreshUnrecoverable, got: %v", err)
	}
	if attempts != 1 {
		t.Errorf("expected exactly 1 attempt for permanent error, got %d", attempts)
	}
}

func TestCodexRefreshTokenRetriesTransientErrors(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"server error","code":"server_error"}}`))
	}))
	defer srv.Close()

	oldIssuer := codexAuthIssuer
	oldDelay := baseRetryDelay
	codexAuthIssuer = srv.URL
	baseRetryDelay = 0
	defer func() {
		codexAuthIssuer = oldIssuer
		baseRetryDelay = oldDelay
	}()

	auth := &CodexAuthState{RefreshToken: "test-refresh-token"}
	err := codexRefreshToken(auth, func() error { return nil })
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.Is(err, errRefreshUnrecoverable) {
		t.Errorf("transient error should NOT be errRefreshUnrecoverable, got: %v", err)
	}
	if attempts != maxRefreshRetries {
		t.Errorf("expected %d attempts for transient error, got %d", maxRefreshRetries, attempts)
	}
}

// writeOfficialAuthJSON writes a mock auth.json to dir and sets CODEX_HOME=dir.
// Returns a cleanup function that restores the original CODEX_HOME.
func writeOfficialAuthJSON(t *testing.T, dir string, data officialCodexAuthJSON) func() {
	t.Helper()
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatalf("marshal auth.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), b, 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}
	old := os.Getenv("CODEX_HOME")
	os.Setenv("CODEX_HOME", dir)
	return func() { os.Setenv("CODEX_HOME", old) }
}

func TestCodexRefreshTokenUsesOnDiskTokenWhenDifferent(t *testing.T) {
	var capturedRefreshToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			capturedRefreshToken = body["refresh_token"]
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`))
	}))
	defer srv.Close()

	oldIssuer := codexAuthIssuer
	codexAuthIssuer = srv.URL
	defer func() { codexAuthIssuer = oldIssuer }()

	dir := t.TempDir()
	diskRefresh := "disk-refresh-token"
	accountID := "acc-123"
	cleanup := writeOfficialAuthJSON(t, dir, officialCodexAuthJSON{
		Tokens: &officialTokenData{
			AccessToken:  "disk-access",
			RefreshToken: diskRefresh,
			ExpiresAt:    time.Now().Unix() - 10, // expired — force HTTP refresh
			AccountID:    &accountID,
		},
	})
	defer cleanup()

	auth := &CodexAuthState{RefreshToken: "stale-in-memory-refresh"}
	if err := codexRefreshToken(auth, func() error { return nil }); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedRefreshToken != diskRefresh {
		t.Errorf("expected HTTP POST to use on-disk refresh token %q, got %q", diskRefresh, capturedRefreshToken)
	}
}

func TestCodexRefreshTokenSkipsHTTPWhenOnDiskTokenFresh(t *testing.T) {
	httpCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	oldIssuer := codexAuthIssuer
	codexAuthIssuer = srv.URL
	defer func() { codexAuthIssuer = oldIssuer }()

	dir := t.TempDir()
	accountID := "acc-fresh"
	cleanup := writeOfficialAuthJSON(t, dir, officialCodexAuthJSON{
		Tokens: &officialTokenData{
			AccessToken:  "fresh-access",
			RefreshToken: "fresh-refresh",
			ExpiresAt:    time.Now().Unix() + 3600, // well within fresh window
			AccountID:    &accountID,
		},
	})
	defer cleanup()

	auth := &CodexAuthState{RefreshToken: "old-refresh", ExpiresAt: time.Now().Unix() - 60}
	if err := codexRefreshToken(auth, func() error { return nil }); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if httpCalled {
		t.Error("HTTP refresh endpoint was called despite on-disk token being fresh")
	}
	if auth.AccessToken != "fresh-access" {
		t.Errorf("expected auth.AccessToken=%q, got %q", "fresh-access", auth.AccessToken)
	}
}

func TestCodexRefreshTokenContinuesWhenOfficialStoreUnavailable(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`))
	}))
	defer srv.Close()

	oldIssuer := codexAuthIssuer
	codexAuthIssuer = srv.URL
	defer func() { codexAuthIssuer = oldIssuer }()

	old := os.Getenv("CODEX_HOME")
	os.Setenv("CODEX_HOME", "/nonexistent-codex-home-dir-that-does-not-exist")
	defer func() { os.Setenv("CODEX_HOME", old) }()

	auth := &CodexAuthState{RefreshToken: "in-memory-refresh"}
	if err := codexRefreshToken(auth, func() error { return nil }); err != nil {
		t.Fatalf("refresh should succeed when official store is unavailable, got: %v", err)
	}
	if attempts != 1 {
		t.Errorf("expected 1 HTTP attempt, got %d", attempts)
	}
	if auth.AccessToken != "new-access" {
		t.Errorf("expected auth.AccessToken=%q, got %q", "new-access", auth.AccessToken)
	}
}
