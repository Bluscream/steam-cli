package asf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	// Otherwise Result may be keyed by bot name. Only treat it that way when
	// each value is itself an envelope carrying a scalar Result or a Message.
	// Endpoints like Api/Bot return a rich object per bot; reducing those to a
	// single line would discard the very data the caller asked for, so they
	// are reported as unparsed and printed as JSON instead.
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
		if json.Unmarshal(byBot[name], &inner) != nil {
			return nil, false
		}
		value, ok := scalar(inner.Result)
		if !ok || value == "" {
			if inner.Message == "" {
				// No per-item result and no message: this is a data object,
				// not an outcome. Let the caller print it in full.
				return nil, false
			}
			value = inner.Message
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

// BotSummary is the handful of fields worth showing per bot in a listing.
type BotSummary struct {
	Name           string
	SteamID        string
	Connected      bool
	Farming        bool
	GamesRemaining int
	CardsRemaining int
}

// Bots extracts a per-bot summary from an Api/Bot response. It reports false
// when the payload is not a bot listing, so the caller can print it verbatim.
func Bots(b []byte) ([]BotSummary, bool) {
	var env struct {
		Result map[string]struct {
			BotName     string `json:"BotName"`
			SteamID     string `json:"s_SteamID"`
			Nickname    string `json:"Nickname"`
			Connected   bool   `json:"IsConnectedAndLoggedOn"`
			KeepRunning bool   `json:"KeepRunning"`
			CardsFarmer struct {
				Paused      bool `json:"Paused"`
				NowFarming  bool `json:"NowFarming"`
				GamesToFarm []struct {
					AppID          uint32 `json:"AppID"`
					CardsRemaining int    `json:"CardsRemaining"`
				} `json:"GamesToFarm"`
			} `json:"CardsFarmer"`
		} `json:"Result"`
	}
	if json.Unmarshal(b, &env) != nil || len(env.Result) == 0 {
		return nil, false
	}

	names := make([]string, 0, len(env.Result))
	for k := range env.Result {
		names = append(names, k)
	}
	sort.Strings(names)

	out := make([]BotSummary, 0, len(names))
	for _, n := range names {
		r := env.Result[n]
		if r.BotName == "" {
			// Not a bot object; let the caller print the payload as-is.
			return nil, false
		}
		cards := 0
		for _, g := range r.CardsFarmer.GamesToFarm {
			cards += g.CardsRemaining
		}
		out = append(out, BotSummary{
			Name:           r.BotName,
			SteamID:        r.SteamID,
			Connected:      r.Connected,
			Farming:        r.CardsFarmer.NowFarming,
			GamesRemaining: len(r.CardsFarmer.GamesToFarm),
			CardsRemaining: cards,
		})
	}
	return out, true
}

// SetNickname executes ASF !nickname command.
func (c *Client) SetNickname(ctx context.Context, bot, name string) (string, error) {
	if bot == "" {
		bot = "ASF"
	}
	cmd := fmt.Sprintf("nickname %s %s", bot, name)
	b, err := c.Command(ctx, cmd)
	if err != nil {
		return "", err
	}
	lines, ok := Parse(b)
	if ok && len(lines) > 0 {
		return lines[0].Value, nil
	}
	return string(b), nil
}

// SetPrivacy executes ASF !privacy command.
func (c *Client) SetPrivacy(ctx context.Context, bot, settings string) (string, error) {
	if bot == "" {
		bot = "ASF"
	}
	cmd := fmt.Sprintf("privacy %s %s", bot, settings)
	b, err := c.Command(ctx, cmd)
	if err != nil {
		return "", err
	}
	lines, ok := Parse(b)
	if ok && len(lines) > 0 {
		return lines[0].Value, nil
	}
	return string(b), nil
}

// Play executes ASF !play command to idle games or set custom display game text.
func (c *Client) Play(ctx context.Context, bot string, appIDs []int, customName string) (string, error) {
	if bot == "" {
		bot = "ASF"
	}
	var parts []string
	for _, id := range appIDs {
		parts = append(parts, fmt.Sprintf("%d", id))
	}
	arg := strings.Join(parts, ",")
	if customName != "" {
		if arg == "" {
			arg = "0"
		}
		arg = arg + " " + customName
	}
	if arg == "" {
		return "", errors.New("play requires at least one AppID or a custom game name")
	}
	cmd := fmt.Sprintf("play %s %s", bot, arg)
	b, err := c.Command(ctx, cmd)
	if err != nil {
		return "", err
	}
	lines, ok := Parse(b)
	if ok && len(lines) > 0 {
		return lines[0].Value, nil
	}
	return string(b), nil
}

// Resume executes ASF !resume command to return bots to normal farming.
func (c *Client) Resume(ctx context.Context, bot string) (string, error) {
	if bot == "" {
		bot = "ASF"
	}
	cmd := fmt.Sprintf("resume %s", bot)
	b, err := c.Command(ctx, cmd)
	if err != nil {
		return "", err
	}
	lines, ok := Parse(b)
	if ok && len(lines) > 0 {
		return lines[0].Value, nil
	}
	return string(b), nil
}
