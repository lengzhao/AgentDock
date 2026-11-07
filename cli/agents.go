package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/lengzhao/AgentDock/agent"
)

func newAgentsCmd() *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "Detect the configured agent adapters",
		Long: `Detect each configured adapter: whether it resolves, and what version it reports.

"ready" means the binary ran and answered a version probe. It does not mean the
agent is logged in — only ` + "`agentdock verify`" + ` proves the full path through a
real message.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := rootEnv.loadConfig()
			if err != nil {
				return err
			}
			infos := agent.Detect(cmd.Context(), cfg, timeout)
			out := cmd.OutOrStdout()
			for _, info := range infos {
				fmt.Fprintf(out, "%-6s %-10s %s\n", info.State, info.ID, joinArgs(info.Command))
				if info.Path != "" {
					fmt.Fprintf(out, "       path: %s\n", info.Path)
				}
				if info.Version != "" {
					fmt.Fprintf(out, "       version: %s\n", info.Version)
				}
				if info.Detail != "" {
					fmt.Fprintf(out, "       detail: %s\n", info.Detail)
				}
			}
			for _, info := range infos {
				if info.ID == cfg.Runtime.DefaultAgent {
					if info.State == agent.StateMissing || info.State == agent.StateBroken {
						return WithExitCode(1, fmt.Errorf("default agent %q is %s", info.ID, info.State))
					}
				}
			}
			return nil
		},
	}
	cmd.AddCommand(newAgentsAddCmd())
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Second, "per-agent detection timeout")
	return cmd
}
