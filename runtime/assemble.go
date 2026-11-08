// Package runtime turns an AgentDock config into the AgentKit instance graph.
//
// The graph is the only place AgentKit kinds are named. Everything above this
// package speaks AgentDock's own config model, so swapping a kind (for example
// a different platform plugin) is a change here plus a config field, not a
// change in the setup flow, doctor, or service code.
package runtime

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lengzhao/agentkit"
	_ "github.com/lengzhao/agentkit/plugins"
	"github.com/lengzhao/pluginkit/build"

	"github.com/lengzhao/AgentDock/config"
	"github.com/lengzhao/AgentDock/credentials"
	"github.com/lengzhao/AgentDock/envslash"
	"github.com/lengzhao/AgentDock/plugin"
	verifyRecord "github.com/lengzhao/AgentDock/record"
	"github.com/lengzhao/AgentDock/verify"
)

// Node is one instance in the pluginkit graph.
type Node = map[string]any

// Layout holds the on-disk roots the runtime needs. They are supplied by the
// caller (from config.Paths) rather than stored in config.yaml, so moving an
// install does not require rewriting the file.
type Layout struct {
	// Home is the AgentDock home directory.
	Home string
	// InstanceDir is the per-installation runtime root; workspace local scope and
	// session storage live here.
	InstanceDir string
	// StateDir holds access.json, setup progress and verify markers.
	StateDir string
}

// Secrets holds the resolved credential values for one run. They are kept in
// memory only and handed to AgentKit as graph values, never written to disk.
type Secrets struct {
	AppID     string
	AppSecret string
	AgentEnv  map[string]map[string]string
	// Platforms holds resolved secrets for additional connections, keyed by kind.
	Platforms map[string]Secrets
}

// Graph builds the pluginkit instance graph for cfg. Every secret is injected
// here as a plain config value, so the on-disk config stays reference-only.
func Graph(cfg config.Config, layout Layout, secrets Secrets) (map[string]any, string, error) {
	return GraphWithOverrides(cfg, layout, secrets, nil)
}

