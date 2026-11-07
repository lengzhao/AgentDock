// Package platform defines the shared platform authentication boundary.
// Auth plugins perform authorization; SaveLogin owns persistence and verification.
package platform

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"

	"github.com/lengzhao/AgentDock/credentials"
)

// LoginRequest supplies common interaction callbacks and platform-specific options.
// Secrets never pass through argv or serialized options.
type LoginRequest struct {
	Out            io.Writer
	ExistingID     string
	ReadSecret     func() (string, error)
	ReadCode       func() (string, error)
	Options        map[string]string
	NonInteractive bool
	// BeforeRemote records a pending registration before creating external state.
	BeforeRemote func() error
}

// LoginResult holds ephemeral credentials. Never serialize or log this value.
// id and secret are the common runtime roles; owner is a verified identity when available.
type LoginResult struct {
	Credentials map[string]string
	Settings    map[string]string
}

// Authenticator is implemented by each platform's replaceable login plugin.
type Authenticator interface {
	Login(context.Context, LoginRequest) (LoginResult, error)
	Owner(context.Context, map[string]string, map[string]string) (string, error)
}

// SavedLogin contains only references and non-sensitive settings.
type SavedLogin struct {
	References map[string]string
	Settings   map[string]string
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// SaveLogin stores a complete generation under an installation/platform namespace.
// Failed writes leave the previous generation intact. References are returned only
// after every value is independently read back; callers then atomically save config.
func SaveLogin(ctx context.Context, store credentials.Store, installation, kind string, login LoginResult) (SavedLogin, error) {
	if store == nil || !safeName.MatchString(installation) || !safeName.MatchString(kind) {
		return SavedLogin{}, errors.New("invalid platform credential namespace or store")
	}
	if login.Credentials["id"] == "" || login.Credentials["secret"] == "" {
		return SavedLogin{}, errors.New("login did not return complete credentials")
	}
	roles := make([]string, 0, len(login.Credentials))
	for role, value := range login.Credentials {
		if !safeName.MatchString(role) || value == "" {
			return SavedLogin{}, errors.New("login returned an invalid credential role or empty value")
		}
		roles = append(roles, role)
	}
	sort.Strings(roles)
	var generation [12]byte
	if _, err := rand.Read(generation[:]); err != nil {
		return SavedLogin{}, err
	}
	service := "platform." + installation + "." + kind + "." + hex.EncodeToString(generation[:])
	saved := SavedLogin{References: map[string]string{}, Settings: map[string]string{}}
	for _, role := range roles {
		value := login.Credentials[role]
		if err := store.Set(ctx, service, role, value); err != nil {
			return SavedLogin{}, fmt.Errorf("save platform credential %s failed", role)
		}
		got, err := store.Get(ctx, service, role)
		if err != nil || got != value {
			return SavedLogin{}, fmt.Errorf("verify platform credential %s failed", role)
		}
		saved.References[role] = credentials.KeyringRef(service, role).String()
	}
	for k, v := range login.Settings {
		saved.Settings[k] = v
	}
	return saved, nil
}
