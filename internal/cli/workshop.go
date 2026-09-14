package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"steamcli.local/steam/internal/library"
	"steamcli.local/steam/internal/webapi"
	"steamcli.local/steam/internal/workshop"
)

func workshopCommand(o *options) *cobra.Command {
	root := &cobra.Command{
		Use:   "workshop",
		Short: "Manage Workshop items and collections (subscribe, query, create, edit, delete)",
	}

	workshopClient := func() (*workshop.Client, error) {
		s, err := o.settings()
		if err != nil {
			return nil, err
		}
		key, _ := s.WebKey()
		token, _ := s.AccessToken()
		web := &webapi.Client{
			HTTP:        o.http(),
			BaseURL:     s.WebURL,
			Key:         key,
			CacheDir:    s.CacheDir,
			AccessToken: token,
		}
		return &workshop.Client{Web: web}, nil
	}

	// 1. Subscribe (multi, or from collection)
	var fromCollectionSub string
	sub := &cobra.Command{
		Use:     "sub APPID [ITEMID...]",
		Aliases: []string{"subscribe"},
		Short:   "Subscribe to one or multiple workshop items, or all items in a collection",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appID, err := strconv.Atoi(args[0])
			if err != nil {
				return errors.New("APPID must be an integer")
			}
			wc, err := workshopClient()
			if err != nil {
				return err
			}

			items := args[1:]
			if fromCollectionSub != "" {
				coll, err := wc.GetCollectionDetails(cmd.Context(), fromCollectionSub)
				if err != nil {
					return fmt.Errorf("failed resolving collection items: %w", err)
				}
				for _, ch := range coll.Children {
					items = append(items, ch.PublishedFileID)
				}
			}

			if len(items) == 0 {
				return errors.New("specify at least one ITEMID or use --from-collection")
			}

			results := wc.Subscribe(cmd.Context(), appID, items)
			return o.print(cmd, results)
		},
	}
	sub.Flags().StringVar(&fromCollectionSub, "from-collection", "", "Subscribe to all items in this collection")

	// 2. Unsubscribe (multi, or from collection)
	var fromCollectionUnsub string
	unsub := &cobra.Command{
		Use:     "unsub APPID [ITEMID...]",
		Aliases: []string{"unsubscribe"},
		Short:   "Unsubscribe from one or multiple workshop items, or all items in a collection",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appID, err := strconv.Atoi(args[0])
			if err != nil {
				return errors.New("APPID must be an integer")
			}
			wc, err := workshopClient()
			if err != nil {
				return err
			}

			items := args[1:]
			if fromCollectionUnsub != "" {
				coll, err := wc.GetCollectionDetails(cmd.Context(), fromCollectionUnsub)
				if err != nil {
					return fmt.Errorf("failed resolving collection items: %w", err)
				}
				for _, ch := range coll.Children {
					items = append(items, ch.PublishedFileID)
				}
			}

			if len(items) == 0 {
				return errors.New("specify at least one ITEMID or use --from-collection")
			}

			results := wc.Unsubscribe(cmd.Context(), appID, items)
			return o.print(cmd, results)
		},
	}
	unsub.Flags().StringVar(&fromCollectionUnsub, "from-collection", "", "Unsubscribe from all items in this collection")

	// 3. Collection details & inspection
	var withItemDetails bool
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

			if withItemDetails && len(coll.Children) > 0 {
				childIDs := make([]string, len(coll.Children))
				for i, ch := range coll.Children {
					childIDs[i] = ch.PublishedFileID
				}
				details, err := wc.GetDetails(cmd.Context(), childIDs)
				if err == nil {
					return o.print(cmd, map[string]any{
						"collection": coll,
						"items":      details,
					})
				}
			}

			return o.print(cmd, coll)
		},
	}
	collection.Flags().BoolVar(&withItemDetails, "items", true, "Fetch full metadata for all items in the collection")

	// 3b. List user's subscriptions (for a specific game or all games)
	var withSubDetails bool
	var roots []string
	subsCmd := &cobra.Command{
		Use:     "subs [APPID]",
		Aliases: []string{"list-subs", "subscriptions"},
		Short:   "List subscribed/installed workshop items for a game or all games",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := roots
			if len(r) == 0 {
				r = library.Defaults()
			}
			rep, err := library.Scan(r)
			if err != nil {
				return err
			}

			targetAppID := 0
			if len(args) > 0 {
				targetAppID, err = strconv.Atoi(args[0])
				if err != nil {
					return errors.New("APPID must be an integer")
				}
			}

			subscribedApps, err := workshop.ScanLocalSubscriptions(rep.Libraries, targetAppID)
			if err != nil {
				return err
			}

			if withSubDetails {
				wc, err := workshopClient()
				if err == nil {
					allItemIDs := make([]string, 0)
					for _, sa := range subscribedApps {
						allItemIDs = append(allItemIDs, sa.Items...)
					}
					details, _ := wc.GetDetails(cmd.Context(), allItemIDs)
					return o.print(cmd, map[string]any{
						"apps":  subscribedApps,
						"items": details,
					})
				}
			}

			return o.print(cmd, subscribedApps)
		},
	}
	subsCmd.Flags().BoolVar(&withSubDetails, "details", false, "Fetch title and metadata for subscribed items")
	subsCmd.Flags().StringArrayVar(&roots, "root", nil, "Steam root directory; repeat for multiple installations")


	// 4. Search items & collections
	var page, count int
	var query string
	var searchFileType int
	searchCmd := &cobra.Command{
		Use:     "search APPID [QUERY]",
		Aliases: []string{"search-items"},
		Short:   "Search or list workshop items for a game",
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			appID, err := strconv.Atoi(args[0])
			if err != nil {
				return errors.New("APPID must be an integer")
			}
			wc, err := workshopClient()
			if err != nil {
				return err
			}

			q := query
			if len(args) > 1 && q == "" {
				q = args[1]
			}

			items, total, err := wc.QueryItems(cmd.Context(), appID, searchFileType, page, count, q)
			if err != nil {
				return err
			}

			return o.print(cmd, map[string]any{
				"total": total,
				"page":  page,
				"count": len(items),
				"items": items,
			})
		},
	}
	searchCmd.Flags().IntVar(&page, "page", 1, "Results page number")
	searchCmd.Flags().IntVar(&count, "count", 20, "Number of items per page")
	searchCmd.Flags().StringVarP(&query, "query", "q", "", "Search query for item title/description")
	searchCmd.Flags().IntVar(&searchFileType, "filetype", -1, "File type filter: 0=Community Item, 2=Collection (-1 for all)")

	// 4b. Search / List collections
	listColls := &cobra.Command{
		Use:     "search-collections APPID [QUERY]",
		Aliases: []string{"list-collections"},
		Short:   "Search or list workshop collections for a game",
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			appID, err := strconv.Atoi(args[0])
			if err != nil {
				return errors.New("APPID must be an integer")
			}
			wc, err := workshopClient()
			if err != nil {
				return err
			}

			q := query
			if len(args) > 1 && q == "" {
				q = args[1]
			}

			items, total, err := wc.QueryCollections(cmd.Context(), appID, page, count, q)
			if err != nil {
				return err
			}

			return o.print(cmd, map[string]any{
				"total":       total,
				"page":        page,
				"count":       len(items),
				"collections": items,
			})
		},
	}
	listColls.Flags().IntVar(&page, "page", 1, "Results page number")
	listColls.Flags().IntVar(&count, "count", 20, "Number of items per page")
	listColls.Flags().StringVarP(&query, "query", "q", "", "Search query for collection title/description")

	// 5. Create collection
	var title, desc string
	var visibility int
	var fromSubs, fromFavs bool
	var itemIDs []string
	createColl := &cobra.Command{
		Use:   "create-collection APPID --title TITLE",
		Short: "Create a new workshop collection (supports importing from subscriptions or favorites)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appID, err := strconv.Atoi(args[0])
			if err != nil {
				return errors.New("APPID must be an integer")
			}
			if title == "" {
				return errors.New("--title is required")
			}
			wc, err := workshopClient()
			if err != nil {
				return err
			}

			initialItems := append([]string(nil), itemIDs...)

			if fromSubs {
				rep, err := library.Scan(library.Defaults())
				if err == nil {
					subApps, err := workshop.ScanLocalSubscriptions(rep.Libraries, appID)
					if err == nil {
						for _, sa := range subApps {
							initialItems = append(initialItems, sa.Items...)
						}
					}
				}
			}

			if fromFavs {
				// Query user's favorites if accessible via API/web
				favItems, _, err := wc.QueryItems(cmd.Context(), appID, 2, 1, 100, "")
				if err == nil {
					for _, fi := range favItems {
						initialItems = append(initialItems, fi.PublishedFileID)
					}
				}
			}

			// Deduplicate items
			seen := make(map[string]bool)
			var deduped []string
			for _, it := range initialItems {
				it = strings.TrimSpace(it)
				if it != "" && !seen[it] {
					seen[it] = true
					deduped = append(deduped, it)
				}
			}

			id, err := wc.CreateCollection(cmd.Context(), appID, title, desc, visibility, deduped)
			if err != nil {
				return err
			}
			return o.print(cmd, map[string]any{
				"success":         true,
				"publishedfileid": id,
				"title":           title,
				"items_count":     len(deduped),
				"items":           deduped,
			})
		},
	}
	createColl.Flags().StringVar(&title, "title", "", "Collection title")
	createColl.Flags().StringVar(&desc, "description", "", "Collection description")
	createColl.Flags().IntVar(&visibility, "visibility", 0, "Visibility: 0=Public, 1=FriendsOnly, 2=Private")
	createColl.Flags().BoolVar(&fromSubs, "from-subscriptions", false, "Populate collection with items from local subscriptions for this game")
	createColl.Flags().BoolVar(&fromFavs, "from-favorites", false, "Populate collection with favorited items for this game")
	createColl.Flags().StringArrayVar(&itemIDs, "item", nil, "Specify item IDs to include in the collection; repeat for multiple")

	// 6. Edit collection
	var newTitle, newDesc string
	var newVisibility int
	editColl := &cobra.Command{
		Use:   "edit-collection APPID COLLECTION_ID",
		Short: "Edit a workshop collection's title, description, or visibility",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			appID, err := strconv.Atoi(args[0])
			if err != nil {
				return errors.New("APPID must be an integer")
			}
			wc, err := workshopClient()
			if err != nil {
				return err
			}

			err = wc.EditCollection(cmd.Context(), appID, args[1], newTitle, newDesc, newVisibility)
			if err != nil {
				return err
			}
			return o.print(cmd, map[string]any{
				"success":         true,
				"publishedfileid": args[1],
			})
		},
	}
	editColl.Flags().StringVar(&newTitle, "title", "", "Updated collection title")
	editColl.Flags().StringVar(&newDesc, "description", "", "Updated collection description")
	editColl.Flags().IntVar(&newVisibility, "visibility", -1, "Updated visibility: 0=Public, 1=FriendsOnly, 2=Private (-1 leaves unchanged)")

	// 7. Delete collection
	deleteColl := &cobra.Command{
		Use:   "delete-collection APPID COLLECTION_ID",
		Short: "Delete a workshop collection",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			appID, err := strconv.Atoi(args[0])
			if err != nil {
				return errors.New("APPID must be an integer")
			}
			wc, err := workshopClient()
			if err != nil {
				return err
			}

			err = wc.DeleteCollection(cmd.Context(), appID, args[1])
			if err != nil {
				return err
			}
			return o.print(cmd, map[string]any{
				"success":         true,
				"publishedfileid": args[1],
			})
		},
	}

	root.AddCommand(sub, unsub, collection, subsCmd, searchCmd, listColls, createColl, editColl, deleteColl)
	return root
}
