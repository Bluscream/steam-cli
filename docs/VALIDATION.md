---
title: Validation record
nav_order: 3
---

# Validation record

Validated locally on **2026-09-13**, Linux amd64. Account payloads, API keys, IPC passwords, and generated two-factor codes are intentionally absent from this report.

## Automated checks

- `go test -mod=vendor -race -coverprofile=coverage.out ./...`: passed, 66.7% aggregate statement coverage. The command entrypoint and convenience handlers account for much of the uncovered code; this is not an exhaustive API conformance claim.
- `go vet -mod=vendor ./...`: passed.
- Minimum supported Go 1.26.8 test run: passed.
- `GOTOOLCHAIN=go1.27.1 go run -mod=mod golang.org/x/vuln/cmd/govulncheck@latest ./...`: **No vulnerabilities found** with govulncheck v1.8.0. This result is date-specific.
- Vendor-only, CGO-free build with `GOPROXY=off`, `GOSUMDB=off`, `GOTOOLCHAIN=local`, and the already downloaded Go 1.27.1 compiler executable: passed. A bootstrap Go launcher may still require network/checksum verification to acquire a toolchain; use an installed compiler for offline builds.
- CGO-free cross-builds: **linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64, windows/arm64** all passed.

Test coverage includes form/query placement, Unicode/repeated parameters, lossless large JSON integers, authenticated ASF command bodies, application-level failures, offline network prevention, URL/path validation, cross-host redirect rejection, secret-safe errors, response limits, GET-only retries, per-key schema cache isolation, configuration precedence, VDF parsing, ID conversions, archive traversal/escaping links/duplicates and deferred safe framework links, bootstrap checksums/idempotency, argument preservation/order, success marker streaming, app manifest validation, process exit propagation, and Unix batch cancellation.

The test suite uses local HTTP servers, temporary directories, and small fake subprocesses. An optional `STEAM_CLI_TEST_ARCHIVES` fixture test audits previously downloaded official archives without executing their contents. It needs neither a Steam account nor ASF service. Hosted native-OS CI is supplied but has **not** been executed in this workspace.

## Live integration checks

| Check | Result |
| --- | --- |
| Public Steam server information, no key | Passed |
| Authenticated Steam `GetSupportedAPIList` | Passed: 54 interfaces, 169 method/version entries |
| Offline catalog filtering after online discovery | Passed |
| Authenticated player summaries, helper and discovered call | Passed; payload discarded |
| Raw `IPlayerService/GetOwnedGames` using `input_json` | Passed; payload discarded |
| ASF instance 1: authenticated status, named bot read, OpenAPI | Passed; ASF 6.3.10.1, 46 schema paths |
| ASF instance 2: authenticated status, named bot read, OpenAPI | Passed; ASF 6.3.10.1, 46 schema paths |
| SteamCMD Linux bootstrap from Valve CDN | Passed |
| Actual Windows/macOS bootstrap archive extraction on Linux | Passed, including four macOS framework symlinks; no foreign executable launched |
| SteamCMD first-run self-update and quit | Passed; Valve client version 1788292693 |
| SteamCMD anonymous app 1007 download with validation | Passed; 107,911,652 bytes reported by Valve |
| SteamCMD repeat validation in the requested installation directory | Passed |
| Download manifest check | Matching app 1007, `StateFlags` marks fully installed |
| Local Steam library scan, offline | Passed: one library, four apps, zero warnings |
| Offline SteamID conversion | Passed |

ASF checks used the user-supplied LAN services on port 1242 with the password in the `Authentication` header. Only read-only endpoints were exercised; bot start/stop/pause, commands that change state, key redemption, trades, and token generation were not run against the user's accounts.

The test SteamCMD runtime and downloaded redistributable are retained under ignored `.references/runtime/`. The CLI did not replace or install over the desktop Steam executable. Valve's own runtime also wrote its standard Steam logs outside that directory, so the managed installation should not be mistaken for an OS sandbox.

## Remaining validation boundaries

