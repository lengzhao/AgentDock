package cli

import (
	"context"
	"fmt"
	"github.com/lengzhao/AgentDock/assembly"
	"github.com/lengzhao/AgentDock/plugin"

	"github.com/spf13/cobra"

	"github.com/lengzhao/AgentDock/catalog"
)

func newCatalogCmd() *cobra.Command {
	var jsonOut bool
	var all bool
	cmd := &cobra.Command{
		Use:   "catalog",
		Short: "List ACP agent adapters known to the official registry",
		Long: `List ACP agent adapters.

Data comes from the official ACP registry, cached for 24h. A stale cache is used
when the CDN is unreachable; the output says so when it happens.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := rootEnv.catalogSource()
			if err != nil {
				return err
			}
			idx, err := src.Fetch(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if jsonOut {
				return writeJSON(cmd, idx)
			}
			if rootEnv.catalogKind != "" && rootEnv.catalogKind != plugin.KindCatalogACPRegistry {
				fmt.Fprintf(out, "source: %s\n", rootEnv.catalogKind)
			} else if idx.FetchedAt.IsZero() {
				fmt.Fprintln(out, "source: cache only (registry unavailable)")
			} else {
				fmt.Fprintf(out, "source: %s (%s)\n", catalog.DefaultIndexURL, idx.FetchedAt.Format("2006-01-02 15:04"))
			}
			entries := idx.Sorted()
			for _, e := range entries {
				argv, kind, cerr := e.Command()
				if cerr != nil && !all {
					continue
				}
				cmdline := "(no distribution for this platform)"
				if cerr == nil {
					cmdline = kind + ": " + joinArgs(argv)
				}
				fmt.Fprintf(out, "%-22s %-12s %-22s %s\n", e.ID, e.Version, e.License, cmdline)
				if warning := e.LicenseWarning(); warning != "" {
					fmt.Fprintf(out, "%-22s %s\n", "", "! "+warning)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print the raw index as JSON")
	cmd.Flags().BoolVar(&all, "all", false, "include adapters with no distribution for this platform")
	return cmd
}

func joinArgs(argv []string) string {
	out := ""
	for i, a := range argv {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}

func (e *env) catalogSource() (catalog.Source, error) {
	kind := e.catalogKind
	if kind == "" {
		kind = plugin.KindCatalogACPRegistry
	}
	return assembly.Open[catalog.Source](context.Background(), kind, map[string]any{"cacheDir": e.paths.CacheDir})
}
