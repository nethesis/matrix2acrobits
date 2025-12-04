package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/nethesis/matrix2acrobits/logger"
	"github.com/nethesis/matrix2acrobits/models"
	"github.com/nethesis/matrix2acrobits/service"
)

const adminTokenHeader = "X-Super-Admin-Token"

// RegisterRoutes wires API endpoints to Echo handlers.
func RegisterRoutes(e *echo.Echo, svc *service.MessageService, adminToken string) {
	h := handler{svc: svc, adminToken: adminToken}
	e.POST("/api/client/send_message", h.sendMessage)
	e.POST("/api/client/fetch_messages", h.fetchMessages)
	e.POST("/api/internal/map_sms_to_matrix", h.postMapping)
	e.GET("/api/internal/map_sms_to_matrix", h.getMapping)
}

type handler struct {
	svc        *service.MessageService
	adminToken string
}

func (h handler) sendMessage(c echo.Context) error {
	var req models.SendMessageRequest
	if err := c.Bind(&req); err != nil {
		logger.Warn().Str("endpoint", "send_message").Err(err).Msg("invalid request payload")
		return echo.NewHTTPError(http.StatusBadRequest, "invalid payload")
	}

	logger.Debug().Str("endpoint", "send_message").Str("from", req.From).Str("to", req.SMSTo).Msg("processing send message request")

	resp, err := h.svc.SendMessage(c.Request().Context(), &req)
	if err != nil {
		logger.Error().Str("endpoint", "send_message").Str("from", req.From).Str("to", req.SMSTo).Err(err).Msg("failed to send message")
		return mapServiceError(err)
	}

	logger.Info().Str("endpoint", "send_message").Str("from", req.From).Str("to", req.SMSTo).Str("sms_id", resp.SMSID).Msg("message sent successfully")
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

	logger.Info().Str("endpoint", "fetch_messages").Str("username", req.Username).Int("received", len(resp.ReceivedSMSS)).Int("sent", len(resp.SentSMSS)).Msg("messages fetched successfully")
	return c.JSON(http.StatusOK, resp)
}

func (h handler) postMapping(c echo.Context) error {
	if err := h.ensureAdminAccess(c); err != nil {
		return err
	}

	var req models.MappingRequest
	if err := c.Bind(&req); err != nil {
		logger.Warn().Str("endpoint", "post_mapping").Err(err).Msg("invalid request payload")
		return echo.NewHTTPError(http.StatusBadRequest, "invalid payload")
	}

	logger.Debug().Str("endpoint", "post_mapping").Str("sms_number", req.SMSNumber).Str("room_id", req.RoomID).Msg("saving mapping")

	resp, err := h.svc.SaveMapping(&req)
	if err != nil {
		logger.Error().Str("endpoint", "post_mapping").Str("sms_number", req.SMSNumber).Err(err).Msg("failed to save mapping")
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	logger.Info().Str("endpoint", "post_mapping").Str("sms_number", req.SMSNumber).Str("room_id", req.RoomID).Msg("mapping saved successfully")
	return c.JSON(http.StatusOK, resp)
}

func (h handler) getMapping(c echo.Context) error {
	if err := h.ensureAdminAccess(c); err != nil {
		return err
	}

	smsNumber := strings.TrimSpace(c.QueryParam("sms_number"))
	if smsNumber == "" {
		logger.Debug().Str("endpoint", "get_mapping").Msg("listing all mappings")
		// return full mappings list when sms_number is not provided
		respList, err := h.svc.ListMappings()
		if err != nil {
			logger.Error().Str("endpoint", "get_mapping").Err(err).Msg("failed to list mappings")
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		logger.Info().Str("endpoint", "get_mapping").Int("count", len(respList)).Msg("listed all mappings")
		return c.JSON(http.StatusOK, respList)
	}

	logger.Debug().Str("endpoint", "get_mapping").Str("sms_number", smsNumber).Msg("looking up mapping")

	resp, err := h.svc.LookupMapping(smsNumber)
	if err != nil {
		if errors.Is(err, service.ErrMappingNotFound) {
			logger.Warn().Str("endpoint", "get_mapping").Str("sms_number", smsNumber).Msg("mapping not found")
			return echo.NewHTTPError(http.StatusNotFound, err.Error())
		}
		logger.Error().Str("endpoint", "get_mapping").Str("sms_number", smsNumber).Err(err).Msg("failed to lookup mapping")
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	logger.Info().Str("endpoint", "get_mapping").Str("sms_number", smsNumber).Str("room_id", resp.RoomID).Msg("mapping found")
	return c.JSON(http.StatusOK, resp)
}

func (h handler) ensureAdminAccess(c echo.Context) error {
	if h.adminToken == "" {
		return echo.NewHTTPError(http.StatusInternalServerError, "admin token not configured")
	}
	if !isLocalhost(c.RealIP()) {
		return echo.NewHTTPError(http.StatusForbidden, "mapping API only available from localhost")
	}
	token := c.Request().Header.Get(adminTokenHeader)
	if token == "" || token != h.adminToken {
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid admin token")
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

func isLocalhost(ip string) bool {
	trimmed := ip
	if colon := strings.LastIndex(trimmed, ":"); colon != -1 {
		trimmed = trimmed[:colon]
	}
	switch trimmed {
	case "127.0.0.1", "::1", "localhost":
		return true
	default:
		return false
	}
}
