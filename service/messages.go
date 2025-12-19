package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nethesis/matrix2acrobits/db"
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
	pushTokenDB  *db.Database
	now          func() time.Time
	proxyURL     string // Public-facing URL of this proxy (e.g., https://matrix.example.com)
	// External auth configuration
	extAuthURL     string
	extAuthTimeout time.Duration
	authClient     *HTTPAuthClient
	// Homeserver host used to build Matrix IDs from auth response
	homeserverHost string

	mu                sync.RWMutex
	mappings          map[string]mappingEntry
	subNumberMappings map[int]string    // subNumber -> MatrixID
	batchTokens       map[string]string // userID -> next_batch token

	// Caches for room resolution
	roomAliasCache       *RoomAliasCache
	roomAliasesCache     *RoomAliasesCache
	roomParticipantCache *RoomParticipantCache
}

type mappingEntry struct {
	Number     int
	MatrixID   string
	RoomID     id.RoomID
	UserName   string
	SubNumbers []int
	UpdatedAt  time.Time
}

// NewMessageService wires the provided Matrix client and push token database into the service layer.
func NewMessageService(matrixClient *matrix.MatrixClient, pushTokenDB *db.Database, cfg *Config) *MessageService {
	logger.Debug().Int("cache_ttl_seconds", cfg.CacheTTLSeconds).Msg("initialized message service with cache TTL")

	// External auth configuration
	if cfg.ExtAuthURL == "" {
		logger.Warn().Msg("EXT_AUTH_URL not set!")
	}

	return &MessageService{
		matrixClient:         matrixClient,
		pushTokenDB:          pushTokenDB,
		now:                  time.Now,
		proxyURL:             cfg.ProxyURL,
		mappings:             make(map[string]mappingEntry),
		subNumberMappings:    make(map[int]string),
		batchTokens:          make(map[string]string),
		roomAliasCache:       NewRoomAliasCache(cfg.CacheTTL),
		roomAliasesCache:     NewRoomAliasesCache(cfg.CacheTTL),
		roomParticipantCache: NewRoomParticipantCache(cfg.CacheTTL),
		extAuthURL:           cfg.ExtAuthURL,
		extAuthTimeout:       cfg.ExtAuthTimeout,
		authClient:           NewHTTPAuthClient(cfg.ExtAuthURL, cfg.ExtAuthTimeout, cfg.CacheTTL),
		homeserverHost:       cfg.MatrixHomeserverHost,
	}
}

// authenticateAndPersistMappings validates credentials with the external auth service
// and persists all returned mappings to the local store.
// Returns ErrAuthentication if validation fails.
func (s *MessageService) authenticateAndPersistMappings(ctx context.Context, username, password string) error {
	mappings, ok, err := s.authClient.Validate(ctx, username, password, s.homeserverHost)
	if err != nil {
		if !ok {
			logger.Warn().Str("username", username).Msg("external auth failed: unauthorized")
			return ErrAuthentication
		}
		logger.Error().Err(err).Msg("external auth request failed")
		return fmt.Errorf("external auth request failed: %w", err)
	}

	// Persist all mappings returned by auth
	for _, mapReq := range mappings {
		if _, err := s.SaveMapping(mapReq); err != nil {
			logger.Error().Err(err).Msg("failed to save mapping from external auth response")
			return fmt.Errorf("failed to save mapping: %w", err)
		}
	}
	return nil
}

