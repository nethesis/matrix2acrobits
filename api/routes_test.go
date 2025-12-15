package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/nethesis/matrix2acrobits/db"
	"github.com/nethesis/matrix2acrobits/models"
	"github.com/nethesis/matrix2acrobits/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsLocalhost(t *testing.T) {
	h := handler{}
	tests := []struct {
		name     string
		ip       string
		expected bool
	}{
		{"127.0.0.1", "127.0.0.1", true},
		{"127.0.0.1 with port", "127.0.0.1:8080", true},
		{"localhost", "localhost", true},
		{"localhost with port", "localhost:8080", true},
		{"Remote IP", "192.168.1.1", false},
		{"Remote IP with port", "192.168.1.1:8080", false},
		{"IPv4 different", "10.0.0.1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := h.isLocalhost(tt.ip)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPushTokenReport(t *testing.T) {
	e := echo.New()
	svc := service.NewMessageService(nil, nil, service.NewTestConfig())

	t.Run("valid push token report", func(t *testing.T) {
		reqBody := models.PushTokenReportRequest{
			Selector:   "12869E0E6E553673C54F29105A0647204C416A2A:7C3A0D14",
			TokenMsgs:  "QVBBOTFiRzlhcVd2bW54bllCWldHOWh4dnRrZ3pUWFNvcGZpdWZ6bWM2dFAzS2J",
			AppIDMsgs:  "com.cloudsoftphone.app",
			TokenCalls: "Udl99X2JFP1bWwS5gR/wGeLE1hmAB2CMpr1Ej0wxkrY=",
			AppIDCalls: "com.cloudsoftphone.app.pushkit",
		}

		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPost, "/api/client/push_token_report", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		c := e.NewContext(req, rec)

		h := handler{svc: svc, adminToken: "test"}
		err := h.pushTokenReport(c)

		// Since we don't have a real database, this will fail with "database not initialized"
		// but we can verify the handler processes the request correctly
		assert.Error(t, err)
	})

	t.Run("invalid json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/client/push_token_report", bytes.NewBufferString("invalid json"))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		c := e.NewContext(req, rec)

		h := handler{svc: svc, adminToken: "test"}
		err := h.pushTokenReport(c)

		// Should return a bind error
		assert.Error(t, err)
	})

	t.Run("empty selector", func(t *testing.T) {
		reqBody := models.PushTokenReportRequest{
			Selector: "",
		}

		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPost, "/api/client/push_token_report", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		c := e.NewContext(req, rec)

		h := handler{svc: svc, adminToken: "test"}
		err := h.pushTokenReport(c)

		assert.Error(t, err)
	})
}

