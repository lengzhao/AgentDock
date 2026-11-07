package weixin

import (
	"context"
	"errors"
	"github.com/lengzhao/AgentDock/platform"
	"io"
	"testing"
)

type fakeLoginAPI struct{ status LoginStatus }

func (f fakeLoginAPI) QR(context.Context) (QRCode, error) {
	return QRCode{Code: "qr", Image: "https://example.test/qr"}, nil
}
func (f fakeLoginAPI) QRStatus(context.Context, string, string) (LoginStatus, error) {
	return f.status, nil
}
func TestAuthReturnsCredentialsWithoutPersisting(t *testing.T) {
	auth := Auth{api: fakeLoginAPI{LoginStatus{Status: "confirmed", BotID: "bot", UserID: "owner", Token: "secret", BaseURL: DefaultBaseURL}}}
	result, err := auth.Login(context.Background(), platform.LoginRequest{Out: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if result.Credentials["owner"] != "owner" || result.Credentials["secret"] != "secret" {
		t.Fatal("login result missing")
	}
	if result.Settings["baseUrl"] != DefaultBaseURL {
		t.Fatal("base URL missing")
	}
}
func TestAuthFailsForExpiredOrIncompleteLogin(t *testing.T) {
	for _, status := range []LoginStatus{{Status: "expired"}, {Status: "confirmed", Token: "secret"}} {
		_, err := (Auth{api: fakeLoginAPI{status}}).Login(context.Background(), platform.LoginRequest{})
		if err == nil {
			t.Fatal("incomplete login accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (Auth{api: fakeLoginAPI{LoginStatus{Status: "wait"}}}).Login(ctx, platform.LoginRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}
