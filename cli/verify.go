package cli

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/spf13/cobra"
)

// VerificationNonceEnv is set for the running bot so a test message can be
// recognized as the verification challenge rather than a user task.
const VerificationNonceEnv = "AGENTDOCK_VERIFY_NONCE"

func newVerifyCmd() *cobra.Command {
	var timeout time.Duration
	var selectedPlatform string
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Send a real end-to-end test message and record the result",
		Long: `Verify the full path: bot online, inbound message received, agent replied.

The command prints a one-time token and waits for that token to come back through
the bot. A WebSocket that connects but never delivers a message is not a pass
(LARK-03).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := rootEnv.loadConfig()
			if err != nil {
				return err
			}
			kind := selectedPlatform
			if kind == "" {
				kind = cfg.Platform.Kind
			}
			return runVerify(cmd.Context(), cmd.OutOrStdout(), rootEnv.paths, cfg, kind, timeout)
		},
	}
	cmd.Flags().StringVar(&selectedPlatform, "platform", "", "platform to verify (default: primary platform)")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "how long to wait for the reply")
	return cmd
}

func newNonce() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "AGENTDOCK-" + hex.EncodeToString(buf), nil
}
