package matrix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nethesis/matrix2acrobits/logger"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// Config configures the Matrix client wrapper.
type Config struct {
	HomeserverURL string
	AsUserID      id.UserID
	AsToken       string
	HTTPClient    *http.Client
}

// MatrixClient is a client wrapper for performing Application Service actions.
// Note: The underlying mautrix client is stateful for impersonation in this version.
// A mutex is used to make operations thread-safe.
type MatrixClient struct {
	cli            *mautrix.Client
	homeserverURL  string
	homeserverName string
	mu             sync.Mutex
}

// NewClient creates a MatrixClient authenticated as an Application Service.
func NewClient(cfg Config) (*MatrixClient, error) {
	if cfg.HomeserverURL == "" {
		return nil, errors.New("homeserver url is required")
	}
	if cfg.AsToken == "" {
		return nil, errors.New("application service token (as_token) is required")
	}
	if cfg.AsUserID == "" {
		return nil, errors.New("application service user ID (as_user_id) is required")
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	// For v0.26.0, the AS token and user ID are passed to NewClient.
	client, err := mautrix.NewClient(cfg.HomeserverURL, cfg.AsUserID, cfg.AsToken)
	if err != nil {
		return nil, fmt.Errorf("create mautrix client: %w", err)
	}
	client.Client = httpClient
	// This flag enables the `user_id` query parameter for impersonation.
	client.SetAppServiceUserID = true

	// Extract homeserver name from URL (e.g., https://synapse.example.com -> synapse.example.com)
	homeserverName := strings.TrimPrefix(cfg.HomeserverURL, "https://")
	homeserverName = strings.TrimPrefix(homeserverName, "http://")
	homeserverName = strings.TrimSuffix(homeserverName, "/")
	// Remove path if present
	if idx := strings.Index(homeserverName, "/"); idx > 0 {
		homeserverName = homeserverName[:idx]
	}

	return &MatrixClient{
		cli:            client,
		homeserverURL:  cfg.HomeserverURL,
		homeserverName: homeserverName,
	}, nil
}

// SendMessage sends a message to a room, impersonating the specified userID.
func (mc *MatrixClient) SendMessage(ctx context.Context, userID id.UserID, roomID id.RoomID, content *event.MessageEventContent) (*mautrix.RespSendEvent, error) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	logger.Debug().Str("user_id", string(userID)).Str("room_id", string(roomID)).Msg("matrix: sending message event")

	mc.cli.UserID = userID
	resp, err := mc.cli.SendMessageEvent(ctx, roomID, event.EventMessage, content)
	if err != nil {
		logger.Error().Str("user_id", string(userID)).Str("room_id", string(roomID)).Err(err).Msg("matrix: failed to send message event")
		return nil, err
	}

	logger.Debug().Str("user_id", string(userID)).Str("room_id", string(roomID)).Str("event_id", string(resp.EventID)).Msg("matrix: message event sent")
	return resp, nil
}

// Sync performs a sync for the specified user to fetch messages.
// If filterAfterEventID is provided (non-empty), uses post-processing to filter events.
func (mc *MatrixClient) Sync(ctx context.Context, userID id.UserID, since, filterAfterEventID string) (*mautrix.RespSync, error) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	logger.Debug().Str("user_id", string(userID)).Str("since", since).Str("filter_after_event_id", filterAfterEventID).Msg("matrix: performing sync")

	mc.cli.UserID = userID

	// Create a filter that only returns message events to optimize bandwidth
	filter := &mautrix.Filter{
		Room: &mautrix.RoomFilter{
			Timeline: &mautrix.FilterPart{
				Types: []event.Type{event.EventMessage},
				Limit: 100, // Reasonable limit to get enough context
			},
		},
	}

	// Marshal the filter to JSON string for the SyncRequest
	filterBytes, err := json.Marshal(filter)
	if err != nil {
		logger.Error().Str("user_id", string(userID)).Err(err).Msg("matrix: failed to marshal filter")
		return nil, fmt.Errorf("marshal filter: %w", err)
	}
	filterJSON := string(filterBytes)
	logger.Debug().Str("user_id", string(userID)).Str("filter", filterJSON).Msg("matrix: using message filter for sync")

	// The SyncRequest method signature: SyncRequest(ctx, timeoutMS, since, filter, fullState, setPresence)
	resp, err := mc.cli.SyncRequest(ctx, 30000, since, filterJSON, false, event.PresenceOnline)
	if err != nil {
		logger.Error().Str("user_id", string(userID)).Err(err).Msg("matrix: sync failed")
		return nil, err
	}

	// Post-process: If filterAfterEventID is provided, filter room timelines to only include events after it
	// This is necessary because the Matrix spec doesn't support server-side filtering by event ID
	if filterAfterEventID != "" {
		logger.Debug().Str("user_id", string(userID)).Str("filter_after_event_id", filterAfterEventID).Msg("matrix: filtering sync results by event ID")
		for roomID, room := range resp.Rooms.Join {
			foundEvent := false
			// Find the index of the target event
			for j, evt := range room.Timeline.Events {
				if string(evt.ID) == filterAfterEventID {
					// Keep only events after this one
					room.Timeline.Events = room.Timeline.Events[j+1:]
					resp.Rooms.Join[roomID] = room
					logger.Debug().Str("user_id", string(userID)).Str("room_id", roomID.String()).Int("events_removed", j+1).Msg("matrix: filtered room timeline")
					foundEvent = true
					break
				}
			}
			if !foundEvent && len(room.Timeline.Events) > 0 {
				// Event not found in this timeline window - the requested event is older than what we got
				// Keep all events since they're all newer than the requested event
				logger.Debug().Str("user_id", string(userID)).Str("room_id", roomID.String()).Msg("matrix: filter event not found in timeline, keeping all events")
			}
		}
	}

	logger.Debug().Str("user_id", string(userID)).Int("rooms", len(resp.Rooms.Join)).Msg("matrix: sync completed")
	return resp, nil
}

