package service

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/nethesis/matrix2acrobits/logger"
	"maunium.net/go/mautrix/id"
)

const (
	defaultPort            = "8080"
	defaultCacheTTLSeconds = 3600
	defaultPushTokenDBPath = "/tmp/push_tokens.db"
	defaultExtAuthTimeoutS = 5
	defaultLogLevel        = "INFO"
)

// Config holds all configuration loaded from environment variables
type Config struct {
	// Server configuration
	ProxyPort string
	LogLevel  string

	// Matrix configuration
	MatrixHomeserverURL  string
	MatrixAsToken        string
	MatrixAsUserID       id.UserID
	MatrixHomeserverHost string

	// Push tokens database
	PushTokenDBPath string

	// Proxy configuration for push registration
	ProxyURL string

	// Message service configuration
	CacheTTLSeconds int
	CacheTTL        time.Duration

	// External authentication configuration
	ExtAuthURL      string
	ExtAuthTimeoutS int
	ExtAuthTimeout  time.Duration

	// Mapping file
	MappingFile string
}

// NewConfig loads all configuration from environment variables with validation
func NewConfig() (*Config, error) {
	cfg := &Config{}

	logger.Debug().Msg("starting configuration loading from environment variables")

	// Load server configuration
	cfg.LogLevel = os.Getenv("LOGLEVEL")
	if cfg.LogLevel == "" {
		cfg.LogLevel = defaultLogLevel
		logger.Debug().Str("LOGLEVEL", cfg.LogLevel).Msg("using default log level")
	} else {
		logger.Debug().Str("LOGLEVEL", cfg.LogLevel).Msg("log level loaded from environment")
	}

	cfg.ProxyPort = os.Getenv("PROXY_PORT")
	if cfg.ProxyPort == "" {
		cfg.ProxyPort = defaultPort
		logger.Debug().Str("PROXY_PORT", cfg.ProxyPort).Msg("using default proxy port")
	} else {
		logger.Debug().Str("PROXY_PORT", cfg.ProxyPort).Msg("proxy port loaded from environment")
	}

	// Load Matrix configuration (required)
	cfg.MatrixHomeserverURL = os.Getenv("MATRIX_HOMESERVER_URL")
	if cfg.MatrixHomeserverURL == "" {
		logger.Error().Msg("MATRIX_HOMESERVER_URL environment variable is missing")
		return nil, fmt.Errorf("MATRIX_HOMESERVER_URL is required")
	}
	logger.Debug().Str("MATRIX_HOMESERVER_URL", cfg.MatrixHomeserverURL).Msg("matrix homeserver URL loaded from environment")

	cfg.MatrixAsToken = os.Getenv("MATRIX_AS_TOKEN")
	if cfg.MatrixAsToken == "" {
		logger.Error().Msg("MATRIX_AS_TOKEN environment variable is missing")
		return nil, fmt.Errorf("MATRIX_AS_TOKEN is required (must be the Application Service as_token)")
	}
	logger.Debug().Msg("MATRIX_AS_TOKEN loaded from environment")

	asUserIDStr := os.Getenv("AS_USER_ID")
	if asUserIDStr == "" {
		logger.Error().Msg("AS_USER_ID environment variable is missing")
		return nil, fmt.Errorf("AS_USER_ID is required (e.g., '@_acrobits_proxy:your.server.com')")
	}
	cfg.MatrixAsUserID = id.UserID(asUserIDStr)
	logger.Debug().Str("AS_USER_ID", asUserIDStr).Msg("application service user ID loaded from environment")

	// Derive homeserver host from MATRIX_HOMESERVER_URL
	if u, err := url.Parse(cfg.MatrixHomeserverURL); err == nil {
		cfg.MatrixHomeserverHost = u.Hostname()
		logger.Debug().Str("homeserver_host", cfg.MatrixHomeserverHost).Msg("extracted homeserver host from URL")
	} else {
		logger.Warn().Err(err).Str("MATRIX_HOMESERVER_URL", cfg.MatrixHomeserverURL).Msg("failed to parse homeserver URL")
	}

	// Load push tokens database configuration
	cfg.PushTokenDBPath = os.Getenv("PUSH_TOKEN_DB_PATH")
	if cfg.PushTokenDBPath == "" {
		cfg.PushTokenDBPath = defaultPushTokenDBPath
		logger.Debug().Str("PUSH_TOKEN_DB_PATH", cfg.PushTokenDBPath).Msg("using default push token database path")
	} else {
		logger.Debug().Str("PUSH_TOKEN_DB_PATH", cfg.PushTokenDBPath).Msg("push token database path loaded from environment")
	}

	// Load proxy configuration
	cfg.ProxyURL = os.Getenv("PROXY_URL")
	if cfg.ProxyURL == "" {
		cfg.ProxyURL = cfg.MatrixHomeserverURL
		logger.Info().Str("PROXY_URL", cfg.ProxyURL).Msg("PROXY_URL not configured, assuming same as MATRIX_HOMESERVER_URL")
	} else {
		logger.Debug().Str("PROXY_URL", cfg.ProxyURL).Msg("proxy URL loaded from environment")
	}

	// Load cache configuration
	cacheTTLStr := os.Getenv("CACHE_TTL_SECONDS")
	cfg.CacheTTLSeconds = defaultCacheTTLSeconds
	if cacheTTLStr != "" {
		if parsed, err := strconv.Atoi(cacheTTLStr); err == nil && parsed > 0 {
			cfg.CacheTTLSeconds = parsed
			logger.Debug().Int("CACHE_TTL_SECONDS", cfg.CacheTTLSeconds).Msg("cache TTL loaded from environment")
		} else {
			logger.Warn().Str("CACHE_TTL_SECONDS", cacheTTLStr).Err(err).Int("default", defaultCacheTTLSeconds).Msg("invalid cache TTL value, using default")
		}
	} else {
		logger.Debug().Int("CACHE_TTL_SECONDS", cfg.CacheTTLSeconds).Msg("using default cache TTL")
	}
	cfg.CacheTTL = time.Duration(cfg.CacheTTLSeconds) * time.Second

	// Load external authentication configuration
	cfg.ExtAuthURL = os.Getenv("EXT_AUTH_URL")
	if cfg.ExtAuthURL == "" {
		logger.Warn().Msg("EXT_AUTH_URL not set - external authentication will not be available")
	} else {
		logger.Debug().Str("EXT_AUTH_URL", cfg.ExtAuthURL).Msg("external authentication URL loaded from environment")
	}

	cfg.ExtAuthTimeoutS = defaultExtAuthTimeoutS
	if v := os.Getenv("EXT_AUTH_TIMEOUT_S"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			cfg.ExtAuthTimeoutS = parsed
			logger.Debug().Int("EXT_AUTH_TIMEOUT_S", cfg.ExtAuthTimeoutS).Msg("external auth timeout loaded from environment")
		} else {
			logger.Warn().Str("EXT_AUTH_TIMEOUT_S", v).Err(err).Int("default", defaultExtAuthTimeoutS).Msg("invalid external auth timeout value, using default")
		}
	} else {
		logger.Debug().Int("EXT_AUTH_TIMEOUT_S", cfg.ExtAuthTimeoutS).Msg("using default external auth timeout")
	}
	cfg.ExtAuthTimeout = time.Duration(cfg.ExtAuthTimeoutS) * time.Second

	// Load mapping file configuration
	cfg.MappingFile = os.Getenv("MAPPING_FILE")
	if cfg.MappingFile != "" {
		logger.Debug().Str("MAPPING_FILE", cfg.MappingFile).Msg("mapping file path loaded from environment")
	} else {
		logger.Debug().Msg("MAPPING_FILE not set - no mapping file will be loaded at startup")
	}

	logger.Debug().Msg("configuration loading completed successfully")

	return cfg, nil
}

// NewTestConfig creates a minimal Config for testing purposes
func NewTestConfig() *Config {
	return &Config{
		ProxyPort:            defaultPort,
		LogLevel:             defaultLogLevel,
		MatrixHomeserverURL:  "https://example.com",
		MatrixAsToken:        "test_token",
		MatrixAsUserID:       "@test:example.com",
		MatrixHomeserverHost: "example.com",
		PushTokenDBPath:      defaultPushTokenDBPath,
		ProxyURL:             "https://example.com",
		CacheTTLSeconds:      defaultCacheTTLSeconds,
		CacheTTL:             time.Duration(defaultCacheTTLSeconds) * time.Second,
		ExtAuthTimeoutS:      defaultExtAuthTimeoutS,
		ExtAuthTimeout:       time.Duration(defaultExtAuthTimeoutS) * time.Second,
	}
}

// NewTestConfigWithAuth creates a Config for testing with custom auth URL
func NewTestConfigWithAuth(extAuthURL string) *Config {
	cfg := NewTestConfig()
	cfg.ExtAuthURL = extAuthURL
	return cfg
}
