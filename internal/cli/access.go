package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/lengzhao/AgentDock/internal/access"
	"github.com/lengzhao/AgentDock/internal/credentials"
	"github.com/lengzhao/AgentDock/internal/setup"
)

func newAccessCmd() *cobra.Command {
	var auto bool
	bind := &cobra.Command{
		Use:   "bind",
		Short: "Bind the application owner automatically or print a manual challenge",
		Long:  "Use --auto to query and bind the application's owner. Without --auto, print a single-use challenge valid for 15 minutes; send it from your own account to bind that account.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if auto {
				cfg, err := rootEnv.loadConfig()
				if err != nil {
					return err
				}
				store, err := rootEnv.store()
				if err != nil {
					return err
				}
				return autoBindApplicationOwner(cmd.Context(), cmd.OutOrStdout(), cfg, credentials.NewResolver(store, os.LookupEnv))
			}
			store := access.NewStore(accessFile())
			challenge, err := store.StartBinding(15 * time.Minute)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Send this token to the bot within 15 minutes:\n\n    %s\n\n", challenge)
			fmt.Fprintln(cmd.OutOrStdout(), "The account that sends it becomes the owner. The token is single-use.")
			return nil
		},
	}
	bind.Flags().BoolVar(&auto, "auto", false, "query the configured application's owner and bind it if unbound")
	cmd := &cobra.Command{
		Use:   "access",
		Short: "Manage the bot owner and allowed users",
	}
	cmd.AddCommand(
		bind,
		&cobra.Command{
			Use:   "status",
			Short: "Show the bound owner",
			RunE: func(cmd *cobra.Command, args []string) error {
				store := access.NewStore(accessFile())
				st, err := store.Load()
				if err != nil {
					return err
				}
				out := cmd.OutOrStdout()
				switch {
				case st.OwnerOpenID == "" && st.Challenge != "":
					fmt.Fprintln(out, "owner: (unbound) — waiting for the pending challenge to be sent")
				case st.OwnerOpenID == "":
					fmt.Fprintln(out, "owner: (unbound) — run `agentdock access bind`")
				default:
					fmt.Fprintf(out, "owner: %s\n", st.OwnerOpenID)
					if state, err := setup.LoadState(rootEnv.paths); err == nil {
						_ = state.MarkDone(rootEnv.paths, setup.StepOwner)
					}
				}
				if st.Challenge != "" {
					fmt.Fprintf(out, "pending binding: %s until %s\n", st.Challenge, st.ChallengeDeadline.Format("2006-01-02 15:04:05Z07:00"))
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "allow <open_id>",
			Short: "Add an open_id to the additional allowlist",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				store := access.NewStore(accessFile())
				_, err := store.Update(func(st *access.State) error {
					for _, part := range []string{st.AllowFrom} {
						if part == args[0] {
							return nil
						}
					}
					if st.AllowFrom == "" {
						st.AllowFrom = args[0]
					} else {
						st.AllowFrom += "," + args[0]
					}
					return nil
				})
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "allowed: %s\n", args[0])
				return nil
			},
		},
		&cobra.Command{
			Use:   "show",
			Short: "Print the raw access state",
			RunE: func(cmd *cobra.Command, args []string) error {
				raw, err := os.ReadFile(accessFile())
				if err != nil {
					if os.IsNotExist(err) {
						fmt.Fprintln(cmd.OutOrStdout(), "{}")
						return nil
					}
					return err
				}
				var st access.State
				if err := json.Unmarshal(raw, &st); err != nil {
					return err
				}
				return writeJSON(cmd, st)
			},
		},
	)
	return cmd
}

func accessFile() string {
	return filepath.Join(rootEnv.paths.StateDir, "access.json")
}
