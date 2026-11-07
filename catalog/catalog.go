// Package catalog is the ACP adapter catalog slot.
//
// The upstream ACP registry publishes one JSON document listing every known
// adapter with, per platform, a download URL and sha256. Keeping this behind an
// interface means the setup flow can offer "claude / codex / kimi …" without
// hardcoding versions, and a future private catalog can be dropped in.
package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"time"
)

// DefaultIndexURL is the public ACP registry index.
const DefaultIndexURL = "https://cdn.agentclientprotocol.com/registry/v1/latest/registry.json"

// Distribution is one way to obtain an adapter.
type Distribution struct {
	// NPM is set when the adapter is published to npm.
	NPM *NPMDistribution `json:"npx,omitempty"`
	// UVX is set when the adapter is published as a Python package.
	UVX *UVXDistribution `json:"uvx,omitempty"`
	// Binary maps GOOS-GOARCH (darwin-aarch64 …) to a release artifact.
	Binary map[string]BinaryDistribution `json:"binary,omitempty"`
}

// NPMDistribution describes an npx install.
type NPMDistribution struct {
	Package string   `json:"package"`
	Args    []string `json:"args,omitempty"`
	Version string   `json:"version,omitempty"`
}

// UVXDistribution describes a uvx install.
type UVXDistribution struct {
	Package string   `json:"package"`
	Args    []string `json:"args,omitempty"`
}

// BinaryDistribution describes a release artifact.
type BinaryDistribution struct {
	Archive string   `json:"archive"`
	Cmd     string   `json:"cmd"`
	Args    []string `json:"args,omitempty"`
	SHA256  string   `json:"sha256"`
}

// Entry is one adapter in the registry.
type Entry struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Version      string       `json:"version"`
	Description  string       `json:"description"`
	Repository   string       `json:"repository"`
	License      string       `json:"license"`
	Distribution Distribution `json:"distribution"`
}

// Index is the parsed registry document.
type Index struct {
	Version string  `json:"version"`
	Agents  []Entry `json:"agents"`
	// FetchedAt is when this document was retrieved.
	FetchedAt time.Time `json:"-"`
}

// Source fetches the catalog.
type Source interface {
	// Fetch returns the current index, using a cached copy when still fresh.
	Fetch(ctx context.Context) (Index, error)
}

// Lookup finds an entry by id.
func (i Index) Lookup(id string) (Entry, bool) {
	for _, e := range i.Agents {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

// Sorted returns entries ordered by id.
func (i Index) Sorted() []Entry {
	out := append([]Entry(nil), i.Agents...)
	sort.Slice(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out
}

// Command returns the argv that launches this adapter on the current platform,
// plus the distribution kind. It prefers a binary build for the exact platform
// (pinned by sha256) and falls back to npm.
func (e Entry) Command() (argv []string, kind string, err error) {
	key := runtime.GOOS + "-"
	switch runtime.GOARCH {
	case "arm64":
		key += "aarch64"
	case "amd64":
		key += "x86_64"
	default:
		key += runtime.GOARCH
	}
	if b, ok := e.Distribution.Binary[key]; ok {
		cmd := b.Cmd
		if cmd == "" {
			cmd = "./" + e.ID
		}
		return append([]string{cmd}, b.Args...), "binary:" + key, nil
	}
	if e.Distribution.NPM != nil && e.Distribution.NPM.Package != "" {
		argv = []string{"npx", "-y"}
		pkg := e.Distribution.NPM.Package
		if e.Version != "" && !strings.Contains(pkg, "@"+(e.Version)) {
			// Only pin when the registry gives a version; an unpinned package would
			// silently change behaviour between restarts.
			if idx := strings.LastIndex(pkg, "@"); idx <= 0 {
				pkg = pkg + "@" + e.Version
			}
		}
		argv = append(argv, pkg)
		argv = append(argv, e.Distribution.NPM.Args...)
		return argv, "npm", nil
	}
	if e.Distribution.UVX != nil && e.Distribution.UVX.Package != "" {
		argv = append([]string{"uvx"}, e.Distribution.UVX.Package)
		argv = append(argv, e.Distribution.UVX.Args...)
		return argv, "uvx", nil
	}
	return nil, "", fmt.Errorf("adapter %q has no distribution for %s", e.ID, key)
}

// LicenseWarning returns a non-empty note when an adapter's license means the
// user must confirm redistribution terms (docs research §11 item 1).
func (e Entry) LicenseWarning() string {
	switch strings.ToLower(strings.TrimSpace(e.License)) {
	case "", "noassertion":
		return "license not declared by upstream; confirm terms before redistributing"
	case "proprietary":
		return "proprietary upstream license; install from the official source and do not redistribute"
	}
	return ""
}

// MarshalJSON keeps the raw document usable by `agentdock catalog --json`.
func (i Index) MarshalJSON() ([]byte, error) {
	type alias Index
	return json.Marshal(alias(i))
}

// Filtered restricts a Source to an allowlist.
type Filtered struct {
	Inner Source
	// AllowIDs, when non-empty, drops every entry not listed.
	AllowIDs []string
}

// Fetch implements Source.
func (f Filtered) Fetch(ctx context.Context) (Index, error) {
	idx, err := f.Inner.Fetch(ctx)
	if err != nil {
		return Index{}, err
	}
	if len(f.AllowIDs) == 0 {
		return idx, nil
	}
	allow := make(map[string]bool, len(f.AllowIDs))
	for _, id := range f.AllowIDs {
		allow[id] = true
	}
	kept := make([]Entry, 0, len(idx.Agents))
	for _, e := range idx.Agents {
		if allow[e.ID] {
			kept = append(kept, e)
		}
	}
	idx.Agents = kept
	return idx, nil
}
