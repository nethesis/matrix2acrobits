package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestAuthenticateWithDex_Success(t *testing.T) {
	if os.Getenv("RUN_INTEGRATION_TESTS") != "" {
		t.Skip("skipping dex auth unit test during integration test run")
	}
	// Start a local test server to mock Dex token endpoint
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("failed to parse form: %v", err)
		}
		if r.FormValue("grant_type") != "password" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.FormValue("username") != "alice" || r.FormValue("password") != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token": "token-abc", "expires_in":3600}`))
	}))
	defer srv.Close()

	os.Setenv("DEX_TOKEN_ENDPOINT", srv.URL)
	os.Setenv("DEX_CLIENT_ID", "test-client")
	os.Unsetenv("DEX_CLIENT_SECRET")

	tok, err := AuthenticateWithDex(context.Background(), "alice", "secret")
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if tok != "token-abc" {
		t.Fatalf("unexpected token: %s", tok)
	}
}

func TestAuthenticateWithDex_InvalidCreds(t *testing.T) {
	if os.Getenv("RUN_INTEGRATION_TESTS") != "" {
		t.Skip("skipping dex auth unit test during integration test run")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()

	os.Setenv("DEX_TOKEN_ENDPOINT", srv.URL)
	os.Setenv("DEX_CLIENT_ID", "test-client")

	_, err := AuthenticateWithDex(context.Background(), "alice", "wrong")
	if err == nil {
		t.Fatal("expected error for invalid credentials")
	}
}
