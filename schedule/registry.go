// Package schedule provides durable, authorized AgentDock scheduling adapters.
package schedule

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/lengzhao/AgentDock/access"
	"github.com/lengzhao/AgentDock/config"
	"github.com/lengzhao/agentkit"
	capschedule "github.com/lengzhao/agentkit/cap/schedule"
	"github.com/lengzhao/agentkit/runtime/rctx"
	engines "github.com/lengzhao/agentkit/runtime/schedule"
	"github.com/lengzhao/pluginkit"
)

const (
	KindEngine      = "agentdock/schedule-engine"
	KindStore       = "agentdock/schedule-store"
	KindScoped      = "agentdock/schedule-scoped"
	DefaultTimezone = "Asia/Shanghai"
)

type EngineConfig struct {
	Timezone string `json:"timezone"`
}
type localizedEngine struct{ location *time.Location }
type localizedCron struct {
	inner    capschedule.Cron
	location *time.Location
}

func location(name string) (*time.Location, error) {
	if name == "" {
		name = DefaultTimezone
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, errors.New("invalid schedule timezone")
	}
	return loc, nil
}
func NewEngine(cfg EngineConfig) (capschedule.Engine, error) {
	loc, err := location(cfg.Timezone)
	if err != nil {
		return nil, err
	}
	return localizedEngine{location: loc}, nil
}
func (e localizedEngine) ParseCron(expr string) (capschedule.Cron, error) {
	parsed, err := engines.ParseCron(expr)
	if err != nil {
		return nil, err
	}
	return localizedCron{inner: parsed, location: e.location}, nil
}
func (e localizedEngine) NextFire(job capschedule.Job, after time.Time) (time.Time, bool) {
	return engines.NextFire(job, after.In(e.location))
}
func (c localizedCron) Next(after time.Time) (time.Time, bool) {
	return c.inner.Next(after.In(c.location))
}

type StoreConfig struct {
	Path     string `json:"path"`
	Timezone string `json:"timezone"`
	MaxJobs  int    `json:"maxJobs"`
}
type StoreDeps struct {
	Engine capschedule.Engine `json:"engine"`
}
type Store struct {
	path    string
	maxJobs int
	engine  capschedule.Engine
	loc     *time.Location
	mu      *sync.Mutex
}
type fileState struct {
	Jobs []capschedule.Job `json:"jobs"`
}

var pathLocks sync.Map

