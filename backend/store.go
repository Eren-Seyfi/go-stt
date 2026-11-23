package backend

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registered as "sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS settings (
	key        TEXT PRIMARY KEY,
	value      TEXT NOT NULL,        -- JSON
	updated_at INTEGER NOT NULL      -- unix ms
);
CREATE TABLE IF NOT EXISTS ai_config (
	id         INTEGER PRIMARY KEY CHECK (id = 1),
	value      TEXT NOT NULL,        -- JSON AIConfig
	updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS ai_keys (
	provider   TEXT PRIMARY KEY,
	api_key    TEXT NOT NULL,
	updated_at INTEGER NOT NULL
);`

// Store is the app's local SQLite database.
type Store struct{ db *sql.DB }

// OpenStore opens (creating if needed) the settings database. The file holds
// API keys, so it is only readable by the current user.
func OpenStore() (*Store, error) {
	p := DBPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, err
	}
	// Create the file with tight permissions before SQLite does; the -wal and
	// -shm files SQLite creates next to it copy these permissions.
	if f, err := os.OpenFile(p, os.O_RDONLY|os.O_CREATE, 0o600); err == nil {
		f.Close()
	}
	_ = os.Chmod(p, 0o600)

	dsn := "file:" + p + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite allows one writer; serialize everything
	if _, err := db.Exec(schema + transcriptSchema + statsSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("veritabanı hazırlanamadı: %w", err)
	}
	if err := migrateTranscript(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("veritabanı güncellenemedi: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func nowMs() int64 { return time.Now().UnixMilli() }

// ---- UI settings --------------------------------------------------------

// LoadSettings returns every saved setting, decoded from JSON.
func (s *Store) LoadSettings() (map[string]any, error) {
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]any{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		var val any
		if json.Unmarshal([]byte(v), &val) == nil {
			out[k] = val
		}
	}
	return out, rows.Err()
}

// SaveSettings upserts the given settings in one transaction.
func (s *Store) SaveSettings(values map[string]any) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	now := nowMs()
	for k, v := range values {
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("%s: %w", k, err)
		}
		if _, err := stmt.Exec(k, string(b), now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ClearSettings deletes every saved UI setting (back to defaults).
func (s *Store) ClearSettings() error {
	_, err := s.db.Exec(`DELETE FROM settings`)
	return err
}

// ---- AI config and keys -------------------------------------------------

// LoadAI returns the stored AI config (ok=false if none) and API keys.
func (s *Store) LoadAI() (cfg AIConfig, ok bool, keys map[string]string, err error) {
	keys = map[string]string{}
	var raw string
	switch err = s.db.QueryRow(`SELECT value FROM ai_config WHERE id = 1`).Scan(&raw); {
	case errors.Is(err, sql.ErrNoRows):
		err = nil
	case err != nil:
		return
	default:
		ok = json.Unmarshal([]byte(raw), &cfg) == nil
	}
	rows, err := s.db.Query(`SELECT provider, api_key FROM ai_keys`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var p, k string
		if err = rows.Scan(&p, &k); err != nil {
			return
		}
		keys[p] = k
	}
	err = rows.Err()
	return
}

// SaveAIConfig stores the AI config.
func (s *Store) SaveAIConfig(c AIConfig) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO ai_config (id, value, updated_at) VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, string(b), nowMs())
	return err
}

// SetAIKey stores a provider's API key, or removes it when key is "".
func (s *Store) SetAIKey(provider, key string) error {
	if key == "" {
		_, err := s.db.Exec(`DELETE FROM ai_keys WHERE provider = ?`, provider)
		return err
	}
	_, err := s.db.Exec(`INSERT INTO ai_keys (provider, api_key, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(provider) DO UPDATE SET api_key = excluded.api_key, updated_at = excluded.updated_at`,
		provider, key, nowMs())
	return err
}
