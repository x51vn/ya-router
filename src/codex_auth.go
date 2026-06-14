// codex_auth.go — OpenAI device-code authentication for Codex.
// Uses the same endpoints and client_id as the official Codex CLI:
//  1. POST /api/accounts/deviceauth/usercode → {device_auth_id, user_code}
//  2. User visits auth.openai.com/codex/device → enters code
//  3. Poll /api/accounts/deviceauth/token → {authorization_code, code_verifier}
//  4. Exchange auth code (PKCE) at /oauth/token → {id_token, access_token, refresh_token}
//
// For chatgpt mode, tokens are read from / written to the official Codex
// auth store (~/.codex/auth.json or $CODEX_HOME/auth.json).
package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	codexCredentialSourceEnv           = "OPENAI_API_KEY environment variable"
	codexCredentialSourceOfficialStore = "official Codex auth store"
	codexCredentialSourceProxyConfig   = "proxy config fallback"
)

// errRefreshUnrecoverable is returned by codexRefreshToken when the server
// responds with a permanent, non-retryable OAuth error (e.g. refresh_token_reused,
// invalid_grant). Callers can use errors.Is to distinguish this from transient
// failures that may resolve on retry.
var errRefreshUnrecoverable = errors.New("unrecoverable refresh error")

// unrecoverableRefreshCodes lists OAuth error codes that indicate the refresh
// token is permanently invalid and retrying will never succeed.
// Matches the official openai/codex classify_refresh_token_failure mapping.
var unrecoverableRefreshCodes = map[string]bool{
	"refresh_token_reused":      true,
	"refresh_token_expired":     true,
	"refresh_token_invalidated": true,
	"invalid_grant":             true,
}

// isUnrecoverableRefreshError parses a JSON OAuth error body and returns true
// when the "code" field matches a known-permanent failure.
func isUnrecoverableRefreshError(body []byte) bool {
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return false
	}
	return unrecoverableRefreshCodes[envelope.Error.Code]
}

type resolvedCodexChatGPTAuth struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    int64
	AccountID    string
	Source       string
}

// codexAuthIssuer is the base URL for OpenAI auth endpoints.
// Declared as a var so tests can override it to point at a local httptest server.
var codexAuthIssuer = "https://auth.openai.com"

const (
	codexOAuthClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexUserAgent     = "github-copilot-svcs/1.0"
)

// Step 1 response: device auth user code.
type codexUserCodeResp struct {
	DeviceAuthID string `json:"device_auth_id"`
	UserCode     string `json:"user_code"`
	Interval     string `json:"interval"`
	ExpiresAt    string `json:"expires_at"`
}

// Step 3 response: authorization code + PKCE from server.
type codexDeviceTokenResp struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeChallenge     string `json:"code_challenge"`
	CodeVerifier      string `json:"code_verifier"`
}

