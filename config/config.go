// Package config owns AgentDock's user configuration: the on-disk model, its
// validation, credential references, and atomic persistence.
//
// The file never contains secret values. Every secret is a reference
// (keyring://service/account or env:NAME) resolved at startup by the credentials
// package; see docs/requirements.md SEC-01/SEC-02.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/lengzhao/AgentDock/credentials"
)

// Version is the current config schema version.
const Version = 2

// DefaultPlatformKind is used when Platform.Kind is empty.
const DefaultPlatformKind = "lark"

// EnvHome overrides the AgentDock home directory (used by tests and by
// `agentdock --home`).
const EnvHome = "AGENTDOCK_HOME"

// Config is the whole user configuration.
type Config struct {
	Scheduling Scheduling `yaml:"schedule,omitempty"`
	Platforms  []Platform `yaml:"platforms,omitempty"`
	PrimaryKind string    `yaml:"primaryKind,omitempty"`
	Version    int        `yaml:"version"`
	InstallID  string     `yaml:"installId"`
	Agents     []Agent    `yaml:"agents"`
	Runtime    Runtime    `yaml:"runtime"`
	Service    Service    `yaml:"service"`
}

// Scheduling configures the shared timer service. Disabled is explicit so older
// installations gain scheduling without needing to rewrite their config first.
type Scheduling struct {
	Disabled           bool   `yaml:"disabled,omitempty"`
	Timezone           string `yaml:"timezone,omitempty"`
	PollSeconds        int    `yaml:"pollSeconds,omitempty"`
	MaxJobs            int    `yaml:"maxJobs,omitempty"`
	MissedGraceSeconds int    `yaml:"missedGraceSeconds,omitempty"`
}

func (c Config) ScheduleOptions() Scheduling {
	s := c.Scheduling
	if s.Timezone == "" {
		s.Timezone = "Asia/Shanghai"
	}
	if s.PollSeconds == 0 {
		s.PollSeconds = 1
	}
	if s.MaxJobs == 0 {
		s.MaxJobs = 32
	}
	if s.MissedGraceSeconds == 0 {
		s.MissedGraceSeconds = 300
	}
	return s
}

// Platform describes one messaging connection.
type Platform struct {
	// AuthPlugin overrides the registered login kind. Empty selects platform/auth-<kind>.
	AuthPlugin string `yaml:"authPlugin,omitempty"`
	// Credentials maps platform roles to references, never literal values.
	Credentials map[string]string `yaml:"credentials,omitempty"`
	// Settings contains non-sensitive platform metadata returned by login.
	Settings map[string]string `yaml:"settings,omitempty"`
	// Kind is the AgentKit platform kind to use: "lark" (international) or
	// "feishu" (China). Empty means "lark".
	Kind string `yaml:"kind"`
	// AppIDRef is a credential reference. The App ID is not a secret, but keeping
	// both halves in the same backend avoids half-configured installs.
	AppIDRef string `yaml:"appIdRef"`
	// AppSecretRef must be a credential reference, never a literal secret.
	AppSecretRef string `yaml:"appSecretRef"`
	// AllowFrom is a comma-separated open_id allowlist. Empty means "anyone the
	// platform lets in", which is only acceptable together with Access.Owner.
	AllowFrom string `yaml:"allowFrom"`
	// AllowChat is a comma-separated group chat_id allowlist; empty means all.
	AllowChat string `yaml:"allowChat"`
	// GroupReplyAll makes the bot answer every group message instead of only
	// mentions. Off by default.
	GroupReplyAll bool `yaml:"groupReplyAll"`
}

// Agent is one ACP adapter entry.
type Agent struct {
	// ID is the agent id referenced by loop.defaultAgent and /agent use.
	ID string `yaml:"id"`
	// Command is the adapter argv, e.g. ["npx","-y","@agentclientprotocol/codex-acp@2.5.0"].
	Command []string `yaml:"command"`
	// Cwd is relative to the instance work dir.
	Cwd string `yaml:"cwd"`
	// AuthMethod is passed to ACP authenticate when non-empty.
	AuthMethod string `yaml:"authMethod"`
	// AutoApprove grants tool permission requests without asking. Off by default;
	// turning it on removes the approval loop described in ACL-02.
	AutoApprove bool `yaml:"autoApprove"`
	// Env contains literal nonsecret settings or keyring:// / env: references.
	Env map[string]string `yaml:"env"`
}

