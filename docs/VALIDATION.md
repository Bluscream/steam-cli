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
