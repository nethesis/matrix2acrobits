package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/nethesis/matrix2acrobits/db"
	"github.com/nethesis/matrix2acrobits/logger"
	"github.com/nethesis/matrix2acrobits/models"
)

const acrobitsPushURL = "https://pnm.cloudsoftphone.com/pnm2/send"

var (
	ErrPushTokenNotFound = errors.New("push token not found")
	ErrPushFailed        = errors.New("push notification failed")
)

// PushService handles Matrix push notifications and forwards them to Acrobits
type PushService struct {
	pushTokenDB *db.Database
	httpClient  *http.Client
}

// NewPushService creates a new push notification service
func NewPushService(pushTokenDB *db.Database) *PushService {
	return &PushService{
		pushTokenDB: pushTokenDB,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// HandleMatrixPushNotification processes a Matrix push notification and forwards it to Acrobits
func (s *PushService) HandleMatrixPushNotification(ctx context.Context, req *models.MatrixPushNotifyRequest) (*models.MatrixPushNotifyResponse, error) {
	logger.Debug().Interface("notification", req.Notification).Msg("processing matrix push notification")

	rejected := make([]string, 0)

	// Process each device in the notification
	for _, device := range req.Notification.Devices {
		logger.Debug().
			Str("pushkey", device.Pushkey).
			Str("app_id", device.AppID).
			Msg("processing device for push notification")

		// Look up the push token in our database using the pushkey
		token, err := s.pushTokenDB.GetPushTokenByPushkey(device.Pushkey)
		if err != nil {
			logger.Error().
				Str("pushkey", device.Pushkey).
				Err(err).
				Msg("error looking up push token in database")
			rejected = append(rejected, device.Pushkey)
			continue
		}
		if token == nil {
			logger.Warn().
				Str("pushkey", device.Pushkey).
				Msg("push token not found in database, marking as rejected")
			rejected = append(rejected, device.Pushkey)
			continue
		}

		// Translate Matrix notification to Acrobits format
		acrobitsReq := s.translateToAcrobits(req.Notification, device, token)

		// Send to Acrobits
		if err := s.sendToAcrobits(ctx, acrobitsReq); err != nil {
			logger.Error().
				Str("pushkey", device.Pushkey).
				Str("selector", token.Selector).
				Err(err).
				Msg("failed to send push notification to Acrobits")

			// If Acrobits returns 404, the token is invalid
			if errors.Is(err, ErrPushTokenNotFound) {
				rejected = append(rejected, device.Pushkey)
			}
		} else {
			logger.Info().
				Str("pushkey", device.Pushkey).
				Str("selector", token.Selector).
				Str("event_id", req.Notification.EventID).
				Msg("push notification sent successfully to Acrobits")
		}
	}

	return &models.MatrixPushNotifyResponse{
		Rejected: rejected,
	}, nil
}

// translateToAcrobits converts a Matrix notification to Acrobits push format
func (s *PushService) translateToAcrobits(notification models.MatrixNotification, device models.MatrixDevice, token *db.PushToken) *models.AcrobitsPushRequest {
	req := &models.AcrobitsPushRequest{
		Verb:        "NotifyTextMessage",
		AppID:       token.AppIDMsgs,
		DeviceToken: token.TokenMsgs,
		Selector:    token.Selector,
	}

	// Extract message body from content
	if notification.Content != nil {
		if body, ok := notification.Content["body"].(string); ok {
			req.Message = body
		}
		if msgtype, ok := notification.Content["msgtype"].(string); ok {
			req.ContentType = msgtype
		}
	}

	// Set badge count from unread messages
	if notification.Counts != nil {
		req.Badge = notification.Counts.Unread
	}

	// Set sender information
	if notification.SenderDisplayName != "" {
		req.UserDisplayName = notification.SenderDisplayName
	} else if notification.Sender != "" {
		req.UserDisplayName = notification.Sender
	}
	req.UserName = notification.Sender

	// Set message ID for deduplication
	if notification.EventID != "" {
		req.ID = notification.EventID
	}

	// Use room_id as thread identifier
	if notification.RoomID != "" {
		req.ThreadID = notification.RoomID
	}

	// Determine sound from tweaks
	if device.Tweaks != nil {
		if sound, ok := device.Tweaks["sound"].(string); ok && sound != "" {
			req.Sound = sound
		} else {
			req.Sound = "default"
		}
	} else {
		req.Sound = "default"
	}

	logger.Debug().
		Interface("acrobits_request", req).
		Msg("translated Matrix notification to Acrobits format")

	return req
}

// sendToAcrobits sends a push notification to the Acrobits PNM service
func (s *PushService) sendToAcrobits(ctx context.Context, req *models.AcrobitsPushRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal acrobits request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", acrobitsPushURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create http request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	logger.Debug().
		Str("url", acrobitsPushURL).
		Str("selector", req.Selector).
		Msg("sending push notification to Acrobits PNM")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send request to acrobits: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read acrobits response: %w", err)
	}

	var acrobitsResp models.AcrobitsPushResponse
	if err := json.Unmarshal(respBody, &acrobitsResp); err != nil {
		logger.Warn().
			Str("response_body", string(respBody)).
			Err(err).
			Msg("failed to parse acrobits response")
		return fmt.Errorf("failed to parse acrobits response: %w", err)
	}

	logger.Debug().
		Int("code", acrobitsResp.Code).
		Str("response", acrobitsResp.Response).
		Msg("received response from Acrobits PNM")

	// Check if the push was successful
	if acrobitsResp.Code != 200 {
		// 404 means the device token is no longer valid
		if acrobitsResp.Code == 404 || strings.Contains(acrobitsResp.Response, "404") {
			return ErrPushTokenNotFound
		}
		return fmt.Errorf("%w: code=%d, response=%s", ErrPushFailed, acrobitsResp.Code, acrobitsResp.Response)
	}

	return nil
}