// Runtime holds AgentKit-level runtime settings.
type Runtime struct {
	// SessionScope is channel|thread|user.
	SessionScope string `yaml:"sessionScope"`
	// DefaultAgent is the agent used when a new session starts.
	DefaultAgent string `yaml:"defaultAgent"`
	// MaxConcurrentTurns caps cross-session parallelism.
	MaxConcurrentTurns int `yaml:"maxConcurrentTurns"`
	// WorkDir is the directory agents operate in, relative to the instance dir.
	WorkDir string `yaml:"workDir"`
}

// Service holds launchd settings.
type Service struct {
	// Label is the LaunchAgent label.
	Label string `yaml:"label"`
	// KeepAlive restarts the process after it exits.
	KeepAlive bool `yaml:"keepAlive"`
	// ThrottleSeconds is launchd's restart throttle.
	ThrottleSeconds int `yaml:"throttleSeconds"`
}

// Default returns a config with sane defaults for a fresh install.
func Default() Config {
	return Config{
		Version:     Version,
		InstallID:   NewInstallID(),
		PrimaryKind: DefaultPlatformKind,
		Platforms: []Platform{
			{Kind: DefaultPlatformKind},
		},
		Runtime: Runtime{
			SessionScope:       "thread",
			MaxConcurrentTurns: 4,
			WorkDir:            "work",
		},
		Service: Service{
			Label:           "io.agentdock.agent",
			KeepAlive:       true,
			ThrottleSeconds: 10,
		},
	}
}

// NewInstallID returns a fresh random installation id. It namespaces keychain
// accounts so a test install and a real install on one machine do not collide.
func NewInstallID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "install-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return "install-" + hex.EncodeToString(buf)
}

