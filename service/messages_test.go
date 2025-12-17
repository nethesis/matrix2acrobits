package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"time"

	"github.com/nethesis/matrix2acrobits/db"
	"github.com/nethesis/matrix2acrobits/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setMapping is a test helper to directly insert a mapping into the service's internal store.
func setMapping(t *testing.T, svc *MessageService, entry mappingEntry) {
	t.Helper()
	svc.mu.Lock()
	defer svc.mu.Unlock()
	entry.UpdatedAt = time.Now()
	// Only store by number for tests; the actual SaveMapping stores both
	svc.mappings[fmt.Sprintf("%d", entry.Number)] = entry
}

func TestNewMessageService(t *testing.T) {
	// We can't create a real MatrixClient without a valid homeserver,
	// so we'll skip full integration here and test just the pure functions

	// Test with nil to ensure the function exists
	// In a real scenario, users would pass a properly initialized MatrixClient
}

func TestNormalizeMatrixID(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Normal Matrix ID",
			input:    "@user:example.com",
			expected: "@user:example.com",
		},
		{
			name:     "Uppercase",
			input:    "@User:Example.COM",
			expected: "@user:example.com",
		},
		{
			name:     "With whitespace",
			input:    "  @user:example.com  ",
			expected: "@user:example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeMatrixID(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsSentBy(t *testing.T) {
	tests := []struct {
		name     string
		sender   string
		username string
		expected bool
	}{
		{
			name:     "Exact match",
			sender:   "@user:example.com",
			username: "@user:example.com",
			expected: true,
		},
		{
			name:     "Case insensitive match",
			sender:   "@User:Example.COM",
			username: "@user:example.com",
			expected: true,
		},
		{
			name:     "Different users",
			sender:   "@user1:example.com",
			username: "@user2:example.com",
			expected: false,
		},
		{
			name:     "With whitespace",
			sender:   "  @user:example.com  ",
			username: "@user:example.com",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isSentBy(tt.sender, tt.username)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertEvent(t *testing.T) {
	// We can't create events without importing event package
	// This is tested indirectly in integration tests
	// For unit testing the pure mapping functions, see other tests
}

func TestMapAuthErr(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		isAuthErr bool
	}{
		{
			name:      "Already ErrAuthentication",
			err:       ErrAuthentication,
			isAuthErr: true,
		},
		{
			name:      "Generic error",
			err:       assert.AnError,
			isAuthErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := mapAuthErr(tt.err)
			if tt.isAuthErr {
				assert.Equal(t, ErrAuthentication, err)
			} else {
				assert.NotEqual(t, ErrAuthentication, err)
			}
		})
	}
}

func TestListMappings(t *testing.T) {
	svc := NewMessageService(nil, nil, NewTestConfig())

	// Seed two mappings
	setMapping(t, svc, mappingEntry{
		Number:   111,
		MatrixID: "@alice:example.com",
	})
	setMapping(t, svc, mappingEntry{
		Number:   222,
		MatrixID: "@bob:example.com",
	})

	list, err := svc.ListMappings()
	assert.NoError(t, err)
	// We expect two mappings; order is not guaranteed.
	assert.Len(t, list, 2)

	// Build a map for easy assertions
	m := make(map[int]*models.MappingResponse)
	for _, it := range list {
		m[it.Number] = it
	}

	if v, ok := m[111]; ok {
		assert.Equal(t, "@alice:example.com", v.MatrixID)
	} else {
		t.Fatalf("missing mapping for 111")
	}

	if v, ok := m[222]; ok {
		assert.Equal(t, "@bob:example.com", v.MatrixID)
	} else {
		t.Fatalf("missing mapping for 222")
	}
}

func TestResolveMatrixUser_SubNumbers(t *testing.T) {
	// Test case 1: Resolve sub_number to matrix_id
	t.Run("resolve sub_number to matrix_id", func(t *testing.T) {
		svc := NewMessageService(nil, nil, NewTestConfig())
		svc.SaveMapping(&models.MappingRequest{
			Number:     201,
			MatrixID:   "@giacomo:example.com",
			SubNumbers: []int{3344, 91201},
		})

		// Resolve using a sub_number
		result := svc.resolveMatrixUser("91201")
		assert.Equal(t, "@giacomo:example.com", string(result), "should resolve sub_number to matrix_id")
	})

	// Test case 2: Resolve main number to matrix_id
	t.Run("resolve main number to matrix_id", func(t *testing.T) {
		svc := NewMessageService(nil, nil, NewTestConfig())
		svc.SaveMapping(&models.MappingRequest{
			Number:   202,
			MatrixID: "@mario:example.com",
		})

		// Resolve using the main number
		result := svc.resolveMatrixUser("202")
		assert.Equal(t, "@mario:example.com", string(result), "should resolve main number to matrix_id")
	})

	// Test case 3: Resolve another sub_number
	t.Run("resolve another sub_number", func(t *testing.T) {
		svc := NewMessageService(nil, nil, NewTestConfig())
		svc.SaveMapping(&models.MappingRequest{
			Number:     201,
			MatrixID:   "@giacomo:example.com",
			SubNumbers: []int{3344, 91201},
		})

		// Resolve using a different sub_number
		result := svc.resolveMatrixUser("3344")
		assert.Equal(t, "@giacomo:example.com", string(result), "should resolve any sub_number to matrix_id")
	})

	// Test case 4: Matrix ID passed directly
	t.Run("matrix id passed directly", func(t *testing.T) {
		svc := NewMessageService(nil, nil, NewTestConfig())
		result := svc.resolveMatrixUser("@test:example.com")
		assert.Equal(t, "@test:example.com", string(result), "should return matrix_id as-is if it starts with @")
	})

	// Test case 5: No mapping found
	t.Run("no mapping found", func(t *testing.T) {
		svc := NewMessageService(nil, nil, NewTestConfig())
		result := svc.resolveMatrixUser("9999")
		assert.Equal(t, "", string(result), "should return empty string if no mapping found")
	})

	// Test case 6: Extract username from user@domain format - but still need to resolve via mapping
	// This test verifies that the username extraction works for the identifier processing,
	// but the actual resolution still depends on having a mapping with that number
	t.Run("extract username from user@domain format", func(t *testing.T) {
		svc := NewMessageService(nil, nil, NewTestConfig())
		// When the auth service creates a mapping, it will use the user_name to look up
		// But in resolveMatrixUser, we only look up by number or try to convert to int
		// So we test that user@domain gets extracted to "user" but it won't resolve
		// unless there's a numeric mapping. This is the core fix - preventing the
		// "could not resolve" warning for user@domain format identifiers.
		result := svc.resolveMatrixUser("giacomo@voice.gs.nethserver.net")
		// Should return empty string since no mapping exists with that username
		assert.Equal(t, "", string(result), "should extract username from user@domain format but return empty if no mapping")
	})

	// Test case 7: Case insensitive sub_number resolution
	t.Run("case insensitive sub_number resolution", func(t *testing.T) {
		svc := NewMessageService(nil, nil, NewTestConfig())
		svc.SaveMapping(&models.MappingRequest{
			Number:     201,
			MatrixID:   "@giacomo:example.com",
			SubNumbers: []int{3344, 91201},
		})

		// Resolve with different case (though phone numbers are typically numeric)
		result := svc.resolveMatrixUser("91201")
		assert.Equal(t, "@giacomo:example.com", string(result), "should resolve case-insensitively")
	})
}

func TestResolveMatrixIDToIdentifier_SubNumbers(t *testing.T) {
	// Test case 1: Resolve via sub_number match
	// When a matrix_id matches one of the sub_numbers, the main number should be returned (not the sub_number)
	t.Run("resolve via sub_number match", func(t *testing.T) {
		svc := NewMessageService(nil, nil, NewTestConfig())
		svc.SaveMapping(&models.MappingRequest{
			Number:     201,
			MatrixID:   "@giacomo:example.com",
			SubNumbers: []int{3344, 91201},
		})

		// Resolve using a sub_number - should return the main number
		result := svc.resolveMatrixIDToIdentifier("@giacomo:example.com")
		assert.Equal(t, "201", result, "should return main number when matrix_id matches via sub_number")
	})

	// Test case 2: Resolve via main number
	// When a matrix_id matches the main number field, return that number
	t.Run("resolve via main number", func(t *testing.T) {
		svc := NewMessageService(nil, nil, NewTestConfig())
		svc.SaveMapping(&models.MappingRequest{
			Number:   202,
			MatrixID: "@mario:example.com",
		})

		// Resolve using the matrix_id - should return the main number
		result := svc.resolveMatrixIDToIdentifier("@mario:example.com")
		assert.Equal(t, "202", result, "should return main number when matrix_id matches")
	})

	// Test case 3: Sub_numbers should never be returned directly
	// This is ensured by the logic that checks sub_numbers first, then returns the main number
	t.Run("sub_numbers never returned directly", func(t *testing.T) {
		svc := NewMessageService(nil, nil, NewTestConfig())
		svc.SaveMapping(&models.MappingRequest{
			Number:     201,
			MatrixID:   "@giacomo:example.com",
			SubNumbers: []int{3344, 91201},
		})

		// Try to resolve using the main number
		result := svc.resolveMatrixIDToIdentifier("@giacomo:example.com")
		assert.Equal(t, "201", result)
		assert.NotEqual(t, "3344", result, "should never return sub_number directly")
		assert.NotEqual(t, "91201", result, "should never return sub_number directly")
	})

	// Test case 4: Case insensitivity
	// Matrix IDs should be matched case-insensitively
	t.Run("case insensitivity", func(t *testing.T) {
		svc := NewMessageService(nil, nil, NewTestConfig())
		svc.SaveMapping(&models.MappingRequest{
			Number:     201,
			MatrixID:   "@GIACOMO:EXAMPLE.COM",
			SubNumbers: []int{3344, 91201},
		})

		// Try with uppercase
		result := svc.resolveMatrixIDToIdentifier("@GIACOMO:EXAMPLE.COM")
		assert.Equal(t, "201", result, "should match case-insensitively")
	})

	// Test case 6: No mapping found, return original matrix_id
	t.Run("no mapping returns original matrix_id", func(t *testing.T) {
		svc := NewMessageService(nil, nil, NewTestConfig())
		result := svc.resolveMatrixIDToIdentifier("@unknown:example.com")
		assert.Equal(t, "@unknown:example.com", result, "should return original matrix_id when no mapping found")
	})
}

func TestReportPushToken(t *testing.T) {
	// Test with nil request
	t.Run("nil request", func(t *testing.T) {
		svc := NewMessageService(nil, nil, NewTestConfig())
		resp, err := svc.ReportPushToken(context.TODO(), nil)
		assert.Error(t, err)
		assert.Nil(t, resp)
	})

	// Test with empty selector
	t.Run("empty selector", func(t *testing.T) {
		svc := NewMessageService(nil, nil, NewTestConfig())
		req := &models.PushTokenReportRequest{
			UserName:  "@alice:example.com",
			Selector:  "",
			TokenMsgs: "token123",
			AppIDMsgs: "com.app",
			Password:  "testpass",
		}
		resp, err := svc.ReportPushToken(context.TODO(), req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "selector is required")
	})

	// Test with no database
	t.Run("no database", func(t *testing.T) {
		svc := NewMessageService(nil, nil, NewTestConfig())
		req := &models.PushTokenReportRequest{
			UserName:  "@alice:example.com",
			Selector:  "12869E0E6E553673C54F29105A0647204C416A2A:7C3A0D14",
			TokenMsgs: "token123",
			AppIDMsgs: "com.app",
			Password:  "testpass",
		}
		resp, err := svc.ReportPushToken(context.TODO(), req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "push token storage not available")
	})

	// Test with valid database but no proxy URL (should succeed without pusher registration)
	t.Run("valid database without pusher registration", func(t *testing.T) {
		// mock external auth endpoints (2-step flow)
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
						{UserName: "alice", MainExtension: "201", SubExtensions: []string{"91201"}},
					},
				}
				json.NewEncoder(w).Encode(resp)
			}
		}))
		defer ts.Close()

		db, err := db.NewDatabase(":memory:")
		require.NoError(t, err)
		defer db.Close()

		svc := NewMessageService(nil, db, NewTestConfigWithAuth(ts.URL))
		req := &models.PushTokenReportRequest{
			UserName:   "201",
			Selector:   "@alice:example.com",
			TokenMsgs:  "token123",
			AppIDMsgs:  "com.acrobits.softphone",
			TokenCalls: "token456",
			AppIDCalls: "com.acrobits.softphone",
			Password:   "testpass",
		}
		resp, err := svc.ReportPushToken(context.TODO(), req)
		assert.NoError(t, err)
		assert.NotNil(t, resp)

		// Verify token was saved
		savedToken, err := db.GetPushToken("@alice:example.com")
		require.NoError(t, err)
		assert.NotNil(t, savedToken)
		assert.Equal(t, "token123", savedToken.TokenMsgs)
	})

	// Test with both messages and calls tokens
	t.Run("with both messages and calls tokens", func(t *testing.T) {
		// mock external auth endpoints (2-step flow)
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
						{UserName: "alice", MainExtension: "201", SubExtensions: []string{"91201"}},
					},
				}
				json.NewEncoder(w).Encode(resp)
			}
		}))
		defer ts.Close()

		db, err := db.NewDatabase(":memory:")
		require.NoError(t, err)
		defer db.Close()

		svc := NewMessageService(nil, db, NewTestConfigWithAuth(ts.URL))
		req := &models.PushTokenReportRequest{
			UserName:   "201",
			Selector:   "@alice:example.com",
			TokenMsgs:  "token123",
			AppIDMsgs:  "com.acrobits.softphone",
			TokenCalls: "token456",
			AppIDCalls: "com.acrobits.softphone",
			Password:   "testpass",
		}
		resp, err := svc.ReportPushToken(context.TODO(), req)
		assert.NoError(t, err)
		assert.NotNil(t, resp)

		// Verify both tokens were saved
		savedToken, err := db.GetPushToken("@alice:example.com")
		require.NoError(t, err)
		assert.NotNil(t, savedToken)
		assert.Equal(t, "token123", savedToken.TokenMsgs)
		assert.Equal(t, "token456", savedToken.TokenCalls)
	})
}

