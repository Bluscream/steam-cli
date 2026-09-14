# steam-cli

A private, cross-platform Go CLI for Steam Web API, Valve SteamCMD, ArchiSteamFarm IPC, and local Steam metadata. Builds to a single executable. No telemetry, hosted middleware, browser automation, Python, Node, or .NET runtime is required by the CLI.

```text
steam status    Steam service health, player counts, CMs, and game coordinators
steam web       API discovery, raw calls, and common player/game queries
steam workshop  subscriptions, favorites, search, and collection management
steam cmd       automatic SteamCMD bootstrap, execution, app/workshop downloads
steam asf       IPC calls, bot controls, commands, OpenAPI, two-factor tokens
steam library   local library and installed-app inspection
steam id        offline SteamID64 / Steam2 / Steam3 conversion
steam config    non-secret profiles
steam doctor    local configuration and runtime diagnostics
steam completion bash | zsh | fish | powershell
```

The Web API and ASF client are native Go. SteamCMD is downloaded from Valve when needed, and an ASF instance must already be running. Valve's platform dependencies and ASF's configuration still apply. This project remains local/private; nothing has been published.

## Build and run

Use Go **1.26 or newer**; the module selects the tested **1.27.1** toolchain. Dependencies and their licenses are included in `vendor/`.

```sh
go build -mod=vendor -trimpath -o bin/steam ./cmd/steam
./bin/steam --help
./bin/steam doctor
./bin/steam web --no-key server-info
```

On Windows, use `-o bin/steam.exe` and `./bin/steam.exe`. The desktop Steam client also uses the name `steam`; keep this executable under its own directory, invoke it explicitly, or name it `steam-cli`. Do not replace your desktop client's executable.

For a completely offline build with an **already installed Go >= 1.26 toolchain**:

```sh
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off CGO_ENABLED=0 go build -mod=vendor -trimpath -o bin/steam ./cmd/steam
```

Otherwise the Go launcher may download the selected toolchain once. `scripts/build.sh` or `scripts/build.ps1` builds six standalone artifacts in `dist/`: Linux, macOS, and Windows, each for amd64 and arm64. The native HTTP/offline functionality supports all six; SteamCMD has narrower platform support below.

## Credentials and profiles

The CLI reads existing environment variables. It does not load `.env` files automatically or persist credentials.

| Variable | Purpose |
| --- | --- |
| `STEAM_WEB_API_KEY` | Preferred Steam Web API key |
| `STEAM_API_KEY` | Compatibility fallback when the preferred variable is unset |
| `STEAM_ACCESS_TOKEN` | Web API access token for methods a key cannot authorize |
| `STEAM_LOGIN_SECURE` | Community session cookie; required for collection membership, subscriptions, and favorites |
| `ASF_IPC_PASSWORD` | ASF authentication header |
| `STEAM_WEB_URL` | Web API base URL override |
| `STEAM_COMMUNITY_URL` | Community base URL override |
| `STEAM_ASF_URL` | ASF base URL override |
| `STEAMCMD_PATH` | Existing executable or compatibility wrapper |
| `STEAM_CLI_DATA_DIR` | Persistent SteamCMD installation directory |
| `STEAM_CLI_CACHE_DIR` | API discovery cache directory |

Use your shell's secret handling or password manager to populate credentials. A protected file can also be referenced by `web_key_file`, `access_token_file`, `community_login_secure_file`, or `asf_password_file` in a profile. Environment values take precedence over files; an explicitly empty variable suppresses its fallback. Custom credential environment variable names are supported per profile.

```sh
./bin/steam config init
./bin/steam config show
./bin/steam --config ./config.example.json --profile partner web methods
```

`config init` refuses to overwrite a file and creates it with Unix mode 0600. `config show` never loads credential contents. Configuration is under the OS user configuration directory (`steam-cli/config.json`). On Linux, persistent data uses `$XDG_DATA_HOME/steam-cli` or `~/.local/share/steam-cli`; caches use `$XDG_CACHE_HOME/steam-cli` or `~/.cache/steam-cli`. macOS/Windows use Go's OS-native configuration/cache locations. `doctor` reports the actual paths. On Windows, file privacy follows the containing directory's ACLs.

