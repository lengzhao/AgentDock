// Package plugin registers AgentDock's slot implementations with pluginkit.
//
// The research doc's slot model says: AgentDock defines the interface, third-party
// libraries stay inside the adapter, and the pluginkit kind carries an
// "agentdock/" prefix so it cannot collide with agentkit's platform/*, agent/*,
// tool/* namespaces. This file is the registration half of that contract.
package plugin

import (
	"fmt"

	"github.com/lengzhao/agentkit/cap/ingress"
	"github.com/lengzhao/pluginkit"

	"github.com/lengzhao/AgentDock/access"
	"github.com/lengzhao/AgentDock/catalog"
	"github.com/lengzhao/AgentDock/credentials"
	"github.com/lengzhao/AgentDock/service"
)

// Slot kind names.
const (
	// KindCredentialsKeyring is the OS credential store slot implementation.
	KindCredentialsKeyring = "agentdock/credentials-keyring"
	// KindCredentialsMemory is the non-persistent test store.
	KindCredentialsMemory = "agentdock/credentials-memory"
	// KindServiceLaunchd is the macOS user LaunchAgent implementation.
	KindServiceLaunchd = "agentdock/service-launchd"
	// KindCatalogACPRegistry is the official ACP registry catalog.
	KindCatalogACPRegistry = "agentdock/catalog-acp-registry"
	// KindPolicyOwner is the owner-binding inbound policy.
	KindPolicyOwner = "agentdock/policy-owner"
)

// KeyringConfig configures the keyring slot.
type KeyringConfig struct {
	// ServicePrefix namespaces every entry, e.g. io.agentdock.
	ServicePrefix string `json:"servicePrefix"`
}

// MemoryConfig configures the in-memory slot.
type MemoryConfig struct{}

// LaunchdConfig configures the launchd slot.
type LaunchdConfig struct {
	// Label is the LaunchAgent label.
	Label string `json:"label"`
}

// CatalogConfig configures the ACP registry slot.
type CatalogConfig struct {
	// IndexURL overrides the registry URL.
	IndexURL string `json:"indexUrl"`
	// CacheDir stores the last successful fetch.
	CacheDir string `json:"cacheDir"`
	// AllowIDs limits which adapters may be selected. Empty means no limit.
	AllowIDs []string `json:"allowIds"`
}

// OwnerPolicyConfig configures the owner-binding policy.
type OwnerPolicyConfig struct {
	// Path is the access state file.
	Path string `json:"path"`
}

func init() {
	// Register panics on a bad signature, which is the point: a slot that cannot
	// be constructed must fail at startup, not on first use.
	pluginkit.Register(KindCredentialsKeyring, func(cfg KeyringConfig) (credentials.Store, error) {
		return credentials.NewKeyringStore(cfg.ServicePrefix), nil
	})
	pluginkit.Register(KindCredentialsMemory, func(cfg MemoryConfig) (credentials.Store, error) {
		return credentials.NewMemoryStore(), nil
	})
	pluginkit.Register(KindServiceLaunchd, func(cfg LaunchdConfig) (service.ServiceManager, error) {
		return service.NewLaunchd(cfg.Label)
	})
	pluginkit.Register(KindPolicyOwner, func(cfg OwnerPolicyConfig) (ingress.Policy, error) {
		if cfg.Path == "" {
			return nil, fmt.Errorf("%s requires path", KindPolicyOwner)
		}
		return &access.Policy{Store: access.NewStore(cfg.Path)}, nil
	})
	pluginkit.Register(KindCatalogACPRegistry, func(cfg CatalogConfig) (catalog.Source, error) {
		src := catalog.NewRegistrySource(cfg.IndexURL, cfg.CacheDir)
		return catalog.Filtered{Inner: src, AllowIDs: cfg.AllowIDs}, nil
	})
}