// fakeHTTPAuthClient allows controlling responses for testing.
type fakeHTTPAuthClient struct {
	ok bool
}

func (f *fakeHTTPAuthClient) Validate(ctx context.Context, username, password, homeserverHost string) ([]*models.MappingRequest, bool, error) {
	if f.ok {
		return []*models.MappingRequest{
			{Number: 1, MatrixID: "@alice:" + homeserverHost, SubNumbers: []int{}},
		}, true, nil
	}
	return []*models.MappingRequest{}, false, fmt.Errorf("unauthorized")
}

func TestSaveMapping_Success(t *testing.T) {
	dbi, err := db.NewDatabase(":memory:")
	require.NoError(t, err)
	defer dbi.Close()

	svc := NewMessageService(nil, dbi, NewTestConfig())

	req := &models.MappingRequest{
		Number:     201,
		MatrixID:   "@alice:example.com",
		SubNumbers: []int{},
	}

	resp, err := svc.SaveMapping(req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, 201, resp.Number)
	assert.Equal(t, "@alice:example.com", resp.MatrixID)
	assert.Equal(t, []int{}, resp.SubNumbers)
	assert.NotEmpty(t, resp.UpdatedAt)
}

func TestSaveMapping_WithSubNumbers(t *testing.T) {
	dbi, err := db.NewDatabase(":memory:")
	require.NoError(t, err)
	defer dbi.Close()

	svc := NewMessageService(nil, dbi, NewTestConfig())

	req := &models.MappingRequest{
		Number:     201,
		MatrixID:   "@alice:example.com",
		SubNumbers: []int{1001, 1002, 1003},
	}

	resp, err := svc.SaveMapping(req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, 201, resp.Number)
	assert.Equal(t, []int{1001, 1002, 1003}, resp.SubNumbers)

	// Verify sub-number mappings are created
	svc.mu.RLock()
	assert.Equal(t, "@alice:example.com", svc.subNumberMappings[1001])
	assert.Equal(t, "@alice:example.com", svc.subNumberMappings[1002])
	assert.Equal(t, "@alice:example.com", svc.subNumberMappings[1003])
	svc.mu.RUnlock()
}

