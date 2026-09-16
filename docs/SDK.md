---
title: Native Steamworks SDK
nav_order: 5
---

# Native Steamworks SDK

`steamcli sdk` calls Valve's **local native Steamworks library**. `steamcli web`
uses HTTP; the SDK instead communicates with the logged-in desktop client or
initializes a game-server context. Your app's permissions, ownership, configured
features and prerequisites still apply. This does not inject into a running game.

## Build once

Provide a local Steamworks SDK containing `public/steam/steam_api.json`, the
corresponding headers, and `redistributable_bin`. Obtain an SDK through your
normal Steamworks development setup; the CLI does not redistribute Valve's SDK.

```sh
steamcli sdk build --sdk-dir /path/to/sdk
steamcli sdk path
steamcli sdk methods GetSteamID
steamcli sdk schema > steamworks-schema.json
```

The build needs a C++17 compiler (`c++`, or `clang++` on Windows; override with
`--cxx /path/to/compiler` or `CXX`). No Python, Node, CMake or external JSON package
is required. The compiler is only needed to build/rebuild the helper. The main
Go executable remains CGO-free and embeds the adapter source plus the MIT-licensed
nlohmann/json single header. Generated code uses `decltype(&SteamAPI_...)` against
the supplied SDK, so the native compiler determines the ABI.

Builds are locked and cached under the CLI data directory using a hash of adapter
source, metadata-derived dispatch, JSON dependency, SDK headers, compiler path,
and target platform. Re-run `sdk build` after updating the CLI or SDK. The SDK and
runtime must remain available at their recorded paths. `STEAM_SDK_DIR` supplies
a default SDK directory. `--library` / `STEAM_SDK_LIBRARY` overrides the runtime
with an absolute path; it must match the helper's architecture and SDK interfaces.

The supplied reference SDK generated **977 entry points** (913 interface methods
and 64 struct methods). Discovery includes all its interfaces, overloads, enums,
typedefs, callback descriptions and structs. Counts follow your SDK, not a frozen
list in this CLI. Overloaded methods require their exact flat symbol.

## One call

Start Steam and sign in. Select an explicit AppID; 480 is Valve's Spacewar sample.
SDK initialization can temporarily show that app as running in your Steam presence.

```sh
steamcli sdk call ISteamUtils GetAppID --appid 480
steamcli sdk call ISteamUser BLoggedOn --appid 480
steamcli sdk call ISteamFriends GetFriendCount --appid 480 --args '[4]'
steamcli sdk call ISteamFriends GetFriendByIndex --appid 480 --args '[0,4]'
```

Arguments are a JSON array in native parameter order. Strings, booleans, numbers
and numeric enums are supported. Pass 64-bit IDs/handles as **decimal strings**;
64-bit results are emitted as strings to avoid JSON consumer rounding. The return
value appears under `value`; a returned `false` is a native return value, not a
transport failure. Initialization, conversion, missing-symbol and process failures
exit nonzero. `--run-timeout 30s` bounds native execution; zero means unlimited.

Steam diagnostics go to stderr, leaving stdout parseable. A one-shot call exits
and shuts down its native session; use a session for async results or resources.
`--offline` refuses native sessions because a local Steam client can use networking;
method/schema discovery and helper compilation work offline.

## Persistent sessions

`session` accepts JSON Lines on stdin and writes one JSON reply per request.
It first initializes the client unless `--no-init` is supplied. An optional `id`
is echoed in the reply. Replies have `ok` and `result` or `error`. Failed requests
are reported individually and the process exits nonzero if any request failed.

```sh
steamcli sdk session --appid 480 <<'JSONL'
{"id":"buffer","op":"buffer","size":256}
{"id":"beta","op":"call","method":"SteamAPI_ISteamApps_GetCurrentBetaName","args":[{"buffer":"b1"},256]}
{"id":"contents","op":"read","buffer":"b1"}
{"id":"events","op":"poll","wait_ms":1000}
JSONL
```

