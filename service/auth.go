package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/nethesis/matrix2acrobits/logger"
)

var (
	ErrAuthFailed = errors.New("authentication failed")
)

// DexTokenResponse represents the token endpoint response from Dex
type DexTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// AuthenticateWithDex authenticates the user against Dex using Resource Owner Password Credentials (password grant).
// It returns the access token on success. The function reads configuration from env vars:
// - DEX_TOKEN_ENDPOINT (optional) - full token endpoint URL. If empty, constructed from MATRIX_HOMESERVER_URL + "/dex/token".
// - DEX_CLIENT_ID - client id for the token endpoint (required)
// - DEX_CLIENT_SECRET - client secret (optional; if set, used as client_secret_post)
// The function performs an HTTP POST and returns the access token string or an error.
func AuthenticateWithDex(ctx context.Context, username, password string) (string, error) {
	if username == "" || password == "" {
		return "", fmt.Errorf("%w: empty username or password", ErrAuthFailed)
	}

	homeserver := os.Getenv("MATRIX_HOMESERVER_URL")
	tokenEndpoint := os.Getenv("DEX_TOKEN_ENDPOINT")
	if tokenEndpoint == "" {
		if homeserver == "" {
			return "", fmt.Errorf("%w: no token endpoint configured and MATRIX_HOMESERVER_URL empty", ErrAuthFailed)
		}
		// default to <homeserver>/dex/token
		tokenEndpoint = strings.TrimSuffix(homeserver, "/") + "/dex/token"
	}

	clientID := os.Getenv("DEX_CLIENT_ID")
	if clientID == "" {
		return "", fmt.Errorf("%w: DEX_CLIENT_ID not set", ErrAuthFailed)
	}
	clientSecret := os.Getenv("DEX_CLIENT_SECRET")

	logger.Debug().Str("token_endpoint", tokenEndpoint).Str("client_id", clientID).Str("client_secret", clientSecret).Str("username", username).Str("password", password).Msg("auth: authenticating with Dex token endpoint")

	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("username", username)
	form.Set("password", password)
	form.Set("scope", "openid email profile")
	// Use client credentials in body (client_secret_post) if client secret is set
	form.Set("client_id", clientID)
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrAuthFailed, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrAuthFailed, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Try to decode error
		var body []byte
		body, _ = io.ReadAll(resp.Body)
		logger.Error().Str("status", resp.Status).Str("body", string(body)).Msg("auth: dex token endpoint returned non-200")
		return "", fmt.Errorf("%w: token endpoint returned status %d", ErrAuthFailed, resp.StatusCode)
	}

	var tr DexTokenResponse
	// Read the full response body so we can debug/log it, then decode
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error().Err(err).Msg("auth: failed to read token response body")
		return "", fmt.Errorf("%w: failed to read token response: %v", ErrAuthFailed, err)
	}
	// Log the raw token response at debug level for troubleshooting
	logger.Debug().Str("token_response", string(bodyBytes)).Msg("auth: dex token response")

	if err := json.Unmarshal(bodyBytes, &tr); err != nil {
		logger.Error().Err(err).Str("body", string(bodyBytes)).Msg("auth: failed to decode token response")
		return "", fmt.Errorf("%w: failed to decode token response: %v", ErrAuthFailed, err)
	}

	// Log selected fields (avoid logging access_token)
	logger.Debug().
		Int("expires_in", tr.ExpiresIn).
		Str("token_type", tr.TokenType).
		Str("scope", tr.Scope).
		Bool("has_refresh_token", tr.RefreshToken != "").
		Msg("auth: parsed token response fields")

	if tr.AccessToken == "" {
		return "", fmt.Errorf("%w: no access_token in response", ErrAuthFailed)
	}

	return tr.AccessToken, nil
}

// (no helper) use io.ReadAll directly