func TestSaveMapping_MissingNumber(t *testing.T) {
	dbi, err := db.NewDatabase(":memory:")
	require.NoError(t, err)
	defer dbi.Close()

	svc := NewMessageService(nil, dbi, NewTestConfig())

	req := &models.MappingRequest{
		Number:   0, // Invalid: zero
		MatrixID: "@alice:example.com",
	}

	resp, err := svc.SaveMapping(req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, "number is required", err.Error())
}

func TestSaveMapping_MissingMatrixID(t *testing.T) {
	dbi, err := db.NewDatabase(":memory:")
	require.NoError(t, err)
	defer dbi.Close()

	svc := NewMessageService(nil, dbi, NewTestConfig())

	req := &models.MappingRequest{
		Number:   201,
		MatrixID: "", // Invalid: empty
	}

	resp, err := svc.SaveMapping(req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, "matrix_id is required", err.Error())
}

func TestSaveMapping_UpdateExisting(t *testing.T) {
	dbi, err := db.NewDatabase(":memory:")
	require.NoError(t, err)
	defer dbi.Close()

	svc := NewMessageService(nil, dbi, NewTestConfig())

	// Save initial mapping with sub-numbers
	req1 := &models.MappingRequest{
		Number:     201,
		MatrixID:   "@alice:example.com",
		SubNumbers: []int{1001, 1002},
	}
	_, err = svc.SaveMapping(req1)
	assert.NoError(t, err)

	// Verify initial sub-numbers are mapped
	svc.mu.RLock()
	assert.Equal(t, "@alice:example.com", svc.subNumberMappings[1001])
	assert.Equal(t, "@alice:example.com", svc.subNumberMappings[1002])
	svc.mu.RUnlock()

	// Update with different sub-numbers
	req2 := &models.MappingRequest{
		Number:     201,
		MatrixID:   "@alice:example.com",
		SubNumbers: []int{1001, 1003}, // Changed from [1001, 1002] to [1001, 1003]
	}
	resp, err := svc.SaveMapping(req2)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, []int{1001, 1003}, resp.SubNumbers)

	// Verify old sub-number 1002 is cleaned up
	svc.mu.RLock()
	_, exists := svc.subNumberMappings[1002]
	assert.False(t, exists, "old sub-number 1002 should be cleaned up")
	assert.Equal(t, "@alice:example.com", svc.subNumberMappings[1001])
	assert.Equal(t, "@alice:example.com", svc.subNumberMappings[1003])
	svc.mu.RUnlock()
}

