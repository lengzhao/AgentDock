package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/lengzhao/AgentDock/config"
	"github.com/lengzhao/AgentDock/service"
	"github.com/lengzhao/AgentDock/setup"
)

func installAndStartService(ctx context.Context, out io.Writer, paths config.Paths, cfg config.Config) error {
	mgr, err := assembleService(cfg.Service.Label)
	if err != nil {
		return err
	}
	spec, err := serviceSpec(cfg)
	if err != nil {
		return err
	}
	if err := mgr.Install(ctx, spec); err != nil {
		return err
	}
	if err := mgr.Start(ctx); err != nil {
		return err
	}
	st, err := service.WaitRunning(ctx, mgr, 20*time.Second)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Service %s running (pid %d).\n", spec.Label, st.PID)
	fmt.Fprintf(out, "Logs: %s\n", filepath.Join(spec.LogDir, "service.err.log"))
	state, err := setup.LoadState(paths)
	if err != nil {
		return err
	}
	return state.MarkDone(paths, setup.StepService)
}
