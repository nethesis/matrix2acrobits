package main

import (
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/nethesis/matrix2acrobits/api"
	"github.com/nethesis/matrix2acrobits/db"
	"github.com/nethesis/matrix2acrobits/logger"
	"github.com/nethesis/matrix2acrobits/matrix"
	"github.com/nethesis/matrix2acrobits/service"
)

func main() {
	// Load configuration from environment variables
	cfg, err := service.NewConfig()
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to load configuration")
	}

	// Initialize logger
	logger.Init(logger.Level(cfg.LogLevel))
	logger.Info().Str("level", cfg.LogLevel).Msg("logger initialized")

	e := echo.New()
	e.HideBanner = true
	e.Pre(middleware.RemoveTrailingSlash())
	e.Use(middleware.RequestID())
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	e.GET("/health", func(c echo.Context) error {
		return c.JSON(200, map[string]string{"status": "ok"})
	})

	logger.Info().Str("homeserver", cfg.MatrixHomeserverURL).Str("as_user_id", cfg.MatrixAsUserID.String()).Msg("initializing matrix client")

	matrixClient, err := matrix.NewClient(matrix.Config{
		HomeserverURL: cfg.MatrixHomeserverURL,
		AsToken:       cfg.MatrixAsToken,
		AsUserID:      cfg.MatrixAsUserID,
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to initialize matrix client")
	}

	// Initialize push token database
	pushTokenDB, err := db.NewDatabase(cfg.PushTokenDBPath)
	if err != nil {
		logger.Fatal().Err(err).Str("path", cfg.PushTokenDBPath).Msg("failed to initialize push token database")
	}
	defer func() {
		if err := pushTokenDB.Close(); err != nil {
			logger.Warn().Err(err).Msg("failed to close push token database")
		}
	}()

	logger.Info().Str("proxy_url", cfg.ProxyURL).Msg("proxy URL configured for pusher registration")

	svc := service.NewMessageService(matrixClient, pushTokenDB, cfg)
	pushSvc := service.NewPushService(pushTokenDB)
	api.RegisterRoutes(e, svc, pushSvc, cfg.MatrixAsToken, pushTokenDB)

	logger.Info().Str("port", cfg.ProxyPort).Msg("starting server")
	if err := e.Start(":" + cfg.ProxyPort); err != nil {
		logger.Fatal().Err(err).Msg("server stopped")
	}
}
