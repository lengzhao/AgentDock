package access

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/lengzhao/agentkit/cap/ingress"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(filepath.Join(t.TempDir(), "access.json"))
}

func TestUnboundRejectsStrangers(t *testing.T) {
	t.Parallel()
	store := newStore(t)
	d, err := store.Resolve("ou_stranger", []byte("hi bot"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Allow {
		t.Fatal("an unbound bot must not accept a stranger as owner")
	}
	if d.Reason == "" {
		t.Fatal("rejection should explain how to bind")
	}
}

func TestBindingRequiresChallenge(t *testing.T) {
	t.Parallel()
	store := newStore(t)
	challenge, err := store.StartBinding(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if challenge == "" {
		t.Fatal("empty challenge")
	}

	// The wrong token must not bind, even while a challenge is pending.
	d, err := store.Resolve("ou_a", []byte("bind-me"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Allow {
		t.Fatal("wrong token must not bind")
	}

	d, err = store.Resolve("ou_a", []byte(`{"text":"`+challenge+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !d.Allow || !d.Bound {
		t.Fatalf("decision = %+v, want bound", d)
	}
	owner, err := store.Owner()
	if err != nil || owner != "ou_a" {
		t.Fatalf("owner = %q, %v", owner, err)
	}

	// Replaying the same token from another account must not rebind.
	d, err = store.Resolve("ou_b", []byte(challenge))
	if err != nil {
		t.Fatal(err)
	}
	if d.Allow {
		t.Fatal("challenge must be single use")
	}
}

func TestExpiredChallengeDoesNotBind(t *testing.T) {
	t.Parallel()
	store := newStore(t)
	challenge, err := store.StartBinding(-time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	d, err := store.Resolve("ou_a", []byte(challenge))
	if err != nil {
		t.Fatal(err)
	}
	if d.Allow {
		t.Fatal("expired challenge must not bind")
	}
}

func TestOwnerAndAllowlistAfterBinding(t *testing.T) {
	t.Parallel()
	store := newStore(t)
	challenge, err := store.StartBinding(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Resolve("ou_owner", []byte(challenge)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(func(st *State) error {
		st.AllowFrom = "ou_friend, ou_other"
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		user string
		want bool
	}{
		{"ou_owner", true},
		{"ou_friend", true},
		{"ou_other", true},
		{"ou_stranger", false},
	} {
		d, err := store.Resolve(tc.user, []byte("hello"))
		if err != nil {
			t.Fatal(err)
		}
		if d.Allow != tc.want {
			t.Fatalf("%s allowed = %v, want %v (%s)", tc.user, d.Allow, tc.want, d.Reason)
		}
	}
}

func TestResolveRejectsEmptyUser(t *testing.T) {
	t.Parallel()
	store := newStore(t)
	d, err := store.Resolve("", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Allow {
		t.Fatal("a message without identity must be rejected")
	}
}

func TestPolicyAdaptsToIngress(t *testing.T) {
	t.Parallel()
	store := newStore(t)
	var bound string
	policy := &Policy{Store: store, OnBound: func(user string) { bound = user }}
	challenge, err := store.StartBinding(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := policy.Evaluate(context.Background(), ingress.Input{UserID: "ou_x", Raw: []byte(challenge)})
	if err != nil {
		t.Fatal(err)
	}
	if dec.Reject {
		t.Fatalf("binding message rejected: %s", dec.Reason)
	}
	if bound != "ou_x" {
		t.Fatalf("OnBound not called, bound=%q", bound)
	}
	dec, err = policy.Evaluate(context.Background(), ingress.Input{UserID: "ou_y", Raw: []byte("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Reject {
		t.Fatal("non-owner must be rejected")
	}
}

func TestStoreSurvivesCorruptState(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "access.json")
	if err := writeRaw(path, "{not json"); err != nil {
		t.Fatal(err)
	}
	store := NewStore(path)
	if _, err := store.Load(); err == nil {
		t.Fatal("corrupt state must be reported, not treated as empty")
	}
	d, err := store.Resolve("ou_a", []byte("x"))
	if err == nil {
		t.Fatal("resolve over corrupt state must surface the error")
	}
	if d.Allow {
		t.Fatal("must fail closed")
	}
}
