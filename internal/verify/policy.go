// Package verify implements the end-to-end verification marker.
//
// `agentdock verify` prints a one-time token and waits. Only a message that
// actually reaches the agent layer can produce that token, so the marker is
// written from the inbound policy — the one place every accepted message passes
// through. A connected WebSocket that never delivers a message therefore cannot
// produce a false pass (LARK-03).
package verify

import (
	"context"
	"regexp"
	"sync"

	"github.com/lengzhao/agentkit/cap/ingress"
	"github.com/lengzhao/pluginkit"

	"github.com/lengzhao/AgentDock/internal/config"
	"github.com/lengzhao/AgentDock/internal/doctor"
)

// TokenPattern matches the tokens printed by `agentdock verify`.
var TokenPattern = regexp.MustCompile(`AGENTDOCK-[0-9a-f]{16}`)

// Config configures the slot.
type Config struct {
	// StateDir holds the marker file.
	StateDir string `json:"stateDir"`
}

// Deps is the decorated policy. Composition happens in the graph, which keeps
// this package from knowing about the owner policy at all.
type Deps struct {
	// Inner is evaluated first; a rejected message is never recorded as a pass.
	Inner ingress.Policy `json:"inner"`
}

// Policy is an ingress.Policy decorator that records verification tokens.
type Policy struct {
	inner  ingress.Policy
	marker string
	state  string

	mu   sync.Mutex
	seen map[string]bool
}

// Kind is the pluginkit kind for this slot.
const Kind = "agentdock/policy-verify"

func init() {
	pluginkit.Register(Kind, New)
}

// New builds the policy.
func New(cfg Config, deps Deps) (ingress.Policy, error) {
	return &Policy{
		inner:  deps.Inner,
		marker: doctor.VerifyMarkerFile(config.Paths{StateDir: cfg.StateDir}),
		state:  cfg.StateDir,
		seen:   map[string]bool{},
	}, nil
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
	// Only an accepted message proves the path works end to end.
	if decision.Reject || len(in.Raw) == 0 {
		return decision, nil
	}
	for _, token := range TokenPattern.FindAll(in.Raw, -1) {
		p.record(string(token))
	}
	return decision, nil
}

func (p *Policy) record(token string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.seen[token] {
		return
	}
	p.seen[token] = true
	_ = doctor.WriteVerifyMarker(config.Paths{StateDir: p.state}, token)
}
