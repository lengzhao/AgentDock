// Package cli wires the command tree.
package cli

import (
	"context"
	"errors"
	"fmt"
	"github.com/lengzhao/AgentDock/agent/presets"
	"github.com/lengzhao/AgentDock/assembly"
	"github.com/lengzhao/AgentDock/plugin"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/lengzhao/AgentDock/buildinfo"
	"github.com/lengzhao/AgentDock/config"
	"github.com/lengzhao/AgentDock/credentials"
)

// env holds values resolved once in PersistentPreRun and shared by subcommands.
type env struct {
	accessPlatform string
	home           string
	paths          config.Paths
	// homeOverride is --home.
	homeOverride string
	// storeOverride forces the in-memory credential store (tests / dry runs).
	storeOverride string
	serviceKind   string
	catalogKind   string
	verbose       bool
}

var rootEnv = &env{}

// Execute runs the command tree.
func Execute() error {
	return newRootCmd().Execute()
}

// ExitCode maps an error onto a process exit code.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ec exitCoder
	if errors.As(err, &ec) {
		return ec.ExitCode()
	}
	return 1
}

type exitCoder interface{ ExitCode() int }

type codedError struct {
	code int
	err  error
}

func (e codedError) Error() string { return e.err.Error() }
func (e codedError) Unwrap() error { return e.err }
func (e codedError) ExitCode() int { return e.code }

// WithExitCode wraps err so the process exits with code.
func WithExitCode(code int, err error) error { return codedError{code: code, err: err} }

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "agentdock",
		Short:         "Run AI agents as always-on assistants on your messaging platform",
		Version:       buildinfo.String(),
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			home := rootEnv.homeOverride
			if home == "" {
				h, err := config.Home()
				if err != nil {
					return err
				}
				home = h
			}
			abs, err := filepath.Abs(home)
			if err != nil {
				return err
			}
			rootEnv.home = abs
			rootEnv.paths = config.ResolvePaths(abs)
			if err := presets.EnsureUserFile(rootEnv.paths.AgentPresetsFile); err != nil {
				return fmt.Errorf("agent presets: %w", err)
			}
			if err := presets.LoadFile(rootEnv.paths.AgentPresetsFile); err != nil {
				return fmt.Errorf("%s: %w", rootEnv.paths.AgentPresetsFile, err)
			}
			configureLogging(rootEnv.verbose)
			return nil
		},
	}
	cmd.PersistentFlags().StringVar(&rootEnv.homeOverride, "home", "", "AgentDock home directory (default $AGENTDOCK_HOME or ~/.agentdock)")
	cmd.PersistentFlags().StringVar(&rootEnv.storeOverride, "credential-store", "", "credential plugin kind (keyring, memory, or registered kind)")
	cmd.PersistentFlags().BoolVarP(&rootEnv.verbose, "verbose", "v", false, "verbose output")

	cmd.PersistentFlags().StringVar(&rootEnv.serviceKind, "service-plugin", plugin.KindServiceLaunchd, "registered service plugin kind")
	cmd.PersistentFlags().StringVar(&rootEnv.catalogKind, "catalog-plugin", plugin.KindCatalogACPRegistry, "registered catalog plugin kind")
	cmd.AddCommand(
		newSetupCmd(),
		newRunCmd(),
		newServiceCmd(),
		newStatusCmd(),
		newDoctorCmd(),
		newVerifyCmd(),
		newCatalogCmd(),
		newAccessCmd(),
		newAgentsCmd(),
		newTasksCmd(),
		newConfigCmd(),
		newVersionCmd(),
	)
	return cmd
}

// store returns the credential store for this invocation.
func (e *env) store() (credentials.Store, error) {
	kind := e.storeOverride
	switch kind {
	case "", "keyring":
		kind = plugin.KindCredentialsKeyring
	case "memory":
		kind = plugin.KindCredentialsMemory
	}
	var options map[string]any
	if kind == plugin.KindCredentialsKeyring {
		options = map[string]any{"servicePrefix": credentialServicePrefix}
	}
	return assembly.Open[credentials.Store](context.Background(), kind, options)
}

// credentialServicePrefix namespaces every AgentDock credential entry.
const credentialServicePrefix = "io.agentdock"

// loadConfig reads config.yaml.
func (e *env) loadConfig() (config.Config, error) {
	return config.Load(e.paths.ConfigFile)
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), buildinfo.String())
			return nil
		},
	}
}

// configureLogging sets the process-wide slog level. The service redirects
// stderr into logs/service.err.log, so text output is the right handler here.
func configureLogging(verbose bool) {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
}

func warnf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "warning: "+format+"\n", args...)
}