- The Linux binary was executed here; macOS and Windows binaries were cross-compiled, not executed on those systems. Native CI and manual SteamCMD login/download checks are still required there.
- Interactive named-account SteamCMD login/Steam Guard was not tested; anonymous login was. Account passwords are deliberately left to Valve's terminal prompt.
- Workshop argument construction and success-marker logic are covered by tests, but no real workshop item was downloaded. Different app entitlements and item availability can affect results.
- Real Steam/ASF write methods were not exercised. Request construction and failure behavior are tested locally.
- Steam API discovery describes only what the tested key was allowed to see. Publisher-only and undocumented endpoints depend on their own authorization and current upstream behavior.
- HTTP requests and archive inputs are bounded, but the external SteamCMD runtime is not sandboxed. Its downloads, self-updates, and account cache behavior belong to Valve.
- SHA-256 checks apply to the downloaded bootstrap only. The wrapper does not claim an independently verified vendor signature or immutable SteamCMD self-updates.

---

# Validation record: workshop and status remediation

Validated on **2026-09-14**, Linux amd64, after the audit of the `status` and `workshop` features.

## Automated checks

- `go test -mod=vendor -race -cover ./...`: passed, **72.0%** aggregate statement coverage (was 66.7%).
- `go vet -mod=vendor ./...`: passed. `gofmt -l`: clean.
- Per-package coverage for the packages this round touched:

| Package | Before | After |
| --- | --- | --- |
| `internal/status` | 1.2% | 91.1% |
| `internal/workshop` | 33.5% | 88.7% |
| `internal/community` | new | 89.4% |
| `internal/cli` | 44.2% | 58.3% |
| `internal/webapi` | 56.7% | 60.7% |

The previous `internal/status` suite contained a `TestFetchPlayerCount` that never called `fetchPlayerCount`; it could not, because the probes hardcoded Valve's hostnames. Probe endpoints are now injectable and the function is tested against a fixture server.

New coverage includes: EResult interpretation from both the `x-eresult` header and the response body, including the silent-success case; per-item batch outcomes where one item succeeds and another is refused; collection membership add/remove against a fixture Community server; CSRF double-submit (the `sessionid` cookie and form field must agree); expired-session detection via login redirect; cursor pagination including a server that repeats a page; workshop ACF parsing against a manifest whose item blocks contain sizes, timestamps, manifest IDs and ugchandles that must not be mistaken for item IDs; and the refusal paths that report a missing Community session instead of a false success.

## Live integration checks

| Check | Result |
| --- | --- |
| `status --output raw`, full probe set | Passed; 4 endpoints, 8 player counts, CS2 coordinator with matchmaking and 30+ datacenter regions, 5 CMs |
| Steam Help returning HTTP 302 | Classified `normal`; the host is serving traffic |
| `workshop installed 107410` | Passed; 191 items, matching an independent block-level parse of the same manifest |
| `workshop collection 3052582377` | Passed; title and 47 children |
| `workshop search 4000 "car"` | Passed; 129,656 total, cursor-paged |
| `workshop search-collections 4000 "weapons" --all` | Passed; walked ~49 cursor pages, 968 of 1,007 returned |
| `workshop search --page 4` | Passed; returns results past the depth where page-based paging is capped |
| Session-required commands without a cookie | Passed; each names `STEAM_LOGIN_SECURE` and exits nonzero |
| `delete-collection` without `--yes` | Passed; refuses |

## Remaining validation boundaries

- **No Community session was available during validation, so no session-authenticated write was executed against Steam.** `add-items`, `remove-items`, populated `create-collection`, `subs`, `favorites`, and the session path of `delete-collection` are covered by fixture-server tests that assert the request shape — path, form fields, CSRF double-submit, cookie — and by their refusal paths. Their behavior against live steamcommunity.com is unverified. The endpoints and form fields follow what the Workshop web UI sends; Valve can change them without notice, and they are not part of any documented API.
- The subscription and favorite listings are parsed from HTML, anchored on the `sharedfile_<id>` element ID. This is the same data the Workshop page shows, and it will break if Valve restyles that markup. An item Steam declines to render does not appear.
- No subscribe, unsubscribe, publish, edit, or delete was executed against the user's real account. Request construction, EResult handling, and per-item reporting are covered by tests.
- `IPublishedFileService/Delete` being publisher-only is taken from the bundled xPaw catalog annotation and was not confirmed by attempting a live delete.
- Player counts and coordinator data reflect Valve's public endpoints at the time of the run.

## Addendum, 2026-09-14: binary rename, `client`, `--bots`, `--output parsed`

