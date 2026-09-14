package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"sync"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
	"steamcli.local/steam/internal/community"
	"steamcli.local/steam/internal/library"
	"steamcli.local/steam/internal/webapi"
)

// StoreAppItem represents an app returned by the Steam Store search API.
type StoreAppItem struct {
	ID        int               `json:"id"`
	Type      string            `json:"type"`
	Name      string            `json:"name"`
	Metascore string            `json:"metascore,omitempty"`
	Platforms map[string]bool   `json:"platforms,omitempty"`
	Price     map[string]any    `json:"price,omitempty"`
}

// StoreSearchResult represents the envelope returned by https://store.steampowered.com/api/storesearch/
type StoreSearchResult struct {
	Total int            `json:"total"`
	Items []StoreAppItem `json:"items"`
}

// SearchStoreApps queries Steam Store search API for apps matching a query string.
func (o *options) searchStoreApps(ctx context.Context, query string) ([]StoreAppItem, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("search query cannot be empty")
	}
	endpoint := "https://store.steampowered.com/api/storesearch/"
	params := url.Values{
		"term": {query},
		"l":    {"english"},
		"cc":   {"US"},
	}
	b, err := o.http().Do(ctx, "GET", endpoint, params, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("search store apps: %w", err)
	}
	var res StoreSearchResult
	if err := json.Unmarshal(b, &res); err != nil {
		return nil, fmt.Errorf("parse store search response: %w", err)
	}
	// Filter to apps if type is present
	var apps []StoreAppItem
	for _, it := range res.Items {
		if it.Type == "" || it.Type == "app" {
			apps = append(apps, it)
		}
	}
	return apps, nil
}

// resolveAppID takes an input string that is either a numeric AppID or an app name.
// If numeric, it returns the parsed integer. If not, it searches the Steam store,
// returns the best matching AppID, and prints an informational notice to stderr.
func (o *options) resolveAppID(ctx context.Context, in string, errOut io.Writer) (int, error) {
	s := strings.TrimSpace(in)
	if s == "" {
		return 0, errors.New("APPID or app name must not be empty")
	}
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n, nil
	}
	// It's a name query, search Steam Store
	apps, err := o.searchStoreApps(ctx, s)
	if err != nil {
		return 0, fmt.Errorf("resolving app %q: %w", s, err)
	}
	if len(apps) == 0 {
		return 0, fmt.Errorf("no Steam app found matching %q", s)
	}
	best := apps[0]
	if errOut != nil {
		fmt.Fprintf(errOut, "%s Resolved %q to %s (AppID %d)\n", yellow.Sprint("Notice:"), s, best.Name, best.ID)
	}
	return best.ID, nil
}

func appsCommand(o *options) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:     "apps [QUERY]",
		Aliases: []string{"search-apps", "find-app"},
		Short:   "Search for Steam games/apps by name and view AppIDs",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			q := ""
			if len(args) > 0 {
				q = args[0]
			}
			if q == "" {
				return errors.New("a search query is required (e.g. steamcli apps vrchat)")
			}
			items, err := o.searchStoreApps(cmd.Context(), q)
			if err != nil {
				return err
			}
			if limit > 0 && len(items) > limit {
				items = items[:limit]
			}
			return o.emit(cmd, map[string]any{
				"query": q,
				"total": len(items),
				"apps":  items,
			}, func(w io.Writer) {
				if len(items) == 0 {
					fmt.Fprintf(w, "No Steam apps found matching %q.\n", q)
					return
				}
				t := o.newTable(w)
				t.AppendHeader(table.Row{"AppID", "Name", "Platforms"})
				t.SetColumnConfigs([]table.ColumnConfig{
					{Number: 1, Align: text.AlignRight},
				})
				for _, it := range items {
					var plats []string
					if it.Platforms["windows"] {
						plats = append(plats, "Win")
					}
					if it.Platforms["mac"] {
						plats = append(plats, "Mac")
					}
					if it.Platforms["linux"] {
						plats = append(plats, "Linux")
					}
					pStr := strings.Join(plats, "/")
					if pStr == "" {
						pStr = "-"
					}
					t.AppendRow(table.Row{it.ID, truncate(it.Name, 55), pStr})
				}
				o.renderTable(t)
				if o.format != "csv" {
					fmt.Fprintf(w, "%s\n", faint(fmt.Sprintf("%d app(s) found for %q.", len(items), q)))
				}
			})
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", 25, "Maximum number of search results to show")
	return cmd
}

