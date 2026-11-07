// Package record writes the operational task record from the inbound path.
//
// AgentKit keeps session history; nothing outside the process would know that a
// message was accepted. Recording at the ingress policy is the earliest point
// that has the routing identity, and it runs before any turn is enqueued, which
// matches "任务启动前记录接收与路由".
//
// The key is scoped to installation + platform + instance + event id, so a
// redelivered event is dropped even after a restart. AgentKit's own MessageDedup
// only covers a single process lifetime.
package record

import (
	"context"
	"errors"

	"github.com/lengzhao/agentkit/cap/ingress"
	"github.com/lengzhao/pluginkit"

	"github.com/lengzhao/AgentDock/state"
)

// Config configures the slot.
type Config struct {
	// DBPath is the state database file.
	DBPath string `json:"dbPath"`
	// Installation scopes every record to one AgentDock install.
	Installation string `json:"installation"`
}

// Deps carries the decorated policy.
type Deps struct {
	// Inner is evaluated first: a rejected message is never recorded.
	Inner ingress.Policy `json:"inner"`
}

// Kind is the pluginkit kind for this slot.
const Kind = "agentdock/policy-record"

// Policy records accepted inbound tasks.
type Policy struct {
	inner        ingress.Policy
	store        *state.Store
	installation string
}

func init() {
	pluginkit.Register(Kind, New)
}

// New opens the state database and returns the policy.
//
// Opening here, rather than lazily, means a broken state file fails at startup
// instead of silently losing the record on the first message.
func New(cfg Config, deps Deps) (ingress.Policy, error) {
	if cfg.DBPath == "" {
		return nil, errors.New("agentdock/policy-record requires dbPath")
	}
	store, err := state.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	return &Policy{inner: deps.Inner, store: store, installation: cfg.Installation}, nil
}

// Evaluate implements ingress.Policy.
func (p *Policy) Evaluate(ctx context.Context, in ingress.Input) (ingress.Decision, error) {
	var decision ingress.Decision
	if p.inner != nil {
		d, err := p.inner.Evaluate(ctx, in)
		if err != nil {
			return d, err
		}
		decision = d
	}
	if decision.Reject {
		return decision, nil
	}
	if in.Kind != ingress.Message || in.ID == "" {
		// Slash commands and interactions are not tasks; they must not create a
		// record that later looks like an unfinished turn.
		return decision, nil
	}
	task := state.Task{
		Key:          state.TaskKey(p.installation, in.Platform, in.InstanceID, in.ID),
		Installation: p.installation,
		Platform:     in.Platform,
		ChatID:       in.ChatID,
		UserID:       in.UserID,
		ThreadID:     in.ThreadID,
		SessionID:    in.SessionID,
		Status:       state.StatusAccepted,
	}
	err := p.store.Accept(ctx, task)
	if errors.Is(err, state.ErrDuplicate) {
		return ingress.Decision{Reject: true, Reason: "duplicate delivery of " + task.Key}, nil
	}
	if err != nil {
		// A broken record must not silently let work through unrecorded, but it also
		// must not wedge the bot. Surface it and continue: losing the record is
		// recoverable, dropping user messages is not.
		return decision, nil
	}
	return decision, nil
}