// Step 4 response: OAuth tokens.
type codexOAuthTokenResp struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// Refresh response.
type codexRefreshResp struct {
	IDToken      string `json:"id_token,omitempty"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int64  `json:"expires_in"`
}

// codexAuthenticate runs the official Codex device-code flow and
// persists the resulting tokens via save.
func codexAuthenticate(auth *CodexAuthState, save func() error) error {
	now := time.Now().Unix()
	if auth.AccessToken != "" && auth.ExpiresAt > now+60 {
		log.Printf("Codex token still valid: expires in %d seconds",
			auth.ExpiresAt-now)
		return nil
	}
	if auth.AccessToken != "" {
		log.Printf("Codex token expiring soon (%d s), re-authenticating",
			auth.ExpiresAt-now)
	} else {
		log.Printf("No Codex token found, starting authentication")
	}

	// Step 1: request device code via OpenAI's custom endpoint.
	// The scope must include model.request for Codex access.
	// api.responses.write is NOT requested here — it is only available on
	// Platform API clients and causes no-op or errors with the ChatGPT
	// device-code OAuth client (app_EMoamEEZ73f0CkXaXp7hrann).
	apiBase := codexAuthIssuer + "/api/accounts"
	ucBody := map[string]string{
		"client_id": codexOAuthClientID,
		"scope":     "openid offline_access model.request",
	}
	ucJSON, _ := json.Marshal(ucBody)

	req, err := http.NewRequest("POST", apiBase+"/deviceauth/usercode",
		strings.NewReader(string(ucJSON)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", codexUserAgent)

	resp, err := sharedHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("device code request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("device code request returned %d: %s",
			resp.StatusCode, string(body))
	}

	var uc codexUserCodeResp
	if err := json.NewDecoder(resp.Body).Decode(&uc); err != nil {
		return err
	}

	interval := 5
	if v, err := parseInt(uc.Interval); err == nil && v > 0 {
		interval = v
	}

	verifyURL := codexAuthIssuer + "/codex/device"
	fmt.Printf("\n"+
		"Follow these steps to sign in with ChatGPT:\n\n"+
		"1. Open this link in your browser and sign in:\n"+
		"   %s\n\n"+
		"2. Enter this one-time code (expires in 15 minutes):\n"+
		"   %s\n\n"+
		"Device codes are a common phishing target. Never share this code.\n\n",
		verifyURL, uc.UserCode)

	// Step 2: poll for authorization code.
	deadline := time.Now().Add(15 * time.Minute)
	var codeResp codexDeviceTokenResp

	for time.Now().Before(deadline) {
		time.Sleep(time.Duration(interval) * time.Second)

		cr, status, pollErr := pollCodexDeviceToken(
			apiBase, uc.DeviceAuthID, uc.UserCode)
		if pollErr != nil {
			return pollErr
		}
		if status == http.StatusForbidden ||
			status == http.StatusNotFound {
			// Authorization pending — user hasn't entered code yet.
			continue
		}
		if status != http.StatusOK {
			return fmt.Errorf("device auth poll returned status %d", status)
		}
		codeResp = *cr
		break
	}
	if codeResp.AuthorizationCode == "" {
		return fmt.Errorf("authentication timed out after 15 minutes")
	}

	// Step 3: exchange authorization code for tokens (with PKCE).
	tokens, err := exchangeCodexAuthCode(
		codeResp.AuthorizationCode, codeResp.CodeVerifier)
	if err != nil {
		return fmt.Errorf("token exchange failed: %w", err)
	}

	auth.AccessToken = tokens.AccessToken
	auth.RefreshToken = tokens.RefreshToken
	// Extract account_id from id_token JWT — required for the
	// chatgpt-account-id header sent to chatgpt.com/backend-api/.
	if tokens.IDToken != "" {
		if aid := extractAccountIDFromJWT(tokens.IDToken); aid != "" {
			auth.AccountID = aid
			log.Printf("[codex] extracted account_id metadata from id_token")
		} else {
			log.Printf("[codex] warning: id_token present but no chatgpt_account_id claim found")
		}
	} else {
		log.Printf("[codex] warning: no id_token in token exchange response")
	}
	if tokens.ExpiresIn > 0 {
		auth.ExpiresAt = time.Now().Unix() + tokens.ExpiresIn
	} else {
		auth.ExpiresAt = time.Now().Unix() + 3600
	}

	if err := save(); err != nil {
		return err
	}
	fmt.Println("Codex authentication successful!")
	return nil
}

// pollCodexDeviceToken polls the device auth token endpoint.
// Returns (response, httpStatus, error).
func pollCodexDeviceToken(
	apiBase, deviceAuthID, userCode string,
) (*codexDeviceTokenResp, int, error) {
	body := map[string]string{
		"device_auth_id": deviceAuthID,
		"user_code":      userCode,
	}
	bodyJSON, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", apiBase+"/deviceauth/token",
		strings.NewReader(string(bodyJSON)))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", codexUserAgent)

	resp, err := sharedHTTPClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, nil
	}

	var cr codexDeviceTokenResp
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return nil, resp.StatusCode, err
	}
	return &cr, http.StatusOK, nil
}

// exchangeCodexAuthCode exchanges an authorization code for tokens
// using the PKCE code_verifier provided by the device auth server.
func exchangeCodexAuthCode(
	authCode, codeVerifier string,
) (*codexOAuthTokenResp, error) {
	redirectURI := codexAuthIssuer + "/deviceauth/callback"
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {authCode},
		"redirect_uri":  {redirectURI},
		"client_id":     {codexOAuthClientID},
		"code_verifier": {codeVerifier},
	}
	req, err := http.NewRequest("POST", codexAuthIssuer+"/oauth/token",
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", codexUserAgent)

	resp, err := sharedHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("token exchange returned %d: %s",
			resp.StatusCode, string(body))
	}

	var tokens codexOAuthTokenResp
	if err := json.NewDecoder(resp.Body).Decode(&tokens); err != nil {
		return nil, err
	}
	return &tokens, nil
}

// parseInt parses a string as an integer (for the interval field).
func parseInt(s string) (int, error) {
	s = strings.TrimSpace(s)
	var v int
	_, err := fmt.Sscanf(s, "%d", &v)
	return v, err
}

// codexRefreshToken uses the stored refresh_token to obtain a new
// access_token. Retries with exponential backoff.
//
// Reload-before-refresh guard: reads ~/.codex/auth.json before the HTTP call.
// If the official store already holds a fresh token (rotated by another process
// such as the official Codex CLI), we adopt that token and skip the HTTP POST.
// If the on-disk refresh token differs from ours, we use the on-disk value so
// we don't present a stale, already-consumed token to the OAuth server.
func codexRefreshToken(auth *CodexAuthState, save func() error) error {
	if auth.RefreshToken == "" {
		return errors.New("no refresh token available for Codex")
	}

	// Reload-before-refresh guard: adopt on-disk credentials when the official
	// Codex store was updated by another process since our last load.
	if disk, err := loadOfficialCodexAuth(); err == nil && disk != nil {
		now := time.Now().Unix()
		if disk.AccessToken != "" && disk.ExpiresAt > now+300 {
			// On-disk token is fresh — no HTTP call needed.
			auth.AccessToken = disk.AccessToken
			auth.RefreshToken = disk.RefreshToken
			auth.ExpiresAt = disk.ExpiresAt
			if disk.AccountID != "" {
				auth.AccountID = disk.AccountID
			}
			log.Printf("[codex] reload-before-refresh: adopted fresh token from official store (expires in %ds)", disk.ExpiresAt-now)
			return save()
		}
		if disk.RefreshToken != "" && disk.RefreshToken != auth.RefreshToken {
			// On-disk refresh token is different (rotated by another process);
			// use it instead of our stale in-memory token.
			log.Printf("[codex] reload-before-refresh: using updated refresh token from official store")
			auth.RefreshToken = disk.RefreshToken
		}
	}
	// If loadOfficialCodexAuth returns an error or nil, proceed with in-memory token (non-fatal).

	for attempt := 1; attempt <= maxRefreshRetries; attempt++ {
		log.Printf("Refreshing Codex token (attempt %d/%d)",
			attempt, maxRefreshRetries)

		refreshReq := map[string]string{
			"client_id":     codexOAuthClientID,
			"grant_type":    "refresh_token",
			"refresh_token": auth.RefreshToken,
		}
		reqJSON, _ := json.Marshal(refreshReq)

		req, err := http.NewRequest("POST",
			codexAuthIssuer+"/oauth/token",
			strings.NewReader(string(reqJSON)))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", codexUserAgent)

		resp, err := sharedHTTPClient.Do(req)
		if err != nil {
			if attempt == maxRefreshRetries {
				return fmt.Errorf("refresh failed after %d attempts: %w",
					maxRefreshRetries, err)
			}
			wait := time.Duration(baseRetryDelay*attempt*attempt) * time.Second
			log.Printf("Refresh failed (attempt %d), retrying in %v: %v",
				attempt, wait, err)
			time.Sleep(wait)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			errMsg := string(body)
			if isUnrecoverableRefreshError(body) {
				return fmt.Errorf("refresh error (status %d): %s: %w",
					resp.StatusCode, errMsg, errRefreshUnrecoverable)
			}
			if attempt == maxRefreshRetries {
				return fmt.Errorf("refresh error (status %d): %s",
					resp.StatusCode, errMsg)
			}
			wait := time.Duration(baseRetryDelay*attempt*attempt) * time.Second
			log.Printf("Refresh error (attempt %d, status %d): %s, retrying in %v",
				attempt, resp.StatusCode, errMsg, wait)
			time.Sleep(wait)
			continue
		}

		var rr codexRefreshResp
		if decErr := json.NewDecoder(resp.Body).Decode(&rr); decErr != nil {
			resp.Body.Close()
			return decErr
		}
		resp.Body.Close()

		log.Printf("Codex token refreshed: expires in %d s", rr.ExpiresIn)
		auth.AccessToken = rr.AccessToken
		if rr.RefreshToken != "" {
			auth.RefreshToken = rr.RefreshToken
		}
		// Update account_id if the refresh response includes a new id_token.
		if rr.IDToken != "" {
			if aid := extractAccountIDFromJWT(rr.IDToken); aid != "" {
				auth.AccountID = aid
				log.Printf("[codex] updated account_id metadata from refreshed id_token")
			}
		}
		if rr.ExpiresIn > 0 {
			auth.ExpiresAt = time.Now().Unix() + rr.ExpiresIn
		} else {
			auth.ExpiresAt = time.Now().Unix() + 3600
		}

		// Also persist refreshed tokens to the official Codex store.
		if isChatGPTMode(auth.Mode) {
			if err := persistToOfficialStore(auth); err != nil {
				log.Printf("[codex] warning: failed to persist refreshed tokens to official store: %v", err)
			}
		}

		return save()
	}
	return errors.New("maximum retry attempts exceeded")
}

// -----------------------------------------------------------------------
// Official Codex auth store (~/.codex/auth.json)
// -----------------------------------------------------------------------

// officialCodexAuthJSON mirrors the on-disk auth.json used by the
// official openai/codex CLI.
type officialCodexAuthJSON struct {
	OpenAIAPIKey *string            `json:"OPENAI_API_KEY"`
	Tokens       *officialTokenData `json:"tokens"`
	LastRefresh  *string            `json:"last_refresh"`
}

// officialTokenData mirrors TokenData from the official Codex CLI.
type officialTokenData struct {
	IDToken      string  `json:"id_token"`
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	ExpiresAt    int64   `json:"expires_at,omitempty"`
	AccountID    *string `json:"account_id,omitempty"`
}

// isChatGPTMode returns true for any mode that uses the ChatGPT backend.
func isChatGPTMode(mode string) bool {
	switch mode {
	case "chatgpt", "device_code", "chatgpt_device_auth":
		return true
	default:
		return false
	}
}

// isAPIKeyMode returns true for explicit API key mode.
func isAPIKeyMode(mode string) bool {
	return mode == "api_key"
}

func resolveCodexAPIKey(auth *CodexAuthState) (string, string, error) {
	if key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); key != "" {
		return key, codexCredentialSourceEnv, nil
	}
	if auth != nil && strings.TrimSpace(auth.APIKey) != "" {
		return auth.APIKey, codexCredentialSourceProxyConfig, nil
	}
	official, err := loadOfficialCodexAuth()
	if err != nil {
		return "", "", err
	}
	if official != nil && strings.TrimSpace(official.APIKey) != "" {
		return official.APIKey, codexCredentialSourceOfficialStore, nil
	}
	return "", "", nil
}

func resolveCodexChatGPTAuth(auth *CodexAuthState) (*resolvedCodexChatGPTAuth, error) {
	official, err := loadOfficialCodexAuth()
	if err != nil {
		return nil, err
	}
	if official != nil && official.AccessToken != "" {
		return &resolvedCodexChatGPTAuth{
			AccessToken:  official.AccessToken,
			RefreshToken: official.RefreshToken,
			ExpiresAt:    official.ExpiresAt,
			AccountID:    official.AccountID,
			Source:       codexCredentialSourceOfficialStore,
		}, nil
	}
	if auth != nil && auth.AccessToken != "" {
		return &resolvedCodexChatGPTAuth{
			AccessToken:  auth.AccessToken,
			RefreshToken: auth.RefreshToken,
			ExpiresAt:    auth.ExpiresAt,
			AccountID:    auth.AccountID,
			Source:       codexCredentialSourceProxyConfig,
		}, nil
	}
	return nil, nil
}

func applyResolvedCodexChatGPTAuth(dst *CodexAuthState, resolved *resolvedCodexChatGPTAuth) {
	if dst == nil || resolved == nil {
		return
	}
	dst.AccessToken = resolved.AccessToken
	dst.RefreshToken = resolved.RefreshToken
	dst.ExpiresAt = resolved.ExpiresAt
	dst.AccountID = resolved.AccountID
}

func clearPersistedChatGPTSecrets(auth *CodexAuthState) {
	if auth == nil {
		return
	}
	auth.AccessToken = ""
	auth.RefreshToken = ""
	auth.ExpiresAt = 0
	auth.AccountID = ""
}

// codexHomePath returns the official Codex home directory, honoring
// $CODEX_HOME and falling back to ~/.codex.
func codexHomePath() (string, error) {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".codex"), nil
}

// officialAuthJSONPath returns the path to the official auth.json file.
func officialAuthJSONPath() (string, error) {
	ch, err := codexHomePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(ch, "auth.json"), nil
}

// officialModelsCachePath returns the path to the official models_cache.json.
func officialModelsCachePath() (string, error) {
	ch, err := codexHomePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(ch, "models_cache.json"), nil
}

// loadOfficialCodexModels reads the Codex CLI cache and returns supported
// public model IDs. This avoids relying on the hardcoded fallback list.
func loadOfficialCodexModels() ([]Model, error) {
	path, err := officialModelsCachePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var payload struct {
		Models []struct {
			Slug           string `json:"slug"`
			SupportedInAPI bool   `json:"supported_in_api"`
			Visibility     string `json:"visibility"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	seen := make(map[string]bool)
	models := make([]Model, 0, len(payload.Models))
	for _, item := range payload.Models {
		if item.Slug == "" || !item.SupportedInAPI || item.Visibility == "hide" || seen[item.Slug] {
			continue
		}
		seen[item.Slug] = true
		models = append(models, Model{ID: item.Slug, Object: "model", OwnedBy: "openai"})
	}
	return models, nil
}

