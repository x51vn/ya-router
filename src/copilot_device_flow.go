package yarouter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type copilotDeviceCredentials struct {
	GitHubToken  string
	CopilotToken string
	ExpiresAt    int64
	RefreshIn    int64
}

func runCopilotDeviceFlow(ctx context.Context, waiting func(map[string]string) error) (copilotDeviceCredentials, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, copilotDeviceCodeURL, strings.NewReader(
		fmt.Sprintf(`{"client_id":"%s","scope":"%s"}`, copilotClientID, copilotScope)))
	if err != nil {
		return copilotDeviceCredentials{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", userAgent)
	response, err := copilotAuthHTTPClient().Do(request)
	if err != nil {
		return copilotDeviceCredentials{}, fmt.Errorf("request device code: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return copilotDeviceCredentials{}, fmt.Errorf("device code request returned status %d", response.StatusCode)
	}
	var device deviceCodeResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&device); err != nil {
		return copilotDeviceCredentials{}, fmt.Errorf("decode device code response: %w", err)
	}
	if device.DeviceCode == "" || device.UserCode == "" || device.VerificationURI == "" {
		return copilotDeviceCredentials{}, fmt.Errorf("device code response is incomplete")
	}
	if err := waiting(map[string]string{"verification_uri": device.VerificationURI, "user_code": device.UserCode}); err != nil {
		return copilotDeviceCredentials{}, err
	}
	githubToken, err := pollForGitHubTokenContext(ctx, device.DeviceCode, device.Interval)
	if err != nil {
		return copilotDeviceCredentials{}, err
	}
	copilotToken, expiresAt, refreshIn, err := getCopilotTokenContext(ctx, githubToken)
	if err != nil {
		return copilotDeviceCredentials{}, err
	}
	return copilotDeviceCredentials{GitHubToken: githubToken, CopilotToken: copilotToken, ExpiresAt: expiresAt, RefreshIn: refreshIn}, nil
}

func pollForGitHubTokenContext(ctx context.Context, deviceCode string, interval int) (string, error) {
	if interval < 1 {
		interval = 1
	}
	for attempt := 0; attempt < 120; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(time.Duration(interval) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return "", ctx.Err()
			case <-timer.C:
			}
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, copilotTokenURL, strings.NewReader(
			fmt.Sprintf(`{"client_id":"%s","device_code":"%s","grant_type":"urn:ietf:params:oauth:grant-type:device_code"}`, copilotClientID, deviceCode)))
		if err != nil {
			return "", err
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("User-Agent", userAgent)
		response, err := copilotAuthHTTPClient().Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			continue
		}
		var token tokenResponse
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&token)
		response.Body.Close()
		if decodeErr != nil {
			return "", fmt.Errorf("decode token response: %w", decodeErr)
		}
		if token.Error == "authorization_pending" {
			continue
		}
		if token.Error != "" {
			return "", fmt.Errorf("authorization error: %s", sanitizeReasonCode(token.Error))
		}
		if token.AccessToken != "" {
			return token.AccessToken, nil
		}
	}
	return "", fmt.Errorf("device authorization timed out")
}

func getCopilotTokenContext(ctx context.Context, githubToken string) (string, int64, int64, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, copilotAPIKeyURL, nil)
	if err != nil {
		return "", 0, 0, err
	}
	request.Header.Set("Authorization", "token "+githubToken)
	request.Header.Set("User-Agent", userAgent)
	response, err := copilotAuthHTTPClient().Do(request)
	if err != nil {
		return "", 0, 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", 0, 0, fmt.Errorf("Copilot token request returned status %d", response.StatusCode)
	}
	var token copilotTokenResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&token); err != nil {
		return "", 0, 0, err
	}
	if token.Token == "" {
		return "", 0, 0, fmt.Errorf("Copilot token response is incomplete")
	}
	return token.Token, token.ExpiresAt, token.RefreshIn, nil
}

func copilotAuthHTTPClient() *http.Client {
	if sharedHTTPClient != nil {
		return sharedHTTPClient
	}
	return http.DefaultClient
}
