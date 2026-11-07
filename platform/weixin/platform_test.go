package weixin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lengzhao/agentkit"
	"github.com/lengzhao/agentkit/cap/ingress"
	"github.com/lengzhao/agentkit/cap/permission"
	"github.com/lengzhao/agentkit/runtime/platform/common"
	"github.com/lengzhao/agentkit/runtime/rctx"
)

type mockTransport struct {
	mu      sync.Mutex
	batches []Updates
	sent    []string
	tokens  []string
	cursors []string
}

func (m *mockTransport) Updates(ctx context.Context, cursor string) (Updates, error) {
	m.mu.Lock()
	m.cursors = append(m.cursors, cursor)
	if len(m.batches) > 0 {
		v := m.batches[0]
		m.batches = m.batches[1:]
		m.mu.Unlock()
		return v, nil
	}
	m.mu.Unlock()
	<-ctx.Done()
	return Updates{}, ctx.Err()
}
func (m *mockTransport) SendText(_ context.Context, to, token, id, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, to+":"+text)
	m.tokens = append(m.tokens, token)
	return nil
}

type ownerPolicy struct{}

func (ownerPolicy) Evaluate(_ context.Context, in ingress.Input) (ingress.Decision, error) {
	return ingress.Decision{Reject: in.UserID != "owner"}, nil
}
func incoming(id, user, text string) Message {
	return Message{ID: json.Number(id), FromUserID: user, ToUserID: "bot", Type: 1, State: 2, ContextToken: "context-secret", Items: []Item{{Type: 1, TextItem: &TextItem{Text: text}}}}
}
func newTestPlatform(t *testing.T, m *mockTransport) *Platform {
	t.Helper()
	p, err := NewWithClient(Config{BotID: "bot", Token: "token", CursorPath: filepath.Join(t.TempDir(), "cursor.json")}, Deps{InboundPolicy: ownerPolicy{}}, m)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestPlatformReceivesOnlyAdmittedPrivateTextAndReplies(t *testing.T) {
	m := &mockTransport{batches: []Updates{{Messages: []Message{incoming("1", "stranger", "ignore"), incoming("2", "owner", "hello")}, Cursor: "next"}}}
	p := newTestPlatform(t, m)
	event, err := p.Receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if common.ModelMessageText(event.Message) != "hello" || event.Envelope.Route.Instance != "bot" {
		t.Fatalf("wrong envelope: %+v", event.Envelope)
	}
	reply := agentkit.ModelMessage{Role: "assistant", Content: []agentkit.ContentPart{{Type: "text", Text: "world"}}}
	data, _ := json.Marshal(reply)
	if err := p.Send(context.Background(), agentkit.OutboundEvent{Route: event.Envelope.Route, Type: agentkit.EventAssistantMessage, Data: data}); err != nil {
		t.Fatal(err)
	}
	if len(m.sent) != 1 || m.sent[0] != "owner:world" || m.tokens[0] != "context-secret" {
		t.Fatalf("sent %v", m.sent)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := p.Receive(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel: %v", err)
	}
	if m.cursors[len(m.cursors)-1] != "next" {
		t.Fatal("cursor not advanced after consumed batch")
	}
}
func TestPlatformRejectsMissingPolicyAndForeignDestination(t *testing.T) {
	if _, err := NewWithClient(Config{BotID: "bot", Token: "token"}, Deps{}, &mockTransport{}); err == nil {
		t.Fatal("missing policy accepted")
	}
	p := newTestPlatform(t, &mockTransport{})
	route := rctx.BuildSessionRoute(agentkit.SessionRouteInput{Platform: "weixin", Instance: "other", ChannelID: "owner", DeliveryID: "weixin:owner"})
	if err := p.Send(context.Background(), agentkit.OutboundEvent{Route: route, Type: agentkit.EventAssistantMessage}); err == nil {
		t.Fatal("foreign instance accepted")
	}
}
func TestPlatformPermissionReplyIsBoundAndSingleUse(t *testing.T) {
	m := &mockTransport{batches: []Updates{{Messages: []Message{incoming("1", "owner", "hello")}}}}
	p := newTestPlatform(t, m)
	first, err := p.Receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	req := permission.RequestPayload{Request: permission.Request{ID: "req1", Kind: permission.KindAllowDeny, Reason: "run command"}, Conversation: "conversation1"}
	data, _ := json.Marshal(req)
	if err := p.Send(context.Background(), agentkit.OutboundEvent{Route: first.Envelope.Route, Type: agentkit.EventPermissionRequest, Data: data, TurnID: "turn1"}); err != nil {
		t.Fatal(err)
	}
	m.batches = []Updates{{Messages: []Message{incoming("2", "stranger", "/approve req1"), incoming("3", "owner", "/approve req1")}}}
	event, err := p.Receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var answer permission.Reply
	if err := json.Unmarshal(event.Reply, &answer); err != nil {
		t.Fatal(err)
	}
	if answer.RequestID != "req1" || answer.UserID != "owner" || answer.Decision != "allow" || event.Envelope.Conversation != "conversation1" {
		t.Fatalf("reply %+v", answer)
	}
}

func TestPlatformCursorCommitsAfterBatchAndSurvivesRestart(t *testing.T) {
	m := &mockTransport{batches: []Updates{{Messages: []Message{incoming("1", "owner", "one"), incoming("2", "owner", "two")}, Cursor: "saved"}}}
	p := newTestPlatform(t, m)
	if _, err := p.Receive(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.cfg.CursorPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cursor saved before batch consumed")
	}
	if _, err := p.Receive(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, _ = p.Receive(ctx)
	raw, err := os.ReadFile(p.cfg.CursorPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "context-secret") || strings.Contains(string(raw), "token") {
		t.Fatal("cursor contains credentials")
	}
	next, err := NewWithClient(p.cfg, p.deps, &mockTransport{})
	if err != nil {
		t.Fatal(err)
	}
	if next.cursor != "saved" {
		t.Fatal("cursor not restored")
	}
	st, _ := os.Stat(p.cfg.CursorPath)
	if st.Mode().Perm() != 0600 {
		t.Fatal("cursor must be private")
	}
}

type flakyPolicy struct{ calls int }

func (p *flakyPolicy) Evaluate(context.Context, ingress.Input) (ingress.Decision, error) {
	p.calls++
	if p.calls == 1 {
		return ingress.Decision{}, errors.New("temporary policy error")
	}
	return ingress.Decision{}, nil
}
func TestPlatformRetainsMessageWhenAdmissionFails(t *testing.T) {
	m := &mockTransport{batches: []Updates{{Messages: []Message{incoming("1", "owner", "one")}, Cursor: "next"}}}
	p := newTestPlatform(t, m)
	p.deps.InboundPolicy = &flakyPolicy{}
	if _, err := p.Receive(context.Background()); err == nil {
		t.Fatal("expected policy error")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	event, err := p.Receive(ctx)
	if err != nil || common.ModelMessageText(event.Message) != "one" {
		t.Fatalf("message lost: %v", err)
	}
}

type handlersDisabled struct{}

func (handlersDisabled) Evaluate(context.Context, ingress.Input) (ingress.Decision, error) {
	return ingress.Decision{Handlers: ingress.Disable}, nil
}
func TestPlatformDisabledHandlersCannotApprove(t *testing.T) {
	m := &mockTransport{batches: []Updates{{Messages: []Message{incoming("1", "owner", "hello")}}}}
	p := newTestPlatform(t, m)
	first, err := p.Receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	req := permission.RequestPayload{Request: permission.Request{ID: "req-disabled", Kind: permission.KindAllowDeny}, Conversation: "conversation"}
	data, _ := json.Marshal(req)
	if err := p.Send(context.Background(), agentkit.OutboundEvent{Route: first.Envelope.Route, Type: agentkit.EventPermissionRequest, Data: data}); err != nil {
		t.Fatal(err)
	}
	p.deps.InboundPolicy = handlersDisabled{}
	m.batches = []Updates{{Messages: []Message{incoming("2", "owner", "/approve req-disabled"), incoming("3", "owner", "next")}}}
	event, err := p.Receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(event.Reply) > 0 || common.ModelMessageText(event.Message) != "next" {
		t.Fatal("disabled handlers processed approval")
	}
	if _, ok := p.pending["req-disabled"]; !ok {
		t.Fatal("disabled handler consumed approval")
	}
}

func TestPlatformMultiplexIdentityKeepsAccountDedupeScope(t *testing.T) {
	m := &mockTransport{batches: []Updates{{Messages: []Message{incoming("1", "owner", "hello")}}}}
	p := newTestPlatform(t, m)
	if p.PlatformID() != "weixin" {
		t.Fatal("wrong platform identifier")
	}
	p.SetPlatformInstance("weixin")
	capture := &captureInstancePolicy{}
	p.deps.InboundPolicy = capture
	event, err := p.Receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if capture.instance != "bot" {
		t.Fatalf("deduplication account identity changed: %s", capture.instance)
	}
	if event.Envelope.Route.Instance != "weixin" {
		t.Fatalf("instance: %s", event.Envelope.Route.Instance)
	}
	reply := agentkit.ModelMessage{Role: "assistant", Content: []agentkit.ContentPart{{Type: "text", Text: "reply"}}}
	data, _ := json.Marshal(reply)
	if err := p.Send(context.Background(), agentkit.OutboundEvent{Route: event.Envelope.Route, Type: agentkit.EventAssistantMessage, Data: data}); err != nil {
		t.Fatal(err)
	}
	foreign := event.Envelope.Route
	foreign.Instance = "bot"
	if err := p.Send(context.Background(), agentkit.OutboundEvent{Route: foreign, Type: agentkit.EventAssistantMessage, Data: data}); err == nil {
		t.Fatal("old standalone identity accepted after multiplex binding")
	}
}

type captureInstancePolicy struct{ instance string }

func (c *captureInstancePolicy) Evaluate(_ context.Context, in ingress.Input) (ingress.Decision, error) {
	c.instance = in.InstanceID
	return ingress.Decision{}, nil
}

type expiredTransport struct{ calls int }

func (e *expiredTransport) Updates(context.Context, string) (Updates, error) {
	e.calls++
	return Updates{}, ErrSessionExpired
}
func (*expiredTransport) SendText(context.Context, string, string, string, string) error { return nil }
func TestExpiredSessionPausesOnlyWeixinUntilShutdown(t *testing.T) {
	api := &expiredTransport{}
	p, err := NewWithClient(Config{BotID: "bot", Token: "token"}, Deps{InboundPolicy: ownerPolicy{}}, api)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		_, err := p.Receive(ctx)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expired channel stopped multiplex runner: %v", err)
		}
	}
	if api.calls != 1 {
		t.Fatalf("expired channel kept polling: %d", api.calls)
	}
}
