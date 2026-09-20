# Arachne SDK

Adopter-facing Rust APIs for the portable Arachne fabric.

## Status

**Pre-release alpha extraction — not yet published.**

This repository contains the first deliberate SDK boundary: an explicit,
typed facade over `arachne-runtime`. The facade keeps application payloads
opaque and does not require ATAK, Android, JNI, CoT translation, or an official
ATAK plugin.

The `core/` directory is a pinned staging link to
[`arachne-core`](https://github.com/arachne-systems/arachne-core), not a copy of
its implementation. It will be replaced by a released core dependency before
the SDK is published.

## Boundary

- `arachne-sdk` exposes the typed client, workspace, invitation, connectivity
  and opaque-publication projections needed by native adopters.
- Core implementation remains in `arachne-core` and retains its MPL-2.0 terms.
- ATAK/Android adapters, feed-specific payloads, relay services and platform
  bindings are outside this initial extraction.

## Build and test

Clone the staging dependency and run the pinned Rust checks:

```sh
git clone --recurse-submodules https://github.com/arachne-systems/arachne-sdk.git
cd arachne-sdk
cargo +1.98.0 test --locked --offline --workspace
cargo +1.98.0 check --locked --offline --workspace --examples
```

The example opens a native endpoint without an APK or ATAK installation:

```sh
cargo +1.98.0 run --example endpoint
```

## Licensing

Arachne-owned SDK source is licensed under [Apache-2.0](LICENSE). The boundary
does not relicense the separately consumed core or any third-party dependency;
see [LICENSING.md](LICENSING.md).
