package service

import (
	"sync"
	"time"
)

// cacheEntry is a wrapper around a cached value with an expiration time.
type cacheEntry[T any] struct {
	Value     T
	ExpiresAt time.Time
}

// isExpired checks if the cache entry has expired based on the current time.
func (e *cacheEntry[T]) isExpired(now time.Time) bool {
	return now.After(e.ExpiresAt)
}

// RoomAliasCache caches room alias to room ID mappings (e.g., "user1|user2" -> "!roomid:server").
// This is used by ensureDirectRoom to avoid repeated ResolveRoomAlias calls.
type RoomAliasCache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry[string]
	ttl     time.Duration
	now     func() time.Time
}

// NewRoomAliasCache creates a new RoomAliasCache with the specified TTL.
func NewRoomAliasCache(ttl time.Duration) *RoomAliasCache {
	return &RoomAliasCache{
		entries: make(map[string]cacheEntry[string]),
		ttl:     ttl,
		now:     time.Now,
	}
}

// Get retrieves a cached room ID for the given alias, or returns empty string if not found or expired.
func (c *RoomAliasCache) Get(alias string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[alias]
	if !ok {
		return ""
	}

	if entry.isExpired(c.now()) {
		// Entry expired, but we don't remove it here to avoid write lock
		return ""
	}

	return entry.Value
}

// Set stores a room ID for the given alias with TTL expiration.
func (c *RoomAliasCache) Set(alias string, roomID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[alias] = cacheEntry[string]{
		Value:     roomID,
		ExpiresAt: c.now().Add(c.ttl),
	}
}

// Clear removes all entries from the cache.
func (c *RoomAliasCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]cacheEntry[string])
}

// RoomAliasesCache caches room ID to room aliases mappings (e.g., "!roomid:server" -> ["user1|user2"]).
// This is used by resolveRoomIDToOtherIdentifier to avoid repeated GetRoomAliases calls.
type RoomAliasesCache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry[[]string]
	ttl     time.Duration
	now     func() time.Time
}

// NewRoomAliasesCache creates a new RoomAliasesCache with the specified TTL.
func NewRoomAliasesCache(ttl time.Duration) *RoomAliasesCache {
	return &RoomAliasesCache{
		entries: make(map[string]cacheEntry[[]string]),
		ttl:     ttl,
		now:     time.Now,
	}
}

// Get retrieves cached aliases for the given room ID, or returns nil if not found or expired.
func (c *RoomAliasesCache) Get(roomID string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[roomID]
	if !ok {
		return nil
	}

	if entry.isExpired(c.now()) {
		// Entry expired, but we don't remove it here to avoid write lock
		return nil
	}

	// Return a copy to avoid external mutation
	// Handle empty slices explicitly to preserve non-nil empty slices
	if len(entry.Value) == 0 {
		return []string{}
	}
	return append([]string(nil), entry.Value...)
}

// Set stores aliases for the given room ID with TTL expiration.
func (c *RoomAliasesCache) Set(roomID string, aliases []string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Store a copy to avoid external mutations
	// Preserve empty slices as non-nil
	var aliasCopy []string
	if len(aliases) > 0 {
		aliasCopy = append([]string(nil), aliases...)
	} else {
		aliasCopy = []string{}
	}
	c.entries[roomID] = cacheEntry[[]string]{
		Value:     aliasCopy,
		ExpiresAt: c.now().Add(c.ttl),
	}
}

// Clear removes all entries from the cache.
func (c *RoomAliasesCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]cacheEntry[[]string])
}

// RoomParticipantCache caches room ID to other participant identifier mappings
// (e.g., "!roomid:server" -> "201" or "@user:server").
// This is used by resolveRoomIDToOtherIdentifier to avoid recomputing the other participant.
type RoomParticipantCache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry[string]
	ttl     time.Duration
	now     func() time.Time
}

// NewRoomParticipantCache creates a new RoomParticipantCache with the specified TTL.
func NewRoomParticipantCache(ttl time.Duration) *RoomParticipantCache {
	return &RoomParticipantCache{
		entries: make(map[string]cacheEntry[string]),
		ttl:     ttl,
		now:     time.Now,
	}
}

// Get retrieves the cached other participant identifier for the given room ID,
// or returns empty string if not found or expired.
func (c *RoomParticipantCache) Get(key string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[key]
	if !ok {
		return ""
	}

	if entry.isExpired(c.now()) {
		// Entry expired, but we don't remove it here to avoid write lock
		return ""
	}

	return entry.Value
}

// Set stores the other participant identifier for the given room ID with TTL expiration.
// The key is "roomID|myMatrixID" to handle different perspectives (same room, different viewers).
func (c *RoomParticipantCache) Set(key string, identifier string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[key] = cacheEntry[string]{
		Value:     identifier,
		ExpiresAt: c.now().Add(c.ttl),
	}
}

// Clear removes all entries from the cache.
func (c *RoomParticipantCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]cacheEntry[string])
}
