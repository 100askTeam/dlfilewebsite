package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

type User struct {
	ID           int64
	Username     string
	PasswordHash string
	Role         string
	Disabled     bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Session struct {
	User
	CSRFHash []byte
	Expires  time.Time
}

type AuditEvent struct {
	ID         int64  `json:"id"`
	OccurredAt string `json:"occurred_at"`
	Actor      string `json:"actor"`
	Action     string `json:"action"`
	ObjectType string `json:"object_type"`
	ObjectID   string `json:"object_id"`
	RemoteIP   string `json:"remote_ip"`
	Outcome    string `json:"outcome"`
	Details    string `json:"details_json"`
}

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=FULL",
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("configure database: %w", err)
		}
	}
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("secure database permissions: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer tx.Rollback()
	statements := []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL CHECK(role IN ('admin','release_manager','uploader','auditor')),
			disabled INTEGER NOT NULL DEFAULT 0 CHECK(disabled IN (0,1)),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token_hash BLOB PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			csrf_hash BLOB NOT NULL,
			created_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS sessions_expiry_idx ON sessions(expires_at)`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			occurred_at INTEGER NOT NULL,
			actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
			actor TEXT NOT NULL,
			action TEXT NOT NULL,
			object_type TEXT NOT NULL,
			object_id TEXT NOT NULL,
			remote_ip TEXT NOT NULL,
			user_agent TEXT NOT NULL,
			outcome TEXT NOT NULL CHECK(outcome IN ('success','failure','denied')),
			details_json TEXT NOT NULL DEFAULT '{}'
		)`,
		`CREATE INDEX IF NOT EXISTS audit_events_time_idx ON audit_events(occurred_at DESC)`,
		`INSERT OR IGNORE INTO schema_migrations(version,name,applied_at)
		 VALUES(1,'secure_auth_sessions_audit',unixepoch())`,
		`CREATE TABLE IF NOT EXISTS releases (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			product TEXT NOT NULL,
			channel TEXT NOT NULL,
			version TEXT NOT NULL,
			status TEXT NOT NULL CHECK(status IN ('staged','published','superseded')),
			notes TEXT NOT NULL,
			manifest_json TEXT NOT NULL,
			staged_path TEXT NOT NULL,
			published_path TEXT NOT NULL DEFAULT '',
			created_by INTEGER NOT NULL REFERENCES users(id),
			created_at INTEGER NOT NULL,
			published_at INTEGER,
			UNIQUE(product,channel,version)
		)`,
		`CREATE TABLE IF NOT EXISTS release_assets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			release_id INTEGER NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
			target TEXT NOT NULL,
			kind TEXT NOT NULL CHECK(kind IN ('full','delta')),
			from_version TEXT NOT NULL DEFAULT '',
			file_name TEXT NOT NULL,
			size INTEGER NOT NULL,
			sha256 TEXT NOT NULL,
			signature TEXT NOT NULL,
			mirrors_json TEXT NOT NULL DEFAULT '[]',
			storage TEXT NOT NULL DEFAULT 'site' CHECK(storage IN ('site','external')),
			UNIQUE(release_id,target,kind,from_version)
		)`,
		`CREATE TABLE IF NOT EXISTS channel_heads (
			product TEXT NOT NULL,
			channel TEXT NOT NULL,
			release_id INTEGER NOT NULL REFERENCES releases(id),
			updated_at INTEGER NOT NULL,
			PRIMARY KEY(product,channel)
		)`,
		`INSERT OR IGNORE INTO schema_migrations(version,name,applied_at)
		 VALUES(2,'release_staging_and_channel_heads',unixepoch())`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply migration: %w", err)
		}
	}
	assetColumns, err := tx.QueryContext(ctx, `PRAGMA table_info(release_assets)`)
	if err != nil {
		return fmt.Errorf("inspect release_assets columns: %w", err)
	}
	hasStorage := false
	for assetColumns.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := assetColumns.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = assetColumns.Close()
			return fmt.Errorf("scan release_assets column: %w", err)
		}
		if name == "storage" {
			hasStorage = true
		}
	}
	if err := assetColumns.Close(); err != nil {
		return fmt.Errorf("close release_assets columns: %w", err)
	}
	if !hasStorage {
		if _, err := tx.ExecContext(ctx,
			`ALTER TABLE release_assets ADD COLUMN storage TEXT NOT NULL DEFAULT 'site' CHECK(storage IN ('site','external'))`); err != nil {
			return fmt.Errorf("add release asset storage: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO schema_migrations(version,name,applied_at)
		 VALUES(3,'external_release_asset_storage',unixepoch())`); err != nil {
		return fmt.Errorf("record release asset storage migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}

func (s *Store) UserCount(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return count, nil
}

func (s *Store) CreateUser(ctx context.Context, username, passwordHash, role string) (int64, error) {
	now := time.Now().Unix()
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO users(username,password_hash,role,created_at,updated_at)
		 VALUES(?1,?2,?3,?4,?4)`, username, passwordHash, role, now)
	if err != nil {
		return 0, fmt.Errorf("create user: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read user id: %w", err)
	}
	return id, nil
}

func (s *Store) UserByUsername(ctx context.Context, username string) (User, error) {
	var user User
	var disabled int
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id,username,password_hash,role,disabled,created_at,updated_at
		 FROM users WHERE username=?1`, username).
		Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Role, &disabled, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("read user: %w", err)
	}
	user.Disabled = disabled != 0
	user.CreatedAt = time.Unix(createdAt, 0).UTC()
	user.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return user, nil
}

func (s *Store) UpdatePassword(ctx context.Context, userID int64, passwordHash string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE users SET password_hash=?1,updated_at=?2 WHERE id=?3`,
		passwordHash, time.Now().Unix(), userID)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read password update result: %w", err)
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateSession(
	ctx context.Context,
	tokenHash []byte,
	csrfHash []byte,
	userID int64,
	expires time.Time,
) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions(token_hash,user_id,csrf_hash,created_at,expires_at)
		 VALUES(?1,?2,?3,?4,?5)`, tokenHash, userID, csrfHash, now, expires.Unix())
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (s *Store) Session(ctx context.Context, tokenHash []byte, now time.Time) (Session, error) {
	var session Session
	var disabled int
	var createdAt, updatedAt, expiresAt int64
	err := s.db.QueryRowContext(ctx,
		`SELECT u.id,u.username,u.password_hash,u.role,u.disabled,u.created_at,u.updated_at,
		        s.csrf_hash,s.expires_at
		 FROM sessions s JOIN users u ON u.id=s.user_id
		 WHERE s.token_hash=?1 AND s.expires_at>?2`, tokenHash, now.Unix()).Scan(
		&session.ID, &session.Username, &session.PasswordHash, &session.Role, &disabled,
		&createdAt, &updatedAt, &session.CSRFHash, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("read session: %w", err)
	}
	session.Disabled = disabled != 0
	session.CreatedAt = time.Unix(createdAt, 0).UTC()
	session.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	session.Expires = time.Unix(expiresAt, 0).UTC()
	return session, nil
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?1`, tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (s *Store) DeleteUserSessions(ctx context.Context, userID int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?1`, userID); err != nil {
		return fmt.Errorf("delete user sessions: %w", err)
	}
	return nil
}

func (s *Store) PurgeExpiredSessions(ctx context.Context, now time.Time) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at<=?1`, now.Unix()); err != nil {
		return fmt.Errorf("purge expired sessions: %w", err)
	}
	return nil
}

