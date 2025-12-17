package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/nethesis/matrix2acrobits/models"
	"github.com/stretchr/testify/require"
)

// createTestJWT creates a JWT token with the specified claims for testing
func createTestJWT(nethvoiceCTIChat bool) string {
	claims := jwt.MapClaims{
		"nethvoice_cti.chat": nethvoiceCTIChat,
		"exp":                time.Now().Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	// We sign with a key but don't verify in tests since our code doesn't verify signature
	tokenString, _ := token.SignedString([]byte("test-secret"))
	return tokenString
}

func TestHTTPAuthClient_SuccessfulTwoStepAuth(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/login" && r.Method == "POST" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			resp := models.LoginResponse{Token: createTestJWT(true)}
			json.NewEncoder(w).Encode(resp)
		} else if r.URL.Path == "/api/chat" && r.Method == "GET" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			resp := models.ChatResponse{
				Matrix: models.ChatMatrixConfig{
					BaseURL:     "https://matrix.example.com",
					AcrobitsURL: "https://matrix.example.com/m2a",
				},
				Users: []models.ChatUser{
					{
						UserName:      "giacomo",
						MainExtension: "201",
						SubExtensions: []string{"91201", "92201"},
					},
					{
						UserName:      "mario",
						MainExtension: "202",
						SubExtensions: []string{"91202"},
					},
				},
			}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer ts.Close()

	c := NewHTTPAuthClient(ts.URL, 2*time.Second, 0)
	mappings, ok, err := c.Validate(context.TODO(), "giacomo@example.com", "secret", "example.com")
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, mappings, 2)

	// Find giacomo
	var giacomoMapping *models.MappingRequest
	for _, m := range mappings {
		if m.Number == 201 {
			giacomoMapping = m
			break
		}
	}
	require.NotNil(t, giacomoMapping)
	require.Equal(t, 201, giacomoMapping.Number)
	require.Equal(t, "@giacomo:example.com", giacomoMapping.MatrixID)
	require.Equal(t, []int{91201, 92201}, giacomoMapping.SubNumbers)
}

func TestHTTPAuthClient_PlainUsername(t *testing.T) {
	// Tests that the client correctly sends the plain username (no extraction needed)
	// Any username extraction should be done by the caller before calling Validate
	loginCalled := false
	var capturedUsername string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/login" && r.Method == "POST" {
			loginCalled = true
			var req models.LoginRequest
			json.NewDecoder(r.Body).Decode(&req)
			capturedUsername = req.Username
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			resp := models.LoginResponse{Token: createTestJWT(true)}
			json.NewEncoder(w).Encode(resp)
		} else if r.URL.Path == "/api/chat" && r.Method == "GET" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			resp := models.ChatResponse{
				Users: []models.ChatUser{
					{UserName: "giacomo", MainExtension: "201", SubExtensions: []string{}},
				},
			}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer ts.Close()

	c := NewHTTPAuthClient(ts.URL, 2*time.Second, 0)
	// Pass the plain username directly - no extraction happens in Validate
	_, ok, err := c.Validate(context.TODO(), "giacomo", "secret", "example.com")
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, loginCalled)
	require.Equal(t, "giacomo", capturedUsername)
}

func TestHTTPAuthClient_MissingChatClaim(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/login" && r.Method == "POST" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			// JWT without nethvoice_cti.chat claim
			resp := models.LoginResponse{Token: createTestJWT(false)}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer ts.Close()

	c := NewHTTPAuthClient(ts.URL, 2*time.Second, 0)
	_, ok, err := c.Validate(context.TODO(), "user@example.com", "secret", "example.com")
	require.Error(t, err)
	require.False(t, ok)
}

func TestHTTPAuthClient_LoginFails(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/login" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte("invalid credentials"))
		}
	}))
	defer ts.Close()

	c := NewHTTPAuthClient(ts.URL, 2*time.Second, 0)
	_, ok, err := c.Validate(context.TODO(), "user@example.com", "wrongsecret", "example.com")
	require.Error(t, err)
	require.False(t, ok)
}

func TestHTTPAuthClient_ChatFails(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/login" && r.Method == "POST" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			resp := models.LoginResponse{Token: createTestJWT(true)}
			json.NewEncoder(w).Encode(resp)
		} else if r.URL.Path == "/api/chat" && r.Method == "GET" {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("server error"))
		}
	}))
	defer ts.Close()

	c := NewHTTPAuthClient(ts.URL, 2*time.Second, 0)
	_, ok, err := c.Validate(context.TODO(), "user@example.com", "secret", "example.com")
	require.Error(t, err)
	require.False(t, ok)
}

func TestHTTPAuthClient_CacheHit(t *testing.T) {
	callCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if r.URL.Path == "/api/login" && r.Method == "POST" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			resp := models.LoginResponse{Token: createTestJWT(true)}
			json.NewEncoder(w).Encode(resp)
		} else if r.URL.Path == "/api/chat" && r.Method == "GET" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			resp := models.ChatResponse{
				Users: []models.ChatUser{
					{UserName: "giacomo", MainExtension: "201", SubExtensions: []string{}},
				},
			}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer ts.Close()

	c := NewHTTPAuthClient(ts.URL, 2*time.Second, 100*time.Millisecond)

	// First call should make requests
	mappings1, ok1, err1 := c.Validate(context.TODO(), "giacomo@example.com", "secret", "example.com")
	require.NoError(t, err1)
	require.True(t, ok1)
	require.Len(t, mappings1, 1)
	require.Equal(t, 2, callCount) // 1 login + 1 chat

	// Second call should use cache and return empty mappings
	mappings2, ok2, err2 := c.Validate(context.TODO(), "giacomo@example.com", "secret", "example.com")
	require.NoError(t, err2)
	require.True(t, ok2)
	require.Len(t, mappings2, 0)   // Cache returns empty array
	require.Equal(t, 2, callCount) // No additional calls
}

func TestHTTPAuthClient_InvalidMainExtension(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/login" && r.Method == "POST" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			resp := models.LoginResponse{Token: createTestJWT(true)}
			json.NewEncoder(w).Encode(resp)
		} else if r.URL.Path == "/api/chat" && r.Method == "GET" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			resp := models.ChatResponse{
				Users: []models.ChatUser{
					{UserName: "giacomo", MainExtension: "201", SubExtensions: []string{}},
					{UserName: "alice", MainExtension: "invalid", SubExtensions: []string{}},
				},
			}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer ts.Close()

	c := NewHTTPAuthClient(ts.URL, 2*time.Second, 0)
	mappings, ok, err := c.Validate(context.TODO(), "giacomo@example.com", "secret", "example.com")
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, mappings, 1) // Only valid mapping
	require.Equal(t, 201, mappings[0].Number)
}

func TestHTTPAuthClient_EmptyUsersList(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/login" && r.Method == "POST" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			resp := models.LoginResponse{Token: createTestJWT(true)}
			json.NewEncoder(w).Encode(resp)
		} else if r.URL.Path == "/api/chat" && r.Method == "GET" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			resp := models.ChatResponse{Users: []models.ChatUser{}}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer ts.Close()

	c := NewHTTPAuthClient(ts.URL, 2*time.Second, 0)
	mappings, ok, err := c.Validate(context.TODO(), "user@example.com", "secret", "example.com")
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, mappings, 0)
}
