# Arachne SDK

Typed Rust client for Arachne's secure peer-to-peer workspaces.

The SDK lets native Rust applications open peer endpoints, create or join
workspaces through invitations, apply workspace policy, send and receive
protected application data, and persist workspace state. Payload formats and
user experience stay with the application.

## Status

**Pre-release alpha source; not published to crates.io.** The SDK consumes
[`arachne-core`](https://github.com/arachne-systems/arachne-core) as a pinned
source submodule and does not copy its implementation. Clone recursively to
include the core source.

The SDK source is Apache-2.0. The separate core source remains MPL-2.0; see
[LICENSING.md](LICENSING.md) and [core/LICENSING.md](core/LICENSING.md).
Arachne SDK is independent and is not affiliated with or endorsed by the TAK
Product Center or the U.S. Government.

## Build

Use Rust 1.98.0 and clone with the core submodule:

```sh
git clone --recurse-submodules https://github.com/arachne-systems/arachne-sdk.git
cd arachne-sdk
cargo +1.98.0 test --locked -p arachne-sdk
cargo +1.98.0 check --locked -p arachne-sdk --examples
```

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
