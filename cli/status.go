package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/lengzhao/AgentDock/config"
	"github.com/lengzhao/AgentDock/runtime"
	"github.com/lengzhao/AgentDock/setup"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show configuration, setup progress and service state",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "home: %s\n", rootEnv.paths.Home)

			cfg, err := rootEnv.loadConfig()
			switch {
			case err == nil:
				fmt.Fprintf(out, "config: %s\n", rootEnv.paths.ConfigFile)
				for _, connection := range cfg.Connections() {
					fmt.Fprintf(out, "platform: %s (%s)\n", connection.Kind, connection.References()["id"])
				}
				for _, line := range runtime.Describe(cfg) {
					fmt.Fprintf(out, "agent: %s\n", line)
				}
			case os.IsNotExist(err) || err == config.ErrNotConfigured:
				fmt.Fprintf(out, "config: not configured (run `agentdock setup`)\n")
			default:
				fmt.Fprintf(out, "config: invalid: %v\n", err)
			}

			state, err := setup.LoadState(rootEnv.paths)
			if err != nil {
				fmt.Fprintf(out, "setup: %v\n", err)
			} else if next := state.NextStep(); next == "" {
				fmt.Fprintf(out, "setup: complete\n")
			} else {
				fmt.Fprintf(out, "setup: next step %q\n", next)
			}

			if mgr, err := newServiceManager(); err == nil {
				st, err := mgr.Status(cmd.Context())
				if err != nil {
					fmt.Fprintf(out, "service: %v\n", err)
				} else {
					line := string(st.State)
					if st.PID != 0 {
						line += fmt.Sprintf(" (pid %d)", st.PID)
					}
					if st.Detail != "" {
						line += " " + st.Detail
					}
					fmt.Fprintf(out, "service: %s\n", line)
				}
			}
			return nil
		},
	}
}
