package assembly_test

import (
	"context"
	"testing"

	"github.com/lengzhao/AgentDock/assembly"
	"github.com/lengzhao/AgentDock/credentials"
	_ "github.com/lengzhao/AgentDock/plugin"
	"github.com/lengzhao/pluginkit"
)

func TestCustomCredentialPlugin(t *testing.T) {
	pluginkit.Register("test/custom-store", func() (credentials.Store, error) { return credentials.NewMemoryStore(), nil })
	store, err := assembly.Open[credentials.Store](context.Background(), "test/custom-store", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(context.Background(), "svc", "account", "value"); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(context.Background(), "svc", "account")
	if err != nil || got != "value" {
		t.Fatalf("got %q, %v", got, err)
	}
}
func TestRejectUnknownOrWrongContract(t *testing.T) {
	for _, kind := range []string{"test/missing", "agentdock/credentials-memory"} {
		if _, err := assembly.Open[interface{ Missing() }](context.Background(), kind, nil); err == nil {
			t.Fatalf("%s accepted", kind)
		}
	}
}
