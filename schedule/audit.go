package schedule

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/lengzhao/AgentDock/access"
	"github.com/lengzhao/AgentDock/platform/weixin"
	"github.com/lengzhao/AgentDock/state"
	"github.com/lengzhao/agentkit"
	"github.com/lengzhao/agentkit/cap/chathistory"
	"github.com/lengzhao/agentkit/cap/delivery"
	"github.com/lengzhao/agentkit/cap/permission"
	capschedule "github.com/lengzhao/agentkit/cap/schedule"
	capsession "github.com/lengzhao/agentkit/cap/session"
	"github.com/lengzhao/agentkit/runtime/rctx"
	"github.com/lengzhao/pluginkit"
)

const RecordsKind = "agentdock/schedule-records"
const RuntimeKind = "agentdock/schedule-runtime"
const PlatformKind = "agentdock/schedule-delivery"

type RecordsConfig struct {
	DBPath       string `json:"dbPath"`
	Installation string `json:"installation"`
}
type runRecord struct {
	mu             sync.Mutex
	key            string
	deliveryID     int64
	deliveryFailed bool
	hasOutput      bool
}

// Records observes scheduled turns only. It never retains response text or retries sends.
type Records struct {
	cfg  RecordsConfig
	mu   sync.Mutex
	runs map[string]*runRecord
}