// SendMessage translates an Acrobits send_message request into Matrix /send.
// Only 1-to-1 direct messaging is supported.
// Both sender and recipient are resolved to Matrix user IDs using local mappings if necessary.
func (s *MessageService) SendMessage(ctx context.Context, req *models.SendMessageRequest) (*models.SendMessageResponse, error) {
	// Debug full request
	logger.Debug().Interface("request", req).Msg("send message request received")

	if err := s.authenticateAndPersistMappings(context.Background(), req.From, req.Password); err != nil {
		return nil, err
	}

	// Resolve sender to Matrix ID using mappings
	senderMatrix := s.resolveMatrixUser(req.From)
	if senderMatrix == "" {
		logger.Warn().Str("from", req.From).Msg("resolved to empty Matrix user ID")
		return nil, ErrAuthentication
	}

	if req.To == "" {
		logger.Warn().Msg("send message: empty recipient")
		return nil, ErrInvalidRecipient
	}

	// Require password for send_message requests
	if strings.TrimSpace(req.Password) == "" {
		logger.Warn().Msg("send message: empty password")
		return nil, ErrAuthentication
	}

	// Try to resolve as Matrix user ID or mapping
	recipientMatrix := s.resolveMatrixUser(req.To)
	if recipientMatrix == "" {
		logger.Warn().Str("recipient", req.To).Msg("recipient is not a valid Matrix user ID or room ID")
		return nil, ErrInvalidRecipient
	}

	logger.Debug().Str("sender", string(senderMatrix)).Str("recipient", string(recipientMatrix)).Msg("resolved sender and recipient to Matrix user IDs")

	// For 1-to-1 messaging, ensure a direct room exists between sender and recipient
	var err error
	roomID, err := s.ensureDirectRoom(ctx, senderMatrix, recipientMatrix)
	if err != nil {
		logger.Error().Str("sender", string(senderMatrix)).Str("recipient", string(recipientMatrix)).Err(err).Msg("failed to ensure direct room")
		return nil, err
	}

	if recipientMatrix != "" {
		logger.Debug().Str("sender", string(senderMatrix)).Str("recipient", string(recipientMatrix)).Str("room_id", string(roomID)).Msg("sending message to direct room")
	} else {
		logger.Debug().Str("sender", string(senderMatrix)).Str("room_id", string(roomID)).Msg("sending message to room")
	}
	// Ensure the sender is a member of the room (in case join failed during room creation)
	_, err = s.matrixClient.JoinRoom(ctx, senderMatrix, roomID)
	if err != nil {
		logger.Error().Str("sender", string(senderMatrix)).Str("room_id", string(roomID)).Err(err).Msg("failed to join room")
		return nil, fmt.Errorf("send message: %w", err)
	}

	content := &event.MessageEventContent{
		MsgType: event.MsgText,
		Body:    req.Body,
	}

	resp, err := s.matrixClient.SendMessage(ctx, senderMatrix, roomID, content)
	if err != nil {
		logger.Error().Str("sender", string(senderMatrix)).Str("room_id", string(roomID)).Err(err).Msg("failed to send message")
		return nil, fmt.Errorf("send message: %w", mapAuthErr(err))
	}

	logger.Debug().Str("sender", string(senderMatrix)).Str("room_id", string(roomID)).Str("event_id", string(resp.EventID)).Msg("message sent successfully")
	return &models.SendMessageResponse{ID: string(resp.EventID)}, nil
}

