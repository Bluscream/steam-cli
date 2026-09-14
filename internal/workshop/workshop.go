package workshop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"steamcli.local/steam/internal/webapi"
)

type Client struct {
	Web *webapi.Client
}

type CollectionDetails struct {
	PublishedFileID string                 `json:"publishedfileid"`
	Result          int                    `json:"result"`
	Details         *PublishedFileDetails  `json:"details,omitempty"`
	Children        []CollectionChild      `json:"children,omitempty"`
}

type CollectionChild struct {
	PublishedFileID string `json:"publishedfileid"`
	SortOrder       int    `json:"sortorder"`
	FileType        int    `json:"filetype"`
}

type PublishedFileDetails struct {
	PublishedFileID string `json:"publishedfileid"`
	Result          int    `json:"result"`
	Creator         string `json:"creator,omitempty"`
	ConsumerAppID   int    `json:"consumer_appid,omitempty"`
	Title           string `json:"title,omitempty"`
	Description     string `json:"file_description,omitempty"`
	TimeCreated     int64  `json:"time_created,omitempty"`
	TimeUpdated     int64  `json:"time_updated,omitempty"`
	Visibility      int    `json:"visibility,omitempty"`
	Subscriptions   int    `json:"subscriptions,omitempty"`
	Favorites       int    `json:"favorited,omitempty"`
	Views           int    `json:"views,omitempty"`
	NumChildren     int    `json:"num_children,omitempty"`
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
		"collectioncount":      {"1"},
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
	detailsMap, _ := c.GetDetails(ctx, []string{collectionID})
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
		if err != nil {
			return nil, err
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
			item := PublishedFileDetails{
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
	results := make([]BatchResult, 0, len(itemIDs))
	for _, id := range itemIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		params := url.Values{
			"publishedfileid": {id},
			"appid":           {strconv.Itoa(appID)},
		}
		_, err := c.Web.Call(ctx, "IPublishedFileService", "Subscribe", 1, "POST", params)
		if err != nil {
			results = append(results, BatchResult{
				PublishedFileID: id,
				Success:         false,
				Error:           err.Error(),
			})
		} else {
			results = append(results, BatchResult{
				PublishedFileID: id,
				Success:         true,
			})
		}
	}
	return results
}

// Unsubscribe unsubscribes from one or more published file IDs for an app.
func (c *Client) Unsubscribe(ctx context.Context, appID int, itemIDs []string) []BatchResult {
	results := make([]BatchResult, 0, len(itemIDs))
	for _, id := range itemIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		params := url.Values{
			"publishedfileid": {id},
			"appid":           {strconv.Itoa(appID)},
		}
		_, err := c.Web.Call(ctx, "IPublishedFileService", "Unsubscribe", 1, "POST", params)
		if err != nil {
			results = append(results, BatchResult{
				PublishedFileID: id,
				Success:         false,
				Error:           err.Error(),
			})
		} else {
			results = append(results, BatchResult{
				PublishedFileID: id,
				Success:         true,
			})
		}
	}
	return results
}