func NewRecords(cfg RecordsConfig) (*Records, error) {
	if !filepath.IsAbs(cfg.DBPath) || cfg.Installation == "" {
		return nil, errors.New("schedule records require absolute DB path and installation")
	}
	return &Records{cfg: cfg, runs: map[string]*runRecord{}}, nil
}
func init() {
	pluginkit.Register(RecordsKind, NewRecords)
	pluginkit.Register(RuntimeKind, NewAuditedRuntime)
	pluginkit.Register(PlatformKind, NewAuditedPlatform)
}
func (r *Records) InitApp(ctx context.Context) error {
	store, err := state.Open(r.cfg.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()
	pending, err := store.PendingDeliveries(ctx)
	if err != nil {
		return err
	}
	for _, d := range pending {
		if d.Status == state.DeliveryPending {
			if err := store.UpdateDelivery(ctx, d.ID, state.DeliveryUnknown, "interrupted before delivery confirmation"); err != nil {
				return err
			}
		}
	}
	return nil
}
func (r *Records) begin(ctx context.Context, event agentkit.MessageEvent, job string) (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	turn := "schedule-" + hex.EncodeToString(id[:])
	key := "schedule:" + job + ":" + turn
	target, _ := rctx.DecodeSessionRoute(event.Envelope.Route)
	store, err := state.Open(r.cfg.DBPath)
	if err != nil {
		return "", err
	}
	defer store.Close()
	if err := store.Accept(ctx, state.Task{Key: key, Installation: r.cfg.Installation, Platform: event.Envelope.Route.Platform, ChatID: target.ChannelID, ThreadID: target.ThreadID, UserID: event.Envelope.Actor.UserID, SessionID: event.Envelope.Conversation, Status: state.StatusAccepted, Detail: "scheduled job " + job}); err != nil {
		return "", err
	}
	r.mu.Lock()
	r.runs[turn] = &runRecord{key: key}
	r.mu.Unlock()
	return turn, nil
}
func (r *Records) fail(ctx context.Context, turn, detail string) error {
	r.mu.Lock()
	run := r.runs[turn]
	delete(r.runs, turn)
	r.mu.Unlock()
	if run == nil {
		return nil
	}
	store, err := state.Open(r.cfg.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()
	return store.SetStatus(ctx, run.key, state.StatusFailed, detail)
}

type AuditConfig struct {
	AccessPaths map[string]string `json:"accessPaths"`
	Instances   map[string]string `json:"instances"`
}
type AuditDeps struct {
	Schedule capschedule.Registry `json:"schedule"`
	Inner    capschedule.Runtime  `json:"inner"`
	Records  *Records             `json:"records"`
}
type auditedRuntime struct {
	cfg  AuditConfig
	deps AuditDeps
}

func NewAuditedRuntime(cfg AuditConfig, deps AuditDeps) (capschedule.Runtime, error) {
	if deps.Inner == nil || deps.Records == nil || deps.Schedule == nil {
		return nil, errors.New("schedule runtime requires source and records")
	}
	return &auditedRuntime{cfg: cfg, deps: deps}, nil
}
func (r *auditedRuntime) Start(ctx context.Context, submit capschedule.SubmitFunc) error {
	return r.deps.Inner.Start(ctx, func(ctx context.Context, event agentkit.MessageEvent) error {
		meta, ok := capschedule.FireMeta(event.Envelope.Metadata)
		if !ok {
			return errors.New("scheduled inbound is missing fire metadata")
		}
		job, _ := meta["jobId"].(string)
		jobs, err := r.deps.Schedule.List(ctx)
		if err != nil {
			return err
		}
		found := false
		for _, stored := range jobs {
			if stored.ID == job {
				if stored.Actor.UserID != event.Envelope.Actor.UserID || stored.Route.Platform != event.Envelope.Route.Platform {
					return errors.New("scheduled identity does not match persisted job")
				}
				event.Envelope.Workspace = stored.ChannelKey
				found = true
				break
			}
		}
		if !found {
			return errors.New("scheduled task was removed before submission")
		}

		instance, valid := r.cfg.Instances[event.Envelope.Route.Platform]
		if valid {
			event.Envelope.Route.Instance = instance
		}
		turn, err := r.deps.Records.begin(ctx, event, job)
		if err != nil {
			return err
		}
		path := r.cfg.AccessPaths[event.Envelope.Route.Platform]
		if !valid || path == "" || event.Envelope.Actor.UserID == "" {
			_ = r.deps.Records.fail(ctx, turn, "schedule authorization or destination unavailable")
			return errors.New("scheduled task has no authorized destination")
		}
		allowed, err := access.NewStore(path).Resolve(event.Envelope.Actor.UserID, nil)
		if err != nil || !allowed.Allow {
			_ = r.deps.Records.fail(ctx, turn, "schedule authorization revoked")
			return errors.New("scheduled task authorization revoked")
		}
		if event.Envelope.Metadata == nil {
			event.Envelope.Metadata = map[string]any{}
		}
		event.Envelope.Metadata[agentkit.MetadataTurnID] = turn
		event.Envelope.Metadata[agentkit.MetadataQueueTurn] = true
		if err := submit(ctx, event); err != nil {
			_ = r.deps.Records.fail(ctx, turn, "scheduled task submission failed")
			return err
		}
		return nil
	})
}
func (r *auditedRuntime) Stop(ctx context.Context) error { return r.deps.Inner.Stop(ctx) }

type PlatformDeps struct {
	Inner   agentkit.Platform `json:"inner"`
	Records *Records          `json:"records"`
}
type auditedPlatform struct {
	inner   agentkit.Platform
	records *Records
}

func NewAuditedPlatform(_ struct{}, deps PlatformDeps) (agentkit.Platform, error) {
	if deps.Inner == nil || deps.Records == nil {
		return nil, errors.New("scheduled delivery requires platform and records")
	}
	return &auditedPlatform{inner: deps.Inner, records: deps.Records}, nil
}
func (p *auditedPlatform) Receive(ctx context.Context) (agentkit.MessageEvent, error) {
	return p.inner.Receive(ctx)
}
func (p *auditedPlatform) Send(ctx context.Context, event agentkit.OutboundEvent) error {
	p.records.mu.Lock()
	run := p.records.runs[event.TurnID]
	p.records.mu.Unlock()
	if run == nil {
		return p.inner.Send(ctx, event)
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	// Audit writes survive the turn's cancellation but have a bounded deadline.
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	store, err := state.Open(p.records.cfg.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()
	if event.Type == agentkit.EventTurnStart {
		if err := store.SetStatus(auditCtx, run.key, state.StatusRunning, ""); err != nil {
			return err
		}
	}
	finalOutput := finalResponse(event)
	if finalOutput || event.Type == agentkit.EventTurnEnd {
		if run.deliveryID == 0 {
			d, err := store.RecordDelivery(auditCtx, run.key, state.DeliveryPending, "")
			if err != nil {
				return err
			}
			run.deliveryID = d.ID
		}
		if finalOutput {
			run.hasOutput = true
		}
	}
	sendErr := p.inner.Send(ctx, event)
	if sendErr != nil {
		run.deliveryFailed = true
		if run.deliveryID == 0 {
			d, err := store.RecordDelivery(auditCtx, run.key, state.DeliveryPending, "")
			if err != nil {
				return errors.Join(sendErr, err)
			}
			run.deliveryID = d.ID
		}
		status := state.DeliveryUnknown
		var definite *delivery.Error
		if errors.Is(sendErr, weixin.ErrReplyContextUnavailable) || errors.Is(sendErr, weixin.ErrSessionExpired) || (errors.As(sendErr, &definite) && !definite.Unknown) {
			status = state.DeliveryFailed
		}
		if err := store.UpdateDelivery(auditCtx, run.deliveryID, status, "platform delivery failed; response was not retried"); err != nil {
			return errors.Join(sendErr, err)
		}
	}
	if event.Type == agentkit.EventTurnEnd {
		var end capsession.TurnEndData
		status := state.StatusRunning
		detail := ""
		if json.Unmarshal(event.Data, &end) != nil {
			status = state.StatusFailed
			detail = "invalid terminal event"
		} else if end.Cancelled {
			status = state.StatusCancelled
		} else if end.Failed {
			status = state.StatusFailed
		}
		if run.deliveryFailed {
			detail = "execution ended; delivery failed or is unknown; not retried"
		} else if run.deliveryID != 0 {
			delivered := run.hasOutput || len(end.Message) > 0
			deliveryStatus := state.DeliverySent
			if !delivered {
				deliveryStatus = state.DeliveryFailed
				detail = "execution ended without a final response"
			}
			if err := store.UpdateDelivery(auditCtx, run.deliveryID, deliveryStatus, detail); err != nil {
				return errors.Join(sendErr, err)
			}
		}
		if err := store.SetStatus(auditCtx, run.key, status, detail); err != nil {
			return errors.Join(sendErr, err)
		}

	}
	if sendErr != nil {
		return &deliveryFailure{cause: sendErr, generated: finalOutput}
	}
	return nil
}

// Preserve optional delivery/approval/history capabilities of single and multiplex platforms.
func (p *auditedPlatform) PermissionCapabilityFor(id string) permission.Capability {
	if router, ok := p.inner.(permission.CapabilityRouter); ok {
		return router.PermissionCapabilityFor(id)
	}
	if capable, ok := p.inner.(permission.Capable); ok {
		return capable.PermissionCapability()
	}
	return permission.Capability{}
}
func (p *auditedPlatform) PermissionCapability() permission.Capability {
	if capable, ok := p.inner.(permission.Capable); ok {
		return capable.PermissionCapability()
	}
	return permission.Capability{}
}
func (p *auditedPlatform) SendCard(ctx context.Context, route agentkit.RouteRef, card *delivery.Card) (delivery.MessageRef, error) {
	if sender, ok := p.inner.(delivery.CardSender); ok {
		return sender.SendCard(ctx, route, card)
	}
	return delivery.MessageRef{}, &delivery.Error{Code: delivery.CodeUnsupported}
}
func (p *auditedPlatform) ChatHistoryFor(route agentkit.RouteRef) chathistory.Provider {
	if router, ok := p.inner.(chathistory.Router); ok {
		return router.ChatHistoryFor(route)
	}
	provider, _ := p.inner.(chathistory.Provider)
	return provider
}

var _ agentkit.Platform = (*auditedPlatform)(nil)
var _ capschedule.Runtime = (*auditedRuntime)(nil)
var _ agentkit.AppInitializer = (*Records)(nil)

func finalResponse(event agentkit.OutboundEvent) bool {
	var message agentkit.ModelMessage
	switch event.Type {
	case agentkit.EventAssistantMessage:
		if json.Unmarshal(event.Data, &message) != nil {
			return false
		}
	case agentkit.EventMessageEnd:
		var payload agentkit.MessageEndPayload
		if json.Unmarshal(event.Data, &payload) != nil {
			return false
		}
		message = payload.Message
	default:
		return false
	}
	for _, part := range message.Content {
		if part.Type != "thinking" && (part.Text != "" || part.URL != "" || part.Source != "") {
			return true
		}
	}
	return false
}

// deliveryFailure preserves evidence that a final result was generated before
// transport failure. This is separate from prompt/agent execution failure.
type deliveryFailure struct {
	cause     error
	generated bool
}

func (e *deliveryFailure) Error() string { return e.cause.Error() }
func (e *deliveryFailure) Unwrap() error { return e.cause }
func generatedDeliveryFailure(err error) bool {
	var failure *deliveryFailure
	return errors.As(err, &failure) && failure.generated
}
func (p *auditedPlatform) UserTimezone(userID string) string {
	if provider, ok := p.inner.(agentkit.UserTimezoneProvider); ok {
		return provider.UserTimezone(userID)
	}
	return ""
}
