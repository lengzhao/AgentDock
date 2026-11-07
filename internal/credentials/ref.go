package credentials

import (
	"fmt"
	"strings"
)

// RefKind identifies the backend a credential reference points at.
type RefKind int

const (
	// RefNone is the zero value: no reference configured.
	RefNone RefKind = iota
	// RefKeyring points at the OS credential store: keyring://<service>/<account>.
	RefKeyring
	// RefEnv points at an environment variable: env:NAME.
	RefEnv
)

// Ref is a parsed credential reference. Configuration only ever stores
// references; the secret itself lives in the backend (SEC-01/SEC-02).
type Ref struct {
	Kind    RefKind
	Service string
	Account string
	Name    string
	raw     string
}

// ParseRef parses "keyring://service/account" or "env:NAME". A bare value is
// rejected rather than accepted, so a pasted secret fails loudly instead of
// being written into config.
func ParseRef(raw string) (Ref, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return Ref{}, fmt.Errorf("credential reference is empty")
	}
	switch {
	case strings.HasPrefix(value, "keyring://"):
		service, account, ok := strings.Cut(strings.TrimPrefix(value, "keyring://"), "/")
		if !ok || service == "" || account == "" {
			return Ref{}, fmt.Errorf("credential reference %q must look like keyring://service/account", value)
		}
		if strings.ContainsAny(service, " \t\n") || strings.ContainsAny(account, " \t\n") {
			return Ref{}, fmt.Errorf("credential reference %q contains whitespace", value)
		}
		return Ref{Kind: RefKeyring, Service: service, Account: account, raw: value}, nil
	case strings.HasPrefix(value, "env:"):
		name := strings.TrimPrefix(value, "env:")
		if !isEnvName(name) {
			return Ref{}, fmt.Errorf("credential reference %q is not a valid environment variable", value)
		}
		return Ref{Kind: RefEnv, Name: name, raw: value}, nil
	case strings.Contains(value, "://"):
		scheme, _, _ := strings.Cut(value, "://")
		return Ref{}, fmt.Errorf("credential reference scheme %q is not supported (use keyring:// or env:)", scheme)
	default:
		return Ref{}, fmt.Errorf("credential reference %q is not a reference; use keyring://service/account or env:NAME", value)
	}
}

// KeyringRef builds a keyring reference.
func KeyringRef(service, account string) Ref {
	return Ref{Kind: RefKeyring, Service: service, Account: account}
}

// String returns the canonical reference text.
func (r Ref) String() string {
	if r.raw != "" {
		return r.raw
	}
	switch r.Kind {
	case RefKeyring:
		return "keyring://" + r.Service + "/" + r.Account
	case RefEnv:
		return "env:" + r.Name
	default:
		return ""
	}
}

// EnvName returns the variable name for an env reference.
func (r Ref) EnvName() string {
	if r.Kind != RefEnv {
		return ""
	}
	return r.Name
}

// IsZero reports whether the reference is unset.
func (r Ref) IsZero() bool { return r.Kind == RefNone }

func isEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r == '_', r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}
