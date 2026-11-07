package weixin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestClientLoginAndMessagingProtocol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("iLink-App-Id") != "bot" || r.Header.Get("iLink-App-ClientVersion") != "132104" {
			t.Errorf("app headers: %v", r.Header)
		}
		if r.URL.Path == "/ilink/bot/get_qrcode_status" {
			if r.Method != "GET" || r.URL.Query().Get("qrcode") != "qr&code" || r.URL.Query().Get("verify_code") != "1234" {
				t.Errorf("QR status request: %s", r.URL)
			}
			if r.Header.Get("Authorization") != "" || r.Header.Get("AuthorizationType") != "" || r.Header.Get("X-WECHAT-UIN") != "" {
				t.Error("QR status leaked auth headers")
			}
			fmt.Fprint(w, `{"status":"confirmed","bot_token":"token","ilink_bot_id":"bot","ilink_user_id":"user","baseurl":"https://ilinkai.weixin.qq.com"}`)
			return
		}
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("AuthorizationType") != "ilink_bot_token" {
			t.Errorf("POST headers: %v", r.Header)
		}
		uin, err := base64.StdEncoding.DecodeString(r.Header.Get("X-WECHAT-UIN"))
		if err != nil {
			t.Error(err)
		}
		if _, err := strconv.ParseUint(string(uin), 10, 32); err != nil {
			t.Errorf("uin: %v", err)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		switch r.URL.Path {
		case "/ilink/bot/get_bot_qrcode":
			if r.URL.Query().Get("bot_type") != "3" || r.Header.Get("Authorization") != "" {
				t.Error("QR request must be unauthenticated")
			}
			if tokens, ok := body["local_token_list"].([]any); !ok || len(tokens) != 0 {
				t.Error("missing token list")
			}
			if body["base_info"] != nil {
				t.Error("QR body must omit base_info")
			}
			fmt.Fprint(w, `{"qrcode":"qr&code","qrcode_img_content":"https://example.test/qr"}`)
		case "/ilink/bot/getupdates":
			if r.Header.Get("Authorization") != "Bearer private-token" || body["get_updates_buf"] != "cursor-in" {
				t.Error("updates auth/cursor")
			}
			base, _ := body["base_info"].(map[string]any)
			if base["channel_version"] != "2.4.8" || base["bot_agent"] != "AgentDock/0.1.0" {
				t.Errorf("base info: %v", base)
			}
			fmt.Fprint(w, `{"ret":0,"get_updates_buf":"cursor-out","msgs":[{"message_id":9007199254740993,"from_user_id":"user","to_user_id":"bot","message_type":1,"message_state":2,"context_token":"context-secret","create_time_ms":1234,"item_list":[{"type":1,"text_item":{"text":"hi"}}]}]}`)
		case "/ilink/bot/sendmessage":
			msg, _ := body["msg"].(map[string]any)
			if msg["from_user_id"] != "" || msg["to_user_id"] != "user" || msg["context_token"] != "context-secret" || msg["client_id"] != "send-id" || msg["message_type"] != float64(2) || msg["message_state"] != float64(2) {
				t.Errorf("send: %v", msg)
			}
			items, _ := msg["item_list"].([]any)
			if len(items) != 1 || items[0].(map[string]any)["text_item"].(map[string]any)["text"] != "hello" {
				t.Errorf("items: %v", items)
			}
			fmt.Fprint(w, `{"ret":0}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	c, err := NewClient(server.URL, "private-token", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	qr, err := c.QR(context.Background())
	if err != nil || qr.Code != "qr&code" {
		t.Fatalf("QR: %+v %v", qr, err)
	}
	status, err := c.QRStatus(context.Background(), qr.Code, "1234")
	if err != nil || status.Token != "token" || status.UserID != "user" {
		t.Fatalf("status: %+v %v", status, err)
	}
	updates, err := c.Updates(context.Background(), "cursor-in")
	if err != nil {
		t.Fatal(err)
	}
	if updates.Cursor != "cursor-out" || len(updates.Messages) != 1 || updates.Messages[0].ID.String() != "9007199254740993" || updates.Messages[0].Items[0].TextItem.Text != "hi" {
		t.Fatalf("updates: %+v", updates)
	}
	if err := c.SendText(context.Background(), "user", "context-secret", "send-id", "hello"); err != nil {
		t.Fatal(err)
	}
}

func TestClientErrorsAreSanitized(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		expired    bool
	}{
		{"expired", `{"ret":-14,"errmsg":"private-token"}`, 200, true},
		{"errcode", `{"ret":0,"errcode":-14}`, 200, true},
		{"api", `{"ret":-2,"errmsg":"private-token"}`, 200, false},
		{"http", `private-token`, 500, false},
		{"malformed", `private-token`, 200, false},
		{"empty", ``, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
			defer server.Close()
			c, _ := NewClient(server.URL, "private-token", server.Client())
			_, err := c.Updates(context.Background(), "cursor-secret")
			if err == nil || strings.Contains(err.Error(), "private-token") || strings.Contains(err.Error(), "cursor-secret") || strings.Contains(err.Error(), server.URL) {
				t.Fatalf("unsafe error: %v", err)
			}
			if errors.Is(err, ErrSessionExpired) != tc.expired {
				t.Fatalf("session expired: %v", err)
			}
		})
	}
}

func TestClientURLValidationAndRedirect(t *testing.T) {
	for _, url := range []string{"http://ilinkai.weixin.qq.com", "https://example.com", "https://weixin.qq.com.evil.test", "https://u:p@ilinkai.weixin.qq.com", "https://ilinkai.weixin.qq.com/path", "https://ilinkai.weixin.qq.com?token=secret", "http://127.0.0.1:99"} {
		if _, err := NewClient(url, "token", nil); err == nil {
			t.Errorf("accepted unsafe URL %s", url)
		}
	}
	for _, url := range []string{"", "https://ilinkai.weixin.qq.com", "https://api.ilinkai.weixin.qq.com/"} {
		if _, err := NewClient(url, "token", nil); err != nil {
			t.Errorf("rejected URL %s: %v", url, err)
		}
	}
	calls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; fmt.Fprint(w, `{"ret":0}`) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	c, _ := NewClient(source.URL, "private-token", source.Client())
	if _, err := c.Updates(context.Background(), ""); err == nil || calls != 0 {
		t.Fatalf("redirect followed: calls=%d err=%v", calls, err)
	}
}

func TestClientCancellation(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer func() { close(release); server.Close() }()
	c, _ := NewClient(server.URL, "private-token", server.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := c.Updates(ctx, "")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
