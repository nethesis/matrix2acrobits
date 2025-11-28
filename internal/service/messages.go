package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gsanchietti/matrix2acrobits/internal/matrix"
	"github.com/gsanchietti/matrix2acrobits/pkg/models"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

var (
	ErrAuthentication   = errors.New("matrix authentication failed")
	ErrInvalidRecipient = errors.New("recipient is not resolvable to a Matrix room")
	ErrMappingNotFound  = errors.New("mapping not found")
)

// MessageService handles sending/fetching messages plus the mapping store.
type MessageService struct {
	matrixClient *matrix.MatrixClient
	now          func() time.Time

	mu       sync.RWMutex
	mappings map[string]mappingEntry
}

type mappingEntry struct {
	SMSNumber string
	MatrixID  string
	RoomID    id.RoomID
	UpdatedAt time.Time
}

// NewMessageService wires the provided Matrix client into the service layer.
func NewMessageService(matrixClient *matrix.MatrixClient) *MessageService {
	return &MessageService{
		matrixClient: matrixClient,
		now:          time.Now,
		mappings:     make(map[string]mappingEntry),
	}
}

// SendMessage translates an Acrobits send_message request into Matrix /send.
func (s *MessageService) SendMessage(ctx context.Context, req *models.SendMessageRequest) (*models.SendMessageResponse, error) {
	// The user to impersonate is taken from the 'From' field.
	userID := id.UserID(req.From)
	if userID == "" {
		return nil, ErrAuthentication
	}

	roomID, err := s.resolveRecipient(ctx, userID, req.SMSTo)
	if err != nil {
		return nil, err
	}

	content := &event.MessageEventContent{
		MsgType: event.MsgText,
		Body:    req.SMSBody,
	}

	resp, err := s.matrixClient.SendMessage(ctx, userID, roomID, content)
	if err != nil {
		return nil, fmt.Errorf("send message: %w", mapAuthErr(err))
	}

	return &models.SendMessageResponse{SMSID: string(resp.EventID)}, nil
}

// FetchMessages translates Matrix /sync into the Acrobits fetch_messages response.
func (s *MessageService) FetchMessages(ctx context.Context, req *models.FetchMessagesRequest) (*models.FetchMessagesResponse, error) {
	// The user to impersonate is taken from the 'Username' field.
	userID := id.UserID(req.Username)
	if userID == "" {
		return nil, ErrAuthentication
	}

	resp, err := s.matrixClient.Sync(ctx, userID, req.LastID)
	if err != nil {
		return nil, fmt.Errorf("sync messages: %w", mapAuthErr(err))
	}

	received, sent := make([]models.Message, 0, 8), make([]models.Message, 0, 8)
	for _, room := range resp.Rooms.Join {
		for _, evt := range room.Timeline.Events {
			if evt.Type != event.EventMessage {
				continue
			}
			msg := convertEvent(evt)
			if isSentBy(msg.Sender, req.Username) {
				sent = append(sent, msg)
			} else {
				received = append(received, msg)
			}
		}
	}

	return &models.FetchMessagesResponse{
		Date:         s.now().UTC().Format(time.RFC3339),
		ReceivedSMSS: received,
		SentSMSS:     sent,
	}, nil
}

func (s *MessageService) resolveRecipient(ctx context.Context, actingUserID id.UserID, raw string) (id.RoomID, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", ErrInvalidRecipient
	}
	// If it looks like a RoomID, use it directly.
	if strings.HasPrefix(trimmed, "!") {
		return id.RoomID(trimmed), nil
	}
	// If it looks like a UserID, ensure a DM exists and use that room.
	if strings.HasPrefix(trimmed, "@") {
		return s.ensureDirectRoom(ctx, actingUserID, id.UserID(trimmed))
	}
	// If it's a room alias, resolve it.
	if strings.HasPrefix(trimmed, "#") {
		resp, err := s.matrixClient.ResolveRoomAlias(ctx, trimmed)
		if err != nil {
			return "", fmt.Errorf("resolve room alias: %w", err)
		}
		return resp.RoomID, nil
	}
	// Otherwise, check our internal mapping for a phone number.
	if entry, ok := s.getMapping(trimmed); ok && entry.RoomID != "" {
		return entry.RoomID, nil
	}
	return "", ErrInvalidRecipient
}

