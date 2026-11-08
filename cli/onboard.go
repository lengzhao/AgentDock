package cli

import (
	"time"

	"github.com/spf13/cobra"
)

func newOnboardCmd() *cobra.Command {
	flags := &setupFlags{}
	var skipVerify bool
	var skipService bool
	var verifyTimeout time.Duration
	cmd := &cobra.Command{
		Use:   "onboard",
		Short: "Guided setup through agents, platform, service and verification",
		Long: `Run the full setup flow in one place.

Progress is stored in setup.json; cancel and re-run to resume. With a TTY, forms
guide each step; without a TTY use the same flags as setup plus --non-interactive.

When complete, the background service is installed and (unless skipped) a live
message test has passed.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runOnboard(cmd.Context(), cmd.OutOrStdout(), onboardOpts{
				flags:         flags,
				useTUI:        onboardUseTUI(flags),
				skipVerify:    skipVerify,
				skipService:   skipService,
				verifyTimeout: verifyTimeout,
			})
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&flags.agentIDs, "agent", nil, "agent adapter ids (repeatable)")
	f.StringVar(&flags.platformKind, "platform", "", "platform: lark, feishu, weixin")
	f.StringVar(&flags.authPlugin, "platform-auth", "", "registered platform authentication plugin kind")
	f.StringVar(&flags.appID, "app-id", "", "existing platform app id")
	f.BoolVar(&flags.secretStdin, "app-secret-stdin", false, "read app secret from stdin")
	f.StringVar(&flags.allowFrom, "allow-from", "", "comma-separated open_id allowlist")
	f.BoolVar(&flags.nonInteract, "non-interactive", false, "no prompts; required flags must be set")
	f.StringVar(&flags.tenantBrand, "tenant-brand", "", "registration page edition: feishu or lark")
	f.BoolVar(&flags.noAddons, "no-addons", false, "do not prefill scopes on registration")
	f.BoolVar(&skipVerify, "skip-verify", false, "do not wait for the end-to-end message test")
	f.BoolVar(&skipService, "skip-service", false, "do not install the background service")
	f.DurationVar(&verifyTimeout, "verify-timeout", 5*time.Minute, "how long to wait during the message test")
	return cmd
}
