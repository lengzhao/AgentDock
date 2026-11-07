package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/lengzhao/agentkit"
	capschedule "github.com/lengzhao/agentkit/cap/schedule"
	upstream "github.com/lengzhao/agentkit/plugins/tool/schedule"
	"github.com/lengzhao/pluginkit"
)

const KindTool = "agentdock/tool-schedule"

type ToolConfig struct {
	Timezone string `json:"timezone"`
	MaxJobs  int    `json:"maxJobs"`
}
type ToolDeps struct {
	Schedule capschedule.Registry `json:"schedule"`
	Engine   capschedule.Engine   `json:"engine"`
}
type safeTool struct {
	inner    agentkit.Tool
	loc      *time.Location
	schedule capschedule.Registry
	mu       sync.Mutex
}

// NewTool retains the upstream schedule tool schema and /cron provider while
// interpreting offset-free absolute times in the configured timezone.
func NewTool(cfg ToolConfig, deps ToolDeps) (agentkit.Tool, error) {
	loc, err := location(cfg.Timezone)
	if err != nil {
		return nil, err
	}
	tool, err := upstream.NewSchedule(upstream.ScheduleConfig{MaxJobs: cfg.MaxJobs}, upstream.ScheduleDeps{Schedule: deps.Schedule, Engine: deps.Engine})
	if err != nil {
		return nil, err
	}
	return &safeTool{inner: tool, loc: loc, schedule: deps.Schedule}, nil
}
func (t *safeTool) Name() string { return t.inner.Name() }
func (t *safeTool) Description() string {
	return t.inner.Description() + " Times without an explicit UTC offset use " + t.loc.String() + "."
}
func (t *safeTool) InputSchema() agentkit.JSONSchema { return t.inner.InputSchema() }
func (t *safeTool) Commands() []agentkit.Command {
	if provider, ok := t.inner.(agentkit.CommandProvider); ok {
		return provider.Commands()
	}
	return nil
}
func (t *safeTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	// Do not let the generic tool builder turn authorization failures into a
	// successful transport response. The scoped registry rechecks on mutation.
	if _, err := t.schedule.List(ctx); err != nil {
		return "", err
	}
	var input upstream.ScheduleInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", errors.New("invalid schedule tool arguments")
	}
	if strings.EqualFold(strings.TrimSpace(input.Op), "add") && strings.EqualFold(strings.TrimSpace(input.Kind), capschedule.KindAt) {
		at, err := parseAt(strings.TrimSpace(input.At), t.loc)
		if err != nil {
			return "", err
		}
		input.At = at.Format(time.RFC3339Nano)
		raw, err = json.Marshal(input)
		if err != nil {
			return "", errors.New("cannot encode schedule arguments")
		}
	}
	// The model-visible list/count/add sequence is serialized for one shared tool.
	// The durable Store additionally enforces its cap atomically across instances.
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.inner.Call(ctx, raw)
}
func parseAt(raw string, loc *time.Location) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed, nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04"} {
		if parsed, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, errors.New("invalid absolute schedule time; use RFC3339 or YYYY-MM-DDTHH:MM")
}
func init() { pluginkit.Register(KindTool, NewTool) }
