package cli

import (
	"fmt"
	"time"

	"github.com/lengzhao/AgentDock/agent"
	"github.com/lengzhao/AgentDock/config"
	"github.com/spf13/cobra"
)

func newAgentsAddCmd() *cobra.Command {
	var makeDefault bool
	cmd := &cobra.Command{Use: "add <id>", Short: "Add an installed agent without changing platforms or other agents", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := rootEnv.loadConfig()
		if err != nil {
			return err
		}
		id := agent.NormalizeID(args[0])
		var added config.Agent
		foundExisting := false
		for _, existing := range cfg.Agents {
			if existing.ID == id {
				added = existing
				foundExisting = true
				break
			}
		}
		if !foundExisting {
			added, err = agent.Resolve(id)
			if err != nil {
				return err
			}
			probe := cfg
			probe.Agents = []config.Agent{added}
			infos := agent.Detect(cmd.Context(), probe, 10*time.Second)
			if len(infos) != 1 || infos[0].State == agent.StateMissing || infos[0].State == agent.StateBroken {
				return fmt.Errorf("agent %s is unavailable; install its CLI first, then retry agents add", added.ID)
			}
		}
		already := false
		_, err = config.Update(cmd.Context(), rootEnv.paths.ConfigFile, func(latest *config.Config) error {
			for _, existing := range latest.Agents {
				if existing.ID == added.ID {
					already = true
					if makeDefault {
						latest.Runtime.DefaultAgent = added.ID
					}
					return nil
				}
			}
			if foundExisting {
				return fmt.Errorf("agent %s was removed during update; retry the command", added.ID)
			}
			added.Cwd = latest.Runtime.WorkDir
			latest.Agents = append(latest.Agents, added)
			if makeDefault {
				latest.Runtime.DefaultAgent = added.ID
			}
			return nil
		})
		if err != nil {
			return err
		}
		if already {
			fmt.Fprintf(cmd.OutOrStdout(), "Agent %s is already configured.\n", added.ID)
			return nil
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Added agent %s. Login: %s. Restart the service to load it.\n", added.ID, agent.LoginHint(added.ID))
		return nil
	}}
	cmd.Flags().BoolVar(&makeDefault, "default", false, "make this the default agent")
	return cmd
}
