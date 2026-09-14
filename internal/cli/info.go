package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
	"steamcli.local/steam/internal/asf"
	"steamcli.local/steam/internal/community"
	"steamcli.local/steam/internal/httpx"
	"steamcli.local/steam/internal/library"
	"steamcli.local/steam/internal/status"
	"steamcli.local/steam/internal/steamclient"
	"steamcli.local/steam/internal/steamcmd"
	"steamcli.local/steam/internal/webapi"
)

type serverInfoData struct {
	ServerTime       int64  `json:"servertime,omitempty"`
	ServerTimeString string `json:"servertimestring,omitempty"`
	UTCTime          string `json:"utc_time,omitempty"`
	Error            string `json:"error,omitempty"`
}

type asfInfoData struct {
	Version      string `json:"version,omitempty"`
	BuildVariant string `json:"build_variant,omitempty"`
	ProcessID    int64  `json:"process_id,omitempty"`
	MemoryUsage  int64  `json:"memory_usage,omitempty"`
	StartedAt    string `json:"started_at,omitempty"`
	BotsCount    int    `json:"bots_count,omitempty"`
	Error        string `json:"error,omitempty"`
}

type libraryFolderInfo struct {
	Path       string `json:"path"`
	AppsCount  int    `json:"apps_count"`
	SizeBytes  int64  `json:"size_bytes"`
	SizeHuman  string `json:"size_human"`
}

type clientInfoData struct {
	Platform       string              `json:"platform"`
	Profile        string              `json:"profile"`
	SteamPath      string              `json:"steam_path,omitempty"`
	SteamCMDPath   string              `json:"steamcmd_path,omitempty"`
	LibrariesCount int                 `json:"libraries_count"`
	InstalledApps  int                 `json:"installed_apps"`
	Libraries      []libraryFolderInfo `json:"libraries,omitempty"`
	TotalSizeBytes int64               `json:"total_size_bytes"`
	TotalSizeHuman string              `json:"total_size_human"`
}

type aggregatedInfo struct {
	Server       *serverInfoData        `json:"server,omitempty"`
	CoreServices []status.EndpointStatus `json:"core_services,omitempty"`
	User         *playerSummary         `json:"user,omitempty"`
	ASF          *asfInfoData           `json:"asf,omitempty"`
	Client       *clientInfoData        `json:"client,omitempty"`
}

func infoCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "info",
		Short: "Aggregated generic system, server, user, client, and service information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			info := &aggregatedInfo{}
			var mu sync.Mutex
			var wg sync.WaitGroup

			s, err := o.settings()
			hasSettings := err == nil

			// 1. Steam Server Info (ISteamWebAPIUtil/GetServerInfo/v1/)
			wg.Add(1)
			go func() {
				defer wg.Done()
				baseURL := "https://api.steampowered.com"
				if hasSettings && s.WebURL != "" {
					baseURL = s.WebURL
				}
				endpoint, err := httpx.Endpoint(baseURL, "ISteamWebAPIUtil/GetServerInfo/v1/", o.allowHTTP)
				if err != nil {
					return
				}
				b, err := o.http().Do(ctx, http.MethodGet, endpoint, nil, nil, nil)
				if err != nil {
					return
				}
				var res struct {
					ServerTime       int64  `json:"servertime"`
					ServerTimeString string `json:"servertimestring"`
				}
				if json.Unmarshal(b, &res) == nil && res.ServerTime > 0 {
					utc := time.Unix(res.ServerTime, 0).UTC().Format("2006-01-02 15:04:05 UTC")
					mu.Lock()
					info.Server = &serverInfoData{
						ServerTime:       res.ServerTime,
						ServerTimeString: res.ServerTimeString,
						UTCTime:          utc,
					}
					mu.Unlock()
				}
			}()

			// 2. Core Services Status
			wg.Add(1)
			go func() {
				defer wg.Done()
				mon := &status.Monitor{
					HTTP:         o.http(),
					WebAPIURL:    s.WebURL,
					CommunityURL: s.CommunityURL,
				}
				eps, err := mon.CheckCoreServices(ctx)
				if err == nil {
					mu.Lock()
					info.CoreServices = eps
					mu.Unlock()
				}
			}()

			// 3. User info (default logged in user)
			wg.Add(1)
			go func() {
				defer wg.Done()
				userSteamID := ""
				if hasSettings {
					if id, err := s.SteamUserID(); err == nil && id != "" {
						userSteamID = id
					} else if cLogin, err := s.CommunityLoginSecure(); err == nil && cLogin != "" {
						userSteamID, _ = (&community.Client{LoginSecure: cLogin}).SteamID()
					}
				}
				if userSteamID == "" {
					userSteamID, _ = library.LoggedInUser(nil)
				}
				if userSteamID == "" {
					return
				}

				key := ""
				if hasSettings {
					key, _ = s.WebKey()
				}
				if key != "" {
					token, _ := s.AccessToken()
					wc := &webapi.Client{
						HTTP:        o.http(),
						BaseURL:     s.WebURL,
						Key:         key,
						CacheDir:    s.CacheDir,
						AccessToken: token,
					}
					b, err := wc.Call(ctx, "ISteamUser", "GetPlayerSummaries", 2, "GET", url.Values{"steamids": {userSteamID}})
					if err == nil {
						var res struct {
							Response struct {
								Players []playerSummary `json:"players"`
							} `json:"response"`
						}
						if json.Unmarshal(b, &res) == nil && len(res.Response.Players) > 0 {
							mu.Lock()
							info.User = &res.Response.Players[0]
							mu.Unlock()
							return
						}
					}
				}

				// Fallback if no Web API key or GetPlayerSummaries failed
				mu.Lock()
				info.User = &playerSummary{
					SteamID:    userSteamID,
					ProfileURL: "https://steamcommunity.com/profiles/" + userSteamID,
				}
				mu.Unlock()
			}()

			// 4. ASF Status
			wg.Add(1)
			go func() {
				defer wg.Done()
				if !hasSettings {
					return
				}
				pwd, _ := s.ASFPassword()
				asfClient := &asf.Client{
					HTTP:     o.http(),
					BaseURL:  s.ASFURL,
					Password: pwd,
				}
				b, err := asfClient.Call(ctx, "GET", "Api/ASF", nil, nil)
				if err != nil {
					return
				}
				var env struct {
					Result struct {
						Version      string `json:"Version"`
						ProcessID    int64  `json:"ProcessID"`
						MemoryUsage  int64  `json:"MemoryUsage"`
						StartedAt    string `json:"ProcessStartTime"`
						BotsCount    int    `json:"BotsCount"`
						BuildVariant string `json:"BuildVariant"`
					} `json:"Result"`
				}
				if json.Unmarshal(b, &env) == nil && env.Result.Version != "" {
					botCount := env.Result.BotsCount
					// If Api/ASF reports 0 bots, query Api/Bot/ASF to get the actual count of configured bots
					if botBytes, err := asfClient.Call(ctx, "GET", "Api/Bot/ASF", nil, nil); err == nil {
						if botList, ok := asf.Bots(botBytes); ok {
							botCount = len(botList)
						}
					}

					mu.Lock()
					info.ASF = &asfInfoData{
						Version:      env.Result.Version,
						BuildVariant: env.Result.BuildVariant,
						ProcessID:    env.Result.ProcessID,
						MemoryUsage:  env.Result.MemoryUsage,
						StartedAt:    env.Result.StartedAt,
						BotsCount:    botCount,
					}
					mu.Unlock()
				}
			}()

			// 5. Client & Local Environment
			wg.Add(1)
			go func() {
				defer wg.Done()
				clientData := &clientInfoData{
					Platform: runtime.GOOS + "/" + runtime.GOARCH,
					Profile:  "default",
				}
				if hasSettings && s.ProfileName != "" {
					clientData.Profile = s.ProfileName
				}

				// Steam client executable
				loc := steamclient.Locator{}
				if hasSettings {
					loc.Path = s.SteamClientPath
				}
				if exe, err := loc.Find(); err == nil {
					clientData.SteamPath = exe
				}

				// SteamCMD executable
				var dataDir, cmdPath string
				if hasSettings {
					dataDir = s.DataDir
					cmdPath = s.SteamCMDPath
				}
				m := &steamcmd.Manager{DataDir: dataDir, Path: cmdPath}
				if p, err := m.Find(); err == nil {
					clientData.SteamCMDPath = p
				}

				// Local library scan
				rep, err := library.Scan(library.Defaults())
				if err == nil {
					clientData.LibrariesCount = len(rep.Libraries)
					clientData.InstalledApps = len(rep.Apps)

					libApps := make(map[string]int)
					libSize := make(map[string]int64)
					var totalBytes int64
					for _, app := range rep.Apps {
						libApps[app.Library]++
						if n, err := strconv.ParseInt(app.SizeOnDisk, 10, 64); err == nil && n > 0 {
							libSize[app.Library] += n
							totalBytes += n
						}
					}

					var libFolders []libraryFolderInfo
					for _, lib := range rep.Libraries {
						sz := libSize[lib]
						libFolders = append(libFolders, libraryFolderInfo{
							Path:       lib,
							AppsCount:  libApps[lib],
							SizeBytes:  sz,
							SizeHuman:  humanBytes(sz),
						})
					}
					clientData.Libraries = libFolders
					clientData.TotalSizeBytes = totalBytes
					clientData.TotalSizeHuman = humanBytes(totalBytes)
				}

				mu.Lock()
				info.Client = clientData
				mu.Unlock()
			}()

			wg.Wait()

			return o.emit(cmd, info, func(w io.Writer) {
				// 1. Steam Server Info
				if info.Server != nil {
					o.heading(w, "Steam Server Info")
					t := o.newDetail(w)
					detailRows(t,
						kv("Server time", info.Server.ServerTimeString),
						kv("Unix timestamp", fmt.Sprint(info.Server.ServerTime)),
						kv("UTC time", info.Server.UTCTime),
					)
					o.renderTable(t)
				}

				// 2. Core Services Status
				if len(info.CoreServices) > 0 {
					o.heading(w, "Core Services")
					t := o.newTable(w)
					t.AppendHeader(table.Row{"Service", "State", "Latency", "HTTP"})
					t.SetColumnConfigs([]table.ColumnConfig{{Number: 3, Align: text.AlignRight}})
					for _, ep := range info.CoreServices {
						if ep.Error != "" {
							t.AppendRow(table.Row{ep.Name, colorStatus(ep.Status), "", ep.Error})
							continue
						}
						t.AppendRow(table.Row{ep.Name, colorStatus(ep.Status),
							fmt.Sprintf("%d ms", ep.LatencyMS), ep.HTTPCode})
					}
					o.renderTable(t)
				}

				// 3. User / Account
				if info.User != nil {
					o.heading(w, "Logged-in User")
					t := o.newDetail(w)
					rows := []([2]string){
						kv("Persona", info.User.Persona),
						kv("Real name", info.User.RealName),
						kv("SteamID64", info.User.SteamID),
					}
					if conv, err := convertID(info.User.SteamID); err == nil {
						rows = append(rows, kv("SteamID3", conv["steamid3"]), kv("SteamID2", conv["steamid2"]))
					}
					if info.User.State != nil || info.User.GameName != "" {
						rows = append(rows, kv("Status", colorPersona(*info.User)))
					}
					if info.User.GameName != "" {
						rows = append(rows, kv("Game", info.User.GameName+gameSuffix(*info.User)))
					}
					if info.User.Visibility != nil {
						rows = append(rows, kv("Visibility", colorVisibility(info.User.visibility())))
					}
					loc := locality(*info.User)
					if loc != "" {
						rows = append(rows, kv("Country", loc))
					}
					rows = append(rows, kv("Profile URL", info.User.ProfileURL))
					detailRows(t, rows...)
					o.renderTable(t)
				}

				// 4. Client & Environment
				if info.Client != nil {
					o.heading(w, "Client & Environment")
					t := o.newDetail(w)
					steamPath := info.Client.SteamPath
					if steamPath == "" {
						steamPath = red.Sprint("not found")
					}
					cmdPath := info.Client.SteamCMDPath
					if cmdPath == "" {
						cmdPath = faint("not installed")
					}
					detailRows(t,
						kv("Platform", info.Client.Platform),
						kv("Profile", info.Client.Profile),
						kv("Steam client", steamPath),
						kv("SteamCMD", cmdPath),
					)
					o.renderTable(t)

					if len(info.Client.Libraries) > 0 {
						o.heading(w, "Library Folders")
						lt := o.newTable(w)
						lt.AppendHeader(table.Row{"Library Path", "Apps", "Size"})
						lt.SetColumnConfigs([]table.ColumnConfig{
							{Number: 2, Align: text.AlignRight},
							{Number: 3, Align: text.AlignRight},
						})
						for _, lib := range info.Client.Libraries {
							lt.AppendRow(table.Row{lib.Path, lib.AppsCount, lib.SizeHuman})
						}
						lt.AppendFooter(table.Row{"Total", info.Client.InstalledApps, info.Client.TotalSizeHuman})
						o.renderTable(lt)
					}
				}

				// 5. ArchiSteamFarm Status (if available)
				if info.ASF != nil {
					o.heading(w, "ArchiSteamFarm (ASF)")
					t := o.newDetail(w)
					memMB := float64(info.ASF.MemoryUsage) / 1024.0
					rows := []([2]string){
						kv("ASF version", info.ASF.Version),
						kv("Build variant", info.ASF.BuildVariant),
					}
					if info.ASF.ProcessID > 0 {
						rows = append(rows, kv("Process ID", fmt.Sprint(info.ASF.ProcessID)))
					}
					rows = append(rows,
						kv("Memory usage", fmt.Sprintf("%.1f MiB", memMB)),
						kv("Started at", info.ASF.StartedAt),
						kv("Bots configured", fmt.Sprint(info.ASF.BotsCount)),
					)
					detailRows(t, rows...)
					o.renderTable(t)
				}
			})
		},
	}

	return cmd
}
