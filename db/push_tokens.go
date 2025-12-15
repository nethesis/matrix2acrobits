package db

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/nethesis/matrix2acrobits/logger"
	_ "modernc.org/sqlite"
)

// PushToken represents a stored push token record.
type PushToken struct {
	ID         int
	Selector   string
	TokenMsgs  string
	AppIDMsgs  string
	TokenCalls string
	AppIDCalls string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Database manages push token persistence using SQLite.
type Database struct {
	db *sql.DB
	mu sync.RWMutex
}

// NewDatabase initializes a SQLite database at the given path and creates the schema.
func NewDatabase(dbPath string) (*Database, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// Verify connection
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping sqlite database: %w", err)
	}

	d := &Database{db: db}

	// Create schema if needed
	if err := d.createSchema(); err != nil {
		if cerr := db.Close(); cerr != nil {
			logger.Warn().Err(cerr).Msg("failed to close sqlite database after createSchema error")
		}
		return nil, err
	}

	logger.Info().Str("path", dbPath).Msg("push token database initialized")
	return d, nil
}

// createSchema creates the push_tokens table if it doesn't exist.
func (d *Database) createSchema() error {
	query := `
	CREATE TABLE IF NOT EXISTS push_tokens (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		selector TEXT NOT NULL UNIQUE,
		token_msgs TEXT,
		appid_msgs TEXT,
		token_calls TEXT,
		appid_calls TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	`
	_, err := d.db.Exec(query)
	if err != nil {
		return fmt.Errorf("failed to create push_tokens table: %w", err)
	}
	return nil
}

// SavePushToken saves or updates a push token record by selector.
func (d *Database) SavePushToken(selector, tokenMsgs, appIDMsgs, tokenCalls, appIDCalls string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now().UTC()

	query := `
	INSERT INTO push_tokens (selector, token_msgs, appid_msgs, token_calls, appid_calls, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(selector) DO UPDATE SET
		token_msgs = excluded.token_msgs,
		appid_msgs = excluded.appid_msgs,
		token_calls = excluded.token_calls,
		appid_calls = excluded.appid_calls,
		updated_at = excluded.updated_at;
	`

	_, err := d.db.Exec(query, selector, tokenMsgs, appIDMsgs, tokenCalls, appIDCalls, now, now)
	if err != nil {
		return fmt.Errorf("failed to save push token: %w", err)
	}

	logger.Debug().Str("selector", selector).Msg("push token saved")
	return nil
}

// GetPushToken retrieves a push token by selector.
func (d *Database) GetPushToken(selector string) (*PushToken, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var pt PushToken
	query := `
	SELECT id, selector, token_msgs, appid_msgs, token_calls, appid_calls, created_at, updated_at
	FROM push_tokens
	WHERE selector = ?;
	`

	err := d.db.QueryRow(query, selector).Scan(
		&pt.ID, &pt.Selector, &pt.TokenMsgs, &pt.AppIDMsgs, &pt.TokenCalls, &pt.AppIDCalls, &pt.CreatedAt, &pt.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get push token: %w", err)
	}

	return &pt, nil
}

// GetPushTokenByPushkey retrieves a push token by the actual device token (pushkey).
// The pushkey can be either token_msgs or token_calls.
func (d *Database) GetPushTokenByPushkey(pushkey string) (*PushToken, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var pt PushToken
	query := `
	SELECT id, selector, token_msgs, appid_msgs, token_calls, appid_calls, created_at, updated_at
	FROM push_tokens
	WHERE token_msgs = ? OR token_calls = ?;
	`

	err := d.db.QueryRow(query, pushkey, pushkey).Scan(
		&pt.ID, &pt.Selector, &pt.TokenMsgs, &pt.AppIDMsgs, &pt.TokenCalls, &pt.AppIDCalls, &pt.CreatedAt, &pt.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get push token by pushkey: %w", err)
	}

	return &pt, nil
}

// DeletePushToken removes a push token by selector.
func (d *Database) DeletePushToken(selector string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	query := `DELETE FROM push_tokens WHERE selector = ?;`
	_, err := d.db.Exec(query, selector)
	if err != nil {
		return fmt.Errorf("failed to delete push token: %w", err)
	}

	logger.Debug().Str("selector", selector).Msg("push token deleted")
	return nil
}

// ListPushTokens returns all stored push tokens.
func (d *Database) ListPushTokens() ([]*PushToken, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `
	SELECT id, selector, token_msgs, appid_msgs, token_calls, appid_calls, created_at, updated_at
	FROM push_tokens
	ORDER BY updated_at DESC;
	`

	rows, err := d.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query push tokens: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var tokens []*PushToken
	for rows.Next() {
		var pt PushToken
		if err := rows.Scan(&pt.ID, &pt.Selector, &pt.TokenMsgs, &pt.AppIDMsgs, &pt.TokenCalls, &pt.AppIDCalls, &pt.CreatedAt, &pt.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan push token: %w", err)
		}
		tokens = append(tokens, &pt)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating push tokens: %w", err)
	}

	return tokens, nil
}

// ResetPushTokens deletes all push tokens from the database.
func (d *Database) ResetPushTokens() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	query := `DELETE FROM push_tokens;`
	result, err := d.db.Exec(query)
	if err != nil {
		return fmt.Errorf("failed to reset push tokens: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	logger.Info().Int64("rows_deleted", rowsAffected).Msg("push tokens database reset")
	return nil
}

// Close closes the database connection.
func (d *Database) Close() error {
	if d.db != nil {
		return d.db.Close()
	}
	return nil
}
