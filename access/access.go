// Package access implements the owner-binding and allowlist rules (ACL-01).
//
// The rule is deliberately strict in one direction: while no owner is bound,
// every inbound message is rejected. That is the opposite of "the first stranger
// to message the bot becomes the owner", which ACL-01 forbids.
package access

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/lengzhao/agentkit/cap/ingress"

	"github.com/lengzhao/AgentDock/config"
)

// State is the persisted access state. It contains no secrets beyond the
// one-time challenge, which is short-lived and worthless after binding.
type State struct {
	// OwnerOpenID is the bound owner, empty when unbound.
	OwnerOpenID string `json:"ownerOpenId,omitempty"`
	// AllowFrom is the additional open_id allowlist (comma separated in config).
	AllowFrom string `json:"allowFrom,omitempty"`
	// Challenge is the pending one-time binding token.
	Challenge string `json:"challenge,omitempty"`
	// ChallengeDeadline is when the pending challenge expires.
	ChallengeDeadline time.Time `json:"challengeDeadline,omitempty"`
	// UpdatedAt is the last write time.
	UpdatedAt time.Time `json:"updatedAt"`
}

// Store reads and writes access state.
type Store struct {
	path string
	mu   sync.Mutex
}

// NewStore returns a store backed by path.
func NewStore(path string) *Store { return &Store{path: path} }

// Load reads the state; missing file is an empty state.
func (s *Store) Load() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *Store) loadLocked() (State, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return State{}, nil
		}
		return State{}, fmt.Errorf("read access state: %w", err)
	}
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		return State{}, fmt.Errorf("parse access state: %w", err)
	}
	return st, nil
}

// Update applies fn to the state and persists the result atomically.
func (s *Store) Update(fn func(*State) error) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// A stable lock file serializes read/modify/write across Store instances and
	// processes. Do not unlink it: waiters must continue locking the same inode.
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return State{}, fmt.Errorf("create access state directory: %w", err)
	}
	f, err := os.OpenFile(s.path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return State{}, fmt.Errorf("open access state lock: %w", err)
	}
	defer f.Close()
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		return State{}, fmt.Errorf("lock access state: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	st, err := s.loadLocked()
	if err != nil {
		return State{}, err
	}
	if err := fn(&st); err != nil {
		return st, err
	}
	st.UpdatedAt = time.Now().UTC()
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return st, err
	}
	if err := config.WriteFileAtomic(s.path, raw); err != nil {
		return st, err
	}
	return st, nil
}

// NewChallenge returns a one-time binding token.
func NewChallenge() (string, error) {
	buf := make([]byte, 9)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "bind-" + hex.EncodeToString(buf), nil
}

// StartBinding issues a fresh challenge valid for ttl and returns it.
func (s *Store) StartBinding(ttl time.Duration) (string, error) {
	challenge, err := NewChallenge()
	if err != nil {
		return "", err
	}
	_, err = s.Update(func(st *State) error {
		st.Challenge = challenge
		st.ChallengeDeadline = time.Now().Add(ttl).UTC()
		return nil
	})
	if err != nil {
		return "", err
	}
	return challenge, nil
}

// Decision is the outcome of one admission check.
type Decision struct {
	Allow  bool
	Reason string
	// Bound is true when this message completed the owner binding.
	Bound bool
}

// Resolve decides whether userID may talk to the bot. raw is the platform's raw
// message payload, searched for a pending challenge token.
//
// The search is a byte scan rather than a parse: the token is unguessable and
// only needs to appear in the message, which keeps this policy independent of
// per-platform envelope shapes.
func (s *Store) Resolve(userID string, raw []byte) (Decision, error) {
	st, err := s.Load()
	if err != nil {
		return Decision{Reason: "access state unreadable: " + err.Error()}, err
	}
	if userID == "" {
		return Decision{Reason: "message has no user identity"}, nil
	}
	if st.OwnerOpenID != "" {
		if userID == st.OwnerOpenID || allowed(st.AllowFrom, userID) {
			return Decision{Allow: true}, nil
		}
		return Decision{Reason: "sender " + userID + " is not the bound owner or an allowed user"}, nil
	}
	// Unbound: only a valid challenge binds, and only once.
	challenge := st.Challenge
	if challenge == "" || time.Now().After(st.ChallengeDeadline) {
		return Decision{Reason: "no owner is bound and no valid binding challenge is pending; run `agentdock access bind`"}, nil
	}
	if !strings.Contains(string(raw), challenge) {
		return Decision{Reason: "owner binding is pending; send the challenge printed by `agentdock access bind`"}, nil
	}
	updated, err := s.Update(func(cur *State) error {
		if cur.OwnerOpenID != "" {
			return fmt.Errorf("owner was bound by another message")
		}
		if cur.Challenge != challenge {
			return fmt.Errorf("binding challenge changed")
		}
		cur.OwnerOpenID = userID
		cur.Challenge = ""
		cur.ChallengeDeadline = time.Time{}
		return nil
	})
	if err != nil {
		return Decision{Reason: "binding failed: " + err.Error()}, nil
	}
	return Decision{Allow: true, Bound: true, Reason: "bound " + updated.OwnerOpenID + " as owner"}, nil
}

func allowed(list, userID string) bool {
	for _, part := range strings.Split(list, ",") {
		if strings.TrimSpace(part) == userID {
			return true
		}
	}
	return false
}

// Policy adapts the Store to AgentKit's ingress.Policy slot.
type Policy struct {
	Store *Store
	// OnBound is called after a successful binding, for logging.
	OnBound func(userID string)
}

var _ ingress.Policy = (*Policy)(nil)

// Evaluate implements ingress.Policy.
//
// It only ever vetoes: an allowed message returns an empty Decision so the
// platform's default plan (which already handles mentions, groups and threads)
// is untouched. The check runs on every inbound message, before any turn is
// enqueued, so an unauthorized sender never reaches an agent.
func (p *Policy) Evaluate(_ context.Context, in ingress.Input) (ingress.Decision, error) {
	decision, err := p.Store.Resolve(in.UserID, in.Raw)
	if err != nil {
		return ingress.Decision{Reject: true, Reason: "access check failed: " + err.Error()}, nil
	}
	if !decision.Allow {
		return ingress.Decision{Reject: true, Reason: decision.Reason}, nil
	}
	if decision.Bound && p.OnBound != nil {
		p.OnBound(in.UserID)
	}
	return ingress.Decision{Reason: decision.Reason}, nil
}

// Owner returns the bound owner open_id, or "" when unbound.
func (s *Store) Owner() (string, error) {
	st, err := s.Load()
	if err != nil {
		return "", err
	}
	return st.OwnerOpenID, nil
}
