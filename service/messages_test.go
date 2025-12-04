package service

import (
	"os"
	"testing"

	"github.com/nethesis/matrix2acrobits/models"
	"github.com/stretchr/testify/assert"
)

func TestNewMessageService(t *testing.T) {
	// We can't create a real MatrixClient without a valid homeserver,
	// so we'll skip full integration here and test just the pure functions

	// Test with nil to ensure the function exists
	// In a real scenario, users would pass a properly initialized MatrixClient
}

func TestNormalizeMappingKey(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Normal phone number",
			input:    "+1234567890",
			expected: "+1234567890",
		},
		{
			name:     "With whitespace",
			input:    "  +1234567890  ",
			expected: "+1234567890",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeMappingKey(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
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
	svc := NewMessageService(nil)

	// Seed two mappings
	svc.setMapping(mappingEntry{
		SMSNumber: "+111",
		MatrixID:  "@alice:example.com",
		RoomID:    "!room1:example.com",
	})
	svc.setMapping(mappingEntry{
		SMSNumber: "+222",
		MatrixID:  "@bob:example.com",
		RoomID:    "!room2:example.com",
	})

	list, err := svc.ListMappings()
	assert.NoError(t, err)
	// We expect two mappings; order is not guaranteed.
	assert.Len(t, list, 2)

	// Build a map for easy assertions
	m := make(map[string]*models.MappingResponse)
	for _, it := range list {
		m[it.SMSNumber] = it
	}

	if v, ok := m["+111"]; ok {
		assert.Equal(t, "@alice:example.com", v.MatrixID)
		assert.Equal(t, "!room1:example.com", v.RoomID)
	} else {
		t.Fatalf("missing mapping for +111")
	}

	if v, ok := m["+222"]; ok {
		assert.Equal(t, "@bob:example.com", v.MatrixID)
		assert.Equal(t, "!room2:example.com", v.RoomID)
	} else {
		t.Fatalf("missing mapping for +222")
	}
}

func TestIsPhoneNumber(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "Simple phone number",
			input:    "1234567890",
			expected: true,
		},
		{
			name:     "Phone with plus prefix",
			input:    "+1234567890",
			expected: true,
		},
		{
			name:     "Phone with hyphens",
			input:    "123-456-7890",
			expected: true,
		},
		{
			name:     "Phone with spaces",
			input:    "123 456 7890",
			expected: true,
		},
		{
			name:     "Phone with parentheses",
			input:    "(123) 456-7890",
			expected: true,
		},
		{
			name:     "Matrix user ID",
			input:    "@user:example.com",
			expected: false,
		},
		{
			name:     "Room ID",
			input:    "!room123:example.com",
			expected: false,
		},
		{
			name:     "Room alias",
			input:    "#alias:example.com",
			expected: false,
		},
		{
			name:     "Empty string",
			input:    "",
			expected: false,
		},
		{
			name:     "Only formatting chars",
			input:    "+-() ",
			expected: false,
		},
		{
			name:     "With invalid characters",
			input:    "123-ABC",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isPhoneNumber(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestLoadMappingsFromFile(t *testing.T) {
	// Create a temporary JSON file with test mappings in array format
	tmpFile, err := os.CreateTemp("", "mappings_*.json")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	// Write test data in array format
	testData := `[
  {
    "sms_number": "91201",
    "matrix_id": "@giacomo:example.com",
    "room_id": "!room1:example.com"
  },
  {
    "sms_number": "91202",
    "matrix_id": "@mario:example.com",
    "room_id": "!room2:example.com"
  }
]`
	_, err = tmpFile.WriteString(testData)
	assert.NoError(t, err)
	tmpFile.Close()

	// Create a message service
	svc := NewMessageService(nil)

	// Load mappings from file
	err = svc.LoadMappingsFromFile(tmpFile.Name())
	assert.NoError(t, err)

	// Verify mappings were loaded
	mappings, err := svc.ListMappings()
	assert.NoError(t, err)
	assert.Len(t, mappings, 2)

	// Check specific mappings
	mapping1, err := svc.LookupMapping("91201")
	assert.NoError(t, err)
	assert.Equal(t, "@giacomo:example.com", mapping1.MatrixID)
	assert.Equal(t, "!room1:example.com", mapping1.RoomID)

	mapping2, err := svc.LookupMapping("91202")
	assert.NoError(t, err)
	assert.Equal(t, "@mario:example.com", mapping2.MatrixID)
	assert.Equal(t, "!room2:example.com", mapping2.RoomID)
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
	svc := NewMessageService(nil)

	// Load mappings from file - should fail since we only support extended format now
	err = svc.LoadMappingsFromFile(tmpFile.Name())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse mapping file")
}

func TestLoadMappingsFromFile_FileNotFound(t *testing.T) {
	svc := NewMessageService(nil)

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

	svc := NewMessageService(nil)

	err = svc.LoadMappingsFromFile(tmpFile.Name())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse mapping file")
}
