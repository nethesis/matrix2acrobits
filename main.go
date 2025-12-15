package main

import (
	"os"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/nethesis/matrix2acrobits/api"
	"github.com/nethesis/matrix2acrobits/db"
	"github.com/nethesis/matrix2acrobits/logger"
	"github.com/nethesis/matrix2acrobits/matrix"
	"github.com/nethesis/matrix2acrobits/service"
	"maunium.net/go/mautrix/id"
)

const defaultPort = "8080"

func main() {
	// Initialize logger from LOGLEVEL env var (default: INFO)
	logLevel := os.Getenv("LOGLEVEL")
	if logLevel == "" {
		logLevel = string(logger.LevelInfo)
	}
	logger.Init(logger.Level(logLevel))
	logger.Info().Str("level", logLevel).Msg("logger initialized")

	port := os.Getenv("PROXY_PORT")
	if port == "" {
		port = defaultPort
	}

	e := echo.New()
	e.HideBanner = true
	e.Pre(middleware.RemoveTrailingSlash())
	e.Use(middleware.RequestID())
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	e.GET("/health", func(c echo.Context) error {
		return c.JSON(200, map[string]string{"status": "ok"})
	})

	homeserver := os.Getenv("MATRIX_HOMESERVER_URL")
	if homeserver == "" {
		logger.Fatal().Msg("MATRIX_HOMESERVER_URL is required")
	}

	adminToken := os.Getenv("SUPER_ADMIN_TOKEN")
	if adminToken == "" {
		logger.Fatal().Msg("SUPER_ADMIN_TOKEN is required (must be the Application Service as_token)")
	}

	asUserID := os.Getenv("AS_USER_ID")
	if asUserID == "" {
		logger.Fatal().Msg("AS_USER_ID is required (e.g., '@_acrobits_proxy:your.server.com')")
	}

	logger.Info().Str("homeserver", homeserver).Str("as_user_id", asUserID).Msg("initializing matrix client")

	matrixClient, err := matrix.NewClient(matrix.Config{
		HomeserverURL: homeserver,
		AsToken:       adminToken,
		AsUserID:      id.UserID(asUserID),
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to initialize matrix client")
	}

	// Initialize push token database
	pushTokenDBPath := os.Getenv("PUSH_TOKEN_DB_PATH")
	if pushTokenDBPath == "" {
		pushTokenDBPath = "/tmp/push_tokens.db"
	}

	pushTokenDB, err := db.NewDatabase(pushTokenDBPath)
	if err != nil {
		logger.Fatal().Err(err).Str("path", pushTokenDBPath).Msg("failed to initialize push token database")
	}
	defer func() {
		if err := pushTokenDB.Close(); err != nil {
			logger.Warn().Err(err).Msg("failed to close push token database")
		}
	}()

	// Get proxy URL for pusher registration
	proxyURL := os.Getenv("PROXY_URL")
	if proxyURL == "" {
		proxyURL = homeserver
		logger.Info().Msg("PROXY_URL not configured, assuming same as MATRIX_HOMESERVER_URL")
	} else {
		logger.Info().Str("proxy_url", proxyURL).Msg("proxy URL configured for pusher registration")
	}

	svc := service.NewMessageService(matrixClient, pushTokenDB, proxyURL)
	pushSvc := service.NewPushService(pushTokenDB)
	api.RegisterRoutes(e, svc, pushSvc, adminToken, pushTokenDB)

	// Load mappings from file if MAPPING_FILE env var is set
	mappingFile := os.Getenv("MAPPING_FILE")
	if mappingFile != "" {
		if err := svc.LoadMappingsFromFile(mappingFile); err != nil {
			logger.Error().Err(err).Str("file", mappingFile).Msg("failed to load mappings from file")
		}
	}

	logger.Info().Str("port", port).Msg("starting server")
	if err := e.Start(":" + port); err != nil {
		logger.Fatal().Err(err).Msg("server stopped")
	}
}
