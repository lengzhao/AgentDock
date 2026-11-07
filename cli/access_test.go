package cli

import (
	"io"
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