// GraphWithOverrides replaces complete named instances in the default graph.
// Overrides must satisfy the contracts of their consumers. Config and deps are
// replaced together, preventing accidental inheritance of backend-specific values.
// The input nodes are read-only for this call.
func GraphWithOverrides(cfg config.Config, layout Layout, secrets Secrets, overrides map[string]Node) (map[string]any, string, error) {
	if err := cfg.Validate(); err != nil {
		return nil, "", err
	}
	primary := cfg.Primary()
	platformKind := "platform/" + primary.Kind
	domain := "https://open.larksuite.com"
	if primary.Kind == "feishu" {
		platformKind = "platform/feishu"
		domain = "https://open.feishu.cn"
	}

	agents := make([]any, 0, len(cfg.Agents))
	for _, a := range cfg.Agents {
		env := a.Env
		if resolved, ok := secrets.AgentEnv[a.ID]; ok {
			env = resolved
		}
		for key, value := range a.Env {
			if (strings.HasPrefix(value, "keyring://") || strings.HasPrefix(value, "env:")) && (env[key] == value || env[key] == "") {
				return nil, "", fmt.Errorf("agent %s environment %s has not been resolved", a.ID, key)
			}
		}
		agents = append(agents, Node{
			"use": "agent/acp-remote",
			"config": Node{
				"id":                         a.ID,
				"command":                    toAnySlice(a.Command),
				"cwd":                        a.Cwd,
				"releaseSubprocessAfterTurn": true,
				"autoApprove":                a.AutoApprove,
				"authMethod":                 a.AuthMethod,
				"env":                        toAnyMap(env),
			},
			"deps": Node{
				"workspace":     "workspace.default",
				"fs":            "filesystem.local.state",
				"sessionStore":  "sessionStore.default",
				"sessionEvents": "session.events",
				"telemetry":     "telemetry.toolkit",
			},
		})
	}

	graph := map[string]any{
		"runner.default": Node{
			"use": "runner",
			"config": Node{
				"shutdownTimeoutSeconds": 15,
				"sessionScope":           cfg.Runtime.SessionScope,
				"maxConcurrentTurns":     cfg.Runtime.MaxConcurrentTurns,
				"defaultTimezone":        cfg.ScheduleOptions().Timezone,
				// Every supported built-in plus an explicit allowlist of useful
				// non-secret metadata. Missing values are omitted by AgentKit.
				"inject": []any{
					"sender_id", "sender_name", "sender_email", "mentions",
					"platform", "chat_id", "thread_id", "timestamp",
					"task_id", "trace_id", "language",
					"chat_type", "message_timestamp", "sessionScope",
					"project", "cwd", "workspace", "session", "session_id",
					"conversation_id", "agent", "agent_id",
				},
			},
			"deps": Node{
				"platform":     "platform.main",
				"loop":         "loop.default",
				"sessionStore": "sessionStore.default",
				"workspace":    "workspace.default",
				"telemetry":    "telemetry.default",
			},
		},
		"platform.main": Node{
			"use": platformKind,
			"config": Node{
				"appId":              secrets.AppID,
				"appSecret":          secrets.AppSecret,
				"domain":             domain,
				"allowFrom":          primary.AllowFrom,
				"allowChat":          primary.AllowChat,
				"groupReplyAll":      primary.GroupReplyAll,
				"dmThreadMode":       true,
				"runnerSessionScope": cfg.Runtime.SessionScope,
				"showToolProgress":   true,
				"showThinking":       false,
			},
			"deps": Node{
				"commands":     "commands.default",
				"sessionStore": "sessionStore.default",
				"workspace":    "workspace.default",
				// Order matters: owner decides first, record then stores accepted
				// messages, and verify only sees what both allowed. A stranger
				// therefore cannot satisfy `agentdock verify`, and a rejected message
				// never leaves a task record behind.
				"inboundPolicy": Node{
					"use":    verify.Kind,
					"config": Node{"stateDir": layout.StateDir},
					"deps": Node{
						"inner": Node{
							"use": verifyRecord.Kind,
							"config": Node{
								"dbPath":       filepath.Join(layout.StateDir, "state.db"),
								"installation": cfg.InstallID,
							},
							"deps": Node{
								"inner": Node{
									"use":    plugin.KindPolicyOwner,
									"config": Node{"path": filepath.Join(layout.StateDir, "access.json")},
								},
							},
						},
					},
				},
			},
		},
		"loop.default": Node{
			"use":    "loop/default",
			"config": Node{"defaultAgent": cfg.Runtime.DefaultAgent},
			"deps": Node{
				"agents":       agents,
				"telemetry":    "telemetry.default",
				"sessionStore": "sessionStore.default",
				"workspace":    "workspace.default",
			},
		},
		"commands.default": Node{
			"use":  scopedCommandsKind,
			"deps": Node{"sessionCommands": "sessionCommands.default"},
		},
		"sessionCommands.default": Node{
			"use":  "session/commands",
			"deps": Node{"sessionStore": "sessionStore.default"},
		},
		"sessionStore.default": Node{
			"use":    "session/store",
			"config": Node{"dir": "sessions"},
			"deps":   Node{"workspace": "workspace.default"},
		},
		"session.events": Node{"use": "session/events"},
		"workspace.default": Node{
			"use": "workspace/default",
			"config": Node{
				"global":       filepath.Join(layout.Home, "agents"),
				"local":        layout.InstanceDir,
				"scope":        "local",
				"workDir":      cfg.Runtime.WorkDir,
				"uploadSubdir": "upload",
			},
		},
		"filesystem.local.state": Node{
			"use":    "filesystem/local",
			"config": Node{"root": ".", "unrestricted": true},
			"deps":   Node{"workspace": "workspace.default"},
		},
		"telemetry.default": Node{"use": "telemetry/none"},
		"telemetry.toolkit": Node{"use": "telemetry/toolkit"},
	}
	if primary.Kind == "weixin" {
		graph["platform.main"].(Node)["config"] = Node{
			"botId": secrets.AppID, "token": secrets.AppSecret, "baseUrl": primary.Settings["baseUrl"],
			"cursorPath": filepath.Join(layout.StateDir, "weixin-cursor.json"),
			"allowFrom":  primary.AllowFrom, "sessionScope": cfg.Runtime.SessionScope,
		}
		deps := graph["platform.main"].(Node)["deps"].(Node)
		delete(deps, "workspace")
		delete(deps, "sessionStore")
	}
	platformDeps := graph["platform.main"].(Node)["deps"].(Node)
	graph["env.default"] = Node{
		"use":    envslash.Kind,
		"config": Node{"path": filepath.Join(layout.Home, "config.yaml"), "accessPath": filepath.Join(layout.StateDir, "access.json"), "platform": primary.Kind},
		"deps": Node{
			"store": Node{"use": plugin.KindCredentialsKeyring, "config": Node{"servicePrefix": "io.agentdock"}},
			"inner": platformDeps["inboundPolicy"],
		},
	}
	platformDeps["inboundPolicy"] = "env.default"
	graph["runner.default"].(Node)["deps"].(Node)["init"] = []any{"env.default"}

	if extras := cfg.AdditionalPlatforms(); len(extras) > 0 {
		platformIDs := []any{"platform.main"}
		initializers := []any{"env.default"}
		for _, connection := range extras {
			single := cfg.AsSinglePlatform(connection.Kind)
			single.Scheduling.Disabled = true
			scoped := layout
			scoped.StateDir = filepath.Join(layout.StateDir, "platforms", connection.Kind)
			resolved := secrets.Platforms[connection.Kind]
			resolved.AgentEnv = secrets.AgentEnv
			extra, _, err := Graph(single, scoped, resolved)
			if err != nil {
				return nil, "", err
			}
			ids := map[string]string{
				"platform.main":    "platform." + connection.Kind,
				"commands.default": "commands." + connection.Kind,
				"env.default":      "env." + connection.Kind,
			}
			for _, source := range []string{"platform.main", "commands.default", "env.default"} {
				node := extra[source].(Node)
				rewriteNodeDependencies(node, ids)
				// Access/verification/cursor state is per connection; task records stay in
				// the single operational DB used by tasks, recovery and retention.
				sharedRecordDB(node, filepath.Join(layout.StateDir, "state.db"))
				graph[ids[source]] = node
			}
			platformIDs = append(platformIDs, ids["platform.main"])
			initializers = append(initializers, ids["env.default"])
		}
		graph["platform.multiplex"] = Node{"use": "platform/multiplex", "deps": Node{"platforms": platformIDs}}
		rootDeps := graph["runner.default"].(Node)["deps"].(Node)
		rootDeps["platform"] = "platform.multiplex"
		rootDeps["init"] = initializers

		for index, connection := range cfg.Connections() {
			commandID, envID := "commands.default", "env.default"
			if index > 0 {
				commandID, envID = "commands."+connection.Kind, "env."+connection.Kind
			}
			node := graph[commandID].(Node)
			node["use"] = scopedCommandsKind
			node["config"] = Node{"provider": envID, "scopedProviders": initializers}
		}
	}
	attachScheduling(cfg, layout, secrets, graph)
	for id, node := range overrides {
		kind, _ := node["use"].(string)
		if strings.TrimSpace(id) == "" || strings.TrimSpace(kind) == "" {
			return nil, "", fmt.Errorf("plugin override %q requires an instance ID and use kind", id)
		}
		graph[id] = node
	}
	return graph, "runner.default", nil
}

