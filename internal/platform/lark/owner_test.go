package lark

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestLookupApplicationOwner(t *testing.T) {
	for _, tc := range []struct {
		name, kind, body, want string
		wantErr                bool
	}{
		{"lark owner", "lark", `{"code":0,"data":{"app":{"owner":{"owner_id":"ou_current"},"creator_id":"ou_old"}}}`, "ou_current", false},
		{"feishu creator field", "feishu", `{"code":0,"data":{"app":{"creator_id":"ou_creator"}}}`, "ou_creator", false},
		{"empty owner", "lark", `{"code":0,"data":{"app":{}}}`, "", true},
		{"missing app", "lark", `{"code":0,"data":{}}`, "", true},
		{"permission denied", "lark", `{"code":210508,"msg":"denied"}`, "", true},
		{"wrong id type", "lark", `{"code":0,"data":{"app":{"creator_id":"employee-id"}}}`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := http.DefaultClient
			t.Cleanup(func() { http.DefaultClient = old })
			calls := 0
			http.DefaultClient = &http.Client{Transport: registrationTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				wantHost := "open.larksuite.com"
				if tc.kind == "feishu" {
					wantHost = "open.feishu.cn"
				}
				if r.URL.Host != wantHost {
					t.Errorf("host = %s, want %s", r.URL.Host, wantHost)
				}
				body := tc.body
				if strings.Contains(r.URL.Path, "tenant_access_token") {
					body = `{"code":0,"tenant_access_token":"test-token","expire":7200}`
				} else {
					if r.URL.Path != "/open-apis/application/v6/applications/me" {
						t.Errorf("path = %s", r.URL.Path)
					}
					if r.URL.Query().Get("user_id_type") != "open_id" {
						t.Error("must request open_id")
					}
					if r.Header.Get("Authorization") != "Bearer test-token" {
						t.Error("missing tenant token")
					}
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			got, err := LookupApplicationOwner(context.Background(), tc.kind, "test-app", "test-secret")
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("owner = %q, err = %v; want %q, error=%v", got, err, tc.want, tc.wantErr)
			}
			if calls != 2 {
				t.Errorf("requests = %d, want 2", calls)
			}
		})
	}
}

func TestLookupApplicationOwnerHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := LookupApplicationOwner(ctx, "lark", "test-app", "test-secret")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want cancellation, got %v", err)
	}
}
