package workshop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/andygrunwald/vdf"

	"steamcli.local/steam/internal/community"
	"steamcli.local/steam/internal/webapi"
)

type Client struct {
	Web       *webapi.Client
	Community *community.Client
}

type CollectionDetails struct {
	PublishedFileID string                `json:"publishedfileid"`
	Result          int                   `json:"result"`
	Details         *PublishedFileDetails `json:"details,omitempty"`
	Children        []CollectionChild     `json:"children,omitempty"`
}

type CollectionChild struct {
	PublishedFileID string `json:"publishedfileid"`
	SortOrder       int    `json:"sortorder"`
	FileType        int    `json:"filetype"`
}

type PublishedFileDetails struct {
	PublishedFileID string            `json:"publishedfileid"`
	Result          int               `json:"result"`
	Creator         string            `json:"creator,omitempty"`
	ConsumerAppID   int               `json:"consumer_appid,omitempty"`
	Title           string            `json:"title,omitempty"`
	Description     string            `json:"file_description,omitempty"`
	TimeCreated     int64             `json:"time_created,omitempty"`
	TimeUpdated     int64             `json:"time_updated,omitempty"`
	Visibility      int               `json:"visibility,omitempty"`
	Subscriptions   int               `json:"subscriptions,omitempty"`
	Favorites       int               `json:"favorited,omitempty"`
	Views           int               `json:"views,omitempty"`
	NumChildren     int               `json:"num_children,omitempty"`
	Children        []CollectionChild `json:"children,omitempty"`
}

type BatchResult struct {
	PublishedFileID string `json:"publishedfileid"`
	Success         bool   `json:"success"`
	Error           string `json:"error,omitempty"`
}