func (s *Store) AppendAudit(
	ctx context.Context,
	actorUserID *int64,
	actor, action, objectType, objectID, remoteIP, userAgent, outcome, details string,
) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_events(
			occurred_at,actor_user_id,actor,action,object_type,object_id,
			remote_ip,user_agent,outcome,details_json
		 ) VALUES(?1,?2,?3,?4,?5,?6,?7,?8,?9,?10)`,
		time.Now().Unix(), actorUserID, actor, action, objectType, objectID,
		remoteIP, userAgent, outcome, details)
	if err != nil {
		return fmt.Errorf("append audit: %w", err)
	}
	return nil
}

func (s *Store) ListAudit(ctx context.Context, limit int) ([]AuditEvent, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,occurred_at,actor,action,object_type,object_id,remote_ip,outcome,details_json
		 FROM audit_events ORDER BY id DESC LIMIT ?1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list audit: %w", err)
	}
	defer rows.Close()
	events := make([]AuditEvent, 0)
	for rows.Next() {
		var event AuditEvent
		var occurredAt int64
		if err := rows.Scan(&event.ID, &occurredAt, &event.Actor, &event.Action,
			&event.ObjectType, &event.ObjectID, &event.RemoteIP, &event.Outcome, &event.Details); err != nil {
			return nil, fmt.Errorf("scan audit: %w", err)
		}
		event.OccurredAt = time.Unix(occurredAt, 0).UTC().Format(time.RFC3339)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit: %w", err)
	}
	return events, nil
}
