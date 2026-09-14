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

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
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