// GetCollectionDetails retrieves a collection and its child items.
func (c *Client) GetCollectionDetails(ctx context.Context, collectionID string) (CollectionDetails, error) {
	if collectionID == "" {
		return CollectionDetails{}, errors.New("collection ID cannot be empty")
	}

	// 1. Get collection item list from ISteamRemoteStorage/GetCollectionDetails
	params := url.Values{
		"collectioncount":     {"1"},
		"publishedfileids[0]": {collectionID},
	}
	body, err := c.Web.Call(ctx, "ISteamRemoteStorage", "GetCollectionDetails", 1, "POST", params)
	if err != nil {
		return CollectionDetails{}, fmt.Errorf("fetch collection %s: %w", collectionID, err)
	}

	var raw struct {
		Response struct {
			Result            int `json:"result"`
			ResultCount       int `json:"resultcount"`
			CollectionDetails []struct {
				PublishedFileID string `json:"publishedfileid"`
				Result          int    `json:"result"`
				Children        []struct {
					PublishedFileID string `json:"publishedfileid"`
					SortOrder       int    `json:"sortorder"`
					FileType        int    `json:"filetype"`
				} `json:"children"`
			} `json:"collectiondetails"`
		} `json:"response"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return CollectionDetails{}, fmt.Errorf("decode collection details: %w", err)
	}

	if len(raw.Response.CollectionDetails) == 0 {
		return CollectionDetails{}, fmt.Errorf("collection %s not found", collectionID)
	}

	item := raw.Response.CollectionDetails[0]
	cd := CollectionDetails{
		PublishedFileID: item.PublishedFileID,
		Result:          item.Result,
	}
	for _, ch := range item.Children {
		cd.Children = append(cd.Children, CollectionChild{
			PublishedFileID: ch.PublishedFileID,
			SortOrder:       ch.SortOrder,
			FileType:        ch.FileType,
		})
	}

	// 2. Fetch metadata/title for collection itself via IPublishedFileService/GetDetails
	detailsMap, err := c.GetDetails(ctx, []string{collectionID})
	if err != nil {
		return cd, fmt.Errorf("collection %s resolved, but its metadata could not be fetched: %w", collectionID, err)
	}
	if d, ok := detailsMap[collectionID]; ok {
		cd.Details = &d
	}

	return cd, nil
}

// GetDetails retrieves details for a batch of published file IDs.
func (c *Client) GetDetails(ctx context.Context, itemIDs []string) (map[string]PublishedFileDetails, error) {
	if len(itemIDs) == 0 {
		return nil, nil
	}

	out := make(map[string]PublishedFileDetails, len(itemIDs))

	// Chunk by 50 to respect Steam Web API request size limits
	const chunkSize = 50
	for i := 0; i < len(itemIDs); i += chunkSize {
		end := i + chunkSize
		if end > len(itemIDs) {
			end = len(itemIDs)
		}
		chunk := itemIDs[i:end]

		params := url.Values{
			"includetags":     {"true"},
			"includechildren": {"true"},
		}
		for idx, id := range chunk {
			params.Set(fmt.Sprintf("publishedfileids[%d]", idx), id)
		}

		body, err := c.Web.Call(ctx, "IPublishedFileService", "GetDetails", 1, "GET", params)
		if err != nil || len(body) == 0 {
			// Fallback to ISteamRemoteStorage/GetPublishedFileDetails which works without an API key
			rsParams := url.Values{
				"itemcount": {strconv.Itoa(len(chunk))},
			}
			for idx, id := range chunk {
				rsParams.Set(fmt.Sprintf("publishedfileids[%d]", idx), id)
			}
			rsBody, rsErr := c.Web.Call(ctx, "ISteamRemoteStorage", "GetPublishedFileDetails", 1, "POST", rsParams)
			if rsErr != nil {
				if err != nil {
					return nil, err
				}
				return nil, rsErr
			}
			body = rsBody
		}

		var raw struct {
			Response struct {
				PublishedFileDetails []struct {
					PublishedFileID string `json:"publishedfileid"`
					Result          int    `json:"result"`
					Creator         string `json:"creator"`
					ConsumerAppID   int    `json:"consumer_appid"`
					Title           string `json:"title"`
					FileDescription string `json:"file_description"`
					Description     string `json:"description"`
					TimeCreated     int64  `json:"time_created"`
					TimeUpdated     int64  `json:"time_updated"`
					Visibility      int    `json:"visibility"`
					Subscriptions   int    `json:"subscriptions"`
					Favorited       int    `json:"favorited"`
					Views           int    `json:"views"`
					NumChildren     int    `json:"num_children"`
					Children        []struct {
						PublishedFileID string `json:"publishedfileid"`
						SortOrder       int    `json:"sortorder"`
						FileType        int    `json:"filetype"`
					} `json:"children"`
				} `json:"publishedfiledetails"`
			} `json:"response"`
		}

		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("decode published file details: %w", err)
		}

		for _, d := range raw.Response.PublishedFileDetails {
			desc := d.FileDescription
			if desc == "" {
				desc = d.Description
			}
			item := PublishedFileDetails{
				PublishedFileID: d.PublishedFileID,
				Result:          d.Result,
				Creator:         d.Creator,
				ConsumerAppID:   d.ConsumerAppID,
				Title:           d.Title,
				Description:     desc,
				TimeCreated:     d.TimeCreated,
				TimeUpdated:     d.TimeUpdated,
				Visibility:      d.Visibility,
				Subscriptions:   d.Subscriptions,
				Favorites:       d.Favorited,
				Views:           d.Views,
				NumChildren:     d.NumChildren,
			}
			for _, ch := range d.Children {
				item.Children = append(item.Children, CollectionChild{
					PublishedFileID: ch.PublishedFileID,
					SortOrder:       ch.SortOrder,
					FileType:        ch.FileType,
				})
			}
			out[d.PublishedFileID] = item
		}
	}

	return out, nil
}

// Subscribe subscribes to one or more published file IDs for an app.
func (c *Client) Subscribe(ctx context.Context, appID int, itemIDs []string) []BatchResult {
	return c.batch(ctx, "Subscribe", appID, itemIDs)
}

// Unsubscribe unsubscribes from one or more published file IDs for an app.
func (c *Client) Unsubscribe(ctx context.Context, appID int, itemIDs []string) []BatchResult {
	return c.batch(ctx, "Unsubscribe", appID, itemIDs)
}

// batch applies a per-item subscription method. Each result reflects Steam's
// EResult for that item, not merely that the HTTP request was accepted.
func (c *Client) batch(ctx context.Context, method string, appID int, itemIDs []string) []BatchResult {
	results := make([]BatchResult, 0, len(itemIDs))
	for _, id := range itemIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		params := url.Values{
			"publishedfileid": {id},
			"appid":           {strconv.Itoa(appID)},
			"list_type":       {"1"},
		}
		res := BatchResult{PublishedFileID: id}
		r, err := c.Web.CallFull(ctx, "IPublishedFileService", method, 1, "POST", params)
		if err == nil {
			err = webapi.CheckResult(r)
		}
		if err != nil {
			res.Error = err.Error()
		} else {
			res.Success = true
		}
		results = append(results, res)
	}
	return results
}

// QueryOptions describes a Workshop search.
type QueryOptions struct {
	SearchText string
	AppID      int
	FileType   int
	Page       int
	NumPerPage int
	AllPages   bool
}

// Query searches workshop items for a game.
//
// Paging uses Steam's cursor rather than the page parameter: page-based paging
// is capped server-side and silently stops returning results past that depth.
// The first request sends cursor="*" and each reply carries the next cursor.
func (c *Client) Query(ctx context.Context, opts QueryOptions) ([]PublishedFileDetails, int, error) {
	if opts.NumPerPage <= 0 || opts.NumPerPage > 100 {
		opts.NumPerPage = 20
	}
	if opts.Page < 1 {
		opts.Page = 1
	}

	all := []PublishedFileDetails{}
	total := 0
	cursor := "*"
	seen := make(map[string]bool)

	// Without --all, skip ahead to the requested page and return just that page.
	wanted := opts.NumPerPage
	if opts.AllPages {
		wanted = 0
	}

	for page := 1; ; page++ {
		params := url.Values{
			"query_type":     {"0"}, // k_PublishedFileQueryType_RankedByVote
			"appid":          {strconv.Itoa(opts.AppID)},
			"cursor":         {cursor},
			"numperpage":     {strconv.Itoa(opts.NumPerPage)},
			"return_details": {"true"},
		}
		if opts.FileType >= 0 {
			params.Set("filetype", strconv.Itoa(opts.FileType))
		}
		if opts.SearchText != "" {
			params.Set("search_text", opts.SearchText)
		}

		body, err := c.Web.Call(ctx, "IPublishedFileService", "QueryFiles", 1, "GET", params)
		if err != nil {
			return nil, 0, err
		}

		var raw struct {
			Response struct {
				Total                int    `json:"total"`
				NextCursor           string `json:"next_cursor"`
				PublishedFileDetails []struct {
					PublishedFileID string `json:"publishedfileid"`
					Result          int    `json:"result"`
					Creator         string `json:"creator"`
					ConsumerAppID   int    `json:"consumer_appid"`
					Title           string `json:"title"`
					FileDescription string `json:"file_description"`
					TimeCreated     int64  `json:"time_created"`
					TimeUpdated     int64  `json:"time_updated"`
					Visibility      int    `json:"visibility"`
					Subscriptions   int    `json:"subscriptions"`
					Favorited       int    `json:"favorited"`
					Views           int    `json:"views"`
					NumChildren     int    `json:"num_children"`
				} `json:"publishedfiledetails"`
			} `json:"response"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, 0, fmt.Errorf("decode query response: %w", err)
		}
		total = raw.Response.Total

		batch := make([]PublishedFileDetails, 0, len(raw.Response.PublishedFileDetails))
		for _, d := range raw.Response.PublishedFileDetails {
			if seen[d.PublishedFileID] {
				continue
			}
			seen[d.PublishedFileID] = true
			batch = append(batch, PublishedFileDetails{
				PublishedFileID: d.PublishedFileID,
				Result:          d.Result,
				Creator:         d.Creator,
				ConsumerAppID:   d.ConsumerAppID,
				Title:           d.Title,
				Description:     d.FileDescription,
				TimeCreated:     d.TimeCreated,
				TimeUpdated:     d.TimeUpdated,
				Visibility:      d.Visibility,
				Subscriptions:   d.Subscriptions,
				Favorites:       d.Favorited,
				Views:           d.Views,
				NumChildren:     d.NumChildren,
			})
		}

		if opts.AllPages || page >= opts.Page {
			all = append(all, batch...)
		}

		next := raw.Response.NextCursor
		done := len(raw.Response.PublishedFileDetails) == 0 || next == "" || next == cursor
		if !opts.AllPages && len(all) >= wanted {
			done = true
		}
		if done {
			break
		}
		cursor = next
	}

	if !opts.AllPages && len(all) > wanted {
		all = all[:wanted]
	}
	return all, total, nil
}

