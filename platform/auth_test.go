package platform_test

import (
	"context"
	"github.com/lengzhao/AgentDock/credentials"
	"github.com/lengzhao/AgentDock/platform"
	"strings"
	"testing"
)

func TestSaveLoginNamespacesAndVerifiesAllCredentials(t *testing.T) {
	ctx := context.Background()
	store := credentials.NewMemoryStore()
	result := platform.LoginResult{Credentials: map[string]string{"id": "bot", "secret": "secret", "owner": "user"}, Settings: map[string]string{"baseUrl": "https://example.test"}}
	saved, err := platform.SaveLogin(ctx, store, "install1", "weixin", result)
	if err != nil {
		t.Fatal(err)
	}
	for role, ref := range saved.References {
		value, err := credentials.NewResolver(store, nil).Resolve(ctx, ref)
		if err != nil || value != result.Credentials[role] {
			t.Fatalf("role %s: %v", role, err)
		}
	}
	other, err := platform.SaveLogin(ctx, store, "install2", "weixin", result)
	if err != nil {
		t.Fatal(err)
	}
	if saved.References["secret"] == other.References["secret"] {
		t.Fatal("installations share credentials")
	}
}
func TestSaveLoginRejectsEmptyWithoutLeaking(t *testing.T) {
	_, err := platform.SaveLogin(context.Background(), credentials.NewMemoryStore(), "install", "weixin", platform.LoginResult{Credentials: map[string]string{"id": "bot", "secret": ""}})
	if err == nil || strings.Contains(err.Error(), "bot") {
		t.Fatalf("invalid login: %v", err)
	}
}

type corruptStore struct{ credentials.Store }

func (corruptStore) Get(context.Context, string, string) (string, error) { return "wrong", nil }
func TestSaveLoginChecksReadbackWithoutExposingSecret(t *testing.T) {
	_, err := platform.SaveLogin(context.Background(), corruptStore{credentials.NewMemoryStore()}, "install", "weixin", platform.LoginResult{Credentials: map[string]string{"id": "bot", "secret": "do-not-print"}})
	if err == nil || strings.Contains(err.Error(), "do-not-print") {
		t.Fatalf("expected sanitized readback failure, got %v", err)
	}
}
