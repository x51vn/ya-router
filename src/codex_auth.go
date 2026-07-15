// codex_auth.go — native OpenAI device-code authentication for Codex.
//
// The official Codex auth store is read-only import data. ya-router stores its
// own runtime credentials in its permission-restricted configuration so one
// process never truncates or rewrites another application's credential schema.
package yarouter

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
	"path/filepath"
	"strings"
	"time"
)

const (
	codexCredentialSourceEnv           = "OPENAI_API_KEY environment variable"
	codexCredentialSourceOfficialStore = "official Codex auth store"
	codexCredentialSourceProxyConfig   = "ya-router config"

	codexAuthIssuer             = "https://auth.openai.com"
	defaultCodexOAuthClientID   = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexOAuthClientIDEnv       = "YA_ROUTER_CODEX_OAUTH_CLIENT_ID"
	codexUserAgent              = "ya-router/1.0"
	codexDeviceAuthorizationTTL = 15 * time.Minute
)

type resolvedCodexChatGPTAuth struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    int64
	AccountID    string
	Source       string
}

type codexUserCodeResp struct {
	DeviceAuthID string `json:"device_auth_id"`
	UserCode     string `json:"user_code"`
	Interval     string `json:"interval"`
	ExpiresAt    string `json:"expires_at"`
}

type codexDeviceTokenResp struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeChallenge     string `json:"code_challenge"`
	CodeVerifier      string `json:"code_verifier"`
}

type codexOAuthTokenResp struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

