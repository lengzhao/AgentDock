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

	"github.com/lengzhao/AgentDock/internal/agent"
	"github.com/lengzhao/AgentDock/internal/catalog"
	"github.com/lengzhao/AgentDock/internal/config"
	"github.com/lengzhao/AgentDock/internal/credentials"
	"github.com/lengzhao/AgentDock/internal/platform/lark"
	"github.com/lengzhao/AgentDock/internal/setup"
)

type setupFlags struct {
	agentIDs     []string
	platformKind string
	appID        string
	secretStdin  bool
	allowFrom    string
	nonInteract  bool
	fromCatalog  bool
	noAddons     bool
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
	f.StringVar(&flags.platformKind, "platform", "lark", "platform: lark (international) or feishu (China)")
	f.StringVar(&flags.appID, "app-id", "", "existing platform app id (skips catalog selection for the platform)")
	// A boolean, not a string: a string flag would let the secret be typed into
	// argv, where `ps` exposes it (SEC-02).
	f.BoolVar(&flags.secretStdin, "app-secret-stdin", false, "read the app secret from stdin instead of prompting")
	f.StringVar(&flags.allowFrom, "allow-from", "", "comma-separated open_id allowlist for inbound messages")
	f.BoolVar(&flags.nonInteract, "non-interactive", false, "never prompt; fail when a required value is missing")
	f.BoolVar(&flags.fromCatalog, "from-catalog", true, "resolve agent commands from the ACP registry catalog")
	f.BoolVar(&flags.noAddons, "no-addons", false, "do not prefill scopes/events on the registration page (use when the page reports an expired link)")
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

	fmt.Fprintf(out, "\nNext steps:\n")
	fmt.Fprintf(out, "  agentdock run                 # foreground, verify the connection\n")
	fmt.Fprintf(out, "  agentdock verify              # send a real end-to-end test message\n")
	fmt.Fprintf(out, "  agentdock service install     # run in the background\n")
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
	if len(ids) == 0 {
		ids = []string{"claude-acp", "codex-acp"}
		if flags.nonInteract {
			return fmt.Errorf("--agent is required with --non-interactive")
		}
		fmt.Fprintf(out, "No --agent given; defaulting to %s.\n", strings.Join(ids, ", "))
	}

	var index catalog.Index
	if flags.fromCatalog {
		src := catalog.NewRegistrySource("", rootEnv.paths.CacheDir)
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
	switch flags.platformKind {
	case "lark", "feishu":
		cfg.Platform.Kind = flags.platformKind
	default:
		return fmt.Errorf("--platform must be lark or feishu, got %q", flags.platformKind)
	}
	cfg.Platform.AllowFrom = flags.allowFrom

	if flags.appID != "" {
		return useExistingApp(ctx, out, cfg, flags, store)
	}
	return registerApp(ctx, out, cfg, flags, store, state)
}

// useExistingApp stores credentials for an app the user already owns (LARK-02).
func useExistingApp(ctx context.Context, out io.Writer, cfg *config.Config, flags *setupFlags, store credentials.Store) error {
	appID := flags.appID
	appSecret, err := readAppSecret(out, flags)
	if err != nil {
		return err
	}
	if strings.TrimSpace(appSecret) == "" {
		return fmt.Errorf("app secret is empty")
	}
	if err := setup.StorePlatformCredentials(ctx, store, appID, appSecret); err != nil {
		return fmt.Errorf("store platform credentials: %w", err)
	}
	appIDRef, appSecretRef := setup.PlatformRefs()
	cfg.Platform.AppIDRef = appIDRef
	cfg.Platform.AppSecretRef = appSecretRef
	fmt.Fprintf(out, "Stored app credentials for %s in the credential store (%s) and verified read-back.\n",
		appID, store.Status(ctx).Backend)
	return nil
}

// registerApp runs the official scan-to-create flow (LARK-01).
//
// Registration is an external side effect: the state flag is set before the flow
// starts and only cleared after the credentials are safely stored, so an
// interrupted run tells the next one to check the console instead of creating a
// second application (SET-02).
func registerApp(ctx context.Context, out io.Writer, cfg *config.Config, flags *setupFlags, store credentials.Store, state *setup.State) error {
	if flags.nonInteract {
		return fmt.Errorf("--app-id is required with --non-interactive; scan registration needs a human to confirm")
	}
	fmt.Fprintf(out, "No --app-id given: starting %s scan registration.\n", displayOr(flags.platformKind, "lark"))
	state.PendingPlatform = true
	state.PendingAppID = ""
	if err := setup.Save(rootEnv.paths, *state); err != nil {
		return err
	}

	reg, err := lark.RegisterApp(ctx, lark.RegisterOptions{
		Kind:     cfg.Platform.Kind,
		Store:    store,
		Out:      out,
		NoAddons: flags.noAddons,
		AppName: func() string {
			return "AgentDock"
		}(),
	})
	if err != nil {
		// Leave PendingPlatform set: the user must reconcile before retrying.
		return err
	}

	appIDRef, appSecretRef := lark.Refs()
	cfg.Platform.AppIDRef = appIDRef
	cfg.Platform.AppSecretRef = appSecretRef
	state.PendingPlatform = false
	state.PendingAppID = ""
	if err := setup.Save(rootEnv.paths, *state); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nCreated app %s (secret %s) and stored both in the credential store.\n",
		reg.AppID, reg.SecretFingerprint)
	if flags.noAddons {
		fmt.Fprintf(out, "\nPermissions were NOT prefilled. In the developer console for %s, add:\n", reg.AppID)
		fmt.Fprintf(out, "  scopes: %s\n", strings.Join(lark.TenantScopes, ", "))
		fmt.Fprintf(out, "  events: %s\n", strings.Join(lark.TenantEvents, ", "))
		fmt.Fprintf(out, "\nThen enable the bot and use long-connection (长连接) event delivery.\n")
	}
	if reg.OwnerOpenID != "" {
		fmt.Fprintf(out, "You are %s; bind it as owner with `agentdock access bind`.\n", reg.OwnerOpenID)
	}
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
