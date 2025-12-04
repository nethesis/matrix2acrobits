package matrix

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	cli *mautrix.Client
	mu  sync.Mutex
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

	return &MatrixClient{
		cli: client,
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
func (mc *MatrixClient) Sync(ctx context.Context, userID id.UserID, since string) (*mautrix.RespSync, error) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	logger.Debug().Str("user_id", string(userID)).Str("since", since).Msg("matrix: performing sync")

	mc.cli.UserID = userID
	// The SyncRequest method takes filter parameters directly in this version.
	// Using empty filter to ensure we get all rooms and messages
	resp, err := mc.cli.SyncRequest(ctx, 30000, since, "", false, event.PresenceOnline)
	if err != nil {
		logger.Error().Str("user_id", string(userID)).Err(err).Msg("matrix: sync failed")
		return nil, err
	}

	logger.Debug().Str("user_id", string(userID)).Int("rooms", len(resp.Rooms.Join)).Msg("matrix: sync completed")
	return resp, nil
}

// CreateDirectRoom creates a new direct message room impersonating 'userID' and inviting 'targetUserID'.
func (mc *MatrixClient) CreateDirectRoom(ctx context.Context, userID id.UserID, targetUserID id.UserID) (*mautrix.RespCreateRoom, error) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	logger.Debug().Str("user_id", string(userID)).Str("target_user_id", string(targetUserID)).Msg("matrix: creating direct room")

	mc.cli.UserID = userID
	req := &mautrix.ReqCreateRoom{
		Invite:   []id.UserID{targetUserID},
		Preset:   "trusted_private_chat",
		IsDirect: true,
	}
	resp, err := mc.cli.CreateRoom(ctx, req)
	if err != nil {
		logger.Error().Str("user_id", string(userID)).Str("target_user_id", string(targetUserID)).Err(err).Msg("matrix: failed to create direct room")
		return nil, err
	}

	logger.Info().Str("user_id", string(userID)).Str("target_user_id", string(targetUserID)).Str("room_id", string(resp.RoomID)).Msg("matrix: direct room created")
	return resp, nil
}

// JoinRoom joins a room, impersonating the specified userID.
func (mc *MatrixClient) JoinRoom(ctx context.Context, userID id.UserID, roomID id.RoomID) (*mautrix.RespJoinRoom, error) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.cli.UserID = userID
	return mc.cli.JoinRoom(ctx, string(roomID), nil)
}

// CreateRoom creates a new room impersonating the specified userID.
func (mc *MatrixClient) CreateRoom(ctx context.Context, userID id.UserID, name string, invitees []id.UserID) (*mautrix.RespCreateRoom, error) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.cli.UserID = userID
	req := &mautrix.ReqCreateRoom{
		Name:   name,
		Invite: invitees,
	}
	return mc.cli.CreateRoom(ctx, req)
}

// ResolveRoomAlias resolves a room alias to a room ID.
func (mc *MatrixClient) ResolveRoomAlias(ctx context.Context, roomAlias string) (*mautrix.RespAliasResolve, error) {
	// This action does not require impersonation, so no lock is needed.
	return mc.cli.ResolveAlias(ctx, id.RoomAlias(roomAlias))
}
