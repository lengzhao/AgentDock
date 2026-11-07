package lark

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	larksdk "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkapplication "github.com/larksuite/oapi-sdk-go/v3/service/application/v6"
)

// LookupApplicationOwner queries only this application's information. The
// returned ID is scoped to this app and can be compared to inbound open_id.
func LookupApplicationOwner(ctx context.Context, kind, appID, appSecret string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	baseURL := "https://open.larksuite.com"
	switch kind {
	case "lark":
	case "feishu":
		baseURL = "https://open.feishu.cn"
	default:
		return "", fmt.Errorf("unsupported platform kind %q", kind)
	}
	client := larksdk.NewClient(appID, appSecret,
		larksdk.WithOpenBaseUrl(baseURL), larksdk.WithHttpClient(http.DefaultClient),
		larksdk.WithEnableTokenCache(false), larksdk.WithLogLevel(larkcore.LogLevelError))
	// Keep the short-lived token local to this lookup, outside global SDK caches.
	token, err := client.GetTenantAccessTokenBySelfBuiltApp(ctx, &larkcore.SelfBuiltTenantAccessTokenReq{AppID: appID, AppSecret: appSecret})
	if err != nil {
		return "", fmt.Errorf("obtain application token: %w", err)
	}
	if token == nil {
		return "", fmt.Errorf("application token response is empty")
	}
	if !token.Success() || token.TenantAccessToken == "" {
		return "", fmt.Errorf("application token request failed (code %d)", token.Code)
	}
	resp, err := client.Application.V6.Application.Get(ctx,
		larkapplication.NewGetApplicationReqBuilder().AppId("me").Lang("en_us").UserIdType("open_id").Build(),
		larkcore.WithTenantAccessToken(token.TenantAccessToken))
	if err != nil {
		return "", fmt.Errorf("query application owner: %w", err)
	}
	if resp == nil {
		return "", fmt.Errorf("application information response is empty")
	}
	if !resp.Success() {
		return "", fmt.Errorf("application owner request failed (code %d)", resp.Code)
	}
	if resp.Data == nil || resp.Data.App == nil {
		return "", fmt.Errorf("application information contains no app")
	}
	app := resp.Data.App
	var owner string
	if app.Owner != nil {
		if app.Owner.OwnerId != nil {
			owner = *app.Owner.OwnerId
		}
	} else if app.CreatorId != nil {
		// The API documents creator_id as the application creator (owner).
		owner = *app.CreatorId
	}
	if !strings.HasPrefix(owner, "ou_") || len(owner) <= len("ou_") {
		return "", fmt.Errorf("application information contains no owner open_id")
	}
	return owner, nil
}