// FetchMessages translates Matrix /sync into the Acrobits fetch_messages response.
func (s *MessageService) FetchMessages(ctx context.Context, req *models.FetchMessagesRequest) (*models.FetchMessagesResponse, error) {
	logger.Debug().Interface("request", req).Msg("fetch messages request received")

	// Authenticate user using external auth (require password)
	userName := strings.TrimSpace(req.Username)
	if userName == "" {
		logger.Warn().Msg("fetch messages: empty username")
		return nil, ErrAuthentication
	}

	// If username is already a Matrix ID, skip external auth
	if !strings.HasPrefix(userName, "@") {
		// Not a Matrix ID - check if we have a mapping for it
		resolvedMatrix := s.resolveMatrixUser(userName)
		if resolvedMatrix == "" {
			// No mapping exists - try external auth if password is provided
			if strings.TrimSpace(req.Password) == "" {
				logger.Warn().Str("username", userName).Msg("username not resolvable and no password provided")
				return nil, ErrAuthentication
			}
			if err := s.authenticateAndPersistMappings(ctx, userName, req.Password); err != nil {
				return nil, err
			}
		} else {
			logger.Debug().Str("username", userName).Str("resolved_matrix_id", string(resolvedMatrix)).Msg("username resolved from existing mapping, skipping external auth")
		}
	} else {
		logger.Debug().Str("username", userName).Msg("username is already a Matrix ID, skipping external auth")
	}

	// Resolve username to Matrix ID using mappings
	userID := s.resolveMatrixUser(userName)
	if userID == "" {
		logger.Warn().Str("username", userName).Msg("resolved to empty Matrix user ID")
		return nil, ErrAuthentication
	}

	logger.Debug().Str("user_id", string(userID)).Msg("syncing messages from matrix")

	// Retrieve the last batch token for this user
	batchToken := s.getBatchToken(string(userID))
	logger.Debug().Str("user_id", string(userID)).Str("batch_token", batchToken).Msg("using batch token for incremental sync")

	resp, err := s.matrixClient.Sync(ctx, userID, batchToken)
	if err != nil {
		// If the token is invalid (e.g. expired or from a different session), retry with a full sync.
		if strings.Contains(err.Error(), "Invalid stream token") || strings.Contains(err.Error(), "M_UNKNOWN") {
			logger.Warn().Err(err).Msg("invalid stream token, retrying with full sync")
			s.clearBatchToken(string(userID))
			resp, err = s.matrixClient.Sync(ctx, userID, "")
		}
	}
	if err != nil {
		logger.Error().Str("user_id", string(userID)).Err(err).Msg("matrix sync failed")
		return nil, fmt.Errorf("sync messages: %w", mapAuthErr(err))
	}

	// Store the next_batch token for subsequent calls
	if resp.NextBatch != "" {
		s.setBatchToken(string(userID), resp.NextBatch)
		logger.Debug().Str("user_id", string(userID)).Str("next_batch", resp.NextBatch).Msg("stored next batch token")
	}

	received, sent := make([]models.SMS, 0, 8), make([]models.SMS, 0, 8)

	// Resolve the caller's identifier (e.g. "91201" -> "201")
	callerIdentifier := s.resolveMatrixIDToIdentifier(string(userID))

	for roomID, room := range resp.Rooms.Join {
		for _, evt := range room.Timeline.Events {
			if evt.Type != event.EventMessage {
				continue
			}

			eventRoomID := evt.RoomID
			if eventRoomID == "" {
				eventRoomID = roomID
			}

			logger.Debug().Str("event_id", string(evt.ID)).Str("room_id", string(eventRoomID)).Msg("processing message event")

			// Extract msgtype to determine message type
			msgtype := ""
			if mt, ok := evt.Content.Raw["msgtype"].(string); ok {
				msgtype = mt
			}

			body := ""
			if b, ok := evt.Content.Raw["body"].(string); ok {
				body = b
			}

			sms := models.SMS{
				SMSID:       string(evt.ID),
				SendingDate: time.UnixMilli(evt.Timestamp).UTC().Format(time.RFC3339),
				SMSText:     body,
				ContentType: "text/plain",
				StreamID:    string(roomID),
			}

			// Handle image messages
			if msgtype == "m.image" {
				logger.Debug().Str("event_id", string(evt.ID)).Msg("processing image message")

				// Extract image information from event content
				var mxcURL string
				var contentSize int
				var contentType string

				if url, ok := evt.Content.Raw["url"].(string); ok {
					mxcURL = url
				}

				// Get info from info object if present
				if info, ok := evt.Content.Raw["info"].(map[string]interface{}); ok {
					if size, ok := info["size"].(float64); ok {
						contentSize = int(size)
					}
					if ct, ok := info["mimetype"].(string); ok {
						contentType = ct
					}
				}

				// Download the image and generate preview
				if mxcURL != "" {
					imageData, detectedType, err := s.matrixClient.DownloadMedia(ctx, mxcURL)
					if err != nil {
						logger.Error().Str("mxc_url", mxcURL).Err(err).Msg("failed to download image, skipping attachment")
					} else {
						// Use detected type if not specified in event
						if contentType == "" {
							contentType = detectedType
						}

						// Generate preview
						preview, err := GenerateImagePreview(imageData)
						if err != nil {
							logger.Warn().Err(err).Msg("failed to generate image preview")
						}

						// Build the public URL for the image using proxy URL
						// Convert mxc://server/mediaId to https://proxy.url/_matrix/media/v3/download/server/mediaId
						publicURL := s.buildMediaURL(mxcURL)

						attachment := models.Attachment{
							ContentType: contentType,
							ContentURL:  publicURL,
							ContentSize: contentSize,
							Filename:    body, // Use body as filename if available
						}

						if preview != "" {
							attachment.Preview = &models.AttachmentPreview{
								ContentType: "image/jpeg",
								Content:     preview,
							}
						}

						// Create the FileTransfer object and marshal it to JSON for SMSText
						ft := models.FileTransfer{
							Body:        body,
							Attachments: []models.Attachment{attachment},
						}

						ftJSON, err := json.Marshal(ft)
						if err != nil {
							logger.Error().Err(err).Msg("failed to marshal file transfer JSON")
						} else {
							sms.SMSText = string(ftJSON)
							sms.ContentType = "application/x-acro-filetransfer+json"
						}

						logger.Debug().
							Str("event_id", string(evt.ID)).
							Str("mxc_url", mxcURL).
							Str("public_url", publicURL).
							Int("size", contentSize).
							Msg("processed image attachment")
					}
				}
			}

			// Determine if I sent the message
			senderMatrixID := string(evt.Sender)
			isSent := isSentBy(senderMatrixID, string(userID))

			// Remap sender to identifier (e.g. "202" or "91201")
			sms.Sender = string(s.resolveMatrixIDToIdentifier(senderMatrixID))

			// Determine Recipient
			if isSent {
				// I sent it. Recipient is the other person in the room.
				other := s.resolveRoomIDToOtherIdentifier(ctx, eventRoomID, string(userID))
				sms.Recipient = other
				sent = append(sent, sms)
			} else {
				// I received it. Recipient is me.
				sms.Recipient = callerIdentifier
				received = append(received, sms)
			}
			// Debug each processed message
			logger.Debug().
				Str("sender", sms.Sender).
				Str("recipient", sms.Recipient).
				Bool("is_sent", isSent).
				Interface("sms", sms).
				Msg("processed message from sync")

		}
	}

	logger.Debug().Str("user_id", string(userID)).Int("received_count", len(received)).Int("sent_count", len(sent)).Msg("processed sync messages")

	return &models.FetchMessagesResponse{
		Date:         s.now().UTC().Format(time.RFC3339),
		ReceivedSMSs: received,
		SentSMSs:     sent,
	}, nil
}

