package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/lengzhao/AgentDock/access"
	"github.com/lengzhao/AgentDock/config"
	"github.com/lengzhao/AgentDock/credentials"
	"github.com/lengzhao/AgentDock/platform/lark"
	"github.com/lengzhao/AgentDock/setup"
)

func autoBindApplicationOwner(ctx context.Context, out io.Writer, cfg config.Config, resolver *credentials.Resolver) error {
	store := access.NewStore(accessFile())
	owner, err := store.Owner()
	if err != nil {
		return err
	}
	if owner != "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	appID, err := resolver.Resolve(ctx, cfg.Platform.AppIDRef)
	if err != nil {
		return fmt.Errorf("resolve app ID for owner lookup: %w", err)
	}
	secret, err := resolver.Resolve(ctx, cfg.Platform.AppSecretRef)
	if err != nil {
		return fmt.Errorf("resolve app secret for owner lookup: %w", err)
	}
	owner, err = lark.LookupApplicationOwner(ctx, cfg.Platform.Kind, appID, secret)
	if err != nil {
		return err
	}
	st, err := store.Update(func(st *access.State) error {
		// A binding completed during the lookup takes precedence.
		if st.OwnerOpenID == "" {
			st.OwnerOpenID = owner
			st.Challenge = ""
			st.ChallengeDeadline = time.Time{}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("save application owner binding: %w", err)
	}
	fmt.Fprintf(out, "Owner bound: %s (verified application owner: %s).\n", st.OwnerOpenID, owner)
	progress, err := setup.LoadState(rootEnv.paths)
	if err != nil {
		return err
	}
	return progress.MarkDone(rootEnv.paths, setup.StepOwner)
}

func tryAutoBindApplicationOwner(ctx context.Context, out io.Writer, cfg config.Config, resolver *credentials.Resolver) {
	if err := autoBindApplicationOwner(ctx, out, cfg, resolver); err != nil {
		fmt.Fprintf(out, "Automatic owner binding unavailable: %v. Use `agentdock access bind` for manual binding.\n", err)
	}
}
