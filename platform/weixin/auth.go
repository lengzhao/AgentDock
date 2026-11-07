package weixin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/lengzhao/AgentDock/platform"
	"github.com/lengzhao/pluginkit"
	"github.com/mdp/qrterminal/v3"
)

type loginAPI interface {
	QR(context.Context) (QRCode, error)
	QRStatus(context.Context, string, string) (LoginStatus, error)
}

// Auth performs interactive QR login. Persistence is owned by platform.SaveLogin.
type Auth struct{ api loginAPI }

func init() {
	pluginkit.Register("platform/auth-weixin", func() (platform.Authenticator, error) { return Auth{}, nil })
}
func (a Auth) Login(ctx context.Context, req platform.LoginRequest) (platform.LoginResult, error) {
	if req.NonInteractive || req.ExistingID != "" {
		return platform.LoginResult{}, errors.New("weixin requires interactive QR login; omit --non-interactive and --app-id")
	}
	out := req.Out
	if out == nil {
		out = io.Discard
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	api := a.api
	if api == nil {
		client, err := NewClient("", "", nil)
		if err != nil {
			return platform.LoginResult{}, err
		}
		api = client
	}
	qr, err := api.QR(ctx)
	if err != nil {
		return platform.LoginResult{}, err
	}
	if qr.Code == "" || qr.Image == "" {
		return platform.LoginResult{}, errors.New("weixin returned no QR code")
	}
	fmt.Fprintln(out, "Scan with WeChat and confirm the bot login:")
	qrterminal.GenerateWithConfig(qr.Image, qrterminal.Config{Level: qrterminal.L, Writer: out, HalfBlocks: true, QuietZone: 2})
	fmt.Fprintf(out, "QR login: %s\n", qr.Image)
	code := ""
	for {
		if err := ctx.Err(); err != nil {
			return platform.LoginResult{}, err
		}
		status, err := api.QRStatus(ctx, qr.Code, code)
		if err != nil {
			return platform.LoginResult{}, err
		}
		switch status.Status {
		case "confirmed":
			if status.Token == "" || status.BotID == "" || status.UserID == "" {
				return platform.LoginResult{}, errors.New("weixin login returned incomplete account identity")
			}
			if status.BaseURL == "" {
				status.BaseURL = DefaultBaseURL
			}
			if err := ValidBaseURL(status.BaseURL); err != nil {
				return platform.LoginResult{}, err
			}
			return platform.LoginResult{Credentials: map[string]string{"id": status.BotID, "secret": status.Token, "owner": status.UserID}, Settings: map[string]string{"baseUrl": status.BaseURL}}, nil
		case "need_verifycode":
			if req.ReadCode == nil {
				return platform.LoginResult{}, errors.New("weixin requires a verification code; retry in an interactive terminal")
			}
			code, err = req.ReadCode()
			if err != nil {
				return platform.LoginResult{}, err
			}
			if strings.TrimSpace(code) == "" {
				return platform.LoginResult{}, errors.New("verification code is empty")
			}
		case "scaned_but_redirect":
			host := status.RedirectHost
			if !strings.Contains(host, "://") {
				host = "https://" + host
			}
			if status.RedirectHost == "" {
				return platform.LoginResult{}, errors.New("weixin QR redirect has no host")
			}
			client, err := NewClient(host, "", nil)
			if err != nil {
				return platform.LoginResult{}, err
			}
			api = client
		case "wait", "scaned":
		case "expired", "verify_code_blocked", "binded_redirect":
			return platform.LoginResult{}, errors.New("weixin QR login expired or cannot complete; rerun setup to create a fresh code")
		default:
			return platform.LoginResult{}, errors.New("weixin returned an unsupported QR login status")
		}
		if err := wait(ctx, time.Second); err != nil {
			return platform.LoginResult{}, err
		}
	}
}
func (Auth) Owner(_ context.Context, values, settings map[string]string) (string, error) {
	if values["owner"] == "" {
		return "", errors.New("no verified scanner identity; run setup --platform weixin or bind manually")
	}
	return values["owner"], nil
}
func wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
