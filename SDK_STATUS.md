# SDK status

## Current

- `arachne-sdk` exposes the typed `arachne-runtime::Client` seam explicitly,
  with a smoke test and a native endpoint example.
- Core is pinned as a source submodule. The GitHub source release depends on
  that exact checkout; the Rust crate remains `publish = false`.
- The typed client exposes protected publication plus native record storage,
  so durable callers can save the staged snapshot before adoption and restore
  after restart.
- The client can mark a signed service profile; admission and workspace policy
  still control access and publication.

## Not included

- ATAK, Android, JNI and CoT adapters.
- Feed-specific payload models.
- Relay or hosted-service code.
- Language bindings beyond the native Rust boundary.

## Release boundary

- GitHub currently reports both `arachne-sdk` and its pinned `arachne-core`
  repository as private, with no release tags. A public source release must
  make the pinned core commit publicly readable as well.
- The GitHub description still calls this a private future home for bindings
  and examples; update it to the narrower Rust SDK boundary before opening it.
- A crates.io release remains blocked until core provides compatible released
  Cargo packages; its current crates use workspace paths and are unpublished.
- External contributions remain closed until the contributor process and
  terms described in [CONTRIBUTING.md](CONTRIBUTING.md) are approved.
- The SDK checkout's exact Cargo dependency versions are in `Cargo.lock`; the
  core submodule retains its MPL-2.0 and vendored dependency notices.
