package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/nethesis/matrix2acrobits/logger"
	"github.com/nethesis/matrix2acrobits/models"
)

// AuthResponse represents the user data returned from the external /chat endpoint.
type AuthResponse struct {
	MainExtension string   `json:"main_extension"`
	SubExtensions []string `json:"sub_extensions"`
	UserName      string   `json:"user_name"`
}

// HTTPAuthClient is the default AuthClient implementation that calls the external HTTP endpoint.
type HTTPAuthClient struct {
	url      string
	client   *http.Client
	mu       sync.RWMutex
	cache    map[string]cachedAuth
	cacheTTL time.Duration
}

// NewHTTPAuthClient constructs an HTTPAuthClient.
func NewHTTPAuthClient(url string, timeout time.Duration, cacheTTL time.Duration) *HTTPAuthClient {
	return &HTTPAuthClient{
		url: url,
		client: &http.Client{
			Timeout: timeout,
		},
		cache:    make(map[string]cachedAuth),
		cacheTTL: cacheTTL,
	}
}

type cachedAuth struct {
	expiry time.Time
}

// Validate performs a 2-step authentication process:
// 1. POST to /api/login with username and password to get JWT token
// 2. Extracts nethvoice_cti.chat claim from JWT
// 3. If claim exists, GET /api/chat?users to retrieve user mappings
// homeserverHost is used to build full Matrix IDs when the returned user_name is a localpart.
func (h *HTTPAuthClient) Validate(ctx context.Context, username, password, homeserverHost string) ([]*models.MappingRequest, bool, error) {
	// Normalize username: if it's in the form localpart@domain, remove the domain part
	username = strings.TrimSpace(username)
	if at := strings.Index(username, "@"); at > 0 {
		original := username
		username = username[:at]
		logger.Debug().Str("original_username", original).Str("username", username).Msg("authclient: stripped domain from username")
	}
	// Check cache
	key := username + "|" + homeserverHost
	logger.Debug().Str("key", key).Str("username", username).Msg("authclient: validate called")
	if h.cacheTTL > 0 {
		h.mu.RLock()
		if c, ok := h.cache[key]; ok {
			if time.Now().Before(c.expiry) {
				h.mu.RUnlock()
				logger.Debug().Str("key", key).Time("expiry", c.expiry).Msg("authclient: cache hit")
				return []*models.MappingRequest{}, true, nil
			}
		}
		h.mu.RUnlock()
		logger.Debug().Str("key", key).Msg("authclient: cache miss or expired")
	}

	// Step 1: POST /api/login to get JWT token
	loginURL := strings.TrimRight(h.url, "/") + "/api/login"
	loginReq := models.LoginRequest{
		Username: username,
		Password: password,
	}
	body, _ := json.Marshal(loginReq)
	req, err := http.NewRequestWithContext(ctx, "POST", loginURL, bytes.NewReader(body))
	if err != nil {
		return []*models.MappingRequest{}, false, fmt.Errorf("failed to create login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	logger.Debug().Str("url", loginURL).Str("username", username).Msg("authclient: sending login request")

	resp, err := h.client.Do(req)
	if err != nil {
		return []*models.MappingRequest{}, false, fmt.Errorf("login request failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	logger.Debug().Int("status", resp.StatusCode).Msg("authclient: login response received")
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		logger.Debug().Int("status", resp.StatusCode).Bytes("body", b).Msg("authclient: login failed")
		return []*models.MappingRequest{}, false, fmt.Errorf("login failed: status %d", resp.StatusCode)
	}

	var loginResp models.LoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		logger.Debug().Err(err).Msg("authclient: failed to decode login response")
		return []*models.MappingRequest{}, false, fmt.Errorf("failed to decode login response: %w", err)
	}

	logger.Debug().Msg("authclient: JWT token obtained from login endpoint")

	// Step 2: Parse JWT to check for nethvoice_cti.chat claim
	// Parse without signature verification by decoding the payload
	parts := strings.Split(loginResp.Token, ".")
	if len(parts) != 3 {
		logger.Warn().Msg("authclient: invalid JWT format (not 3 parts)")
		return []*models.MappingRequest{}, false, fmt.Errorf("invalid JWT format")
	}

	// Decode the claims part (base64url)
	// Add padding if necessary
	claimsStr := parts[1]
	padding := 4 - (len(claimsStr) % 4)
	if padding != 4 {
		claimsStr = claimsStr + strings.Repeat("=", padding)
	}

	claimsData, err := base64.URLEncoding.DecodeString(claimsStr)
	if err != nil {
		logger.Warn().Err(err).Msg("authclient: failed to decode JWT claims")
		return []*models.MappingRequest{}, false, fmt.Errorf("failed to decode JWT claims: %w", err)
	}

	var claims jwt.MapClaims
	if err := json.Unmarshal(claimsData, &claims); err != nil {
		logger.Warn().Err(err).Msg("authclient: failed to unmarshal JWT claims")
		return []*models.MappingRequest{}, false, fmt.Errorf("failed to unmarshal JWT claims: %w", err)
	}

	// Check for nethvoice_cti.chat claim
	chatClaimValue, hasChatClaim := claims["nethvoice_cti.chat"]
	if !hasChatClaim {
		logger.Warn().Str("username", username).Msg("authclient: missing nethvoice_cti.chat claim in JWT")
		return []*models.MappingRequest{}, false, fmt.Errorf("user does not have nethvoice_cti.chat capability")
	}

	// Verify the claim is true
	hasChatAccess := false
	if boolVal, ok := chatClaimValue.(bool); ok {
		hasChatAccess = boolVal
	} else if strVal, ok := chatClaimValue.(string); ok {
		hasChatAccess = strings.ToLower(strVal) == "true"
	}

	if !hasChatAccess {
		logger.Warn().Str("username", username).Msg("authclient: nethvoice_cti.chat claim is false")
		return []*models.MappingRequest{}, false, fmt.Errorf("user does not have chat access")
	}

	logger.Debug().Str("username", username).Msg("authclient: nethvoice_cti.chat claim verified")

	// Step 3: GET /api/chat?users=1 to retrieve user mappings
	chatURL := strings.TrimRight(h.url, "/") + "/api/chat?users=1"
	chatReq, err := http.NewRequestWithContext(ctx, "GET", chatURL, nil)
	if err != nil {
		return []*models.MappingRequest{}, false, fmt.Errorf("failed to create chat request: %w", err)
	}
	chatReq.Header.Set("Authorization", "Bearer "+loginResp.Token)

	logger.Debug().Str("url", chatURL).Msg("authclient: sending chat request")

	chatResp, err := h.client.Do(chatReq)
	if err != nil {
		return []*models.MappingRequest{}, false, fmt.Errorf("chat request failed: %w", err)
	}
	defer func() {
		_ = chatResp.Body.Close()
	}()

	logger.Debug().Int("status", chatResp.StatusCode).Msg("authclient: chat response received")
	if chatResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(chatResp.Body)
		logger.Debug().Int("status", chatResp.StatusCode).Bytes("body", b).Msg("authclient: chat request failed")
		return []*models.MappingRequest{}, false, fmt.Errorf("chat request failed: status %d", chatResp.StatusCode)
	}

	var chatResponse models.ChatResponse
	if err := json.NewDecoder(chatResp.Body).Decode(&chatResponse); err != nil {
		logger.Debug().Err(err).Msg("authclient: failed to decode chat response")
		return []*models.MappingRequest{}, false, fmt.Errorf("failed to decode chat response: %w", err)
	}

	logger.Debug().Int("user_count", len(chatResponse.Users)).Msg("authclient: parsed chat response")

	// Cache successful authentication
	if h.cacheTTL > 0 {
		h.mu.Lock()
		h.cache[key] = cachedAuth{expiry: time.Now().Add(h.cacheTTL)}
		h.mu.Unlock()
		logger.Debug().Str("key", key).Time("expiry", time.Now().Add(h.cacheTTL)).Msg("authclient: cached successful authentication")
	}

	// Convert chat users to mappings
	mappings := make([]*models.MappingRequest, 0, len(chatResponse.Users))
	for _, user := range chatResponse.Users {
		logger.Debug().Str("user_name", user.UserName).Str("main_extension", user.MainExtension).Strs("sub_extensions", user.SubExtensions).Msg("authclient: processing chat user")

		// Validate main_extension exists and is a number
		mainExtStr := strings.TrimSpace(user.MainExtension)
		if mainExtStr == "" {
			logger.Warn().Msg("authclient: user has empty main_extension, skipping")
			continue
		}
		mainNum, err := strconv.Atoi(mainExtStr)
		if err != nil {
			logger.Warn().Str("main_extension", mainExtStr).Err(err).Msg("authclient: main_extension is not a valid number, skipping")
			continue
		}

		// Parse sub extensions
		subNums := make([]int, 0, len(user.SubExtensions))
		for _, ssub := range user.SubExtensions {
			ssub = strings.TrimSpace(ssub)
			if ssub == "" {
				continue
			}
			if v, err := strconv.Atoi(ssub); err == nil {
				subNums = append(subNums, v)
			} else {
				logger.Debug().Str("sub_extension", ssub).Err(err).Msg("authclient: skipping invalid sub_extension")
			}
		}

		// Build matrix id
		if user.UserName == "" {
			logger.Warn().Msg("authclient: user has empty user_name, skipping")
			continue
		}
		matrixID := fmt.Sprintf("@%s:%s", strings.ToLower(strings.TrimSpace(user.UserName)), homeserverHost)

		mapping := &models.MappingRequest{
			Number:     mainNum,
			MatrixID:   matrixID,
			SubNumbers: subNums,
		}
		mappings = append(mappings, mapping)

		logger.Debug().Int("number", mainNum).Str("matrix_id", matrixID).Ints("sub_numbers", subNums).Msg("authclient: added mapping from chat response")
	}

	return mappings, true, nil
}