// Build constructs the runner from cfg, resolving secrets through res.
func Build(ctx context.Context, cfg config.Config, layout Layout, res *credentials.Resolver) (agentkit.Runner, *build.Result, error) {
	return BuildWithOverrides(ctx, cfg, layout, res, nil)
}

// BuildWithOverrides resolves credentials and assembles a customized instance graph.
func BuildWithOverrides(ctx context.Context, cfg config.Config, layout Layout, res *credentials.Resolver, overrides map[string]Node) (agentkit.Runner, *build.Result, error) {
	// A missing credential is the most common first-run failure, so the message
	// names the reference and the command that fixes it instead of only the
	// backend error.
	resolvedPlatforms := make(map[string]Secrets, len(cfg.Connections()))
	for _, connection := range cfg.Connections() {
		values := map[string]string{}
		for role, ref := range connection.References() {
			value, err := res.Resolve(ctx, ref)
			if err != nil {
				return nil, nil, fmt.Errorf("resolve %s platform credential %s failed; run agentdock setup or doctor", connection.Kind, role)
			}
			values[role] = value
		}
		resolvedPlatforms[connection.Kind] = Secrets{AppID: values["id"], AppSecret: values["secret"]}
	}
	resolved := resolvedPlatforms[cfg.PrimaryKind]
	resolved.Platforms = resolvedPlatforms
	agentEnv, err := ResolveAgentEnvironment(ctx, cfg, res)
	if err != nil {
		return nil, nil, err
	}
	resolved.AgentEnv = agentEnv
	graph, rootID, err := GraphWithOverrides(cfg, layout, resolved, overrides)
	if err != nil {
		return nil, nil, err
	}
	runner, result, err := build.Build[agentkit.Runner](ctx, graph, rootID)
	if err != nil {
		return nil, nil, fmt.Errorf("assemble runner: %w", err)
	}
	for index, connection := range cfg.Connections() {
		id := "env.default"
		if index > 0 {
			id = "env." + connection.Kind
		}
		_, envOverridden := overrides[id]
		if provider, ok := build.GetByID[*envslash.Provider](result, id); ok && !envOverridden {
			if err := provider.UseCredentialStore(res.Store()); err != nil {
				return nil, nil, err
			}
		}
	}
	return runner, result, nil
}