func (s *MessageService) ensureDirectRoom(ctx context.Context, actingUserID, targetUserID id.UserID) (id.RoomID, error) {
	// Use both user IDs to create a consistent mapping key for the DM.
	key := fmt.Sprintf("%s|%s", actingUserID, targetUserID)
	if actingUserID > targetUserID {
		key = fmt.Sprintf("%s|%s", targetUserID, actingUserID)
	}

	if entry, ok := s.getMapping(key); ok && entry.RoomID != "" {
		return entry.RoomID, nil
	}
	if !strings.HasPrefix(string(targetUserID), "@") {
		return "", ErrInvalidRecipient
	}

	resp, err := s.matrixClient.CreateDirectRoom(ctx, actingUserID, targetUserID)
	if err != nil {
		return "", err
	}

	// Ensure the target user joins the room so they can see it in their sync
	_, err = s.matrixClient.JoinRoom(ctx, targetUserID, resp.RoomID)
	if err != nil {
		return "", fmt.Errorf("join room as target user: %w", err)
	}

	entry := mappingEntry{
		SMSNumber: key, // Use the combined key for internal storage
		MatrixID:  string(targetUserID),
		RoomID:    resp.RoomID,
		UpdatedAt: s.now(),
	}
	entry = s.setMapping(entry)
	return entry.RoomID, nil
}

func (s *MessageService) getMapping(key string) (mappingEntry, bool) {
	normalized := normalizeMappingKey(key)
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.mappings[normalized]
	return entry, ok
}

func (s *MessageService) setMapping(entry mappingEntry) mappingEntry {
	entry.SMSNumber = strings.TrimSpace(entry.SMSNumber)
	normalized := normalizeMappingKey(entry.SMSNumber)
	if normalized == "" {
		return entry
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry.UpdatedAt = s.now()
	s.mappings[normalized] = entry
	return entry
}

// LookupMapping returns the currently stored mapping for a given sms number.
func (s *MessageService) LookupMapping(smsNumber string) (*models.MappingResponse, error) {
	entry, ok := s.getMapping(smsNumber)
	if !ok {
		return nil, ErrMappingNotFound
	}
	return s.buildMappingResponse(entry), nil
}

// SaveMapping persists a new SMS-to-Matrix mapping via the admin API.
func (s *MessageService) SaveMapping(req *models.MappingRequest) (*models.MappingResponse, error) {
	smsNumber := strings.TrimSpace(req.SMSNumber)
	if smsNumber == "" {
		return nil, errors.New("sms_number is required")
	}
	roomID := strings.TrimSpace(req.RoomID)
	if roomID == "" {
		return nil, errors.New("room_id is required")
	}

	entry := mappingEntry{
		SMSNumber: smsNumber,
		MatrixID:  strings.TrimSpace(req.MatrixID),
		RoomID:    id.RoomID(roomID),
		UpdatedAt: s.now(),
	}
	entry = s.setMapping(entry)
	return s.buildMappingResponse(entry), nil
}

func (s *MessageService) buildMappingResponse(entry mappingEntry) *models.MappingResponse {
	return &models.MappingResponse{
		SMSNumber: entry.SMSNumber,
		MatrixID:  entry.MatrixID,
		RoomID:    string(entry.RoomID),
		UpdatedAt: entry.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func normalizeMatrixID(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeMappingKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func isSentBy(sender, username string) bool {
	return strings.EqualFold(normalizeMatrixID(sender), normalizeMatrixID(username))
}

func convertEvent(evt *event.Event) models.Message {
	body := ""
	if b, ok := evt.Content.Raw["body"].(string); ok {
		body = b
	}
	contentType := "text/plain"
	if mt, ok := evt.Content.Raw["msgtype"].(string); ok {
		contentType = mt
	}

	sendingDate := time.UnixMilli(evt.Timestamp).UTC().Format(time.RFC3339)
	return models.Message{
		SMSID:       string(evt.ID),
		SendingDate: sendingDate,
		Sender:      string(evt.Sender),
		Recipient:   string(evt.RoomID),
		SMSText:     body,
		ContentType: contentType,
		StreamID:    string(evt.RoomID),
	}
}

func mapAuthErr(err error) error {
	if errors.Is(err, ErrAuthentication) {
		return err
	}
	if errors.Is(err, mautrix.MUnknownToken) || errors.Is(err, mautrix.MMissingToken) || errors.Is(err, mautrix.MForbidden) {
		return fmt.Errorf("%w", ErrAuthentication)
	}
	return err
}
