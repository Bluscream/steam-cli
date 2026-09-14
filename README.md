# steam-cli

[![test](https://github.com/Bluscream/steam-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/Bluscream/steam-cli/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/Bluscream/steam-cli?sort=semver)](https://github.com/Bluscream/steam-cli/releases)
[![license](https://img.shields.io/badge/license-Unlicense-blue)](LICENSE)

A cross-platform Go CLI for Steam Web API, Valve SteamCMD, ArchiSteamFarm IPC, and local Steam metadata. Builds to a single executable. No telemetry, hosted middleware, browser automation, Python, Node, or .NET runtime is required by the CLI.

```text
steamcli status     Steam service health, player counts, CMs, game coordinators
steamcli web        API discovery, raw calls, and common player/game queries
steamcli workshop   subscriptions, favorites, search, and collection management
steamcli client     drive the desktop Steam client (run, install, steam:// URLs)
steamcli cmd        automatic SteamCMD bootstrap, execution, app/workshop downloads
steamcli asf        IPC calls, bot controls, commands, OpenAPI, two-factor tokens
steamcli library    local library and installed-app inspection
steamcli id         offline SteamID64 / Steam2 / Steam3 conversion
steamcli config     non-secret profiles
steamcli doctor     local configuration and runtime diagnostics
steamcli completion bash | zsh | fish | powershell
```

The Web API and ASF client are native Go. SteamCMD is downloaded from Valve when needed, and an ASF instance must already be running. Valve's platform dependencies and ASF's configuration still apply. Not affiliated with, endorsed by, or sponsored by Valve Corporation.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/Bluscream/steam-cli/main/scripts/install.sh | sh
```

Detects your platform, verifies the download against the release `SHA256SUMS`, and installs to `~/.local/bin`. Set `STEAMCLI_PREFIX` to install elsewhere or `STEAMCLI_VERSION=v0.6.0` to pin a release. Read the script before piping it to a shell, as you should with any such installer.

| Method | How |
| --- | --- |
| AppImage | download `steamcli-*-x86_64.AppImage` from [releases](https://github.com/Bluscream/steam-cli/releases), `chmod +x`, run |
| Arch Linux | `cd packaging && makepkg -si` |
| Prebuilt binary | `steamcli-{linux,darwin,windows}-{amd64,arm64}` from [releases](https://github.com/Bluscream/steam-cli/releases) |
| From source | see below |

## Build and run

Use Go **1.26 or newer**; the module selects the tested **1.27.1** toolchain. Dependencies and their licenses are included in `vendor/`.

```sh
go build -mod=vendor -trimpath -o bin/steamcli ./cmd/steamclicli
./bin/steamcli --help
./bin/steamcli doctor
./bin/steamcli web --no-key server-info
```

On Windows, use `-o bin/steamcli.exe` and `./bin/steamcli.exe`. The executable is named `steamcli`, matching Valve's own `steamcmd`, so it never collides with the desktop client's `steam`. Do not rename it to `steam`: `steamcli client` forwards to the desktop client and refuses any candidate that resolves back to this CLI, but shadowing `steam` on PATH would still break every other tool that expects Valve's launcher.

For a completely offline build with an **already installed Go >= 1.26 toolchain**:

```sh
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off CGO_ENABLED=0 go build -mod=vendor -trimpath -o bin/steamcli ./cmd/steamclicli
```

Otherwise the Go launcher may download the selected toolchain once. `scripts/build.sh` or `scripts/build.ps1` builds six standalone artifacts in `dist/`: Linux, macOS, and Windows, each for amd64 and arm64. The native HTTP/offline functionality supports all six; SteamCMD has narrower platform support below.

## Credentials and profiles

The CLI reads existing environment variables. It does not load `.env` files automatically or persist credentials.

| Variable | Purpose |
| --- | --- |
| `STEAM_API_KEY` | Steam Web API key |
| `STEAM_USER_ID` | Default user SteamID64 for profile/player commands |
| `STEAM_ACCESS_TOKEN` | Web API access token for methods a key cannot authorize |
| `STEAM_LOGIN_SECURE` | Community session cookie; required for collection membership, subscriptions, and favorites |
| `ASF_IPC_PASSWORD` | ASF authentication header |
| `STEAM_WEB_URL` | Web API base URL override |
| `STEAM_COMMUNITY_URL` | Community base URL override |
| `STEAM_ASF_URL` | ASF base URL override |
| `STEAMCMD_PATH` | Existing executable or compatibility wrapper |
| `STEAM_CLIENT_PATH` | Desktop Steam client launcher used by `steamcli client` |
| `STEAM_CLIENT_ARGS` | Whitespace-separated default arguments for that launcher |
| `STEAM_CLI_DATA_DIR` | Persistent SteamCMD installation directory |
| `STEAM_CLI_CACHE_DIR` | API discovery cache directory |

Use your shell's secret handling or password manager to populate credentials. A protected file can also be referenced by `web_key_file`, `access_token_file`, `community_login_secure_file`, or `asf_password_file` in a profile. Environment values take precedence over files; an explicitly empty variable suppresses its fallback. Custom credential environment variable names are supported per profile.

```sh
./bin/steamcli config init
./bin/steamcli config show
./bin/steamcli --config ./config.example.json --profile partner web methods
```

`config init` refuses to overwrite a file and creates it with Unix mode 0600. `config show` never loads credential contents. Configuration is under the OS user configuration directory (`steam-cli/config.json`). On Linux, persistent data uses `$XDG_DATA_HOME/steam-cli` or `~/.local/share/steam-cli`; caches use `$XDG_CACHE_HOME/steam-cli` or `~/.cache/steam-cli`. macOS/Windows use Go's OS-native configuration/cache locations. `doctor` reports the actual paths. On Windows, file privacy follows the containing directory's ACLs.

Every key a profile accepts:

| Key | Purpose |
| --- | --- |
| `web_url` | Steam Web API base URL (use `https://partner.steam-api.com` for the partner API) |
| `web_key_env` / `web_key_file` | Where to read the Web API key |
| `access_token_env` / `access_token_file` | Where to read a Web API access token |
| `community_url` | Steam Community base URL |
| `community_login_secure_env` / `community_login_secure_file` | Where to read the `steamLoginSecure` session cookie |
| `asf_url` | ArchiSteamFarm IPC base URL, including any reverse-proxy prefix |
| `asf_password_env` / `asf_password_file` | Where to read the ASF IPC password |
| `allow_http` | Permit plaintext HTTP outside loopback for this profile's hosts |
| `steamcmd_path` | Existing SteamCMD executable or compatibility wrapper |
| `steam_client_path` | Desktop Steam client launcher |
| `steam_client_args` | Arguments prepended to every `steamcli client` launch |

Only `*_env` / `*_file` keys appear here; credential values never do. See [config.example.json](config.example.json) for default, LAN and partner profiles. Profiles select API hosts, credential environment/file references, and an optional `steamcmd_path`; the data directory is shared unless overridden.

## Steam Web API

```sh
./bin/steamcli web methods GetOwnedGames
./bin/steamcli --offline web methods IPlayerService
./bin/steamcli web methods --refresh

./bin/steamcli search vrchat                         # global search across store, local, owned, workshop, players
./bin/steamcli apps vrchat                           # search store apps by name
./bin/steamcli web news vrchat                       # resolves app name to AppID
./bin/steamcli web players cs2                       # resolves app name to AppID
./bin/steamcli web achievements vrchat               # defaults to logged-in user, resolves app name

./bin/steamcli web player 76561197960287930          # aliases: profile, profiles
./bin/steamcli web profile                           # defaults to logged-in user
./bin/steamcli web profile 76561197960287930,76561197960287931
./bin/steamcli web owned                             # defaults to logged-in user
./bin/steamcli web recent                            # defaults to logged-in user
./bin/steamcli web friends                           # defaults to logged-in user
./bin/steamcli web bans                              # defaults to logged-in user
./bin/steamcli web resolve example-vanity-name
./bin/steamcli web server-info
./bin/steamcli web achievements 76561197960287930 730
./bin/steamcli web news 730
./bin/steamcli web players 730

./bin/steamcli web call ISteamUser GetPlayerSummaries steamids=76561197960287930
./bin/steamcli web call IPlayerService GetOwnedGames --input-json '{"steamid":"76561197960287930","include_appinfo":true}'
./bin/steamcli web call IPlayerService GetOwnedGames --input-json @request.json
```

`web player`, aliased `profile`, renders a profile: persona, all three SteamID forms, online status or the game being played, community visibility, country, account creation, primary group and profile URL. Several IDs at once render as a table sorted by persona. `-o json` returns Valve's payload unchanged.

`web call` discovers the HTTP verb and highest available version from `GetSupportedAPIList`. Catalogs are cached for 24 hours, separately for each API host/key fingerprint. `--offline web methods` uses that cache even when stale; no private player responses are cached. Some methods require a key or publisher permissions and some are not advertised at all.

For an undiscovered endpoint or to avoid the discovery request, specify the verb and version:

```sh
./bin/steamcli web call ISteamWebAPIUtil GetServerInfo --method GET --api-version 1
./bin/steamcli web call INTERFACE METHOD --method POST --api-version 1 -p 'name=value'
```

POST calls use form encoding in the body. `--param` / `-p` can be repeated and supports names such as `appids[0]`. `--input-json` accepts literal JSON, `@file`, or `-` for stdin; Steam service APIs receive it as an `input_json` form/query field. No SteamID-sized integer is converted through floating point. Arbitrary parameters are passed through; the CLI does not claim to validate every endpoint's schema or permissions.

## Service status

```sh
./bin/steamcli --output raw status
./bin/steamcli status --no-cm --no-coordinator
./bin/steamcli --output raw status --app 730 --app 570
./bin/steamcli status --cm-limit 20
```

Reports what [steamstat.us](https://steamstat.us/) reports, from the same public sources: reachability and latency for the Store, Community, Web API and Help hosts; live player counts for eight major titles (`--app` replaces that list); the CS2 game coordinator's service states, matchmaking queues and per-region datacenter capacity; and TCP handshake latency against connection managers drawn from `ISteamDirectory/GetCMList`.

A 3xx or 4xx answer is reported as `normal`, because it still proves the host is serving traffic; only 5xx and transport failures are `down`. Above 1500 ms an endpoint is `slow`. Probe failures are recorded per item rather than failing the run, so one unreachable service does not hide the rest. Coordinator status needs a Web API key; without one the report carries a warning instead of silently omitting it. Only CS2 exposes this interface, so it is the only coordinator queried.

## Workshop

```sh
./bin/steamcli workshop search 4000 "map" --count 10
./bin/steamcli workshop search-collections 4000 "weapons" --all
./bin/steamcli workshop collection 3052582377
./bin/steamcli --offline workshop installed 107410
./bin/steamcli workshop subs 4000
./bin/steamcli workshop favorites 4000
./bin/steamcli workshop sub 4000 --from-collection 3052582377
./bin/steamcli workshop unsub 4000 --all
./bin/steamcli workshop edit-collection 4000 COLLECTION_ID --title "New title"
./bin/steamcli workshop remove-items 4000 COLLECTION_ID ITEMID...
./bin/steamcli workshop create-collection 4000 --title "My Picks" --from-favorites
./bin/steamcli workshop add-items 4000 COLLECTION_ID ITEMID...
./bin/steamcli workshop delete-collection 4000 COLLECTION_ID --yes
```

Every `workshop` subcommand is also reachable as `steamcli web workshop ...`, so the whole Web API surface stays under one command tree.

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

## Desktop Steam client

```sh
./bin/steamcli client path
./bin/steamcli client run 730
./bin/steamcli client install 220
./bin/steamcli client validate 730
./bin/steamcli client uninstall 220
./bin/steamcli client store 730
./bin/steamcli client open steam://open/console
./bin/steamcli client launch -- -silent
./bin/steamcli client shutdown
```

`steam_client_args` in a profile, or `STEAM_CLIENT_ARGS`, is prepended to every launch, for options you always want:

```json
"default": { "steam_client_args": ["-console"] }
```

Defaults come first, so `steamcli client run 730` becomes `steam -console steam://run/730`. `--steam-arg` adds to them for one run, `--no-default-args` skips them, and `steamcli client path` prints both the resolved executable and the defaults in effect. The environment variable replaces the profile list rather than extending it.

Prefer Steam's `-console` flag over a second `steam://` URL. `-console` adds the CONSOLE tab to the window Steam opens, whereas passing `steam://open/console` alongside another `steam://` URL leaves the client to decide which one wins.

`client` forwards to Valve's own launcher, which owns login, the overlay, and `steam://` handling; this CLI only locates the executable and hands over the arguments. Discovery checks `--steam-path`, then `STEAM_CLIENT_PATH` or `steam_client_path`, then `steam` on PATH, then the usual per-platform install locations including Flatpak exports. The client's exit status is propagated.

A candidate that resolves to this executable is refused, so naming this CLI `steam` cannot make `client` re-invoke itself; that is also why the binary is `steamcli`. AppID verbs validate the ID as an integer and build the `steam://` URL themselves, and `client open` accepts only `steam://` URLs, so neither can be used to launch an arbitrary protocol handler.

## SteamCMD

```sh
./bin/steamcli cmd install
./bin/steamcli cmd path
./bin/steamcli cmd update
./bin/steamcli cmd run
./bin/steamcli cmd run -- +login anonymous +app_info_print 730 +quit

./bin/steamcli cmd download 1007 --dir ./steamworks-redist --validate --dry-run
./bin/steamcli cmd download 1007 --dir ./steamworks-redist --validate
./bin/steamcli cmd download APPID --dir ./game --user ACCOUNT --platform windows --beta BRANCH
./bin/steamcli cmd workshop APPID ITEMID --user ACCOUNT --validate
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
./bin/steamcli asf status
./bin/steamcli asf bots --bots Alpha,Beta
./bin/steamcli asf token gabeN --output parsed   # aliases: 2fa, auth
./bin/steamcli asf token --bots Alpha,Beta --output parsed
./bin/steamcli asf pause --bots Alpha --resume-in 600
./bin/steamcli asf bots
./bin/steamcli asf bots MyBot
./bin/steamcli asf schema
./bin/steamcli asf command status ASF
./bin/steamcli asf start MyBot
./bin/steamcli asf pause MyBot --resume-in 600
./bin/steamcli asf resume MyBot
./bin/steamcli asf stop MyBot
./bin/steamcli asf token MyBot

./bin/steamcli asf call GET Api/Bot/MyBot
./bin/steamcli asf call POST Api/Bot/MyBot/Pause --data '{"Permanent":false,"ResumeInSeconds":600}'
./bin/steamcli asf call POST Api/Command --data @command.json
```

`asf command`, bot controls, and arbitrary write calls can change account state; they execute exactly when requested. The CLI does not retry write requests. Token output is sensitive and goes to stdout only when requested.

Authentication uses the `Authentication` header, never a password query string added by the CLI. Reverse-proxy URL prefixes are preserved. Plain HTTP is permitted on loopback; add `--allow-http` for an explicitly trusted LAN endpoint, or set `"allow_http": true` in the profile to avoid repeating the flag for a LAN instance. The flag can enable plaintext for a single run but never disables what the profile allows. Exposing ASF over HTTPS instead — a reverse proxy, or Tailscale `serve`, which issues a real certificate for a `*.ts.net` name — avoids the question entirely and needs no flag.

A credential containing control characters cannot be sent as a header. Rather than surfacing Go's transport error, which reads like a network failure, the CLI names the header and suggests checking the value for stray whitespace, newlines, or terminal escape sequences. The credential itself is never echoed. Both user-supplied LAN ASF instances were tested with this option and header authentication.

Commands that act on bots take a selector: a positional argument, the persistent `--bots`/`-b` flag, or neither, in which case `ASF` is used and ArchiSteamFarm reads that as every bot. A positional argument wins over the flag. Selectors are comma-separated bot names.

`asf schema` retrieves `/swagger/ASF/swagger.json` from your instance, covering its version and plugins. Generic `asf call` reaches endpoints without requiring a CLI release. A response with `Success:false` prints its JSON and exits nonzero. The Web API and ASF are separate authentication domains.

## Local metadata and output

```sh
./bin/steamcli --offline library
./bin/steamcli --offline library --root /path/to/Steam
./bin/steamcli --offline id 'STEAM_0:0:11101'
./bin/steamcli --offline id '[U:1:22202]'
./bin/steamcli --output compact web players 730
./bin/steamcli -o json status | jq .player_counts
./bin/steamcli --output raw web call ISteamWebAPIUtil GetServerInfo --method GET -p format=xml
./bin/steamcli completion bash > steam-completion.bash
```

Library discovery checks common Windows/macOS/Linux locations and Linux Flatpak. `--root` handles nonstandard/custom installations. Both legacy and modern `libraryfolders.vdf` layouts are supported. Malformed manifests produce warnings in the JSON report; their contents are never executed. Library results describe local manifest state, not proof of an account license or cloud availability.

**`auto` is the default**: each command prints the clearest form it has — a table, a report, or a reduced ASF value — and falls back to indented JSON when it has no renderer. `table` forces that rendering, `json` and `compact` produce data for scripts, `raw` preserves response bytes from the API, and `parsed` always reduces ASF envelopes. `-o` is the shorthand.

`auto` reduces an ASF envelope only when it carries an *outcome*. A two-factor token prints as the bare code; a bot listing or `asf status` carries data rather than a result, so it is rendered as a table or printed whole rather than flattened to its envelope message.

Before 0.7.0 the default was `json`. Scripts that parsed stdout should pass `-o json` explicitly. `parsed` reduces an ASF response to the value behind it, so `asf token gabeN --output parsed` prints `JKWGP` and nothing else. ASF nests its payload differently per endpoint: a token arrives as `Result[bot].Result`, an executed command as a bare `Result` string, and a refused operation explains itself in `Result[bot].Message` or the envelope's `Message`. `parsed` walks that order and prints the first value it finds, prefixing each line with the bot name when more than one bot answered. A `Success:false` response still exits nonzero while showing its reason. Payloads that are not ASF envelopes are printed as JSON, so `parsed` is safe to set globally. HTTP errors omit response bodies/credential-bearing URLs. There are no hidden browser sessions, analytics, cookie jars, response logs, or background update checks. Normal HTTP proxy environment settings are honored by Go. Read-only HTTP retries are limited to two for 429/502/503/504, honor bounded `Retry-After`, and never retry authentication failures. Redirects are not followed, preventing credentials from being forwarded.

Exit codes: `0` success, `1` CLI/HTTP/application/verification failure, `130` interrupted; SteamCMD's positive nonzero process exit codes pass through. SteamCMD output remains its native terminal output regardless of `--output`.

## Development and verification

```sh
go test -mod=vendor -race -cover ./...
go vet -mod=vendor ./...
./scripts/build.sh
```

The tests run locally without Steam credentials or external API access. A native OS CI matrix is included but has not been run on hosted runners. See [docs/VALIDATION.md](docs/VALIDATION.md) for actual live checks and limitations, [docs/RESEARCH.md](docs/RESEARCH.md) for design decisions and upstream findings, and [docs/THIRD_PARTY.md](docs/THIRD_PARTY.md) for dependency provenance.
