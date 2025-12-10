package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nethesis/matrix2acrobits/models"
	"github.com/stretchr/testify/require"
)

func TestHTTPAuthClient_Non200Response(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("server error"))
	}))
	defer ts.Close()

	c := NewHTTPAuthClient(ts.URL, 2*time.Second, 0)
	_, ok, err := c.Validate(context.TODO(), "123", "secret", "example.com")
	require.Error(t, err)
	require.False(t, ok)
}

func TestHTTPAuthClient_InvalidMainExtension(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Return an array with one valid and one invalid entry
		_, _ = w.Write([]byte(`[
			{"main_extension":"201","sub_extensions":[],"user_name":"giacomo"},
			{"main_extension":"not-a-number","sub_extensions":[],"user_name":"alice"}
		]`))
	}))
	defer ts.Close()

	c := NewHTTPAuthClient(ts.URL, 2*time.Second, 0)
	// Request the invalid extension - the client returns parsed mappings from the
	// response (it does not filter by the requested extension). Expect no error
	// and the valid mapping to be present.
	mappings, ok, err := c.Validate(context.TODO(), "not-a-number", "secret", "example.com")
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, mappings, 1)
	require.Equal(t, 201, mappings[0].Number)
}

func TestHTTPAuthClient_MissingHomeserverHost(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// The server returns entries with localpart user_name (no @domain)
		// When we have no homeserverHost, we should skip these
		_, _ = w.Write([]byte(`[{"main_extension":"1","sub_extensions":[],"user_name":"alice"}]`))
	}))
	defer ts.Close()

	c := NewHTTPAuthClient(ts.URL, 2*time.Second, 0)
	// Request extension 1 with no homeserver host configured. The client will
	// still return parsed mappings but the Matrix ID will include an empty
	// homeserver (trailing colon).
	mappings, ok, err := c.Validate(context.TODO(), "1", "secret", "")
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, mappings, 1)
	require.Equal(t, "@alice:", mappings[0].MatrixID)
}

func TestHTTPAuthClient_MatchingExtensionFromArray(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[
			{"main_extension":"201","sub_extensions":["91201","92201"],"user_name":"giacomo"},
			{"main_extension":"202","sub_extensions":["91202"],"user_name":"mario"}
		]`))
	}))
	defer ts.Close()

	c := NewHTTPAuthClient(ts.URL, 2*time.Second, 0)
	mappings, ok, err := c.Validate(context.TODO(), "202", "secret", "example.com")
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, mappings, 2)

	// Find the mario entry (202)
	var marioMapping *models.MappingRequest
	for _, m := range mappings {
		if m.Number == 202 {
			marioMapping = m
			break
		}
	}
	require.NotNil(t, marioMapping)
	require.Equal(t, 202, marioMapping.Number)
	require.Equal(t, "@mario:example.com", marioMapping.MatrixID)
	require.Equal(t, []int{91202}, marioMapping.SubNumbers)
}

func TestHTTPAuthClient_ExtensionNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[
			{"main_extension":"201","sub_extensions":["91201","92201"],"user_name":"giacomo"},
			{"main_extension":"202","sub_extensions":["91202"],"user_name":"mario"}
		]`))
	}))
	defer ts.Close()

	c := NewHTTPAuthClient(ts.URL, 2*time.Second, 0)
	// Request a non-existing extension - the client still returns all parsed
	// mappings from the auth response when the HTTP call succeeds.
	mappings, ok, err := c.Validate(context.TODO(), "999", "secret", "example.com")
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, mappings, 2)
}

func TestHTTPAuthClient_CacheHitReturnsEmptyMappings(t *testing.T) {
	callCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[
			{"main_extension":"201","sub_extensions":["91201"],"user_name":"giacomo"},
			{"main_extension":"202","sub_extensions":["91202"],"user_name":"mario"}
		]`))
	}))
	defer ts.Close()

	c := NewHTTPAuthClient(ts.URL, 2*time.Second, 100*time.Millisecond)

	// First call should make request and return all mappings
	mappings1, ok1, err1 := c.Validate(context.TODO(), "202", "secret", "example.com")
	require.NoError(t, err1)
	require.True(t, ok1)
	require.Len(t, mappings1, 2)
	require.Equal(t, 1, callCount)

	// Second call should use cache and return empty mappings
	mappings2, ok2, err2 := c.Validate(context.TODO(), "202", "secret", "example.com")
	require.NoError(t, err2)
	require.True(t, ok2)
	require.Len(t, mappings2, 0)   // Cache returns empty array
	require.Equal(t, 1, callCount) // No additional call
}

func TestHTTPAuthClient_CacheFailedAuth(t *testing.T) {
	callCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("unauthorized"))
	}))
	defer ts.Close()

	c := NewHTTPAuthClient(ts.URL, 2*time.Second, 100*time.Millisecond)

	// First call should make request and fail
	mappings1, ok1, err1 := c.Validate(context.TODO(), "999", "wrongsecret", "example.com")
	require.Error(t, err1)
	require.False(t, ok1)
	require.Empty(t, mappings1)
	require.Equal(t, 1, callCount)

	// Second call should NOT use cache (failed auth not cached) and make new request
	mappings2, ok2, err2 := c.Validate(context.TODO(), "999", "wrongsecret", "example.com")
	require.Error(t, err2)
	require.False(t, ok2)
	require.Empty(t, mappings2)
	require.Equal(t, 2, callCount) // Additional call made
}
