package credentials

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseRef(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     string
		want    RefKind
		wantErr bool
	}{
		{name: "keyring", raw: "keyring://io.agentdock.platform/app-secret", want: RefKeyring},
		{name: "env", raw: "env:LARK_APP_SECRET", want: RefEnv},
		{name: "empty", raw: "", wantErr: true},
		{name: "bare secret", raw: "s3cr3t-value", wantErr: true},
		{name: "keyring without account", raw: "keyring://io.agentdock", wantErr: true},
		{name: "keyring empty account", raw: "keyring://svc/", wantErr: true},
		{name: "unknown scheme", raw: "file:///etc/passwd", wantErr: true},
		{name: "env bad name", raw: "env:1BAD", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseRef(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseRef(%q) = %+v, want error", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRef(%q): %v", tc.raw, err)
			}
			if got.Kind != tc.want {
				t.Fatalf("kind = %v, want %v", got.Kind, tc.want)
			}
			if got.String() != tc.raw {
				t.Fatalf("String() = %q, want %q", got.String(), tc.raw)
			}
		})
	}
}

func TestResolverReadsStoreAndEnv(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	if err := store.Set(ctx, "platform", "app-secret", "value-1"); err != nil {
		t.Fatal(err)
	}
	res := NewResolver(store, func(name string) (string, bool) {
		if name == "FROM_ENV" {
			return "env-value", true
		}
		return "", false
	})

	got, err := res.Resolve(ctx, "keyring://platform/app-secret")
	if err != nil || got != "value-1" {
		t.Fatalf("resolve keyring = %q, %v", got, err)
	}
	got, err = res.Resolve(ctx, "env:FROM_ENV")
	if err != nil || got != "env-value" {
		t.Fatalf("resolve env = %q, %v", got, err)
	}

	if _, err := res.Resolve(ctx, "keyring://platform/missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing secret error = %v, want ErrNotFound", err)
	}
	if _, err := res.Resolve(ctx, "env:MISSING"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing env error = %v, want ErrNotFound", err)
	}
	if _, err := res.Resolve(ctx, "plainvalue"); err == nil {
		t.Fatal("bare value must not resolve")
	}
}

func TestResolverWithoutStore(t *testing.T) {
	t.Parallel()
	res := NewResolver(nil, nil)
	_, err := res.Resolve(context.Background(), "keyring://a/b")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestMemoryStoreRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := store.Get(ctx, "svc", "acct"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get missing = %v", err)
	}
	if err := store.Set(ctx, "svc", "acct", "v"); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, "svc", "acct")
	if err != nil || got != "v" {
		t.Fatalf("get = %q, %v", got, err)
	}
	if err := store.Delete(ctx, "svc", "acct"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, "svc", "acct"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete = %v", err)
	}
	if err := store.Delete(ctx, "svc", "acct"); err != nil {
		t.Fatalf("second delete must be a no-op, got %v", err)
	}
}

func TestFingerprintDoesNotLeakWholeSecret(t *testing.T) {
	t.Parallel()
	const secret = "abcdefghijklmnop"
	fp := Fingerprint(secret)
	if strings.Contains(fp, secret) {
		t.Fatalf("fingerprint %q reveals the secret", fp)
	}
	if !strings.Contains(fp, "mnop") {
		t.Fatalf("fingerprint %q should keep the tail", fp)
	}
	if Fingerprint("") != "(empty)" {
		t.Fatalf("empty fingerprint = %q", Fingerprint(""))
	}
}
