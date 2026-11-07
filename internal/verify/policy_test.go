package verify

import (
	"context"
	"os"
	"testing"

	"github.com/lengzhao/agentkit/cap/ingress"

	"github.com/lengzhao/AgentDock/internal/config"
	"github.com/lengzhao/AgentDock/internal/doctor"
)

func markerFor(t *testing.T) (config.Paths, string) {
	t.Helper()
	paths := config.Paths{StateDir: t.TempDir()}
	return paths, doctor.VerifyMarkerFile(paths)
}

func TestRecordsTokenFromAcceptedMessage(t *testing.T) {
	t.Parallel()
	paths, marker := markerFor(t)
	p := &Policy{marker: marker, state: paths.StateDir, seen: map[string]bool{}}

	const token = "AGENTDOCK-0123456789abcdef"
	dec, err := p.Evaluate(context.Background(), ingress.Input{
		UserID: "ou_owner",
		Raw:    []byte(`{"text":"` + token + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if dec.Reject {
		t.Fatalf("decision rejected: %s", dec.Reason)
	}
	ok, err := doctor.ReadVerifyMarker(marker, token)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("token was not recorded")
	}
}

func TestDoesNotRecordForRejectedMessage(t *testing.T) {
	t.Parallel()
	paths, marker := markerFor(t)
	p := &Policy{
		inner:  rejectAll{},
		marker: marker,
		state:  paths.StateDir,
		seen:   map[string]bool{},
	}
	const token = "AGENTDOCK-fedcba9876543210"
	dec, err := p.Evaluate(context.Background(), ingress.Input{
		UserID: "ou_stranger",
		Raw:    []byte(`{"text":"` + token + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Reject {
		t.Fatal("inner rejection must be preserved")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("a rejected message must never satisfy verify")
	}
}

func TestIgnoresTextWithoutToken(t *testing.T) {
	t.Parallel()
	paths, marker := markerFor(t)
	p := &Policy{marker: marker, state: paths.StateDir, seen: map[string]bool{}}
	if _, err := p.Evaluate(context.Background(), ingress.Input{Raw: []byte(`{"text":"hello"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("no marker expected without a token")
	}
}

func TestTokenPattern(t *testing.T) {
	t.Parallel()
	if !TokenPattern.MatchString("AGENTDOCK-0123456789abcdef") {
		t.Fatal("valid token not matched")
	}
	if TokenPattern.MatchString("AGENTDOCK-0123") {
		t.Fatal("short token must not match")
	}
	if TokenPattern.MatchString("agentdock-0123456789abcdef") {
		t.Fatal("lowercase must not match")
	}
}

type rejectAll struct{}

func (rejectAll) Evaluate(context.Context, ingress.Input) (ingress.Decision, error) {
	return ingress.Decision{Reject: true, Reason: "not the owner"}, nil
}