// QueryCollections searches collections for a given game.
func (c *Client) QueryCollections(ctx context.Context, appID int, page, numPerPage int, searchText string) ([]PublishedFileDetails, int, error) {
	return c.Query(ctx, QueryOptions{AppID: appID, FileType: 2, Page: page, NumPerPage: numPerPage, SearchText: searchText})
}

// CreateCollection publishes a new workshop collection and, when items are
// supplied, adds them as children.
//
// Publish creates the collection but has no parameter for its contents: the
// Steam Web API exposes no method that sets collection membership. Children
// are therefore added through the Community session, and that step reports its
// own per-item outcome rather than being assumed to have worked.
func (c *Client) CreateCollection(ctx context.Context, appID int, title, description string, visibility int, items []string) (string, []BatchResult, error) {
	if title == "" {
		return "", nil, errors.New("title cannot be empty")
	}
	if len(items) > 0 && !c.HasSession() {
		return "", nil, fmt.Errorf("cannot add items to a collection: %w", community.ErrNoSession)
	}

	params := url.Values{
		"appid":            {strconv.Itoa(appID)},
		"consumer_appid":   {strconv.Itoa(appID)},
		"file_type":        {"2"}, // k_EWorkshopFileTypeCollection
		"title":            {title},
		"file_description": {description},
		"visibility":       {strconv.Itoa(visibility)},
	}

	r, err := c.Web.CallFull(ctx, "IPublishedFileService", "Publish", 1, "POST", params)
	if err != nil {
		return "", nil, err
	}
	if err := webapi.CheckResult(r); err != nil {
		return "", nil, err
	}

	var res struct {
		Response struct {
			PublishedFileID string `json:"publishedfileid"`
		} `json:"response"`
	}
	if err := json.Unmarshal(r.Body, &res); err != nil {
		return "", nil, fmt.Errorf("decode publish response: %w", err)
	}
	if res.Response.PublishedFileID == "" || res.Response.PublishedFileID == "0" {
		return "", nil, errors.New("Steam did not return a publishedfileid; the collection was not created")
	}
	collID := res.Response.PublishedFileID

	added := c.AddItems(ctx, collID, items)
	return collID, added, nil
}

