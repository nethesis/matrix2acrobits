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
	"github.com/nethesis/matrix2acrobits/models"
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

// UploadMedia uploads media to the Matrix content repository and returns the MXC URI.
// This follows the Matrix spec: https://spec.matrix.org/v1.2/client-server-api/#content-repository
// The returned URI can be used in message events via the `url` field.
func (mc *MatrixClient) UploadMedia(ctx context.Context, userID id.UserID, contentType string, data []byte) (id.ContentURIString, error) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	logger.Debug().
		Str("user_id", string(userID)).
		Str("content_type", contentType).
		Int("size", len(data)).
		Msg("matrix: uploading media to content repository")

	mc.cli.UserID = userID

	resp, err := mc.cli.UploadBytes(ctx, data, contentType)
	if err != nil {
		logger.Error().
			Str("user_id", string(userID)).
			Str("content_type", contentType).
			Err(err).
			Msg("matrix: failed to upload media")
		return "", fmt.Errorf("upload media: %w", err)
	}

	contentURI := resp.ContentURI.CUString()
	logger.Debug().
		Str("user_id", string(userID)).
		Str("content_uri", string(contentURI)).
		Msg("matrix: media uploaded successfully")

	return contentURI, nil
}

// SetPusher registers or updates a push gateway for the specified user.
// This is used to configure Matrix to send push notifications to the proxy's /_matrix/push/v1/notify endpoint.
func (mc *MatrixClient) SetPusher(ctx context.Context, userID id.UserID, req *models.SetPusherRequest) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	logger.Debug().
		Str("user_id", string(userID)).
		Str("pushkey", req.Pushkey).
		Str("app_id", req.AppID).
		Interface("kind", req.Kind).
		Msg("matrix: setting pusher")

	mc.cli.UserID = userID

	// Construct the URL path for the pusher endpoint
	urlPath := mc.cli.BuildClientURL("v3", "pushers", "set")

	// Make the POST request
	_, err := mc.cli.MakeRequest(ctx, http.MethodPost, urlPath, req, nil)
	if err != nil {
		logger.Error().
			Str("user_id", string(userID)).
			Str("pushkey", req.Pushkey).
			Str("app_id", req.AppID).
			Err(err).
			Msg("matrix: failed to set pusher")
		return fmt.Errorf("set pusher: %w", err)
	}

	logger.Info().
		Str("user_id", string(userID)).
		Str("pushkey", req.Pushkey).
		Str("app_id", req.AppID).
		Msg("matrix: pusher set successfully")
	return nil
}

// ResolveMXC converts an MXC URI (mxc://server/mediaId) to a downloadable HTTP URL.
// It uses the configured homeserver URL to construct the download link.
func (mc *MatrixClient) ResolveMXC(mxcURI string) string {
	if !strings.HasPrefix(mxcURI, "mxc://") {
		return mxcURI
	}

	// Parse the MXC URI
	// Format: mxc://<server-name>/<media-id>
	trimmed := strings.TrimPrefix(mxcURI, "mxc://")
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) != 2 {
		return mxcURI
	}
	serverName := parts[0]
	mediaID := parts[1]

	// Construct the download URL
	// Format: /_matrix/media/v3/download/<server-name>/<media-id>
	baseURL := strings.TrimSuffix(mc.homeserverURL, "/")
	return fmt.Sprintf("%s/_matrix/media/v3/download/%s/%s", baseURL, serverName, mediaID)
}
