package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/lengzhao/AgentDock/agent"
	"github.com/lengzhao/AgentDock/config"
	"github.com/lengzhao/AgentDock/credentials"
	"github.com/lengzhao/AgentDock/setup"
)

type onboardOpts struct {
	flags         *setupFlags
	useTUI        bool
	skipVerify    bool
	skipService   bool
	verifyTimeout time.Duration
}

func runOnboard(ctx context.Context, out io.Writer, opts onboardOpts) error {
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

	next := state.NextStep()
	if opts.useTUI {
		if err := onboardWelcome(string(next)); err != nil {
			return err
		}
	}

	cfg, loadErr := config.Load(paths.ConfigFile)
	switch {
	case loadErr == nil:
		fmt.Fprintf(out, "Using configuration at %s.\n", paths.ConfigFile)
	case errors.Is(loadErr, config.ErrNotConfigured):
		cfg = config.Default()
	default:
		return fmt.Errorf("existing configuration is invalid: %w", loadErr)
	}

	if state.PendingPlatform {
		fmt.Fprintf(out, "A previous registration attempt (%s) was interrupted.\n", displayOr(state.PendingAppID, "no app id recorded"))
		fmt.Fprintf(out, "Check the developer console first: if an app was created, use it with --app-id instead of creating another.\n\n")
	}

	// --- agents ---
	if !state.Done(setup.StepAgents) {
		if opts.useTUI {
			existing := agentIDsFromCfg(cfg)
			ids, err := onboardPickAgents(existing)
			if err != nil {
				return err
			}
			opts.flags.agentIDs = ids
		}
		if err := stepAgents(ctx, out, &cfg, opts.flags); err != nil {
			return err
		}
		if err := state.MarkDone(paths, setup.StepAgents); err != nil {
			return err
		}
		state, _ = setup.LoadState(paths)
	}

	// --- agent login (acknowledgement) ---
	if !state.Done(setup.StepAgentLogin) {
		infos := agent.Detect(ctx, cfg, 10*time.Second)
		var hints []string
		for _, info := range infos {
			hints = append(hints, fmt.Sprintf("• %s: %s", info.ID, agent.LoginHint(info.ID)))
		}
		hintBlock := strings.Join(hints, "\n")
		if opts.useTUI {
			if err := onboardConfirmAgentLogin(hintBlock); err != nil {
				return err
			}
		} else {
			fmt.Fprintf(out, "Log in to each agent before messaging:\n%s\n", hintBlock)
		}
		if err := state.MarkDone(paths, setup.StepAgentLogin); err != nil {
			return err
		}
		state, _ = setup.LoadState(paths)
	}

	// --- platform ---
	if !state.Done(setup.StepPlatform) {
		if opts.useTUI {
			kind, err := onboardPickPlatform(cfg.Platform.Kind)
			if err != nil {
				return err
			}
			opts.flags.platformKind = kind
		}
		if err := stepPlatform(ctx, out, &cfg, opts.flags, store, &state); err != nil {
			return err
		}
		if err := state.MarkDone(paths, setup.StepPlatform); err != nil {
			return err
		}
		if err := config.Save(paths.ConfigFile, cfg); err != nil {
			return err
		}
		fmt.Fprintf(out, "Wrote %s (credential references only).\n", paths.ConfigFile)
		state, _ = setup.LoadState(paths)
	} else if errors.Is(loadErr, config.ErrNotConfigured) {
		return fmt.Errorf("setup progress lists platform complete but config is missing; run agentdock doctor")
	}

	resolver := credentials.NewResolver(store, os.LookupEnv)
	if err := autoBindApplicationOwner(ctx, out, cfg, resolver); err != nil {
		return fmt.Errorf("owner binding: %w (try `agentdock access bind`)", err)
	}
	state, _ = setup.LoadState(paths)

	skipVerify := opts.skipVerify
	if !state.Done(setup.StepService) && !opts.skipService {
		if err := installAndStartService(ctx, out, paths, cfg); err != nil {
			return err
		}
		state, _ = setup.LoadState(paths)
	}

	if !state.Done(setup.StepVerify) && !skipVerify {
		if opts.useTUI {
			if err := onboardConfirmVerify(&skipVerify); err != nil {
				return err
			}
		}
		if !skipVerify {
			kind := opts.flags.platformKind
			if kind == "" {
				kind = cfg.Platform.Kind
			}
			if err := runVerify(ctx, out, paths, cfg, kind, opts.verifyTimeout); err != nil {
				return err
			}
		}
		state, _ = setup.LoadState(paths)
	}

	if next := state.NextStep(); next != "" {
		fmt.Fprintf(out, "\nSetup progress: remaining %s\n", strings.Join(remainingSteps(state), ", "))
		fmt.Fprintf(out, "Re-run `agentdock onboard` to continue.\n")
		return WithExitCode(2, fmt.Errorf("onboard incomplete"))
	}

	if opts.useTUI {
		if err := onboardDone(); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(out, "Onboard complete.")
	}
	return nil
}

func agentIDsFromCfg(cfg config.Config) []string {
	if len(cfg.Agents) == 0 {
		return nil
	}
	ids := make([]string, 0, len(cfg.Agents))
	for _, a := range cfg.Agents {
		ids = append(ids, a.ID)
	}
	return ids
}

func onboardUseTUI(flags *setupFlags) bool {
	if flags.nonInteract {
		return false
	}
	return term.IsTerminal(int(os.Stdout.Fd())) && term.IsTerminal(int(os.Stdin.Fd()))
}
