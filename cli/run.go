package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/lengzhao/AgentDock/credentials"
	"github.com/lengzhao/AgentDock/lock"
	"github.com/lengzhao/AgentDock/runtime"
	"github.com/lengzhao/AgentDock/state"
)

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the bot in the foreground",
		Long: `Run the bot in the foreground.

This is the same process the background service runs; the only difference is who
supervises it. A single-instance lock prevents a foreground run and the service
from consuming the same installation at once.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runForeground(cmd)
		},
	}
	return cmd
}

func runForeground(cmd *cobra.Command) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	paths := rootEnv.paths
	if err := paths.EnsureDirs(); err != nil {
		return err
	}
	cfg, err := rootEnv.loadConfig()
	if err != nil {
		return err
	}

	// Single instance per installation (SRV-03).
	release, err := lock.Acquire(filepath.Join(paths.RunDir, "agent.lock"))
	if err != nil {
		return err
	}
	defer release()

	// Reconcile the operational record before serving traffic: a task left in
	// accepted/running by a previous process is reported as interrupted, never
	// retried (restart honesty).
	stateStore, err := state.Open(filepath.Join(paths.StateDir, "state.db"))
	if err != nil {
		return err
	}
	defer stateStore.Close()
	if n, err := stateStore.MarkInterrupted(ctx, ""); err != nil {
		warnf("could not reconcile interrupted tasks: %v", err)
	} else if n > 0 {
		slog.Warn("marked tasks interrupted", "count", n)
	}
	if n, err := stateStore.Purge(ctx, time.Now().Add(-30*24*time.Hour)); err != nil {
		warnf("could not purge old tasks: %v", err)
	} else if n > 0 {
		slog.Info("purged old task records", "count", n)
	}

	store, err := rootEnv.store()
	if err != nil {
		return err
	}
	resolver := credentials.NewResolver(store, os.LookupEnv)
	tryAutoBindApplicationOwner(ctx, cmd.OutOrStdout(), cfg, resolver)
	layout := runtime.Layout{Home: paths.Home, InstanceDir: paths.InstanceDir, StateDir: paths.StateDir}

	runner, result, err := runtime.Build(ctx, cfg, layout, resolver)
	if err != nil {
		return err
	}
	slog.Info("agentdock starting",
		"platform", cfg.Platform.Kind,
		"defaultAgent", cfg.Runtime.DefaultAgent,
		"agents", len(cfg.Agents))

	runErr := runner.Run(ctx, result)
	if ctx.Err() != nil {
		slog.Info("shutting down", "reason", ctx.Err())
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := runner.Stop(stopCtx); err != nil {
		warnf("runner stop: %v", err)
	}
	if runErr != nil && ctx.Err() == nil {
		return fmt.Errorf("runner exited: %w", runErr)
	}
	return nil
}
