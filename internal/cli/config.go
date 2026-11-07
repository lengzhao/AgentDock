package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/lengzhao/AgentDock/internal/config"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and validate the configuration",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "path",
			Short: "Print the config file path",
			RunE: func(cmd *cobra.Command, args []string) error {
				fmt.Fprintln(cmd.OutOrStdout(), rootEnv.paths.ConfigFile)
				return nil
			},
		},
		&cobra.Command{
			Use:   "show",
			Short: "Print the resolved configuration (references only, no secrets)",
			RunE: func(cmd *cobra.Command, args []string) error {
				raw, err := os.ReadFile(rootEnv.paths.ConfigFile)
				if err != nil {
					return err
				}
				_, err = cmd.OutOrStdout().Write(raw)
				return err
			},
		},
		&cobra.Command{
			Use:   "validate",
			Short: "Validate the configuration file",
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, err := config.Load(rootEnv.paths.ConfigFile)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "ok: %d agent(s), default %s, platform %s\n",
					len(cfg.Agents), cfg.Runtime.DefaultAgent, cfg.Platform.Kind)
				return nil
			},
		},
	)
	return cmd
}
