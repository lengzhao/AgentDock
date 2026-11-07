package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lengzhao/AgentDock/access"
	"github.com/lengzhao/AgentDock/config"
	"github.com/lengzhao/AgentDock/credentials"
)

func TestSetupReportsAutomaticallyBoundOwnerAsComplete(t *testing.T) {
	previousEnv, previousClient, previousStdin := rootEnv, http.DefaultClient, os.Stdin
	t.Cleanup(func() { rootEnv, http.DefaultClient, os.Stdin = previousEnv, previousClient, previousStdin })
	dir := t.TempDir()
	rootEnv = &env{paths: config.ResolvePaths(filepath.Join(dir, "home")), storeOverride: "memory"}
	if err := os.WriteFile(filepath.Join(dir, "npx"), []byte("#!/bin/sh\necho 1.0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	secretPath := filepath.Join(dir, "secret")
	if err := os.WriteFile(secretPath, []byte("test-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	os.Stdin = f
	http.DefaultClient = &http.Client{Transport: ownerTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"code":0,"data":{"app":{"creator_id":"ou_owner"}}}`
		if strings.Contains(r.URL.Path, "tenant_access_token") {
			body = `{"code":0,"tenant_access_token":"test-token","expire":7200}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	var out bytes.Buffer
	cmd := newSetupCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--app-id", "test-app", "--app-secret-stdin", "--agent", "codex-acp", "--from-catalog=false"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	_, remaining, _ := strings.Cut(out.String(), "Remaining setup steps:")
	if strings.Contains(remaining, "owner") {
		t.Fatalf("completed owner still listed: %s", remaining)
	}
	if !strings.Contains(out.String(), "Owner bound:") {
		t.Fatal("setup did not bind owner")
	}
}

type ownerTransport func(*http.Request) (*http.Response, error)

func (f ownerTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAutoBindApplicationOwner(t *testing.T) {
	for _, tc := range []struct {
		name, existing, response, want string
		wantErr                        bool
	}{
		{"new owner", "", `{"code":0,"data":{"app":{"creator_id":"ou_owner"}}}`, "ou_owner", false},
		{"preserve existing", "ou_existing", "", "ou_existing", false},
		{"lookup failure", "", `{"code":210508}`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldEnv, oldClient := rootEnv, http.DefaultClient
			t.Cleanup(func() { rootEnv, http.DefaultClient = oldEnv, oldClient })
			rootEnv = &env{paths: config.ResolvePaths(t.TempDir())}
			store := access.NewStore(accessFile())
			if _, err := store.StartBinding(time.Minute); err != nil {
				t.Fatal(err)
			}
			if tc.existing != "" {
				if _, err := store.Update(func(s *access.State) error { s.OwnerOpenID = tc.existing; return nil }); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			http.DefaultClient = &http.Client{Transport: ownerTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				body := tc.response
				if strings.Contains(r.URL.Path, "tenant_access_token") {
					body = `{"code":0,"tenant_access_token":"test-token","expire":7200}`
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			creds := credentials.NewMemoryStore()
			if err := creds.Set(context.Background(), "platform", "app-id", "test-app"); err != nil {
				t.Fatal(err)
			}
			if err := creds.Set(context.Background(), "platform", "app-secret", "test-secret"); err != nil {
				t.Fatal(err)
			}
			cfg := config.Default()
			cfg.Platform.AppIDRef, cfg.Platform.AppSecretRef = "keyring://platform/app-id", "keyring://platform/app-secret"
			err := autoBindApplicationOwner(context.Background(), io.Discard, cfg, credentials.NewResolver(creds, nil))
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v", err)
			}
			st, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			if st.OwnerOpenID != tc.want {
				t.Errorf("owner = %q, want %q", st.OwnerOpenID, tc.want)
			}
			if tc.existing != "" && calls != 0 {
				t.Fatal("existing owner must skip network/credential lookup")
			}
			if tc.name == "new owner" && (st.Challenge != "" || !st.ChallengeDeadline.IsZero()) {
				t.Error("stale challenge was not cleared")
			}
			if tc.wantErr && st.Challenge == "" {
				t.Error("lookup failure must preserve manual binding")
			}
		})
	}
}
