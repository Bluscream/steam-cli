---
title: Third-party provenance
nav_order: 4
---

# Third-party provenance

The application code in `cmd/` and `internal/` is released into the public domain under [the Unlicense](../LICENSE). No third-party repository source was pasted into those directories. Protocol conventions, endpoint names, and behavioral findings were researched against the sources in [RESEARCH.md](RESEARCH.md); they are not asserted to be newly invented algorithms.

Runtime-linked Go modules are pinned by `go.mod` / `go.sum` and copied by `go mod vendor`. Keep `vendor/` license files with any source redistribution and include applicable notices with binary distributions. The Go standard library and toolchain retain their own licenses.

| Module | Pinned version | Role | License location |
| --- | --- | --- | --- |
| github.com/spf13/cobra | v1.10.2 | Commands, help, shell completion | `vendor/github.com/spf13/cobra/LICENSE.txt` |
| github.com/spf13/pflag | v1.0.9 | Flag parsing (Cobra dependency) | `vendor/github.com/spf13/pflag/LICENSE` |
| github.com/inconshreveable/mousetrap | v1.1.0 | Windows console behavior (Cobra dependency) | `vendor/github.com/inconshreveable/mousetrap/LICENSE` |
| github.com/andygrunwald/vdf | v1.1.0 | Valve KeyValues/ACF parsing | `vendor/github.com/andygrunwald/vdf/LICENSE` |
| github.com/gofrs/flock | v0.13.1 | Cross-process file locks | `vendor/github.com/gofrs/flock/LICENSE` |
| golang.org/x/term | v0.46.0 | Terminal detection | `vendor/golang.org/x/term/LICENSE` |
| golang.org/x/sys | v0.48.0 | OS calls for locks/terminals | `vendor/golang.org/x/sys/LICENSE` |

## Embedded data

One third-party data file is compiled into the binary rather than merely vendored for the build:

| File | Origin | License | Role |
| --- | --- | --- | --- |
| `internal/webapi/data/xpaw.json` | [xPaw/SteamWebAPIDocumentation](https://github.com/xPaw/SteamWebAPIDocumentation) | MIT, Copyright (c) 2019 Pavel Djundik; text in `internal/webapi/data/LICENSE.xpaw` | Offline Steam Web API catalog backing `web methods` and verb/version discovery |

It is embedded with `go:embed`, so every distributed artifact contains it and the MIT notice must accompany any redistribution. It is data, not source: no xPaw code is compiled in. `--catalog live` avoids it entirely and queries `GetSupportedAPIList` instead.

Reference-only repositories remain under ignored `.references/`, with exact revisions in [references.json](references.json). Their complete licenses remain in those clones. They are not compiled into or bundled with the CLI. Reference libraries were assessed for suitability; no new API-client runtime dependency was added where the standard HTTP library already supplied the needed functionality.

SteamCMD is proprietary Valve software, fetched directly from Valve on demand. It is not redistributed as part of this source tree or the CLI artifacts. ASF is a separately operated service; its C# application is not embedded.

The Unlicense covers this project's own code only. It does not and cannot relicense the vendored Go modules, the embedded xPaw catalog, Valve's SteamCMD, or anything else listed above; those keep their own terms, and their notices must travel with any redistribution.

This project is not affiliated with, endorsed by, or sponsored by Valve Corporation. Steam, SteamCMD and the Steam logo are trademarks of Valve Corporation.
