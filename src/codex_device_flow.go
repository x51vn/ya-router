package yarouter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type codexDeviceCredentials struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    int64
	AccountID    string
}

func runCodexDeviceFlow(ctx context.Context, waiting func(map[string]string) error) (codexDeviceCredentials, error) {
	requestBody, _ := json.Marshal(map[string]string{
		"client_id": codexOAuthClientIDValue(),
		"scope":     "openid offline_access model.request",
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, codexAuthIssuer+"/api/accounts/deviceauth/usercode", strings.NewReader(string(requestBody)))
	if err != nil {
		return codexDeviceCredentials{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", codexUserAgent)
	response, err := codexAuthHTTPClient().Do(request)
	if err != nil {
		return codexDeviceCredentials{}, fmt.Errorf("request device code: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return codexDeviceCredentials{}, fmt.Errorf("device code request returned status %d", response.StatusCode)
	}
	var device codexUserCodeResp
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&device); err != nil {
		return codexDeviceCredentials{}, fmt.Errorf("decode device code response: %w", err)
	}
	if device.DeviceAuthID == "" || device.UserCode == "" {
		return codexDeviceCredentials{}, fmt.Errorf("device code response is incomplete")
	}
	if err := waiting(map[string]string{"verification_uri": codexAuthIssuer + "/codex/device", "user_code": device.UserCode}); err != nil {
		return codexDeviceCredentials{}, err
	}
	interval := 5
	if parsed, err := parseInt(device.Interval); err == nil && parsed > 0 {
		interval = parsed
	}
	deviceToken, err := pollCodexDeviceTokenContext(ctx, device.DeviceAuthID, device.UserCode, interval)
	if err != nil {
		return codexDeviceCredentials{}, err
	}
	tokens, err := exchangeCodexAuthCodeContext(ctx, deviceToken.AuthorizationCode, deviceToken.CodeVerifier)
	if err != nil {
		return codexDeviceCredentials{}, err
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		return codexDeviceCredentials{}, fmt.Errorf("token exchange response is incomplete")
	}
	accountID := extractAccountIDFromJWT(tokens.IDToken)
	if accountID == "" {
		accountID = extractAccountIDFromJWT(tokens.AccessToken)
	}
	expiresAt := extractJWTExpiry(tokens.AccessToken)
	if tokens.ExpiresIn > 0 {
		expiresAt = time.Now().Unix() + tokens.ExpiresIn
	}
	return codexDeviceCredentials{AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken, ExpiresAt: expiresAt, AccountID: accountID}, nil
}

func pollCodexDeviceTokenContext(ctx context.Context, deviceAuthID, userCode string, interval int) (*codexDeviceTokenResp, error) {
	if interval < 1 {
		interval = 1
	}
	for attempt := 0; attempt < 180; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(time.Duration(interval) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		body, _ := json.Marshal(map[string]string{"device_auth_id": deviceAuthID, "user_code": userCode})
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, codexAuthIssuer+"/api/accounts/deviceauth/token", strings.NewReader(string(body)))
		if err != nil {
			return nil, err
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("User-Agent", codexUserAgent)
		response, err := codexAuthHTTPClient().Do(request)
		if err != nil {
			return nil, err
		}
		if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusNotFound {
			response.Body.Close()
			continue
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			return nil, fmt.Errorf("device authorization poll returned status %d", response.StatusCode)
		}
		var result codexDeviceTokenResp
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&result)
		response.Body.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("decode device authorization response: %w", decodeErr)
		}
		if result.AuthorizationCode == "" || result.CodeVerifier == "" {
			return nil, fmt.Errorf("device authorization response is incomplete")
		}
		return &result, nil
	}
	return nil, fmt.Errorf("device authorization timed out")
}

func exchangeCodexAuthCodeContext(ctx context.Context, authCode, codeVerifier string) (*codexOAuthTokenResp, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {authCode},
		"redirect_uri":  {codexAuthIssuer + "/deviceauth/callback"},
		"client_id":     {codexOAuthClientIDValue()},
		"code_verifier": {codeVerifier},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, codexAuthIssuer+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("User-Agent", codexUserAgent)
	response, err := codexAuthHTTPClient().Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange returned status %d", response.StatusCode)
	}
	var tokens codexOAuthTokenResp
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&tokens); err != nil {
		return nil, err
	}
	return &tokens, nil
}

func codexAuthHTTPClient() *http.Client {
	if sharedHTTPClient != nil {
		return sharedHTTPClient
	}
	return http.DefaultClient
}
