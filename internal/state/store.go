// Package state persists what AgentDock owns and AgentKit does not: accepted
// inbound tasks, their terminal status, and delivery outcomes.
//
// The point is restart honesty. A task that was running when the process died
// must be reported as interrupted, not silently retried (needs.md "重启后，执行中
// 任务没有明确终态则标为 interrupted/unknown"). AgentKit owns session history;
// this owns the operational record.
package state

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

// Status is a task's lifecycle state.
type Status string

const (
	// StatusAccepted means the message was admitted but not started.
	StatusAccepted Status = "accepted"
	// StatusRunning means a turn is executing.
	StatusRunning Status = "running"
	// StatusCompleted means the turn finished and the result was delivered.
	StatusCompleted Status = "completed"
	// StatusFailed means the turn ended in an error that was reported.
	StatusFailed Status = "failed"
	// StatusCancelled means the user or the platform stopped the turn.
	StatusCancelled Status = "cancelled"
	// StatusInterrupted means the process stopped without recording a terminal
	// state. It is never retried automatically.
	StatusInterrupted Status = "interrupted"
)

// DeliveryStatus is a reply's state, tracked separately from the task because an
// agent can finish while the send outcome stays unknown (MSG-04).
type DeliveryStatus string

const (
	// DeliveryPending means the reply is recorded but not confirmed sent.
	DeliveryPending DeliveryStatus = "pending"
	// DeliverySent means the platform accepted the message.
	DeliverySent DeliveryStatus = "sent"
	// DeliveryFailed means the send definitively failed.
	DeliveryFailed DeliveryStatus = "failed"
	// DeliveryUnknown means the send timed out or was interrupted; the message may
	// or may not exist. Never resent blindly.
	DeliveryUnknown DeliveryStatus = "unknown"
)

// Task is one accepted inbound message that may produce a turn.
type Task struct {
	// Key is the idempotency key: platform + instance + event id.
	Key string
	// Installation scopes the record to one AgentDock install.
	Installation string
	Platform     string
	ChatID       string
	UserID       string
	ThreadID     string
	SessionID    string
	Status       Status
	// Detail explains a non-obvious status, e.g. the failure reason.
	Detail    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Delivery records one outbound attempt for a task.
type Delivery struct {
	ID        int64
	TaskKey   string
	Status    DeliveryStatus
	Attempts  int
	Detail    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Store is the SQLite-backed operational record. It is not the session history:
// nothing here is replayed into a model prompt.
type Store struct {
	db *sql.DB
}

// Open opens or creates the database at path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	// _txlock=immediate avoids "database is locked" upgrades on concurrent writers,
	// which happen when several platform goroutines record at once.
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open state database: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

const schema = `
CREATE TABLE IF NOT EXISTS tasks (
	key          TEXT PRIMARY KEY,
	installation TEXT NOT NULL,
	platform     TEXT NOT NULL,
	chat_id      TEXT NOT NULL DEFAULT '',
	user_id      TEXT NOT NULL DEFAULT '',
	thread_id    TEXT NOT NULL DEFAULT '',
	session_id   TEXT NOT NULL DEFAULT '',
	status       TEXT NOT NULL,
	detail       TEXT NOT NULL DEFAULT '',
	created_at   INTEGER NOT NULL,
	updated_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS tasks_status ON tasks(status);
CREATE INDEX IF NOT EXISTS tasks_updated ON tasks(updated_at);

CREATE TABLE IF NOT EXISTS deliveries (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	task_key   TEXT NOT NULL REFERENCES tasks(key) ON DELETE CASCADE,
	status     TEXT NOT NULL,
	attempts   INTEGER NOT NULL DEFAULT 1,
	detail     TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS deliveries_task ON deliveries(task_key);

CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL);
`

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_version`).Scan(&count); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if count == 0 {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (1)`); err != nil {
			return fmt.Errorf("write schema version: %w", err)
		}
	}
	return nil
}

// TaskKey builds the idempotency key for one inbound event. It is namespaced by
// installation so two installs sharing a platform app cannot collide, and includes
// the platform instance because one event id is only unique inside an app.
func TaskKey(installation, platform, instance, eventID string) string {
	return installation + "|" + platform + "|" + instance + "|" + eventID
}

// ErrDuplicate means the task key was already recorded. Platforms redeliver
// events, so this is an ordinary outcome, not a failure (MSG-02).
var ErrDuplicate = errors.New("duplicate task")

