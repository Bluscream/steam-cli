package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
	"steamcli.local/steam/internal/config"
	"steamcli.local/steam/internal/httpx"
)

// Session represents an authenticated Steam Community and Web session.
type Session struct {
	SteamID          string `json:"steamid"`
	AccountName      string `json:"account_name,omitempty"`
	RefreshToken     string `json:"refresh_token,omitempty"`
	AccessToken      string `json:"access_token,omitempty"`
	SteamLoginSecure string `json:"steam_login_secure"`
}

// Client handles interactive and automated Steam authentication.
type Client struct {
	HTTP    *httpx.Client
	BaseURL string
}

func NewClient(h *httpx.Client) *Client {
	return &Client{
		HTTP:    h,
		BaseURL: "https://api.steampowered.com",
	}
}

// BeginQR starts an auth session with Steam's mobile QR challenge.
func (c *Client) BeginQR(ctx context.Context) (clientID string, challengeURL string, requestID string, interval time.Duration, err error) {
	endpoint, err := httpx.Endpoint(c.BaseURL, "IAuthenticationService/BeginAuthSessionViaQR/v1/", false)
	if err != nil {
		return "", "", "", 0, err
	}

	form := url.Values{
		"device_details[device_friendly_name]": {"steam-cli"},
		"device_details[platform_type]":        {"1"},
	}

	res, err := c.HTTP.DoFull(ctx, http.MethodPost, endpoint, nil, []byte(form.Encode()), map[string][]string{
		"Content-Type": {"application/x-www-form-urlencoded"},
	})
	if err != nil {
		return "", "", "", 0, err
	}

	var raw struct {
		Response struct {
			ClientID     string `json:"client_id"`
			ChallengeURL string `json:"challenge_url"`
			RequestID    string `json:"request_id"`
			Interval     int    `json:"interval"`
		} `json:"response"`
	}

	if err := json.Unmarshal(res.Body, &raw); err != nil {
		return "", "", "", 0, fmt.Errorf("decode qr response: %w", err)
	}

	if raw.Response.ClientID == "" || raw.Response.ChallengeURL == "" {
		return "", "", "", 0, errors.New("steam returned an invalid QR auth session")
	}

	pollInterval := time.Duration(raw.Response.Interval) * time.Second
	if pollInterval < time.Second {
		pollInterval = 5 * time.Second
	}

	return raw.Response.ClientID, raw.Response.ChallengeURL, raw.Response.RequestID, pollInterval, nil
}

// PollQR polls until the mobile app approves the QR challenge or context expires.
func (c *Client) PollQR(ctx context.Context, clientID, requestID string, interval time.Duration) (*Session, error) {
	endpoint, err := httpx.Endpoint(c.BaseURL, "IAuthenticationService/PollAuthSessionStatus/v1/", false)
	if err != nil {
		return nil, err
	}

	form := url.Values{
		"client_id":  {clientID},
		"request_id": {requestID},
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			res, err := c.HTTP.DoFull(ctx, http.MethodPost, endpoint, nil, []byte(form.Encode()), map[string][]string{
				"Content-Type": {"application/x-www-form-urlencoded"},
			})
			if err != nil {
				continue
			}

			var raw struct {
				Response struct {
					RefreshToken string `json:"refresh_token"`
					AccessToken  string `json:"access_token"`
					AccountName  string `json:"account_name"`
				} `json:"response"`
			}

			if err := json.Unmarshal(res.Body, &raw); err != nil {
				continue
			}

			if raw.Response.RefreshToken != "" {
				return c.FinalizeSession(ctx, raw.Response.RefreshToken, raw.Response.AccessToken, raw.Response.AccountName)
			}
		}
	}
}

// FinalizeSession turns a Steam RefreshToken / AccessToken into a valid steamLoginSecure session.
func (c *Client) FinalizeSession(ctx context.Context, refreshToken, accessToken, accountName string) (*Session, error) {
	sess := &Session{
		AccountName:  accountName,
		RefreshToken: refreshToken,
		AccessToken:  accessToken,
	}

	// 1. Extract SteamID from JWT
	parts := strings.Split(accessToken, ".")
	if len(parts) >= 2 {
		payload, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err == nil {
			var jwtClaims struct {
				Sub string `json:"sub"`
			}
			if err := json.Unmarshal(payload, &jwtClaims); err == nil && jwtClaims.Sub != "" {
				sess.SteamID = jwtClaims.Sub
			}
		}
	}

	if sess.SteamID != "" && accessToken != "" {
		sess.SteamLoginSecure = sess.SteamID + "%7C%7C" + accessToken
	}

	return sess, nil
}

// RenderTerminalQR renders an ASCII QR code to a writer.
func RenderTerminalQR(w io.Writer, content string) error {
	qr, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, qr.ToSmallString(false))
	return nil
}

