# Language bindings

## BLUF

The SDK packages Core's typed API in one native library. UniFFI generates Kotlin,
Swift, Python and Go from the Core metadata. Core owns persistence, candidate
checks and protocol behavior. The SDK owns generation, language packages and the
Android AAR.

## API and runtime modules

| Language | API types and errors | Client, context and storage |
| --- | --- | --- |
| Kotlin | `org.arachne.core.api` | `org.arachne.core.runtime` |
| Swift | `ArachneSDK` | `ArachneSDK` |
| Python | `arachne_generated.arachne_api` | `arachne_generated.arachne_runtime` |
| Go | `generated/go/arachne_api` | `generated/go/arachne_runtime` |

Python also exports both modules from `arachne_generated`. The Go import prefix is
`github.com/arachne-systems/arachne-sdk/`. The SwiftPM product is `ArachneSDK`.
These modules all use the SDK's `libarachne_sdk` library. An application does not
build or load a second Core library.

The Swift package compiles both generated components into the `ArachneSDK` module.
Each component has a separate C FFI module. This lets the runtime component use
Core API types without a copied record or an extra import in generated code.

The Core crates `arachne-api` and `arachne-runtime` define the UniFFI records,
enums and objects behind their `uniffi` feature. The SDK enables that feature and
links their scaffolding. It has no copied Client implementation or candidate map.
The old JSON C ABI, C header and hand Go, Python and Swift clients are removed.
The generated UniFFI implementation is also the selected Kotlin line. The
historical handwritten Kotlin branch was reconciled without importing its copied
client, models, native bridge or Rust FFI. The Android AAR compiles the generated
Kotlin sources directly. See the
[reconciliation record](reviews/2026-09-26-kotlin-line-reconciliation.md).

## Client contract

Use `default_client_config(network)`, `default_transport_options()` and
`default_limits()` to obtain Core's defaults. Set the endpoint secret and storage
before creating, joining or restoring a workspace. `StorageConfig.open_sqlite`
takes a host-private directory and a separate 32-byte storage root. Create the
directory before opening storage.

Core zeroizes its native copies of the endpoint secret after opening. The host
runtime still owns the bytes placed in `ClientConfig`: keep that buffer
short-lived and clear mutable Go, Kotlin and Swift buffers after `open`. Python
bytes are immutable, so load them just before `open` and release references
immediately afterward. Generated diagnostic strings redact the secret.

Use `Context.owned(limits, power, workers)` when the host needs independent limits
or lifecycle control. Open the client with `Client.open_in(context, config)`.
`Client.open(config)` uses the process default context. Suspending an owned context
does not suspend other contexts.

| Group | Operations |
| --- | --- |
| Lifecycle | Endpoint description, workspace state, event wait, wake, deadline, network change and close |
| Storage | Open SQLite storage; restore active, joining or removed state; freshness anchors; reset |
| Membership | Invitations, admission, join and workspace drivers; management, self-update, leave and removal |
| Publication | Workspace or member policy, interests, protected publications and current values |
| Inbox | Stage and adopt reception; read pending objects; acknowledge or reject |
| Recovery | Range, direct and current-view recovery; cutoff notices; stage and adopt |
| Resources | Protected resource publication, tickets, fetch and status |
| Discovery | Nearby advertisement and scan; address hints; presence and connectivity |

Candidates are opaque objects. The matching adopt operation accepts a candidate
only from its own client, for the correct operation and at most once. Core writes
and reads back the durable state before it adopts the change. A caller does not
save or reconstruct candidate bytes. A caller can discard a candidate without
adopting it.

IDs use lowercase hex strings in the generated languages. Counts use fixed-width
integers. A freshness anchor uses Core's stable 40-byte encoding. Errors are Core
`ApiError` variants. Use `api_error_code(error)` to branch on the stable error code.
A malformed foreign ID fails binding conversion before a Core operation starts.
Keep a default branch for future enum variants and check `api_version()`.

All Client calls block. Call them on a worker thread. `wake`, `close`, `next_event`
and `wait_for_work` can run on separate threads. Close releases a parked waiter.
Core's Kotlin configuration names the session close operation `shutdown`; the generated
`AutoCloseable.close()` releases the foreign object handle. Call `shutdown()`
before `close()` or before the end of `use { }`.

## Generate and check

All generators use UniFFI `=0.31.2`. The Go generator is pinned and patched for
explicit enum discriminants; see [patch provenance](../patches/README.md).

```sh
scripts/build-uniffi-bindgen-go.sh
scripts/generate-bindings.sh
JNA_JAR=/path/to/jna-5.17.0.jar scripts/uniffi-smoke.sh
```

The smoke script checks Kotlin, Swift, Python and Go. The language flows cover
invitation, join, publication, recovery, inbox acknowledgement and rejection,
candidate misuse, and lifecycle control. The Python storage check also covers
owned context isolation, durable reopen, typed workspace progress, management,
candidate discard, nearby results and a restored removal. Each run has a
120-second limit.

Generated files are committed. CI generates them again and rejects drift. Do not
edit generated code. `scripts/generate-bindings.sh` reads the per-crate Core
configuration for Kotlin, Swift and Python. The SDK configuration sets the Go
module prefix. Python package metadata is in the root `pyproject.toml`. Go uses
the root `go.mod`.

For local native builds, keep Cargo caches bounded:

```sh
CARGO_INCREMENTAL=0 CARGO_PROFILE_DEV_DEBUG=0 CARGO_PROFILE_TEST_DEBUG=0 \
  cargo +1.98.0 build --locked -p arachne-sdk
```

The native library must match the host OS and architecture. Python's generated
loader loads it from beside the generated modules. The source package contains
Python code; install the matching native library in that directory separately:

```sh
uv pip install --target target/python .
cp target/debug/libarachne_sdk.so target/python/arachne_generated/
PYTHONPATH=target/python python3 -c 'from arachne_generated import api_version; print(api_version())'
```

Use `.dylib` on macOS. Go uses cgo. Swift links `arachne_sdk` from the host's library
search path. Native wheels and prebuilt release libraries need a separate release
package decision.

## Android AAR

The AAR contains generated Kotlin and `libarachne_sdk.so` for `arm64-v8a` and
`x86_64`, with API 26 as the minimum. `scripts/build-android-libs.sh` uses cargo-ndk
and NDK 27.1.12297006. It checks 16 KB LOAD alignment in both native libraries.

```sh
GRADLE_LANE=local scripts/gradle.sh --no-daemon \
  :sdk:publishReleasePublicationToLocalRepository :smoke:assembleRelease
ANDROID_HOME=~/Android/Sdk scripts/check-android-r8.sh
```

The smoke app consumes the local Maven publication and uses R8. The AAR ships the
Core package and JNA keep rules. JNA 5.17.0 is a POM dependency; it is not embedded
in the AAR. The APK must contain one copy of `libjnidispatch.so` for each ABI.

The CI Android job uses a 16 KB API 35 emulator. Local AAR or R8 checks do not prove
that emulator run. The ATAK host and its JNA classloader still need a separate
owner-authorized device check. No device reset is part of a local SDK build.

## Release limits

The SDK remains pre-release. Publication, remote CI and ATAK host qualification
remain release decisions. Local Core commit pins cannot be fetched by remote CI
until the owner publishes those commits. See the
[H5 handoff](reviews/2026-09-26-h5-sdk-completion.md) for the exact source pins
and prior checks.