// resolveMatrixUser resolves an identifier to a valid Matrix user ID.
// If the identifier is already a valid Matrix user ID (starts with @), it's returned as-is.
// If the identifier is in format "username@domain", extracts the username part.
// Otherwise, it tries to look up the identifier in the mapping store with the following logic:
//   - First tries to match the identifier as the main number
//   - If no match, tries to find the identifier in any entry's sub_numbers array
//     (if a sub_number matches, returns the matrix_id of that entry)
//
// Returns empty string if the identifier cannot be resolved.
func (s *MessageService) resolveMatrixUser(identifier string) id.UserID {
	logger.Debug().Str("identifier", identifier).Msg("resolving identifier to Matrix user ID")
	identifier = strings.TrimSpace(identifier)

	// If it's already a valid Matrix user ID, return it
	if strings.HasPrefix(identifier, "@") {
		return id.UserID(identifier)
	}

	// Extract username from "username@domain" format if present
	if idx := strings.Index(identifier, "@"); idx > 0 {
		identifier = identifier[:idx]
		logger.Debug().Str("extracted_username", identifier).Msg("extracted username from user@domain format")
	}

	// Try to look up in mappings (e.g., phone number to Matrix user)
	if entry, ok := s.LookupMapping(identifier); ok == nil {
		logger.Debug().Str("original_identifier", identifier).Str("resolved_user", entry.MatrixID).Msg("identifier resolved from mapping")
		return id.UserID(entry.MatrixID)
	}

	// If not found as main number, try to find it in any sub_numbers
	s.mu.RLock()
	if subNum, err := strconv.Atoi(identifier); err == nil {
		if matrixID, ok := s.subNumberMappings[subNum]; ok {
			s.mu.RUnlock()
			logger.Debug().Str("original_identifier", identifier).Int("sub_number", subNum).Str("resolved_user", matrixID).Msg("identifier resolved from sub_number mapping")
			return id.UserID(matrixID)
		}
	}
	s.mu.RUnlock()

	// Could not resolve
	logger.Warn().Str("identifier", identifier).Msg("identifier could not be resolved to a Matrix user ID")
	return ""
}

