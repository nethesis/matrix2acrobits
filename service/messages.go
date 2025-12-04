package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nethesis/matrix2acrobits/logger"
	"github.com/nethesis/matrix2acrobits/matrix"
	"github.com/nethesis/matrix2acrobits/models"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

var (
	ErrAuthentication   = errors.New("matrix authentication failed")
	ErrInvalidRecipient = errors.New("recipient is not resolvable to a Matrix user or room")
	ErrMappingNotFound  = errors.New("mapping not found")
	ErrInvalidSender    = errors.New("sender is not resolvable to a Matrix user")
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
// Only 1-to-1 direct messaging is supported.
// Both sender and recipient are resolved to Matrix user IDs using local mappings if necessary.
func (s *MessageService) SendMessage(ctx context.Context, req *models.SendMessageRequest) (*models.SendMessageResponse, error) {
	senderStr := strings.TrimSpace(req.From)
	if senderStr == "" {
		logger.Warn().Msg("send message: empty sender")
		return nil, ErrInvalidSender
	}

	recipientStr := strings.TrimSpace(req.SMSTo)
	if recipientStr == "" {
		logger.Warn().Msg("send message: empty recipient")
		return nil, ErrInvalidRecipient
	}

	// Resolve sender to a valid Matrix user ID
	sender := s.resolveMatrixUser(senderStr)
	if sender == "" {
		logger.Warn().Str("sender", senderStr).Msg("sender is not a valid Matrix user ID")
		return nil, ErrInvalidSender
	}

	// Resolve recipient to a valid Matrix user ID
	recipient := s.resolveMatrixUser(recipientStr)
	if recipient == "" {
		logger.Warn().Str("recipient", recipientStr).Msg("recipient is not a valid Matrix user ID")
		return nil, ErrInvalidRecipient
	}

	logger.Debug().Str("sender", string(sender)).Str("recipient", string(recipient)).Msg("resolved sender and recipient to Matrix user IDs")

	// For 1-to-1 messaging, ensure a direct room exists between sender and recipient
	roomID, err := s.ensureDirectRoom(ctx, sender, recipient)
	if err != nil {
		logger.Error().Str("sender", string(sender)).Str("recipient", string(recipient)).Err(err).Msg("failed to ensure direct room")
		return nil, err
	}

	logger.Debug().Str("sender", string(sender)).Str("recipient", string(recipient)).Str("room_id", string(roomID)).Msg("sending message to direct room")

	// Ensure the sender is a member of the room (in case join failed during room creation)
	_, err = s.matrixClient.JoinRoom(ctx, sender, roomID)
	if err != nil {
		logger.Error().Str("sender", string(sender)).Str("room_id", string(roomID)).Err(err).Msg("failed to join room")
		return nil, fmt.Errorf("send message: %w", err)
	}

	content := &event.MessageEventContent{
		MsgType: event.MsgText,
		Body:    req.SMSBody,
	}

	resp, err := s.matrixClient.SendMessage(ctx, sender, roomID, content)
	if err != nil {
		logger.Error().Str("sender", string(sender)).Str("room_id", string(roomID)).Err(err).Msg("failed to send message")
		return nil, fmt.Errorf("send message: %w", mapAuthErr(err))
	}

	logger.Debug().Str("sender", string(sender)).Str("room_id", string(roomID)).Str("event_id", string(resp.EventID)).Msg("message sent successfully")
	return &models.SendMessageResponse{SMSID: string(resp.EventID)}, nil
}

// FetchMessages translates Matrix /sync into the Acrobits fetch_messages response.
func (s *MessageService) FetchMessages(ctx context.Context, req *models.FetchMessagesRequest) (*models.FetchMessagesResponse, error) {
	// The user to impersonate is taken from the 'Username' field.
	userID := id.UserID(req.Username)
	if userID == "" {
		logger.Warn().Msg("fetch messages: empty user ID")
		return nil, ErrAuthentication
	}

	logger.Debug().Str("user_id", string(userID)).Str("since", req.LastID).Msg("syncing messages from matrix")

	resp, err := s.matrixClient.Sync(ctx, userID, req.LastID)
	if err != nil {
		logger.Error().Str("user_id", string(userID)).Err(err).Msg("matrix sync failed")
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

	logger.Debug().Str("user_id", string(userID)).Int("received_count", len(received)).Int("sent_count", len(sent)).Msg("processed sync messages")

	return &models.FetchMessagesResponse{
		Date:         s.now().UTC().Format(time.RFC3339),
		ReceivedSMSS: received,
		SentSMSS:     sent,
	}, nil
}

// resolveMatrixUser resolves an identifier to a valid Matrix user ID.
// If the identifier is already a valid Matrix user ID (starts with @), it's returned as-is.
// Otherwise, it tries to look up the identifier in the mapping store (e.g., phone number to user).
// Returns empty string if the identifier cannot be resolved.
func (s *MessageService) resolveMatrixUser(identifier string) id.UserID {
	identifier = strings.TrimSpace(identifier)

	// If it's already a valid Matrix user ID, return it
	if strings.HasPrefix(identifier, "@") {
		return id.UserID(identifier)
	}

	// Try to look up in mappings (e.g., phone number to Matrix user)
	if entry, ok := s.getMapping(identifier); ok && entry.MatrixID != "" {
		logger.Debug().Str("original_identifier", identifier).Str("resolved_user", entry.MatrixID).Msg("identifier resolved from mapping")
		return id.UserID(entry.MatrixID)
	}

	// Could not resolve
	logger.Warn().Str("identifier", identifier).Msg("identifier could not be resolved to a Matrix user ID")
	return ""
}

func (s *MessageService) ensureDirectRoom(ctx context.Context, actingUserID, targetUserID id.UserID) (id.RoomID, error) {
	// Use both user IDs to create a consistent mapping key for the DM.
	key := fmt.Sprintf("%s|%s", actingUserID, targetUserID)
	if actingUserID > targetUserID {
		key = fmt.Sprintf("%s|%s", targetUserID, actingUserID)
	}

	logger.Debug().Str("acting_user", string(actingUserID)).Str("target_user", string(targetUserID)).Msg("ensuring direct room exists")

	if entry, ok := s.getMapping(key); ok && entry.RoomID != "" {
		logger.Debug().Str("acting_user", string(actingUserID)).Str("target_user", string(targetUserID)).Str("room_id", string(entry.RoomID)).Msg("existing DM room found in cache")
		return entry.RoomID, nil
	}
	if !strings.HasPrefix(string(targetUserID), "@") {
		logger.Warn().Str("acting_user", string(actingUserID)).Str("target_user", string(targetUserID)).Msg("invalid target user ID format")
		return "", ErrInvalidRecipient
	}

	logger.Info().Str("acting_user", string(actingUserID)).Str("target_user", string(targetUserID)).Msg("creating new direct room")

	resp, err := s.matrixClient.CreateDirectRoom(ctx, actingUserID, targetUserID)
	if err != nil {
		logger.Error().Str("acting_user", string(actingUserID)).Str("target_user", string(targetUserID)).Err(err).Msg("failed to create direct room")
		return "", err
	}

	// Ensure the target user joins the room so they can see it in their sync
	_, err = s.matrixClient.JoinRoom(ctx, targetUserID, resp.RoomID)
	if err != nil {
		logger.Error().Str("acting_user", string(actingUserID)).Str("target_user", string(targetUserID)).Str("room_id", string(resp.RoomID)).Err(err).Msg("target user failed to join room")
		return "", fmt.Errorf("join room as target user: %w", err)
	}

	entry := mappingEntry{
		SMSNumber: key, // Use the combined key for internal storage
		MatrixID:  string(targetUserID),
		RoomID:    resp.RoomID,
		UpdatedAt: s.now(),
	}
	entry = s.setMapping(entry)
	logger.Info().Str("acting_user", string(actingUserID)).Str("target_user", string(targetUserID)).Str("room_id", string(resp.RoomID)).Msg("direct room created and cached")
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
		logger.Warn().Msg("attempted to set mapping with empty key")
		return entry
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry.UpdatedAt = s.now()
	s.mappings[normalized] = entry
	logger.Debug().Str("key", entry.SMSNumber).Str("room_id", string(entry.RoomID)).Msg("mapping stored")
	return entry
}

// LookupMapping returns the currently stored mapping for a given key (phone number or user pair).
func (s *MessageService) LookupMapping(key string) (*models.MappingResponse, error) {
	entry, ok := s.getMapping(key)
	if !ok {
		return nil, ErrMappingNotFound
	}
	return s.buildMappingResponse(entry), nil
}

// ListMappings returns all stored mappings.
func (s *MessageService) ListMappings() ([]*models.MappingResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*models.MappingResponse, 0, len(s.mappings))
	for _, entry := range s.mappings {
		out = append(out, s.buildMappingResponse(entry))
	}
	return out, nil
}

// SaveMapping persists a new mapping via the admin API.
// For 1-to-1 messaging, this maps a key (phone number or identifier) to a direct room.
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

// LoadMappingsFromFile loads mappings from a JSON file in the format:
//
//	[
//	  {"sms_number": "91201", "matrix_id": "@giacomo:synapse.gs.nethserver.net", "room_id": "!giacomo-room:synapse.gs.nethserver.net"},
//	  {"sms_number": "91202", "matrix_id": "@mario:synapse.gs.nethserver.net", "room_id": "!mario-room:synapse.gs.nethserver.net"}
//	]
//
// This is typically called at startup if MAPPING_FILE environment variable is set.
func (s *MessageService) LoadMappingsFromFile(filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read mapping file: %w", err)
	}

	var mappingArray []models.MappingRequest
	if err := json.Unmarshal(data, &mappingArray); err != nil {
		return fmt.Errorf("failed to parse mapping file: %w", err)
	}

	for _, req := range mappingArray {
		if req.SMSNumber == "" {
			logger.Warn().Msg("skipping mapping with empty sms_number")
			continue
		}
		entry := mappingEntry{
			SMSNumber: req.SMSNumber,
			MatrixID:  req.MatrixID,
			RoomID:    id.RoomID(req.RoomID),
			UpdatedAt: s.now(),
		}
		s.setMapping(entry)
	}

	logger.Info().Int("count", len(mappingArray)).Str("file", filePath).Msg("mappings loaded from file")
	return nil
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
	if errors.Is(err, mautrix.MUnknownToken) || errors.Is(err, mautrix.MMissingToken) {
		return fmt.Errorf("%w", ErrAuthentication)
	}
	return err
}

// isPhoneNumber checks if a string looks like a phone number.
// Returns true if the string contains only digits, spaces, hyphens, plus signs, and/or parentheses.
func isPhoneNumber(s string) bool {
	if s == "" {
		return false
	}
	trimmed := strings.TrimSpace(s)
	// Check if it starts with @ or ! or #, indicating it's a Matrix ID/room ID/alias
	if strings.HasPrefix(trimmed, "@") || strings.HasPrefix(trimmed, "!") || strings.HasPrefix(trimmed, "#") {
		return false
	}
	// A phone number contains only digits and optional formatting characters
	for _, r := range trimmed {
		if !isPhoneNumberRune(r) {
			return false
		}
	}
	// Must contain at least one digit
	for _, r := range trimmed {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

// isPhoneNumberRune checks if a rune is a valid character in a phone number
func isPhoneNumberRune(r rune) bool {
	return (r >= '0' && r <= '9') || r == ' ' || r == '-' || r == '+' || r == '(' || r == ')'
}
