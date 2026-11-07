package lark

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/lengzhao/AgentDock/credentials"
)

type registrationTransport func(*http.Request) (*http.Response, error)

func (f registrationTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRegistrationBootstrapsOnFeishuAndConfirmsOnSelectedBrand(t *testing.T) {
	for _, tc := range []struct{ kind, brand, pageHost string }{
		{"lark", "lark", "open.larksuite.com"},
		{"lark", "", "open.larksuite.com"},
		{"feishu", "feishu", "open.feishu.cn"},
	} {
		t.Run(tc.kind+"/"+tc.brand, func(t *testing.T) {
			previous := http.DefaultClient
			t.Cleanup(func() { http.DefaultClient = previous })
			polls := 0
			http.DefaultClient = &http.Client{Transport: registrationTransport(func(r *http.Request) (*http.Response, error) {
				if err := r.ParseForm(); err != nil {
					t.Fatal(err)
				}
				body := ""
				switch r.Form.Get("action") {
				case "begin":
					if r.URL.Host != "accounts.feishu.cn" {
						t.Errorf("bootstrap host = %s; want accounts.feishu.cn", r.URL.Host)
					}
					body = `{"device_code":"test-device","verification_uri_complete":"https://open.feishu.cn/page/launcher?user_code=test-code","expire_in":600}`
				case "poll":
					polls++
					wantHost := "accounts.feishu.cn"
					if polls > 1 {
						wantHost = "accounts.larksuite.com"
					}
					if r.URL.Host != wantHost {
						t.Errorf("poll %d host = %s; want %s", polls, r.URL.Host, wantHost)
					}
					if r.Form.Get("device_code") != "test-device" {
						t.Error("device code changed")
					}
					if tc.kind == "lark" && polls == 1 {
						body = `{"error":"authorization_pending","user_info":{"tenant_brand":"lark"}}`
					} else {
						body = `{"client_id":"test-app","client_secret":"test-secret"}`
					}
				default:
					t.Fatalf("unexpected action %q", r.Form.Get("action"))
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			var page string
			store := credentials.NewMemoryStore()
			reg, err := RegisterApp(context.Background(), RegisterOptions{
				Installation: "test-install", Kind: tc.kind, TenantBrand: tc.brand, NoAddons: true, Store: store,
				OpenURL: func(u string) { page = u },
			})
			if err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(page)
			if err != nil {
				t.Fatal(err)
			}
			if u.Host != tc.pageHost {
				t.Errorf("confirmation host = %s; want %s", u.Host, tc.pageHost)
			}
			if u.Query().Get("user_code") != "test-code" {
				t.Error("confirmation user code changed")
			}
			if got, err := credentials.NewResolver(store, nil).Resolve(context.Background(), reg.References["secret"]); err != nil || got != "test-secret" {
				t.Fatal("credentials were not stored")
			}
		})
	}
}
