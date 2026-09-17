package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
	"steamcli.local/steam/internal/community"
	"steamcli.local/steam/internal/library"
	"steamcli.local/steam/internal/webapi"
	"steamcli.local/steam/internal/workshop"
)

// summarize reports how a batch of per-item operations turned out, so a
// partial failure is visible instead of being averaged into "success".
func summarize(results []workshop.BatchResult) map[string]any {
	ok := 0
	for _, r := range results {
		if r.Success {
			ok++
		}
	}
	return map[string]any{
		"succeeded": ok,
		"failed":    len(results) - ok,
		"results":   results,
	}
}

// batchErr makes the process exit nonzero when no item in a batch succeeded.
func batchErr(results []workshop.BatchResult) error {
	if len(results) == 0 {
		return nil
	}
	for _, r := range results {
		if r.Success {
			return nil
		}
	}
	return fmt.Errorf("all %d operations failed; see the results above", len(results))
}

func workshopCommand(o *options) *cobra.Command {
	root := &cobra.Command{
		Use:   "workshop",
		Short: "Manage Workshop items and collections (subscribe, search, create, edit, delete)",
	}

	workshopClient := func() (*workshop.Client, error) {
		s, err := o.settings()
		if err != nil {
			return nil, err
		}
		key, err := s.WebKey()
		if err != nil {
			return nil, err
		}
		token, err := s.AccessToken()
		if err != nil {
			return nil, err
		}
		cookie, err := s.CommunityLoginSecure()
		if err != nil {
			return nil, err
		}
		http := o.http()
		return &workshop.Client{
			Web: &webapi.Client{
				HTTP:        http,
				BaseURL:     s.WebURL,
				Key:         key,
				CacheDir:    s.CacheDir,
				AccessToken: token,
			},
			Community: &community.Client{
				HTTP:        http,
				BaseURL:     s.CommunityURL,
				LoginSecure: cookie,
			},
		}, nil
	}

	// resolveItems expands the item sources shared by several subcommands.
	resolveItems := func(cmd *cobra.Command, wc *workshop.Client, appID int, explicit []string, fromCollection string, fromSubs, fromFavs, fromInstalled bool) ([]string, error) {
		items := append([]string(nil), explicit...)

		if fromCollection != "" {
			coll, err := wc.GetCollectionDetails(cmd.Context(), fromCollection)
			if err != nil {
				return nil, fmt.Errorf("resolve collection %s: %w", fromCollection, err)
			}
			for _, ch := range coll.Children {
				items = append(items, ch.PublishedFileID)
			}
		}
		if fromSubs {
			ids, err := wc.ListUserItems(cmd.Context(), appID, community.FilterSubscriptions)
			if err != nil {
				return nil, fmt.Errorf("list subscriptions: %w", err)
			}
			items = append(items, ids...)
		}
		if fromFavs {
			ids, err := wc.ListUserItems(cmd.Context(), appID, community.FilterFavorites)
			if err != nil {
				return nil, fmt.Errorf("list favorites: %w", err)
			}
			items = append(items, ids...)
		}
		if fromInstalled {
			rep, err := library.Scan(library.Defaults())
			if err != nil {
				return nil, fmt.Errorf("scan local libraries: %w", err)
			}
			apps, err := workshop.ScanInstalled(rep.Libraries, appID)
			if err != nil {
				return nil, err
			}
			for _, a := range apps {
				items = append(items, a.Items...)
			}
		}

		seen := make(map[string]bool)
		var deduped []string
		for _, it := range items {
			it = strings.TrimSpace(it)
			if it != "" && !seen[it] {
				seen[it] = true
				deduped = append(deduped, it)
			}
		}
		return deduped, nil
	}

	appIDArg := func(cmd *cobra.Command, s string) (int, error) {
		return o.resolveAppID(cmd.Context(), s, cmd.ErrOrStderr())
	}

	// 1. Subscribe
	var subFromCollection string
	var subFromFavs, subFromInstalled bool
	sub := &cobra.Command{
		Use:     "sub APPID [ITEMID...]",
		Aliases: []string{"subscribe"},
		Short:   "Subscribe to workshop items, a whole collection, or your favorites",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appID, err := appIDArg(cmd, args[0])
			if err != nil {
				return err
			}
			wc, err := workshopClient()
			if err != nil {
				return err
			}
			items, err := resolveItems(cmd, wc, appID, args[1:], subFromCollection, false, subFromFavs, subFromInstalled)
			if err != nil {
				return err
			}
			if len(items) == 0 {
				return errors.New("no items to subscribe to; pass ITEMIDs or use --from-collection, --from-favorites, or --from-installed")
			}
			results := wc.Subscribe(cmd.Context(), appID, items)
			if err := o.emit(cmd, summarize(results), o.renderBatch(results)); err != nil {
				return err
			}
			return batchErr(results)
		},
	}
	sub.Flags().StringVar(&subFromCollection, "from-collection", "", "Subscribe to every item in this collection")
	sub.Flags().BoolVar(&subFromFavs, "from-favorites", false, "Subscribe to your favorited items for this game")
	sub.Flags().BoolVar(&subFromInstalled, "from-installed", false, "Subscribe to items already present in your local library")

	// 2. Unsubscribe
	var unsubFromCollection string
	var unsubAll bool
	unsub := &cobra.Command{
		Use:     "unsub APPID [ITEMID...]",
		Aliases: []string{"unsubscribe"},
		Short:   "Unsubscribe from workshop items, a whole collection, or everything for a game",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appID, err := appIDArg(cmd, args[0])
			if err != nil {
				return err
			}
			wc, err := workshopClient()
			if err != nil {
				return err
			}
			items, err := resolveItems(cmd, wc, appID, args[1:], unsubFromCollection, unsubAll, false, false)
			if err != nil {
				return err
			}
			if len(items) == 0 {
				return errors.New("no items to unsubscribe from; pass ITEMIDs or use --from-collection or --all")
			}
			results := wc.Unsubscribe(cmd.Context(), appID, items)
			if err := o.emit(cmd, summarize(results), o.renderBatch(results)); err != nil {
				return err
			}
			return batchErr(results)
		},
	}
	unsub.Flags().StringVar(&unsubFromCollection, "from-collection", "", "Unsubscribe from every item in this collection")
	unsub.Flags().BoolVar(&unsubAll, "all", false, "Unsubscribe from every item you are subscribed to for this game")

	// 3. Collection inspection
	var withItemDetails, idsOnly bool
	collection := &cobra.Command{
		Use:   "collection COLLECTION_ID",
		Short: "Inspect a collection's metadata and child items",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			wc, err := workshopClient()
			if err != nil {
				return err
			}
			coll, err := wc.GetCollectionDetails(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			childIDs := make([]string, len(coll.Children))
			for i, ch := range coll.Children {
				childIDs[i] = ch.PublishedFileID
			}
			if idsOnly {
				return o.emit(cmd, childIDs, func(w io.Writer) {
					for _, id := range childIDs {
						fmt.Fprintln(w, id)
					}
				})
			}
			if !withItemDetails || len(coll.Children) == 0 {
				return o.emit(cmd, coll, func(w io.Writer) { o.renderCollection(w, coll, nil) })
			}
			details, err := wc.GetDetails(cmd.Context(), childIDs)
			if err != nil {
				return fmt.Errorf("fetch item details (use --items=false to skip): %w", err)
			}
			return o.emit(cmd, map[string]any{"collection": coll, "items": details},
				func(w io.Writer) { o.renderCollection(w, coll, details) })
		},
	}
	collection.Flags().BoolVar(&withItemDetails, "items", true, "Fetch full metadata for all items in the collection")
	collection.Flags().BoolVar(&idsOnly, "ids-only", false, "Output only child item IDs, one per line")

	// Info for individual items
	infoCmd := &cobra.Command{
		Use:     "info ITEMID...",
		Aliases: []string{"item", "details"},
		Short:   "View metadata and details for one or more Workshop items",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			wc, err := workshopClient()
			if err != nil {
				return err
			}
			details, err := wc.GetDetails(cmd.Context(), args)
			if err != nil {
				return err
			}
			var items []workshop.PublishedFileDetails
			for _, id := range args {
				if d, ok := details[id]; ok {
					items = append(items, d)
				}
			}
			if len(items) == 1 {
				it := items[0]
				return o.emit(cmd, it, func(w io.Writer) {
					t := o.newDetail(w)
					detailRows(t,
						kv("Title", it.Title),
						kv("ID", it.PublishedFileID),
						kv("Creator", it.Creator),
						kv("AppID", fmt.Sprint(it.ConsumerAppID)),
						kv("Subscriptions", thousands(it.Subscriptions)),
						kv("Favorites", thousands(it.Favorites)),
						kv("Views", thousands(it.Views)),
						kv("Updated", unixDate(it.TimeUpdated)),
						kv("URL", "https://steamcommunity.com/sharedfiles/filedetails/?id="+it.PublishedFileID),
					)
					o.renderTable(t)
					if it.Description != "" {
						fmt.Fprintf(w, "\n%s\n", truncate(it.Description, 500))
					}
				})
			}
			return o.emit(cmd, items, func(w io.Writer) {
				t := o.newTable(w)
				t.AppendHeader(table.Row{"ID", "Title", "Subscribers", "Favorites", "Updated"})
				t.SetColumnConfigs([]table.ColumnConfig{
					{Number: 3, Align: text.AlignRight, Transformer: o.numberT()},
					{Number: 4, Align: text.AlignRight, Transformer: o.numberT()},
				})
				for _, it := range items {
					t.AppendRow(table.Row{it.PublishedFileID, truncate(it.Title, 48),
						it.Subscriptions, it.Favorites, unixDate(it.TimeUpdated)})
				}
				o.renderTable(t)
			})
		},
	}

	// 4. Subscriptions and favorites, as Steam records them
	listCmd := func(use, filter, short string, aliases []string) *cobra.Command {
		var listDetails bool
		c := &cobra.Command{
			Use:     use,
			Aliases: aliases,
			Short:   short,
			Args:    cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				appID, err := appIDArg(cmd, args[0])
				if err != nil {
					return err
				}
				wc, err := workshopClient()
				if err != nil {
					return err
				}
				ids, err := wc.ListUserItems(cmd.Context(), appID, filter)
				if err != nil {
					return err
				}
				out := map[string]any{"appid": appID, "total": len(ids), "items": ids}
				var details map[string]workshop.PublishedFileDetails
				if listDetails && len(ids) > 0 {
					details, err = wc.GetDetails(cmd.Context(), ids)
					if err != nil {
						return fmt.Errorf("fetch item details: %w", err)
					}
					out["details"] = details
				}
				return o.emit(cmd, out, func(w io.Writer) {
					t := o.newTable(w)
					if details != nil {
						t.AppendHeader(table.Row{"ID", "Title", "Updated"})
						for _, id := range ids {
							d := details[id]
							t.AppendRow(table.Row{id, truncate(d.Title, 56), unixDate(d.TimeUpdated)})
						}
					} else {
						t.AppendHeader(table.Row{"ID"})
						for _, id := range ids {
							t.AppendRow(table.Row{id})
						}
					}
					o.renderTable(t)
					if o.format != "csv" {
						fmt.Fprintf(w, "%s\n", faint(fmt.Sprintf("%d item(s) for AppID %d.", len(ids), appID)))
					}
				})
			},
		}
		c.Flags().BoolVar(&listDetails, "details", false, "Fetch title and metadata for each item")
		return c
	}
	subsCmd := listCmd("subs APPID", community.FilterSubscriptions,
		"List the items you are subscribed to for a game (requires a Community session)",
		[]string{"subscriptions", "list-subs"})
	favsCmd := listCmd("favorites APPID", community.FilterFavorites,
		"List the items you have favorited for a game (requires a Community session)",
		[]string{"favs", "list-favorites"})

	// 5. Locally installed items
	var installedRoots []string
	var installedDetails bool
	installedCmd := &cobra.Command{
		Use:     "installed [APPID]",
		Aliases: []string{"local"},
		Short:   "List workshop items present on disk (local library state, not subscriptions)",
		Long: "List workshop items found in local Steam library folders.\n\n" +
			"This reports disk state. An item you are subscribed to but have not downloaded\n" +
			"will not appear, and an item left on disk after unsubscribing still will.\n" +
			"For the account's real subscription list, use 'steam workshop subs'.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := installedRoots
			if len(r) == 0 {
				r = library.Defaults()
			}
			rep, err := library.Scan(r)
			if err != nil {
				return err
			}
			targetAppID := 0
			if len(args) > 0 {
				if targetAppID, err = appIDArg(cmd, args[0]); err != nil {
					return err
				}
			}
			apps, err := workshop.ScanInstalled(rep.Libraries, targetAppID)
			if err != nil {
				return err
			}
			if !installedDetails {
				return o.emit(cmd, apps, func(w io.Writer) {
					t := o.newTable(w)
					t.AppendHeader(table.Row{"AppID", "Items"})
					t.SetColumnConfigs([]table.ColumnConfig{{Number: 2, Align: text.AlignRight}})
					for _, a := range apps {
						t.AppendRow(table.Row{a.AppID, a.Total})
					}
					o.renderTable(t)
				})
			}
			wc, err := workshopClient()
			if err != nil {
				return err
			}
			var allIDs []string
			for _, a := range apps {
				allIDs = append(allIDs, a.Items...)
			}
			details, err := wc.GetDetails(cmd.Context(), allIDs)
			if err != nil {
				return fmt.Errorf("fetch item details (omit --details to skip): %w", err)
			}
			return o.emit(cmd, map[string]any{"apps": apps, "items": details}, func(w io.Writer) {
				t := o.newTable(w)
				t.AppendHeader(table.Row{"AppID", "Item", "Title"})
				for _, a := range apps {
					for _, id := range a.Items {
						t.AppendRow(table.Row{a.AppID, id, truncate(details[id].Title, 56)})
					}
				}
				o.renderTable(t)
			})
		},
	}
	installedCmd.Flags().BoolVar(&installedDetails, "details", false, "Fetch title and metadata for installed items")
	installedCmd.Flags().StringArrayVar(&installedRoots, "root", nil, "Steam root directory; repeat for multiple installations")

	// 6. Search
	newSearch := func(use, short string, aliases []string, collectionsOnly bool) *cobra.Command {
		var page, count int
		var query string
		var fileType int
		var all bool
		c := &cobra.Command{
			Use:     use,
			Aliases: aliases,
			Short:   short,
			Args:    cobra.RangeArgs(1, 2),
			RunE: func(cmd *cobra.Command, args []string) error {
				appID, err := appIDArg(cmd, args[0])
				if err != nil {
					return err
				}
				wc, err := workshopClient()
				if err != nil {
					return err
				}
				q := query
				if len(args) > 1 && q == "" {
					q = args[1]
				}
				ft := fileType
				if collectionsOnly {
					ft = 2
				}

				opts := workshop.QueryOptions{
					AppID:      appID,
					FileType:   ft,
					NumPerPage: count,
					SearchText: q,
					Page:       page,
					AllPages:   all,
				}
				items, total, err := wc.Query(cmd.Context(), opts)
				if err != nil {
					return err
				}
				key := "items"
				if collectionsOnly {
					key = "collections"
				}
				return o.emit(cmd, map[string]any{
					"total": total,
					"count": len(items),
					key:     items,
				}, func(w io.Writer) {
					t := o.newTable(w)
					t.AppendHeader(table.Row{"ID", "Title", "Subscribers", "Favorites", "Updated"})
					t.SetColumnConfigs([]table.ColumnConfig{
						{Number: 3, Align: text.AlignRight, Transformer: o.numberT()},
						{Number: 4, Align: text.AlignRight, Transformer: o.numberT()},
					})
					for _, it := range items {
						t.AppendRow(table.Row{it.PublishedFileID, truncate(it.Title, 48),
							it.Subscriptions, it.Favorites, unixDate(it.TimeUpdated)})
					}
					o.renderTable(t)
					if o.format != "csv" {
						fmt.Fprintf(w, "%s\n", faint(fmt.Sprintf("%d of %d shown.", len(items), total)))
					}
				})
			},
		}
		c.Flags().IntVar(&page, "page", 1, "Results page number")
		c.Flags().IntVar(&count, "count", 20, "Number of items per page (max 100)")
		c.Flags().StringVarP(&query, "query", "q", "", "Search text matched against title and description")
		c.Flags().BoolVar(&all, "all", false, "Page through every result using Steam's cursor")
		if !collectionsOnly {
			c.Flags().IntVar(&fileType, "filetype", -1, "File type filter: 0=item, 2=collection (-1 for all)")
		}
		return c
	}
	searchCmd := newSearch("search APPID [QUERY]", "Search or list workshop items for a game", []string{"search-items"}, false)
	listColls := newSearch("search-collections APPID [QUERY]", "Search or list workshop collections for a game", []string{"list-collections"}, true)

	// 7. Create collection
	var title, desc string
	var visibility int
	var createFromSubs, createFromFavs, createFromInstalled bool
	var createFromCollection string
	var itemIDs []string
	createColl := &cobra.Command{
		Use:   "create-collection APPID --title TITLE",
		Short: "Create a workshop collection, optionally populated from your subscriptions or favorites",
		Long: "Create a workshop collection.\n\n" +
			"Creating the collection uses the Steam Web API. Adding items to it does not:\n" +
			"the Web API has no method that sets collection membership, so items are added\n" +
			"through an authenticated Community session. Populating a collection therefore\n" +
			"requires STEAM_LOGIN_SECURE to be set; creating an empty one does not.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appID, err := appIDArg(cmd, args[0])
			if err != nil {
				return err
			}
			if title == "" {
				return errors.New("--title is required")
			}
			wc, err := workshopClient()
			if err != nil {
				return err
			}
			items, err := resolveItems(cmd, wc, appID, itemIDs, createFromCollection, createFromSubs, createFromFavs, createFromInstalled)
			if err != nil {
				return err
			}
			id, added, err := wc.CreateCollection(cmd.Context(), appID, title, desc, visibility, items)
			if err != nil {
				return err
			}
			out := map[string]any{
				"publishedfileid": id,
				"title":           title,
				"url":             "https://steamcommunity.com/sharedfiles/filedetails/?id=" + id,
				"items_requested": len(items),
			}
			if len(added) > 0 {
				out["items_added"] = summarize(added)
			}
			if err := o.print(cmd, out); err != nil {
				return err
			}
			return batchErr(added)
		},
	}
	createColl.Flags().StringVar(&title, "title", "", "Collection title")
	createColl.Flags().StringVar(&desc, "description", "", "Collection description")
	createColl.Flags().IntVar(&visibility, "visibility", 0, "Visibility: 0=Public, 1=FriendsOnly, 2=Private")
	createColl.Flags().BoolVar(&createFromSubs, "from-subscriptions", false, "Populate from the items you are subscribed to for this game")
	createColl.Flags().BoolVar(&createFromFavs, "from-favorites", false, "Populate from the items you have favorited for this game")
	createColl.Flags().BoolVar(&createFromInstalled, "from-installed", false, "Populate from items present in your local library")
	createColl.Flags().StringVar(&createFromCollection, "from-collection", "", "Populate by copying another collection's items")
	createColl.Flags().StringArrayVar(&itemIDs, "item", nil, "Item ID to include; repeat for multiple")

	// 8. Edit collection
	var newTitle, newDesc string
	var newVisibility int
	editColl := &cobra.Command{
		Use:   "edit-collection APPID COLLECTION_ID",
		Short: "Edit a collection's title, description, or visibility",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			appID, err := appIDArg(cmd, args[0])
			if err != nil {
				return err
			}
			wc, err := workshopClient()
			if err != nil {
				return err
			}
			if err := wc.EditCollection(cmd.Context(), appID, args[1], newTitle, newDesc, newVisibility); err != nil {
				return err
			}
			return o.print(cmd, map[string]any{"success": true, "publishedfileid": args[1]})
		},
	}
	editColl.Flags().StringVar(&newTitle, "title", "", "Updated collection title")
	editColl.Flags().StringVar(&newDesc, "description", "", "Updated collection description")
	editColl.Flags().IntVar(&newVisibility, "visibility", -1, "Updated visibility: 0=Public, 1=FriendsOnly, 2=Private (-1 leaves unchanged)")

	// 9. Collection membership
	newMembership := func(use, short string, add bool) *cobra.Command {
		var fromCollection string
		var fromSubs, fromFavs, fromInstalled bool
		c := &cobra.Command{
			Use:   use,
			Short: short,
			Long:  short + ".\n\nCollection membership is not exposed by the Steam Web API, so this requires\nan authenticated Community session (STEAM_LOGIN_SECURE).",
			Args:  cobra.MinimumNArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				appID, err := appIDArg(cmd, args[0])
				if err != nil {
					return err
				}
				wc, err := workshopClient()
				if err != nil {
					return err
				}
				if !wc.HasSession() {
					return community.ErrNoSession
				}
				items, err := resolveItems(cmd, wc, appID, args[2:], fromCollection, fromSubs, fromFavs, fromInstalled)
				if err != nil {
					return err
				}
				if len(items) == 0 {
					return errors.New("no items specified")
				}
				var results []workshop.BatchResult
				if add {
					results = wc.AddItems(cmd.Context(), args[1], items)
				} else {
					results = wc.RemoveItems(cmd.Context(), args[1], items)
				}
				if err := o.emit(cmd, summarize(results), o.renderBatch(results)); err != nil {
					return err
				}
				return batchErr(results)
			},
		}
		c.Flags().StringVar(&fromCollection, "from-collection", "", "Take the items from another collection")
		c.Flags().BoolVar(&fromSubs, "from-subscriptions", false, "Take the items from your subscriptions for this game")
		c.Flags().BoolVar(&fromFavs, "from-favorites", false, "Take the items from your favorites for this game")
		c.Flags().BoolVar(&fromInstalled, "from-installed", false, "Take the items from your local library")
		return c
	}
	addItems := newMembership("add-items APPID COLLECTION_ID [ITEMID...]", "Add items to a collection", true)
	removeItems := newMembership("remove-items APPID COLLECTION_ID [ITEMID...]", "Remove items from a collection", false)

	// 10. Delete collection
	var confirmDelete bool
	deleteColl := &cobra.Command{
		Use:   "delete-collection APPID COLLECTION_ID",
		Short: "Delete a workshop collection you own",
		Long: "Delete a workshop collection you own.\n\n" +
			"Prefers your Community session, because IPublishedFileService/Delete is\n" +
			"publisher-only and rejects ordinary user API keys.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			appID, err := appIDArg(cmd, args[0])
			if err != nil {
				return err
			}
			if !confirmDelete {
				return fmt.Errorf("deleting collection %s cannot be undone; pass --yes to proceed", args[1])
			}
			wc, err := workshopClient()
			if err != nil {
				return err
			}
			if err := wc.DeleteCollection(cmd.Context(), appID, args[1]); err != nil {
				return err
			}
			return o.print(cmd, map[string]any{"success": true, "publishedfileid": args[1]})
		},
	}
	deleteColl.Flags().BoolVar(&confirmDelete, "yes", false, "Confirm the deletion")

	// 11. Sync subscriptions with collection
	syncCmd := &cobra.Command{
		Use:   "sync APPID COLLECTION_ID",
		Short: "Synchronize subscriptions with a collection (subscribe missing, unsubscribe extraneous)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			appID, err := appIDArg(cmd, args[0])
			if err != nil {
				return err
			}
			wc, err := workshopClient()
			if err != nil {
				return err
			}
			if !wc.HasSession() {
				return community.ErrNoSession
			}

			// Get collection target items
			coll, err := wc.GetCollectionDetails(cmd.Context(), args[1])
			if err != nil {
				return fmt.Errorf("fetch collection %s: %w", args[1], err)
			}
			targetMap := make(map[string]bool)
			for _, ch := range coll.Children {
				targetMap[ch.PublishedFileID] = true
			}

			// Get current subscriptions
			currentSubs, err := wc.ListUserItems(cmd.Context(), appID, community.FilterSubscriptions)
			if err != nil {
				return fmt.Errorf("fetch subscriptions for %d: %w", appID, err)
			}
			currentMap := make(map[string]bool)
			for _, id := range currentSubs {
				currentMap[id] = true
			}

			var toSub, toUnsub []string
			for _, ch := range coll.Children {
				if !currentMap[ch.PublishedFileID] {
					toSub = append(toSub, ch.PublishedFileID)
				}
			}
			for _, id := range currentSubs {
				if !targetMap[id] {
					toUnsub = append(toUnsub, id)
				}
			}

			var allResults []workshop.BatchResult
			if len(toUnsub) > 0 {
				unsubResults := wc.Unsubscribe(cmd.Context(), appID, toUnsub)
				allResults = append(allResults, unsubResults...)
			}
			if len(toSub) > 0 {
				subResults := wc.Subscribe(cmd.Context(), appID, toSub)
				allResults = append(allResults, subResults...)
			}

			if len(allResults) == 0 {
				return o.emit(cmd, map[string]any{"synced": true, "message": "Already fully in sync with collection"}, func(w io.Writer) {
					fmt.Fprintf(w, "%s Subscriptions are already perfectly in sync with collection %s (%d items)\n",
						green.Sprint("✓"), args[1], len(coll.Children))
				})
			}

			if err := o.emit(cmd, summarize(allResults), o.renderBatch(allResults)); err != nil {
				return err
			}
			return batchErr(allResults)
		},
	}

	root.AddCommand(infoCmd, sub, unsub, collection, subsCmd, favsCmd, installedCmd, searchCmd, listColls,
		createColl, editColl, addItems, removeItems, deleteColl, syncCmd)
	return root
}

