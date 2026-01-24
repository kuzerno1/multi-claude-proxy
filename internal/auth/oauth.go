package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kuzerno1/multi-claude-proxy/internal/config"
	"github.com/kuzerno1/multi-claude-proxy/internal/utils"
)

// PKCEData contains PKCE data for OAuth flow.
type PKCEData struct {
	Verifier  string
	Challenge string
	State     string
}

// TokenResponse represents the response from token exchange.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// UserInfo represents user information from Google.
type UserInfo struct {
	Email   string `json:"email"`
	Name    string `json:"name,omitempty"`
	Picture string `json:"picture,omitempty"`
}

// AuthorizationResult contains the result of an authorization flow.
type AuthorizationResult struct {
	Email        string
	RefreshToken string
	AccessToken  string
	ProjectID    string
}

// generatePKCE generates PKCE code verifier and challenge.
func generatePKCE() (*PKCEData, error) {
	// Generate 32 random bytes for verifier
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return nil, fmt.Errorf("failed to generate random bytes: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)

	// Generate challenge from verifier
	hash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])

	// Generate state
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		return nil, fmt.Errorf("failed to generate state: %w", err)
	}
	state := fmt.Sprintf("%x", stateBytes)

	return &PKCEData{
		Verifier:  verifier,
		Challenge: challenge,
		State:     state,
	}, nil
}

// GetAuthorizationURL generates the authorization URL for Google OAuth.
// Returns the URL, PKCE data, and any error.
func GetAuthorizationURL() (string, *PKCEData, error) {
	pkce, err := generatePKCE()
	if err != nil {
		return "", nil, err
	}

	params := url.Values{}
	params.Set("client_id", config.OAuthConfig.ClientID)
	params.Set("redirect_uri", config.OAuthConfig.RedirectURI)
	params.Set("response_type", "code")
	params.Set("scope", strings.Join(config.OAuthConfig.Scopes, " "))
	params.Set("access_type", "offline")
	params.Set("prompt", "consent")
	params.Set("code_challenge", pkce.Challenge)
	params.Set("code_challenge_method", "S256")
	params.Set("state", pkce.State)

	authURL := fmt.Sprintf("%s?%s", config.OAuthConfig.AuthURL, params.Encode())

	return authURL, pkce, nil
}

// ExtractCodeFromInput extracts authorization code and state from user input.
// User can paste either:
// - Full callback URL: http://localhost:51121/oauth-callback?code=xxx&state=xxx
// - Just the code parameter: 4/0xxx...
func ExtractCodeFromInput(input string) (code string, state string, err error) {
	if input == "" {
		return "", "", fmt.Errorf("no input provided")
	}

	input = strings.TrimSpace(input)

	// Check if it looks like a URL
	if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		u, err := url.Parse(input)
		if err != nil {
			return "", "", fmt.Errorf("invalid URL format: %w", err)
		}

		if e := u.Query().Get("error"); e != "" {
			return "", "", fmt.Errorf("OAuth error: %s", e)
		}

		code = u.Query().Get("code")
		if code == "" {
			return "", "", fmt.Errorf("no authorization code found in URL")
		}

		state = u.Query().Get("state")
		return code, state, nil
	}

	// Assume it's a raw code
	if len(input) < 10 {
		return "", "", fmt.Errorf("input is too short to be a valid authorization code")
	}

	return input, "", nil
}

// StartCallbackServer starts a local server to receive the OAuth callback.
// Returns a channel that will receive the authorization code.
func StartCallbackServer(expectedState string, timeout time.Duration) (string, error) {
	var code string
	var authErr error
	var once sync.Once
	done := make(chan struct{})

	mux := http.NewServeMux()
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", config.OAuthConfig.CallbackPort),
		Handler: mux,
	}

	mux.HandleFunc("/oauth-callback", func(w http.ResponseWriter, r *http.Request) {
		// Use sync.Once to ensure we only process the first callback
		// This prevents race conditions from multiple callbacks
		once.Do(func() {
			defer close(done)

			if e := r.URL.Query().Get("error"); e != "" {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, htmlErrorPage("OAuth error: %s", e))
				authErr = fmt.Errorf("OAuth error: %s", e)
				return
			}

			state := r.URL.Query().Get("state")
			if state != expectedState {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, htmlErrorPage("State mismatch - possible CSRF attack"))
				authErr = fmt.Errorf("state mismatch")
				return
			}

			code = r.URL.Query().Get("code")
			if code == "" {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, htmlErrorPage("No authorization code received"))
				authErr = fmt.Errorf("no authorization code")
				return
			}

			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, htmlSuccessPage())
		})

		// For duplicate requests, just return success without processing
	})

	// Start server in goroutine
	go func() {
		utils.Info("[OAuth] Callback server listening on port %d", config.OAuthConfig.CallbackPort)
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			utils.Error("[OAuth] Server error: %v", err)
		}
	}()

	// Wait for callback or timeout
	select {
	case <-done:
		// Callback received
	case <-time.After(timeout):
		authErr = fmt.Errorf("OAuth callback timeout - no response received")
	}

	// Shutdown server
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	server.Shutdown(ctx)

	if authErr != nil {
		return "", authErr
	}

	return code, nil
}

