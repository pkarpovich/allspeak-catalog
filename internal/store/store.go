package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/pkarpovich/allspeak-catalog/internal/manifest"
)

// ErrNotFound is returned by Get, UpdateManifest, and Delete for an unknown id.
var ErrNotFound = errors.New("session not found")

// ErrExists is returned by Create when a session with the same id already exists.
var ErrExists = errors.New("session already exists")

// Session is one catalog entry: metadata plus its file manifest.
type Session struct {
	ID        string
	Title     string
	Revision  int
	CreatedAt time.Time
	UpdatedAt time.Time
	Manifest  manifest.Manifest
}

// Store is the SQLite-backed catalog.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

const schema = `CREATE TABLE IF NOT EXISTS sessions (
	id TEXT PRIMARY KEY,
	title TEXT NOT NULL,
	revision INTEGER NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	manifest TEXT NOT NULL
)`

const columns = `id, title, revision, created_at, updated_at, manifest`

// New opens (creating if absent) the SQLite catalog at path, enables WAL mode,
// and ensures the sessions table exists. It is idempotent across reopens.
func New(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	// pragmas go in the DSN so they apply to every pooled connection; busy_timeout
	// is per-connection and would otherwise only be set on one arbitrary connection.
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	return &Store{db: db, now: func() time.Time { return time.Now().UTC() }}, nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error {
	return s.db.Close()
}

// Create inserts session as a new catalog entry with revision 1. The id, title,
// and manifest come from session; revision and timestamps are assigned here. It
// returns ErrExists if a session with the same id already exists.
func (s *Store) Create(ctx context.Context, session Session) error {
	raw, err := json.Marshal(session.Manifest)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	now := s.now().Format(time.RFC3339)
	const q = `INSERT INTO sessions (` + columns + `) VALUES (?, ?, 1, ?, ?, ?)`
	if _, err := s.db.ExecContext(ctx, q, session.ID, session.Title, now, now, string(raw)); err != nil {
		var serr *sqlite.Error
		if errors.As(err, &serr) && serr.Code()&0xff == sqlite3.SQLITE_CONSTRAINT {
			return ErrExists
		}
		return fmt.Errorf("insert session %q: %w", session.ID, err)
	}
	return nil
}

// UpdateManifest replaces the title and manifest of an existing session,
// atomically increments its revision, and returns the new revision. It returns
// ErrNotFound if no session has session.ID.
func (s *Store) UpdateManifest(ctx context.Context, session Session) (int, error) {
	raw, err := json.Marshal(session.Manifest)
	if err != nil {
		return 0, fmt.Errorf("marshal manifest: %w", err)
	}
	now := s.now().Format(time.RFC3339)
	const q = `UPDATE sessions
		SET title = ?, manifest = ?, revision = revision + 1, updated_at = ?
		WHERE id = ?
		RETURNING revision`
	var revision int
	err = s.db.QueryRowContext(ctx, q, session.Title, string(raw), now, session.ID).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("update session %q: %w", session.ID, err)
	}
	return revision, nil
}

// Get returns the session with the given id, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id string) (Session, error) {
	const q = `SELECT ` + columns + ` FROM sessions WHERE id = ?`
	session, err := s.scan(s.db.QueryRowContext(ctx, q, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("get session %q: %w", id, err)
	}
	return session, nil
}

// List returns every session ordered by creation time, newest first.
func (s *Store) List(ctx context.Context) ([]Session, error) {
	const q = `SELECT ` + columns + ` FROM sessions ORDER BY created_at DESC`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var sessions []Session
	for rows.Next() {
		session, err := s.scan(rows)
		if err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}
	return sessions, nil
}

// Delete removes the session with the given id, or returns ErrNotFound.
func (s *Store) Delete(ctx context.Context, id string) error {
	const q = `DELETE FROM sessions WHERE id = ?`
	res, err := s.db.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("delete session %q: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete session %q: rows affected: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func (s *Store) scan(row rowScanner) (Session, error) {
	var (
		session   Session
		createdAt string
		updatedAt string
		raw       string
	)
	if err := row.Scan(&session.ID, &session.Title, &session.Revision, &createdAt, &updatedAt, &raw); err != nil {
		return Session{}, err
	}
	created, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return Session{}, fmt.Errorf("parse created_at: %w", err)
	}
	updated, err := time.Parse(time.RFC3339, updatedAt)
	if err != nil {
		return Session{}, fmt.Errorf("parse updated_at: %w", err)
	}
	if err := json.Unmarshal([]byte(raw), &session.Manifest); err != nil {
		return Session{}, fmt.Errorf("unmarshal manifest: %w", err)
	}
	session.CreatedAt = created
	session.UpdatedAt = updated
	return session, nil
}