// --- human-readable renderers ----------------------------------------------

func truncate(s string, n int) string {
	if s == "" {
		return "(untitled)"
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func unixDate(t int64) string {
	if t <= 0 {
		return ""
	}
	return time.Unix(t, 0).UTC().Format("2006-01-02")
}

func (o *options) renderBatch(results []workshop.BatchResult) func(io.Writer) {
	return func(w io.Writer) {
		t := o.newTable(w)
		t.AppendHeader(table.Row{"Item", "Result"})
		ok := 0
		for _, r := range results {
			if r.Success {
				ok++
				t.AppendRow(table.Row{r.PublishedFileID, colorOK(true, "ok", "")})
			} else {
				t.AppendRow(table.Row{r.PublishedFileID, colorOK(false, "", r.Error)})
			}
		}
		o.renderTable(t)
		if o.format != "csv" {
			summary := fmt.Sprintf("%d succeeded, %d failed.", ok, len(results)-ok)
			if ok == len(results) {
				fmt.Fprintln(w, green.Sprint(summary))
			} else if ok == 0 {
				fmt.Fprintln(w, red.Sprint(summary))
			} else {
				fmt.Fprintln(w, yellow.Sprint(summary))
			}
		}
	}
}

func (o *options) renderCollection(w io.Writer, coll workshop.CollectionDetails, details map[string]workshop.PublishedFileDetails) {
	d := coll.Details
	t := o.newDetail(w)
	if d != nil {
		detailRows(t,
			kv("Title", d.Title),
			kv("ID", coll.PublishedFileID),
			kv("Creator", d.Creator),
			kv("AppID", fmt.Sprint(d.ConsumerAppID)),
			kv("Favorites", thousands(d.Favorites)),
			kv("Views", thousands(d.Views)),
			kv("Updated", unixDate(d.TimeUpdated)),
			kv("Children", fmt.Sprint(len(coll.Children))),
			kv("URL", "https://steamcommunity.com/sharedfiles/filedetails/?id="+coll.PublishedFileID),
		)
	} else {
		detailRows(t, kv("ID", coll.PublishedFileID), kv("Children", fmt.Sprint(len(coll.Children))))
	}
	o.renderTable(t)

	if len(coll.Children) == 0 {
		return
	}
	ct := o.newTable(w)
	if details != nil {
		ct.AppendHeader(table.Row{"Item", "Title", "Updated"})
	} else {
		ct.AppendHeader(table.Row{"Item", "Sort"})
	}
	for _, ch := range coll.Children {
		if details != nil {
			cd := details[ch.PublishedFileID]
			ct.AppendRow(table.Row{ch.PublishedFileID, truncate(cd.Title, 56), unixDate(cd.TimeUpdated)})
		} else {
			ct.AppendRow(table.Row{ch.PublishedFileID, ch.SortOrder})
		}
	}
	o.renderTable(ct)
}
