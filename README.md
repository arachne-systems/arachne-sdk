# Arachne SDK

Rust SDK with Go, Python, and Swift bindings for Arachne's secure peer-to-peer
workspaces.

Use it to open peer endpoints, create or join workspaces through invitations,
apply workspace policy, send and receive protected application data, and
persist workspace state. Payload formats and user experience stay with the
application.

## Status

**Pre-release alpha source.** The Rust crate is not published to crates.io.
Go, Python, and Swift call the same Rust runtime through a small C ABI. Build
the native library for your target before using those bindings; this repository
does not distribute prebuilt libraries.

The SDK source is Apache-2.0. The separate core source remains MPL-2.0; see
[LICENSING.md](LICENSING.md) and [core/LICENSING.md](core/LICENSING.md).
Arachne SDK is independent and is not affiliated with or endorsed by the TAK
Product Center or the U.S. Government.

## Build the native library

Clone with the Core submodule and build the SDK library:

```sh
git clone --recurse-submodules https://github.com/arachne-systems/arachne-sdk.git
cd arachne-sdk
cargo +1.98.0 build --locked -p arachne-sdk
```

The library is written to `target/debug` (`libarachne_sdk.so` on Linux,
`libarachne_sdk.dylib` on macOS). Build it for the same operating system and
architecture as the language application. Linux x86_64 is the currently
verified target; other targets are not release-qualified yet.

## Language bindings

| Language | Package | Quick start |
| --- | --- | --- |
| Rust | `arachne-sdk` crate | `cargo check -p arachne-sdk --examples` |
| Go | [`bindings/go`](bindings/go) | Import `github.com/arachne-systems/arachne-sdk/bindings/go`; requires cgo and the native library. |
| Python | [`bindings/python`](bindings/python) | Install the local package, set `ARACHNE_SDK_LIBRARY`, then use its typed `Client`. |
| Swift | `ArachneSDK` SwiftPM product | Add this repository as a package dependency; build the native library separately. |

See [language bindings](docs/language-bindings.md) for setup and language
examples, and the [workflow guide](docs/workflows.md) for persistence rules.

## Examples

Run an example from the repository root with `cargo +1.98.0 run --example <name>`:

- `endpoint` opens a direct endpoint and creates a workspace.
- `join_flow` walks through invitation and admission with two clients in one
  process. It passes admission material in memory and uses fixed demo-only
  credentials.
- `protected_publish` stages and adopts a protected publication under
  workspace-derived policy. It has no receiving peer.
- `protected_receive` sends and receives a protected publication between two
  direct peers. Its fixed credentials are for the local demo only; invitation
  and admission material stay in process, and the workspaces are non-durable.

Any fixed credentials in these samples are demo values; replace them with
private, unique random credentials in an application.

See the [workflow guide](docs/workflows.md) for credentials, service startup,
admission, current-value publication, persistence ordering, and example limits.
Client calls are synchronous; run them on a blocking worker rather than an
async executor or UI thread.