// Home returns the AgentDock home directory: $AGENTDOCK_HOME or ~/.agentdock.
func Home() (string, error) {
	if v := strings.TrimSpace(os.Getenv(EnvHome)); v != "" {
		return filepath.Abs(v)
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(dir, ".agentdock"), nil
}

// Paths are the files AgentDock owns under Home.
type Paths struct {
	Home string
	// InstanceDir is the runtime root: agent workspace, session logs and state.
	InstanceDir string
	ConfigFile       string
	AgentPresetsFile string
	StateDir         string
	LogDir      string
	CacheDir    string
	RunDir      string
}

// ResolvePaths returns the layout under home, creating nothing.
func ResolvePaths(home string) Paths {
	instance := filepath.Join(home, "instance")
	return Paths{
		Home:        home,
		InstanceDir: instance,
		ConfigFile:       filepath.Join(home, "config.yaml"),
		AgentPresetsFile: filepath.Join(home, "agent-presets.json"),
		StateDir:         filepath.Join(instance, "state"),
		LogDir:      filepath.Join(home, "logs"),
		CacheDir:    filepath.Join(home, "cache"),
		RunDir:      filepath.Join(home, "run"),
	}
}

// EnsureDirs creates the directories AgentDock writes to.
func (p Paths) EnsureDirs() error {
	for _, dir := range []string{p.Home, p.InstanceDir, p.StateDir, p.LogDir, p.CacheDir, p.RunDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	return nil
}

// ErrNotConfigured means config.yaml does not exist yet.
var ErrNotConfigured = errors.New("agentdock is not configured: run `agentdock setup`")

// Load reads and validates the config file.
func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, ErrNotConfigured
		}
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg Config
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

// Save writes the config atomically (temp file + fsync + rename) with 0600.
func Save(path string, cfg Config) error {
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return err
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	header := "# AgentDock configuration. Secrets are references only; values live in the OS credential store.\n"
	return WriteFileAtomic(path, append([]byte(header), out...))
}

// WriteFileAtomic replaces path with data in one step, so a crash cannot leave a
// half-written config or state file behind.
func WriteFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		// Best effort: the rename below removes the need for this on success.
		_ = os.Remove(tmpName)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

func (c *Config) applyDefaults() {
	if c.Version == 0 {
		c.Version = Version
	}
	if len(c.Platforms) == 0 {
		c.Platforms = []Platform{{Kind: DefaultPlatformKind}}
	}
	for i := range c.Platforms {
		if c.Platforms[i].Kind == "" {
			c.Platforms[i].Kind = DefaultPlatformKind
		}
	}
	if c.PrimaryKind == "" {
		c.PrimaryKind = c.Platforms[0].Kind
	}
	if _, ok := c.Connection(c.PrimaryKind); !ok {
		c.Platforms = append([]Platform{{Kind: c.PrimaryKind}}, c.Platforms...)
	}
	if c.Runtime.SessionScope == "" {
		c.Runtime.SessionScope = "thread"
	}
	if c.Runtime.WorkDir == "" {
		c.Runtime.WorkDir = "work"
	}
	if c.Runtime.MaxConcurrentTurns == 0 {
		c.Runtime.MaxConcurrentTurns = 4
	}
	if c.Service.Label == "" {
		c.Service.Label = "io.agentdock.agent"
	}
	if c.Service.ThrottleSeconds == 0 {
		c.Service.ThrottleSeconds = 10
	}
	for i := range c.Agents {
		if c.Agents[i].Cwd == "" {
			c.Agents[i].Cwd = c.Runtime.WorkDir
		}
	}
}

// Validate checks structural rules. It deliberately does not reach out to the
// credential store or the network; doctor does that.
func (c Config) Validate() error {
	scheduling := c.ScheduleOptions()
	if _, err := time.LoadLocation(scheduling.Timezone); err != nil {
		return errors.New("schedule.timezone must be a valid IANA timezone")
	}
	if scheduling.PollSeconds < 1 || scheduling.MaxJobs < 1 || scheduling.MissedGraceSeconds < 1 {
		return errors.New("schedule pollSeconds, maxJobs and missedGraceSeconds must be positive")
	}
	if c.Version != Version {
		return fmt.Errorf("unsupported config version %d (want %d)", c.Version, Version)
	}
	if c.InstallID == "" {
		return errors.New("installId is required")
	}
	if c.PrimaryKind == "" {
		return errors.New("primaryKind is required")
	}
	if len(c.Platforms) == 0 {
		return errors.New("at least one platform is required")
	}
	if _, ok := c.Connection(c.PrimaryKind); !ok {
		return fmt.Errorf("primaryKind %q is not configured", c.PrimaryKind)
	}
	seenPlatforms := map[string]bool{}
	for _, connection := range c.Platforms {
		if seenPlatforms[connection.Kind] {
			return fmt.Errorf("platforms contains duplicate platform %q", connection.Kind)
		}
		seenPlatforms[connection.Kind] = true
		if err := validatePlatform(connection); err != nil {
			return fmt.Errorf("platforms[%s]: %w", connection.Kind, err)
		}
	}
	if len(c.Agents) == 0 {
		return errors.New("at least one agent is required")
	}
	seen := map[string]bool{}
	for i, a := range c.Agents {
		if strings.TrimSpace(a.ID) == "" {
			return fmt.Errorf("agents[%d].id is required", i)
		}
		if seen[a.ID] {
			return fmt.Errorf("agents[%d].id %q is duplicated", i, a.ID)
		}
		seen[a.ID] = true
		if len(a.Command) == 0 {
			return fmt.Errorf("agents[%s].command is required", a.ID)
		}
		for k := range a.Env {
			if !credentials.ValidEnvName(k) {
				return fmt.Errorf("agents[%s] contains an invalid environment variable name", a.ID)
			}
			if strings.HasPrefix(a.Env[k], "keyring://") || strings.HasPrefix(a.Env[k], "env:") {
				if _, err := credentials.ParseRef(a.Env[k]); err != nil {
					return fmt.Errorf("agents[%s].env[%s] has an invalid credential reference", a.ID, k)
				}
				continue
			}
			if LooksLikeSecretName(k) && a.Env[k] != "" {
				return fmt.Errorf("agents[%s].env[%s] looks like a secret; use a credential reference instead", a.ID, k)
			}
		}
	}
	if c.Runtime.DefaultAgent == "" {
		return errors.New("runtime.defaultAgent is required")
	}
	if !seen[c.Runtime.DefaultAgent] {
		return fmt.Errorf("runtime.defaultAgent %q is not a configured agent", c.Runtime.DefaultAgent)
	}
	switch c.Runtime.SessionScope {
	case "channel", "thread", "user":
	default:
		return fmt.Errorf("runtime.sessionScope must be channel, thread, or user, got %q", c.Runtime.SessionScope)
	}
	if c.Runtime.MaxConcurrentTurns < 0 {
		return errors.New("runtime.maxConcurrentTurns must not be negative")
	}
	if c.Service.ThrottleSeconds < 0 {
		return errors.New("service.throttleSeconds must not be negative")
	}
	return nil
}

func validatePlatform(p Platform) error {
	if !platformName.MatchString(p.Kind) {
		return errors.New("platform.kind must be a simple lowercase name")
	}
	switch p.Kind {
	case "lark", "feishu", "weixin":
	default:
		if p.AuthPlugin == "" || strings.TrimSpace(p.Kind) == "" {
			return fmt.Errorf("platform.kind requires a registered auth plugin for %q", p.Kind)
		}
	}
	if len(p.Credentials) > 0 && (p.AppIDRef != "" || p.AppSecretRef != "") {
		return errors.New("platform.credentials cannot be combined with legacy app references")
	}
	for name, value := range p.Settings {
		if LooksLikeSecretName(name) && value != "" {
			return errors.New("platform.settings must not contain credential values; use platform.credentials references")
		}
	}
	refs := p.References()
	if len(p.Credentials) == 0 {
		if _, err := credentials.ParseRef(p.AppIDRef); err != nil {
			return fmt.Errorf("platform.appIdRef: %w", err)
		}
		if _, err := credentials.ParseRef(p.AppSecretRef); err != nil {
			return fmt.Errorf("platform.appSecretRef: %w", err)
		}
	}
	if refs["id"] == "" || refs["secret"] == "" {
		return errors.New("platform.credentials requires id and secret references")
	}
	for role, ref := range refs {
		if _, err := credentials.ParseRef(ref); err != nil {
			return fmt.Errorf("platform credential role %q must be a valid reference", role)
		}
	}
	return nil
}

// Agent looks up a configured agent by id.
func (c Config) Agent(id string) (Agent, bool) {
	for _, a := range c.Agents {
		if a.ID == id {
			return a, true
		}
	}
	return Agent{}, false
}

// secretNameHints are substrings that mark an env var as a secret.
var secretNameHints = []string{"secret", "token", "password", "api_key", "apikey", "credential"}

// LooksLikeSecretName reports whether an environment variable name suggests a
// secret value that must not be stored in plain configuration.
func LooksLikeSecretName(name string) bool {
	lower := strings.ToLower(name)
	for _, hint := range secretNameHints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

// References returns current role references, with legacy app references as fallback.
func (p Platform) References() map[string]string {
	refs := map[string]string{}
	if len(p.Credentials) > 0 {
		for k, v := range p.Credentials {
			refs[k] = v
		}
		return refs
	}
	refs["id"] = p.AppIDRef
	refs["secret"] = p.AppSecretRef
	return refs
}

// LoginKind selects the platform's authentication plugin.
func (p Platform) LoginKind() string {
	if p.AuthPlugin != "" {
		return p.AuthPlugin
	}
	return "platform/auth-" + p.Kind
}

var platformName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Primary returns the configured primary platform connection.
func (c Config) Primary() Platform {
	if p, ok := c.Connection(c.PrimaryKind); ok {
		return p
	}
	if len(c.Platforms) > 0 {
		return c.Platforms[0]
	}
	return Platform{Kind: DefaultPlatformKind}
}

// AdditionalPlatforms lists non-primary connections.
func (c Config) AdditionalPlatforms() []Platform {
	out := make([]Platform, 0, len(c.Platforms))
	for _, p := range c.Platforms {
		if p.Kind != c.PrimaryKind {
			out = append(out, p)
		}
	}
	return out
}

// AsSinglePlatform returns a copy that treats kind as the only connection.
func (c Config) AsSinglePlatform(kind string) Config {
	p, ok := c.Connection(kind)
	if !ok {
		return c
	}
	out := c
	out.PrimaryKind = p.Kind
	out.Platforms = []Platform{p}
	return out
}

// Connections returns the primary connection followed by additional platforms.
func (c Config) Connections() []Platform {
	out := make([]Platform, 0, len(c.Platforms))
	if p, ok := c.Connection(c.PrimaryKind); ok {
		out = append(out, p)
	}
	for _, p := range c.Platforms {
		if p.Kind != c.PrimaryKind {
			out = append(out, p)
		}
	}
	return out
}

// Connection finds a configured platform. Empty kind selects the primary connection.
func (c Config) Connection(kind string) (Platform, bool) {
	if kind == "" {
		kind = c.PrimaryKind
	}
	for _, p := range c.Platforms {
		if p.Kind == kind {
			return p, true
		}
	}
	return Platform{}, false
}

// UpsertPlatform adds or updates only the selected platform. The first login
// fills the default unconfigured primary slot; later logins preserve it.
func (c *Config) UpsertPlatform(p Platform) {
	if p.Kind == "" {
		p.Kind = DefaultPlatformKind
	}
	for i, existing := range c.Platforms {
		if existing.Kind == p.Kind {
			c.Platforms[i] = p
			return
		}
	}
	if len(c.Platforms) == 1 && c.Platforms[0].References()["id"] == "" {
		c.Platforms[0] = p
		c.PrimaryKind = p.Kind
		return
	}
	c.Platforms = append(c.Platforms, p)
}

// ForPlatform keeps the existing primary state path and isolates added platforms.
// kind must come from a validated Config.Connection, never an arbitrary path.
func (p Paths) ForPlatform(kind, primary string) Paths {
	if kind != primary {
		p.StateDir = filepath.Join(p.StateDir, "platforms", kind)
	}
	return p
}
