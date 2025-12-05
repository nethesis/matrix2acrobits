package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRoomAliasCacheBasic tests basic cache operations: Set, Get, expiration.
func TestRoomAliasCacheBasic(t *testing.T) {
	// Create cache with 1 second TTL
	cache := NewRoomAliasCache(1 * time.Second)

	// Mock time
	now := time.Now()
	cache.now = func() time.Time { return now }

	// Test Set and Get
	cache.Set("user1|user2", "!room123:server")
	result := cache.Get("user1|user2")
	assert.Equal(t, "!room123:server", result)

	// Test Get for non-existent key
	result = cache.Get("nonexistent")
	assert.Equal(t, "", result)

	// Test expiration
	now = now.Add(2 * time.Second)
	result = cache.Get("user1|user2")
	assert.Equal(t, "", result, "entry should be expired")
}

// TestRoomAliasCacheConcurrency tests concurrent access to RoomAliasCache.
func TestRoomAliasCacheConcurrency(t *testing.T) {
	cache := NewRoomAliasCache(10 * time.Second)
	done := make(chan bool, 10)

	// Writer goroutines
	for i := 0; i < 5; i++ {
		go func(idx int) {
			key := "alias" + string(rune(idx))
			for j := 0; j < 100; j++ {
				cache.Set(key, "roomid"+string(rune(j)))
			}
			done <- true
		}(i)
	}

	// Reader goroutines
	for i := 0; i < 5; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = cache.Get("alias0")
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

// TestRoomAliasCacheClear tests cache clearing.
func TestRoomAliasCacheClear(t *testing.T) {
	cache := NewRoomAliasCache(10 * time.Second)

	cache.Set("alias1", "room1")
	cache.Set("alias2", "room2")

	// Before clear
	assert.Equal(t, "room1", cache.Get("alias1"))

	cache.Clear()

	// After clear
	assert.Equal(t, "", cache.Get("alias1"))
	assert.Equal(t, "", cache.Get("alias2"))
}

// TestRoomAliasesCacheBasic tests basic operations for RoomAliasesCache.
func TestRoomAliasesCacheBasic(t *testing.T) {
	cache := NewRoomAliasesCache(1 * time.Second)

	now := time.Now()
	cache.now = func() time.Time { return now }

	// Test Set and Get
	aliases := []string{"alias1", "alias2", "alias3"}
	cache.Set("!room:server", aliases)

	result := cache.Get("!room:server")
	assert.Equal(t, aliases, result)

	// Test that returned slice is a copy (modifications don't affect cache)
	result[0] = "modified"
	result2 := cache.Get("!room:server")
	assert.Equal(t, "alias1", result2[0], "cache should return a copy")

	// Test expiration
	now = now.Add(2 * time.Second)
	result = cache.Get("!room:server")
	assert.Nil(t, result, "entry should be expired")
}

// TestRoomAliasesCacheEmptySlice tests handling of empty slices.
func TestRoomAliasesCacheEmptySlice(t *testing.T) {
	cache := NewRoomAliasesCache(10 * time.Second)

	// Store empty slice
	cache.Set("!room:server", []string{})
	result := cache.Get("!room:server")
	assert.Empty(t, result)
}

// TestRoomAliasesCacheClear tests cache clearing.
func TestRoomAliasesCacheClear(t *testing.T) {
	cache := NewRoomAliasesCache(10 * time.Second)

	cache.Set("room1", []string{"alias1"})
	cache.Set("room2", []string{"alias2"})

	assert.Equal(t, []string{"alias1"}, cache.Get("room1"))

	cache.Clear()

	assert.Nil(t, cache.Get("room1"))
	assert.Nil(t, cache.Get("room2"))
}

// TestRoomParticipantCacheBasic tests basic operations for RoomParticipantCache.
func TestRoomParticipantCacheBasic(t *testing.T) {
	cache := NewRoomParticipantCache(1 * time.Second)

	now := time.Now()
	cache.now = func() time.Time { return now }

	// Test Set and Get
	cache.Set("!room:server|@user:server", "201")
	result := cache.Get("!room:server|@user:server")
	assert.Equal(t, "201", result)

	// Test Get for non-existent key
	result = cache.Get("nonexistent")
	assert.Equal(t, "", result)

	// Test expiration
	now = now.Add(2 * time.Second)
	result = cache.Get("!room:server|@user:server")
	assert.Equal(t, "", result, "entry should be expired")
}

// TestRoomParticipantCacheClear tests cache clearing.
func TestRoomParticipantCacheClear(t *testing.T) {
	cache := NewRoomParticipantCache(10 * time.Second)

	cache.Set("key1", "participant1")
	cache.Set("key2", "participant2")

	assert.Equal(t, "participant1", cache.Get("key1"))

	cache.Clear()

	assert.Equal(t, "", cache.Get("key1"))
	assert.Equal(t, "", cache.Get("key2"))
}

// TestCacheEntryExpiration tests the cacheEntry.isExpired method directly.
func TestCacheEntryExpiration(t *testing.T) {
	now := time.Now()
	ttl := 1 * time.Second

	entry := cacheEntry[string]{
		Value:     "test",
		ExpiresAt: now.Add(ttl),
	}

	// Before expiration
	assert.False(t, entry.isExpired(now))
	assert.False(t, entry.isExpired(now.Add(500*time.Millisecond)))

	// At expiration boundary
	assert.False(t, entry.isExpired(now.Add(ttl)))

	// After expiration
	assert.True(t, entry.isExpired(now.Add(ttl+1*time.Millisecond)))
}

// TestRoomAliasCacheMultipleEntries tests cache with multiple entries.
func TestRoomAliasCacheMultipleEntries(t *testing.T) {
	cache := NewRoomAliasCache(10 * time.Second)

	// Add multiple entries
	for i := 0; i < 100; i++ {
		key := "alias" + fmt.Sprintf("%d", i%10)
		roomID := "room" + fmt.Sprintf("%d", i%10)
		cache.Set(key, roomID)
	}

	// Verify entries
	result := cache.Get("alias0")
	assert.Equal(t, "room0", result)

	result = cache.Get("alias5")
	assert.Equal(t, "room5", result)
}

// TestRoomAliasesCacheCopyPrevention tests that modifications to returned slices don't affect cache.
func TestRoomAliasesCacheCopyPrevention(t *testing.T) {
	cache := NewRoomAliasesCache(10 * time.Second)

	original := []string{"alias1", "alias2", "alias3"}
	cache.Set("!room:server", original)

	// Get and modify
	retrieved := cache.Get("!room:server")
	require.NotNil(t, retrieved)
	retrieved[0] = "modified"
	retrieved = append(retrieved, "added")

	// Verify cache is unchanged
	cached := cache.Get("!room:server")
	assert.Equal(t, 3, len(cached), "cache should still have original length")
	assert.Equal(t, "alias1", cached[0], "cache should have original value")
}

// TestRoomParticipantCacheConcurrency tests concurrent access patterns.
func TestRoomParticipantCacheConcurrency(t *testing.T) {
	cache := NewRoomParticipantCache(10 * time.Second)
	done := make(chan bool, 20)

	// Writer goroutines
	for i := 0; i < 10; i++ {
		go func(idx int) {
			for j := 0; j < 50; j++ {
				key := "room" + string(rune(idx)) + "|user" + string(rune(j))
				cache.Set(key, "participant"+string(rune(idx)))
			}
			done <- true
		}(i)
	}

	// Reader goroutines
	for i := 0; i < 10; i++ {
		go func(idx int) {
			for j := 0; j < 50; j++ {
				key := "room" + string(rune(idx)) + "|user" + string(rune(j))
				_ = cache.Get(key)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 20; i++ {
		<-done
	}
}

// TestCacheTTLVariation tests caches with different TTL values.
func TestCacheTTLVariation(t *testing.T) {
	shortCache := NewRoomAliasCache(100 * time.Millisecond)
	longCache := NewRoomAliasCache(10 * time.Second)

	now := time.Now()
	shortCache.now = func() time.Time { return now }
	longCache.now = func() time.Time { return now }

	shortCache.Set("alias1", "room1")
	longCache.Set("alias1", "room1")

	// Both should have the value initially
	assert.Equal(t, "room1", shortCache.Get("alias1"))
	assert.Equal(t, "room1", longCache.Get("alias1"))

	// Move time forward slightly
	now = now.Add(200 * time.Millisecond)

	// Short cache should be expired, long cache should not
	assert.Equal(t, "", shortCache.Get("alias1"))
	assert.Equal(t, "room1", longCache.Get("alias1"))
}

// TestRoomAliasesCacheNilReturnVsEmpty tests distinction between expired and never-set.
func TestRoomAliasesCacheNilReturnVsEmpty(t *testing.T) {
	cache := NewRoomAliasesCache(1 * time.Second)

	now := time.Now()
	cache.now = func() time.Time { return now }

	// Set with empty slice
	cache.Set("room1", []string{})
	result1 := cache.Get("room1")
	assert.NotNil(t, result1, "should return empty slice, not nil")
	assert.Equal(t, 0, len(result1))

	// Get non-existent key
	result2 := cache.Get("room2")
	assert.Nil(t, result2, "non-existent key should return nil")

	// Expire the entry and try to get it
	now = now.Add(2 * time.Second)
	result3 := cache.Get("room1")
	assert.Nil(t, result3, "expired entry should return nil")
}
