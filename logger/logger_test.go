package logger

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInit(t *testing.T) {
	tests := []struct {
		name  string
		level Level
	}{
		{
			name:  "Debug level",
			level: LevelDebug,
		},
		{
			name:  "Info level",
			level: LevelInfo,
		},
		{
			name:  "Warning level",
			level: LevelWarning,
		},
		{
			name:  "Critical level",
			level: LevelCritical,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Just ensure it doesn't panic
			Init(tt.level)
		})
	}
}

func TestLogLevels(t *testing.T) {
	var buf bytes.Buffer
	InitWithWriter(LevelDebug, &buf)

	Debug().Msg("debug message")
	assert.Contains(t, buf.String(), "debug message")
	assert.Contains(t, buf.String(), "DBG")

	buf.Reset()
	Info().Msg("info message")
	assert.Contains(t, buf.String(), "info message")
	assert.Contains(t, buf.String(), "INF")

	buf.Reset()
	Warn().Msg("warning message")
	assert.Contains(t, buf.String(), "warning message")
	assert.Contains(t, buf.String(), "WRN")

	buf.Reset()
	Error().Msg("error message")
	assert.Contains(t, buf.String(), "error message")
	assert.Contains(t, buf.String(), "ERR")
}

func TestLogLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	InitWithWriter(LevelWarning, &buf)

	// Debug and Info should not be logged at WARNING level
	Debug().Msg("debug message")
	assert.Empty(t, buf.String())

	Info().Msg("info message")
	assert.Empty(t, buf.String())

	// Warning should be logged
	Warn().Msg("warning message")
	assert.Contains(t, buf.String(), "warning message")

	buf.Reset()
	Error().Msg("error message")
	assert.Contains(t, buf.String(), "error message")
}

func TestLogWithFields(t *testing.T) {
	var buf bytes.Buffer
	InitWithWriter(LevelInfo, &buf)

	Info().Str("user", "@alice:example.com").Int("count", 42).Msg("user action")

	output := buf.String()
	assert.Contains(t, output, "user action")
	assert.Contains(t, output, "@alice:example.com")
	assert.Contains(t, output, "42")
}

func TestDefaultLevel(t *testing.T) {
	var buf bytes.Buffer
	// Invalid level should default to INFO
	InitWithWriter("INVALID", &buf)

	Debug().Msg("debug message")
	assert.Empty(t, buf.String())

	Info().Msg("info message")
	assert.Contains(t, buf.String(), "info message")
}

func TestCaseInsensitiveLevel(t *testing.T) {
	tests := []struct {
		name     string
		level    Level
		expected string
	}{
		{
			name:     "lowercase debug",
			level:    Level(strings.ToLower(string(LevelDebug))),
			expected: "debug",
		},
		{
			name:     "mixed case info",
			level:    Level("InFo"),
			expected: "info",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			InitWithWriter(tt.level, &buf)
			// Just ensure it doesn't panic
			assert.NotPanics(t, func() {
				Info().Msg("test")
			})
		})
	}
}
