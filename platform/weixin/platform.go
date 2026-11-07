package weixin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lengzhao/AgentDock/config"
	"github.com/lengzhao/agentkit"
	"github.com/lengzhao/agentkit/cap/ingress"
	"github.com/lengzhao/agentkit/cap/permission"
	"github.com/lengzhao/agentkit/runtime/platform/common"
	"github.com/lengzhao/agentkit/runtime/rctx"
	"github.com/lengzhao/pluginkit"
)

const Kind = "platform/weixin"

// ErrReplyContextUnavailable means no message can be sent until a new inbound message supplies context.
var ErrReplyContextUnavailable = errors.New("weixin reply context is unavailable; send a new private message first")

type Config struct {
	BotID        string `json:"botId"`
	Token        string `json:"token"`
	BaseURL      string `json:"baseUrl"`
	CursorPath   string `json:"cursorPath"`
	AllowFrom    string `json:"allowFrom"`
	SessionScope string `json:"sessionScope"`
}
type Deps struct {
	InboundPolicy ingress.Policy    `json:"inboundPolicy"`
	Commands      agentkit.Commands `json:"commands,omitempty"`
}

// Transport permits protocol simulation and alternate iLink client implementations.
type Transport interface {
	Updates(context.Context, string) (Updates, error)
	SendText(context.Context, string, string, string, string) error
}
type pendingPermission struct {
	user, conversation, turn string
	kind                     permission.Kind
	deadline                 time.Time
}

// Platform adapts iLink private text conversations to AgentKit. Receive is single
// consumer; Send and permission state are concurrency safe. No send is retried.
type Platform struct {
	cfg                Config
	deps               Deps
	api                Transport
	outbound           *common.Outbound
	queue              []Message
	cursor, nextCursor string
	mu                 sync.Mutex
	tokens             map[string]string
	pending            map[string]pendingPermission
	instance           string
	sessionExpired     bool
}

var _ agentkit.Platform = (*Platform)(nil)
var _ permission.Capable = (*Platform)(nil)
var _ agentkit.PlatformIdentifier = (*Platform)(nil)
var _ agentkit.PlatformInstanceSetter = (*Platform)(nil)

func init() { pluginkit.Register(Kind, New) }

func (*Platform) PlatformID() string { return "weixin" }

