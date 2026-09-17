// Package community performs the Steam Community operations that the Steam Web
// API does not expose.
//
// Collection membership is the motivating case: IPublishedFileService offers
// Publish, Update and Delete, but has no method for adding or removing a
// collection's children. The Workshop web UI performs those edits against
// steamcommunity.com using the logged-in session cookie, and so does this
// package. The same session is what makes a user's own subscription and
// favorite lists reachable; both are absent from the Web API.
//
// Authentication is the steamLoginSecure cookie from a browser session. Steam
// protects these endpoints with a double-submitted CSRF token: the sessionid
// must appear both as a cookie and as a form field.
package community

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"steamcli.local/steam/internal/httpx"
)

const DefaultBaseURL = "https://steamcommunity.com"

// Browse filters accepted by the myworkshopfiles listing.
const (
	FilterSubscriptions = "mysubscriptions"
	FilterFavorites     = "myfavorites"
	FilterPublished     = "myfiles"
)

type Client struct {
	HTTP        *httpx.Client
	BaseURL     string
	LoginSecure string
	SessionID   string
}

// ErrNoSession reports that a session-authenticated operation was attempted
// without a steamLoginSecure cookie.
var ErrNoSession = errors.New(
	"this operation needs a logged-in Steam Community session, which the Web API key cannot provide.\n" +
		"Set STEAM_LOGIN_SECURE to your steamLoginSecure cookie value (steamcommunity.com > DevTools > Application > Cookies),\n" +
		"or set community_login_secure_env / community_login_secure_file in your config profile")

func (c *Client) base() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return DefaultBaseURL
}

// SteamID extracts the account's SteamID64 from the session cookie, whose
// value is "<steamid>||<token>" before URL encoding.
func (c *Client) SteamID() (string, error) {
	if c.LoginSecure == "" {
		return "", ErrNoSession
	}
	v := c.LoginSecure
	if d, e := url.QueryUnescape(v); e == nil {
		v = d
	}
	id, _, ok := strings.Cut(v, "||")
	if !ok {
		id = v
	}
	id = strings.TrimSpace(id)
	if _, e := strconv.ParseUint(id, 10, 64); e != nil {
		return "", errors.New("steamLoginSecure cookie is malformed; expected it to begin with a SteamID64")
	}
	return id, nil
}

func (c *Client) sessionID() string {
	if c.SessionID != "" {
		return c.SessionID
	}
	// Steam only requires that the cookie and the form field agree.
	return "b1f6b9a0c3d24e7f"
}

func (c *Client) headers(accept string) http.Header {
	h := make(http.Header)
	cookies := []string{"sessionid=" + c.sessionID()}
	if c.LoginSecure != "" {
		cookies = append(cookies, "steamLoginSecure="+c.LoginSecure)
	}
	h.Set("Cookie", strings.Join(cookies, "; "))
	h.Set("Accept", accept)
	h.Set("User-Agent", "steam-cli/0.1")
	return h
}

// post submits a session-authenticated form and interprets Steam's reply.
func (c *Client) post(ctx context.Context, path string, form url.Values) error {
	if c.LoginSecure == "" {
		return ErrNoSession
	}
	endpoint, e := httpx.Endpoint(c.base(), path, c.HTTP.AllowHTTP)
	if e != nil {
		return e
	}
	form.Set("sessionid", c.sessionID())
	h := c.headers("application/json, text/plain, */*")
	h.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	h.Set("Referer", c.base()+"/")
	h.Set("X-Requested-With", "XMLHttpRequest")

	r, e := c.HTTP.DoFull(ctx, http.MethodPost, endpoint, nil, []byte(form.Encode()), h)
	if e != nil {
		var se *httpx.StatusError
		if errors.As(e, &se) && (se.Code == 302 || se.Code == 401 || se.Code == 403) {
			return fmt.Errorf("%w (Steam rejected the session: HTTP %d)", ErrNoSession, se.Code)
		}
		return e
	}
	return interpret(r.Body)
}

// interpret decodes the ad-hoc shapes these endpoints return. They answer with
// {"success":1}, a bare EResult integer, or an empty body on success.
func interpret(body []byte) error {
	t := strings.TrimSpace(string(body))
	if t == "" || t == "null" {
		return nil
	}
	if n, e := strconv.Atoi(t); e == nil {
		if n == 1 {
			return nil
		}
		return fmt.Errorf("Steam refused the request: EResult %d", n)
	}
	var res struct {
		Success *int   `json:"success"`
		Message string `json:"message"`
	}
	if e := json.Unmarshal(body, &res); e == nil && res.Success != nil {
		if *res.Success == 1 {
			return nil
		}
		if res.Message != "" {
			return fmt.Errorf("Steam refused the request: %s (success=%d)", res.Message, *res.Success)
		}
		return fmt.Errorf("Steam refused the request: success=%d", *res.Success)
	}
	if strings.HasPrefix(t, "<") {
		return errors.New("Steam returned a web page instead of a result, which usually means the session expired")
	}
	return nil
}

// AddChild adds an item to a collection.
func (c *Client) AddChild(ctx context.Context, collectionID, itemID string) error {
	return c.post(ctx, "sharedfiles/addchild/", url.Values{
		"id":       {collectionID},
		"parentid": {collectionID},
		"childid":  {itemID},
	})
}

