package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nethesis/matrix2acrobits/logger"
	"github.com/nethesis/matrix2acrobits/models"
)

// AuthResponse represents the JSON returned by the external testextauth endpoint.
type AuthResponse struct {
	MainExtension string   `json:"main_extension"`
	SubExtensions []string `json:"sub_extensions"`
	UserName      string   `json:"user_name"`
}

// AuthClient abstracts the external authentication call so it can be mocked in tests.
// Validate returns all MappingRequests built from the auth response array.
type AuthClient interface {
	Validate(ctx context.Context, extension, secret, homeserverHost string) ([]*models.MappingRequest, bool, error)
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

// Validate calls the configured external auth endpoint and converts its result
// into an array of models.MappingRequest. It caches authentication success/failure.
// homeserverHost is used to build full Matrix IDs when the returned user_name is a localpart.
func (h *HTTPAuthClient) Validate(ctx context.Context, extension, secret, homeserverHost string) ([]*models.MappingRequest, bool, error) {
	// check cache
	key := extension + "|" + secret + "|" + homeserverHost
	logger.Debug().Str("key", key).Msg("authclient: validate called")
	if h.cacheTTL > 0 {
		h.mu.RLock()
		if c, ok := h.cache[key]; ok {
			if time.Now().Before(c.expiry) {
				h.mu.RUnlock()
				logger.Debug().Str("key", key).Time("expiry", c.expiry).Msg("authclient: cache hit")
				// Return empty array on cache hit for authenticated requests
				return []*models.MappingRequest{}, true, nil
			}
		}
		h.mu.RUnlock()
		logger.Debug().Str("key", key).Msg("authclient: cache miss or expired")
	}

	payload := map[string]string{"extension": extension, "secret": secret}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", h.url, bytes.NewReader(body))
	if err != nil {
		return []*models.MappingRequest{}, false, err
	}
	req.Header.Set("Content-Type", "application/json")

	logger.Debug().Str("url", h.url).Str("extension", extension).Msg("authclient: sending auth request")

	resp, err := h.client.Do(req)
	if err != nil {
		return []*models.MappingRequest{}, false, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	logger.Debug().Int("status", resp.StatusCode).Msg("authclient: received response")
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		logger.Debug().Int("status", resp.StatusCode).Bytes("body", b).Msg("authclient: non-200 response")
		return []*models.MappingRequest{}, false, fmt.Errorf("status %d: %s", resp.StatusCode, string(b))
	}

	var responses []AuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&responses); err != nil {
		logger.Debug().Err(err).Msg("authclient: failed to decode response")
		return []*models.MappingRequest{}, false, err
	}

	logger.Debug().Int("response_count", len(responses)).Msg("authclient: parsed auth response array")

	// Cache successful authentication (without mapping data)
	if h.cacheTTL > 0 {
		h.mu.Lock()
		h.cache[key] = cachedAuth{expiry: time.Now().Add(h.cacheTTL)}
		h.mu.Unlock()
		logger.Debug().Str("key", key).Time("expiry", time.Now().Add(h.cacheTTL)).Msg("authclient: cached successful authentication")
	}

	mappings := make([]*models.MappingRequest, 0, len(responses))
	var mainNum int
	var errNum error
	for _, ar := range responses {
		logger.Debug().Str("main_extension", ar.MainExtension).Strs("sub_extensions", ar.SubExtensions).Str("user_name", ar.UserName).Msg("authclient: processing auth response")

		// Validate main_extension exists and is a number
		mainExtStr := strings.TrimSpace(ar.MainExtension)
		if mainExtStr == "" {
			logger.Warn().Msg("authclient: response has empty main_extension, skipping")
			continue
		}
		if mainNum, errNum = strconv.Atoi(mainExtStr); errNum != nil {
			logger.Warn().Str("main_extension", mainExtStr).Err(errNum).Msg("authclient: main_extension is not a valid number, skipping")
			continue
		}

		// Parse sub extensions
		subNums := make([]int, 0, len(ar.SubExtensions))
		for _, ssub := range ar.SubExtensions {
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
		if ar.UserName == "" {
			logger.Warn().Msg("authclient: response has empty user_name, skipping")
			continue
		}
		matrixID := fmt.Sprintf("@%s:%s", strings.ToLower(strings.TrimSpace(ar.UserName)), homeserverHost)

		mapping := &models.MappingRequest{
			Number:     mainNum,
			MatrixID:   matrixID,
			SubNumbers: subNums,
		}
		mappings = append(mappings, mapping)

		logger.Debug().Int("number", mainNum).Str("matrix_id", matrixID).Ints("sub_numbers", subNums).Msg("authclient: added mapping from response")
	}

	return mappings, true, nil
}