// OpenBrowser opens a URL using the OS default browser.
func OpenBrowser(targetURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", targetURL)
	case "darwin":
		cmd = exec.Command("open", targetURL)
	default:
		cmd = exec.Command("xdg-open", targetURL)
	}
	return cmd.Start()
}

// BrowserFlow launches a local loopback server and instructions for the user to submit steamLoginSecure.
func BrowserFlow(ctx context.Context, port int) (*Session, error) {
	if port <= 0 {
		port = 20888
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		// Fallback to random port if 20888 is busy
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("listen loopback: %w", err)
		}
	}
	defer listener.Close()

	actualPort := listener.Addr().(*net.TCPAddr).Port
	authURL := fmt.Sprintf("http://127.0.0.1:%d/", actualPort)

	resultChan := make(chan *Session, 1)
	errChan := make(chan error, 1)

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head>
<title>steam-cli Authentication</title>
<style>
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; background: #171d25; color: #c5c3c0; margin: 40px auto; max-width: 650px; padding: 20px; line-height: 1.6; }
h1 { color: #fff; border-bottom: 2px solid #66c0f4; padding-bottom: 8px; }
input[type=text] { width: 100%; padding: 12px; margin: 10px 0; box-sizing: border-box; background: #2a475e; border: 1px solid #101822; color: #fff; border-radius: 4px; font-family: monospace; font-size: 14px; }
button { background: linear-gradient( to right, #47bfff 5%, #1a44c2 95%); color: #fff; padding: 12px 24px; border: none; border-radius: 3px; cursor: pointer; font-size: 16px; font-weight: bold; }
button:hover { background: #66c0f4; }
.card { background: #1b2838; padding: 20px; border-radius: 6px; box-shadow: 0 4px 8px rgba(0,0,0,0.3); margin-top: 20px; }
a { color: #66c0f4; text-decoration: none; }
a:hover { text-decoration: underline; }
code { background: #0e141b; padding: 2px 6px; border-radius: 3px; color: #a4d007; font-family: monospace; }
</style>
</head>
<body>
<h1>steam-cli Session Authorization</h1>
<div class="card">
<p>Sign in to <a href="https://steamcommunity.com" target="_blank"><strong>steamcommunity.com</strong></a> in your browser.</p>
<p>Then copy your <code>steamLoginSecure</code> cookie (from <em>DevTools (F12) &gt; Storage/Application &gt; Cookies</em>) and paste it below:</p>
<form method="POST" action="/submit">
<label for="cookie"><strong>steamLoginSecure Cookie Value:</strong></label><br>
<input type="text" id="cookie" name="cookie" placeholder="76561198...||eyAidHlwIjog..." required autocomplete="off">
<br><br>
<button type="submit">Authorize steam-cli</button>
</form>
</div>
</body>
</html>`)
				return
			}

			if r.Method == http.MethodPost && r.URL.Path == "/submit" {
				if err := r.ParseForm(); err != nil {
					http.Error(w, "invalid form", http.StatusBadRequest)
					return
				}
				cookie := strings.TrimSpace(r.FormValue("cookie"))
				if cookie == "" {
					http.Error(w, "cookie cannot be empty", http.StatusBadRequest)
					return
				}

				// Basic validation
				steamID := ""
				val := cookie
				if d, e := url.QueryUnescape(val); e == nil {
					val = d
				}
				parts := strings.Split(val, "||")
				if len(parts) > 0 {
					steamID = strings.TrimSpace(parts[0])
				}

				sess := &Session{
					SteamID:          steamID,
					SteamLoginSecure: cookie,
				}

				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head><title>Success</title><style>body { font-family: sans-serif; background: #171d25; color: #a4d007; text-align: center; padding: 50px; }</style></head>
<body>
<h2>&#10004; Authentication Received!</h2>
<p style="color: #c5c3c0;">You can now close this browser tab and return to your terminal.</p>
</body>
</html>`)
				resultChan <- sess
				return
			}

			http.NotFound(w, r)
		}),
	}

	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errChan <- err
		}
	}()

	_ = OpenBrowser(authURL)

	select {
	case <-ctx.Done():
		_ = server.Shutdown(context.Background())
		return nil, ctx.Err()
	case err := <-errChan:
		return nil, err
	case sess := <-resultChan:
		_ = server.Shutdown(context.Background())
		return sess, nil
	}
}

// SaveSession saves the captured session to ~/.local/share/steam-cli/session.json and sets community secret file.
func SaveSession(s *Session) error {
	_, dataDir, _, err := config.Paths()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return err
	}

	// Save session.json
	sessPath := filepath.Join(dataDir, "session.json")
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(sessPath, b, 0600); err != nil {
		return err
	}

	// Also write plain steam_login_secure secret file
	cookiePath := filepath.Join(dataDir, "steam_login_secure")
	return os.WriteFile(cookiePath, []byte(s.SteamLoginSecure), 0600)
}
