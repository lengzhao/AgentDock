// Package lark implements the Lark/Feishu platform slot pieces AgentDock owns:
// guided application registration and platform-specific validation.
//
// Message transport itself is AgentKit's platform/lark (international) and
// platform/feishu (China) kinds; this package never opens a second WebSocket.
package lark

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/larksuite/oapi-sdk-go/v3/scene/registration"
	"github.com/mdp/qrterminal/v3"

	"github.com/lengzhao/AgentDock/internal/credentials"
)

// ErrNotSupported means the registry says this flow cannot run for this tenant.
var ErrNotSupported = errors.New("automatic application registration is not available")

// Scopes and events AgentDock needs for its first-version feature set. They are
// kept minimal on purpose: the delivery plan forbids requesting permissions for
// features that are not shipped yet.
var (
	// TenantScopes are application-identity permissions.
	TenantScopes = []string{
		"im:message",             // read inbound messages
		"im:message:send_as_bot", // reply as the bot
		"im:resource",            // download attachments sent to the bot
	}
	// TenantEvents are application-identity event subscriptions.
	TenantEvents = []string{
		"im.message.receive_v1",
	}
	// Callbacks are card interactions used for tool approval.
	Callbacks = []string{
		"card.action.trigger",
	}
)

// RegisterOptions configures one registration attempt.
type RegisterOptions struct {
	// Kind is "lark" or "feishu"; it selects the accounts domain.
	Kind string
	// AppName is pre-filled on the confirmation page.
	AppName string
	// Out receives human-readable progress; the QR code is drawn here.
	Out io.Writer
	// OpenURL is called with the authorization URL so a caller can also show a
	// clickable link or open a browser. Optional.
	OpenURL func(url string)
	// Store receives the resulting credentials. It must verify read-back.
	Store credentials.Store
}

// Registration is the result of a successful registration.
type Registration struct {
	AppID string
	// SecretFingerprint is a printable, non-reversible hint for logs.
	SecretFingerprint string
	// OwnerOpenID is the id of the user who scanned, when the tenant returns it.
	OwnerOpenID string
	TenantBrand string
}

// RegisterApp runs the official device-authorization flow: the user scans a QR
// code (or opens a link), confirms on the Lark page, and the SDK returns the app
// credentials. The credentials are stored and read back before RegisterApp
// reports success, so a failed keychain write cannot look like a win.
func RegisterApp(ctx context.Context, opt RegisterOptions) (Registration, error) {
	if opt.Store == nil {
		return Registration{}, fmt.Errorf("registration needs a credential store")
	}
	if opt.Out == nil {
		opt.Out = io.Discard
	}

	var once sync.Once
	opts := &registration.Options{
		Source: "agentdock",
		AppPreset: &registration.AppPreset{
			Name: opt.AppName,
			Desc: "AgentDock: your agents, on Lark",
		},
		Addons: &registration.AppAddons{
			Scopes: registration.AppAddonsScopes{Tenant: TenantScopes},
			Events: registration.AppAddonsEvents{
				Items: registration.AppAddonsEventItems{Tenant: TenantEvents},
			},
			Callbacks: registration.AppAddonsCallbacks{Items: Callbacks},
		},
		OnQRCode: func(info *registration.QRCodeInfo) {
			once.Do(func() {
				fmt.Fprintf(opt.Out, "\nScan this code in %s to create the app:\n\n", brandName(opt.Kind))
				qrterminal.GenerateWithConfig(info.URL, qrterminal.Config{
					Level:      qrterminal.L,
					Writer:     opt.Out,
					HalfBlocks: true,
					QuietZone:  2,
				})
				fmt.Fprintf(opt.Out, "\nOr open: %s\n", info.URL)
				fmt.Fprintf(opt.Out, "Expires in %d seconds. Ctrl+C cancels; no app is created until you confirm.\n\n",
					info.ExpireIn)
				if opt.OpenURL != nil {
					opt.OpenURL(info.URL)
				}
			})
		},
		OnStatusChange: func(info *registration.StatusChangeInfo) {
			switch info.Status {
			case registration.StatusSlowDown:
				fmt.Fprintf(opt.Out, "  (server asked to slow down; polling every %ds)\n", info.Interval)
			case registration.StatusDomainSwitched:
				fmt.Fprintf(opt.Out, "  (tenant is Lark; switching authorization domain)\n")
			}
		},
	}
	if opt.Kind == "lark" {
		opts.Domain = "https://accounts.larksuite.com"
	}

	result, err := registration.RegisterApp(ctx, opts)
	if err != nil {
		return Registration{}, describeRegistrationError(err, opt.Kind)
	}
	if result.ClientID == "" || result.ClientSecret == "" {
		return Registration{}, fmt.Errorf("registration returned no credentials")
	}

	// Persist first, and only report success once read-back matches.
	if err := storeRegistration(ctx, opt.Store, result.ClientID, result.ClientSecret); err != nil {
		return Registration{}, fmt.Errorf("store registered credentials: %w", err)
	}

	reg := Registration{
		AppID:             result.ClientID,
		SecretFingerprint: credentials.Fingerprint(result.ClientSecret),
	}
	if result.UserInfo != nil {
		reg.OwnerOpenID = result.UserInfo.OpenID
		reg.TenantBrand = result.UserInfo.TenantBrand
	}
	return reg, nil
}