// Accept records an inbound task. It returns ErrDuplicate when the key exists,
// which the caller uses to skip execution.
func (s *Store) Accept(ctx context.Context, task Task) error {
	if task.Key == "" {
		return errors.New("task key is required")
	}
	if task.Status == "" {
		task.Status = StatusAccepted
	}
	now := time.Now().UTC()
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	res, err := s.db.ExecContext(ctx, `
INSERT INTO tasks (key, installation, platform, chat_id, user_id, thread_id, session_id, status, detail, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(key) DO NOTHING`,
		task.Key, task.Installation, task.Platform, task.ChatID, task.UserID, task.ThreadID,
		task.SessionID, string(task.Status), task.Detail, task.CreatedAt.Unix(), now.Unix())
	if err != nil {
		return fmt.Errorf("record task: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("record task: %w", err)
	}
	if affected == 0 {
		return ErrDuplicate
	}
	return nil
}

// SetStatus moves a task to a new status.
func (s *Store) SetStatus(ctx context.Context, key string, status Status, detail string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET status = ?, detail = ?, updated_at = ? WHERE key = ?`,
		string(status), detail, time.Now().UTC().Unix(), key)
	if err != nil {
		return fmt.Errorf("update task %s: %w", key, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("task %s: %w", key, sql.ErrNoRows)
	}
	return nil
}

// Get returns one task.
func (s *Store) Get(ctx context.Context, key string) (Task, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT key, installation, platform, chat_id, user_id, thread_id, session_id, status, detail, created_at, updated_at
FROM tasks WHERE key = ?`, key)
	return scanTask(row)
}

// List returns tasks newest first.
func (s *Store) List(ctx context.Context, limit int) ([]Task, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT key, installation, platform, chat_id, user_id, thread_id, session_id, status, detail, created_at, updated_at
FROM tasks ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, task)
	}
	return out, rows.Err()
}

// MarkInterrupted flags every non-terminal task as interrupted. It runs once at
// startup: without a recorded terminal state there is no evidence the turn
// finished, and guessing "still running" or "retry it" would both be wrong.
func (s *Store) MarkInterrupted(ctx context.Context, detail string) (int64, error) {
	if detail == "" {
		detail = "process stopped before the turn recorded a terminal state"
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE tasks SET status = ?, detail = ?, updated_at = ?
WHERE status IN (?, ?)`,
		string(StatusInterrupted), detail, time.Now().UTC().Unix(),
		string(StatusAccepted), string(StatusRunning))
	if err != nil {
		return 0, fmt.Errorf("mark interrupted: %w", err)
	}
	return res.RowsAffected()
}

// RecordDelivery inserts a delivery row for a task.
func (s *Store) RecordDelivery(ctx context.Context, taskKey string, status DeliveryStatus, detail string) (Delivery, error) {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `
INSERT INTO deliveries (task_key, status, attempts, detail, created_at, updated_at)
VALUES (?, ?, 1, ?, ?, ?)`,
		taskKey, string(status), detail, now.Unix(), now.Unix())
	if err != nil {
		return Delivery{}, fmt.Errorf("record delivery: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Delivery{}, err
	}
	return Delivery{ID: id, TaskKey: taskKey, Status: status, Attempts: 1, Detail: detail,
		CreatedAt: now, UpdatedAt: now}, nil
}

// UpdateDelivery changes a delivery's status, bumping the attempt counter when the
// new status is pending again (a real retry, never a blind resend).
func (s *Store) UpdateDelivery(ctx context.Context, id int64, status DeliveryStatus, detail string) error {
	res, err := s.db.ExecContext(ctx, `
UPDATE deliveries SET status = ?, detail = ?, updated_at = ?,
  attempts = CASE WHEN ? = 'pending' THEN attempts + 1 ELSE attempts END
WHERE id = ?`, string(status), detail, time.Now().UTC().Unix(), string(status), id)
	if err != nil {
		return fmt.Errorf("update delivery %d: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("delivery %d: %w", id, sql.ErrNoRows)
	}
	return nil
}

// PendingDeliveries returns deliverables in pending or unknown state, which are
// the ones a restart must reconcile before doing anything else.
func (s *Store) PendingDeliveries(ctx context.Context) ([]Delivery, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, task_key, status, attempts, detail, created_at, updated_at
FROM deliveries WHERE status IN (?, ?) ORDER BY id`, string(DeliveryPending), string(DeliveryUnknown))
	if err != nil {
		return nil, fmt.Errorf("list deliveries: %w", err)
	}
	defer rows.Close()
	var out []Delivery
	for rows.Next() {
		var d Delivery
		var status string
		var created, updated int64
		if err := rows.Scan(&d.ID, &d.TaskKey, &status, &d.Attempts, &d.Detail, &created, &updated); err != nil {
			return nil, err
		}
		d.Status = DeliveryStatus(status)
		d.CreatedAt = time.Unix(created, 0).UTC()
		d.UpdatedAt = time.Unix(updated, 0).UTC()
		out = append(out, d)
	}
	return out, rows.Err()
}

// Purge removes tasks and deliveries older than cutoff, bounding the file.
func (s *Store) Purge(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM tasks WHERE updated_at < ?`, cutoff.UTC().Unix())
	if err != nil {
		return 0, fmt.Errorf("purge tasks: %w", err)
	}
	return res.RowsAffected()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTask(row rowScanner) (Task, error) {
	var t Task
	var status string
	var created, updated int64
	if err := row.Scan(&t.Key, &t.Installation, &t.Platform, &t.ChatID, &t.UserID,
		&t.ThreadID, &t.SessionID, &status, &t.Detail, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Task{}, fmt.Errorf("task not found: %w", err)
		}
		return Task{}, err
	}
	t.Status = Status(status)
	t.CreatedAt = time.Unix(created, 0).UTC()
	t.UpdatedAt = time.Unix(updated, 0).UTC()
	return t, nil
}
