package record_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lengzhao/agentkit/cap/ingress"

	"github.com/lengzhao/AgentDock/internal/record"
	"github.com/lengzhao/AgentDock/internal/state"
)

func newPolicy(t *testing.T) (ingress.Policy, *state.Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	policy, err := record.New(record.Config{DBPath: dbPath, Installation: "inst-1"}, record.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return policy, store
}

func messageInput(id string) ingress.Input {
	return ingress.Input{
		Platform:   "lark",
		InstanceID: "platform.main",
		Kind:       ingress.Message,
		ID:         id,
		ChatID:     "oc_1",
		UserID:     "ou_1",
		SessionID:  "lark:oc_1",
	}
}

func TestRecordsAcceptedMessage(t *testing.T) {
	t.Parallel()
	policy, store := newPolicy(t)
	in := messageInput("om_1")
	if _, err := policy.Evaluate(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	task, err := store.Get(context.Background(), state.TaskKey("inst-1", "lark", "platform.main", "om_1"))
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != state.StatusAccepted {
		t.Fatalf("status = %s", task.Status)
	}
	if task.ChatID != "oc_1" || task.UserID != "ou_1" || task.SessionID != "lark:oc_1" {
		t.Fatalf("routing identity not recorded: %+v", task)
	}
}

func TestDuplicateDeliveryIsRejected(t *testing.T) {
	t.Parallel()
	policy, store := newPolicy(t)
	in := messageInput("om_2")
	if _, err := policy.Evaluate(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	dec, err := policy.Evaluate(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Reject {
		t.Fatal("a redelivered event must not start a second turn")
	}
	tasks, err := store.List(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(tasks))
	}
}

func TestRejectedMessageIsNotRecorded(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	policy, err := record.New(record.Config{DBPath: dbPath, Installation: "inst-1"},
		record.Deps{Inner: rejectAll{}})
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if _, err := policy.Evaluate(context.Background(), messageInput("om_3")); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.List(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("rejected message left %d task rows", len(tasks))
	}
}

func TestInteractionsAreNotTasks(t *testing.T) {
	t.Parallel()
	policy, store := newPolicy(t)
	in := messageInput("evt_1")
	in.Kind = ingress.Interaction
	if _, err := policy.Evaluate(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.List(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatal("a card interaction is not an accepted task")
	}
}

func TestPerProcessDedupIsNotEnough(t *testing.T) {
	t.Parallel()
	// Two policies over the same database stand in for a restart: the second
	// process must still drop the redelivered event.
	dbPath := filepath.Join(t.TempDir(), "state.db")
	first, err := record.New(record.Config{DBPath: dbPath, Installation: "inst-1"}, record.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Evaluate(context.Background(), messageInput("om_4")); err != nil {
		t.Fatal(err)
	}
	second, err := record.New(record.Config{DBPath: dbPath, Installation: "inst-1"}, record.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	dec, err := second.Evaluate(context.Background(), messageInput("om_4"))
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Reject {
		t.Fatal("dedup must survive a process restart")
	}
}

func TestDifferentInstallsDoNotCollide(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	a, err := record.New(record.Config{DBPath: dbPath, Installation: "inst-a"}, record.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := record.New(record.Config{DBPath: dbPath, Installation: "inst-b"}, record.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Evaluate(context.Background(), messageInput("om_5")); err != nil {
		t.Fatal(err)
	}
	dec, err := b.Evaluate(context.Background(), messageInput("om_5"))
	if err != nil {
		t.Fatal(err)
	}
	if dec.Reject {
		t.Fatal("the same event id in another installation is not a duplicate")
	}
}

type rejectAll struct{}

func (rejectAll) Evaluate(context.Context, ingress.Input) (ingress.Decision, error) {
	return ingress.Decision{Reject: true, Reason: "not the owner"}, nil
}