// ExchangeCode exchanges an authorization code for tokens.
func ExchangeCode(code string, verifier string) (*TokenResponse, error) {
	data := url.Values{}
	data.Set("client_id", config.OAuthConfig.ClientID)
	data.Set("client_secret", config.OAuthConfig.ClientSecret)
	data.Set("code", code)
	data.Set("code_verifier", verifier)
	data.Set("grant_type", "authorization_code")
	data.Set("redirect_uri", config.OAuthConfig.RedirectURI)

	resp, err := http.Post(config.OAuthConfig.TokenURL, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("token exchange request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		utils.Error("[OAuth] Token exchange failed: %d %s", resp.StatusCode, string(body))
		return nil, fmt.Errorf("token exchange failed: %s", string(body))
	}

	var tokens TokenResponse
	if err := json.Unmarshal(body, &tokens); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	if tokens.AccessToken == "" {
		return nil, fmt.Errorf("no access token received")
	}

	utils.Info("[OAuth] Token exchange successful, access_token length: %d", len(tokens.AccessToken))

	return &tokens, nil
}

// RefreshAccessToken refreshes an access token using a refresh token.
func RefreshAccessToken(refreshToken string) (*TokenResponse, error) {
	data := url.Values{}
	data.Set("client_id", config.OAuthConfig.ClientID)
	data.Set("client_secret", config.OAuthConfig.ClientSecret)
	data.Set("refresh_token", refreshToken)
	data.Set("grant_type", "refresh_token")

	resp, err := http.Post(config.OAuthConfig.TokenURL, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("token refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh failed: %s", string(body))
	}

	var tokens TokenResponse
	if err := json.Unmarshal(body, &tokens); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	return &tokens, nil
}

// GetUserEmail gets the user's email from an access token.
func GetUserEmail(accessToken string) (string, error) {
	req, err := http.NewRequest("GET", config.OAuthConfig.UserInfoURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("user info request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		utils.Error("[OAuth] getUserEmail failed: %d %s", resp.StatusCode, string(body))
		return "", fmt.Errorf("failed to get user info: %d", resp.StatusCode)
	}

	var userInfo UserInfo
	if err := json.Unmarshal(body, &userInfo); err != nil {
		return "", fmt.Errorf("failed to parse user info: %w", err)
	}

	return userInfo.Email, nil
}

// DiscoverProjectID discovers the project ID for the authenticated user.
func DiscoverProjectID(accessToken string) (string, error) {
	for _, endpoint := range config.AntigravityEndpointFallbacks {
		projectID, err := tryDiscoverProject(endpoint, accessToken)
		if err == nil && projectID != "" {
			return projectID, nil
		}
		utils.Warn("[OAuth] Project discovery failed at %s: %v", endpoint, err)
	}

	return "", fmt.Errorf("project discovery failed for all endpoints")
}

func tryDiscoverProject(endpoint string, accessToken string) (string, error) {
	reqBody := `{"metadata":{"ideType":"IDE_UNSPECIFIED","platform":"PLATFORM_UNSPECIFIED","pluginType":"GEMINI"}}`

	req, err := http.NewRequest("POST", endpoint+"/v1internal:loadCodeAssist", strings.NewReader(reqBody))
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range config.GetAntigravityHeaders() {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", err
	}

	// Try string format
	if project, ok := data["cloudaicompanionProject"].(string); ok {
		utils.Success("[OAuth] Discovered project: %s", project)
		return project, nil
	}

	// Try object format
	if projectObj, ok := data["cloudaicompanionProject"].(map[string]interface{}); ok {
		if id, ok := projectObj["id"].(string); ok {
			utils.Success("[OAuth] Discovered project: %s", id)
			return id, nil
		}
	}

	return "", fmt.Errorf("project not found in response")
}

// CompleteOAuthFlow completes the OAuth flow and returns all account info.
func CompleteOAuthFlow(code string, verifier string) (*AuthorizationResult, error) {
	// Exchange code for tokens
	tokens, err := ExchangeCode(code, verifier)
	if err != nil {
		return nil, err
	}

	// Get user email
	email, err := GetUserEmail(tokens.AccessToken)
	if err != nil {
		return nil, err
	}

	// Discover project ID
	projectID, err := DiscoverProjectID(tokens.AccessToken)
	if err != nil {
		utils.Warn("[OAuth] Project discovery failed, using default: %v", err)
		projectID = config.DefaultProjectID
	}

	return &AuthorizationResult{
		Email:        email,
		RefreshToken: tokens.RefreshToken,
		AccessToken:  tokens.AccessToken,
		ProjectID:    projectID,
	}, nil
}

// HTML templates for callback pages
func htmlSuccessPage() string {
	return `<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"><title>Authentication Successful</title></head>
<body style="font-family: system-ui; padding: 40px; text-align: center;">
    <h1 style="color: #28a745;">Authentication Successful!</h1>
    <p>You can close this window and return to the terminal.</p>
    <script>setTimeout(() => window.close(), 2000);</script>
</body>
</html>`
}

func htmlErrorPage(format string, args ...interface{}) string {
	msg := fmt.Sprintf(format, args...)
	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"><title>Authentication Failed</title></head>
<body style="font-family: system-ui; padding: 40px; text-align: center;">
    <h1 style="color: #dc3545;">Authentication Failed</h1>
    <p>%s</p>
    <p>You can close this window.</p>
</body>
</html>`, msg)
}