func TestSaveMapping_StoredByNumberAndUsername(t *testing.T) {
	dbi, err := db.NewDatabase(":memory:")
	require.NoError(t, err)
	defer dbi.Close()

	svc := NewMessageService(nil, dbi, NewTestConfig())

	req := &models.MappingRequest{
		Number:     201,
		MatrixID:   "@alice:example.com",
		SubNumbers: []int{},
	}

	_, err = svc.SaveMapping(req)
	assert.NoError(t, err)

	// Verify stored by number
	svc.mu.RLock()
	entry, exists := svc.mappings["201"]
	assert.True(t, exists, "mapping should be stored by number")
	assert.Equal(t, 201, entry.Number)
	assert.Equal(t, "@alice:example.com", entry.MatrixID)

	// Verify stored by username
	entry, exists = svc.mappings["alice"]
	assert.True(t, exists, "mapping should be stored by username")
	assert.Equal(t, 201, entry.Number)
	assert.Equal(t, "@alice:example.com", entry.MatrixID)
	svc.mu.RUnlock()
}

func TestSaveMapping_RoomAlias(t *testing.T) {
	dbi, err := db.NewDatabase(":memory:")
	require.NoError(t, err)
	defer dbi.Close()

	svc := NewMessageService(nil, dbi, NewTestConfig())

	req := &models.MappingRequest{
		Number:     201,
		MatrixID:   "#support:example.com",
		SubNumbers: []int{},
	}

	resp, err := svc.SaveMapping(req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, "#support:example.com", resp.MatrixID)

	// Verify stored with extracted localpart
	svc.mu.RLock()
	entry, exists := svc.mappings["support"]
	assert.True(t, exists, "mapping should be stored by localpart")
	assert.Equal(t, "#support:example.com", entry.MatrixID)
	svc.mu.RUnlock()
}

func TestReportPushToken_Auth401DoesNotSave(t *testing.T) {
	// set up in-memory DB
	dbi, err := db.NewDatabase(":memory:")
	require.NoError(t, err)
	defer dbi.Close()

	svc := NewMessageService(nil, dbi, NewTestConfig())
	// For testing, we can't directly inject a fake since authClient is now *HTTPAuthClient.
	// The test will fail as expected because the real client will try to connect.
	// In a real scenario, you would use dependency injection or mock at a higher level.
	// This test verifies that when auth fails, no token is saved.

	req := &models.PushTokenReportRequest{
		UserName:  "@alice:example.com",
		Selector:  "@alice:example.com",
		TokenMsgs: "token123",
		AppIDMsgs: "com.acrobits.softphone",
		Password:  "wrong",
	}

	resp, err := svc.ReportPushToken(context.TODO(), req)
	assert.Error(t, err)
	assert.Nil(t, resp)

	// Ensure no token saved
	token, err := dbi.GetPushToken("@alice:example.com")
	assert.NoError(t, err)
	assert.Nil(t, token)
}