// SetPlatformInstance selects the aggregator-assigned routing identity. Account
// identity remains BotID for transport authentication, cursor and deduplication.
func (p *Platform) SetPlatformInstance(instance string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.instance = instance
}
func (p *Platform) platformInstance() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.instance != "" {
		return p.instance
	}
	return p.cfg.BotID
}
func New(cfg Config, deps Deps) (agentkit.Platform, error) {
	api, err := NewClient(cfg.BaseURL, cfg.Token, nil)
	if err != nil {
		return nil, err
	}
	return NewWithClient(cfg, deps, api)
}
func NewWithClient(cfg Config, deps Deps, api Transport) (*Platform, error) {
	if cfg.BotID == "" || cfg.Token == "" || deps.InboundPolicy == nil || api == nil {
		return nil, errors.New("weixin requires bot identity, token, transport and inbound policy")
	}
	p := &Platform{cfg: cfg, deps: deps, api: api, tokens: map[string]string{}, pending: map[string]pendingPermission{}}
	if cfg.CursorPath != "" {
		data, err := os.ReadFile(cfg.CursorPath)
		if err == nil {
			var state struct {
				BotID  string `json:"botId"`
				Cursor string `json:"cursor"`
			}
			if json.Unmarshal(data, &state) != nil {
				return nil, errors.New("weixin cursor state is invalid")
			}
			if state.BotID == cfg.BotID {
				p.cursor = state.Cursor
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("cannot read weixin cursor state")
		}
	}
	p.outbound = common.NewOutbound(p.sendBuffered, func(context.Context, agentkit.SessionID, agentkit.ContentPart) error {
		return errors.New("weixin currently supports text replies only")
	})
	return p, nil
}
func (*Platform) PermissionCapability() permission.Capability {
	return permission.Capability{Interactive: true, MultiSelect: false, DefaultTimeout: 10 * time.Minute, AnswerScope: permission.ScopeAsker}
}
func (p *Platform) checkpoint() error {
	if p.nextCursor == "" {
		return nil
	}
	if p.cfg.CursorPath != "" {
		body, _ := json.Marshal(map[string]string{"botId": p.cfg.BotID, "cursor": p.nextCursor})
		if err := config.WriteFileAtomic(p.cfg.CursorPath, body); err != nil {
			return errors.New("cannot save weixin cursor state")
		}
	}
	p.cursor = p.nextCursor
	p.nextCursor = ""
	return nil
}
func (p *Platform) Receive(ctx context.Context) (agentkit.MessageEvent, error) {
	delay := time.Second
	for {
		if err := ctx.Err(); err != nil {
			return agentkit.MessageEvent{}, err
		}

		if p.sessionExpired {
			<-ctx.Done()
			return agentkit.MessageEvent{}, ctx.Err()
		}
		if len(p.queue) == 0 {
			if err := p.checkpoint(); err != nil {
				return agentkit.MessageEvent{}, err
			}
			batch, err := p.api.Updates(ctx, p.cursor)
			if err != nil {
				if ctx.Err() != nil {
					return agentkit.MessageEvent{}, ctx.Err()
				}
				if errors.Is(err, ErrSessionExpired) {
					// A paused Weixin account must not bring down other multiplexed
					// platforms. Re-login and restart the service to resume this account.
					p.sessionExpired = true
					slog.Error("weixin session expired; channel paused; log in again with agentdock setup --platform weixin and restart the service")
					continue
				}
				if err := wait(ctx, delay); err != nil {
					return agentkit.MessageEvent{}, err
				}
				if delay < 30*time.Second {
					delay *= 2
					if delay > 30*time.Second {
						delay = 30 * time.Second
					}
				}
				continue
			}
			delay = time.Second
			p.queue = batch.Messages
			p.nextCursor = batch.Cursor
			if len(p.queue) == 0 {
				if err := wait(ctx, 200*time.Millisecond); err != nil {
					return agentkit.MessageEvent{}, err
				}
				continue
			}
		}
		msg := p.queue[0]
		if msg.Type != 1 || msg.State == 1 || msg.GroupID != "" || msg.FromUserID == "" || msg.ID == "" || msg.ContextToken == "" || (msg.ToUserID != "" && msg.ToUserID != p.cfg.BotID) {
			p.queue = p.queue[1:]
			continue
		}
		if p.cfg.AllowFrom != "" && !common.AllowList(p.cfg.AllowFrom, msg.FromUserID) {
			p.queue = p.queue[1:]
			continue
		}
		parts := []string{}
		for _, item := range msg.Items {
			if item.Type == 1 && item.TextItem != nil {
				parts = append(parts, item.TextItem.Text)
			}
		}
		text := strings.TrimSpace(strings.Join(parts, "\n"))
		if text == "" {
			p.queue = p.queue[1:]
			continue
		}
		raw, _ := json.Marshal(msg)
		delivery := rctx.BuildDeliverySessionID("weixin", msg.FromUserID, "", "")
		in := ingress.Input{Platform: "weixin", InstanceID: p.cfg.BotID, Kind: ingress.Message, ID: msg.ID.String(), ChatID: msg.FromUserID, UserID: msg.FromUserID, ConversationKind: "direct", Raw: raw, Message: &ingress.MessageFacts{HasText: true, AddressedToAgent: true}, Default: ingress.Plan{Agent: ingress.Submit, Handlers: true}}
		command, requestID, answer := permissionCommand(text)
		if command != "" {
			in.Kind = ingress.Interaction
			in.Default.Agent = ingress.Skip
		}
		decision, err := p.deps.InboundPolicy.Evaluate(ctx, in)
		if err != nil {
			return agentkit.MessageEvent{}, err
		}
		plan, err := ingress.Resolve(in, decision)
		if err != nil {
			return agentkit.MessageEvent{}, err
		}
		// A failed admission leaves the message at the head for the next Receive.
		// Once admitted/rejected, consuming it is independent from outbound delivery.
		p.queue = p.queue[1:]
		if plan.Rejected {
			continue
		}
		p.mu.Lock()
		p.tokens[msg.FromUserID] = msg.ContextToken
		p.mu.Unlock()
		route := agentkit.SessionRouteInput{Platform: "weixin", Instance: p.platformInstance(), DeliveryID: delivery, ChannelID: msg.FromUserID}
		event := common.InboundFromContent("", route, msg.FromUserID, text, "", nil, nil, nil, nil, nil)
		metadata := map[string]any{
			"sender_id": msg.FromUserID, "chat_id": msg.FromUserID,
			"sessionScope": p.cfg.SessionScope, "chat_type": "direct",
		}
		if msg.CreateTimeMS > 0 {
			metadata["message_timestamp"] = time.UnixMilli(msg.CreateTimeMS).UTC().Format(time.RFC3339Nano)
		}
		event = common.WithMetadata(event, metadata)
		if command != "" {
			if !plan.Handlers {
				continue
			}
			pending, ok := p.consumePermission(requestID, msg.FromUserID, command)
			if !ok {
				if err := p.sendText(ctx, msg.FromUserID, "Approval request is invalid, expired, or already answered."); err != nil {
					return agentkit.MessageEvent{}, err
				}
				continue
			}
			reply := permission.Reply{RequestID: requestID, UserID: msg.FromUserID}
			if command == "answer" {
				reply.Text = answer
			} else if command == "approve" {
				reply.Decision = "allow"
			} else {
				reply.Decision = "deny"
			}
			event.Reply, _ = json.Marshal(reply)
			event.Message = agentkit.ModelMessage{}
			event.Envelope.Conversation = pending.conversation
			return event, nil
		}
		if plan.Handlers && p.deps.Commands != nil {
			outcome, err := common.ProcessSlash(ctx, p.deps.Commands, common.SlashContext{Route: event.Envelope.Route, SessionScope: rctx.ParseScope(p.cfg.SessionScope), UserID: msg.FromUserID}, text)
			if err != nil {
				return agentkit.MessageEvent{}, err
			}
			if outcome.Reply != "" {
				if err := p.sendText(ctx, msg.FromUserID, outcome.Reply); err != nil {
					return agentkit.MessageEvent{}, err
				}
			}
			if outcome.Kind == common.SlashHandled {
				continue
			}
		}
		if plan.Agent == ingress.Skip || plan.Agent == ingress.Defer {
			continue
		}
		return event, nil
	}
}
func permissionCommand(text string) (command, id, answer string) {
	words := strings.Fields(text)
	if len(words) < 2 {
		return "", "", ""
	}
	switch words[0] {
	case "/approve":
		command = "approve"
	case "/deny":
		command = "deny"
	case "/answer":
		command = "answer"
	default:
		return "", "", ""
	}
	if command != "answer" && len(words) != 2 {
		return "", "", ""
	}
	if command == "answer" && len(words) < 3 {
		return "", "", ""
	}
	return command, words[1], strings.Join(words[2:], " ")
}
func (p *Platform) consumePermission(id, user, command string) (pendingPermission, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	req, ok := p.pending[id]
	if !ok || req.user != user || time.Now().After(req.deadline) {
		return pendingPermission{}, false
	}
	if (req.kind == permission.KindAllowDeny && command == "answer") || (req.kind != permission.KindAllowDeny && command != "answer") {
		return pendingPermission{}, false
	}
	delete(p.pending, id)
	return req, true
}
func (p *Platform) sendText(ctx context.Context, user, text string) error {
	if text == "" {
		return nil
	}
	p.mu.Lock()
	token := p.tokens[user]
	p.mu.Unlock()
	if token == "" {
		return ErrReplyContextUnavailable
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	return p.api.SendText(ctx, user, token, hex.EncodeToString(id[:]), text)
}
func (p *Platform) sendBuffered(ctx context.Context, delivery agentkit.SessionID, text string) error {
	parts := rctx.ParseDelivery(delivery, "")
	if !parts.Routable || parts.Platform != "weixin" || parts.Channel == "" {
		return errors.New("invalid weixin delivery route")
	}
	return p.sendText(ctx, parts.Channel, text)
}
func (p *Platform) Send(ctx context.Context, event agentkit.OutboundEvent) error {
	if event.Route.Platform != "weixin" || event.Route.Instance != p.platformInstance() {
		return errors.New("outbound route does not match weixin account")
	}
	target, ok := rctx.DecodeSessionRoute(event.Route)
	if !ok || target.ChannelID == "" {
		return errors.New("invalid weixin destination")
	}
	switch event.Type {
	case agentkit.EventPermissionRequest:
		var req permission.RequestPayload
		if err := json.Unmarshal(event.Data, &req); err != nil {
			return errors.New("invalid permission request")
		}
		if req.ID == "" || req.Conversation == "" {
			return errors.New("permission request requires identity and conversation")
		}
		if req.Kind != permission.KindAllowDeny && req.Kind != permission.KindQuestion {
			return errors.New("weixin permission kind is not supported")
		}
		if req.Question != nil && (req.Question.MultiSelect || len(req.Question.Options) > 0) {
			return errors.New("weixin does not support choice-based questions yet")
		}
		ttl := permission.EffectiveTimeout(req.Request, p.PermissionCapability())
		p.mu.Lock()
		for id, entry := range p.pending {
			if time.Now().After(entry.deadline) {
				delete(p.pending, id)
			}
		}
		p.pending[req.ID] = pendingPermission{user: target.ChannelID, conversation: req.Conversation, turn: event.TurnID, kind: req.Kind, deadline: time.Now().Add(ttl)}
		p.mu.Unlock()
		prompt := common.FormatPermissionRequest(req)
		if req.Kind == permission.KindAllowDeny {
			prompt += fmt.Sprintf("\n/approve %s\n/deny %s", req.ID, req.ID)
		} else {
			prompt += fmt.Sprintf("\n/answer %s <text>", req.ID)
		}
		return p.sendText(ctx, target.ChannelID, prompt)
	case agentkit.EventPermissionResolved:
		var resolved struct {
			RequestID string `json:"requestId"`
			ID        string `json:"id"`
		}
		_ = json.Unmarshal(event.Data, &resolved)
		p.mu.Lock()
		delete(p.pending, resolved.RequestID)
		delete(p.pending, resolved.ID)
		p.mu.Unlock()
		return nil
	case agentkit.EventTurnEnd:
		p.mu.Lock()
		for id, req := range p.pending {
			if req.user == target.ChannelID && (req.turn == event.TurnID || event.TurnID == "") {
				delete(p.pending, id)
			}
		}
		p.mu.Unlock()
	}
	return p.outbound.Handle(ctx, event)
}
