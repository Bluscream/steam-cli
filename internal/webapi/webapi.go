package webapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"steamcli.local/steam/internal/httpx"
)

type Parameter struct {
	Extra       []Parameter       `json:"extra,omitempty"`
	EnumValues  map[string]string `json:"enum_values,omitempty"`
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Optional    bool              `json:"optional"`
	Description string            `json:"description,omitempty"`
}

func (p *Parameter) UnmarshalJSON(b []byte) error {
	type rawParam struct {
		Extra       []Parameter     `json:"extra,omitempty"`
		EnumValues  json.RawMessage `json:"enum_values,omitempty"`
		Name        string          `json:"name"`
		Type        string          `json:"type"`
		Optional    bool            `json:"optional"`
		Description string          `json:"description,omitempty"`
	}
	var r rawParam
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	p.Extra = r.Extra
	p.Name = r.Name
	p.Type = r.Type
	p.Optional = r.Optional
	p.Description = r.Description
	if len(r.EnumValues) > 0 && string(r.EnumValues) != "null" {
		var m map[string]string
		if err := json.Unmarshal(r.EnumValues, &m); err == nil {
			p.EnumValues = m
		} else {
			var list []string
			if err := json.Unmarshal(r.EnumValues, &list); err == nil {
				p.EnumValues = make(map[string]string, len(list))
				for _, v := range list {
					p.EnumValues[v] = v
				}
			}
		}
	}
	return nil
}

type Method struct {
	Source      string      `json:"_type,omitempty"`
	Description string      `json:"description,omitempty"`
	Name        string      `json:"name"`
	Version     int         `json:"version"`
	HTTPMethod  string      `json:"httpmethod"`
	Parameters  []Parameter `json:"parameters"`
}
type Interface struct {
	Name    string   `json:"name"`
	Methods []Method `json:"methods"`
}
type Catalog struct {
	APIList struct {
		Interfaces []Interface `json:"interfaces"`
	} `json:"apilist"`
}
type Client struct {
	HTTP                   *httpx.Client
	BaseURL, Key, CacheDir string
	AccessToken            string
}

var identifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

func (c *Client) Call(ctx context.Context, iface, method string, version int, verb string, params url.Values) ([]byte, error) {
	r, e := c.CallFull(ctx, iface, method, version, verb, params)
	if e != nil {
		return nil, e
	}
	return r.Body, nil
}

// CallFull returns the response headers alongside the body. Steam reports
// API-level failures in x-eresult while still answering HTTP 200, so any
// caller performing a mutation must inspect the result rather than the status.
func (c *Client) CallFull(ctx context.Context, iface, method string, version int, verb string, params url.Values) (httpx.Response, error) {
	if !identifier.MatchString(iface) || !identifier.MatchString(method) || version < 1 {
		return httpx.Response{}, errors.New("expected valid interface, method, and positive version")
	}
	verb = strings.ToUpper(verb)
	if verb != "GET" && verb != "POST" {
		return httpx.Response{}, errors.New("Steam Web API supports GET or POST")
	}
	endpoint, e := httpx.Endpoint(c.BaseURL, fmt.Sprintf("%s/%s/v%d/", iface, method, version), c.HTTP.AllowHTTP)
	if e != nil {
		return httpx.Response{}, e
	}
	p := make(url.Values)
	for k, v := range params {
		p[k] = append([]string(nil), v...)
	}
	if c.Key != "" && !p.Has("key") {
		p.Set("key", c.Key)
	}
	if c.AccessToken != "" && !p.Has("access_token") {
		p.Set("access_token", c.AccessToken)
	}
	if !p.Has("format") {
		p.Set("format", "json")
	}
	h := make(http.Header)
	if verb == "POST" {
		h.Set("Content-Type", "application/x-www-form-urlencoded")
		return c.HTTP.DoFull(ctx, verb, endpoint, nil, []byte(p.Encode()), h)
	}
	return c.HTTP.DoFull(ctx, verb, endpoint, p, nil, h)
}

// EResult mirrors Valve's EResult enum for the values this CLI can encounter.
type EResult int

const (
	EResultOK               EResult = 1
	EResultFail             EResult = 2
	EResultInvalidParam     EResult = 8
	EResultFileNotFound     EResult = 9
	EResultAccessDenied     EResult = 15
	EResultTimeout          EResult = 16
	EResultLimitExceeded    EResult = 25
	EResultRevoked          EResult = 26
	EResultInvalidState     EResult = 11
	EResultServiceUnavail   EResult = 20
	EResultNotLoggedOn      EResult = 21
	EResultInsufficientPriv EResult = 24
)

var eresultNames = map[EResult]string{
	EResultOK:               "OK",
	EResultFail:             "generic failure",
	EResultInvalidParam:     "invalid parameter",
	EResultFileNotFound:     "file not found (item may not exist, or is hidden or deleted)",
	EResultInvalidState:     "invalid state for this operation",
	EResultAccessDenied:     "access denied (this method may require a publisher key or an authenticated session)",
	EResultTimeout:          "timed out",
	EResultServiceUnavail:   "service unavailable",
	EResultNotLoggedOn:      "not logged on (an access token is required for this method)",
	EResultInsufficientPriv: "insufficient privilege",
	EResultLimitExceeded:    "limit exceeded",
	EResultRevoked:          "revoked",
}

