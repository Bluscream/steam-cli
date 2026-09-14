// Package httpx provides bounded, credential-conscious HTTP requests.
package httpx

import (
	"bytes"
	"context"
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

const MaxBody int64 = 32 << 20

type Client struct {
	HTTP               *http.Client
	Offline, AllowHTTP bool
	Retries            int
}
type StatusError struct {
	Code       int
	RetryAfter string
}

func (e *StatusError) Error() string {
	hint := ""
	switch e.Code {
	case 401:
		hint = " (authentication required or invalid)"
	case 403:
		hint = " (access denied; check permissions or IPC ban)"
	case 429:
		hint = " (rate limited)"
	}
	return fmt.Sprintf("HTTP %d%s", e.Code, hint)
}
func New(timeout time.Duration, offline, allowHTTP bool) *Client {
	return &Client{HTTP: &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, Offline: offline, AllowHTTP: allowHTTP, Retries: 2}
}
func ValidateURL(raw string, allowHTTP bool) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil {
		return nil, errors.New("invalid base URL")
	}
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("base URL must have a host and no credentials, query, or fragment")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	loop := host == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && (loop || allowHTTP)) {
		return nil, errors.New("HTTPS is required outside loopback; use --allow-http only for a trusted network")
	}
	return u, nil
}

// Endpoint appends a relative path and preserves reverse-proxy prefixes.
func Endpoint(base, path string, allowHTTP bool) (string, error) {
	u, e := ValidateURL(base, allowHTTP)
	if e != nil {
		return "", e
	}
	if strings.ContainsAny(path, "\\?#") || strings.Contains(path, ":") {
		return "", errors.New("endpoint must be a relative path without query or fragment")
	}
	decoded, e := url.PathUnescape(path)
	if e != nil {
		return "", errors.New("invalid endpoint escaping")
	}
	if strings.ContainsAny(decoded, "\\?#\x00") {
		return "", errors.New("invalid endpoint path")
	}
	for _, p := range strings.Split(decoded, "/") {
		if p == ".." || p == "." {
			return "", errors.New("endpoint traversal is not allowed")
		}
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(decoded, "/")
	u.RawPath = ""
	return u.String(), nil
}

// Response carries the parts of an HTTP reply callers need beyond the body.
// Steam signals API-level failures in headers while still returning 200.
type Response struct {
	Header http.Header
	Body   []byte
	Status int
}

func (c *Client) Do(ctx context.Context, method, endpoint string, q url.Values, body []byte, headers http.Header) ([]byte, error) {
	r, e := c.DoFull(ctx, method, endpoint, q, body, headers)
	if e != nil {
		return nil, e
	}
	return r.Body, nil
}

// DoFull performs the request and returns the status and headers alongside
// the body.
func (c *Client) DoFull(ctx context.Context, method, endpoint string, q url.Values, body []byte, headers http.Header) (Response, error) {
	if c.Offline {
		return Response{}, errors.New("network disabled by --offline")
	}
	u, e := url.Parse(endpoint)
	if e != nil {
		return Response{}, errors.New("invalid request URL")
	}
	base := *u
	base.RawQuery = ""
	if _, e = ValidateURL(base.String(), c.AllowHTTP); e != nil {
		return Response{}, e
	}
	u.RawQuery = q.Encode()
	for attempt := 0; ; attempt++ {
		req, e := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
		if e != nil {
			return Response{}, errors.New("could not build HTTP request")
		}
		req.Header = headers.Clone()
		if req.Header == nil {
			req.Header = make(http.Header)
		}
		if req.Header.Get("User-Agent") == "" {
			req.Header.Set("User-Agent", "steam-cli/0.1")
		}
		if req.Header.Get("Accept") == "" {
			req.Header.Set("Accept", "application/json")
		}
		resp, e := c.HTTP.Do(req)
		if e != nil {
			if ctx.Err() != nil {
				return Response{}, ctx.Err()
			}
			// net/url errors contain the complete URL, including API keys.
			var ne net.Error
			if errors.As(e, &ne) && ne.Timeout() {
				return Response{}, errors.New("HTTP request timed out")
			}
			return Response{}, errors.New("HTTP transport failed (check network, TLS, and endpoint)")
		}
		if method == http.MethodGet && attempt < c.Retries && (resp.StatusCode == 429 || resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504) {
			delay := time.Duration(1<<attempt) * time.Second
			if s := resp.Header.Get("Retry-After"); s != "" {
				if n, e := strconv.Atoi(s); e == nil {
					delay = time.Duration(n) * time.Second
				} else if t, e := http.ParseTime(s); e == nil {
					delay = time.Until(t)
				}
			}
			if delay < 0 {
				delay = 0
			}
			if delay > 30*time.Second {
				resp.Body.Close()
				return Response{}, &StatusError{resp.StatusCode, resp.Header.Get("Retry-After")}
			}
			resp.Body.Close()
			t := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				t.Stop()
				return Response{}, ctx.Err()
			case <-t.C:
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			return Response{}, &StatusError{resp.StatusCode, resp.Header.Get("Retry-After")}
		}
		b, e := io.ReadAll(io.LimitReader(resp.Body, MaxBody+1))
		resp.Body.Close()
		if e != nil {
			return Response{}, errors.New("could not read HTTP response")
		}
		if int64(len(b)) > MaxBody {
			return Response{}, errors.New("HTTP response exceeds 32 MiB")
		}
		return Response{Header: resp.Header, Body: b, Status: resp.StatusCode}, nil
	}
}