See [config.example.json](config.example.json) for default and partner profiles. Profiles select API hosts, credential environment/file references, and an optional `steamcmd_path`; the data directory is shared unless overridden.

## Steam Web API

```sh
./bin/steam web methods GetOwnedGames
./bin/steam --offline web methods IPlayerService
./bin/steam web methods --refresh

./bin/steam web player 76561197960287930
./bin/steam web owned 76561197960287930
./bin/steam web recent 76561197960287930
./bin/steam web friends 76561197960287930
./bin/steam web bans 76561197960287930
./bin/steam web resolve example-vanity-name
./bin/steam web achievements 76561197960287930 730
./bin/steam web news 730
./bin/steam web players 730

./bin/steam web call ISteamUser GetPlayerSummaries steamids=76561197960287930
./bin/steam web call IPlayerService GetOwnedGames --input-json '{"steamid":"76561197960287930","include_appinfo":true}'
./bin/steam web call IPlayerService GetOwnedGames --input-json @request.json
```

`web call` discovers the HTTP verb and highest available version from `GetSupportedAPIList`. Catalogs are cached for 24 hours, separately for each API host/key fingerprint. `--offline web methods` uses that cache even when stale; no private player responses are cached. Some methods require a key or publisher permissions and some are not advertised at all.

For an undiscovered endpoint or to avoid the discovery request, specify the verb and version:

```sh
./bin/steam web call ISteamWebAPIUtil GetServerInfo --method GET --api-version 1
./bin/steam web call INTERFACE METHOD --method POST --api-version 1 -p 'name=value'
```

POST calls use form encoding in the body. `--param` / `-p` can be repeated and supports names such as `appids[0]`. `--input-json` accepts literal JSON, `@file`, or `-` for stdin; Steam service APIs receive it as an `input_json` form/query field. No SteamID-sized integer is converted through floating point. Arbitrary parameters are passed through; the CLI does not claim to validate every endpoint's schema or permissions.

## Service status

```sh
./bin/steam --output raw status
./bin/steam status --no-cm --no-coordinator
./bin/steam --output raw status --app 730 --app 570
./bin/steam status --cm-limit 20
```

