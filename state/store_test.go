package state

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestAcceptIsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTemp(t)
	task := Task{Key: "lark:inst:om_1", Installation: "inst", Platform: "lark", ChatID: "oc_1", UserID: "ou_1"}
	if err := s.Accept(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := s.Accept(ctx, task); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("second accept = %v, want ErrDuplicate", err)
	}
	got, err := s.Get(ctx, task.Key)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusAccepted {
		t.Fatalf("status = %s", got.Status)
	}
}

func TestStatusTransitions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTemp(t)
	key := "lark:inst:om_2"
	if err := s.Accept(ctx, Task{Key: key, Installation: "inst", Platform: "lark"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus(ctx, key, StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus(ctx, key, StatusCompleted, "replied"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusCompleted || got.Detail != "replied" {
		t.Fatalf("task = %+v", got)
	}
	if err := s.SetStatus(ctx, "missing", StatusFailed, ""); err == nil {
		t.Fatal("updating a missing task must fail")
	}
}

func TestMarkInterruptedOnlyTouchesNonTerminal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTemp(t)
	for key, status := range map[string]Status{
		"a": StatusAccepted,
		"b": StatusRunning,
		"c": StatusCompleted,
		"d": StatusFailed,
		"e": StatusCancelled,
	} {
		if err := s.Accept(ctx, Task{Key: key, Installation: "inst", Platform: "lark"}); err != nil {
			t.Fatal(err)
		}
		if err := s.SetStatus(ctx, key, status, ""); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.MarkInterrupted(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("interrupted %d tasks, want 2", n)
	}
	for key, want := range map[string]Status{
		"a": StatusInterrupted,
		"b": StatusInterrupted,
		"c": StatusCompleted,
		"d": StatusFailed,
		"e": StatusCancelled,
	} {
		got, err := s.Get(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != want {
			t.Fatalf("%s = %s, want %s", key, got.Status, want)
		}
	}
	// A second run must not relabel already-interrupted tasks as fresh ones.
	n, err = s.MarkInterrupted(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("second run interrupted %d, want 0", n)
	}
}

func TestDeliveryLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTemp(t)
	key := "lark:inst:om_3"
	if err := s.Accept(ctx, Task{Key: key, Installation: "inst", Platform: "lark"}); err != nil {
		t.Fatal(err)
	}
	d, err := s.RecordDelivery(ctx, key, DeliveryUnknown, "send timed out")
	if err != nil {
		t.Fatal(err)
	}
	if d.Attempts != 1 {
		t.Fatalf("attempts = %d", d.Attempts)
	}
	pending, err := s.PendingDeliveries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Status != DeliveryUnknown {
		t.Fatalf("pending = %+v", pending)
	}
	if err := s.UpdateDelivery(ctx, d.ID, DeliveryPending, "retrying after reconciliation"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateDelivery(ctx, d.ID, DeliverySent, "ok"); err != nil {
		t.Fatal(err)
	}
	pending, err = s.PendingDeliveries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("still pending: %+v", pending)
	}
}

func TestPurgeKeepsRecent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTemp(t)
	old := Task{Key: "old", Installation: "inst", Platform: "lark",
		CreatedAt: time.Now().Add(-48 * time.Hour)}
	if err := s.Accept(ctx, old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE tasks SET updated_at = ? WHERE key = 'old'`,
		time.Now().Add(-48*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	fresh := Task{Key: "fresh", Installation: "inst", Platform: "lark"}
	if err := s.Accept(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	n, err := s.Purge(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("purged %d, want 1", n)
	}
	if _, err := s.Get(ctx, "fresh"); err != nil {
		t.Fatalf("fresh task removed: %v", err)
	}
}

func TestReopenKeepsData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Accept(ctx, Task{Key: "k", Installation: "inst", Platform: "lark"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.Get(ctx, "k"); err != nil {
		t.Fatalf("task lost across reopen: %v", err)
	}
}