// HasSession reports whether Community operations are available.
func (c *Client) HasSession() bool {
	return c.Community != nil && c.Community.LoginSecure != ""
}

// AddItems adds items to a collection, reporting each item's outcome.
func (c *Client) AddItems(ctx context.Context, collectionID string, items []string) []BatchResult {
	return c.children(ctx, collectionID, items, true)
}

// RemoveItems removes items from a collection, reporting each item's outcome.
func (c *Client) RemoveItems(ctx context.Context, collectionID string, items []string) []BatchResult {
	return c.children(ctx, collectionID, items, false)
}

func (c *Client) children(ctx context.Context, collectionID string, items []string, add bool) []BatchResult {
	results := make([]BatchResult, 0, len(items))
	for _, id := range items {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		res := BatchResult{PublishedFileID: id}
		var err error
		if c.Community == nil {
			err = community.ErrNoSession
		} else if add {
			err = c.Community.AddChild(ctx, collectionID, id)
		} else {
			err = c.Community.RemoveChild(ctx, collectionID, id)
		}
		if err != nil {
			res.Error = err.Error()
		} else {
			res.Success = true
		}
		results = append(results, res)
	}
	return results
}

// EditCollection updates title, description, and visibility of a published collection.
func (c *Client) EditCollection(ctx context.Context, appID int, collectionID, title, description string, visibility int) error {
	params := url.Values{
		"appid":           {strconv.Itoa(appID)},
		"publishedfileid": {collectionID},
	}
	if title != "" {
		params.Set("title", title)
	}
	if description != "" {
		params.Set("file_description", description)
	}
	if visibility >= 0 {
		params.Set("visibility", strconv.Itoa(visibility))
	}
	if len(params) == 2 {
		return errors.New("nothing to change; supply --title, --description, or --visibility")
	}

	r, err := c.Web.CallFull(ctx, "IPublishedFileService", "Update", 1, "POST", params)
	if err != nil {
		return err
	}
	return webapi.CheckResult(r)
}