// GlobalSearchResults holds results from all categories for the global search command.
type GlobalSearchResults struct {
	Query         string                  `json:"query"`
	StoreApps     []StoreAppItem          `json:"store_apps,omitempty"`
	LocalApps     []library.App           `json:"local_apps,omitempty"`
	OwnedGames    []ownedGame             `json:"owned_games,omitempty"`
	Players       []playerSummary         `json:"players,omitempty"`
	WorkshopItems []searchWorkshopItem    `json:"workshop_items,omitempty"`
}

type searchWorkshopItem struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	AppID         int    `json:"appid"`
	AppName       string `json:"app_name,omitempty"`
	Subscriptions int    `json:"subscriptions"`
	Favorites     int    `json:"favorites"`
	Updated       string `json:"updated"`
}

func searchCommand(o *options) *cobra.Command {
	var maxPerType int
	cmd := &cobra.Command{
		Use:   "search QUERY",
		Short: "Search games, workshop items, players, and local library across Steam",
		Long: "Search games, workshop items, players, and local library across Steam.\n\n" +
			"Aggregates results from Steam Store apps, Workshop items, locally installed\n" +
			"games, owned library games, and player vanity names. Displays separate tables\n" +
			"for each category that returned matches (empty categories are omitted).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			q := strings.TrimSpace(args[0])
			if q == "" {
				return errors.New("search query cannot be empty")
			}
			if maxPerType <= 0 {
				maxPerType = 100
			}
			ctx := cmd.Context()
			res := GlobalSearchResults{Query: q}
			var wg sync.WaitGroup
			var mu sync.Mutex

			// 1. Steam Store Apps
			wg.Add(1)
			go func() {
				defer wg.Done()
				apps, err := o.searchStoreApps(ctx, q)
				if err == nil && len(apps) > 0 {
					if len(apps) > maxPerType {
						apps = apps[:maxPerType]
					}
					mu.Lock()
					res.StoreApps = apps
					mu.Unlock()
				}
			}()

			// 2. Local installed games
			wg.Add(1)
			go func() {
				defer wg.Done()
				rep, err := library.Scan(library.Defaults())
				if err == nil && len(rep.Apps) > 0 {
					qLower := strings.ToLower(q)
					var matched []library.App
					for _, a := range rep.Apps {
						if strings.Contains(strings.ToLower(a.Name), qLower) || a.AppID == q {
							matched = append(matched, a)
							if len(matched) >= maxPerType {
								break
							}
						}
					}
					if len(matched) > 0 {
						mu.Lock()
						res.LocalApps = matched
						mu.Unlock()
					}
				}
			}()

			// 3. Workshop items across Steam
			wg.Add(1)
			go func() {
				defer wg.Done()
				s, err := o.settings()
				if err != nil {
					return
				}
				key, _ := s.WebKey()
				token, _ := s.AccessToken()
				wc := &webapi.Client{HTTP: o.http(), BaseURL: s.WebURL, Key: key, CacheDir: s.CacheDir, AccessToken: token}
				numToFetch := maxPerType
				if numToFetch > 100 {
					numToFetch = 100
				}
				params := url.Values{
					"query_type":     {"0"},
					"search_text":    {q},
					"numperpage":     {strconv.Itoa(numToFetch)},
					"return_details": {"true"},
				}
				b, err := wc.Call(ctx, "IPublishedFileService", "QueryFiles", 1, "GET", params)
				if err != nil {
					return
				}
				var raw struct {
					Response struct {
						PublishedFileDetails []struct {
							PublishedFileID string `json:"publishedfileid"`
							ConsumerAppID   int    `json:"consumer_appid"`
							Title           string `json:"title"`
							AppName         string `json:"app_name"`
							Subscriptions   int    `json:"subscriptions"`
							Favorited       int    `json:"favorited"`
							TimeUpdated     int64  `json:"time_updated"`
						} `json:"publishedfiledetails"`
					} `json:"response"`
				}
				if json.Unmarshal(b, &raw) == nil && len(raw.Response.PublishedFileDetails) > 0 {
					var items []searchWorkshopItem
					for _, it := range raw.Response.PublishedFileDetails {
						items = append(items, searchWorkshopItem{
							ID:            it.PublishedFileID,
							Title:         it.Title,
							AppID:         it.ConsumerAppID,
							AppName:       it.AppName,
							Subscriptions: it.Subscriptions,
							Favorites:     it.Favorited,
							Updated:       unixDate(it.TimeUpdated),
						})
					}
					mu.Lock()
					res.WorkshopItems = items
					mu.Unlock()
				}
			}()

			// 4. Player / Vanity URL resolution & summary
			wg.Add(1)
			go func() {
				defer wg.Done()
				s, err := o.settings()
				if err != nil {
					return
				}
				key, err := s.WebKey()
				if err != nil || key == "" {
					return
				}
				token, _ := s.AccessToken()
				wc := &webapi.Client{HTTP: o.http(), BaseURL: s.WebURL, Key: key, CacheDir: s.CacheDir, AccessToken: token}

				// Check if query is directly a SteamID or vanity name
				steamID := ""
				if _, err := strconv.ParseUint(q, 10, 64); err == nil && len(q) == 17 {
					steamID = q
				} else {
					b, err := wc.Call(ctx, "ISteamUser", "ResolveVanityURL", 1, "GET", url.Values{"vanityurl": {q}})
					if err == nil {
						var resVanity struct {
							Response struct {
								SteamID string `json:"steamid"`
								Success int    `json:"success"`
							} `json:"response"`
						}
						if json.Unmarshal(b, &resVanity) == nil && resVanity.Response.Success == 1 {
							steamID = resVanity.Response.SteamID
						}
					}
				}

				if steamID != "" {
					b, err := wc.Call(ctx, "ISteamUser", "GetPlayerSummaries", 2, "GET", url.Values{"steamids": {steamID}})
					if err == nil {
						var resPlayers struct {
							Response struct {
								Players []playerSummary `json:"players"`
							} `json:"response"`
						}
						if json.Unmarshal(b, &resPlayers) == nil && len(resPlayers.Response.Players) > 0 {
							mu.Lock()
							res.Players = resPlayers.Response.Players
							mu.Unlock()
						}
					}
				}
			}()

			// 5. Owned games if key & user are available
			wg.Add(1)
			go func() {
				defer wg.Done()
				s, err := o.settings()
				if err != nil {
					return
				}
				key, err := s.WebKey()
				if err != nil || key == "" {
					return
				}
				userSteamID := ""
				if id, err := s.SteamUserID(); err == nil && id != "" {
					userSteamID = id
				} else if cLogin, err := s.CommunityLoginSecure(); err == nil && cLogin != "" {
					userSteamID, _ = (&community.Client{LoginSecure: cLogin}).SteamID()
				}
				if userSteamID == "" {
					return
				}
				token, _ := s.AccessToken()
				wc := &webapi.Client{HTTP: o.http(), BaseURL: s.WebURL, Key: key, CacheDir: s.CacheDir, AccessToken: token}
				params := url.Values{
					"steamid":                  {userSteamID},
					"include_appinfo":          {"1"},
					"include_played_free_games": {"1"},
				}
				b, err := wc.Call(ctx, "IPlayerService", "GetOwnedGames", 1, "GET", params)
				if err != nil {
					return
				}
				var raw struct {
					Response struct {
						Games []ownedGame `json:"games"`
					} `json:"response"`
				}
				if json.Unmarshal(b, &raw) == nil && len(raw.Response.Games) > 0 {
					qLower := strings.ToLower(q)
					var matched []ownedGame
					for _, g := range raw.Response.Games {
						if strings.Contains(strings.ToLower(g.Name), qLower) || strconv.Itoa(g.AppID) == q {
							matched = append(matched, g)
							if len(matched) >= maxPerType {
								break
							}
						}
					}
					if len(matched) > 0 {
						mu.Lock()
						res.OwnedGames = matched
						mu.Unlock()
					}
				}
			}()

			wg.Wait()

			return o.emit(cmd, res, func(w io.Writer) {
				hasAny := false

				// Store apps table
				if len(res.StoreApps) > 0 {
					hasAny = true
					o.heading(w, "Steam Store Apps")
					t := o.newTable(w)
					t.AppendHeader(table.Row{"AppID", "Name", "Platforms"})
					t.SetColumnConfigs([]table.ColumnConfig{{Number: 1, Align: text.AlignRight}})
					for _, it := range res.StoreApps {
						var plats []string
						if it.Platforms["windows"] {
							plats = append(plats, "Win")
						}
						if it.Platforms["mac"] {
							plats = append(plats, "Mac")
						}
						if it.Platforms["linux"] {
							plats = append(plats, "Linux")
						}
						pStr := strings.Join(plats, "/")
						if pStr == "" {
							pStr = "-"
						}
						t.AppendRow(table.Row{it.ID, truncate(it.Name, 50), pStr})
					}
					o.renderTable(t)
					if o.format != "csv" {
						fmt.Fprintf(w, "%s\n\n", faint(fmt.Sprintf("%d store app(s) found.", len(res.StoreApps))))
					}
				}

				// Local apps table
				if len(res.LocalApps) > 0 {
					hasAny = true
					o.heading(w, "Local Library Games")
					t := o.newTable(w)
					t.AppendHeader(table.Row{"AppID", "Name", "Install Directory"})
					t.SetColumnConfigs([]table.ColumnConfig{{Number: 1, Align: text.AlignRight}})
					for _, it := range res.LocalApps {
						t.AppendRow(table.Row{it.AppID, truncate(it.Name, 40), it.InstallDir})
					}
					o.renderTable(t)
					if o.format != "csv" {
						fmt.Fprintf(w, "%s\n\n", faint(fmt.Sprintf("%d local game(s) matched.", len(res.LocalApps))))
					}
				}

				// Owned games table
				if len(res.OwnedGames) > 0 {
					hasAny = true
					o.heading(w, "Owned Account Games")
					t := o.newTable(w)
					t.AppendHeader(table.Row{"AppID", "Name", "Playtime"})
					t.SetColumnConfigs([]table.ColumnConfig{
						{Number: 1, Align: text.AlignRight},
						{Number: 3, Align: text.AlignRight},
					})
					for _, it := range res.OwnedGames {
						t.AppendRow(table.Row{it.AppID, truncate(it.Name, 50), formatPlaytime(it.PlaytimeForever)})
					}
					o.renderTable(t)
					if o.format != "csv" {
						fmt.Fprintf(w, "%s\n\n", faint(fmt.Sprintf("%d owned game(s) matched.", len(res.OwnedGames))))
					}
				}

				// Players table
				if len(res.Players) > 0 {
					hasAny = true
					o.heading(w, "Community Players")
					t := o.newTable(w)
					t.AppendHeader(table.Row{"Persona", "SteamID64", "Profile URL"})
					for _, p := range res.Players {
						t.AppendRow(table.Row{p.Persona, p.SteamID, p.ProfileURL})
					}
					o.renderTable(t)
					if o.format != "csv" {
						fmt.Fprintf(w, "%s\n\n", faint(fmt.Sprintf("%d player(s) found.", len(res.Players))))
					}
				}

				// Workshop items table
				if len(res.WorkshopItems) > 0 {
					hasAny = true
					o.heading(w, "Workshop Items")
					t := o.newTable(w)
					t.AppendHeader(table.Row{"ID", "Title", "Game", "Subscribers", "Updated"})
					t.SetColumnConfigs([]table.ColumnConfig{
						{Number: 4, Align: text.AlignRight, Transformer: thousandsT},
					})
					for _, it := range res.WorkshopItems {
						gameName := it.AppName
						if gameName == "" && it.AppID > 0 {
							gameName = strconv.Itoa(it.AppID)
						}
						t.AppendRow(table.Row{it.ID, truncate(it.Title, 40), truncate(gameName, 20), it.Subscriptions, it.Updated})
					}
					o.renderTable(t)
					if o.format != "csv" {
						fmt.Fprintf(w, "%s\n\n", faint(fmt.Sprintf("%d workshop item(s) found.", len(res.WorkshopItems))))
					}
				}

				if !hasAny {
					fmt.Fprintf(w, "No results found for %q across Steam apps, workshop items, or accounts.\n", q)
				}
			})
		},
	}
	cmd.Flags().IntVarP(&maxPerType, "limit", "n", 100, "Maximum results per category (up to 100)")
	return cmd
}