// CreateDirectRoom creates a new direct message room impersonating 'userID' and inviting 'targetUserID'.
func (mc *MatrixClient) CreateDirectRoom(ctx context.Context, userID id.UserID, targetUserID id.UserID, aliasKey string) (*mautrix.RespCreateRoom, error) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	logger.Debug().Str("user_id", string(userID)).Str("target_user_id", string(targetUserID)).Str("alias_key", aliasKey).Msg("matrix: creating direct room")

	mc.cli.UserID = userID
	req := &mautrix.ReqCreateRoom{
		Invite:        []id.UserID{targetUserID},
		Preset:        "trusted_private_chat",
		IsDirect:      true,
		RoomAliasName: aliasKey,
	}
	resp, err := mc.cli.CreateRoom(ctx, req)
	if err != nil {
		logger.Error().Str("user_id", string(userID)).Str("target_user_id", string(targetUserID)).Str("alias_key", aliasKey).Err(err).Msg("matrix: failed to create direct room")
		return nil, err
	}

	logger.Info().Str("user_id", string(userID)).Str("target_user_id", string(targetUserID)).Str("alias_key", aliasKey).Str("room_id", string(resp.RoomID)).Msg("matrix: direct room created")
	return resp, nil
}

// JoinRoom joins a room, impersonating the specified userID.
func (mc *MatrixClient) JoinRoom(ctx context.Context, userID id.UserID, roomID id.RoomID) (*mautrix.RespJoinRoom, error) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.cli.UserID = userID

	// Extract homeserver from room ID for federated joins
	// Room ID format: !opaque:homeserver.name
	roomIDStr := string(roomID)
	req := &mautrix.ReqJoinRoom{}

	if idx := strings.Index(roomIDStr, ":"); idx > 0 && idx < len(roomIDStr)-1 {
		roomServerName := roomIDStr[idx+1:]

		// Only add Via if the room is on a different homeserver (federated)
		if roomServerName != "" && roomServerName != mc.homeserverName {
			req.Via = []string{roomServerName}
			logger.Debug().Str("user_id", string(userID)).Str("room_id", roomIDStr).Str("room_server", roomServerName).Str("local_server", mc.homeserverName).Msg("matrix: joining federated room with homeserver hint")
		} else {
			logger.Debug().Str("user_id", string(userID)).Str("room_id", roomIDStr).Msg("matrix: joining local room without via")
		}
	}

	logger.Debug().Str("user_id", string(userID)).Str("room_id", roomIDStr).Bool("has_via", len(req.Via) > 0).Msg("matrix: calling JoinRoom")
	return mc.cli.JoinRoom(ctx, roomIDStr, req)
}

// ResolveRoomAlias resolves a room alias to a room ID.
func (mc *MatrixClient) ResolveRoomAlias(ctx context.Context, roomAlias string) (*mautrix.RespAliasResolve, error) {
	// This action does not require impersonation, so no lock is needed.
	return mc.cli.ResolveAlias(ctx, id.RoomAlias(roomAlias))
}

func (mc *MatrixClient) GetRoomAliases(ctx context.Context, roomID id.RoomID) []string {
	// This action does not require impersonation, so no lock is needed.
	resp, err := mc.cli.GetAliases(ctx, roomID)
	if err != nil {
		logger.Error().Str("room_id", roomID.String()).Err(err).Msg("matrix: failed to get room aliases")
		return []string{}
	}
	if resp == nil || len(resp.Aliases) == 0 {
		return []string{}
	}

	aliases := make([]string, 0, len(resp.Aliases))
	for _, a := range resp.Aliases {
		aliases = append(aliases, string(a))
	}
	return aliases
}