// DeleteCollection deletes a collection or workshop item.
//
// IPublishedFileService/Delete is documented as publisher-only: it expects a
// key issued for an app you own, and refuses an ordinary user key. The
// Community session can delete your own items, so it is preferred when
// available and the Web API is the fallback.
func (c *Client) DeleteCollection(ctx context.Context, appID int, collectionID string) error {
	if c.HasSession() {
		if err := c.Community.DeleteFile(ctx, appID, collectionID); err == nil {
			return nil
		} else if !errors.Is(err, community.ErrNoSession) {
			return err
		}
	}
	params := url.Values{
		"appid":           {strconv.Itoa(appID)},
		"publishedfileid": {collectionID},
	}
	r, err := c.Web.CallFull(ctx, "IPublishedFileService", "Delete", 1, "POST", params)
	if err == nil {
		err = webapi.CheckResult(r)
	}
	if err != nil {
		return fmt.Errorf("%w\nIPublishedFileService/Delete is publisher-only and rejects ordinary user keys; "+
			"set STEAM_LOGIN_SECURE so the collection can be deleted through your Community session instead", err)
	}
	return nil
}

// ListUserItems returns the account's own subscriptions or favorites for an
// app, as recorded by Steam rather than as found on disk.
func (c *Client) ListUserItems(ctx context.Context, appID int, filter string) ([]string, error) {
	if c.Community == nil {
		return nil, community.ErrNoSession
	}
	return c.Community.ListWorkshopFiles(ctx, appID, filter, 0)
}

type InstalledApp struct {
	AppID string   `json:"appid"`
	Items []string `json:"items"`
	Total int      `json:"total"`
}

// ScanInstalled reports workshop items present in local Steam library folders.
//
// This is disk state, not subscription state: an item subscribed but not yet
// downloaded is absent, and an item left behind after unsubscribing is
// present. Use ListUserItems for the account's actual subscription list.
func ScanInstalled(libraries []string, targetAppID int) ([]InstalledApp, error) {
	appMap := make(map[string]map[string]bool)

	for _, lib := range libraries {
		workshopDir := filepath.Join(lib, "steamapps", "workshop")
		files, err := filepath.Glob(filepath.Join(workshopDir, "appworkshop_*.acf"))
		if err != nil {
			continue
		}

		for _, file := range files {
			base := filepath.Base(file)
			// appworkshop_<appid>.acf
			if !strings.HasPrefix(base, "appworkshop_") || !strings.HasSuffix(base, ".acf") {
				continue
			}
			idStr := strings.TrimSuffix(strings.TrimPrefix(base, "appworkshop_"), ".acf")
			if targetAppID > 0 && idStr != strconv.Itoa(targetAppID) {
				continue
			}

			items, err := parseWorkshopACF(file)
			if err != nil {
				continue
			}
			if _, ok := appMap[idStr]; !ok {
				appMap[idStr] = make(map[string]bool)
			}
			for _, it := range items {
				appMap[idStr][it] = true
			}
		}
	}

	results := []InstalledApp{}
	for appID, itemSet := range appMap {
		if len(itemSet) == 0 {
			continue
		}
		items := make([]string, 0, len(itemSet))
		for it := range itemSet {
			items = append(items, it)
		}
		sort.Strings(items)
		results = append(results, InstalledApp{
			AppID: appID,
			Items: items,
			Total: len(items),
		})
	}

	sort.Slice(results, func(i, j int) bool {
		ai, _ := strconv.Atoi(results[i].AppID)
		aj, _ := strconv.Atoi(results[j].AppID)
		return ai < aj
	})

	return results, nil
}

// parseWorkshopACF reads item IDs from the keyed sections of an
// appworkshop_<appid>.acf manifest. The IDs are the keys of
// WorkshopItemsInstalled and WorkshopItemDetails, so the file is parsed as VDF
// rather than scanned for numeric-looking lines.
func parseWorkshopACF(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > 64<<20 {
		return nil, errors.New("workshop manifest exceeds 64 MiB")
	}

	parsed, err := vdf.NewParser(io.LimitReader(f, 64<<20)).Parse()
	if err != nil {
		return nil, err
	}

	root, ok := parsed["AppWorkshop"].(map[string]any)
	if !ok {
		return nil, errors.New("manifest has no AppWorkshop section")
	}

	seen := make(map[string]bool)
	var out []string
	for _, section := range []string{"WorkshopItemsInstalled", "WorkshopItemDetails"} {
		items, ok := root[section].(map[string]any)
		if !ok {
			continue
		}
		for id := range items {
			if _, err := strconv.ParseUint(id, 10, 64); err != nil {
				continue
			}
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out, nil
}
