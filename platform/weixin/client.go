// Package weixin implements the Weixin ClawBot iLink transport in Go.
package weixin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultBaseURL = "https://ilinkai.weixin.qq.com"
	// ProtocolVersion pins the public upstream client protocol this adapter follows.
	ProtocolVersion  = "2.4.8"
	maxResponseBytes = 4 << 20
)

// ErrSessionExpired means the account must be authenticated again.
var ErrSessionExpired = errors.New("weixin session expired; run agentdock setup --platform weixin to log in again")

// Client is a concurrency-safe HTTP protocol client. It never retries sends.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// ValidBaseURL checks a production iLink endpoint without leaking the supplied URL.
func ValidBaseURL(raw string) error {
	_, err := parseBaseURL(raw, false)
	return err
}

func parseBaseURL(raw string, allowLoopback bool) (*url.URL, error) {
	if raw == "" {
		raw = DefaultBaseURL
	}
	u, err := url.Parse(raw)
	if err != nil || u == nil {
		return nil, errors.New("invalid weixin API base URL")
	}
	host := strings.ToLower(u.Hostname())
	official := host == "weixin.qq.com" || strings.HasSuffix(host, ".weixin.qq.com")
	loopback := false
	if ip := net.ParseIP(host); ip != nil {
		loopback = ip.IsLoopback()
	}
	loopback = loopback || host == "localhost"
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" {
		return nil, errors.New("weixin API base URL must contain only an HTTPS origin")
	}
	if !(official && u.Scheme == "https" && (u.Port() == "" || u.Port() == "443")) && !(allowLoopback && loopback && (u.Scheme == "http" || u.Scheme == "https")) {
		return nil, errors.New("weixin API base URL must be an official HTTPS weixin.qq.com origin")
	}
	u.Path = ""
	return u, nil
}

// NewClient builds an iLink client. A supplied HTTP client is copied, and its
// redirect handler is replaced to prevent credential forwarding. Explicit clients
// may use loopback endpoints for tests; production callers should pass nil.
func NewClient(baseURL, token string, httpClient *http.Client) (*Client, error) {
	u, err := parseBaseURL(baseURL, httpClient != nil)
	if err != nil {
		return nil, err
	}
	if strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("invalid weixin bot token")
	}
	h := http.Client{Timeout: 45 * time.Second}
	if httpClient != nil {
		h = *httpClient
		if h.Timeout == 0 {
			h.Timeout = 45 * time.Second
		}
	}
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{baseURL: strings.TrimRight(u.String(), "/"), token: token, http: &h}, nil
}

type QRCode struct {
	Code  string `json:"qrcode"`
	Image string `json:"qrcode_img_content"`
}
type LoginStatus struct {
	Status       string `json:"status"`
	Token        string `json:"bot_token"`
	BotID        string `json:"ilink_bot_id"`
	UserID       string `json:"ilink_user_id"`
	BaseURL      string `json:"baseurl"`
	RedirectHost string `json:"redirect_host"`
}
type Updates struct {
	Messages []Message `json:"msgs"`
	Cursor   string    `json:"get_updates_buf"`
}
type Message struct {
	ID           json.Number `json:"message_id"`
	FromUserID   string      `json:"from_user_id"`
	ToUserID     string      `json:"to_user_id"`
	GroupID      string      `json:"group_id"`
	ContextToken string      `json:"context_token"`
	Type         int         `json:"message_type"`
	State        int         `json:"message_state"`
	Items        []Item      `json:"item_list"`
	CreateTimeMS int64       `json:"create_time_ms"`
}
type Item struct {
	Type     int       `json:"type"`
	TextItem *TextItem `json:"text_item,omitempty"`
}
type TextItem struct {
	Text string `json:"text"`
}

func (c *Client) QR(ctx context.Context) (QRCode, error) {
	var out QRCode
	err := c.request(ctx, http.MethodPost, "/ilink/bot/get_bot_qrcode?bot_type=3", map[string]any{"local_token_list": []string{}}, false, &out)
	if err == nil && (out.Code == "" || out.Image == "") {
		err = errors.New("weixin QR response is incomplete")
	}
	return out, err
}

func (c *Client) QRStatus(ctx context.Context, code, verifyCode string) (LoginStatus, error) {
	var out LoginStatus
	q := url.Values{"qrcode": {code}}
	if verifyCode != "" {
		q.Set("verify_code", verifyCode)
	}
	err := c.request(ctx, http.MethodGet, "/ilink/bot/get_qrcode_status?"+q.Encode(), nil, false, &out)
	if err == nil && out.Status == "" {
		err = errors.New("weixin QR status response is incomplete")
	}
	return out, err
}

func baseInfo() map[string]string {
	return map[string]string{"channel_version": ProtocolVersion, "bot_agent": "AgentDock/0.1.0"}
}

func (c *Client) Updates(ctx context.Context, cursor string) (Updates, error) {
	var out Updates
	err := c.request(ctx, http.MethodPost, "/ilink/bot/getupdates", map[string]any{"get_updates_buf": cursor, "base_info": baseInfo()}, true, &out)
	return out, err
}

func (c *Client) SendText(ctx context.Context, to, contextToken, clientID, text string) error {
	if to == "" || clientID == "" || text == "" {
		return errors.New("weixin text requires recipient, client ID and text")
	}
	msg := map[string]any{
		"from_user_id": "", "to_user_id": to, "client_id": clientID,
		"message_type": 2, "message_state": 2, "context_token": contextToken,
		"item_list": []Item{{Type: 1, TextItem: &TextItem{Text: text}}},
	}
	return c.request(ctx, http.MethodPost, "/ilink/bot/sendmessage", map[string]any{"msg": msg, "base_info": baseInfo()}, true, nil)
}

func (c *Client) request(ctx context.Context, method, path string, payload any, authenticated bool, out any) error {
	if authenticated && c.token == "" {
		return errors.New("weixin bot token is required")
	}
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return errors.New("cannot encode weixin request")
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return errors.New("cannot construct weixin request")
	}
	req.Header.Set("iLink-App-Id", "bot")
	req.Header.Set("iLink-App-ClientVersion", strconv.Itoa(0x020408))
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("AuthorizationType", "ilink_bot_token")
		var random [4]byte
		if _, err := rand.Read(random[:]); err != nil {
			return errors.New("cannot generate weixin request metadata")
		}
		uin := strconv.FormatUint(uint64(binary.LittleEndian.Uint32(random[:])), 10)
		req.Header.Set("X-WECHAT-UIN", base64.StdEncoding.EncodeToString([]byte(uin)))
	}
	if authenticated {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("weixin HTTP request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("weixin HTTP status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("cannot read weixin response")
	}
	if len(data) > maxResponseBytes {
		return errors.New("weixin response exceeds size limit")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return errors.New("invalid weixin JSON response")
	}
	var status struct {
		Ret     int `json:"ret"`
		ErrCode int `json:"errcode"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return errors.New("invalid weixin status response")
	}
	if status.Ret == -14 || status.ErrCode == -14 {
		return ErrSessionExpired
	}
	if status.Ret != 0 || status.ErrCode != 0 {
		return fmt.Errorf("weixin API rejected request (ret=%d, errcode=%d)", status.Ret, status.ErrCode)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return errors.New("invalid weixin response fields")
		}
	}
	return nil
}
