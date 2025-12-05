package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"maunium.net/go/mautrix/id"
)

// TestMessageServiceCacheInitialization tests that MessageService initializes with caches.
func TestMessageServiceCacheInitialization(t *testing.T) {
	svc := NewMessageService(nil, nil)

	assert.NotNil(t, svc.roomAliasCache, "roomAliasCache should be initialized")
	assert.NotNil(t, svc.roomAliasesCache, "roomAliasesCache should be initialized")
	assert.NotNil(t, svc.roomParticipantCache, "roomParticipantCache should be initialized")
}

// TestRoomAliasCacheSetGet tests basic cache set/get operations within service context.
func TestRoomAliasCacheSetGet(t *testing.T) {
	svc := NewMessageService(nil, nil)

	alias := "user1|user2"
	roomID := "!room123:server"

	// Cache should be empty initially
	assert.Equal(t, "", svc.roomAliasCache.Get(alias))

	// Set a value
	svc.roomAliasCache.Set(alias, roomID)

	// Should be retrievable
	assert.Equal(t, roomID, svc.roomAliasCache.Get(alias))
}

// TestRoomAliasesCacheSetGet tests basic cache set/get operations for room aliases.
func TestRoomAliasesCacheSetGet(t *testing.T) {
	svc := NewMessageService(nil, nil)

	roomID := "!room123:server"
	aliases := []string{"alias1", "alias2"}

	// Cache should be empty initially
	assert.Nil(t, svc.roomAliasesCache.Get(roomID))

	// Set a value
	svc.roomAliasesCache.Set(roomID, aliases)

	// Should be retrievable
	retrieved := svc.roomAliasesCache.Get(roomID)
	assert.Equal(t, aliases, retrieved)
}

// TestRoomParticipantCacheSetGet tests basic cache set/get for participant identifiers.
func TestRoomParticipantCacheSetGet(t *testing.T) {
	svc := NewMessageService(nil, nil)

	key := "!room123:server|@user1:server"
	identifier := "201"

	// Cache should be empty initially
	assert.Equal(t, "", svc.roomParticipantCache.Get(key))

	// Set a value
	svc.roomParticipantCache.Set(key, identifier)

	// Should be retrievable
	assert.Equal(t, identifier, svc.roomParticipantCache.Get(key))
}

// TestCacheTTLExpiration tests that cached values expire after TTL.
func TestCacheTTLExpiration(t *testing.T) {
	svc := &MessageService{
		matrixClient:         nil,
		pushTokenDB:          nil,
		now:                  time.Now,
		mappings:             make(map[string]mappingEntry),
		batchTokens:          make(map[string]string),
		roomAliasCache:       NewRoomAliasCache(50 * time.Millisecond),
		roomAliasesCache:     NewRoomAliasesCache(50 * time.Millisecond),
		roomParticipantCache: NewRoomParticipantCache(50 * time.Millisecond),
	}

	alias := "user1|user2"
	roomID := "!room123:server"

	// Set value
	svc.roomAliasCache.Set(alias, roomID)
	assert.Equal(t, roomID, svc.roomAliasCache.Get(alias))

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	// Should be expired
	assert.Equal(t, "", svc.roomAliasCache.Get(alias))
}

// TestResolveRoomIDToOtherIdentifierCacheBehavior tests cache interactions in resolveRoomIDToOtherIdentifier.
func TestResolveRoomIDToOtherIdentifierCacheBehavior(t *testing.T) {
	svc := NewMessageService(nil, nil)

	// Add a mapping so the resolution can complete
	svc.setMapping(mappingEntry{
		Number:   201,
		MatrixID: "@user2:server",
	})

	roomID := id.RoomID("!room123:server")
	myMatrixID := "@user1:server"

	// Cache the aliases directly
	svc.roomAliasesCache.Set(string(roomID), []string{"user1|user2"})

	// Call resolveRoomIDToOtherIdentifier
	result := svc.resolveRoomIDToOtherIdentifier(nil, roomID, myMatrixID)

	// Should resolve to 201 (the mapped number for @user2:server)
	assert.Equal(t, "201", result)

	// Check that participant cache was populated
	cacheKey := string(roomID) + "|" + myMatrixID
	cached := svc.roomParticipantCache.Get(cacheKey)
	assert.Equal(t, "201", cached)

	// Call again - should use participant cache
	result2 := svc.resolveRoomIDToOtherIdentifier(nil, roomID, myMatrixID)
	assert.Equal(t, "201", result2)
}

// TestParticipantCacheKeyUniquenessForDifferentViewers tests separate cache entries for different viewers.
func TestParticipantCacheKeyUniquenessForDifferentViewers(t *testing.T) {
	svc := NewMessageService(nil, nil)

	// Add mappings for both users
	svc.setMapping(mappingEntry{
		Number:   201,
		MatrixID: "@user2:server",
	})
	svc.setMapping(mappingEntry{
		Number:   102,
		MatrixID: "@user1:server",
	})

	roomID := id.RoomID("!room123:server")
	user1 := "@user1:server"
	user2 := "@user2:server"

	// Cache the aliases
	svc.roomAliasesCache.Set(string(roomID), []string{"user1|user2"})

	// Call from user1 perspective
	result1 := svc.resolveRoomIDToOtherIdentifier(nil, roomID, user1)
	assert.Equal(t, "201", result1) // Should see user2

	// Call from user2 perspective
	result2 := svc.resolveRoomIDToOtherIdentifier(nil, roomID, user2)
	assert.Equal(t, "102", result2) // Should see user1

	// Verify both are cached separately
	key1 := string(roomID) + "|" + user1
	key2 := string(roomID) + "|" + user2

	assert.Equal(t, "201", svc.roomParticipantCache.Get(key1))
	assert.Equal(t, "102", svc.roomParticipantCache.Get(key2))
}

// TestCacheMultipleRoomAliases tests cache with multiple room aliases.
func TestCacheMultipleRoomAliases(t *testing.T) {
	svc := NewMessageService(nil, nil)

	// Set multiple aliases
	for i := 0; i < 10; i++ {
		alias := "user" + string(rune('0'+i)) + "|other"
		roomID := "!room" + string(rune('0'+i)) + ":server"
		svc.roomAliasCache.Set(alias, roomID)
	}

	// Verify all are cached
	for i := 0; i < 10; i++ {
		alias := "user" + string(rune('0'+i)) + "|other"
		roomID := "!room" + string(rune('0'+i)) + ":server"
		assert.Equal(t, roomID, svc.roomAliasCache.Get(alias))
	}
}

// TestCacheEmptyAliasesList tests caching of empty aliases list.
func TestCacheEmptyAliasesList(t *testing.T) {
	svc := NewMessageService(nil, nil)

	roomID := "!room123:server"

	// Cache empty list
	svc.roomAliasesCache.Set(roomID, []string{})

	// Should return non-nil empty slice
	retrieved := svc.roomAliasesCache.Get(roomID)
	assert.NotNil(t, retrieved)
	assert.Empty(t, retrieved)

	// Get non-existent
	retrieved2 := svc.roomAliasesCache.Get("!nonexistent:server")
	assert.Nil(t, retrieved2)
}
