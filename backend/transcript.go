package backend

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

const transcriptSchema = `
CREATE TABLE IF NOT EXISTS transcript (
	id         REAL PRIMARY KEY,     -- row ID chosen by the UI
	at         REAL NOT NULL,        -- when it was spoken (unix ms)
	text       TEXT NOT NULL,
	ai         TEXT,                 -- JSON AI reply, NULL if never sent
	source     TEXT NOT NULL DEFAULT '', -- '' = spoken, 'typed' = written in the chat box
	attachments TEXT,                -- JSON []Attachment, NULL if none
	updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS transcript_at ON transcript (at, id);`

// TranscriptRow is one transcribed phrase, plus the AI reply if it was sent.
type TranscriptRow struct {
	ID          float64        `json:"id"`
	Text        string         `json:"text"`
	At          float64        `json:"at"`
	AI          map[string]any `json:"ai,omitempty"`
	Source      string         `json:"source,omitempty"` // "" = spoken, "typed" = chat box
	Attachments []Attachment   `json:"attachments,omitempty"`
}

// migrateTranscript adds columns that older databases lack.
func migrateTranscript(db *sql.DB) error {
	for _, c := range []struct{ name, def string }{
		{"source", "TEXT NOT NULL DEFAULT ''"},
		{"attachments", "TEXT"},
	} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('transcript') WHERE name = ?`, c.name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := db.Exec(`ALTER TABLE transcript ADD COLUMN ` + c.name + ` ` + c.def); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---- store --------------------------------------------------------------

// LoadTranscript returns every row in spoken order.
func (s *Store) LoadTranscript() ([]TranscriptRow, error) {
	rows, err := s.db.Query(`SELECT id, at, text, ai, source, attachments FROM transcript ORDER BY at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TranscriptRow{}
	for rows.Next() {
		var r TranscriptRow
		var ai, atts *string
		if err := rows.Scan(&r.ID, &r.At, &r.Text, &ai, &r.Source, &atts); err != nil {
			return nil, err
		}
		if ai != nil {
			_ = json.Unmarshal([]byte(*ai), &r.AI)
		}
		if atts != nil && *atts != "" {
			_ = json.Unmarshal([]byte(*atts), &r.Attachments)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SyncTranscript upserts the changed rows and deletes the removed ones in one
// transaction.
func (s *Store) SyncTranscript(upserts []TranscriptRow, deletes []float64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if len(deletes) > 0 {
		del, err := tx.Prepare(`DELETE FROM transcript WHERE id = ?`)
		if err != nil {
			return err
		}
		defer del.Close()
		for _, id := range deletes {
			if _, err := del.Exec(id); err != nil {
				return err
			}
		}
	}
	if len(upserts) > 0 {
		up, err := tx.Prepare(`INSERT INTO transcript (id, at, text, ai, source, attachments, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET at = excluded.at, text = excluded.text, ai = excluded.ai,
			source = excluded.source, attachments = excluded.attachments, updated_at = excluded.updated_at`)
		if err != nil {
			return err
		}
		defer up.Close()
		now := nowMs()
		for _, r := range upserts {
			var ai any // NULL
			if r.AI != nil {
				b, err := json.Marshal(r.AI)
				if err != nil {
					return fmt.Errorf("satır %v: %w", r.ID, err)
				}
				ai = string(b)
			}
			var atts any // NULL
			if len(r.Attachments) > 0 {
				b, err := json.Marshal(r.Attachments)
				if err != nil {
					return err
				}
				atts = string(b)
			}
			if _, err := up.Exec(r.ID, r.At, r.Text, ai, r.Source, atts, now); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// ---- service ------------------------------------------------------------

// TranscriptService keeps the transcript in the local SQLite database.
type TranscriptService struct{ store *Store }

func NewTranscriptService(store *Store) *TranscriptService {
	return &TranscriptService{store: store}
}

// Load returns the saved transcript, oldest first.
func (t *TranscriptService) Load() ([]TranscriptRow, error) { return t.store.LoadTranscript() }

// Sync saves changed rows and removes deleted ones.
func (t *TranscriptService) Sync(upserts []TranscriptRow, deletes []float64) error {
	return t.store.SyncTranscript(upserts, deletes)
}
