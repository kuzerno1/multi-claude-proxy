package copilot

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

const (
	// GitHub OAuth constants
	GitHubClientID       = "Iv1.b507a08c87ecfe98" // VS Code Copilot client ID
	GitHubDeviceCodeURL  = "https://github.com/login/device/code"
	GitHubAccessTokenURL = "https://github.com/login/oauth/access_token"
	GitHubUserURL        = "https://api.github.com/user"
	GitHubAppScopes      = "read:user"

	// Copilot token endpoint
	CopilotTokenURL = "https://api.github.com/copilot_internal/v2/token"

	// API version header
	GitHubAPIVersion = "2022-11-28"

	// HTTP timeout for auth requests
	authHTTPTimeout = 30 * time.Second
)

// authClient is a shared HTTP client with timeout for auth requests.
var authClient = &http.Client{
	Timeout: authHTTPTimeout,
}

// GetDeviceCode initiates the GitHub device authorization flow.
func GetDeviceCode(ctx context.Context) (*DeviceCodeResponse, error) {
	data := url.Values{}
	data.Set("client_id", GitHubClientID)
	data.Set("scope", GitHubAppScopes)

	req, err := http.NewRequestWithContext(ctx, "POST", GitHubDeviceCodeURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := authClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to request device code: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("device code request failed: %s - %s", resp.Status, string(body))
	}

	var result DeviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// PollAccessToken polls GitHub for the access token after user authorization.
// It blocks until the user completes authorization or the context is cancelled.
func PollAccessToken(ctx context.Context, deviceCode *DeviceCodeResponse) (string, error) {
	// Add 1 second buffer to the GitHub-provided interval to avoid rate limiting.
	// GitHub may reject requests that arrive too quickly, so we poll slightly slower.
	interval := time.Duration(deviceCode.Interval+1) * time.Second
	expiry := time.Now().Add(time.Duration(deviceCode.ExpiresIn) * time.Second)

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		if time.Now().After(expiry) {
			return "", fmt.Errorf("device code expired")
		}

		token, err := tryGetAccessToken(ctx, deviceCode.DeviceCode)
		if err == nil && token != "" {
			return token, nil
		}

		// Wait before next poll
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}
	}
}

func tryGetAccessToken(ctx context.Context, deviceCode string) (string, error) {
	data := url.Values{}
	data.Set("client_id", GitHubClientID)
	data.Set("device_code", deviceCode)
	data.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")

	req, err := http.NewRequestWithContext(ctx, "POST", GitHubAccessTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := authClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result AccessTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	// Check for errors in the response
	if result.Error != "" {
		switch result.Error {
		case "authorization_pending":
			// User hasn't completed authorization yet
			return "", nil
		case "slow_down":
			// Need to slow down polling (handled by interval)
			return "", nil
		case "expired_token":
			return "", fmt.Errorf("device code expired")
		case "access_denied":
			return "", fmt.Errorf("user denied authorization")
		default:
			return "", fmt.Errorf("authorization error: %s", result.Error)
		}
	}

	return result.AccessToken, nil
}

// GetCopilotToken exchanges a GitHub access token for a Copilot token.
func GetCopilotToken(ctx context.Context, githubToken string, accountType AccountType) (*CopilotTokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", CopilotTokenURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("token %s", githubToken))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-GitHub-Api-Version", GitHubAPIVersion)
	req.Header.Set("User-Agent", "GitHubCopilotChat/0.26.7")

	resp, err := authClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get copilot token: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("invalid or expired GitHub token")
	}

	if resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("GitHub Copilot access denied - ensure you have an active Copilot subscription")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("copilot token request failed: %s - %s", resp.Status, string(body))
	}

	var result CopilotTokenResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if result.ErrorDetails != "" {
		return nil, fmt.Errorf("copilot token error: %s", result.ErrorDetails)
	}

	return &result, nil
}

// GetGitHubUser fetches the authenticated user's profile.
func GetGitHubUser(ctx context.Context, githubToken string) (*GitHubUser, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", GitHubUserURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("token %s", githubToken))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-GitHub-Api-Version", GitHubAPIVersion)

	resp, err := authClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get user info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("user info request failed: %s - %s", resp.Status, string(body))
	}

	var user GitHubUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("failed to decode user: %w", err)
	}

	return &user, nil
}

// VerifyGitHubToken verifies a GitHub token by attempting to get a Copilot token.
func VerifyGitHubToken(ctx context.Context, githubToken string, accountType AccountType) error {
	_, err := GetCopilotToken(ctx, githubToken, accountType)
	return err
}

const (
	// CopilotUsageURL is the endpoint for fetching Copilot usage/quota information.
	CopilotUsageURL = "https://api.github.com/copilot_internal/user"
)

// GetCopilotUsage fetches the Copilot usage/quota information for an account.
func GetCopilotUsage(ctx context.Context, githubToken string) (*CopilotUsageResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", CopilotUsageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("token %s", githubToken))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-GitHub-Api-Version", GitHubAPIVersion)
	req.Header.Set("User-Agent", "GitHubCopilotChat/0.26.7")

	resp, err := authClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get copilot usage: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("copilot usage request failed: %s - %s", resp.Status, string(body))
	}

	var result CopilotUsageResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}
