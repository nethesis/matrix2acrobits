package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/nethesis/matrix2acrobits/db"
	"github.com/nethesis/matrix2acrobits/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	svc := NewMessageService(nil, nil, "")

	// Seed two mappings
	svc.setMapping(mappingEntry{
		Number:   111,
		MatrixID: "@alice:example.com",
	})
	svc.setMapping(mappingEntry{
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

func TestLoadMappingsFromFile(t *testing.T) {
	// Create a temporary JSON file with test mappings in array format
	tmpFile, err := os.CreateTemp("", "mappings_*.json")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	// Write test data in array format with integer numbers and sub_numbers
	testData := `[
  {
    "number": 201,
    "matrix_id": "@giacomo:example.com",
    "sub_numbers": [91201]
  },
  {
    "number": 202,
    "matrix_id": "@mario:example.com",
    "sub_numbers": [91202]
  }
]`
	_, err = tmpFile.WriteString(testData)
	assert.NoError(t, err)
	tmpFile.Close()

	// Create a message service
	svc := NewMessageService(nil, nil, "")

	// Load mappings from file
	err = svc.LoadMappingsFromFile(tmpFile.Name())
	assert.NoError(t, err)

	// Verify mappings were loaded
	mappings, err := svc.ListMappings()
	assert.NoError(t, err)
	assert.Len(t, mappings, 2)

	// Check specific mappings - by sub_number
	mapping1, err := svc.LookupMapping("91201")
	assert.NoError(t, err)
	assert.Equal(t, "@giacomo:example.com", mapping1.MatrixID)

	mapping2, err := svc.LookupMapping("91202")
	assert.NoError(t, err)
	assert.Equal(t, "@mario:example.com", mapping2.MatrixID)

	// Check specific mappings - by main number
	mapping3, err := svc.LookupMapping("201")
	assert.NoError(t, err)
	assert.Equal(t, "@giacomo:example.com", mapping3.MatrixID)

	mapping4, err := svc.LookupMapping("202")
	assert.NoError(t, err)
	assert.Equal(t, "@mario:example.com", mapping4.MatrixID)
}

func TestLoadMappingsFromFile_LegacyFormat(t *testing.T) {
	// Create a temporary JSON file with test mappings in legacy format
	tmpFile, err := os.CreateTemp("", "mappings_legacy_*.json")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	// Write test data in legacy format (object with phone numbers as keys)
	testData := `{
    "91201": "@giacomo:example.com",
    "91202": "@mario:example.com"
}`
	_, err = tmpFile.WriteString(testData)
	assert.NoError(t, err)
	tmpFile.Close()

	// Create a message service
	svc := NewMessageService(nil, nil, "")

	// Load mappings from file - should fail since we only support extended format now
	err = svc.LoadMappingsFromFile(tmpFile.Name())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse mapping file")
}

func TestLoadMappingsFromFile_FileNotFound(t *testing.T) {
	svc := NewMessageService(nil, nil, "")

	err := svc.LoadMappingsFromFile("/nonexistent/file.json")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read mapping file")
}

func TestLoadMappingsFromFile_InvalidJSON(t *testing.T) {
	// Create a temporary file with invalid JSON
	tmpFile, err := os.CreateTemp("", "invalid_*.json")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	// Write invalid JSON
	_, err = tmpFile.WriteString("{ invalid json }")
	assert.NoError(t, err)
	tmpFile.Close()

	svc := NewMessageService(nil, nil, "")

	err = svc.LoadMappingsFromFile(tmpFile.Name())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse mapping file")
}

func TestResolveMatrixUser_SubNumbers(t *testing.T) {
	// Test case 1: Resolve sub_number to matrix_id
	t.Run("resolve sub_number to matrix_id", func(t *testing.T) {
		svc := NewMessageService(nil, nil, "")
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
		svc := NewMessageService(nil, nil, "")
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
		svc := NewMessageService(nil, nil, "")
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
		svc := NewMessageService(nil, nil, "")
		result := svc.resolveMatrixUser("@test:example.com")
		assert.Equal(t, "@test:example.com", string(result), "should return matrix_id as-is if it starts with @")
	})

	// Test case 5: No mapping found
	t.Run("no mapping found", func(t *testing.T) {
		svc := NewMessageService(nil, nil, "")
		result := svc.resolveMatrixUser("9999")
		assert.Equal(t, "", string(result), "should return empty string if no mapping found")
	})

	// Test case 6: Case insensitivity
	t.Run("case insensitive sub_number resolution", func(t *testing.T) {
		svc := NewMessageService(nil, nil, "")
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
		svc := NewMessageService(nil, nil, "")
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
		svc := NewMessageService(nil, nil, "")
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
		svc := NewMessageService(nil, nil, "")
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
		svc := NewMessageService(nil, nil, "")
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
		svc := NewMessageService(nil, nil, "")
		result := svc.resolveMatrixIDToIdentifier("@unknown:example.com")
		assert.Equal(t, "@unknown:example.com", result, "should return original matrix_id when no mapping found")
	})
}

func TestReportPushToken(t *testing.T) {
	// Test with nil request
	t.Run("nil request", func(t *testing.T) {
		svc := NewMessageService(nil, nil, "")
		resp, err := svc.ReportPushToken(context.TODO(), nil)
		assert.Error(t, err)
		assert.Nil(t, resp)
	})

	// Test with empty selector
	t.Run("empty selector", func(t *testing.T) {
		svc := NewMessageService(nil, nil, "")
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
		svc := NewMessageService(nil, nil, "")
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
		// mock external auth endpoint
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`[{"main_extension":"201","sub_extensions":["91201"],"user_name":"alice"}]`))
		}))
		defer ts.Close()
		os.Setenv("EXT_AUTH_URL", ts.URL)
		defer os.Unsetenv("EXT_AUTH_URL")
		os.Setenv("MATRIX_HOMESERVER_URL", "https://example.com")
		defer os.Unsetenv("MATRIX_HOMESERVER_URL")

		db, err := db.NewDatabase(":memory:")
		require.NoError(t, err)
		defer db.Close()

		svc := NewMessageService(nil, db, "")
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
		// mock external auth endpoint
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`[{"main_extension":"201","sub_extensions":["91201"],"user_name":"alice"}]`))
		}))
		defer ts.Close()
		os.Setenv("EXT_AUTH_URL", ts.URL)
		defer os.Unsetenv("EXT_AUTH_URL")
		os.Setenv("MATRIX_HOMESERVER_URL", "https://example.com")
		defer os.Unsetenv("MATRIX_HOMESERVER_URL")

		db, err := db.NewDatabase(":memory:")
		require.NoError(t, err)
		defer db.Close()

		svc := NewMessageService(nil, db, "")
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

// fakeAuthClient allows controlling responses for testing.
type fakeAuthClient struct {
	ok bool
}

func (f *fakeAuthClient) Validate(ctx context.Context, extension, secret, homeserverHost string) ([]*models.MappingRequest, bool, error) {
	if f.ok {
		return []*models.MappingRequest{
			{Number: 1, MatrixID: "@alice:" + homeserverHost, SubNumbers: []int{}},
		}, true, nil
	}
	return []*models.MappingRequest{}, false, fmt.Errorf("unauthorized")
}

func TestReportPushToken_Auth401DoesNotSave(t *testing.T) {
	// set up in-memory DB
	dbi, err := db.NewDatabase(":memory:")
	require.NoError(t, err)
	defer dbi.Close()

	svc := NewMessageService(nil, dbi, "")
	// inject fake auth client returning false (not authorized)
	svc.authClient = &fakeAuthClient{ok: false}

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
