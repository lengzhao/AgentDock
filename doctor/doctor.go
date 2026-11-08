// Package doctor reports layered health instead of a single "ok".
//
// OBS-01 requires distinguishing process liveness, credential readability,
// platform readiness, agent readiness and a real end-to-end pass. Each check is
// independent so a single failing layer is actionable on its own.
package doctor

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lengzhao/AgentDock/agent"
	"github.com/lengzhao/AgentDock/config"
	"github.com/lengzhao/AgentDock/credentials"
	"github.com/lengzhao/AgentDock/service"
)

// Level is the outcome of one check.
type Level string

const (
	// LevelOK means the check passed.
	LevelOK Level = "ok"
	// LevelWarn means it works but something needs attention.
	LevelWarn Level = "warn"
	// LevelFail means the layer is broken.
	LevelFail Level = "fail"
	// LevelSkip means the check could not run in this context.
	LevelSkip Level = "skip"
)

// Check is one diagnostic result.
type Check struct {
	Name   string
	Level  Level
	Detail string
}

// Report is the ordered set of checks.
type Report struct {
	Checks []Check
	// EndToEndAt is when the last verified end-to-end message succeeded, if known.
	EndToEndAt time.Time
}

// Worst returns the most severe level present.
func (r Report) Worst() Level {
	worst := LevelOK
	rank := map[Level]int{LevelOK: 0, LevelSkip: 1, LevelWarn: 2, LevelFail: 3}
	for _, c := range r.Checks {
		if rank[c.Level] > rank[worst] {
			worst = c.Level
		}
	}
	return worst
}

// ExitCode maps the worst level onto a process exit code.
func (r Report) ExitCode() int {
	switch r.Worst() {
	case LevelFail:
		return 1
	case LevelWarn:
		return 2
	default:
		return 0
	}
}

// Options configures a doctor run.
type Options struct {
	Paths config.Paths
	// Config is the loaded config; nil means the config layer already failed.
	Config *config.Config
	// ConfigErr is why the config could not be loaded.
	ConfigErr error
	// Store is the credential backend.
	Store credentials.Store
	// Service is the service backend; nil skips service checks.
	Service service.ServiceManager
}

