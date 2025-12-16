package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
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
	authClient     AuthClient
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

// validateAndPersistMappings validates credentials with the external auth service
// and persists all returned mappings to the local store.
// Returns ErrAuthentication if validation fails.
func (s *MessageService) validateAndPersistMappings(ctx context.Context, username, password string) error {
	mappings, ok, err := s.authClient.Validate(ctx, username, strings.TrimSpace(password), s.homeserverHost)
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

	senderStr := strings.TrimSpace(req.From)
	if senderStr == "" {
		logger.Warn().Msg("send message: empty sender")
		return nil, ErrInvalidSender
	}

	// If sender is already a Matrix ID, skip external auth
	if !strings.HasPrefix(senderStr, "@") {
		// Not a Matrix ID - check if we have a mapping for it
		resolvedMatrix := s.resolveMatrixUser(senderStr)
		if resolvedMatrix == "" {
			// No mapping exists - try external auth if password is provided
			if strings.TrimSpace(req.Password) == "" {
				logger.Warn().Str("sender", senderStr).Msg("sender not resolvable and no password provided")
				return nil, ErrAuthentication
			}
			if err := s.validateAndPersistMappings(context.Background(), senderStr, req.Password); err != nil {
				return nil, err
			}
		} else {
			logger.Debug().Str("sender", senderStr).Str("resolved_matrix_id", string(resolvedMatrix)).Msg("sender resolved from existing mapping, skipping external auth")
		}
	} else {
		logger.Debug().Str("sender", senderStr).Msg("sender is already a Matrix ID, skipping external auth")
	}

	// Resolve sender to Matrix ID using mappings
	senderMatrix := s.resolveMatrixUser(req.From)
	if senderMatrix == "" {
		logger.Warn().Str("from", req.From).Msg("resolved to empty Matrix user ID")
		return nil, ErrAuthentication
	}

	recipientStr := strings.TrimSpace(req.To)
	if recipientStr == "" {
		logger.Warn().Msg("send message: empty recipient")
		return nil, ErrInvalidRecipient
	}

	// Check if recipient is a room ID first
	var roomID id.RoomID
	var recipientMatrix id.UserID
	if strings.HasPrefix(recipientStr, "!") {
		// It's already a room ID, use it directly
		roomID = id.RoomID(recipientStr)
		logger.Debug().Str("recipient", string(roomID)).Msg("recipient is a room ID, using directly")
	} else {
		// Try to resolve as Matrix user ID or mapping
		recipientMatrix = s.resolveMatrixUser(recipientStr)
		if recipientMatrix == "" {
			logger.Warn().Str("recipient", recipientStr).Msg("recipient is not a valid Matrix user ID or room ID")
			return nil, ErrInvalidRecipient
		}

		logger.Debug().Str("sender", string(senderMatrix)).Str("recipient", string(recipientMatrix)).Msg("resolved sender and recipient to Matrix user IDs")

		// For 1-to-1 messaging, ensure a direct room exists between sender and recipient
		var err error
		roomID, err = s.ensureDirectRoom(ctx, senderMatrix, recipientMatrix)
		if err != nil {
			logger.Error().Str("sender", string(senderMatrix)).Str("recipient", string(recipientMatrix)).Err(err).Msg("failed to ensure direct room")
			return nil, err
		}
	}

	if recipientMatrix != "" {
		logger.Debug().Str("sender", string(senderMatrix)).Str("recipient", string(recipientMatrix)).Str("room_id", string(roomID)).Msg("sending message to direct room")
	} else {
		logger.Debug().Str("sender", string(senderMatrix)).Str("room_id", string(roomID)).Msg("sending message to room")
	}
	// Ensure the sender is a member of the room (in case join failed during room creation)
	_, err := s.matrixClient.JoinRoom(ctx, senderMatrix, roomID)
	if err != nil {
		logger.Error().Str("sender", string(senderMatrix)).Str("room_id", string(roomID)).Err(err).Msg("failed to join room")
		return nil, fmt.Errorf("send message: %w", err)
	}

	var content *event.MessageEventContent

	// Check if this is a file transfer message
	if models.IsFileTransferContentType(req.ContentType) {
		logger.Debug().Str("content_type", req.ContentType).Msg("processing file transfer message")

		// Parse the file transfer JSON from the body
		ftMsg, err := models.ParseFileTransferMessage(req.Body)
		if err != nil {
			logger.Warn().Err(err).Str("body", req.Body).Msg("failed to parse file transfer message")
			return nil, fmt.Errorf("invalid file transfer message: %w", err)
		}

		// Check if attachments are empty - if so, treat as text message
		if len(ftMsg.Attachments) == 0 {
			logger.Debug().Msg("file transfer message has empty attachments, sending as text message")
			content = &event.MessageEventContent{
				MsgType: event.MsgText,
				Body:    ftMsg.Body,
			}
		} else {
			// Download attachments from Acrobits and upload to Matrix content repository
			msgType, rawContent, err := models.FileTransferToMatrixEventContent(ftMsg)
			if err != nil {
				logger.Warn().Err(err).Msg("failed to convert file transfer to Matrix format")
				return nil, fmt.Errorf("failed to convert file transfer: %w", err)
			}

			// Download and upload the main attachment
			acrobitsURL := ""
			if url, ok := rawContent["url"].(string); ok {
				acrobitsURL = url
			}

			mimetype := ""
			if info, ok := rawContent["info"].(map[string]interface{}); ok {
				if mt, ok := info["mimetype"].(string); ok {
					mimetype = mt
				}
			}

			matrixURL := ""
			uploadSuccess := true
			fileSize := 0

			if acrobitsURL != "" {
				logger.Debug().Str("content_url", acrobitsURL).Msg("downloading attachment from Acrobits")
				fileData, err := s.downloadFile(ctx, acrobitsURL)
				if err != nil {
					logger.Warn().Err(err).Str("content_url", acrobitsURL).Msg("failed to download attachment, falling back to text message")
					// Fallback: send as text message only
					content = &event.MessageEventContent{
						MsgType: event.MsgText,
						Body:    ftMsg.Body,
					}
					uploadSuccess = false
				} else {
					fileSize = len(fileData)

					// Detect content type from file data to ensure correct display
					detectedMime := http.DetectContentType(fileData)
					// Strip parameters (e.g. "; charset=utf-8")
					if idx := strings.Index(detectedMime, ";"); idx != -1 {
						detectedMime = detectedMime[:idx]
					}

					// Use detected mime if it's an image or if original is missing/generic
					if strings.HasPrefix(detectedMime, "image/") || mimetype == "" || mimetype == "application/octet-stream" {
						mimetype = detectedMime

						// Update msgType based on new mimetype
						if models.IsImageContentType(mimetype) {
							msgType = "m.image"
						} else if models.IsVideoContentType(mimetype) {
							msgType = "m.video"
						} else if models.IsAudioContentType(mimetype) {
							msgType = "m.audio"
						}
					}

					// Upload to Matrix content repository
					logger.Debug().Str("content_url", acrobitsURL).Int("size", fileSize).Str("mimetype", mimetype).Msg("uploading attachment to Matrix content repository")
					uploadedURL, err := s.matrixClient.UploadMedia(ctx, senderMatrix, mimetype, fileData)
					if err != nil {
						logger.Warn().Err(err).Str("content_url", acrobitsURL).Msg("failed to upload attachment to Matrix, falling back to text message")
						// Fallback: send as text message only
						content = &event.MessageEventContent{
							MsgType: event.MsgText,
							Body:    ftMsg.Body,
						}
						uploadSuccess = false
					} else {
						matrixURL = string(uploadedURL)
						logger.Debug().Str("matrix_url", matrixURL).Str("content_url", acrobitsURL).Msg("attachment uploaded to Matrix content repository")
					}
				}
			}

			if uploadSuccess {
				// Build the Matrix event content
				content = &event.MessageEventContent{
					MsgType: event.MessageType(msgType),
					Body:    rawContent["body"].(string),
				}

				// Set the URL for media messages (use uploaded Matrix URL)
				if matrixURL != "" {
					content.URL = id.ContentURIString(matrixURL)
				}

				// Set the filename if present
				if filename, ok := rawContent["filename"].(string); ok {
					content.FileName = filename
				}

				// Set info block if present
				if info, ok := rawContent["info"].(map[string]interface{}); ok {
					content.Info = &event.FileInfo{}

					// Use the resolved mimetype
					content.Info.MimeType = mimetype

					// Use actual file size if available, otherwise fallback to info
					if fileSize > 0 {
						content.Info.Size = fileSize
					} else if size, ok := info["size"].(int64); ok {
						content.Info.Size = int(size)
					}

					// Handle thumbnail info
					if thumbnailURL, ok := info["thumbnail_url"].(string); ok {
						content.Info.ThumbnailURL = id.ContentURIString(thumbnailURL)
					}
					if thumbnailInfo, ok := info["thumbnail_info"].(map[string]interface{}); ok {
						content.Info.ThumbnailInfo = &event.FileInfo{}
						if tm, ok := thumbnailInfo["mimetype"].(string); ok {
							content.Info.ThumbnailInfo.MimeType = tm
						}
					}
				}

				logger.Debug().Str("msg_type", msgType).Str("matrix_url", matrixURL).Msg("converted file transfer to Matrix media message")
			}
		}
	} else {
		// Regular text message
		content = &event.MessageEventContent{
			MsgType: event.MsgText,
			Body:    req.Body,
		}
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
			if err := s.validateAndPersistMappings(ctx, userName, req.Password); err != nil {
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

			// Extract content fields
			var msgType string
			var body string
			var url string
			var info map[string]interface{}
			var filename string

			if t, ok := evt.Content.Raw["msgtype"].(string); ok {
				msgType = t
			}
			if b, ok := evt.Content.Raw["body"].(string); ok {
				body = b
			}
			if fn, ok := evt.Content.Raw["filename"].(string); ok {
				filename = fn
			}
			if u, ok := evt.Content.Raw["url"].(string); ok {
				url = u
			}
			if i, ok := evt.Content.Raw["info"].(map[string]interface{}); ok {
				info = i
			}

			sms := models.SMS{
				SMSID:       string(evt.ID),
				SendingDate: time.UnixMilli(evt.Timestamp).UTC().Format(time.RFC3339),
				StreamID:    string(roomID),
			}

			// Check if it's a media message
			if msgType == "m.image" || msgType == "m.video" || msgType == "m.audio" || msgType == "m.file" {
				// Convert to Acrobits file transfer format
				// Resolve MXC URI to HTTP URL
				httpURL := s.matrixClient.ResolveMXC(url)
				if httpURL == "" {
					logger.Warn().Str("event_id", string(evt.ID)).Msg("media event missing URL, falling back to text")
					sms.SMSText = body
					sms.ContentType = "text/plain"
				} else {
					// Extract metadata
					mimetype := ""
					var size int64
					thumbnailURL := ""
					thumbnailMime := ""

					if info != nil {
						if m, ok := info["mimetype"].(string); ok {
							mimetype = m
						}
						if sVal, ok := info["size"].(float64); ok { // JSON numbers are float64
							size = int64(sVal)
						} else if sVal, ok := info["size"].(int64); ok {
							size = sVal
						} else if sVal, ok := info["size"].(int); ok {
							size = int64(sVal)
						}
						if tURL, ok := info["thumbnail_url"].(string); ok {
							thumbnailURL = s.matrixClient.ResolveMXC(tURL)
						}
						if tInfo, ok := info["thumbnail_info"].(map[string]interface{}); ok {
							if tm, ok := tInfo["mimetype"].(string); ok {
								thumbnailMime = tm
							}
						}
					}

					// Convert to JSON
					// Prefer explicit filename if present; fall back to body
					effectiveFilename := filename
					if effectiveFilename == "" {
						effectiveFilename = body
					}

					ftJSON, err := models.MatrixMediaToFileTransfer(msgType, body, httpURL, mimetype, effectiveFilename, size, thumbnailURL, thumbnailMime)
					if err != nil {
						logger.Warn().Err(err).Msg("failed to convert matrix media to file transfer")
						// Fallback to text
						sms.SMSText = body
						sms.ContentType = "text/plain"
					} else {
						sms.SMSText = ftJSON
						sms.ContentType = models.FileTransferContentType
					}
				}
			} else {
				// Regular text message
				sms.SMSText = body
				sms.ContentType = "text/plain"
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
// Otherwise, it tries to look up the identifier in the mapping store with the following logic:
//   - First tries to match the identifier as the main number
//   - If no match, tries to find the identifier in any entry's sub_numbers array
//     (if a sub_number matches, returns the matrix_id of that entry)
//
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

func (s *MessageService) setMapping(entry mappingEntry) mappingEntry {
	if entry.Number == 0 {
		logger.Warn().Msg("attempted to set mapping with empty number")
		return entry
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Clean up old sub-number mappings if updating an existing entry
	if oldEntry, exists := s.mappings[fmt.Sprintf("%d", entry.Number)]; exists {
		for _, sub := range oldEntry.SubNumbers {
			delete(s.subNumberMappings, sub)
		}
	}

	entry.UpdatedAt = s.now()
	s.mappings[fmt.Sprintf("%d", entry.Number)] = entry

	// Update sub-number index
	for _, sub := range entry.SubNumbers {
		s.subNumberMappings[sub] = entry.MatrixID
	}

	logger.Debug().Int("number", entry.Number).Str("room_id", string(entry.RoomID)).Msg("mapping stored")
	return entry
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
} // ListMappings returns all stored mappings.
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

	entry := mappingEntry{
		Number:     req.Number,
		MatrixID:   strings.TrimSpace(req.MatrixID),
		SubNumbers: req.SubNumbers,
		UpdatedAt:  s.now(),
	}
	entry = s.setMapping(entry)
	return s.buildMappingResponse(entry), nil
}

// LoadMappingsFromFile loads mappings from a JSON file.
// See docs/example-mapping.json for the expected format.
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
		if req.Number == 0 {
			logger.Warn().Msg("skipping mapping with empty number")
			continue
		}
		entry := mappingEntry{
			Number:     req.Number,
			MatrixID:   req.MatrixID,
			SubNumbers: req.SubNumbers,
			UpdatedAt:  s.now(),
		}
		s.setMapping(entry)
	}

	logger.Info().Int("count", len(mappingArray)).Str("file", filePath).Msg("mappings loaded from file")
	return nil
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
	if err := s.validateAndPersistMappings(ctx, userName, req.Password); err != nil {
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

// downloadFile downloads a file from the given URL with a context timeout.
// Returns the file contents as bytes, or an error if the download fails.
func (s *MessageService) downloadFile(ctx context.Context, url string) ([]byte, error) {
	// Create a new request with context
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create download request: %w", err)
	}

	// Execute the request
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download file: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	// Read the response body with a reasonable size limit (100MB)
	const maxSize = 100 * 1024 * 1024 // 100MB
	limitedReader := io.LimitReader(resp.Body, maxSize+1)
	data, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	if len(data) > maxSize {
		return nil, fmt.Errorf("file too large: %d bytes exceeds limit of %d bytes", len(data), maxSize)
	}

	logger.Debug().Str("url", url).Int("size", len(data)).Int("status", resp.StatusCode).Msg("file downloaded successfully")
	return data, nil
}