// resolveMatrixIDToIdentifier resolves a Matrix user ID to a number
// The resolution logic:
//   - First checks if any sub_number matches the matrix_id; if found, returns the main number
//   - Then checks if the main number matches the matrix_id; if found, returns the number
//   - Returns the original Matrix ID if no mapping is found
//
// Sub_numbers are never returned directly; if a sub_number is matched, the main number is returned instead.
func (s *MessageService) resolveMatrixIDToIdentifier(matrixID string) string {
	matrixID = strings.TrimSpace(matrixID)

	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, entry := range s.mappings {
		if strings.EqualFold(entry.MatrixID, matrixID) {
			// Prefer Number as the identifier
			if entry.Number != 0 {
				logger.Debug().Str("matrix_id", matrixID).Int("number", entry.Number).Msg("resolved matrix id to number")
				return fmt.Sprintf("%d", entry.Number)
			}
		}
	}

	// No mapping found, return the original Matrix ID
	return matrixID
}

// resolveRoomIDToOtherIdentifier finds the identifier of the "other" participant in a room.
func (s *MessageService) resolveRoomIDToOtherIdentifier(ctx context.Context, roomID id.RoomID, myMatrixID string) string {
	// Use a cache key that includes both the room and the viewer to handle different perspectives
	cacheKey := fmt.Sprintf("%s|%s", string(roomID), myMatrixID)

	// Check cache first
	if cachedIdentifier := s.roomParticipantCache.Get(cacheKey); cachedIdentifier != "" {
		logger.Debug().Str("room_id", string(roomID)).Str("my_matrix_id", myMatrixID).Str("cached_identifier", cachedIdentifier).Msg("resolved other participant from cache")
		return cachedIdentifier
	}

	// Check if aliases are cached
	var aliases []string
	if cachedAliases := s.roomAliasesCache.Get(string(roomID)); cachedAliases != nil {
		aliases = cachedAliases
		logger.Debug().Str("room_id", string(roomID)).Int("alias_count", len(aliases)).Msg("fetched room aliases from cache")
	} else {
		// Fetch aliases from Matrix server
		aliases = s.matrixClient.GetRoomAliases(ctx, roomID)
		if len(aliases) > 0 {
			s.roomAliasesCache.Set(string(roomID), aliases)
		}
	}

	for _, alias := range aliases {
		logger.Debug().Str("alias", alias).Msg("processing room alias")

		norm := normalizeLocalpart(alias)
		parts := strings.SplitN(norm, "|", 2)
		if len(parts) != 2 {
			logger.Debug().Str("alias", alias).Msg("room alias does not conform to expected format after normalization")
			continue
		}
		left := strings.TrimSpace(parts[0])
		right := strings.TrimSpace(parts[1])

		me := normalizeLocalpart(myMatrixID)

		var otherLocal string
		if strings.EqualFold(left, me) {
			logger.Debug().Str("my_matrix_id", myMatrixID).Str("other_localpart", right).Msg("resolved other participant from room alias")
			otherLocal = right
		} else if strings.EqualFold(right, me) {
			logger.Debug().Str("my_matrix_id", myMatrixID).Str("other_localpart", left).Msg("resolved other participant from room alias")
			otherLocal = left
		} else {
			continue
		}

		logger.Debug().Str("other_localpart", otherLocal).Msg("returning other participant localpart as identifier")

		// Now transform the other localpart to a matrix ID, then search inside mapping: return the number
		s.mu.RLock()
		for _, entry := range s.mappings {
			normMatrixID := normalizeLocalpart(entry.MatrixID)
			if normMatrixID == otherLocal {
				s.mu.RUnlock()
				// Prefer Number as the identifier
				if entry.Number != 0 {
					identifier := fmt.Sprintf("%d", entry.Number)
					s.roomParticipantCache.Set(cacheKey, identifier)
					logger.Debug().Str("other_localpart", otherLocal).Int("number", entry.Number).Msg("resolved other participant to number from mapping and cached")
					return identifier
				}
			}
		}
		s.mu.RUnlock()

		// Cache the unresolved localpart and return it
		s.roomParticipantCache.Set(cacheKey, otherLocal)
		return otherLocal
	}

	return ""
}

