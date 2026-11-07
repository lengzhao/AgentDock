package runtime

import (
	"path/filepath"

	"github.com/lengzhao/AgentDock/config"
	dockschedule "github.com/lengzhao/AgentDock/schedule"
	_ "github.com/lengzhao/agentkit/plugins/acp/sessionmcptools"
	_ "github.com/lengzhao/agentkit/plugins/schedule"
	_ "github.com/lengzhao/agentkit/runtime/tools"
)

// attachScheduling creates one durable scheduler and one turn-bound MCP tool
// runtime, shared by every platform and ACP agent in this installation.
func attachScheduling(cfg config.Config, layout Layout, secrets Secrets, graph map[string]any) {
	options := cfg.ScheduleOptions()
	if options.Disabled {
		return
	}
	paths := Node{}
	instances := Node{}
	for index, connection := range cfg.Connections() {
		dir := layout.StateDir
		if index > 0 {
			dir = filepath.Join(dir, "platforms", connection.Kind)
		}
		paths[connection.Kind] = filepath.Join(dir, "access.json")
		instance := connection.Kind
		if len(cfg.Connections()) == 1 && connection.Kind == "weixin" {
			instance = secrets.AppID
		}
		instances[connection.Kind] = instance
	}
	graph["schedule.engine"] = Node{"use": "agentdock/schedule-engine", "config": Node{"timezone": options.Timezone}}
	graph["schedule.store"] = Node{"use": "agentdock/schedule-store", "config": Node{"path": filepath.Join(layout.StateDir, "schedules.json"), "timezone": options.Timezone, "maxJobs": options.MaxJobs}, "deps": Node{"engine": "schedule.engine"}}
	graph["schedule.scoped"] = Node{"use": "agentdock/schedule-scoped", "config": Node{"accessPaths": paths}, "deps": Node{"inner": "schedule.store"}}
	graph["schedule.tool"] = Node{"use": "agentdock/tool-schedule", "config": Node{"timezone": options.Timezone, "maxJobs": options.MaxJobs}, "deps": Node{"schedule": "schedule.scoped", "engine": "schedule.engine"}}
	graph["schedule.toolRuntime"] = Node{"use": "tools/runtime", "config": Node{"allowTools": []any{"schedule"}}, "deps": Node{"tools": []any{"schedule.tool"}}}
	graph["schedule.mcp"] = Node{"use": "acp/session-mcp-tools", "config": Node{"serverName": "agentdock"}, "deps": Node{"runtime": "schedule.toolRuntime", "workspace": "workspace.default"}}
	graph["schedule.cron"] = Node{"use": "schedule/cron", "config": Node{"pollSeconds": options.PollSeconds, "missedGraceSeconds": options.MissedGraceSeconds, "sessionMode": "stateless"}, "deps": Node{"schedule": "schedule.store", "engine": "schedule.engine", "workspace": "workspace.default"}}
	graph["schedule.records"] = Node{"use": dockschedule.RecordsKind, "config": Node{"dbPath": filepath.Join(layout.StateDir, "state.db"), "installation": cfg.InstallID}}
	graph["schedule.runtime"] = Node{"use": dockschedule.RuntimeKind, "config": Node{"accessPaths": paths, "instances": instances}, "deps": Node{"inner": "schedule.cron", "records": "schedule.records", "schedule": "schedule.store"}}
	root := graph["runner.default"].(Node)["deps"].(Node)
	graph["platform.audited"] = Node{"use": dockschedule.PlatformKind, "deps": Node{"inner": root["platform"], "records": "schedule.records"}}
	root["platform"] = "platform.audited"
	graph["loop.audited"] = Node{"use": dockschedule.LoopKind, "deps": Node{"inner": root["loop"], "records": "schedule.records"}}
	root["loop"] = "loop.audited"
	root["schedules"] = []any{"schedule.runtime"}
	root["init"] = append(root["init"].([]any), "schedule.records")
	for _, entry := range graph["loop.default"].(Node)["deps"].(Node)["agents"].([]any) {
		entry.(Node)["deps"].(Node)["sessionMcp"] = "schedule.mcp"
	}
}