- Executable renamed `steam` to `steamcli`, matching Valve's `steamcmd` and removing the collision with the desktop client's own `steam`. `cmd/steam` moved to `cmd/steamcli`; build scripts, CI and docs updated; stale `dist/steam-*` artifacts removed and `SHA256SUMS` regenerated.
- `steamcli client` forwards to the desktop client. Discovery verified live: resolves `/usr/bin/steam` on this machine. The self-reference guard was verified live by pointing `STEAM_CLIENT_PATH` at this CLI, which is refused.
- **No game was launched and no `steam://` URL was handed to the running desktop client during validation**, since that has a visible effect on the user's session. Argument forwarding, exit-code propagation and URL construction are covered against a stand-in launcher script.
- `asf --bots` and `--output parsed` verified live against the user's ArchiSteamFarm 6.3.10.1: token extraction for one and several bots, the default `ASF` selector, a scalar command result, and a `Success:false` response printing its reason while exiting nonzero.

## Addendum, 2026-09-14: default output

`--output auto` became the default, with `-o` as shorthand. Verified live: `status`
renders its full report with no flags, `library`, `id`, `doctor`, `workshop
collection`, `search`, `subs`, `installed` and the batch summaries render as tables,
and `asf token` reduces to the bare code.

Making the ASF reduction automatic initially broke `asf bots` and `asf status`,
which were flattened to their envelope message and lost the data being asked for.
`asf.Parse` now claims a payload only when each entry carries a scalar result or a
message, and reports anything else as unparsed so it prints whole; `asf bots` gained
its own table. Both directions are covered by tests, including that a bot listing is
never reduced to `A: OK`.

`--output raw` on `status` still renders the report rather than JSON: `emit` is only
given values this CLI assembles, never server bytes, so raw has no other meaning
there and the pre-existing behaviour is preserved.

**This is a breaking change for scripts** that parsed the previous JSON default;
`-o json` restores it. Tests that parse output were updated to request it explicitly.

---

## Addendum, 2026-09-15: audit of the 0.8.0 work

Reviewed 18 commits adding `info`, `apps`, `search`, `library custom`/`--sort`,
styled tables with colour, CSV output, and logged-in-user auto-detection.
Build, `go vet` and the suite passed on arrival; the following were corrected.

- **`--output parsed` had been removed** in favour of `short`, while the README,
  `docs/index.md` and the published v0.7.1 release notes still instructed its use;
  every documented example errored. `parsed` is now accepted as a deprecated alias
  and the docs name `short`.
- **`STEAM_WEB_API_KEY` had stopped being read** when the default variable became
  `STEAM_API_KEY`. It was the documented primary since the first release, so it is
  restored as a fallback; an environment setting only the old name works again.
- **Three tests required live internet**, and one also read the developer's real
  Steam library, contradicting this document's claim that the suite runs without
  external API access. The store search endpoint is now configurable
  (`store_url` / `STEAM_STORE_URL`, matching `web_url` and `community_url`), the
  tests use fixtures, and the whole suite passes with the network blocked.
- **`steamcli info` discarded every failure silently**: all three error paths
  returned without recording anything, and the `Error` fields on its structs were
  never populated, so an unreachable API produced a section that simply vanished.
  Failures are now collected into `problems` and shown as notes. ASF stays quiet
  when none is configured, since it is optional.
- **Launch options were printed verbatim.** They routinely carry RCON passwords
  and API tokens; values matching credential patterns are now redacted in both
  the table and `-o json`, with `--show-secrets` to override.
- Three files were not `gofmt`-clean.

Coverage moved 71.9% → 69.2% with 2,695 lines added; `internal/library` fell to
47% and gained tests only for the redaction added here. Raising coverage on the
new `info`, `apps` and `search` code remains outstanding.

### Refactoring pass

After the defect fixes above, the following structural problems were addressed.

- **A data race.** `search` and `info` fan out across goroutines that each
  resolved the configuration, and resolving it writes back to the shared options
  struct when a profile sets `allow_http`. Reproducible under `-race`; the
  reproduction is kept as `TestSearchConcurrentSettingsIsRaceFree`. Settings are
  now resolved once with `sync.Once`, which also removed four redundant
  file reads per `apps` invocation.
