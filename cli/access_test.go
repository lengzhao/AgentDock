package cli

import (
	"github.com/lengzhao/AgentDock/setup"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/lengzhao/AgentDock/access"
	"github.com/lengzhao/AgentDock/config"
)

func TestBindChallengeHasAdvertisedFifteenMinuteLifetime(t *testing.T) {
	previous := rootEnv
	rootEnv = &env{paths: config.ResolvePaths(t.TempDir())}
	t.Cleanup(func() { rootEnv = previous })
	cmd := newAccessCmd()
	cmd.SetArgs([]string{"bind"})
	cmd.SetOut(io.Discard)
	start := time.Now()
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	st, err := access.NewStore(accessFile()).Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.ChallengeDeadline.Before(start.Add(15*time.Minute)) || st.ChallengeDeadline.After(time.Now().Add(15*time.Minute)) {
		t.Fatalf("binding lifetime = %s; want 15 minutes", st.ChallengeDeadline.Sub(start))
	}
}

func TestAdditionalOwnerStatusDoesNotCompletePrimarySetup(t *testing.T) {
	previous := rootEnv
	rootEnv = &env{paths: config.ResolvePaths(t.TempDir())}
	t.Cleanup(func() { rootEnv = previous })
	cfg := config.Default()
	cfg.Platform.AppIDRef = "env:LARK_ID"
	cfg.Platform.AppSecretRef = "env:LARK_SECRET"
	cfg.Agents = []config.Agent{{ID: "agent", Command: []string{"agent"}}}
	cfg.Runtime.DefaultAgent = "agent"
	cfg.UpsertPlatform(config.Platform{Kind: "weixin", Credentials: map[string]string{"id": "env:WX_ID", "secret": "env:WX_TOKEN"}})
	if err := config.Save(rootEnv.paths.ConfigFile, cfg); err != nil {
		t.Fatal(err)
	}
	paths := rootEnv.paths.ForPlatform("weixin", "lark")
	if _, err := access.NewStore(filepath.Join(paths.StateDir, "access.json")).Update(func(st *access.State) error { st.OwnerOpenID = "wx-owner"; return nil }); err != nil {
		t.Fatal(err)
	}
	cmd := newAccessCmd()
	cmd.SetArgs([]string{"--platform", "weixin", "status"})
	cmd.SetOut(io.Discard)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	primary, err := setup.LoadState(rootEnv.paths)
	if err != nil {
		t.Fatal(err)
	}
	if primary.Done(setup.StepOwner) {
		t.Fatal("Weixin owner marked primary setup complete")
	}
	added, err := setup.LoadState(paths)
	if err != nil || !added.Done(setup.StepOwner) {
		t.Fatal("Weixin setup progress not marked")
	}
}