func TestGetPushTokens(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test_routes_*.db")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	pushTokenDB, err := db.NewDatabase(tmpFile.Name())
	require.NoError(t, err)
	defer pushTokenDB.Close()

	// Insert test data
	err = pushTokenDB.SavePushToken(
		"selector1",
		"token_msgs_1",
		"app_msgs_1",
		"token_calls_1",
		"app_calls_1",
	)
	require.NoError(t, err)

	err = pushTokenDB.SavePushToken(
		"selector2",
		"token_msgs_2",
		"app_msgs_2",
		"token_calls_2",
		"app_calls_2",
	)
	require.NoError(t, err)

	e := echo.New()
	svc := service.NewMessageService(nil, pushTokenDB, service.NewTestConfig())

	t.Run("get all push tokens with valid token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/internal/push_tokens", nil)
		req.Header.Set("X-Super-Admin-Token", "test-admin-token")
		rec := httptest.NewRecorder()

		// Mock localhost IP
		c := e.NewContext(req, rec)
		c.SetRequest(c.Request().WithContext(c.Request().Context()))
		c.Request().RemoteAddr = "127.0.0.1:12345"

		h := handler{svc: svc, adminToken: "test-admin-token", pushTokenDB: pushTokenDB}
		err := h.getPushTokens(c)

		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var tokens []*db.PushToken
		err = json.Unmarshal(rec.Body.Bytes(), &tokens)
		assert.NoError(t, err)
		assert.Equal(t, 2, len(tokens))

		// Results are ordered by updated_at DESC, so check both selectors are present
		selectors := map[string]bool{}
		for _, token := range tokens {
			selectors[token.Selector] = true
		}
		assert.True(t, selectors["selector1"])
		assert.True(t, selectors["selector2"])
	})

	t.Run("get push tokens without admin token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/internal/push_tokens", nil)
		// Missing X-Super-Admin-Token header
		rec := httptest.NewRecorder()

		c := e.NewContext(req, rec)
		c.Request().RemoteAddr = "127.0.0.1:12345"

		h := handler{svc: svc, adminToken: "test-admin-token", pushTokenDB: pushTokenDB}
		err := h.getPushTokens(c)

		assert.Error(t, err)
		// Should be an echo HTTPError with 401 status
		echoErr, ok := err.(*echo.HTTPError)
		assert.True(t, ok)
		assert.Equal(t, http.StatusUnauthorized, echoErr.Code)
	})

	t.Run("get push tokens with invalid admin token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/internal/push_tokens", nil)
		req.Header.Set("X-Super-Admin-Token", "wrong-token")
		rec := httptest.NewRecorder()

		c := e.NewContext(req, rec)
		c.Request().RemoteAddr = "127.0.0.1:12345"

		h := handler{svc: svc, adminToken: "test-admin-token", pushTokenDB: pushTokenDB}
		err := h.getPushTokens(c)

		assert.Error(t, err)
		echoErr, ok := err.(*echo.HTTPError)
		assert.True(t, ok)
		assert.Equal(t, http.StatusUnauthorized, echoErr.Code)
	})

	t.Run("get push tokens from non-localhost", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/internal/push_tokens", nil)
		req.Header.Set("X-Super-Admin-Token", "test-admin-token")
		rec := httptest.NewRecorder()

		c := e.NewContext(req, rec)
		c.Request().RemoteAddr = "192.168.1.1:12345"

		h := handler{svc: svc, adminToken: "test-admin-token", pushTokenDB: pushTokenDB}
		err := h.getPushTokens(c)

		assert.Error(t, err)
		echoErr, ok := err.(*echo.HTTPError)
		assert.True(t, ok)
		assert.Equal(t, http.StatusForbidden, echoErr.Code)
	})

	t.Run("get push tokens with empty database", func(t *testing.T) {
		tmpFile2, err := os.CreateTemp("", "test_routes_empty_*.db")
		require.NoError(t, err)
		defer os.Remove(tmpFile2.Name())

		emptyDB, err := db.NewDatabase(tmpFile2.Name())
		require.NoError(t, err)
		defer emptyDB.Close()

		req := httptest.NewRequest(http.MethodGet, "/api/internal/push_tokens", nil)
		req.Header.Set("X-Super-Admin-Token", "test-admin-token")
		rec := httptest.NewRecorder()

		c := e.NewContext(req, rec)
		c.Request().RemoteAddr = "127.0.0.1:12345"

		h := handler{svc: svc, adminToken: "test-admin-token", pushTokenDB: emptyDB}
		err = h.getPushTokens(c)

		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var tokens []*db.PushToken
		err = json.Unmarshal(rec.Body.Bytes(), &tokens)
		assert.NoError(t, err)
		assert.Equal(t, 0, len(tokens))
	})

	t.Run("get push tokens with nil database", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/internal/push_tokens", nil)
		req.Header.Set("X-Super-Admin-Token", "test-admin-token")
		rec := httptest.NewRecorder()

		c := e.NewContext(req, rec)
		c.Request().RemoteAddr = "127.0.0.1:12345"

		h := handler{svc: svc, adminToken: "test-admin-token", pushTokenDB: nil}
		err := h.getPushTokens(c)

		assert.Error(t, err)
		echoErr, ok := err.(*echo.HTTPError)
		assert.True(t, ok)
		assert.Equal(t, http.StatusInternalServerError, echoErr.Code)
	})
}

