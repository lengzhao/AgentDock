package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/lengzhao/AgentDock/access"
	"github.com/lengzhao/AgentDock/assembly"
	"github.com/lengzhao/AgentDock/config"
	"github.com/lengzhao/AgentDock/credentials"
	"github.com/lengzhao/AgentDock/platform"
	"github.com/lengzhao/AgentDock/setup"
)

func autoBindApplicationOwner(ctx context.Context, out io.Writer, cfg config.Config, resolver *credentials.Resolver) error {
	var failures []error
	for _, connection := range cfg.Connections() {
		if rootEnv.accessPlatform != "" && connection.Kind != rootEnv.accessPlatform {
			continue
		}
		if err := bindPlatformOwner(ctx, out, cfg, connection, resolver); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", connection.Kind, err))
		}
	}
	return errors.Join(failures...)
}
func bindPlatformOwner(ctx context.Context, out io.Writer, cfg config.Config, connection config.Platform, resolver *credentials.Resolver) error {
	paths := rootEnv.paths.ForPlatform(connection.Kind, cfg.Platform.Kind)
	store := access.NewStore(filepath.Join(paths.StateDir, "access.json"))
	owner, err := store.Owner()
	if err != nil {
		return err
	}
	if owner != "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	values := map[string]string{}
	for role, ref := range connection.References() {
		value, err := resolver.Resolve(ctx, ref)
		if err != nil {
			return fmt.Errorf("resolve platform credential %s for owner lookup failed", role)
		}
		values[role] = value
	}
	auth, err := assembly.Open[platform.Authenticator](ctx, connection.LoginKind(), nil)
	if err != nil {
		return err
	}
	owner, err = auth.Owner(ctx, values, connection.Settings)
	if err != nil {
		return err
	}
	if owner == "" {
		return fmt.Errorf("platform login has no verified owner; use agentdock access bind")
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
	progress, err := setup.LoadState(paths)
	if err != nil {
		return err
	}
	return progress.MarkDone(paths, setup.StepOwner)
}

func tryAutoBindApplicationOwner(ctx context.Context, out io.Writer, cfg config.Config, resolver *credentials.Resolver) {
	if err := autoBindApplicationOwner(ctx, out, cfg, resolver); err != nil {
		fmt.Fprintf(out, "Automatic owner binding unavailable: %v. Use `agentdock access bind` for manual binding.\n", err)
	}
}