// loadOfficialCodexAuth reads the official Codex auth store and
// populates a CodexAuthState.  Returns nil, nil if no auth.json exists.
func loadOfficialCodexAuth() (*CodexAuthState, error) {
	path, err := officialAuthJSONPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var aj officialCodexAuthJSON
	if err := json.Unmarshal(data, &aj); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	// API key mode.
	if aj.OpenAIAPIKey != nil && *aj.OpenAIAPIKey != "" {
		return &CodexAuthState{
			Mode:   "api_key",
			APIKey: *aj.OpenAIAPIKey,
		}, nil
	}

	// ChatGPT token mode.
	if aj.Tokens == nil || aj.Tokens.AccessToken == "" {
		return nil, nil
	}

	state := &CodexAuthState{
		Mode:         "chatgpt",
		AccessToken:  aj.Tokens.AccessToken,
		RefreshToken: aj.Tokens.RefreshToken,
		ExpiresAt:    aj.Tokens.ExpiresAt,
	}
	// Account ID: prefer explicit field, fall back to JWT claims.
	if aj.Tokens.AccountID != nil && *aj.Tokens.AccountID != "" {
		state.AccountID = *aj.Tokens.AccountID
	} else if aj.Tokens.IDToken != "" {
		state.AccountID = extractAccountIDFromJWT(aj.Tokens.IDToken)
	}

	log.Printf("[codex] loaded official auth: has_token=true has_account_metadata=%t",
		state.AccountID != "")
	return state, nil
}