func storeRegistration(ctx context.Context, store credentials.Store, appID, appSecret string) error {
	if err := store.Set(ctx, RegistrationService, AccountAppID, appID); err != nil {
		return err
	}
	if err := store.Set(ctx, RegistrationService, AccountAppSecret, appSecret); err != nil {
		return fmt.Errorf("store app secret: %w", err)
	}
	for account, want := range map[string]string{AccountAppID: appID, AccountAppSecret: appSecret} {
		got, err := store.Get(ctx, RegistrationService, account)
		if err != nil {
			return fmt.Errorf("read back %s: %w", account, err)
		}
		if got != want {
			return fmt.Errorf("read back %s returned a different value", account)
		}
	}
	return nil
}

// Credential store coordinates used for the platform app.
const (
	// RegistrationService is the keyring service name.
	RegistrationService = "platform"
	// AccountAppID and AccountAppSecret are the keyring accounts.
	AccountAppID     = "app-id"
	AccountAppSecret = "app-secret"
)

// Refs returns the credential references a registered install should store in
// config.yaml.
func Refs() (appIDRef, appSecretRef string) {
	return credentials.KeyringRef(RegistrationService, AccountAppID).String(),
		credentials.KeyringRef(RegistrationService, AccountAppSecret).String()
}

func brandName(kind string) string {
	if kind == "feishu" {
		return "Feishu (飞书)"
	}
	return "Lark"
}

// describeRegistrationError turns SDK errors into actionable text without
// echoing any credential material.
func describeRegistrationError(err error, kind string) error {
	var denied *registration.AccessDeniedError
	var expired *registration.ExpiredError
	var reg *registration.RegisterAppError
	switch {
	case errors.As(err, &denied):
		return fmt.Errorf("you declined the authorization in %s; nothing was created. Re-run to try again", brandName(kind))
	case errors.As(err, &expired):
		return fmt.Errorf("the authorization expired before it was confirmed; no app was created. Re-run to get a new code")
	case errors.As(err, &reg):
		code := strings.TrimSpace(reg.Code)
		if code == "" {
			code = "unknown error"
		}
		return fmt.Errorf("registration failed (%s): %s", code, strings.TrimSpace(reg.Description))
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("registration cancelled; the confirmation on the %s page may still complete. "+
			"Check the developer console before retrying so a second app is not created", brandName(kind))
	default:
		return fmt.Errorf("registration failed: %w", err)
	}
}