type codexRefreshResp struct {
	IDToken      string `json:"id_token,omitempty"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int64  `json:"expires_in"`
}

func codexOAuthClientIDValue() string {
	if value := strings.TrimSpace(os.Getenv(codexOAuthClientIDEnv)); value != "" {
		return value
	}
	return defaultCodexOAuthClientID
}

// codexAuthenticate runs the native device-code flow and writes credentials
// only through the supplied ya-router save callback.
func codexAuthenticate(auth *CodexAuthState, save func() error) error {
	if sharedHTTPClient == nil {
		return errors.New("HTTP client is not initialized")
	}
	now := time.Now().Unix()
	if auth.AccessToken != "" {
		if auth.ExpiresAt == 0 {
			auth.ExpiresAt = extractJWTExpiry(auth.AccessToken)
		}
		if auth.ExpiresAt > now+60 {
			return nil
		}
	}

	apiBase := codexAuthIssuer + "/api/accounts"
	requestBody, _ := json.Marshal(map[string]string{
		"client_id": codexOAuthClientIDValue(),
		"scope":     "openid offline_access model.request",
	})
	request, err := http.NewRequest(http.MethodPost, apiBase+"/deviceauth/usercode", strings.NewReader(string(requestBody)))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", codexUserAgent)

	response, err := sharedHTTPClient.Do(request)
	if err != nil {
		return fmt.Errorf("device code request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("device code request returned %d: %s", response.StatusCode, redactAuthError(body))
	}

	var userCode codexUserCodeResp
	if err := json.NewDecoder(response.Body).Decode(&userCode); err != nil {
		return fmt.Errorf("decode device code response: %w", err)
	}
	if userCode.DeviceAuthID == "" || userCode.UserCode == "" {
		return errors.New("device code response is incomplete")
	}
	interval := 5
	if parsed, err := parseInt(userCode.Interval); err == nil && parsed > 0 {
		interval = parsed
	}

	fmt.Printf("\nFollow these steps to sign in with ChatGPT:\n\n")
	fmt.Printf("1. Open %s/codex/device in your browser.\n", codexAuthIssuer)
	fmt.Printf("2. Enter this one-time code: %s\n\n", userCode.UserCode)
	fmt.Println("Never share the device code with another person.")

	deadline := time.Now().Add(codexDeviceAuthorizationTTL)
	var deviceToken codexDeviceTokenResp
	for time.Now().Before(deadline) {
		time.Sleep(time.Duration(interval) * time.Second)
		candidate, status, pollErr := pollCodexDeviceToken(apiBase, userCode.DeviceAuthID, userCode.UserCode)
		if pollErr != nil {
			return pollErr
		}
		if status == http.StatusForbidden || status == http.StatusNotFound {
			continue
		}
		if status != http.StatusOK {
			return fmt.Errorf("device authorization poll returned status %d", status)
		}
		deviceToken = *candidate
		break
	}
	if deviceToken.AuthorizationCode == "" || deviceToken.CodeVerifier == "" {
		return errors.New("device authorization timed out")
	}

	tokens, err := exchangeCodexAuthCode(deviceToken.AuthorizationCode, deviceToken.CodeVerifier)
	if err != nil {
		return fmt.Errorf("token exchange failed: %w", err)
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		return errors.New("token exchange response is incomplete")
	}
	auth.Mode = "chatgpt"
	auth.AccessToken = tokens.AccessToken
	auth.RefreshToken = tokens.RefreshToken
	auth.AccountID = extractAccountIDFromJWT(tokens.IDToken)
	if auth.AccountID == "" {
		auth.AccountID = extractAccountIDFromJWT(tokens.AccessToken)
	}
	if tokens.ExpiresIn > 0 {
		auth.ExpiresAt = time.Now().Unix() + tokens.ExpiresIn
	} else {
		auth.ExpiresAt = extractJWTExpiry(tokens.AccessToken)
	}
	if err := save(); err != nil {
		return err
	}
	fmt.Println("Codex authentication successful.")
	return nil
}

func pollCodexDeviceToken(apiBase, deviceAuthID, userCode string) (*codexDeviceTokenResp, int, error) {
	body, _ := json.Marshal(map[string]string{
		"device_auth_id": deviceAuthID,
		"user_code":      userCode,
	})
	request, err := http.NewRequest(http.MethodPost, apiBase+"/deviceauth/token", strings.NewReader(string(body)))
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", codexUserAgent)
	response, err := sharedHTTPClient.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, response.StatusCode, nil
	}
	var result codexDeviceTokenResp
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, response.StatusCode, err
	}
	return &result, response.StatusCode, nil
}

func exchangeCodexAuthCode(authCode, codeVerifier string) (*codexOAuthTokenResp, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {authCode},
		"redirect_uri":  {codexAuthIssuer + "/deviceauth/callback"},
		"client_id":     {codexOAuthClientIDValue()},
		"code_verifier": {codeVerifier},
	}
	request, err := http.NewRequest(http.MethodPost, codexAuthIssuer+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("User-Agent", codexUserAgent)
	response, err := sharedHTTPClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("token exchange returned %d: %s", response.StatusCode, redactAuthError(body))
	}
	var tokens codexOAuthTokenResp
	if err := json.NewDecoder(response.Body).Decode(&tokens); err != nil {
		return nil, err
	}
	return &tokens, nil
}

func parseInt(value string) (int, error) {
	value = strings.TrimSpace(value)
	var parsed int
	_, err := fmt.Sscanf(value, "%d", &parsed)
	return parsed, err
}

// codexRefreshToken follows the OAuth form-encoded refresh contract. Client
// errors are terminal; only network failures, 429, and 5xx responses retry.
func codexRefreshToken(auth *CodexAuthState, save func() error) error {
	if auth.RefreshToken == "" {
		return errors.New("no refresh token available for Codex")
	}
	for attempt := 1; attempt <= maxRefreshRetries; attempt++ {
		form := url.Values{
			"client_id":     {codexOAuthClientIDValue()},
			"grant_type":    {"refresh_token"},
			"refresh_token": {auth.RefreshToken},
		}
		request, err := http.NewRequest(http.MethodPost, codexAuthIssuer+"/oauth/token", strings.NewReader(form.Encode()))
		if err != nil {
			return err
		}
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("User-Agent", codexUserAgent)

		response, err := sharedHTTPClient.Do(request)
		if err != nil {
			if attempt == maxRefreshRetries {
				return fmt.Errorf("refresh failed after %d attempts: %w", maxRefreshRetries, err)
			}
			time.Sleep(time.Duration(baseRetryDelay*attempt*attempt) * time.Second)
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 64*1024))
		response.Body.Close()
		if readErr != nil {
			return readErr
		}
		if response.StatusCode != http.StatusOK {
			retryable := response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
			if !retryable || attempt == maxRefreshRetries {
				return fmt.Errorf("refresh error (status %d): %s", response.StatusCode, redactAuthError(body))
			}
			time.Sleep(time.Duration(baseRetryDelay*attempt*attempt) * time.Second)
			continue
		}

		var refreshed codexRefreshResp
		if err := json.Unmarshal(body, &refreshed); err != nil {
			return fmt.Errorf("decode refresh response: %w", err)
		}
		if refreshed.AccessToken == "" {
			return errors.New("refresh response did not include an access token")
		}
		auth.AccessToken = refreshed.AccessToken
		if refreshed.RefreshToken != "" {
			auth.RefreshToken = refreshed.RefreshToken
		}
		if refreshed.IDToken != "" {
			auth.AccountID = extractAccountIDFromJWT(refreshed.IDToken)
		}
		if auth.AccountID == "" {
			auth.AccountID = extractAccountIDFromJWT(refreshed.AccessToken)
		}
		if refreshed.ExpiresIn > 0 {
			auth.ExpiresAt = time.Now().Unix() + refreshed.ExpiresIn
		} else {
			auth.ExpiresAt = extractJWTExpiry(refreshed.AccessToken)
		}
		return save()
	}
	return errors.New("maximum refresh attempts exceeded")
}

func redactAuthError(body []byte) string {
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err == nil {
		if code := authErrorReason(payload["error"]); code != "" {
			return code
		}
		if code := authErrorReason(payload["code"]); code != "" {
			return code
		}
	}
	return "upstream_authentication_rejected"
}

func authErrorReason(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return sanitizeReasonCode(typed)
	case map[string]interface{}:
		for _, key := range []string{"code", "type", "error"} {
			if code := authErrorReason(typed[key]); code != "" {
				return code
			}
		}
	}
	return ""
}

func sanitizeReasonCode(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return "upstream_error"
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') &&
			(char < '0' || char > '9') && char != '_' && char != '-' && char != '.' {
			return "upstream_error"
		}
	}
	return value
}

// officialCodexAuthJSON is intentionally read-only and lenient. Unknown fields
// from newer Codex versions are ignored during import and never rewritten.
type officialCodexAuthJSON struct {
	OpenAIAPIKey *string            `json:"OPENAI_API_KEY"`
	Tokens       *officialTokenData `json:"tokens"`
	LastRefresh  *string            `json:"last_refresh"`
}

type officialTokenData struct {
	IDToken      string  `json:"id_token"`
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	ExpiresAt    int64   `json:"expires_at,omitempty"`
	AccountID    *string `json:"account_id,omitempty"`
}

func isChatGPTMode(mode string) bool {
	switch mode {
	case "chatgpt", "device_code", "chatgpt_device_auth":
		return true
	default:
		return false
	}
}

func isAPIKeyMode(mode string) bool { return mode == "api_key" }

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

// resolveCodexChatGPTAuth retains single-account backward compatibility. The
// provider request path invokes it only when an account-owned token is absent.
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

func applyResolvedCodexChatGPTAuth(destination *CodexAuthState, resolved *resolvedCodexChatGPTAuth) {
	if destination == nil || resolved == nil {
		return
	}
	destination.AccessToken = resolved.AccessToken
	destination.RefreshToken = resolved.RefreshToken
	destination.ExpiresAt = resolved.ExpiresAt
	destination.AccountID = resolved.AccountID
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

func codexHomePath() (string, error) {
	if value := strings.TrimSpace(os.Getenv("CODEX_HOME")); value != "" {
		return value, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".codex"), nil
}

func officialAuthJSONPath() (string, error) {
	home, err := codexHomePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "auth.json"), nil
}

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
	var document officialCodexAuthJSON
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if document.OpenAIAPIKey != nil && strings.TrimSpace(*document.OpenAIAPIKey) != "" {
		return &CodexAuthState{Mode: "api_key", APIKey: *document.OpenAIAPIKey}, nil
	}
	if document.Tokens == nil || document.Tokens.AccessToken == "" {
		return nil, nil
	}
	state := &CodexAuthState{
		Mode:         "chatgpt",
		AccessToken:  document.Tokens.AccessToken,
		RefreshToken: document.Tokens.RefreshToken,
		ExpiresAt:    document.Tokens.ExpiresAt,
	}
	if state.ExpiresAt == 0 {
		state.ExpiresAt = extractJWTExpiry(state.AccessToken)
	}
	if document.Tokens.AccountID != nil {
		state.AccountID = strings.TrimSpace(*document.Tokens.AccountID)
	}
	if state.AccountID == "" && document.Tokens.IDToken != "" {
		state.AccountID = extractAccountIDFromJWT(document.Tokens.IDToken)
	}
	if state.AccountID == "" {
		state.AccountID = extractAccountIDFromJWT(state.AccessToken)
	}
	log.Printf("[codex] imported official auth metadata: has_token=true has_account_metadata=%t", state.AccountID != "")
	return state, nil
}

func extractAccountIDFromJWT(token string) string {
	payload, ok := decodeJWTPayload(token)
	if !ok {
		return ""
	}
	var claims struct {
		AccountID string `json:"chatgpt_account_id"`
		Auth      *struct {
			AccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	if claims.AccountID != "" {
		return claims.AccountID
	}
	if claims.Auth != nil {
		return claims.Auth.AccountID
	}
	return ""
}

func extractJWTExpiry(token string) int64 {
	payload, ok := decodeJWTPayload(token)
	if !ok {
		return 0
	}
	var claims struct {
		ExpiresAt int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return 0
	}
	return claims.ExpiresAt
}

func decodeJWTPayload(token string) ([]byte, bool) {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 || parts[1] == "" {
		return nil, false
	}
	payload := parts[1]
	if remainder := len(payload) % 4; remainder != 0 {
		payload += strings.Repeat("=", 4-remainder)
	}
	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return nil, false
	}
	return decoded, true
}

// persistToOfficialStore is retained for source compatibility only. The proxy
// no longer mutates another application's credential store.
func persistToOfficialStore(_ *CodexAuthState) error {
	return nil
}
