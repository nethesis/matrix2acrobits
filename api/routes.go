package api

import (
	"errors"
	"io"
	"net"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/nethesis/matrix2acrobits/db"
	"github.com/nethesis/matrix2acrobits/logger"
	"github.com/nethesis/matrix2acrobits/models"
	"github.com/nethesis/matrix2acrobits/service"
)

const adminTokenHeader = "X-Super-Admin-Token"

// RegisterRoutes wires API endpoints to Echo handlers.
func RegisterRoutes(e *echo.Echo, svc *service.MessageService, pushSvc *service.PushService, adminToken string, pushTokenDB *db.Database) {
	h := handler{svc: svc, pushSvc: pushSvc, adminToken: adminToken, pushTokenDB: pushTokenDB}
	e.POST("/api/client/send_message", h.sendMessage)
	e.POST("/api/client/fetch_messages", h.fetchMessages)
	e.POST("/api/client/push_token_report", h.pushTokenReport)
	e.GET("/api/internal/push_tokens", h.getPushTokens)
	e.DELETE("/api/internal/push_tokens", h.resetPushTokens)

	// Matrix Push Gateway API
	e.POST("/_matrix/push/v1/notify", h.matrixPushNotify)
	// Matrix Application Service transactions (push events to AS)
	e.PUT("/_matrix/app/v1/transactions/:txnId", h.matrixAppTransaction)
}

type handler struct {
	svc         *service.MessageService
	pushSvc     *service.PushService
	adminToken  string
	pushTokenDB *db.Database
}

func (h handler) sendMessage(c echo.Context) error {
	var req models.SendMessageRequest
	if err := c.Bind(&req); err != nil {
		logger.Warn().Str("endpoint", "send_message").Err(err).Msg("invalid request payload")
		return echo.NewHTTPError(http.StatusBadRequest, "invalid payload")
	}

	logger.Debug().Str("endpoint", "send_message").Str("from", req.From).Str("to", req.To).Msg("processing send message request")
	logger.Debug().Str("endpoint", "send_message").Str("raw_from", req.From).Str("raw_to", req.To).Msg("raw identifiers for recipient resolution")

	resp, err := h.svc.SendMessage(c.Request().Context(), &req)
	if err != nil {
		logger.Error().Str("endpoint", "send_message").Str("from", req.From).Str("to", req.To).Err(err).Msg("failed to send message")
		// Add extra context to help debugging recipient resolution
		logger.Debug().Str("endpoint", "send_message").Str("from", req.From).Str("to", req.To).Msg("send_message handler returning error to client; check mapping store and AS configuration")
		return mapServiceError(err)
	}

	logger.Info().Str("endpoint", "send_message").Str("from", req.From).Str("to", req.To).Str("message_id", resp.ID).Msg("message sent successfully")
	return c.JSON(http.StatusOK, resp)
}

func (h handler) fetchMessages(c echo.Context) error {
	var req models.FetchMessagesRequest
	if err := c.Bind(&req); err != nil {
		logger.Warn().Str("endpoint", "fetch_messages").Err(err).Msg("invalid request payload")
		return echo.NewHTTPError(http.StatusBadRequest, "invalid payload")
	}

	logger.Debug().Str("endpoint", "fetch_messages").Str("username", req.Username).Str("last_id", req.LastID).Msg("processing fetch messages request")

	resp, err := h.svc.FetchMessages(c.Request().Context(), &req)
	if err != nil {
		logger.Error().Str("endpoint", "fetch_messages").Str("username", req.Username).Err(err).Msg("failed to fetch messages")
		return mapServiceError(err)
	}

	logger.Info().Str("endpoint", "fetch_messages").Str("username", req.Username).Int("received", len(resp.ReceivedSMSs)).Int("sent", len(resp.SentSMSs)).Msg("messages fetched successfully")
	return c.JSON(http.StatusOK, resp)
}

func (h handler) pushTokenReport(c echo.Context) error {
	var req models.PushTokenReportRequest
	// Bind early so we can log the full struct for debugging.
	// We log the entire request struct to capture all fields without depending on their names.
	if err := c.Bind(&req); err != nil {
		logger.Warn().Str("endpoint", "push_token_report").Err(err).Msg("invalid request payload")
		return echo.NewHTTPError(http.StatusBadRequest, "invalid payload")
	}
	logger.Debug().Str("endpoint", "push_token_report").Interface("payload", req).Msg("push_token_report payload fields")

	resp, err := h.svc.ReportPushToken(c.Request().Context(), &req)
	if err != nil {
		logger.Error().Str("endpoint", "push_token_report").Str("selector", req.Selector).Err(err).Msg("failed to report push token")
		return mapServiceError(err)
	}

	logger.Info().Str("endpoint", "push_token_report").Str("selector", req.Selector).Msg("push token reported successfully")
	return c.JSON(http.StatusOK, resp)
}

