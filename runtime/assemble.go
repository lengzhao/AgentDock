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
	platformKind := "platform/lark"
	domain := "https://open.larksuite.com"
	if cfg.Platform.Kind == "feishu" {
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
				"inject": []any{
					"sender_id", "sender_name", "sender_email", "mentions",
					"chat_id", "timestamp",
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
				"allowFrom":          cfg.Platform.AllowFrom,
				"allowChat":          cfg.Platform.AllowChat,
				"groupReplyAll":      cfg.Platform.GroupReplyAll,
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
			"use":  "commands/registry",
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
	platformDeps := graph["platform.main"].(Node)["deps"].(Node)
	graph["env.default"] = Node{
		"use":    envslash.Kind,
		"config": Node{"path": filepath.Join(layout.Home, "config.yaml"), "accessPath": filepath.Join(layout.StateDir, "access.json"), "platform": cfg.Platform.Kind},
		"deps": Node{
			"store": Node{"use": plugin.KindCredentialsKeyring, "config": Node{"servicePrefix": "io.agentdock"}},
			"inner": platformDeps["inboundPolicy"],
		},
	}
	platformDeps["inboundPolicy"] = "env.default"
	graph["runner.default"].(Node)["deps"].(Node)["init"] = []any{"env.default"}
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
	appID, err := res.Resolve(ctx, cfg.Platform.AppIDRef)
	if err != nil {
		return nil, nil, fmt.Errorf("platform app id (%s): %w\nrun `agentdock doctor` to check the credential store, or `agentdock setup --app-id <id>` to store it again",
			cfg.Platform.AppIDRef, err)
	}
	appSecret, err := res.Resolve(ctx, cfg.Platform.AppSecretRef)
	if err != nil {
		return nil, nil, fmt.Errorf("platform app secret (%s): %w\nrun `agentdock doctor` to check the credential store, or `agentdock setup --app-id <id>` to store it again",
			cfg.Platform.AppSecretRef, err)
	}
	agentEnv, err := ResolveAgentEnvironment(ctx, cfg, res)
	if err != nil {
		return nil, nil, err
	}
	graph, rootID, err := GraphWithOverrides(cfg, layout, Secrets{AppID: appID, AppSecret: appSecret, AgentEnv: agentEnv}, overrides)
	if err != nil {
		return nil, nil, err
	}
	runner, result, err := build.Build[agentkit.Runner](ctx, graph, rootID)
	if err != nil {
		return nil, nil, fmt.Errorf("assemble runner: %w", err)
	}
	_, envOverridden := overrides["env.default"]
	if provider, ok := build.GetByID[*envslash.Provider](result, "env.default"); ok && !envOverridden {
		if err := provider.UseCredentialStore(res.Store()); err != nil {
			return nil, nil, err
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
