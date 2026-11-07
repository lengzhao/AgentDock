package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/lengzhao/AgentDock/access"
	"github.com/lengzhao/AgentDock/state"
	"github.com/lengzhao/agentkit"
	capschedule "github.com/lengzhao/agentkit/cap/schedule"
	"github.com/lengzhao/agentkit/runtime/platform/common"
	"github.com/lengzhao/agentkit/runtime/rctx"
)

type onceRuntime struct{ event agentkit.MessageEvent }

func (r onceRuntime) Start(ctx context.Context, submit capschedule.SubmitFunc) error {
	return submit(ctx, r.event)
}
func (onceRuntime) Stop(context.Context) error { return nil }

type failingPlatform struct{ calls int }

func (*failingPlatform) Receive(context.Context) (agentkit.MessageEvent, error) {
	return agentkit.MessageEvent{}, io.EOF
}
func (p *failingPlatform) Send(_ context.Context, event agentkit.OutboundEvent) error {
	if event.Type == agentkit.EventAssistantMessage {
		p.calls++
		return errors.New("network failed")
	}
	return nil
}
func TestAuditedRunSeparatesExecutionAndUnknownDelivery(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db := filepath.Join(dir, "state.db")
	auth := filepath.Join(dir, "access.json")
	if _, err := access.NewStore(auth).Update(func(s *access.State) error { s.OwnerOpenID = "owner"; return nil }); err != nil {
		t.Fatal(err)
	}
	recorder, err := NewRecords(RecordsConfig{DBPath: db, Installation: "install"})
	if err != nil {
		t.Fatal(err)
	}
	route := rctx.BuildSessionRoute(agentkit.SessionRouteInput{Platform: "weixin", Instance: "old-bot", ChannelID: "owner", DeliveryID: "weixin:owner"})
	event := common.InboundMessage("agent", "schedule:test", "weixin", "owner", "task")
	event.Envelope.Route = route
	event.Envelope.Metadata = map[string]any{"schedule": map[string]any{"fired": true, "jobId": "agent-1"}}
	engine, _ := NewEngine(EngineConfig{})
	registry, _ := NewStore(StoreConfig{Path: filepath.Join(dir, "jobs.json")}, StoreDeps{Engine: engine})
	if _, err := registry.Add(ctx, capschedule.Job{ID: "agent-1", Kind: "delay", Prompt: "task", FireAt: time.Now(), Route: route, Actor: agentkit.ActorRef{UserID: "owner"}, ChannelKey: "original-workspace"}); err != nil {
		t.Fatal(err)
	}
	rt, err := NewAuditedRuntime(AuditConfig{AccessPaths: map[string]string{"weixin": auth}, Instances: map[string]string{"weixin": "weixin"}}, AuditDeps{Inner: onceRuntime{event}, Records: recorder, Schedule: registry})
	if err != nil {
		t.Fatal(err)
	}
	transport := &failingPlatform{}
	outbound, err := NewAuditedPlatform(struct{}{}, PlatformDeps{Inner: transport, Records: recorder})
	if err != nil {
		t.Fatal(err)
	}
	err = rt.Start(ctx, func(ctx context.Context, in agentkit.MessageEvent) error {
		if in.Envelope.Workspace != "original-workspace" {
			t.Fatal("scheduled workspace was lost")
		}
		if in.Envelope.Route.Instance != "weixin" {
			t.Fatal("old instance not normalized")
		}
		turn, _ := in.Envelope.Metadata[agentkit.MetadataTurnID].(string)
		if turn == "" {
			t.Fatal("no stable run identity")
		}
		data, _ := json.Marshal(agentkit.ModelMessage{Role: "assistant", Content: []agentkit.ContentPart{{Type: "text", Text: "done"}}})
		_ = outbound.Send(ctx, agentkit.OutboundEvent{Route: in.Envelope.Route, TurnID: turn, Type: agentkit.EventTurnStart})
		_ = outbound.Send(ctx, agentkit.OutboundEvent{Route: in.Envelope.Route, TurnID: turn, Type: agentkit.EventAssistantMessage, Data: data})
		if err := outbound.Send(ctx, agentkit.OutboundEvent{Route: in.Envelope.Route, TurnID: turn, Type: agentkit.EventTurnEnd, Data: json.RawMessage(`{}`)}); err != nil {
			return err
		}
		return recorder.finish(ctx, turn, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	tasks, err := store.List(ctx, 10)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks %d %v", len(tasks), err)
	}
	if tasks[0].Status != state.StatusCompleted {
		t.Fatalf("execution %s", tasks[0].Status)
	}
	pending, err := store.PendingDeliveries(ctx)
	if err != nil || len(pending) != 1 || pending[0].Status != state.DeliveryUnknown {
		t.Fatalf("delivery %+v %v", pending, err)
	}
	if transport.calls != 1 {
		t.Fatal("failed send retried")
	}
}
