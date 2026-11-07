package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/lengzhao/AgentDock/state"
)

func newTasksCmd() *cobra.Command {
	var limit int
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "tasks",
		Short: "Show recently accepted tasks and their terminal states",
		Long: `Show the operational record: which inbound messages were accepted, what
state they reached, and whether their reply was delivered.

This is deliberately not the conversation history; it exists so a restart,
a cancelled turn, or an unknown send outcome is visible instead of guessed.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := state.Open(filepath.Join(rootEnv.paths.StateDir, "state.db"))
			if err != nil {
				return err
			}
			defer store.Close()
			tasks, err := store.List(cmd.Context(), limit)
			if err != nil {
				return err
			}
			for i := range tasks {
				deliveries, err := store.Deliveries(cmd.Context(), tasks[i].Key)
				if err != nil {
					return err
				}
				tasks[i].Deliveries = deliveries
			}
			out := cmd.OutOrStdout()
			if len(tasks) == 0 {
				fmt.Fprintln(out, "no tasks recorded")
				return nil
			}
			fmt.Fprintf(out, "%-10s %-12s %-22s %s\n", "STATUS", "PLATFORM", "UPDATED", "KEY")
			for _, t := range tasks {
				fmt.Fprintf(out, "%-10s %-12s %-22s %s\n", t.Status, t.Platform,
					t.UpdatedAt.Local().Format("2006-01-02 15:04:05"), t.Key)
				for _, delivery := range t.Deliveries {
					fmt.Fprintf(out, "           delivery: %s (attempts=%d) %s\n", delivery.Status, delivery.Attempts, delivery.Detail)
				}
				if t.Detail != "" {
					fmt.Fprintf(out, "           %s\n", t.Detail)
				}
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum number of tasks to show")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print tasks as JSON")
	return cmd
}
