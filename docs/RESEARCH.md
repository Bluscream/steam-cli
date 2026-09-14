# Research and implementation decisions

Research date: 2026-09-13. Nine upstream repositories were cloned into the ignored `.references/` directory for source inspection. Exact repository URLs, commits, and commit dates are in [references.json](references.json). No reference repository is a runtime dependency.

## Language and architecture

Go was selected for a small dependency surface, mature command tooling, native HTTP/archive/process support, race testing, and straightforward CGO-free cross-compilation. Rust was a viable alternative: clap, reqwest, serde, and archive crates could implement the same architecture. It would not eliminate the need for Valve's proprietary SteamCMD bootstrap or an ASF service. The decision is an implementation tradeoff, not a claim that one language is universally better.

The project separates command parsing, non-secret configuration, bounded HTTP transport, Steam Web API discovery, ASF IPC, SteamCMD management, and local metadata. Cobra supplies standard command help, flag parsing, and shell completion. Mature libraries handle VDF, cross-process file locking, and terminal detection; Go's standard library supplies the protocol and archive primitives. [Cobra](https://github.com/spf13/cobra), [VDF parser](https://github.com/andygrunwald/vdf), [flock](https://github.com/gofrs/flock), [Go standard library](https://pkg.go.dev/std).

## Steam Web API contract

Valve documents separate public and publisher API hosts, interface/method/version URL paths, UTF-8 form parameters, and POST parameters in the request body. Discovery can depend on the key's permissions. This supports a generic transport with a small set of convenience commands rather than a fixed collection of endpoint-specific types. [Valve Web API overview](https://partner.steamgames.com/doc/webapi_overview), [ISteamWebAPIUtil](https://partner.steamgames.com/doc/webapi/ISteamWebAPIUtil).

