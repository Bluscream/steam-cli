package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
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

// RSAPublicKey contains the encrypted parameters from Steam.
type RSAPublicKey struct {
	Mod       *big.Int
	Exp       int
	Timestamp string
}

// GetRSAKey fetches the RSA public key for an account from Steam.
func (c *Client) GetRSAKey(ctx context.Context, accountName string) (*RSAPublicKey, error) {
	endpoint, err := httpx.Endpoint(c.BaseURL, "IAuthenticationService/GetPasswordRSAPublicKey/v1/", false)
	if err != nil {
		return nil, err
	}

	q := url.Values{"account_name": {accountName}}
	res, err := c.HTTP.DoFull(ctx, http.MethodGet, endpoint, q, nil, nil)
	if err != nil {
		return nil, err
	}

	var raw struct {
		Response struct {
			PublickeyMod string `json:"publickey_mod"`
			PublickeyExp string `json:"publickey_exp"`
			Timestamp    string `json:"timestamp"`
		} `json:"response"`
	}

	if err := json.Unmarshal(res.Body, &raw); err != nil {
		return nil, fmt.Errorf("decode rsa key: %w", err)
	}

	modBytes, err := hex.DecodeString(raw.Response.PublickeyMod)
	if err != nil {
		return nil, fmt.Errorf("decode mod: %w", err)
	}
	mod := new(big.Int).SetBytes(modBytes)

	expBytes, err := hex.DecodeString(raw.Response.PublickeyExp)
	if err != nil {
		return nil, fmt.Errorf("decode exp: %w", err)
	}
	exp := int(new(big.Int).SetBytes(expBytes).Int64())

	return &RSAPublicKey{
		Mod:       mod,
		Exp:       exp,
		Timestamp: raw.Response.Timestamp,
	}, nil
}

// EncryptPassword encrypts a plaintext password with the Steam RSA public key.
func EncryptPassword(password string, key *RSAPublicKey) (string, error) {
	pub := &rsa.PublicKey{
		N: key.Mod,
		E: key.Exp,
	}
	encrypted, err := rsa.EncryptPKCS1v15(rand.Reader, pub, []byte(password))
	if err != nil {
		return "", fmt.Errorf("rsa encrypt: %w", err)
	}
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

// CredentialsChallenge contains the session state for credentials login.
type CredentialsChallenge struct {
	ClientID             string
	RequestID            string
	SteamID              string
	Interval             time.Duration
	AllowedConfirmations []int
}

// BeginCredentials starts an authentication session with username and encrypted password.
func (c *Client) BeginCredentials(ctx context.Context, accountName, password string) (*CredentialsChallenge, error) {
	key, err := c.GetRSAKey(ctx, accountName)
	if err != nil {
		return nil, fmt.Errorf("fetch rsa key: %w", err)
	}

	encryptedPass, err := EncryptPassword(password, key)
	if err != nil {
		return nil, err
	}

	endpoint, err := httpx.Endpoint(c.BaseURL, "IAuthenticationService/BeginAuthSessionViaCredentials/v1/", false)
	if err != nil {
		return nil, err
	}

	form := url.Values{
		"device_friendly_name":                 {"steam-cli"},
		"account_name":                         {accountName},
		"encrypted_password":                   {encryptedPass},
		"encryption_timestamp":                 {key.Timestamp},
		"remember_login":                       {"true"},
		"platform_type":                        {"1"},
		"persistence":                          {"1"},
		"device_details[device_friendly_name]": {"steam-cli"},
		"device_details[platform_type]":        {"1"},
	}

	res, err := c.HTTP.DoFull(ctx, http.MethodPost, endpoint, nil, []byte(form.Encode()), map[string][]string{
		"Content-Type": {"application/x-www-form-urlencoded"},
	})
	if err != nil {
		return nil, err
	}

	var raw struct {
		Response struct {
			ClientID             string `json:"client_id"`
			RequestID            string `json:"request_id"`
			SteamID              string `json:"steamid"`
			Interval             int    `json:"interval"`
			AllowedConfirmations []struct {
				ConfirmationType int `json:"confirmation_type"`
			} `json:"allowed_confirmations"`
		} `json:"response"`
	}

	if err := json.Unmarshal(res.Body, &raw); err != nil {
		return nil, fmt.Errorf("decode credentials response: %w", err)
	}

	if raw.Response.ClientID == "" {
		return nil, errors.New("steam rejected credentials or requires captcha")
	}

	interval := time.Duration(raw.Response.Interval) * time.Second
	if interval < time.Second {
		interval = 3 * time.Second
	}

	var confs []int
	for _, ac := range raw.Response.AllowedConfirmations {
		confs = append(confs, ac.ConfirmationType)
	}

	return &CredentialsChallenge{
		ClientID:             raw.Response.ClientID,
		RequestID:            raw.Response.RequestID,
		SteamID:              raw.Response.SteamID,
		Interval:             interval,
		AllowedConfirmations: confs,
	}, nil
}

// SubmitSteamGuardCode sends a 2FA code (email code = 2, TOTP mobile authenticator = 3) to Steam.
func (c *Client) SubmitSteamGuardCode(ctx context.Context, clientID, steamID, code string, codeType int) error {
	endpoint, err := httpx.Endpoint(c.BaseURL, "IAuthenticationService/UpdateAuthSessionWithSteamGuardCode/v1/", false)
	if err != nil {
		return err
	}

	form := url.Values{
		"client_id": {clientID},
		"steamid":   {steamID},
		"code":      {strings.TrimSpace(code)},
		"code_type": {fmt.Sprint(codeType)},
	}

	res, err := c.HTTP.DoFull(ctx, http.MethodPost, endpoint, nil, []byte(form.Encode()), map[string][]string{
		"Content-Type": {"application/x-www-form-urlencoded"},
	})
	if err != nil {
		return err
	}

	var raw struct {
		Response map[string]any `json:"response"`
	}
	if err := json.Unmarshal(res.Body, &raw); err != nil {
		return fmt.Errorf("decode guard submit: %w", err)
	}
	return nil
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

// PollSession polls until Steam approves the challenge or context expires.
func (c *Client) PollSession(ctx context.Context, clientID, requestID string, interval time.Duration) (*Session, error) {
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

// BrowserFlow launches a local loopback server.
func BrowserFlow(ctx context.Context, port int) (*Session, error) {
	if port <= 0 {
		port = 20888
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
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
				fmt.Fprint(w, `<!DOCTYPE html><html><body><h2>steam-cli Session Receiver</h2></body></html>`)
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