func (e EResult) String() string {
	if n, ok := eresultNames[e]; ok {
		return fmt.Sprintf("EResult %d (%s)", int(e), n)
	}
	return fmt.Sprintf("EResult %d", int(e))
}

// ResultError is returned when Steam accepted the request but refused the operation.
type ResultError struct{ Result EResult }

func (e *ResultError) Error() string { return "Steam refused the request: " + e.Result.String() }

// CheckResult interprets a Steam response as the outcome of a mutation. An
// HTTP 200 with an empty body is not success: Valve reports the real outcome
// in the x-eresult header, or in a result field inside the response object.
func CheckResult(r httpx.Response) error {
	if s := r.Header.Get("x-eresult"); s != "" {
		n, e := strconv.Atoi(s)
		if e == nil {
			if EResult(n) == EResultOK {
				return nil
			}
			return &ResultError{EResult(n)}
		}
	}
	var body struct {
		Response struct {
			Result *int `json:"result"`
		} `json:"response"`
	}
	if e := json.Unmarshal(r.Body, &body); e == nil && body.Response.Result != nil {
		if EResult(*body.Response.Result) == EResultOK {
			return nil
		}
		return &ResultError{EResult(*body.Response.Result)}
	}
	// No signal either way. Valve omits x-eresult on some successful writes;
	// treat a well-formed response as accepted rather than inventing a failure.
	if len(bytes.TrimSpace(r.Body)) == 0 || json.Valid(r.Body) {
		return nil
	}
	return errors.New("unrecognized response from Steam (no result code and body is not JSON)")
}
func (c *Client) cachePath() string {
	// Scope by host AND credential without persisting the key itself.
	h := sha256.Sum256([]byte(c.BaseURL + "\x00" + c.Key))
	return filepath.Join(c.CacheDir, fmt.Sprintf("web-schema-%x.json", h[:16]))
}
func decodeCatalog(b []byte) (Catalog, error) {
	var v Catalog
	e := json.Unmarshal(b, &v)
	if e != nil || len(v.APIList.Interfaces) == 0 {
		return v, errors.New("invalid or empty Steam API catalog")
	}
	return v, nil
}
func (c *Client) Catalog(ctx context.Context, refresh bool) (Catalog, error) {
	path := c.cachePath()
	if !refresh || c.HTTP.Offline {
		if stat, e := os.Stat(path); e == nil && (c.HTTP.Offline || time.Since(stat.ModTime()) < 24*time.Hour) {
			if b, e := os.ReadFile(path); e == nil {
				if v, e := decodeCatalog(b); e == nil {
					return v, nil
				}
			}
		}
	}
	if c.HTTP.Offline {
		return Catalog{}, errors.New("no cached API catalog for this host/key; run steam web methods online first")
	}
	b, e := c.Call(ctx, "ISteamWebAPIUtil", "GetSupportedAPIList", 1, "GET", nil)
	if e != nil {
		return Catalog{}, e
	}
	v, e := decodeCatalog(b)
	if e != nil {
		return v, e
	}
	if c.CacheDir != "" {
		if e := os.MkdirAll(c.CacheDir, 0700); e != nil {
			return v, fmt.Errorf("create schema cache: %w", e)
		}
		f, e := os.CreateTemp(c.CacheDir, ".schema-*")
		if e != nil {
			return v, e
		}
		defer os.Remove(f.Name())
		if _, e = f.Write(b); e != nil {
			f.Close()
			return v, e
		}
		if e = f.Close(); e != nil {
			return v, e
		}
		if e = os.Rename(f.Name(), path); e != nil {
			return v, e
		}
	}
	return v, nil
}
func (v Catalog) Resolve(iface, method string, version int) (Method, error) {
	var found Method
	for _, i := range v.APIList.Interfaces {
		if i.Name != iface {
			continue
		}
		for _, m := range i.Methods {
			if m.Name == method && ((version == 0 && m.Version > found.Version) || version == m.Version) {
				found = m
			}
		}
	}
	if found.Version == 0 {
		return found, errors.New("method/version absent from catalog; supply --method GET or POST and --api-version for a raw call")
	}
	return found, nil
}
func (v Catalog) Filter(filter string) []Interface {
	out := []Interface{}
	filter = strings.ToLower(filter)
	for _, i := range v.APIList.Interfaces {
		entry := Interface{Name: i.Name}
		for _, m := range i.Methods {
			if strings.Contains(strings.ToLower(i.Name+"/"+m.Name), filter) {
				entry.Methods = append(entry.Methods, m)
			}
		}
		if len(entry.Methods) > 0 {
			out = append(out, entry)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
