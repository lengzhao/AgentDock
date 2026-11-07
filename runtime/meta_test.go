package runtime_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lengzhao/AgentDock/runtime"
	"github.com/lengzhao/agentkit"
	"github.com/lengzhao/agentkit/runtime/platform/common"
	rtrunner "github.com/lengzhao/agentkit/runtime/runner"
	"github.com/lengzhao/pluginkit/build"
)

type metaPlatform struct {
	event     agentkit.MessageEvent
	delivered bool
}

func (p *metaPlatform) Receive(ctx context.Context) (agentkit.MessageEvent, error) {
	if !p.delivered {
		p.delivered = true
		return p.event, nil
	}
	<-ctx.Done()
	return agentkit.MessageEvent{}, ctx.Err()
}
func (*metaPlatform) Send(context.Context, agentkit.OutboundEvent) error { return nil }

type metaLoop struct{ events chan agentkit.MessageEvent }

func (l *metaLoop) Dispatch(_ context.Context, req agentkit.LoopRequest) error {
	l.events <- req.Event
	return nil
}
func (*metaLoop) Steer(context.Context, agentkit.ModelMessage) error    { return nil }
func (*metaLoop) FollowUp(context.Context, agentkit.ModelMessage) error { return nil }
func (*metaLoop) Cancel(context.Context, string) error                  { return nil }
func (*metaLoop) CancelAllInFlight(string)                              {}
func (*metaLoop) IsSessionBusy(agentkit.SessionID) bool                 { return false }
func (*metaLoop) TryDeliverPermission(agentkit.MessageEvent) bool       { return false }
func (*metaLoop) SupersedePendingForInbound(agentkit.MessageEvent)      {}

func runMetaMessage(t *testing.T, event agentkit.MessageEvent) string {
	t.Helper()
	graph, _, err := runtime.Graph(testConfig(), runtime.Layout{Home: "/tmp/meta", StateDir: "/tmp/meta/state"}, runtime.Secrets{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(graph["runner.default"].(runtime.Node)["config"])
	if err != nil {
		t.Fatal(err)
	}
	var cfg rtrunner.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	loop := &metaLoop{events: make(chan agentkit.MessageEvent, 1)}
	runner, err := rtrunner.New(cfg, rtrunner.Deps{Platform: &metaPlatform{event: event}, Loop: loop})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx, &build.Result{}) }()
	var delivered agentkit.MessageEvent
	select {
	case delivered = <-loop.events:
	case <-ctx.Done():
		t.Fatal("runner did not dispatch metadata message")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runner did not stop")
	}
	return common.ModelMessageText(delivered.Message)
}
func TestRunnerInjectsSupportedNonSecretContextIntoDeliveredMessage(t *testing.T) {
	event := common.InboundFromContent("claude", agentkit.SessionRouteInput{Platform: "lark", Instance: "lark", ChannelID: "chat1", ThreadID: "thread1"}, "owner", "hello", "", nil, nil, nil, nil, nil)
	event.Envelope.Metadata = map[string]any{
		"sender_name": "Alice", "sender_email": "alice@example.com",
		"mentions": []agentkit.ActorRef{{UserID: "coworker", Name: "Bob", Email: "bob@example.com"}},
		"task_id":  "task7", "trace_id": "trace8", "language": "zh-CN",
		"project": "AgentDock", "cwd": "/workspace/project", "workspace": "workspace1", "session_id": "session1", "agent_id": "claude",
		"chat_type": "group", "message_timestamp": "2030-01-02T03:04:05Z",
		"api_token": "secret-never-inject", "headers": map[string]string{"Authorization": "Bearer secret-never-inject"},
	}
	text := runMetaMessage(t, event)
	for _, want := range []string{
		"[meta ", "sender_id=owner", `sender_name="Alice"`, `sender_email="alice@example.com"`,
		`mentions="name=Bob id=coworker email=bob@example.com"`, "platform=lark", "chat_id=chat1", "thread_id=thread1",
		`timezone="Asia/Shanghai"`, `task_id="task7"`, `trace_id="trace8"`, `language="zh-CN"`,
		`project="AgentDock"`, `cwd="/workspace/project"`, `workspace="workspace1"`, `session_id="session1"`, `agent_id="claude"`,
		`chat_type="group"`, `message_timestamp="2030-01-02T03:04:05Z"`, "\nhello",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %s", want, text)
		}
	}
	if strings.Contains(text, "secret-never-inject") || strings.Contains(text, "Authorization") || strings.Contains(text, "api_token") {
		t.Fatal("non-allowlisted metadata leaked")
	}
}
func TestRunnerOmitsMissingIdentityProfileFields(t *testing.T) {
	event := common.InboundFromContent("claude", agentkit.SessionRouteInput{Platform: "weixin", Instance: "weixin", ChannelID: "wxowner"}, "wxowner", "hello", "", nil, nil, nil, nil, nil)
	event.Envelope.Metadata = map[string]any{"chat_type": "direct"}
	text := runMetaMessage(t, event)
	if !strings.Contains(text, "platform=weixin") || !strings.Contains(text, `chat_type="direct"`) {
		t.Fatalf("missing platform metadata: %s", text)
	}
	for _, missing := range []string{"sender_name=", "sender_email=", "mentions=", "thread_id=", "task_id=", "trace_id=", "language=", "message_timestamp="} {
		if strings.Contains(text, missing) {
			t.Fatalf("fabricated %s in %s", missing, text)
		}
	}
}