- **Divergent user resolution.** Three commands had grown their own copy of
  "resolve the logged-in user" and `search` had lost the desktop-client
  fallback, so it resolved a different account from `web` and `info` on the same
  machine. One implementation now serves all three.
- **CSV carried human formatting** — `546,909`, `383 ms`, `33.4 MiB`. go-pretty
  quotes the grouped numbers so it still parsed, but a consumer should not have
  to strip separators and units out of a numeric column. CSV now emits bare
  numbers and raw bytes; the table keeps the readable form.
- **The new local-config readers were untested.** `LoggedInUser`,
  `ScanCompatTools` and `ScanLaunchOptions` decide which account a command acts
  on and parse user-supplied VDF. Covered, including MostRecent winning over a
  later timestamp and a malformed `config.vdf` not failing the scan.
- **`info`, `search` and `apps` were undocumented**; the README command banner
  predated all three.

Coverage: `internal/library` 47% → 83.8%, `internal/cli` 63.9% → 67.2%,
project 69.2% → 72.6%, above the 71.9% that preceded this round of work.

The ASF memory conversion was checked against a live instance: 182,319 KB
reported by ASF renders as 178.1 MiB, matching its OpenAPI schema's KB unit.

### `steamcli server` (Game Servers)

Added a command set mirroring the client's Game Servers dialog. Verified live:

| Check | Result |
| --- | --- |
| `server browse "counter-strike 2" --not-empty` | Passed; name resolved to AppID 730, 5 servers, busiest first |
| `server info <addr> --players` | Passed against a live CS2 server: map, 16/64, version, OS, ping, scoreboard |
| `server favorites` | Passed; 19 entries read from the client's own file |
| `server history` | Passed; 94 entries |
| `server favorites --refresh` | Passed; 1 of 19 servers from 2018 still answering |
| `server add` / `remove` round trip | Passed on a **copy** of the real file: 19 → 20 → 19, history untouched, all 226 entries preserved, backup written |
| `server lan` | Ran; nothing answered on this network |

**A panic in the A2S library was found and contained.** `go-a2s` indexes into
replies without always checking length, and a truncated challenge from one of
the stale favourites crashed the process mid-listing. Server replies are
untrusted input from arbitrary hosts, so every entry point into the library now
recovers and returns an error for that address instead. Found only by querying
real servers; a fixture would not have produced it.

Not exercised: `server connect` (it launches the desktop client and joins a
game), and writes against the live file while Steam is running — the commands
detect a running client and warn that it will overwrite the file on exit.

### Provenance drift

`go-pretty`, `go-runewidth`, `uniseg` and `golang.org/x/text` were vendored into
the binary without being recorded in `docs/THIRD_PARTY.md`, the same class of
gap as the embedded xPaw catalog. All four are now documented, the bundled
licence file regenerated, and `internal/meta` holds a test that fails when a
module in `go.mod` is missing from the document.

## 2026-09-15 Claude handoff audit

See [the audit](AUDIT-2026-09-15.md) for findings and scope. Final `go test -race ./...`, `go vet ./...`, formatting, and `git diff --check` passed. New regression tests cover strict VDF rejection/round-trips, secure backup retention, concurrent-writer exclusion, account selection, root aliases, symlinked tool discovery, launch/DLC/branch/compatibility writers, CLI write refusal and redaction, branch-download argument construction, server editing and unknown-field preservation, and failed CM discovery.

`STEAMCLI_AUDIT_ROOT` opt-in validation parsed and rewrote **eight real configuration/manifest files on temporary copies only**. Local read-only checks passed for compatibility tools, SteamVR branch/DLC metadata and branch-download dry-run. Public HTTP checks returned Store/Community/Web API 200 and Help 302; the live player-count request succeeded. No real Steam configurations, subscriptions, favorites or game files were changed.

The final source cross-built CGO-free for Linux, macOS and Windows on amd64 and arm64. A separate vendored build with `GOPROXY=off`, `GOSUMDB=off` and the already installed compiler passed. The local binary is `0.10.0-dev`; these are local development artifacts, not a published release. Native macOS/Windows process inspection was cross-compiled but not executed here. Real branch downloads and Steam's adoption of edited DLC/branch preferences were not exercised; the tests use temporary files, and SteamCMD's existing validation is reused.