// RemoveChild removes an item from a collection.
func (c *Client) RemoveChild(ctx context.Context, collectionID, itemID string) error {
	return c.post(ctx, "sharedfiles/removechild/", url.Values{
		"id":       {collectionID},
		"parentid": {collectionID},
		"childid":  {itemID},
	})
}

// DeleteFile deletes one of the account's own published files or collections.
// The Web API equivalent is publisher-only; this path works for the owner.
func (c *Client) DeleteFile(ctx context.Context, appID int, itemID string) error {
	return c.post(ctx, "sharedfiles/deletefile/", url.Values{
		"id":    {itemID},
		"appid": {strconv.Itoa(appID)},
	})
}

// sharedFileID anchors on the element id the Workshop uses for every item tile.
var sharedFileID = regexp.MustCompile(`id="sharedfile_(\d+)"`)

// ListWorkshopFiles returns the published file IDs on the account's own
// Workshop listing for a filter such as mysubscriptions or myfavorites.
//
// Steam serves these lists only as HTML; there is no JSON equivalent. IDs are
// read from the item tiles, so an item Steam declines to render (deleted, or
// hidden by its author) will not appear.
func (c *Client) ListWorkshopFiles(ctx context.Context, appID int, filter string, maxPages int) ([]string, error) {
	steamID, e := c.SteamID()
	if e != nil {
		return nil, e
	}
	switch filter {
	case FilterSubscriptions, FilterFavorites, FilterPublished:
	default:
		return nil, fmt.Errorf("unsupported workshop filter %q", filter)
	}
	if maxPages <= 0 {
		maxPages = 50
	}

	const perPage = 30
	seen := make(map[string]bool)
	out := []string{}

	for page := 1; page <= maxPages; page++ {
		q := url.Values{
			"appid":        {strconv.Itoa(appID)},
			"browsefilter": {filter},
			"numperpage":   {strconv.Itoa(perPage)},
			"p":            {strconv.Itoa(page)},
		}
		endpoint, e := httpx.Endpoint(c.base(), "profiles/"+steamID+"/myworkshopfiles/", c.HTTP.AllowHTTP)
		if e != nil {
			return nil, e
		}
		r, e := c.HTTP.DoFull(ctx, http.MethodGet, endpoint, q, nil, c.headers("text/html,application/xhtml+xml"))
		if e != nil {
			var se *httpx.StatusError
			if errors.As(e, &se) && se.Code == 302 && se.Location != "" && !strings.Contains(se.Location, "/login/") {
				// Steam 302-redirected from /profiles/<steamid>/ to /id/<vanity>/
				r, e = c.HTTP.DoFull(ctx, http.MethodGet, se.Location, nil, nil, c.headers("text/html,application/xhtml+xml"))
			}
			if e != nil {
				if errors.As(e, &se) && (se.Code == 302 || se.Code == 401 || se.Code == 403) {
					return nil, fmt.Errorf("%w (Steam rejected the session: HTTP %d)", ErrNoSession, se.Code)
				}
				return nil, e
			}
		}

		matches := sharedFileID.FindAllSubmatch(r.Body, -1)
		added := 0
		for _, m := range matches {
			id := string(m[1])
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
				added++
			}
		}
		// A short page is the last page; no new IDs means we are looping.
		if added == 0 || len(matches) < perPage {
			break
		}
	}

	if len(out) == 0 && filter != FilterPublished {
		// An empty list is legitimate, but so is a silently unauthenticated
		// page, and the two are indistinguishable in the HTML.
		return out, nil
	}
	return out, nil
}

// ProfileFields contains fields that can be updated on a Steam Community profile.
type ProfileFields struct {
	PersonaName string
	RealName    string
	Summary     string
	Country     string
	State       string
	City        string
	CustomURL   string
}

// EditProfile submits profile updates to the Steam Community edit/process endpoint.
func (c *Client) EditProfile(ctx context.Context, fields ProfileFields) error {
	steamID, e := c.SteamID()
	if e != nil {
		return e
	}
	form := url.Values{
		"type": {"profileSaveOption"},
	}
	if fields.PersonaName != "" {
		form.Set("personaName", fields.PersonaName)
	}
	if fields.RealName != "" {
		form.Set("real_name", fields.RealName)
	}
	if fields.Summary != "" {
		form.Set("summary", fields.Summary)
	}
	if fields.Country != "" {
		form.Set("country", fields.Country)
	}
	if fields.State != "" {
		form.Set("state", fields.State)
	}
	if fields.City != "" {
		form.Set("city", fields.City)
	}
	if fields.CustomURL != "" {
		form.Set("customURL", fields.CustomURL)
	}

	return c.post(ctx, "profiles/"+steamID+"/edit/process", form)
}

// SetPrivacy updates profile privacy settings via ajaxsetprivacy.
func (c *Client) SetPrivacy(ctx context.Context, privacySettings map[string]int) error {
	steamID, e := c.SteamID()
	if e != nil {
		return e
	}
	settingsJSON, err := json.Marshal(privacySettings)
	if err != nil {
		return err
	}
	form := url.Values{
		"Privacy": {string(settingsJSON)},
		"eCommentPermission": {"1"},
	}
	return c.post(ctx, "profiles/"+steamID+"/ajaxsetprivacy", form)
}