func toAnySlice(in []string) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}

func toAnyMap(in map[string]string) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Describe renders the agent list for status output.
func Describe(cfg config.Config) []string {
	out := make([]string, 0, len(cfg.Agents))
	for _, a := range cfg.Agents {
		line := a.ID + ": " + joinCommand(a.Command)
		if a.ID == cfg.Runtime.DefaultAgent {
			line += " (default)"
		}
		out = append(out, line)
	}
	return out
}

func joinCommand(cmd []string) string {
	out := ""
	for i, part := range cmd {
		if i > 0 {
			out += " "
		}
		out += strconv.Quote(part)
	}
	return out
}

// rewriteNodeDependencies changes only named dependency references. Backend
// configuration, including arbitrary strings and credentials, is never rewritten.
func rewriteNodeDependencies(node Node, ids map[string]string) {
	deps, _ := node["deps"].(Node)
	for name, value := range deps {
		switch value := value.(type) {
		case string:
			if renamed, ok := ids[value]; ok {
				deps[name] = renamed
			}
		case Node:
			rewriteNodeDependencies(value, ids)
		case []any:
			for index, entry := range value {
				switch entry := entry.(type) {
				case string:
					if renamed, ok := ids[entry]; ok {
						value[index] = renamed
					}
				case Node:
					rewriteNodeDependencies(entry, ids)
				}
			}
		}
	}
}

func sharedRecordDB(node Node, path string) {
	if node["use"] == verifyRecord.Kind {
		if options, ok := node["config"].(Node); ok {
			options["dbPath"] = path
		}
	}
	deps, _ := node["deps"].(Node)
	for _, value := range deps {
		switch value := value.(type) {
		case Node:
			sharedRecordDB(value, path)
		case []any:
			for _, entry := range value {
				if child, ok := entry.(Node); ok {
					sharedRecordDB(child, path)
				}
			}
		}
	}
}
