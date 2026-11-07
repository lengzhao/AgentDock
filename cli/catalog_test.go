package cli

import (
	"bytes"
	"context"
	"github.com/lengzhao/AgentDock/catalog"
	"github.com/lengzhao/pluginkit"
	"strings"
	"testing"
)

type localCatalog struct{}

func (localCatalog) Fetch(context.Context) (catalog.Index, error) { return catalog.Index{}, nil }
func TestCustomCatalogSourceLabel(t *testing.T) {
	pluginkit.Register("test/local-catalog", func(struct {
		CacheDir string `json:"cacheDir"`
	}) (catalog.Source, error) {
		return localCatalog{}, nil
	})
	previous := rootEnv
	rootEnv = &env{catalogKind: "test/local-catalog"}
	t.Cleanup(func() { rootEnv = previous })
	var out bytes.Buffer
	cmd := newCatalogCmd()
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "test/local-catalog") || strings.Contains(out.String(), "registry unavailable") {
		t.Fatalf("incorrect source: %s", out.String())
	}
}
