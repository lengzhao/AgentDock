package schedule

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lengzhao/AgentDock/access"
	"github.com/lengzhao/agentkit"
	capschedule "github.com/lengzhao/agentkit/cap/schedule"
	"github.com/lengzhao/agentkit/runtime/rctx"
)

func testStore(t *testing.T, max int) (capschedule.Registry, string, capschedule.Engine) {
	t.Helper()
	engine, err := NewEngine(EngineConfig{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "schedules.json")
	store, err := NewStore(StoreConfig{Path: path, MaxJobs: max}, StoreDeps{Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	return store, path, engine
}
func jobFor(user string) capschedule.Job {
	return capschedule.Job{Kind: capschedule.KindDelay, FireAt: time.Now().Add(time.Hour), Prompt: "remind", Source: capschedule.SourceAgent,
		Actor: agentkit.ActorRef{UserID: user}, Route: rctx.BuildSessionRoute(agentkit.SessionRouteInput{Platform: "lark", Instance: "lark", ChannelID: "chat", ThreadID: "thread"})}
}
func TestStoreConcurrentWritesAndAtomicPerActorLimit(t *testing.T) {
	store, path, engine := testStore(t, 3)
	var accepted atomic.Int64
	var wg sync.WaitGroup
	for range 24 {
		wg.Go(func() {
			if _, err := store.Add(context.Background(), jobFor("owner")); err == nil {
				accepted.Add(1)
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 3 {
		t.Fatalf("accepted %d", accepted.Load())
	}
	if _, err := store.Add(context.Background(), jobFor("another-owner")); err != nil {
		t.Fatalf("other actor shares limit: %v", err)
	}
	reopened, err := NewStore(StoreConfig{Path: path}, StoreDeps{Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := reopened.List(context.Background())
	if err != nil || len(jobs) != 4 {
		t.Fatalf("jobs %d err %v", len(jobs), err)
	}
	stat, _ := os.Stat(path)
	if stat.Mode().Perm() != 0600 {
		t.Fatalf("mode %v", stat.Mode())
	}
}
func TestStoreOneShotClaimSurvivesRestartWithoutReplay(t *testing.T) {
	store, path, engine := testStore(t, 32)
	job, err := store.Add(context.Background(), jobFor("owner"))
	if err != nil {
		t.Fatal(err)
	}
	due, err := store.Due(context.Background(), job.FireAt.Add(time.Second))
	if err != nil || len(due) != 1 {
		t.Fatalf("due %d: %v", len(due), err)
	}
	if !due[0].Fired || due[0].InFlightAt.IsZero() {
		t.Fatal("claim not durably terminal before enqueue")
	}
	restarted, err := NewStore(StoreConfig{Path: path}, StoreDeps{Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	again, err := restarted.Due(context.Background(), job.FireAt.Add(time.Hour))
	if err != nil || len(again) != 0 {
		t.Fatalf("uncertain one-shot replayed: %v", err)
	}
	if err := restarted.MarkFired(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	jobs, _ := restarted.List(context.Background())
	if !jobs[0].Fired || !jobs[0].InFlightAt.IsZero() {
		t.Fatal("claim completion not reflected")
	}
}
func TestEngineUsesShanghaiWithoutGlobalTimezone(t *testing.T) {
	engine, err := NewEngine(EngineConfig{})
	if err != nil {
		t.Fatal(err)
	}
	after := time.Date(2030, 1, 1, 0, 30, 0, 0, time.UTC)
	next, ok := engine.NextFire(capschedule.Job{Cron: "0 9 * * *", Kind: capschedule.KindCron}, after)
	if !ok || !next.Equal(time.Date(2030, 1, 1, 1, 0, 0, 0, time.UTC)) {
		t.Fatalf("next %s", next)
	}
	cron, err := engine.ParseCron("0 9 * * *")
	if err != nil {
		t.Fatal(err)
	}
	parsed, ok := cron.Next(after)
	if !ok || !parsed.Equal(next) {
		t.Fatal("ParseCron timezone differs")
	}
}
func trustedContext(platform, user, channel, thread string) context.Context {
	route := rctx.BuildSessionRoute(agentkit.SessionRouteInput{Platform: platform, Instance: platform, ChannelID: channel, ThreadID: thread})
	return rctx.ApplyEnvelopeToContext(context.Background(), agentkit.TurnEnvelope{Route: route, Actor: agentkit.ActorRef{UserID: user}, AgentID: "claude"})
}
func TestScopedRegistryAuthorizesAndIsolatesPlatformConversationActor(t *testing.T) {
	store, _, _ := testStore(t, 32)
	paths := map[string]string{}
	for _, kind := range []string{"lark", "weixin"} {
		path := filepath.Join(t.TempDir(), kind+"-access.json")
		if _, err := access.NewStore(path).Update(func(st *access.State) error { st.OwnerOpenID = "owner"; st.AllowFrom = "coworker"; return nil }); err != nil {
			t.Fatal(err)
		}
		paths[kind] = path
	}
	scoped, err := NewScoped(ScopedConfig{AccessPaths: paths}, ScopedDeps{Inner: store})
	if err != nil {
		t.Fatal(err)
	}
	ctx := rctx.WithWorkspace(trustedContext("lark", "owner", "chat", "thread"), "original-workspace")
	spoof := jobFor("attacker")
	spoof.Actor.UserID = "attacker"
	spoof.AgentID = "evil"
	spoof.Route = rctx.BuildSessionRoute(agentkit.SessionRouteInput{Platform: "weixin", ChannelID: "other"})
	first, err := scoped.Add(ctx, spoof)
	if err != nil {
		t.Fatal(err)
	}
	if first.ChannelKey != "original-workspace" {
		t.Fatalf("workspace lost: %s", first.ChannelKey)
	}
	if first.Actor.UserID != "owner" || first.AgentID != "claude" || first.Route.Platform != "lark" {
		t.Fatal("trusted identity not stamped")
	}
	for _, other := range []context.Context{
		trustedContext("weixin", "owner", "chat", "thread"), trustedContext("lark", "coworker", "chat", "thread"),
		trustedContext("lark", "owner", "other-chat", "thread"), trustedContext("lark", "owner", "chat", "other-thread"),
	} {
		jobs, err := scoped.List(other)
		if err != nil || len(jobs) != 0 {
			t.Fatalf("cross-scope list: %d %v", len(jobs), err)
		}
		removed, err := scoped.Remove(other, first.ID)
		if err != nil || removed {
			t.Fatalf("cross-scope removal: %v %v", removed, err)
		}
	}
	jobs, err := scoped.List(rctx.WithWorkspace(ctx, "different-workspace"))
	if err != nil || len(jobs) != 1 {
		t.Fatalf("workspace altered persistence scope: %v", err)
	}
	for _, untrusted := range []context.Context{context.Background(), trustedContext("lark", "stranger", "chat", "thread"), trustedContext("lark", "", "chat", "thread"), trustedContext("unknown", "owner", "chat", "thread")} {
		if _, err := scoped.List(untrusted); err == nil {
			t.Fatal("unauthorized context listed jobs")
		}
		if _, err := scoped.Add(untrusted, jobFor("owner")); err == nil {
			t.Fatal("unauthorized context added job")
		}
	}
	if removed, err := scoped.Remove(ctx, first.ID); err != nil || !removed {
		t.Fatalf("own remove: %v %v", removed, err)
	}
	if err := scoped.SyncSource(ctx, "config", nil); err == nil {
		t.Fatal("scoped registry reconciled global config")
	}
	if _, err := scoped.Due(ctx, time.Now()); err == nil {
		t.Fatal("scoped registry claimed global jobs")
	}
}
func TestStoreCronSkipsMissedBoundariesAndProtectsIDs(t *testing.T) {
	store, _, _ := testStore(t, 32)
	now := time.Now().UTC().Truncate(time.Minute)
	job := jobFor("owner")
	job.Kind = capschedule.KindCron
	job.Cron = "* * * * *"
	job.FireAt = time.Time{}
	job.LastRun = now.Add(-10 * time.Minute)
	saved, err := store.Add(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	due, err := store.Due(context.Background(), now)
	if err != nil || len(due) != 1 {
		t.Fatalf("due: %v", err)
	}
	if !due[0].LastRun.Equal(now) {
		t.Fatal("cron backfilled instead of advancing")
	}
	again, err := store.Due(context.Background(), now)
	if err != nil || len(again) != 0 {
		t.Fatal("same boundary fired twice")
	}
	job.ID = saved.ID
	job.Actor.UserID = "other"
	if _, err := store.Add(context.Background(), job); err == nil {
		t.Fatal("existing ID replaced")
	}
	if err := store.MarkFired(context.Background(), "missing"); err == nil {
		t.Fatal("unknown completion accepted")
	}
	if _, err := NewStore(StoreConfig{Path: "relative.json"}, StoreDeps{Engine: mustEngine(t)}); err == nil {
		t.Fatal("relative schedule file accepted")
	}
}
func mustEngine(t *testing.T) capschedule.Engine {
	t.Helper()
	e, err := NewEngine(EngineConfig{})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestIdlePollDoesNotRewriteDurableStore(t *testing.T) {
	store, path, _ := testStore(t, 32)
	if _, err := store.Add(context.Background(), jobFor("owner")); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Due(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("idle polling rewrote schedule file")
	}
}
