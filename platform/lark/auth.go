package lark

import (
	"context"
	"errors"
	"fmt"
	"github.com/lengzhao/AgentDock/platform"
	"github.com/lengzhao/pluginkit"
	"io"
)

type Auth struct{ Kind string }

func init() {
	pluginkit.Register("platform/auth-lark", func() (platform.Authenticator, error) { return Auth{Kind: "lark"}, nil })
	pluginkit.Register("platform/auth-feishu", func() (platform.Authenticator, error) { return Auth{Kind: "feishu"}, nil })
}
func (a Auth) Login(ctx context.Context, req platform.LoginRequest) (platform.LoginResult, error) {
	out := req.Out
	if out == nil {
		out = io.Discard
	}
	values := map[string]string{}
	if req.ExistingID != "" {
		if req.ReadSecret == nil {
			return platform.LoginResult{}, errors.New("platform secret input is required")
		}
		secret, err := req.ReadSecret()
		if err != nil {
			return platform.LoginResult{}, err
		}
		if secret == "" {
			return platform.LoginResult{}, errors.New("platform secret is empty")
		}
		values["id"], values["secret"] = req.ExistingID, secret
	} else {
		if req.NonInteractive {
			return platform.LoginResult{}, errors.New("--app-id is required with --non-interactive")
		}
		if req.BeforeRemote != nil {
			if err := req.BeforeRemote(); err != nil {
				return platform.LoginResult{}, err
			}
		}
		reg, err := authorizeApp(ctx, RegisterOptions{Kind: a.Kind, Out: out, AppName: "AgentDock", NoAddons: req.Options["noAddons"] == "true", TenantBrand: req.Options["tenantBrand"]})
		if err != nil {
			return platform.LoginResult{}, err
		}
		values["id"], values["secret"] = reg.AppID, reg.secret
		if req.Options["noAddons"] == "true" {
			fmt.Fprintf(out, "Configure tenant scopes: %v; events: %v; enable bot and long-connection delivery.\n", TenantScopes, TenantEvents)
		}
	}
	return platform.LoginResult{Credentials: values}, nil
}
func (a Auth) Owner(ctx context.Context, values, settings map[string]string) (string, error) {
	return LookupApplicationOwner(ctx, a.Kind, values["id"], values["secret"])
}
