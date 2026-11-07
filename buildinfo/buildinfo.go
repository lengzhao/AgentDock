// Package buildinfo exposes build-time version metadata for agentdock.
package buildinfo

// These values are overridden with -ldflags "-X ...".
var (
	// Version is the release version, e.g. "0.1.0-dev".
	Version = "0.1.0-dev"
	// Commit is the git commit the binary was built from.
	Commit = "unknown"
	// BuildDate is the RFC3339 timestamp of the build.
	BuildDate = "unknown"
)

// String renders a single-line version banner.
func String() string {
	return "agentdock " + Version + " (" + Commit + ", " + BuildDate + ")"
}
