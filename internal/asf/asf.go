package asf

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"steamcli.local/steam/internal/httpx"
	"strings"
)

type Client struct {
	HTTP              *httpx.Client
	BaseURL, Password string
}

func (c *Client) Call(ctx context.Context, method, path string, q url.Values, body []byte) ([]byte, error) {
	method = strings.ToUpper(method)
	switch method {
	case "GET", "POST", "PUT", "DELETE", "PATCH":
	default:
		return nil, errors.New("unsupported HTTP method")
	}
	path = strings.TrimLeft(path, "/")
	if lower := strings.ToLower(path); !strings.HasPrefix(lower, "api/") && lower != "api" && !strings.HasPrefix(lower, "swagger/") {
		return nil, errors.New("ASF endpoint must begin with Api/ or swagger/")
	}
	endpoint, e := httpx.Endpoint(c.BaseURL, path, c.HTTP.AllowHTTP)
	if e != nil {
		return nil, e
	}
	if len(body) > 0 && !json.Valid(body) {
		return nil, errors.New("ASF request body must be valid JSON")
	}
	h := make(http.Header)
	if c.Password != "" {
		h.Set("Authentication", c.Password)
	}
	if len(body) > 0 {
		h.Set("Content-Type", "application/json")
	}
	b, e := c.HTTP.Do(ctx, method, endpoint, q, body, h)
	if e != nil {
		return nil, e
	}
	var envelope struct {
		Success *bool `json:"Success"`
	}
	if json.Unmarshal(b, &envelope) == nil && envelope.Success != nil && !*envelope.Success {
		return b, errors.New("ASF returned Success=false; inspect the JSON response")
	}
	return b, nil
}
func (c *Client) Command(ctx context.Context, command string) ([]byte, error) {
	b, _ := json.Marshal(map[string]string{"Command": command})
	return c.Call(ctx, "POST", "Api/Command", nil, b)
}
func BotPath(bots, action string) (string, error) {
	if strings.TrimSpace(bots) == "" || strings.ContainsAny(bots, "/\\?#%\r\n\x00") {
		return "", errors.New("invalid bot selector")
	}
	p := "Api/Bot/" + url.PathEscape(bots)
	if action != "" {
		p += "/" + action
	}
	return p, nil
}
