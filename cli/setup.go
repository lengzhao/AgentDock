package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/lengzhao/AgentDock/agent"
	"github.com/lengzhao/AgentDock/assembly"
	"github.com/lengzhao/AgentDock/catalog"
	"github.com/lengzhao/AgentDock/config"
	"github.com/lengzhao/AgentDock/credentials"
	"github.com/lengzhao/AgentDock/platform"
	"github.com/lengzhao/AgentDock/setup"
)

type setupFlags struct {
	agentIDs     []string
	platformKind string
	authPlugin   string
	appID        string
	secretStdin  bool
	allowFrom    string
	nonInteract  bool
	fromCatalog  bool
	noAddons     bool
	tenantBrand  string
}

func newSetupCmd() *cobra.Command {
	flags := &setupFlags{}
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Guided configuration: choose agents, connect the platform, store credentials",
		Long: `Guided configuration.

Progress is saved after every step, so cancelling or re-running resumes where it
stopped and never registers a second platform application. Secrets are written to
the OS credential store and verified by reading them back; config.yaml only ever
holds references.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetup(cmd, flags)
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&flags.agentIDs, "agent", nil, "agent adapter ids to enable (repeatable), e.g. claude-acp,codex-acp")
	f.StringVar(&flags.platformKind, "platform", "", "platform: lark, feishu, weixin (default: existing platform or lark)")
	f.StringVar(&flags.authPlugin, "platform-auth", "", "registered platform authentication plugin kind")
	f.StringVar(&flags.appID, "app-id", "", "existing platform app id (skips catalog selection for the platform)")
	// A boolean, not a string: a string flag would let the secret be typed into
	// argv, where `ps` exposes it (SEC-02).
	f.BoolVar(&flags.secretStdin, "app-secret-stdin", false, "read the app secret from stdin instead of prompting")
	f.StringVar(&flags.allowFrom, "allow-from", "", "comma-separated open_id allowlist for inbound messages")
	f.BoolVar(&flags.nonInteract, "non-interactive", false, "never prompt; fail when a required value is missing")
	f.BoolVar(&flags.fromCatalog, "from-catalog", true, "resolve agent commands from the ACP registry catalog")
	f.StringVar(&flags.tenantBrand, "tenant-brand", "", "confirmation page edition: feishu (China) or lark (international); defaults to --platform")
	f.BoolVar(&flags.noAddons, "no-addons", false, "do not prefill scopes/events on the registration page; configure them manually")
	return cmd
}

func runSetup(cmd *cobra.Command, flags *setupFlags) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	paths := rootEnv.paths
	if err := paths.EnsureDirs(); err != nil {
		return err
	}
	state, err := setup.LoadState(paths)
	if err != nil {
		return err
	}
	store, err := rootEnv.store()
	if err != nil {
		return err
	}

	if state.PendingPlatform {
		fmt.Fprintf(out, "A previous registration attempt (%s) was interrupted.\n", displayOr(state.PendingAppID, "no app id recorded"))
		fmt.Fprintf(out, "Check the developer console first: if an app was created, use it with --app-id instead of creating another.\n\n")
	}

	// An install that already has a valid config must not be silently rewritten.
	cfg, loadErr := config.Load(paths.ConfigFile)
	switch {
	case loadErr == nil:
		fmt.Fprintf(out, "Found existing configuration at %s (default agent: %s).\n", paths.ConfigFile, cfg.Runtime.DefaultAgent)
	case errors.Is(loadErr, config.ErrNotConfigured):
		cfg = config.Default()
	default:
		return fmt.Errorf("existing configuration is invalid, refusing to overwrite: %w", loadErr)
	}

	// Step 1: agents.
	if err := stepAgents(ctx, out, &cfg, flags); err != nil {
		return err
	}
	if err := state.MarkDone(paths, setup.StepAgents); err != nil {
		return err
	}

	// Step 2: platform application and credential storage.
	if err := stepPlatform(ctx, out, &cfg, flags, store, &state); err != nil {
		return err
	}
	if err := state.MarkDone(paths, setup.StepPlatform); err != nil {
		return err
	}

	// Persist config only after the credentials are readable, so a config file
	// never refers to a credential that does not exist.
	if err := config.Save(paths.ConfigFile, cfg); err != nil {
		return err
	}
	fmt.Fprintf(out, "Wrote %s (no secrets in the file; values are in the credential store).\n", paths.ConfigFile)
	tryAutoBindApplicationOwner(ctx, out, cfg, credentials.NewResolver(store, os.LookupEnv))
	state, err = setup.LoadState(paths)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "\nNext steps:\n")
	fmt.Fprintf(out, "  agentdock run                 # foreground, verify the connection\n")
	target := flags.platformKind
	if target == "" {
		target = cfg.Platform.Kind
	}
	fmt.Fprintf(out, "  agentdock verify --platform %s  # verify the selected platform\n", target)
	fmt.Fprintf(out, "  agentdock service install     # run in the background\n")
	if len(cfg.Connections()) > 1 {
		fmt.Fprintln(out, "  agentdock service restart     # reload all configured platforms if already installed")
	}
	if next := state.NextStep(); next != "" {
		fmt.Fprintf(out, "\nRemaining setup steps: %s\n", strings.Join(remainingSteps(state), ", "))
	}
	return nil
}

func remainingSteps(state setup.State) []string {
	var out []string
	for _, step := range setup.Order {
		if !state.Done(step) {
			out = append(out, string(step))
		}
	}
	return out
}

func stepAgents(ctx context.Context, out io.Writer, cfg *config.Config, flags *setupFlags) error {
	ids := flags.agentIDs
	if len(ids) == 0 && len(cfg.Agents) > 0 {
		fmt.Fprintf(out, "Keeping %d configured agent(s), default %s.\n", len(cfg.Agents), cfg.Runtime.DefaultAgent)
		return nil
	}
	if len(ids) == 0 {
		ids = []string{"claude-acp", "codex-acp"}
		if flags.nonInteract {
			return fmt.Errorf("--agent is required with --non-interactive")
		}
		fmt.Fprintf(out, "No --agent given; defaulting to %s.\n", strings.Join(ids, ", "))
	}

	var index catalog.Index
	if flags.fromCatalog {
		src, err := rootEnv.catalogSource()
		if err != nil {
			return err
		}
		idx, err := src.Fetch(ctx)
		if err != nil {
			warnf("could not load the ACP catalog (%v); using built-in adapter commands", err)
		} else {
			index = idx
		}
	}

	agents := make([]config.Agent, 0, len(ids))
	for _, id := range ids {
		agent, err := resolveAgent(index, id)
		if err != nil {
			return err
		}
		agents = append(agents, agent)
	}
	cfg.Agents = agents
	cfg.Runtime.DefaultAgent = agents[0].ID
	fmt.Fprintf(out, "Agents: %s (default %s)\n", strings.Join(ids, ", "), cfg.Runtime.DefaultAgent)

	// Detect now so a typo or a missing CLI surfaces here rather than at first
	// message. Detection cannot prove a login, so it only warns.
	infos := agent.Detect(ctx, *cfg, 10*time.Second)
	usable := 0
	for _, info := range infos {
		if info.State == agent.StateReady || info.State == agent.StateUnknown {
			usable++
		}
		line := fmt.Sprintf("  %-8s %s", info.State, info.ID)
		if info.Path != "" {
			line += " (" + info.Path + ")"
		}
		if info.Version != "" {
			line += " " + info.Version
		}
		if info.Detail != "" {
			line += " — " + info.Detail
		}
		fmt.Fprintln(out, line)
	}
	if usable == 0 {
		return fmt.Errorf("%w: none of %s resolved; install one or pass a different --agent", agent.ErrNoUsableAgent, strings.Join(ids, ", "))
	}
	loginSteps := make([]string, 0, len(infos))
	for _, info := range infos {
		loginSteps = append(loginSteps, fmt.Sprintf("  %s: %s", info.ID, loginHint(info.ID)))
	}
	fmt.Fprintf(out, "Log in to each agent in this terminal before using it:\n%s\n", strings.Join(loginSteps, "\n"))
	return nil
}

// loginHint names the upstream CLI login command without running it: logging in
// is an interactive, user-owned step, and copying its credentials is not allowed.
func loginHint(id string) string {
	switch id {
	case "claude-acp":
		return "claude /login (Claude Code CLI)"
	case "codex-acp":
		return "codex login (Codex CLI)"
	case "kimi":
		return "kimi login"
	case "opencode":
		return "opencode auth login"
	default:
		return "follow the adapter's own login flow"
	}
}

// builtinAgents are fallbacks used when the catalog cannot be reached. Versions
// are pinned so a restart cannot silently change behaviour.
var builtinAgents = map[string][]string{
	"claude-acp": {"npx", "-y", "@agentclientprotocol/claude-agent-acp@0.101.0"},
	"codex-acp":  {"npx", "-y", "@agentclientprotocol/codex-acp@2.5.0"},
	"kimi":       {"kimi", "--acp"},
	"opencode":   {"opencode", "acp"},
}

func resolveAgent(index catalog.Index, id string) (config.Agent, error) {
	if entry, ok := index.Lookup(id); ok {
		argv, kind, err := entry.Command()
		if err != nil {
			return config.Agent{}, fmt.Errorf("agent %q: %w", id, err)
		}
		if warning := entry.LicenseWarning(); warning != "" {
			warnf("agent %s: %s", id, warning)
		}
		if npm := entry.Distribution.NPM; npm != nil && kind == "npm" && strings.Contains(npm.Package, "latest") {
			warnf("agent %s is distributed as @latest; pin a version for reproducible restarts", id)
		}
		return config.Agent{ID: id, Command: argv}, nil
	}
	if argv, ok := builtinAgents[id]; ok {
		return config.Agent{ID: id, Command: argv}, nil
	}
	return config.Agent{}, fmt.Errorf("unknown agent %q: not in the ACP catalog and no built-in definition", id)
}

func stepPlatform(ctx context.Context, out io.Writer, cfg *config.Config, flags *setupFlags, store credentials.Store, state *setup.State) error {
	kind := flags.platformKind
	if kind == "" {
		kind = cfg.Platform.Kind
	}
	if kind == "" {
		kind = "lark"
	}
	candidate, exists := cfg.Connection(kind)
	if !exists {
		candidate = config.Platform{Kind: kind}
	}
	if flags.authPlugin != "" {
		candidate.AuthPlugin = flags.authPlugin
	}
	auth, err := assembly.Open[platform.Authenticator](ctx, candidate.LoginKind(), nil)
	if err != nil {
		return err
	}
	result, err := auth.Login(ctx, platform.LoginRequest{
		Out: out, ExistingID: flags.appID, NonInteractive: flags.nonInteract,
		ReadSecret: func() (string, error) { return readAppSecret(out, flags) },
		ReadCode:   func() (string, error) { return promptSecret(out, "Enter the verification code shown on your phone: ") },
		Options:    map[string]string{"tenantBrand": flags.tenantBrand, "noAddons": fmt.Sprint(flags.noAddons)},
		BeforeRemote: func() error {
			state.PendingPlatform = true
			state.PendingAppID = ""
			return setup.Save(rootEnv.paths, *state)
		},
	})
	if err != nil {
		return err
	}
	if previousRef := candidate.References()["id"]; previousRef != "" {
		previousID, err := credentials.NewResolver(store, os.LookupEnv).Resolve(ctx, previousRef)
		if err != nil {
			return fmt.Errorf("cannot verify existing platform identity; restore credential access or use a separate --home")
		}
		if result.Credentials["id"] != previousID {
			return fmt.Errorf("login selected a different platform account; use a separate --home to preserve the current owner and sessions")
		}
	}
	saved, err := platform.SaveLogin(ctx, store, cfg.InstallID, kind, result)
	if err != nil {
		return err
	}
	candidate.Credentials = saved.References
	candidate.Settings = saved.Settings
	candidate.AppIDRef = ""
	candidate.AppSecretRef = ""
	candidate.AllowFrom = flags.allowFrom
	cfg.UpsertPlatform(candidate)
	state.PendingPlatform = false
	state.PendingAppID = ""
	if err := setup.Save(rootEnv.paths, *state); err != nil {
		return err
	}
	fmt.Fprintln(out, "Platform login saved and verified (configuration contains references only).")
	return nil
}

func displayOr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

// readAppSecret obtains the app secret without ever accepting it from argv.
func readAppSecret(out io.Writer, flags *setupFlags) (string, error) {
	if flags.secretStdin || !term.IsTerminal(int(os.Stdin.Fd())) {
		secret, err := promptSecret(out, "")
		if err != nil {
			return "", err
		}
		return secret, nil
	}
	return promptSecret(out, "Paste the app secret (input hidden): ")
}

// promptSecret reads a secret without echoing it when stdin is a terminal.
func promptSecret(out io.Writer, prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Fprint(out, prompt)
		raw, err := term.ReadPassword(fd)
		fmt.Fprintln(out)
		if err != nil {
			return "", fmt.Errorf("read secret: %w", err)
		}
		return strings.TrimSpace(string(raw)), nil
	}
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read secret from stdin: %w", err)
	}
	return strings.TrimSpace(line), nil
}