func generateRoomAliasKey(actingUserID id.UserID, targetUserID id.UserID) string {
	a := normalizeLocalpart(string(actingUserID))
	b := normalizeLocalpart(string(targetUserID))

	// Ensure deterministic ordering: smaller|larger
	if a == "" && b == "" {
		return ""
	}
	if a > b {
		a, b = b, a
	}
	return fmt.Sprintf("%s|%s", a, b)
}

func (s *MessageService) ensureDirectRoom(ctx context.Context, actingUserID, targetUserID id.UserID) (id.RoomID, error) {
	key := generateRoomAliasKey(actingUserID, targetUserID)

	logger.Debug().Str("acting_user", string(actingUserID)).Str("target_user", string(targetUserID)).Msg("ensuring direct room exists")

	// Check cache first
	if cachedRoomID := s.roomAliasCache.Get(key); cachedRoomID != "" {
		logger.Debug().Str("alias", key).Str("room_id", cachedRoomID).Msg("direct room found in cache")
		return id.RoomID(cachedRoomID), nil
	}

	// Search between existing rooms
	logger.Debug().Str("key", key).Msg("Searching for direct room with alias")
	roomID := s.matrixClient.ResolveRoomAlias(ctx, key)
	if roomID != "" {
		s.roomAliasCache.Set(key, roomID)
		logger.Debug().Str("alias", key).Str("room_id", roomID).Msg("direct room already exists and cached")
		return id.RoomID(roomID), nil
	}

	// Create a new direct room with the alias
	logger.Info().Str("acting_user", string(actingUserID)).Str("target_user", string(targetUserID)).Msg("creating new direct room")
	resp, err := s.matrixClient.CreateDirectRoom(ctx, actingUserID, targetUserID, key)
	if err != nil {
		logger.Error().Str("acting_user", string(actingUserID)).Str("target_user", string(targetUserID)).Err(err).Msg("failed to create direct room")
		return "", err
	}

	// Cache the newly created room
	s.roomAliasCache.Set(key, string(resp.RoomID))

	// Ensure the target user joins the room so they can see it in their sync
	_, err = s.matrixClient.JoinRoom(ctx, targetUserID, resp.RoomID)
	if err != nil {
		logger.Error().Str("acting_user", string(actingUserID)).Str("target_user", string(targetUserID)).Str("room_id", string(resp.RoomID)).Err(err).Msg("target user failed to join room")
		return "", fmt.Errorf("join room as target user: %w", err)
	}

	return resp.RoomID, nil
}

func (s *MessageService) getMapping(key string) (mappingEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.mappings[key]
	return entry, ok
}

