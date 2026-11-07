package runtime_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lengzhao/AgentDock/access"
	"github.com/lengzhao/AgentDock/config"
	"github.com/lengzhao/AgentDock/credentials"
	"github.com/lengzhao/AgentDock/runtime"
	"github.com/lengzhao/AgentDock/state"
	"github.com/lengzhao/agentkit"
	"github.com/lengzhao/agentkit/runtime/platform/common"
	"github.com/lengzhao/pluginkit"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// A real subprocess talks ACP to the host and uses the advertised HTTP MCP
// endpoint. This catches transport/context and actual ACP event-shape regressions.
func TestTimerACPHelper(t *testing.T) {
	helper := false
	for _, arg := range os.Args {
		if arg == "agentdock-timer-helper" {
			helper = true
		}
	}
	if !helper {
		return
	}
	mode := os.Args[len(os.Args)-1]
	authenticated := false
	decoder := json.NewDecoder(bufio.NewReader(os.Stdin))
	encoder := json.NewEncoder(os.Stdout)
	var servers []struct {
		Type    string `json:"type"`
		URL     string `json:"url"`
		Headers []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"headers"`
	}
	for {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := decoder.Decode(&request); err != nil {
			os.Exit(0)
		}
		if len(request.ID) == 0 {
			continue
		}
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		switch request.Method {
		case "initialize":
			methods := []any{}
			if mode == "cursor" {
				methods = []any{map[string]any{"id": "cursor_login", "name": "Cursor Login", "description": "Fake test auth"}}
			}
			response["result"] = map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{"mcpCapabilities": map[string]any{"http": true}}, "authMethods": methods}
		case "authenticate":
			var params struct {
				Method string `json:"methodId"`
			}
			_ = json.Unmarshal(request.Params, &params)
			if params.Method != "cursor_login" {
				response["error"] = map[string]any{"code": -32602, "message": "wrong cursor auth method"}
			} else {
				authenticated = true
				response["result"] = map[string]any{}
			}
		case "session/new":
			if mode == "cursor" && !authenticated {
				response["error"] = map[string]any{"code": -32000, "message": "cursor_login required"}
				break
			}
			var params struct {
				Servers json.RawMessage `json:"mcpServers"`
			}
			_ = json.Unmarshal(request.Params, &params)
			_ = json.Unmarshal(params.Servers, &servers)
			response["result"] = map[string]any{"sessionId": "fake-timer-session"}
		case "session/prompt":
			scheduled := strings.Contains(string(request.Params), "[schedule kind=")
			if scheduled && mode == "fail" {
				response["error"] = map[string]any{"code": -32603, "message": "simulated scheduled ACP execution failed"}
				break
			}
			text := "timer result"
			if !scheduled {
				if len(servers) == 0 {
					response["error"] = map[string]any{"code": -32603, "message": "no MCP servers were injected"}
					break
				}
				headers := map[string]string{}
				for _, h := range servers[0].Headers {
					headers[h.Name] = h.Value
				}
				c, err := client.NewStreamableHttpClient(servers[0].URL, transport.WithHTTPHeaders(headers))
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				if err == nil {
					err = c.Start(ctx)
				}
				if err == nil {
					init := mcp.InitializeRequest{}
					init.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
					init.Params.ClientInfo = mcp.Implementation{Name: "fake-acp", Version: "1"}
					_, err = c.Initialize(ctx, init)
				}
				if err == nil {
					call := mcp.CallToolRequest{}
					call.Params.Name = "schedule"
					call.Params.Arguments = map[string]any{"op": "add", "kind": "delay", "in": "100ms", "prompt": "Return timer result"}
					var result *mcp.CallToolResult
					result, err = c.CallTool(ctx, call)
					if err == nil && result.IsError {
						err = fmt.Errorf("schedule tool refused request: %v", result.Content)
					}
				}
				if c != nil {
					_ = c.Close()
				}
				cancel()
				if err != nil {
					response["error"] = map[string]any{"code": -32603, "message": err.Error()}
					break
				}
				text = "scheduled"
			}
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "fake-timer-session", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text}}}})
			response["result"] = map[string]any{"stopReason": "end_turn"}
		default:
			response["result"] = map[string]any{}
		}
		if err := encoder.Encode(response); err != nil {
			os.Exit(2)
		}
	}
}

type timerPlatform struct {
	once sync.Once
	out  *common.Outbound
	sent chan string
}

func (p *timerPlatform) Receive(ctx context.Context) (agentkit.MessageEvent, error) {
	first := false
	p.once.Do(func() { first = true })
	if !first {
		<-ctx.Done()
		return agentkit.MessageEvent{}, ctx.Err()
	}
	return common.InboundFromContent("", agentkit.SessionRouteInput{Platform: "lark", Instance: "lark", ChannelID: "owner", DeliveryID: "lark:owner"}, "owner", "Create a timer", "", nil, nil, nil, nil, nil), nil
}
func (p *timerPlatform) Send(ctx context.Context, event agentkit.OutboundEvent) error {
	return p.out.Handle(ctx, event)
}
func TestScheduledACPToolFireAndExecutionAudit(t *testing.T) {
	for _, mode := range []string{"success", "fail", "delivery-fail", "cursor"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			p := &timerPlatform{sent: make(chan string, 8)}
			var finalAttempts atomic.Int32
			p.out = common.NewOutbound(func(ctx context.Context, _ agentkit.SessionID, text string) error {
				if text == "timer result" {
					finalAttempts.Add(1)
					if mode == "delivery-fail" {
						return errors.New("simulated unknown platform send failure")
					}
				}
				select {
				case p.sent <- text:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}, nil)
			kind := "test/timer-platform-" + mode
			pluginkit.Register(kind, func() (agentkit.Platform, error) { return p, nil })
			cfg := testConfig()
			cfg.Agents = []config.Agent{{ID: "fake", Command: []string{os.Args[0], "-test.run=^TestTimerACPHelper$", "--", "agentdock-timer-helper", mode}}}
			cfg.Runtime.DefaultAgent = "fake"
			if mode == "cursor" {
				cfg.Agents[0].ID = "cursor"
				cfg.Agents[0].AuthMethod = "cursor_login"
				cfg.Runtime.DefaultAgent = "cursor"
			}
			layout := runtime.Layout{Home: t.TempDir(), InstanceDir: t.TempDir(), StateDir: t.TempDir()}
			if _, err := access.NewStore(filepath.Join(layout.StateDir, "access.json")).Update(func(st *access.State) error { st.OwnerOpenID = "owner"; return nil }); err != nil {
				t.Fatal(err)
			}
			resolver := credentials.NewResolver(credentials.NewMemoryStore(), func(string) (string, bool) { return "value", true })
			cfg.Platform.AppIDRef = "env:APP_ID"
			cfg.Platform.AppSecretRef = "env:APP_SECRET"
			runner, result, err := runtime.BuildWithOverrides(ctx, cfg, layout, resolver, map[string]runtime.Node{"platform.main": {"use": kind}})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- runner.Run(ctx, result) }()
			t.Cleanup(func() {
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("runner did not stop")
				}
				_ = runner.Stop(context.Background())
			})
			store, err := state.Open(filepath.Join(layout.StateDir, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			wanted := state.StatusCompleted
			if mode == "fail" {
				wanted = state.StatusFailed
			}
			var observed []state.Task
			for {
				observed, err = store.List(ctx, 10)
				if err != nil {
					t.Fatal(err)
				}
				if len(observed) == 1 && mode == "delivery-fail" && observed[0].Status == state.StatusFailed {
					t.Fatal("delivery failure was misclassified as execution failure")
				}
				if len(observed) == 1 && wanted == state.StatusFailed && observed[0].Status == state.StatusCompleted {
					t.Fatal("failed ACP execution was reported completed")
				}
				if len(observed) == 1 && observed[0].Status == wanted {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatalf("timer did not finish: %v tasks=%+v", ctx.Err(), observed)
				case err := <-done:
					done <- err
					t.Fatalf("runner exited before timer: %v", err)
				case <-time.After(20 * time.Millisecond):
				}
			}
			pending, err := store.PendingDeliveries(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "success" || mode == "cursor" {
				deliveries, err := store.Deliveries(ctx, observed[0].Key)
				if err != nil || len(deliveries) != 1 || deliveries[0].Status != state.DeliverySent {
					t.Fatalf("successful ACP delivery misrecorded: %+v %v", deliveries, err)
				}
				if len(pending) != 0 {
					t.Fatalf("successful ACP text misrecorded: %+v", pending)
				}
				saw := false
				for len(p.sent) > 0 {
					if <-p.sent == "timer result" {
						saw = true
					}
				}
				if !saw {
					t.Fatal("timer result never reached original platform")
				}
			}
			if mode == "delivery-fail" {
				deliveries, err := store.Deliveries(ctx, observed[0].Key)
				if err != nil || len(deliveries) != 1 || deliveries[0].Status != state.DeliveryUnknown || deliveries[0].Attempts != 1 || finalAttempts.Load() != 1 {
					t.Fatalf("failed delivery retried or lost: %+v %v attempts=%d", deliveries, err, finalAttempts.Load())
				}
			}
			if mode == "fail" && observed[0].Status == state.StatusCompleted {
				t.Fatal("failed ACP execution reported completed")
			}
			jobs, err := os.ReadFile(filepath.Join(layout.StateDir, "schedules.json"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(jobs), `"fired": true`) {
				t.Fatal("one shot not claimed durably")
			}
		})
	}
}