// NewStore uses one fixed absolute file, independent of the active workspace.
// Mutations hold an in-process and advisory file lock across read-modify-write.
func NewStore(cfg StoreConfig, deps StoreDeps) (capschedule.Registry, error) {
	if !filepath.IsAbs(cfg.Path) || deps.Engine == nil || cfg.MaxJobs < 0 {
		return nil, errors.New("schedule store requires an absolute path, engine and nonnegative maxJobs")
	}
	loc, err := location(cfg.Timezone)
	if err != nil {
		return nil, err
	}
	path := filepath.Clean(cfg.Path)
	mu, _ := pathLocks.LoadOrStore(path, &sync.Mutex{})
	if cfg.MaxJobs == 0 {
		cfg.MaxJobs = 32
	}
	return &Store{path: path, maxJobs: cfg.MaxJobs, engine: deps.Engine, loc: loc, mu: mu.(*sync.Mutex)}, nil
}
func (s *Store) state(ctx context.Context, write bool, fn func(*fileState) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(s.path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN && err != syscall.EINTR {
			return err
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	current := fileState{Jobs: []capschedule.Job{}}
	data, err := os.ReadFile(s.path)
	if err == nil {
		if json.Unmarshal(data, &current) != nil {
			return errors.New("schedule store contains invalid JSON")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var before []byte
	if write {
		before, _ = json.Marshal(current)
	}
	if err := fn(&current); err != nil {
		return err
	}
	if !write {
		return nil
	}
	after, err := json.Marshal(current)
	if err != nil {
		return err
	}
	if bytes.Equal(before, after) {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err = json.MarshalIndent(current, "", "  ")
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(s.path, append(data, '\n'))
}
func (s *Store) List(ctx context.Context) ([]capschedule.Job, error) {
	var jobs []capschedule.Job
	err := s.state(ctx, false, func(st *fileState) error { jobs = st.Jobs; return nil })
	return jobs, err
}
func (s *Store) validate(job capschedule.Job) error {
	if strings.TrimSpace(job.Prompt) == "" && (job.Source != capschedule.SourceConfig || strings.TrimSpace(job.Script) == "") {
		return errors.New("schedule job requires a prompt")
	}
	if job.Script != "" && job.Source != capschedule.SourceConfig {
		return errors.New("agent schedule jobs cannot run scripts")
	}
	switch job.NormalizedKind() {
	case capschedule.KindCron:
		_, err := s.engine.ParseCron(job.Cron)
		return err
	case capschedule.KindAt, capschedule.KindDelay:
		if job.FireAt.IsZero() {
			return errors.New("one-shot schedule requires a fire time")
		}
	default:
		return errors.New("unknown schedule kind")
	}
	return nil
}
func (s *Store) Add(ctx context.Context, job capschedule.Job) (capschedule.Job, error) {
	if err := s.validate(job); err != nil {
		return capschedule.Job{}, err
	}
	if job.Source == "" {
		job.Source = capschedule.SourceAgent
	}
	job.Kind = job.NormalizedKind()
	err := s.state(ctx, true, func(st *fileState) error {
		active := 0
		for _, existing := range st.Jobs {
			if job.ID != "" && existing.ID == job.ID {
				return errors.New("schedule job ID already exists")
			}
			if existing.Source == capschedule.SourceAgent && !existing.Fired && scopeKey(existing.Route, existing.Actor.UserID) == scopeKey(job.Route, job.Actor.UserID) {
				active++
			}
		}
		if job.Source == capschedule.SourceAgent && active >= s.maxJobs {
			return fmt.Errorf("schedule limit of %d active jobs reached for this conversation", s.maxJobs)
		}
		if job.ID == "" {
			var id [12]byte
			if _, err := rand.Read(id[:]); err != nil {
				return err
			}
			job.ID = job.Source + "-" + hex.EncodeToString(id[:])
		}
		if job.Kind == capschedule.KindCron && job.LastRun.IsZero() {
			job.LastRun = time.Now().In(s.loc)
		}
		st.Jobs = append(st.Jobs, job)
		return nil
	})
	return job, err
}
func (s *Store) Remove(ctx context.Context, id string) (bool, error) {
	return s.removeIf(ctx, id, func(capschedule.Job) bool { return true })
}
func (s *Store) removeIf(ctx context.Context, id string, match func(capschedule.Job) bool) (bool, error) {
	removed := false
	err := s.state(ctx, true, func(st *fileState) error {
		for index, job := range st.Jobs {
			if job.ID == id && match(job) {
				st.Jobs = append(st.Jobs[:index], st.Jobs[index+1:]...)
				removed = true
				break
			}
		}
		return nil
	})
	return removed, err
}
func (s *Store) SyncSource(ctx context.Context, source string, jobs []capschedule.Job) error {
	if source == "" {
		return errors.New("schedule source is required")
	}
	incoming := make([]capschedule.Job, len(jobs))
	copy(incoming, jobs)
	ids := map[string]bool{}
	for index := range incoming {
		incoming[index].Source = source
		if incoming[index].ID == "" || ids[incoming[index].ID] {
			return errors.New("source schedule IDs must be nonempty and unique")
		}
		ids[incoming[index].ID] = true
		if err := s.validate(incoming[index]); err != nil {
			return err
		}
	}
	return s.state(ctx, true, func(st *fileState) error {
		previous := map[string]capschedule.Job{}
		kept := make([]capschedule.Job, 0, len(st.Jobs)+len(incoming))
		for _, job := range st.Jobs {
			if job.Source == source {
				previous[job.ID] = job
				continue
			}
			if ids[job.ID] {
				return errors.New("schedule ID belongs to another source")
			}
			kept = append(kept, job)
		}
		for _, job := range incoming {
			if old, ok := previous[job.ID]; ok {
				job.LastRun = old.LastRun
				job.Fired = old.Fired
				job.InFlightAt = old.InFlightAt
			}
			job.Kind = job.NormalizedKind()
			if job.Kind == capschedule.KindCron && job.LastRun.IsZero() {
				job.LastRun = time.Now().In(s.loc)
			}
			kept = append(kept, job)
		}
		st.Jobs = kept
		return nil
	})
}
func (s *Store) Due(ctx context.Context, now time.Time) ([]capschedule.Job, error) {
	due := []capschedule.Job{}
	err := s.state(ctx, true, func(st *fileState) error {
		for index, job := range st.Jobs {
			if job.Fired {
				continue
			}
			if job.IsOneShot() {
				if !job.InFlightAt.IsZero() {
					st.Jobs[index].Fired = true
					continue
				}
				if job.FireAt.After(now) {
					continue
				}
				// Claim is terminal before enqueue. A crash or rejected enqueue remains
				// uncertain and is never replayed as another user-visible side effect.
				st.Jobs[index].Fired = true
				st.Jobs[index].InFlightAt = now
				st.Jobs[index].LastRun = now
			} else {
				next, ok := s.engine.NextFire(job, job.LastRun)
				if !ok || next.After(now) {
					continue
				}
				st.Jobs[index].LastRun = now
			}
			due = append(due, st.Jobs[index])
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(due, func(i, j int) bool { return due[i].ID < due[j].ID })
	return due, nil
}
func (s *Store) MarkFired(ctx context.Context, id string) error {
	return s.state(ctx, true, func(st *fileState) error {
		for index := range st.Jobs {
			if st.Jobs[index].ID == id {
				st.Jobs[index].Fired = true
				st.Jobs[index].InFlightAt = time.Time{}
				return nil
			}
		}
		return capschedule.ErrJobNotFound
	})
}

type ScopedConfig struct {
	AccessPaths map[string]string `json:"accessPaths"`
}
type ScopedDeps struct {
	Inner capschedule.Registry `json:"inner"`
}
type scopedRegistry struct {
	inner capschedule.Registry
	paths map[string]string
	mu    sync.Mutex
}

func NewScoped(cfg ScopedConfig, deps ScopedDeps) (capschedule.Registry, error) {
	if deps.Inner == nil || len(cfg.AccessPaths) == 0 {
		return nil, errors.New("scoped schedule requires registry and platform access paths")
	}
	paths := make(map[string]string, len(cfg.AccessPaths))
	for kind, path := range cfg.AccessPaths {
		if kind == "" || !filepath.IsAbs(path) {
			return nil, errors.New("schedule access path must be absolute")
		}
		paths[kind] = path
	}
	return &scopedRegistry{inner: deps.Inner, paths: paths}, nil
}
func scopeKey(route agentkit.RouteRef, user string) string {
	target, ok := rctx.DecodeSessionRoute(route)
	if !ok || route.Platform == "" || target.ChannelID == "" || user == "" {
		return ""
	}
	key, _ := json.Marshal([]string{route.Platform, target.ChannelID, target.ThreadID, user})
	return string(key)
}
func (s *scopedRegistry) identity(ctx context.Context) (agentkit.TurnEnvelope, string, error) {
	env := rctx.EnvelopeFromContext(ctx)
	if env.Actor.UserID == "" || env.Route.Platform == "" {
		return env, "", agentkit.ErrCommandForbidden
	}
	path := s.paths[env.Route.Platform]
	if path == "" {
		return env, "", agentkit.ErrCommandForbidden
	}
	decision, err := access.NewStore(path).Resolve(env.Actor.UserID, nil)
	if err != nil || !decision.Allow {
		return env, "", agentkit.ErrCommandForbidden
	}
	env.Route = env.Route.Stable()
	if _, ok := rctx.DecodeSessionRoute(env.Route); !ok || !env.Route.HasTarget() {
		if env.Conversation != "" {
			old := env.Route
			parsed := rctx.ParseDelivery(agentkit.SessionID(env.Conversation), env.Actor.UserID)
			if parsed.Platform != "" && parsed.Platform != old.Platform {
				return env, "", agentkit.ErrCommandForbidden
			}
			env.Route = rctx.SessionRouteFromDelivery(old.Platform, agentkit.SessionID(env.Conversation), "").WithInstance(old.Instance).Stable()
		}
	}
	key := scopeKey(env.Route, env.Actor.UserID)
	if key == "" {
		return env, "", agentkit.ErrCommandForbidden
	}
	return env, key, nil
}
func (s *scopedRegistry) List(ctx context.Context) ([]capschedule.Job, error) {
	_, key, err := s.identity(ctx)
	if err != nil {
		return nil, err
	}
	jobs, err := s.inner.List(ctx)
	if err != nil {
		return nil, err
	}
	result := []capschedule.Job{}
	for _, job := range jobs {
		if scopeKey(job.Route, job.Actor.UserID) == key {
			result = append(result, job)
		}
	}
	return result, nil
}
func (s *scopedRegistry) Add(ctx context.Context, job capschedule.Job) (capschedule.Job, error) {
	env, _, err := s.identity(ctx)
	if err != nil {
		return capschedule.Job{}, err
	}
	if job.Script != "" {
		return capschedule.Job{}, errors.New("agent schedule jobs cannot run scripts")
	}
	job.ID = ""
	job.Source = capschedule.SourceAgent
	job.LastRun = time.Time{}
	job.Fired = false
	job.InFlightAt = time.Time{}
	job.Route = env.Route
	job.Actor = env.Actor
	job.AgentID = env.AgentID
	job.ChannelKey = rctx.WorkspaceFromContext(ctx)
	return s.inner.Add(ctx, job)
}
func (s *scopedRegistry) Remove(ctx context.Context, id string) (bool, error) {
	_, key, err := s.identity(ctx)
	if err != nil {
		return false, err
	}
	match := func(job capschedule.Job) bool { return scopeKey(job.Route, job.Actor.UserID) == key }
	if atomic, ok := s.inner.(interface {
		removeIf(context.Context, string, func(capschedule.Job) bool) (bool, error)
	}); ok {
		return atomic.removeIf(ctx, id, match)
	}
	// Alternate registry implementations may lack predicate removal. Serialize the
	// scoped view; the global registry is reserved for the trusted firing runtime.
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs, err := s.inner.List(ctx)
	if err != nil {
		return false, err
	}
	for _, job := range jobs {
		if job.ID == id && match(job) {
			return s.inner.Remove(ctx, id)
		}
	}
	return false, nil
}
func (*scopedRegistry) SyncSource(context.Context, string, []capschedule.Job) error {
	return agentkit.ErrCommandForbidden
}
func (*scopedRegistry) Due(context.Context, time.Time) ([]capschedule.Job, error) {
	return nil, agentkit.ErrCommandForbidden
}
func (*scopedRegistry) MarkFired(context.Context, string) error { return agentkit.ErrCommandForbidden }

func init() {
	pluginkit.Register(KindEngine, NewEngine)
	pluginkit.Register(KindStore, NewStore)
	pluginkit.Register(KindScoped, NewScoped)
}