// LookupMapping returns the currently stored mapping for a given key (phone number or user pair).
// It searches:
//   - First by the main number
//   - Then by any sub_number in the mappings
func (s *MessageService) LookupMapping(key string) (*models.MappingResponse, error) {
	// Try to find by main number first
	if entry, ok := s.getMapping(key); ok {
		return s.buildMappingResponse(entry), nil
	}

	// Try to find by sub_number
	key = strings.TrimSpace(key)
	s.mu.RLock()
	for _, entry := range s.mappings {
		for _, subNum := range entry.SubNumbers {
			if strings.EqualFold(fmt.Sprintf("%d", subNum), key) {
				s.mu.RUnlock()
				logger.Debug().Str("key", key).Int("sub_number", subNum).Int("number", entry.Number).Msg("mapping found via sub_number")
				return s.buildMappingResponse(entry), nil
			}
		}
	}
	s.mu.RUnlock()

	return nil, ErrMappingNotFound
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
	if req.Number == 0 {
		return nil, errors.New("number is required")
	}

	matrixID := strings.TrimSpace(req.MatrixID)
	if matrixID == "" {
		return nil, errors.New("matrix_id is required")
	}

	// Determine a sensible "username" from the provided Matrix identifier.
	// For user IDs and aliases we extract the normalized localpart (without @/# and without :domain).
	// For room IDs (starting with '!') we store the RoomID and leave UserName empty.
	userName := normalizeLocalpart(matrixID)

	entry := mappingEntry{
		Number:     req.Number,
		MatrixID:   strings.TrimSpace(req.MatrixID),
		UserName:   userName,
		SubNumbers: req.SubNumbers,
		UpdatedAt:  s.now(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	numberKey := fmt.Sprintf("%d", req.Number)
	// Clean up old sub-number mappings if updating an existing entry
	if oldEntry, exists := s.mappings[numberKey]; exists {
		for _, sub := range oldEntry.SubNumbers {
			delete(s.subNumberMappings, sub)
		}
	}
	if oldEntry, exists := s.mappings[userName]; exists {
		for _, sub := range oldEntry.SubNumbers {
			delete(s.subNumberMappings, sub)
		}
	}

	entry.UpdatedAt = s.now()
	// Double map: by number and by username
	s.mappings[numberKey] = entry
	s.mappings[userName] = entry

	// Update sub-number index
	for _, sub := range entry.SubNumbers {
		s.subNumberMappings[sub] = entry.MatrixID
	}

	logger.Debug().
		Str("username", entry.UserName).
		Int("number", entry.Number).
		Interface("sub_numbers", entry.SubNumbers).
		Msg("mapping stored")
	return s.buildMappingResponse(entry), nil
}

func (s *MessageService) buildMappingResponse(entry mappingEntry) *models.MappingResponse {
	return &models.MappingResponse{
		Number:     entry.Number,
		MatrixID:   entry.MatrixID,
		SubNumbers: entry.SubNumbers,
		UpdatedAt:  entry.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func normalizeMatrixID(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// normalizeLocalpart extracts and normalizes the local part of a Matrix ID or alias.
// It strips leading prefixes (@, #), removes the domain suffix (part after ':'),
// and converts to lowercase for consistent comparison.
func normalizeLocalpart(value string) string {
	v := strings.TrimSpace(value)
	v = strings.TrimPrefix(v, "#")
	v = strings.TrimPrefix(v, "@")
	if i := strings.IndexByte(v, ':'); i != -1 {
		v = v[:i]
	}
	return strings.ToLower(strings.TrimSpace(v))
}

func isSentBy(sender, username string) bool {
	return strings.EqualFold(normalizeMatrixID(sender), normalizeMatrixID(username))
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

// ReportPushToken saves a push token to the database.
// It accepts selector, token_msgs, appid_msgs, token_calls, and appid_calls from the Acrobits client.
func (s *MessageService) ReportPushToken(ctx context.Context, req *models.PushTokenReportRequest) (*models.PushTokenReportResponse, error) {
	if req == nil {
		return nil, errors.New("request cannot be nil")
	}

	userName := strings.TrimSpace(req.UserName)
	if userName == "" {
		logger.Warn().Msg("push token report: empty username")
		return nil, errors.New("username is required")
	}

	selector := strings.TrimSpace(req.Selector)
	if selector == "" {
		logger.Warn().Msg("push token report: empty selector")
		return nil, errors.New("selector is required")
	}

	// Require password field for push token reporting
	password := strings.TrimSpace(req.Password)
	if password == "" {
		logger.Warn().Msg("push token report: empty password")
		return nil, errors.New("password is required")
	}

	if s.pushTokenDB == nil {
		logger.Warn().Msg("push token report: database not initialized")
		return nil, errors.New("push token storage not available")
	}

	// Validate extension + secret with external auth via AuthClient
	if err := s.authenticateAndPersistMappings(ctx, userName, req.Password); err != nil {
		return nil, err
	}

	// Save to database
	if err := s.pushTokenDB.SavePushToken(
		selector,
		req.TokenMsgs,
		req.AppIDMsgs,
		req.TokenCalls,
		req.AppIDCalls,
	); err != nil {
		logger.Error().Err(err).Str("selector", selector).Msg("failed to save push token")
		return nil, fmt.Errorf("failed to save push token: %w", err)
	}

	logger.Info().Str("selector", selector).Msg("push token reported and saved")

	// Register pusher with Matrix homeserver if we have a push token and proxy URL configured
	if s.proxyURL != "" && req.TokenMsgs != "" {
		// Resolve selector to Matrix user ID
		matrixUserID := s.resolveMatrixUser(userName)
		if matrixUserID == "" {
			logger.Warn().Str("selector", selector).Msg("could not resolve selector to Matrix user ID for pusher registration")
		} else {
			// Construct pusher registration request
			httpKind := "http"
			pusherReq := &models.SetPusherRequest{
				AppDisplayName:    req.AppIDMsgs, // Use app ID as display name
				AppID:             req.AppIDMsgs,
				Append:            false, // Replace existing pushers for this app_id/pushkey combination
				DeviceDisplayName: "Acrobits Softphone",
				Kind:              &httpKind,
				Lang:              "en",
				Pushkey:           req.TokenMsgs,
				Data: &models.PusherData{
					Format: "event_id_only",
					URL:    strings.TrimSuffix(s.proxyURL, "/") + "/_matrix/push/v1/notify",
				},
			}

			// Call Matrix client to register pusher
			if s.matrixClient == nil {
				logger.Warn().Str("selector", selector).Msg("Matrix client not available, skipping pusher registration")
			} else if err := s.matrixClient.SetPusher(ctx, matrixUserID, pusherReq); err != nil {
				// Log error but don't fail the request - push token was still saved
				logger.Error().
					Err(err).
					Str("selector", selector).
					Str("matrix_user_id", string(matrixUserID)).
					Str("pushkey", req.TokenMsgs).
					Str("gateway_url", pusherReq.Data.URL).
					Msg("failed to register pusher with Matrix homeserver")
			} else {
				logger.Info().
					Str("selector", selector).
					Str("matrix_user_id", string(matrixUserID)).
					Str("pushkey", req.TokenMsgs).
					Str("gateway_url", pusherReq.Data.URL).
					Msg("successfully registered pusher with Matrix homeserver")
			}
		}
	} else if s.proxyURL == "" {
		logger.Debug().Msg("PROXY_URL not configured, skipping pusher registration with Matrix")
	}

	return &models.PushTokenReportResponse{}, nil
}

// getBatchToken retrieves the stored batch token for a user
func (s *MessageService) getBatchToken(userID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.batchTokens[userID]
}

// setBatchToken stores the batch token for a user
func (s *MessageService) setBatchToken(userID string, token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batchTokens[userID] = token
}

// clearBatchToken removes the batch token for a user
func (s *MessageService) clearBatchToken(userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.batchTokens, userID)
}

// buildMediaURL converts an mxc:// URL to a public HTTP(S) URL using the homeserver URL.
// Matrix spec: https://spec.matrix.org/latest/client-server-api/#content-repository
// Example: mxc://server.com/mediaId -> https://homeserver.com/_matrix/media/v3/download/server.com/mediaId
func (s *MessageService) buildMediaURL(mxcURL string) string {
	// Parse mxc:// URL format: mxc://<server-name>/<media-id>
	if !strings.HasPrefix(mxcURL, "mxc://") {
		logger.Warn().Str("url", mxcURL).Msg("invalid mxc URL, expected mxc:// prefix")
		return mxcURL
	}

	// Remove mxc:// prefix
	pathPart := strings.TrimPrefix(mxcURL, "mxc://")

	// Use proxy URL if configured, otherwise use homeserver URL
	baseURL := s.proxyURL
	if baseURL == "" {
		logger.Warn().Msg("PROXY_URL not configured, using homeserver URL for media")
		baseURL = s.matrixClient.HomeserverURL
	}

	// Build the public download URL
	publicURL := strings.TrimSuffix(baseURL, "/") + "/_matrix/media/v3/download/" + pathPart

	return publicURL
}