func (h handler) getPushTokens(c echo.Context) error {
	if err := h.ensureAdminAccess(c); err != nil {
		return err
	}

	logger.Debug().Str("endpoint", "get_push_tokens").Msg("fetching all push tokens")

	if err := h.ensurePushTokenDB("get_push_tokens"); err != nil {
		return err
	}

	tokens, err := h.pushTokenDB.ListPushTokens()
	if err != nil {
		logger.Error().Str("endpoint", "get_push_tokens").Err(err).Msg("failed to list push tokens")
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	logger.Info().Str("endpoint", "get_push_tokens").Int("count", len(tokens)).Msg("push tokens listed successfully")
	return c.JSON(http.StatusOK, tokens)
}

func (h handler) resetPushTokens(c echo.Context) error {
	if err := h.ensureAdminAccess(c); err != nil {
		return err
	}

	logger.Debug().Str("endpoint", "reset_push_tokens").Msg("resetting push tokens database")

	if err := h.ensurePushTokenDB("reset_push_tokens"); err != nil {
		return err
	}

	if err := h.pushTokenDB.ResetPushTokens(); err != nil {
		logger.Error().Str("endpoint", "reset_push_tokens").Err(err).Msg("failed to reset push tokens")
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	logger.Info().Str("endpoint", "reset_push_tokens").Msg("push tokens database reset successfully")
	return c.JSON(http.StatusOK, map[string]string{"status": "reset"})
}

func (h handler) ensureAdminAccess(c echo.Context) error {
	if h.adminToken == "" {
		return echo.NewHTTPError(http.StatusInternalServerError, "admin token not configured")
	}
	if !h.isLocalhost(c.RealIP()) {
		return echo.NewHTTPError(http.StatusForbidden, "mapping API only available from localhost")
	}
	token := c.Request().Header.Get(adminTokenHeader)
	if token == "" || token != h.adminToken {
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid admin token")
	}
	return nil
}

// ensurePushTokenDB validates that the push token database is initialized
func (h handler) ensurePushTokenDB(endpoint string) error {
	if h.pushTokenDB == nil {
		logger.Error().Str("endpoint", endpoint).Msg("push token database not available")
		return echo.NewHTTPError(http.StatusInternalServerError, "push token database not available")
	}
	return nil
}

func mapServiceError(err error) error {
	switch {
	case errors.Is(err, service.ErrAuthentication):
		return echo.NewHTTPError(http.StatusUnauthorized, err.Error())
	case errors.Is(err, service.ErrInvalidRecipient):
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	case errors.Is(err, service.ErrMappingNotFound):
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	default:
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
}

func (h handler) isLocalhost(ip string) bool {
	host, _, err := net.SplitHostPort(ip)
	if err != nil {
		// likely no port
		host = ip
	}
	switch host {
	case "127.0.0.1", "::1", "localhost":
		return true
	default:
		return false
	}
}

func (h handler) matrixPushNotify(c echo.Context) error {
	var req models.MatrixPushNotifyRequest
	if err := c.Bind(&req); err != nil {
		logger.Warn().Str("endpoint", "matrix_push_notify").Err(err).Msg("invalid request payload")
		return echo.NewHTTPError(http.StatusBadRequest, "invalid payload")
	}

	logger.Debug().Str("endpoint", "matrix_push_notify").Int("device_count", len(req.Notification.Devices)).Str("event_id", req.Notification.EventID).Msg("processing matrix push notification")

	if h.pushSvc == nil {
		logger.Error().Str("endpoint", "matrix_push_notify").Msg("push service not initialized")
		return echo.NewHTTPError(http.StatusInternalServerError, "push service not available")
	}

	resp, err := h.pushSvc.HandleMatrixPushNotification(c.Request().Context(), &req)
	if err != nil {
		logger.Error().Str("endpoint", "matrix_push_notify").Err(err).Msg("failed to handle matrix push notification")
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	logger.Info().Str("endpoint", "matrix_push_notify").Int("rejected_count", len(resp.Rejected)).Msg("matrix push notification processed")
	return c.JSON(http.StatusOK, resp)
}

// matrixAppTransaction handles incoming Application Service transactions from homeservers.
// It logs the received payload for debugging and returns HTTP 200 as required by the spec.
func (h handler) matrixAppTransaction(c echo.Context) error {
	txnId := c.Param("txnId")
	// Read raw body
	bodyBytes, err := io.ReadAll(c.Request().Body)
	if err != nil {
		logger.Error().Str("endpoint", "matrix_app_transaction").Str("txn_id", txnId).Err(err).Msg("failed to read request body")
		return echo.NewHTTPError(http.StatusBadRequest, "invalid payload")
	}

	logger.Debug().Str("endpoint", "matrix_app_transaction").Str("txn_id", txnId).Str("payload", string(bodyBytes)).Msg("received application service transaction")

	// As per spec, acknowledge with an empty JSON object and 200 OK.
	return c.JSON(http.StatusOK, map[string]interface{}{})
}