Source inspection of `ValvePython/steam/steam/webapi.py` confirmed dynamic interface population, method versions and HTTP verbs, list parameters represented as indexed names, and different query/body placement for GET/POST. The CLI uses these wire conventions, implements them in Go, and tests requests against local HTTP servers. It passes `input_json` through intact for service-style methods. No Python runtime or Python source was embedded. [ValvePython Web API implementation](https://github.com/ValvePython/steam/blob/26166e047b66a7be10bdf3c90e2e14de9283ab5a/steam/webapi.py).

The discovery cache is scoped by host plus a key fingerprint. It contains method descriptions, not the key or private account responses. Calls can explicitly select verb/version to avoid depending on catalog availability. Cached methods are not a promise that every endpoint remains available, that the key grants permission, or that an account's privacy settings expose its data. The wrapper preserves response numbers as JSON bytes to avoid rounding SteamID64 values.

## Existing ecosystem comparison

| Project inspected | Finding from the checked-out source | Decision |
| --- | --- | --- |
| [Philipp15b/go-steamapi](https://github.com/Philipp15b/go-steamapi) | Typed wrappers organized around an older, finite API surface; checked-out tip dated 2021. | Do not make its endpoint coverage the limit of a new generic wrapper. |
| [faceit/go-steam](https://github.com/faceit/go-steam) | Steam network/CM implementation with community/trading features; checked-out tip dated 2019. | Too much protocol/authentication scope for these three integrations; would require independent modern login validation. |
| [ValvePython/steam](https://github.com/ValvePython/steam) | Dynamic Web API layer plus a much larger network/authentication/CDN client. | Reference for Web API behavior and future protocol work; avoid adding a Python runtime. |
| [ValvePython/steamctl](https://github.com/ValvePython/steamctl) | Broad Python CLI including Web API, depot, workshop, and authenticator commands. | Useful coverage/UX reference; its direct depot/CM functionality is not silently claimed by this wrapper. |
| [DoctorMcKay/node-steam-user](https://github.com/DoctorMcKay/node-steam-user) | Rich Steam network client with refresh-token login and related authentication machinery. | Good future reference for direct-client features; adopting it now would require Node and introduce account-session management. |
| [Noxime/steamworks-rs](https://github.com/Noxime/steamworks-rs) | Rust bindings to the Steamworks SDK and its native runtime. | Different abstraction: in-game/client SDK integration, not a replacement for Web API or SteamCMD. |
| [dmadisetti/steam-tui](https://github.com/dmadisetti/steam-tui) | Rust TUI around SteamCMD and local metadata. | Confirms process/metadata approach; a TUI dependency tree is unnecessary for this command-oriented tool. |
| [Austrum-lab/game-fetcher-cli](https://github.com/Austrum-lab/game-fetcher-cli) | Go SteamCMD provider with installer, scripts, output classification, and tests for false success. Provider is internal to its application. | Inspect its operating behavior/tests; implement a narrower package around standard library primitives instead of importing inaccessible internals. |
| [JustArchiNET/ArchiSteamFarm](https://github.com/JustArchiNET/ArchiSteamFarm) | Maintained C# account automation service with typed IPC controllers, authentication middleware, and instance-generated OpenAPI. | Integrate its public IPC contract; let the service own Steam account sessions. |

Tip dates describe the local research snapshots and are not blanket claims that every fork/package is unmaintained.

## SteamCMD bootstrap and process behavior

The reference Go provider points to Valve's `steamcmd_linux.tar.gz`, `steamcmd_osx.tar.gz`, and `steamcmd.zip` installers. Its script tests verify that `force_install_dir` precedes login and demonstrate why a zero process exit code is insufficient. Our helper additionally checks its current-run success marker and the requested app's local manifest. These behaviors have their own tests here; no foreign implementation was copied wholesale. [Provider source](https://github.com/Austrum-lab/game-fetcher-cli/tree/33671e8042cde7a91c275dcbf076e6b29307f3dd/internal/provider/steam).

The Valve Developer Community SteamCMD page could not be retrieved through the web fetcher (HTTP 403). Installer URLs and macOS layout were cross-checked against the provider source and Homebrew's maintained cask. Homebrew stages the executable beneath `MacOS/` because the runtime creates a relative Frameworks link. The CLI follows that layout and does not invent a native ARM Linux bootstrap. Direct inspection of the current macOS archive also found four framework symlinks, so extraction defers contained symlinks until after file writes and validates their resolved targets. [SteamCMD documentation](https://developer.valvesoftware.com/wiki/SteamCMD), [Homebrew cask](https://github.com/Homebrew/homebrew-cask/blob/master/Casks/s/steamcmd.rb).

The bootstrap's mutable HTTPS URL does not provide this wrapper with independently authenticated immutable release metadata. We therefore record its observed SHA-256, allow an optional expected hash, and make no signature-verification claim. Valve self-updates after extraction. Linux installation, self-update, anonymous login, and a real app download were tested locally; macOS/Windows runtime behavior still needs native-machine testing.

SteamCMD has its own output, credential cache, log locations, and platform dependencies. We avoid capturing account passwords and preserve the terminal for Valve's prompts. Raw passthrough permits scripts and advanced commands. Direct downloads require correct account entitlements; this project neither bypasses ownership nor treats an API key as a login session.

## ASF contract

ASF documents IPC header authentication and warns that repeated invalid password attempts can result in a temporary ban. Its API is described by instance-local OpenAPI. The client therefore uses `Authentication`, does not automatically retry authentication failures or write calls, and retrieves the schema directly from the selected service. [ASF IPC documentation](https://github.com/JustArchiNET/ArchiSteamFarm/wiki/IPC).

Offline source inspection covered `ASFController`, `BotController`, `CommandController`, `TwoFactorAuthenticationController`, `BotPauseRequest`, `ApiAuthenticationMiddleware`, and `ArchiKestrel`. This established `/Api/ASF`, `/Api/Bot/{selector}`, JSON `Command`, pause fields, the token endpoint, and `/swagger/ASF/swagger.json`. Bot selectors remain a single encoded path segment. `Success:false` is an application failure even under HTTP 200. [IPC implementation at the research revision](https://github.com/JustArchiNET/ArchiSteamFarm/tree/2d9ca13acc8d951e1b7924c52e5bc4e41410c053/ArchiSteamFarm/IPC).

Both supplied LAN instances were available on port 1242. Their status, bot data, and schemas were read successfully without mutation. Passwords and account payloads were not saved in repository artifacts. This validates the HTTP boundary, not every plugin or state-changing operation.

## Privacy and self-containment boundaries

The wrapper makes only explicit API requests, discovery requests needed for a generic call, and first-use SteamCMD downloads. It has no telemetry service, browser cookies, cloud proxy, or background updater. Credentials stay in environment variables or separately referenced files; URLs with embedded credentials/query strings are rejected as base URLs. Cross-host redirects are never followed. Large responses, archive expansion, input bodies, and VDF reads are bounded.

Vendoring makes Go dependency source available offline. A Go compiler must already exist for a truly offline build; the first toolchain acquisition is separate. The resulting native CLI needs no language runtime, but system CA certificates/network configuration still apply. SteamCMD's proprietary files and ASF's service remain external components. No third-party service can convert restricted Steam data into authorized data for this wrapper.

## Deliberate scope and extension path

Implemented now: generic Web API GET/POST including service JSON, catalog discovery/caching, common read helpers; SteamCMD bootstrap/run/update/download/workshop; generic ASF API/plugin calls, OpenAPI, bot controls, commands and token reads; profiles, diagnostics, completions, local library reading, and Steam ID conversion.

Not implemented: native CM protocol, QR login/token vault, Steam Guard enrollment, standalone depot reconstruction, inventory/trade pagination helpers, websocket log streaming, automatic ASF installation/service management, game launching/Proton orchestration, or an OS keychain backend. Many account operations are already reachable through explicitly requested ASF commands/raw IPC. If direct CM becomes a requirement, evaluate the authentication/protobuf behavior of current SteamKit and steam-user against real accounts before porting individual pieces; importing an old client library is not equivalent to having a tested modern login flow.
