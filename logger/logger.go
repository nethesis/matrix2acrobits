package logger

import (
	"io"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

var log zerolog.Logger

// Level represents the logging level
type Level string

const (
	LevelDebug    Level = "DEBUG"
	LevelInfo     Level = "INFO"
	LevelWarning  Level = "WARNING"
	LevelCritical Level = "CRITICAL"
)

// Init initializes the global logger with the specified level
func Init(level Level) {
	var zLevel zerolog.Level
	switch strings.ToUpper(string(level)) {
	case string(LevelDebug):
		zLevel = zerolog.DebugLevel
	case string(LevelInfo):
		zLevel = zerolog.InfoLevel
	case string(LevelWarning):
		zLevel = zerolog.WarnLevel
	case string(LevelCritical):
		zLevel = zerolog.ErrorLevel
	default:
		zLevel = zerolog.InfoLevel
	}

	zerolog.SetGlobalLevel(zLevel)
	// Configure zerolog for human-readable output
	log = zerolog.New(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}).With().Timestamp().Logger()
}

// InitWithWriter initializes the logger with a custom writer (useful for testing)
func InitWithWriter(level Level, w io.Writer) {
	var zLevel zerolog.Level
	switch strings.ToUpper(string(level)) {
	case string(LevelDebug):
		zLevel = zerolog.DebugLevel
	case string(LevelInfo):
		zLevel = zerolog.InfoLevel
	case string(LevelWarning):
		zLevel = zerolog.WarnLevel
	case string(LevelCritical):
		zLevel = zerolog.ErrorLevel
	default:
		zLevel = zerolog.InfoLevel
	}

	zerolog.SetGlobalLevel(zLevel)
	// Configure zerolog for human-readable output
	log = zerolog.New(zerolog.ConsoleWriter{Out: w, TimeFormat: time.RFC3339}).With().Timestamp().Logger()
}

// Debug logs a debug message
func Debug() *zerolog.Event {
	return log.Debug()
}

// Info logs an info message
func Info() *zerolog.Event {
	return log.Info()
}

// Warn logs a warning message
func Warn() *zerolog.Event {
	return log.Warn()
}

// Error logs an error message (CRITICAL level)
func Error() *zerolog.Event {
	return log.Error()
}

// Fatal logs a fatal message and exits
func Fatal() *zerolog.Event {
	return log.Fatal()
}

// With creates a child logger with additional fields
func With() zerolog.Context {
	return log.With()
}