Reports what [steamstat.us](https://steamstat.us/) reports, from the same public sources: reachability and latency for the Store, Community, Web API and Help hosts; live player counts for eight major titles (`--app` replaces that list); the CS2 game coordinator's service states, matchmaking queues and per-region datacenter capacity; and TCP handshake latency against connection managers drawn from `ISteamDirectory/GetCMList`.

A 3xx or 4xx answer is reported as `normal`, because it still proves the host is serving traffic; only 5xx and transport failures are `down`. Above 1500 ms an endpoint is `slow`. Probe failures are recorded per item rather than failing the run, so one unreachable service does not hide the rest. Coordinator status needs a Web API key; without one the report carries a warning instead of silently omitting it. Only CS2 exposes this interface, so it is the only coordinator queried.

## Workshop

```sh
./bin/steam workshop search 4000 "map" --count 10
./bin/steam workshop search-collections 4000 "weapons" --all
./bin/steam workshop collection 3052582377
./bin/steam --offline workshop installed 107410
./bin/steam workshop subs 4000
./bin/steam workshop favorites 4000
./bin/steam workshop sub 4000 --from-collection 3052582377
./bin/steam workshop create-collection 4000 --title "My Picks" --from-favorites
./bin/steam workshop add-items 4000 COLLECTION_ID ITEMID...
./bin/steam workshop delete-collection 4000 COLLECTION_ID --yes
```

Searches page with Steam's cursor rather than the `page` parameter, which is capped server-side; `--all` walks every page.

Batch operations report per-item outcomes as `{succeeded, failed, results}`. Steam answers HTTP 200 even when it refuses a write and reports the real outcome in `x-eresult`, so each item's result reflects that code, not the HTTP status. A batch exits nonzero only when every item failed.

`installed` reads `appworkshop_<appid>.acf` manifests from local libraries and works `--offline`. It is disk state: an item subscribed but not downloaded is absent, and an item left behind after unsubscribing is present. `subs` and `favorites` report what Steam records for the account.

### What requires a Community session

The Steam Web API has no method that sets a collection's children, and none that lists your own subscriptions or favorites. `IPublishedFileService/Delete` is publisher-only and rejects ordinary user keys. Those operations go through steamcommunity.com using the `steamLoginSecure` cookie from a browser session, exactly as the Workshop web UI does:

| Command | Needs `STEAM_LOGIN_SECURE` |
| --- | --- |
| `search`, `search-collections`, `collection`, `installed` | no |
| `sub`, `unsub`, `edit-collection` | no (Web API key) |
| `create-collection` without items | no |
| `create-collection` with items, `add-items`, `remove-items` | yes |
| `subs`, `favorites` | yes |
| `delete-collection` | preferred; falls back to the publisher-only Web API method |

Commands that need the session say so and name the variable rather than reporting a success that changed nothing. Subscription and favorite lists are read from the account's own Workshop listing, which Steam serves only as HTML; an item Steam declines to render will not appear.

## SteamCMD

```sh
./bin/steam cmd install
./bin/steam cmd path
./bin/steam cmd update
./bin/steam cmd run
./bin/steam cmd run -- +login anonymous +app_info_print 730 +quit

./bin/steam cmd download 1007 --dir ./steamworks-redist --validate --dry-run
./bin/steam cmd download 1007 --dir ./steamworks-redist --validate
./bin/steam cmd download APPID --dir ./game --user ACCOUNT --platform windows --beta BRANCH
./bin/steam cmd workshop APPID ITEMID --user ACCOUNT --validate
```

App 1007 (Steamworks SDK Redistributables) was used for the live download test. Use the current app ID for the server/game you actually need.

Downloads default to anonymous login; account-owned content generally needs `--user`. Valve handles interactive passwords and Steam Guard in your terminal. The Web API key is **not** a Steam login credential. Account passwords are not collected or stored by this wrapper. Use `cmd run -- +runscript /absolute/path/script.txt` for your own batch scripts. Raw passthrough deliberately preserves Valve's behavior and output, including any sensitive values you put in arguments/scripts.

SteamCMD resolution: explicit path, managed installation, then `PATH`. Otherwise it auto-downloads Valve's bootstrap. `--no-download` disables this; `cmd install --sha256 HEX` optionally pins the bytes of a **new** download. `bootstrap.json` records the source, SHA-256, and time. Valve's bootstrap URL is mutable, and its subsequent self-updates are controlled by Valve; the recorded hash is provenance, not an independent vendor signature.

The installer stages extraction, rejects path traversal/escaping links/special files, limits archive expansion, and installs by directory rename. macOS framework symlinks are created only after file extraction and must resolve inside the staging directory. A cross-process lock serializes this CLI's SteamCMD installation/execution in each data directory. Locks cannot coordinate with unrelated SteamCMD processes launched outside this CLI.

The download helper sets the installation directory **before** login. It requires a success marker from the current run; app downloads also require a matching manifest marked fully installed in the requested directory. Workshop downloads require the item's success marker. Raw `cmd run` returns Valve's process exit status without interpreting game-specific output.

`--run-timeout 10m` sets a subprocess deadline (default unlimited); `--timeout` controls HTTP requests only. Unix batch cancellation stops the launcher process group. Interactive children retain the foreground terminal group for prompts; on Windows cancellation targets the direct process. `--offline` disables SteamCMD execution because the wrapper cannot enforce offline behavior inside Valve's client; `cmd path` and `--dry-run` remain usable.

| Platform | Native CLI | SteamCMD |
| --- | --- | --- |
| Linux amd64 | Yes | Valve x86 bootstrap; 32-bit glibc loader and libstdc++ required |
| Linux arm64 | Yes | No native bootstrap; explicit compatibility wrapper required |
| Windows amd64 | Yes | Valve Windows bootstrap |
| Windows arm64 | Yes | Explicit configured x86 compatibility wrapper required |
| macOS amd64 | Yes | Valve Intel bootstrap in a `MacOS/` directory |
| macOS arm64 | Yes | Intel bootstrap; requires Rosetta 2 |

No root/package-manager operations are performed. `doctor` checks for the usual Linux loader path. SteamCMD may write its own Steam logs/account cache outside the managed installation, as observed during testing; its internal storage is not sandboxed by this CLI.

## ArchiSteamFarm

With `STEAM_ASF_URL` and `ASF_IPC_PASSWORD` configured:

```sh
./bin/steam asf status
./bin/steam asf bots
./bin/steam asf bots MyBot
./bin/steam asf schema
./bin/steam asf command status ASF
./bin/steam asf start MyBot
./bin/steam asf pause MyBot --resume-in 600
./bin/steam asf resume MyBot
./bin/steam asf stop MyBot
./bin/steam asf token MyBot

./bin/steam asf call GET Api/Bot/MyBot
./bin/steam asf call POST Api/Bot/MyBot/Pause --data '{"Permanent":false,"ResumeInSeconds":600}'
./bin/steam asf call POST Api/Command --data @command.json
```

`asf command`, bot controls, and arbitrary write calls can change account state; they execute exactly when requested. The CLI does not retry write requests. Token output is sensitive and goes to stdout only when requested.

Authentication uses the `Authentication` header, never a password query string added by the CLI. Reverse-proxy URL prefixes are preserved. Plain HTTP is permitted on loopback; add `--allow-http` for an explicitly trusted LAN endpoint. Both user-supplied LAN ASF instances were tested with this option and header authentication.

`asf schema` retrieves `/swagger/ASF/swagger.json` from your instance, covering its version and plugins. Generic `asf call` reaches endpoints without requiring a CLI release. A response with `Success:false` prints its JSON and exits nonzero. The Web API and ASF are separate authentication domains.

## Local metadata and output

```sh
./bin/steam --offline library
./bin/steam --offline library --root /path/to/Steam
./bin/steam --offline id 'STEAM_0:0:11101'
./bin/steam --offline id '[U:1:22202]'
./bin/steam --output compact web players 730
./bin/steam --output raw web call ISteamWebAPIUtil GetServerInfo --method GET -p format=xml
./bin/steam completion bash > steam-completion.bash
```

Library discovery checks common Windows/macOS/Linux locations and Linux Flatpak. `--root` handles nonstandard/custom installations. Both legacy and modern `libraryfolders.vdf` layouts are supported. Malformed manifests produce warnings in the JSON report; their contents are never executed. Library results describe local manifest state, not proof of an account license or cloud availability.

JSON is indented by default, `compact` produces compact JSON, and `raw` preserves response bytes. HTTP errors omit response bodies/credential-bearing URLs. There are no hidden browser sessions, analytics, cookie jars, response logs, or background update checks. Normal HTTP proxy environment settings are honored by Go. Read-only HTTP retries are limited to two for 429/502/503/504, honor bounded `Retry-After`, and never retry authentication failures. Redirects are not followed, preventing credentials from being forwarded.

Exit codes: `0` success, `1` CLI/HTTP/application/verification failure, `130` interrupted; SteamCMD's positive nonzero process exit codes pass through. SteamCMD output remains its native terminal output regardless of `--output`.

## Development and verification

```sh
go test -mod=vendor -race -cover ./...
go vet -mod=vendor ./...
./scripts/build.sh
```

The tests run locally without Steam credentials or external API access. A native OS CI matrix is included but has not been run on hosted runners. See [docs/VALIDATION.md](docs/VALIDATION.md) for actual live checks and limitations, [docs/RESEARCH.md](docs/RESEARCH.md) for design decisions and upstream findings, and [docs/THIRD_PARTY.md](docs/THIRD_PARTY.md) for dependency provenance.
