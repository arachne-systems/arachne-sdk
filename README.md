# Arachne SDK

Adopter-facing Rust APIs for the portable Arachne fabric.

## Status

**Pre-release alpha source — not published to crates.io.**

This repository contains the first deliberate SDK boundary: an explicit,
typed facade over `arachne-runtime`. The facade keeps application payloads
opaque and does not require ATAK, Android, JNI, CoT translation, or an official
ATAK plugin.

Arachne SDK is an independent project and is not affiliated with or endorsed
by the TAK Product Center or the U.S. Government.

The `core/` directory pins [`arachne-core`](https://github.com/arachne-systems/arachne-core)
as a source dependency; this repository does not copy its implementation. A
public source checkout requires access to that pinned core repository. The SDK
crate stays `publish = false` until core has a released Cargo package.

## Boundary

- `arachne-sdk` exposes the typed client, join and admission flow, workspace,
  invitation, connectivity, publication and native-storage operations needed
  by native adopters.
- Core implementation remains in `arachne-core` and retains its MPL-2.0 terms.
- ATAK/Android adapters, feed-specific payloads, relay services and platform
  bindings are outside this initial extraction.
- An admitted service endpoint calls `Client::use_service_profile` after each
  open or restore; its stable endpoint credential is not an API key and does
  not bypass workspace policy.
- For durable workspaces, enable record storage, save the exact staged snapshot
  before adoption, and use `restore_record_storage` after restart.

## Build and test

Once the SDK and pinned core source are public, clone both and run the
SDK-scoped checks:

```sh
git clone --recurse-submodules https://github.com/arachne-systems/arachne-sdk.git
cd arachne-sdk
cargo +1.98.0 test --locked --offline -p arachne-sdk
cargo +1.98.0 check --locked --offline -p arachne-sdk --examples
```

The example opens a native endpoint without an APK or ATAK installation:

```sh
cargo +1.98.0 run --example endpoint
```

## Licensing

Arachne-owned SDK source is licensed under [Apache-2.0](LICENSE). The boundary
does not relicense the separately consumed core or any third-party dependency;
see [LICENSING.md](LICENSING.md).