Buffer/handle IDs are session-local and sequential; clients should read the IDs
from responses instead of assuming them. In the example, the first allocation
is `b1` because initialization allocates no adapter buffers/handles.

| Operation | Purpose |
| --- | --- |
| `call` | Exact flat `method`, JSON `args`, optional `self` buffer/native handle |
| `buffer` | Allocate zeroed `size` bytes, optionally initialize with `hex` |
| `read` | Read an owned buffer as `{buffer,size,hex}` |
| `write` | Write `hex` into an owned buffer without exceeding its size |
| `layout` | Obtain compiler-derived size, alignment and accessible field offsets/types for a named `type` |
| `poll` | Pump manual callbacks; optional `wait_ms` from 0 through 60000 |
| `init` | Explicit `client` or `gameserver` initialization with `--no-init` |

Pointers accept `null`, `{"buffer":"b1"}`, or `{"handle":"h2"}`. Immutable
`const char*` parameters also accept JSON strings. Returned C strings are copied;
other pointers become opaque session handles. Struct values use exact-size
`{"hex":"..."}` payloads. For struct methods, pass a suitably sized buffer as
`self`. Use `layout` rather than assuming packing, field offsets or pointer sizes.
Incomplete SDK types have no inspectable layout, and inaccessible/private fields
are not fabricated. Parameter buffers and strings remain alive for the session.
Each buffer is limited to 16 MiB, aggregate owned memory to 64 MiB, and stored
handles to 65,536. Native array counts must match the supplied buffers: these are
low-level SDK calls, not an automatic memory-safe replacement for SDK programming.

`poll` copies callback bytes before freeing Valve's callback record. It returns
callback IDs, SDK names and `hex` payloads. `SteamAPICallCompleted_t` events also
include the decimal call handle, result callback ID/name, failure state and
`result_hex`. Interpret payloads using `schema` and compiled `layout` output.
Callbacks are pumped only when requested (the idle helper pumps them regularly).

## Game-server sessions

Use `session --no-init --appid YOUR_APPID` and explicitly initialize:

```json
{"op":"init","mode":"gameserver","ip":0,"game_port":27015,"query_port":27016,"server_mode":1,"version":"1.0.0.0"}
```

Game-server accessors are then selected for shared interfaces. Initialization
alone does not log on or publish a server. Configure it and call the SDK's logon
methods explicitly when intended. Client-only/game-server-only interfaces may be
unavailable in the other context. Steam remains responsible for validating access.

## Idling integration and limits

`steamcli idle 480 --sdk` uses the same helper, waits for successful initialization,
and then keeps a background session alive. `steamcli idle stop` requests shutdown
through a private control file rather than signalling a possibly reused PID.
The native path supports exactly one app and no custom presence text; use ASF
for multiple apps or custom status. Init failure is an error, not a successful PID.

The generic adapter exposes the supplied flat API, but does not implement a game
engine, graphics context, native callback function bodies or C++ callback-object
subclasses. Non-null function-pointer hooks require application-specific native
code; manual polling supports Steam's normal callback/result queue. Some methods
need valid engine-owned resources or lifecycle setup. Released Steam handles can
become invalid even while an adapter handle still exists. Invalid native use can
crash the helper; the main CLI reports a process failure. There is no claim that
all 977 methods are meaningful for every account, app, OS or current game state.

The Go CLI cross-builds for all six supported targets. The helper has been built
and exercised on Linux amd64; native Windows/macOS/ARM execution is not verified
here. Windows ARM64 requires a compatible native Steam runtime supplied explicitly.

## Verification

The optional `STEAMCLI_TEST_SDK=/path/to/sdk go test ./internal/sdk -run TestNativeABI -v`
builds every method against real SDK headers but invokes a local fake shared
library, never a real account. On Linux it checks primitive/string returns,
64-bit precision, output buffers, struct returns, callback lifetime/result copying,
invalid arguments, game-server accessor selection, and acknowledged idle startup
and controlled shutdown. `STEAMCLI_TEST_SDK_CACHE` reuses helper builds for this test.