// QueryItems queries workshop items or collections for a given game.
func (c *Client) QueryItems(ctx context.Context, appID int, fileType int, page, numPerPage int, searchText string) ([]PublishedFileDetails, int, error) {
	if page < 1 {
		page = 1
	}
	if numPerPage <= 0 || numPerPage > 100 {
		numPerPage = 20
	}

	params := url.Values{
		"query_type":     {"0"}, // ranked by vote
		"appid":          {strconv.Itoa(appID)},
		"page":           {strconv.Itoa(page)},
		"numperpage":     {strconv.Itoa(numPerPage)},
		"return_details": {"true"},
	}
	if fileType >= 0 {
		params.Set("filetype", strconv.Itoa(fileType))
	}
	if searchText != "" {
		params.Set("search_text", searchText)
	}

	body, err := c.Web.Call(ctx, "IPublishedFileService", "QueryFiles", 1, "GET", params)
	if err != nil {
		return nil, 0, err
	}

	var raw struct {
		Response struct {
			Total                int `json:"total"`
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
		return nil, 0, err
	}

	items := make([]PublishedFileDetails, len(raw.Response.PublishedFileDetails))
	for i, d := range raw.Response.PublishedFileDetails {
		items[i] = PublishedFileDetails{
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
		}
	}

	return items, raw.Response.Total, nil
}

// QueryCollections queries collections for a given game.
func (c *Client) QueryCollections(ctx context.Context, appID int, page, numPerPage int, searchText string) ([]PublishedFileDetails, int, error) {
	return c.QueryItems(ctx, appID, 2, page, numPerPage, searchText)
}


// CreateCollection publishes a new workshop collection with optional initial items.
func (c *Client) CreateCollection(ctx context.Context, appID int, title, description string, visibility int, items []string) (string, error) {
	if title == "" {
		return "", errors.New("title cannot be empty")
	}
	params := url.Values{
		"appid":            {strconv.Itoa(appID)},
		"consumer_appid":   {strconv.Itoa(appID)},
		"file_type":        {"2"}, // k_EWorkshopFileTypeCollection
		"title":            {title},
		"file_description": {description},
		"visibility":       {strconv.Itoa(visibility)},
	}

	body, err := c.Web.Call(ctx, "IPublishedFileService", "Publish", 1, "POST", params)
	if err != nil {
		return "", err
	}

	var res struct {
		Response struct {
			PublishedFileID string `json:"publishedfileid"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return "", fmt.Errorf("decode publish response: %w", err)
	}
	if res.Response.PublishedFileID == "" {
		return "", errors.New("no publishedfileid returned in response")
	}

	collID := res.Response.PublishedFileID

	// If initial items were provided, associate them via community / web call
	if len(items) > 0 {
		addParams := url.Values{
			"publishedfileid": {collID},
		}
		for _, item := range items {
			addParams.Set(fmt.Sprintf("collections[%s][add]", item), "true")
		}
		_, _ = c.Web.Call(ctx, "ISteamRemoteStorage", "SetUserPublishedFileAction", 1, "POST", addParams)
	}

	return collID, nil
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

	_, err := c.Web.Call(ctx, "IPublishedFileService", "Update", 1, "POST", params)
	return err
}

// DeleteCollection deletes a collection or workshop item.
func (c *Client) DeleteCollection(ctx context.Context, appID int, collectionID string) error {
	params := url.Values{
		"appid":           {strconv.Itoa(appID)},
		"publishedfileid": {collectionID},
	}
	_, err := c.Web.Call(ctx, "IPublishedFileService", "Delete", 1, "POST", params)
	return err
}

type SubscribedApp struct {
	AppID string   `json:"appid"`
	Items []string `json:"items"`
	Total int      `json:"total"`
}

// ScanLocalSubscriptions scans local Steam library folders for subscribed/installed workshop items.
func ScanLocalSubscriptions(libraries []string, targetAppID int) ([]SubscribedApp, error) {
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

			content, err := os.ReadFile(file)
			if err != nil {
				continue
			}

			if _, ok := appMap[idStr]; !ok {
				appMap[idStr] = make(map[string]bool)
			}

			// Parse items from WorkshopItemsInstalled or WorkshopItemDetails
			// format: "itemID" { ... }
			lines := strings.Split(string(content), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "\"") && strings.HasSuffix(line, "\"") {
					val := strings.Trim(line, "\"")
					if _, err := strconv.ParseUint(val, 10, 64); err == nil && len(val) >= 5 {
						appMap[idStr][val] = true
					}
				}
			}
		}
	}

	var results []SubscribedApp
	for appID, itemSet := range appMap {
		if len(itemSet) == 0 {
			continue
		}
		items := make([]string, 0, len(itemSet))
		for it := range itemSet {
			items = append(items, it)
		}
		sort.Strings(items)
		results = append(results, SubscribedApp{
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

