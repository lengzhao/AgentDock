package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/lengzhao/AgentDock/internal/config"
	"github.com/lengzhao/AgentDock/internal/service"
	"github.com/lengzhao/AgentDock/internal/setup"
)

func newServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Install and control the background service",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "install",
			Short: "Install and start the user-level service",
			RunE: func(cmd *cobra.Command, args []string) error {
				return withService(cmd, func(ctx context.Context, mgr *service.Launchd, cfg config.Config) error {
					spec, err := serviceSpec(cfg)
					if err != nil {
						return err
					}
					if err := mgr.Install(ctx, spec); err != nil {
						return err
					}
					if err := mgr.Start(ctx); err != nil {
						return err
					}
					st, err := service.WaitRunning(ctx, mgr, 20*time.Second)
					if err != nil {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "Service %s running (pid %d).\n", spec.Label, st.PID)
					fmt.Fprintf(cmd.OutOrStdout(), "Logs: %s\n", filepath.Join(spec.LogDir, "service.err.log"))
					return markStep(cmd, setup.StepService)
				})
			},
		},
		&cobra.Command{
			Use:   "uninstall",
			Short: "Stop and remove the service definition (keeps config and credentials)",
			RunE: func(cmd *cobra.Command, args []string) error {
				mgr, err := newLaunchd()
				if err != nil {
					return err
				}
				if err := mgr.Uninstall(cmd.Context()); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Service removed. Configuration and credentials were left in place.")
				return nil
			},
		},
		&cobra.Command{
			Use:   "start",
			Short: "Start the installed service",
			RunE:  simpleServiceAction("start", (*service.Launchd).Start),
		},
		&cobra.Command{
			Use:   "stop",
			Short: "Stop the service without removing it",
			RunE:  simpleServiceAction("stop", (*service.Launchd).Stop),
		},
		&cobra.Command{
			Use:   "restart",
			Short: "Restart the service",
			RunE:  simpleServiceAction("restart", (*service.Launchd).Restart),
		},
		&cobra.Command{
			Use:   "status",
			Short: "Show the service state",
			RunE: func(cmd *cobra.Command, args []string) error {
				mgr, err := newLaunchd()
				if err != nil {
					return err
				}
				st, err := mgr.Status(cmd.Context())
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "state: %s\n", st.State)
				if st.PID != 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "pid: %d\n", st.PID)
				}
				if st.Detail != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "detail: %s\n", st.Detail)
				}
				return nil
			},
		},
	)
	return cmd
}

func simpleServiceAction(name string, action func(*service.Launchd, context.Context) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		mgr, err := newLaunchd()
		if err != nil {
			return err
		}
		if err := action(mgr, cmd.Context()); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Service %s: %s.\n", name, mgr.Label)
		return nil
	}
}

func newLaunchd() (*service.Launchd, error) {
	label := "io.agentdock.agent"
	// A configured install may override the label; an unconfigured one still gets
	// a usable manager so `service status` can report "not installed".
	if cfg, err := rootEnv.loadConfig(); err == nil && cfg.Service.Label != "" {
		label = cfg.Service.Label
	}
	return service.NewLaunchd(label)
}

func withService(cmd *cobra.Command, fn func(context.Context, *service.Launchd, config.Config) error) error {
	cfg, err := rootEnv.loadConfig()
	if err != nil {
		return err
	}
	mgr, err := service.NewLaunchd(cfg.Service.Label)
	if err != nil {
		return err
	}
	return fn(cmd.Context(), mgr, cfg)
}

func serviceSpec(cfg config.Config) (service.Spec, error) {
	exe, err := os.Executable()
	if err != nil {
		return service.Spec{}, fmt.Errorf("resolve executable path: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return service.Spec{}, fmt.Errorf("resolve executable path: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return service.Spec{}, err
	}
	return service.Spec{
		Label:           cfg.Service.Label,
		Executable:      userLoginShell(),
		Args:            loginShellArgs([]string{exe, "run", "--home", rootEnv.paths.Home}),
		WorkingDir:      rootEnv.paths.Home,
		LogDir:          rootEnv.paths.LogDir,
		HOME:            home,
		KeepAlive:       cfg.Service.KeepAlive,
		ThrottleSeconds: cfg.Service.ThrottleSeconds,
	}, nil
}

func markStep(cmd *cobra.Command, step setup.Step) error {
	state, err := setup.LoadState(rootEnv.paths)
	if err != nil {
		return err
	}
	return state.MarkDone(rootEnv.paths, step)
}
