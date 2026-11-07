package schedule

import (
	"context"
	"errors"
	"time"

	"github.com/lengzhao/AgentDock/state"
	"github.com/lengzhao/agentkit"
	capschedule "github.com/lengzhao/agentkit/cap/schedule"
	"github.com/lengzhao/pluginkit"
)

const LoopKind = "agentdock/schedule-loop"

type LoopDeps struct {
	Inner   agentkit.Loop `json:"inner"`
	Records *Records      `json:"records"`
}
type auditedLoop struct {
	agentkit.Loop
	records *Records
}

func NewAuditedLoop(_ struct{}, deps LoopDeps) (agentkit.Loop, error) {
	if deps.Inner == nil || deps.Records == nil {
		return nil, errors.New("scheduled loop requires loop and records")
	}
	return &auditedLoop{Loop: deps.Inner, records: deps.Records}, nil
}
func init() { pluginkit.Register(LoopKind, NewAuditedLoop) }
func (l *auditedLoop) Dispatch(ctx context.Context, req agentkit.LoopRequest) error {
	err := l.Loop.Dispatch(ctx, req)
	if !capschedule.IsFireTurn(req.Event.Envelope.Metadata) {
		return err
	}
	turn, _ := req.Event.Envelope.Metadata[agentkit.MetadataTurnID].(string)
	auditErr := l.records.finish(ctx, turn, err)
	if generatedDeliveryFailure(err) {
		return auditErr
	}
	return errors.Join(err, auditErr)
}
func (l *auditedLoop) SteerEntry(ctx context.Context, entry agentkit.SteerEntry) error {
	if reporter, ok := l.Loop.(agentkit.SteerReporter); ok {
		return reporter.SteerEntry(ctx, entry)
	}
	return l.Loop.Steer(ctx, entry.Message)
}
func (l *auditedLoop) Stop(ctx context.Context) error {
	if stopper, ok := l.Loop.(interface{ Stop(context.Context) error }); ok {
		return stopper.Stop(ctx)
	}
	return nil
}
func (r *Records) finish(ctx context.Context, turn string, runErr error) error {
	r.mu.Lock()
	run := r.runs[turn]
	delete(r.runs, turn)
	r.mu.Unlock()
	if run == nil {
		return nil
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	store, err := state.Open(r.cfg.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()
	task, err := store.Get(auditCtx, run.key)
	if err != nil {
		return err
	}
	status := state.StatusCompleted
	if task.Status == state.StatusFailed || task.Status == state.StatusCancelled {
		status = task.Status
	}
	detail := task.Detail
	if errors.Is(runErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		status = state.StatusCancelled
	} else if runErr != nil && !generatedDeliveryFailure(runErr) {
		status = state.StatusFailed
		detail = "scheduled agent execution failed; " + detail
	}
	return store.SetStatus(auditCtx, run.key, status, detail)
}
