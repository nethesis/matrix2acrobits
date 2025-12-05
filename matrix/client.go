package matrix

import (
	"context"
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

	// Extract homeserver name from URL:
	// eg: https://synapse.example.com -> synapse.example.com)
	// eg: http://localhost:8008/ -> localhost
	homeserverName := strings.TrimPrefix(cfg.HomeserverURL, "https://")
	homeserverName = strings.TrimPrefix(homeserverName, "http://")
	homeserverName = strings.TrimSuffix(homeserverName, "/")
	// Remove path if present
	if idx := strings.Index(homeserverName, "/"); idx > 0 {
		homeserverName = homeserverName[:idx]
	}
	homeserverName = strings.SplitN(homeserverName, ":", 2)[0] // Remove port if present

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

// Sync performs a sync for the specified user with an optional batch token for incremental sync.
// If batchToken is empty, a full sync is performed.
func (mc *MatrixClient) Sync(ctx context.Context, userID id.UserID, batchToken string) (*mautrix.RespSync, error) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	logger.Debug().Str("user_id", string(userID)).Str("batch_token", batchToken).Msg("matrix: performing sync with token")

	mc.cli.UserID = userID

	// The SyncRequest method signature: SyncRequest(ctx, timeoutMS, since, filter, fullState, setPresence)
	// Pass batchToken as the 'since' parameter for incremental sync
	resp, err := mc.cli.SyncRequest(ctx, 30000, batchToken, "", true, "online")
	if err != nil {
		logger.Error().Str("user_id", string(userID)).Err(err).Msg("matrix: sync failed")
		return nil, err
	}

	logger.Debug().Str("user_id", string(userID)).Int("rooms", len(resp.Rooms.Join)).Str("next_batch", resp.NextBatch).Msg("matrix: sync completed")
	return resp, nil
}

// CreateDirectRoom creates a new direct message room impersonating 'userID' and inviting 'targetUserID'.
func (mc *MatrixClient) CreateDirectRoom(ctx context.Context, userID id.UserID, targetUserID id.UserID, aliasKey string) (*mautrix.RespCreateRoom, error) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	logger.Debug().Str("user_id", string(userID)).Str("target_user_id", string(targetUserID)).Str("alias_key", aliasKey).Msg("matrix: creating direct room")

	mc.cli.UserID = userID
	req := &mautrix.ReqCreateRoom{
		Invite:   []id.UserID{targetUserID},
		Preset:   "trusted_private_chat",
		IsDirect: true,
	}
	// Set RoomAliasName if aliasKey is provided, allowing rooms to be looked up by alias
	// and reused for private conversations between the same users.
	if aliasKey != "" {
		req.RoomAliasName = aliasKey
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
	req := &mautrix.ReqJoinRoom{}
	logger.Debug().Str("user_id", string(userID)).Str("room_id", string(roomID)).Msg("matrix: joining local room")
	return mc.cli.JoinRoom(ctx, string(roomID), req)
}

// ResolveRoomAlias resolves a room alias to a room ID.
func (mc *MatrixClient) ResolveRoomAlias(ctx context.Context, roomAlias string) string {
	roomAlias = strings.TrimSpace(roomAlias)
	if roomAlias == "" {
		logger.Debug().Msg("matrix: empty room alias")
		return ""
	}
	if !strings.HasPrefix(roomAlias, "#") {
		roomAlias = "#" + roomAlias + ":" + mc.homeserverName
	}
	// This action does not require impersonation, so no lock is needed.
	resp, err := mc.cli.ResolveAlias(ctx, id.RoomAlias(roomAlias))
	if err != nil {
		logger.Debug().Str("room_alias", roomAlias).Err(err).Msg("matrix: failed to resolve room alias")
		return ""
	}
	return string(resp.RoomID)
}

func (mc *MatrixClient) GetRoomAliases(ctx context.Context, roomID id.RoomID) []string {
	// This action does not require impersonation, so no lock is needed.
	logger.Debug().Str("room_id", roomID.String()).Msg("matrix: fetching room aliases")
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
	logger.Debug().Str("room_id", roomID.String()).Int("alias_count", len(aliases)).Msg("matrix: fetched room aliases")
	return aliases
}

func (mc *MatrixClient) ListJoinedRooms(ctx context.Context, userID id.UserID) ([]id.RoomID, error) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.cli.UserID = userID
	resp, err := mc.cli.JoinedRooms(ctx)
	if err != nil {
		logger.Debug().Str("user_id", string(userID)).Err(err).Msg("matrix: failed to list joined rooms")
		return nil, err
	}
	logger.Debug().Str("user_id", string(userID)).Int("joined_room_count", len(resp.JoinedRooms)).Msg("matrix: fetched joined rooms")
	return resp.JoinedRooms, nil
}