// Run performs every check it can. Checks never abort the run: a failure is
// recorded and the next layer still reports.
func Run(ctx context.Context, opt Options) Report {
	var rep Report

	// Layer 1: configuration.
	if opt.ConfigErr != nil {
		rep.Checks = append(rep.Checks, Check{Name: "config", Level: LevelFail, Detail: opt.ConfigErr.Error()})
	} else {
		rep.Checks = append(rep.Checks, Check{Name: "config", Level: LevelOK,
			Detail: fmt.Sprintf("%d agent(s), default %s", len(opt.Config.Agents), opt.Config.Runtime.DefaultAgent)})
	}

	// Layer 2: credential backend and references.
	if opt.Store == nil {
		rep.Checks = append(rep.Checks, Check{Name: "credentials", Level: LevelSkip, Detail: "no credential store configured"})
	} else {
		st := opt.Store.Status(ctx)
		level := LevelOK
		if !st.Available {
			level = LevelFail
		}
		rep.Checks = append(rep.Checks, Check{Name: "credentials", Level: level,
			Detail: st.Backend + ": " + st.Detail})
		// Ephemeral backends cannot hold credentials across processes, so resolving
		// references against them would report a failure of the test harness rather
		// than of the install.
		ephemeral := strings.HasPrefix(st.Backend, "memory")
		if opt.Config != nil && st.Available && !ephemeral {
			for _, connection := range opt.Config.Connections() {
				for role, value := range connection.References() {
					ref := struct{ name, value string }{connection.Kind + "." + role, value}
					if _, err := credentials.NewResolver(opt.Store, os.LookupEnv).Resolve(ctx, ref.value); err != nil {
						rep.Checks = append(rep.Checks, Check{Name: "credentials/" + ref.name, Level: LevelFail,
							Detail: err.Error()})
						continue
					}
					rep.Checks = append(rep.Checks, Check{Name: "credentials/" + ref.name, Level: LevelOK,
						Detail: "readable"})
				}
			}
		} else if opt.Config != nil && ephemeral {
			rep.Checks = append(rep.Checks, Check{Name: "credentials/refs", Level: LevelSkip,
				Detail: "cannot check references against an in-memory store"})
		}
	}

	// Layer 3: agent executables. Existence is not availability (SET-03), so this
	// layer reports what was detected and never claims a login is valid; only
	// `agentdock verify` proves the full path.
	if opt.Config != nil {
		infos := agent.Detect(ctx, *opt.Config, 10*time.Second)
		for _, info := range infos {
			level := LevelOK
			switch info.State {
			case agent.StateMissing:
				level = LevelFail
			case agent.StateBroken:
				level = LevelFail
			case agent.StateUnknown:
				level = LevelWarn
			}
			detail := string(info.State)
			if info.Path != "" {
				detail += ": " + info.Path
			}
			if info.Version != "" {
				detail += " (" + info.Version + ")"
			}
			if info.Detail != "" {
				detail += " — " + info.Detail
			}
			if info.ID == opt.Config.Runtime.DefaultAgent {
				detail += " [default; login not verified]"
			}
			rep.Checks = append(rep.Checks, Check{Name: "agent/" + info.ID, Level: level, Detail: detail})
		}
	}

	// Layer 4: service state.
	if opt.Service != nil {
		st, err := opt.Service.Status(ctx)
		switch {
		case err != nil:
			rep.Checks = append(rep.Checks, Check{Name: "service", Level: LevelWarn, Detail: err.Error()})
		case st.State == service.StateRunning:
			rep.Checks = append(rep.Checks, Check{Name: "service", Level: LevelOK,
				Detail: fmt.Sprintf("running (pid %d)", st.PID)})
		case st.State == service.StateNotInstalled:
			rep.Checks = append(rep.Checks, Check{Name: "service", Level: LevelSkip, Detail: "not installed"})
		default:
			rep.Checks = append(rep.Checks, Check{Name: "service", Level: LevelWarn,
				Detail: strings.TrimSpace(fmt.Sprintf("%s %s", st.State, st.Detail))})
		}
	}

	// Layer 5: last end-to-end evidence.
	if ts, ok := loadEndToEnd(opt.Paths); ok {
		rep.EndToEndAt = ts
		age := time.Since(ts).Round(time.Minute)
		level := LevelOK
		if age > 30*24*time.Hour {
			level = LevelWarn
		}
		rep.Checks = append(rep.Checks, Check{Name: "end-to-end", Level: level,
			Detail: fmt.Sprintf("last verified %s ago (%s)", age, ts.Format(time.RFC3339))})
	} else {
		rep.Checks = append(rep.Checks, Check{Name: "end-to-end", Level: LevelSkip,
			Detail: "no successful test message recorded; run `agentdock verify`"})
	}
	if opt.Config != nil {
		for _, connection := range opt.Config.AdditionalPlatforms() {
			paths := opt.Paths.ForPlatform(connection.Kind, opt.Config.PrimaryKind)
			check := Check{Name: "end-to-end/" + connection.Kind, Level: LevelSkip, Detail: "not verified; run agentdock verify --platform " + connection.Kind}
			if ts, ok := loadEndToEnd(paths); ok {
				check.Level = LevelOK
				check.Detail = "last verified " + ts.Format(time.RFC3339)
				if time.Since(ts) > 30*24*time.Hour {
					check.Level = LevelWarn
				}
			}
			rep.Checks = append(rep.Checks, check)
		}
	}
	return rep
}

func loadEndToEnd(p config.Paths) (time.Time, bool) {
	if p.StateDir == "" {
		return time.Time{}, false
	}
	raw, err := os.ReadFile(p.StateDir + "/end-to-end")
	if err != nil {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339, strings.TrimSpace(string(raw)))
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}

// RecordEndToEnd stamps a successful end-to-end verification.
func RecordEndToEnd(p config.Paths, at time.Time) error {
	if err := os.MkdirAll(p.StateDir, 0o700); err != nil {
		return err
	}
	return config.WriteFileAtomic(p.StateDir+"/end-to-end", []byte(at.UTC().Format(time.RFC3339)+"\n"))
}
