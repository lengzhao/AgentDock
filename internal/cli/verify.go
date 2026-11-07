package cli

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/lengzhao/AgentDock/internal/doctor"
	"github.com/lengzhao/AgentDock/internal/setup"
)

// VerificationNonceEnv is set for the running bot so a test message can be
// recognized as the verification challenge rather than a user task.
const VerificationNonceEnv = "AGENTDOCK_VERIFY_NONCE"

func newVerifyCmd() *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Send a real end-to-end test message and record the result",
		Long: `Verify the full path: bot online, inbound message received, agent replied.

The command prints a one-time token and waits for that token to come back through
the bot. A WebSocket that connects but never delivers a message is not a pass
(LARK-03).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			cfg, err := rootEnv.loadConfig()
			if err != nil {
				return err
			}
			nonce, err := newNonce()
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Send this token to the bot in a direct message:\n\n    %s\n\n", nonce)
			fmt.Fprintf(out, "Waiting up to %s for the reply…\n", timeout)
			// The bot marks success when it handles the token; this command waits for
			// that marker file rather than asserting success from a connection check.
			deadline := time.Now().Add(timeout)
			marker := doctor.VerifyMarkerFile(rootEnv.paths)
			for time.Now().Before(deadline) {
				select {
				case <-cmd.Context().Done():
					return cmd.Context().Err()
				case <-time.After(500 * time.Millisecond):
				}
				ok, err := doctor.ReadVerifyMarker(marker, nonce)
				if err != nil {
					return err
				}
				if ok {
					if err := doctor.RecordEndToEnd(rootEnv.paths, time.Now()); err != nil {
						return err
					}
					state, _ := setup.LoadState(rootEnv.paths)
					_ = state.MarkDone(rootEnv.paths, setup.StepVerify)
					fmt.Fprintln(out, "End-to-end verified: the bot received the token and replied.")
					return nil
				}
			}
			return WithExitCode(1, fmt.Errorf("no end-to-end confirmation within %s; the bot did not handle the token.\nCheck `agentdock doctor` and the service log. Platform: %s", timeout, cfg.Platform.Kind))
		},
	}
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
