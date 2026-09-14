package asf

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"steamcli.local/steam/internal/httpx"
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

// Envelope is the shape every ASF IPC response shares.
type Envelope struct {
	Result  json.RawMessage `json:"Result"`
	Message string          `json:"Message"`
	Success *bool           `json:"Success"`
}

// ParsedLine is one bot's extracted value. Bot is empty when the response
// carried a single unkeyed result.
type ParsedLine struct {
	Bot   string
	Value string
}

// Parse reduces an ASF response to the value a caller actually wants.
//
// ASF nests its payload differently per endpoint. A two-factor token arrives as
// Result[bot].Result, an executed command as a bare Result string, and a failed
// operation explains itself in Result[bot].Message or the envelope's Message.
// Parse walks that preference order so callers do not have to. It reports false
// when the body is not an ASF envelope.
func Parse(b []byte) ([]ParsedLine, bool) {
	var env Envelope
	if e := json.Unmarshal(b, &env); e != nil || (env.Success == nil && env.Message == "" && len(env.Result) == 0) {
		return nil, false
	}

	// A scalar Result is the whole answer.
	if s, ok := scalar(env.Result); ok {
		return []ParsedLine{{Value: s}}, true
	}

	// Otherwise Result is keyed by bot name.
	var byBot map[string]json.RawMessage
	if e := json.Unmarshal(env.Result, &byBot); e != nil || len(byBot) == 0 {
		if env.Message != "" {
			return []ParsedLine{{Value: env.Message}}, true
		}
		return nil, false
	}

	names := make([]string, 0, len(byBot))
	for k := range byBot {
		names = append(names, k)
	}
	sort.Strings(names)

	out := make([]ParsedLine, 0, len(names))
	for _, name := range names {
		var inner Envelope
		value := ""
		if json.Unmarshal(byBot[name], &inner) == nil {
			if s, ok := scalar(inner.Result); ok && s != "" {
				value = s
			} else if inner.Message != "" {
				value = inner.Message
			}
		}
		if value == "" {
			if s, ok := scalar(byBot[name]); ok {
				value = s
			} else {
				value = env.Message
			}
		}
		out = append(out, ParsedLine{Bot: name, Value: value})
	}
	return out, true
}

// scalar renders a JSON string, number, or boolean as text.
func scalar(raw json.RawMessage) (string, bool) {
	t := strings.TrimSpace(string(raw))
	if t == "" || t == "null" {
		return "", false
	}
	switch t[0] {
	case '{', '[':
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, true
	}
	return t, true
}