// extractAccountIDFromJWT parses the id_token JWT to extract
// chatgpt_account_id from the https://api.openai.com/auth claim.
func extractAccountIDFromJWT(jwt string) string {
	parts := strings.SplitN(jwt, ".", 3)
	if len(parts) < 2 {
		return ""
	}
	payload := parts[1]
	if m := len(payload) % 4; m != 0 {
		payload += strings.Repeat("=", 4-m)
	}
	b, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return ""
	}
	var claims struct {
		Auth *struct {
			AccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if json.Unmarshal(b, &claims) == nil && claims.Auth != nil {
		return claims.Auth.AccountID
	}
	return ""
}

// persistToOfficialStore writes the current auth state to ~/.codex/auth.json
// in the format expected by the official Codex CLI.
func persistToOfficialStore(auth *CodexAuthState) error {
	path, err := officialAuthJSONPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create codex home: %w", err)
	}

	// Try to load existing to preserve id_token if available.
	var existing officialCodexAuthJSON
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &existing)
	}

	idToken := ""
	if existing.Tokens != nil {
		idToken = existing.Tokens.IDToken
	}

	var accountID *string
	if auth.AccountID != "" {
		accountID = &auth.AccountID
	}

	now := time.Now().UTC().Format(time.RFC3339)
	aj := officialCodexAuthJSON{
		Tokens: &officialTokenData{
			IDToken:      idToken,
			AccessToken:  auth.AccessToken,
			RefreshToken: auth.RefreshToken,
			ExpiresAt:    auth.ExpiresAt,
			AccountID:    accountID,
		},
		LastRefresh: &now,
	}
	data, err := json.MarshalIndent(aj, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	// If running as root (e.g. docker exec), chown the file to the service user
	// so the non-root service process can read it.
	if os.Getuid() == 0 {
		chownToServiceUser(path)
	}
	return nil
}

func chownToServiceUser(path string) {
	u, err := user.Lookup("appuser")
	if err != nil {
		return
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	_ = os.Chown(path, uid, gid)
}
