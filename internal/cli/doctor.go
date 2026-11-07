package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lengzhao/AgentDock/internal/doctor"
)

func newDoctorCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose configuration, credentials, agents and the service",
		Long: `Diagnose each layer independently: config, credential store and readability,
agent executables, service state, and the last end-to-end verification.

Exit codes: 0 all ok, 2 warnings, 1 failure. Doctor never calls a paid model.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			opt := doctor.Options{Paths: rootEnv.paths}

			cfg, err := rootEnv.loadConfig()
			if err != nil {
				opt.ConfigErr = err
			} else {
				opt.Config = &cfg
			}
			store, serr := rootEnv.store()
			if serr != nil {
				opt.ConfigErr = serr
			} else {
				opt.Store = store
			}
			opt.Service, _ = newLaunchd()

			rep := doctor.Run(ctx, opt)
			if jsonOut {
				return writeJSON(cmd, rep)
			}
			out := cmd.OutOrStdout()
			for _, c := range rep.Checks {
				fmt.Fprintf(out, "%-9s %-24s %s\n", c.Level, c.Name, c.Detail)
			}
			fmt.Fprintf(out, "\noverall: %s\n", rep.Worst())
			if rep.Worst() == doctor.LevelFail || rep.Worst() == doctor.LevelWarn {
				return WithExitCode(rep.ExitCode(), fmt.Errorf("doctor found %s issues", rep.Worst()))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print the report as JSON")
	return cmd
}

func writeJSON(cmd *cobra.Command, v any) error {
	enc := jsonEncoder(cmd.OutOrStdout())
	return enc.Encode(v)
}
