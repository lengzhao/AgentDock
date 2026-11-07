// Command agentdock runs AI agents as an always-on assistant reachable through a
// messaging platform.
package main

import (
	"fmt"
	"os"

	"github.com/lengzhao/AgentDock/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "agentdock:", err)
		os.Exit(cli.ExitCode(err))
	}
}
