# SDK status

## Current

- `arachne-sdk` exposes the typed `arachne-runtime::Client` seam explicitly,
  including typed join/admission operations, with a smoke test and native
  endpoint example.
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
- The current core tree has focused public guides, but older reachable commits
  and surviving remote branches still contain private development guidance,
  including user-specific paths and publication authorization notes. Keep the
  existing repositories private. A single-root core snapshot is prepared on
  local branch `public-source-candidate` at `83dc95f`; the SDK snapshot is on
  the same-named local branch at `e248efe` and omits this internal status file.
  Fresh local bare remotes and a recursive clone were verified; the clone
  passes the locked SDK all-target compile and package tests (2/2), plus the
  serialized core workspace suite with no failures and its declared ignored
  cases. The candidate currently uses the proposed `arachne-core-source` and
  `arachne-sdk-source` URLs. Before publishing SDK, align its core submodule
  URL and pinned commit with the final core release, then repeat the recursive
  clone and feed binary check. If the SDK destination name changes, also
  update the clone command and crate repository URL.
- An isolated clone of feed HEAD `1b8b264` passes
  `cargo +1.98.0 check --locked --offline --bin arachne-feed` against the SDK
  candidate. The feed worktree and its intentional RED catalog test were left
  untouched.
- The existing GitHub SDK description is stale. Suggested description for the
  fresh SDK repository:
  `Typed Rust SDK for Arachne's secure peer-to-peer workspaces.`
- A crates.io release remains blocked until core provides compatible released
  Cargo packages; its current crates use workspace paths and are unpublished.
- External contributions remain closed until the contributor process and
  terms described in [CONTRIBUTING.md](CONTRIBUTING.md) are approved.
- The SDK checkout's exact Cargo dependency versions are in `Cargo.lock`; the
  core submodule retains its MPL-2.0 and
  [vendored source inventory](core/THIRD_PARTY_NOTICES.md).
