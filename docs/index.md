---
title: steam-cli
nav_order: 1
---

# steam-cli

One static Go binary for Steam from the terminal: **Web API** discovery and calls,
**Workshop** search and collection management, **service status**, **SteamCMD**
bootstrap and downloads, **ArchiSteamFarm** IPC, local **library** inspection, and
forwarding to the **desktop client**.

No telemetry, no hosted middleware, no browser automation, and no Python, Node or
.NET runtime. CGO-free, so the binary is genuinely portable.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/Bluscream/steam-cli/main/scripts/install.sh | sh
```

Downloads the right binary for your platform, verifies it against the release
`SHA256SUMS`, and installs to `~/.local/bin`. Set `STEAMCLI_PREFIX` to change where,
or `STEAMCLI_VERSION=v0.6.0` to pin a release.

Other options:

| Method | Command |
| --- | --- |
| AppImage | download `steamcli-*-x86_64.AppImage` from [releases](https://github.com/Bluscream/steam-cli/releases), `chmod +x`, run |
| Arch (PKGBUILD) | `makepkg -si` in [`packaging/`](https://github.com/Bluscream/steam-cli/tree/main/packaging) |
| From source | `go build -o bin/steamcli ./cmd/steamcli` (Go 1.26+) |
| Binary | grab `steamcli-{linux,darwin,windows}-{amd64,arm64}` from [releases](https://github.com/Bluscream/steam-cli/releases) |

## A quick tour

```sh
steamcli info                             # services, account, client, libraries
steamcli search "half-life"               # store, workshop, library, owned, players
steamcli account list                     # list saved Steam accounts
steamcli account switch "GabeN"           # switch active account
steamcli nick "NewNick"                   # quickly change Steam nickname
steamcli idle 730                         # idle game via ASF (with native SDK fallback)
steamcli --output raw status              # service health, player counts, CMs, datacenters
steamcli web player 76561197960287930     # Web API helpers
steamcli web methods GetOwnedGames        # discover any of ~170 methods
steamcli workshop search 4000 "map"       # Workshop search, cursor-paged
steamcli workshop subs 4000               # your subscriptions
steamcli --offline library                # local installs, no network
steamcli cmd download 1007 --dir ./game   # SteamCMD, bootstrapped automatically
steamcli asf 2fa --output short           # ArchiSteamFarm two-factor codes
steamcli client run 730                   # hand off to the desktop client
```

Most commands work `--offline` where the data is local, and every command speaks
JSON by default so it composes with `jq`.

## Documentation

- **[README](https://github.com/Bluscream/steam-cli#readme)** — full command reference, credentials, and configuration
- **[Research and decisions](RESEARCH.md)** — why it is built this way, and what Steam's APIs actually permit
- **[Validation record](VALIDATION.md)** — what was tested live, and what explicitly was not
- **[Third-party provenance](THIRD_PARTY.md)** — dependencies, embedded data, and licences

## Credentials

Nothing is persisted by this tool. It reads existing environment variables, or
files you point it at; a profile stores only *names* of variables and files, never
values. A Steam Web API key unlocks most read operations. Collection membership,
your own subscription and favourite lists, and deleting your own Workshop files are
not exposed by the Web API at all and need a browser session cookie — the CLI says
so plainly rather than reporting a success that changed nothing.

## Licence

Public domain, under [the Unlicense](https://github.com/Bluscream/steam-cli/blob/main/LICENSE).
Not affiliated with or endorsed by Valve Corporation.

## Local game settings

The development build adds `library compat list|get|set`, account-selectable
`library launch get|set`, `library dlc list|enable|disable`, `library branch get|set|download`,
and `library app`. `server edit` updates a favourite's label or recorded AppID.
Close Steam before writes. Local edits keep private backups and refuse ambiguous
configuration files; `--force` explicitly overrides the running-client check.
`branch set` records a preference only; `branch download` invokes managed SteamCMD.
See the [handoff audit](AUDIT-2026-09-15.md) for changes, evidence, and limitations.

## Native Steamworks

Native SDK calls now use a generated C++17 helper while the main CLI remains CGO-free.
See [the SDK guide](SDK.md) for setup, calls, sessions, buffers and callbacks.
