package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/lengzhao/AgentDock/config"
	"github.com/lengzhao/AgentDock/doctor"
	"github.com/lengzhao/AgentDock/setup"
)

func runVerify(ctx context.Context, out io.Writer, paths config.Paths, cfg config.Config, platformKind string, timeout time.Duration) error {
	connection, ok := cfg.Connection(platformKind)
	if !ok {
		return fmt.Errorf("platform %q is not configured", platformKind)
	}
	platPaths := paths.ForPlatform(connection.Kind, cfg.PrimaryKind)
	nonce, err := newNonce()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Send this token to the %s bot in a direct message:\n\n    %s\n\n", connection.Kind, nonce)
	fmt.Fprintf(out, "Waiting up to %s for the reply…\n", timeout)
	deadline := time.Now().Add(timeout)
	marker := doctor.VerifyMarkerFile(platPaths)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
		ok, err := doctor.ReadVerifyMarker(marker, nonce)
		if err != nil {
			return err
		}
		if ok {
			if err := doctor.RecordEndToEnd(platPaths, time.Now()); err != nil {
				return err
			}
			state, _ := setup.LoadState(paths)
			_ = state.MarkDone(paths, setup.StepVerify)
			fmt.Fprintln(out, "End-to-end verified: the bot received the token and replied.")
			return nil
		}
	}
	return WithExitCode(1, fmt.Errorf("no end-to-end confirmation within %s; the bot did not handle the token.\nCheck `agentdock doctor` and the service log. Platform: %s", timeout, connection.Kind))
}
