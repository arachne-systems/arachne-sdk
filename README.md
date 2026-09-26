# Arachne SDK

**BLUF:** The SDK exposes Arachne's secure peer-to-peer workspaces through one
typed Core API, with generated Kotlin, Go, Python and Swift bindings.

Use it to open peer endpoints, create or join workspaces through invitations,
apply workspace policy, send and receive protected application data, and
persist workspace state. Payload formats and user experience stay with the
application.

## Status

**Pre-release alpha source.** The Rust crate is not published to crates.io.
The language bindings use UniFFI metadata owned by Core. Build the native library
for your target before using them; this repository does not distribute prebuilt
release libraries. Android packaging builds an AAR from the same generated API.

The SDK source is Apache-2.0. The separate core source remains MPL-2.0; see
[LICENSING.md](LICENSING.md) and [core/LICENSING.md](core/LICENSING.md).
Arachne SDK is independent and is not affiliated with or endorsed by the TAK
Product Center or the U.S. Government.

## Build the native library

Clone with the Core submodule and build the SDK library:

```sh
git clone --recurse-submodules https://github.com/arachne-systems/arachne-sdk.git
cd arachne-sdk
rustup toolchain install 1.98.0 --profile minimal
CARGO_INCREMENTAL=0 CARGO_PROFILE_DEV_DEBUG=0 CARGO_PROFILE_TEST_DEBUG=0 \
  cargo +1.98.0 build --locked -p arachne-sdk
```

The library is written to `target/debug` (`libarachne_sdk.so` on Linux,
`libarachne_sdk.dylib` on macOS). Build it for the same operating system and
architecture as the language application. Linux x86_64 is the currently
verified target; other targets are not release-qualified yet.

### Optional streaming

The `moq` Cargo feature enables Core's general protected stream transport:

```sh
CARGO_INCREMENTAL=0 CARGO_PROFILE_DEV_DEBUG=0 CARGO_PROFILE_TEST_DEBUG=0 \
  cargo +1.98.0 build --locked -p arachne-sdk --features moq
```

The default library and Android build leave this feature off. Their stream methods
return `Unsupported`. The same generated API works with either library; a host
that needs streaming must package a native library built with `moq`. This feature
adds no microphone, codec, floor or channel model to the SDK.

## Language bindings

| Language | Package | Quick start |
| --- | --- | --- |
| Rust | `arachne-sdk` crate | `cargo check -p arachne-sdk --examples` |
| Kotlin | [`android/`](android) AAR | Generated `org.arachne.core.api` and `org.arachne.core.runtime`; JNA is a POM dependency. |
| Go | [`generated/go`](generated/go) | Import its `arachne_api` and `arachne_runtime` packages; requires cgo and the native library. |
| Python | [`generated/python`](generated/python) | Install the root package with the matching native library; import `arachne_generated`. |
| Swift | `ArachneSDK` SwiftPM product | Import `ArachneSDK`; build the native library separately. |

See [language bindings](docs/language-bindings.md) for setup and language
examples, and the [workflow guide](docs/workflows.md) for persistence rules.

## Examples

Run an example from the repository root with `cargo +1.98.0 run --example <name>`:

- `endpoint` opens a direct endpoint and creates a workspace.
- `join_flow` walks through invitation and admission with two clients in one
  process. It passes admission material in memory and uses fixed demo-only
  credentials and in-memory storage.
- `protected_publish` stages and adopts a protected publication under
  workspace-derived policy. It has no receiving peer.
- `protected_receive` sends and receives a protected publication between two
  direct peers. Its fixed credentials are for the local demo only; invitation
  and admission material stay in process.

Any fixed credentials in these samples are demo values; replace them with
private, unique random credentials in an application.

See the [workflow guide](docs/workflows.md) for credentials, service startup,
admission, current-value publication, persistence ordering, and example limits.
Client calls are synchronous; run them on a blocking worker rather than an
async executor or UI thread.