func TestResetPushTokens(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test_reset_routes_*.db")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	pushTokenDB, err := db.NewDatabase(tmpFile.Name())
	require.NoError(t, err)
	defer pushTokenDB.Close()

	// Insert test data
	err = pushTokenDB.SavePushToken("selector1", "token1", "app1", "token_call1", "app_call1")
	require.NoError(t, err)
	err = pushTokenDB.SavePushToken("selector2", "token2", "app2", "token_call2", "app_call2")
	require.NoError(t, err)

	e := echo.New()
	svc := service.NewMessageService(nil, pushTokenDB, service.NewTestConfig())

	t.Run("reset push tokens with valid token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/internal/push_tokens", nil)
		req.Header.Set("X-Super-Admin-Token", "test-admin-token")
		rec := httptest.NewRecorder()

		c := e.NewContext(req, rec)
		c.Request().RemoteAddr = "127.0.0.1:12345"

		h := handler{svc: svc, adminToken: "test-admin-token", pushTokenDB: pushTokenDB}
		err := h.resetPushTokens(c)

		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var resp map[string]string
		err = json.Unmarshal(rec.Body.Bytes(), &resp)
		assert.NoError(t, err)
		assert.Equal(t, "reset", resp["status"])

		// Verify database is empty
		tokens, err := pushTokenDB.ListPushTokens()
		assert.NoError(t, err)
		assert.Equal(t, 0, len(tokens))
	})

	t.Run("reset push tokens without admin token", func(t *testing.T) {
		// Re-insert data for this test
		err := pushTokenDB.SavePushToken("selector3", "token3", "app3", "token_call3", "app_call3")
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodDelete, "/api/internal/push_tokens", nil)
		// Missing X-Super-Admin-Token header
		rec := httptest.NewRecorder()

		c := e.NewContext(req, rec)
		c.Request().RemoteAddr = "127.0.0.1:12345"

		h := handler{svc: svc, adminToken: "test-admin-token", pushTokenDB: pushTokenDB}
		err = h.resetPushTokens(c)

		assert.Error(t, err)
		echoErr, ok := err.(*echo.HTTPError)
		assert.True(t, ok)
		assert.Equal(t, http.StatusUnauthorized, echoErr.Code)

		// Verify database was not reset
		tokens, err := pushTokenDB.ListPushTokens()
		assert.NoError(t, err)
		assert.Equal(t, 1, len(tokens))
	})

	t.Run("reset push tokens with invalid admin token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/internal/push_tokens", nil)
		req.Header.Set("X-Super-Admin-Token", "wrong-token")
		rec := httptest.NewRecorder()

		c := e.NewContext(req, rec)
		c.Request().RemoteAddr = "127.0.0.1:12345"

		h := handler{svc: svc, adminToken: "test-admin-token", pushTokenDB: pushTokenDB}
		err := h.resetPushTokens(c)

		assert.Error(t, err)
		echoErr, ok := err.(*echo.HTTPError)
		assert.True(t, ok)
		assert.Equal(t, http.StatusUnauthorized, echoErr.Code)
	})

	t.Run("reset push tokens from non-localhost", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/internal/push_tokens", nil)
		req.Header.Set("X-Super-Admin-Token", "test-admin-token")
		rec := httptest.NewRecorder()

		c := e.NewContext(req, rec)
		c.Request().RemoteAddr = "192.168.1.1:12345"

		h := handler{svc: svc, adminToken: "test-admin-token", pushTokenDB: pushTokenDB}
		err := h.resetPushTokens(c)

		assert.Error(t, err)
		echoErr, ok := err.(*echo.HTTPError)
		assert.True(t, ok)
		assert.Equal(t, http.StatusForbidden, echoErr.Code)
	})

	t.Run("reset push tokens with nil database", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/internal/push_tokens", nil)
		req.Header.Set("X-Super-Admin-Token", "test-admin-token")
		rec := httptest.NewRecorder()

		c := e.NewContext(req, rec)
		c.Request().RemoteAddr = "127.0.0.1:12345"

		h := handler{svc: svc, adminToken: "test-admin-token", pushTokenDB: nil}
		err := h.resetPushTokens(c)

		assert.Error(t, err)
		echoErr, ok := err.(*echo.HTTPError)
		assert.True(t, ok)
		assert.Equal(t, http.StatusInternalServerError, echoErr.Code)
	})
}
