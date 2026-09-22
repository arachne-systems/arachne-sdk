# SDK status

## Current

- `arachne-sdk` is a pre-release Rust source SDK; its crate remains
  `publish = false`.
- The GitHub repository description is `Typed Rust SDK for Arachne's secure
  peer-to-peer workspaces.` The repository remains private.
- The README and workflow guide cover client setup, admission, durable joins,
  protected send/receive, current-value publication, and example limits.
- The SDK's local `core` pin is `ca0fca4`, based on public core `main`
  `21f071a`. One local core API commit is not published yet.

## Verified locally

- `cargo +1.98.0 test --locked --offline -p arachne-sdk` passes (2 integration
  tests).
- `cargo +1.98.0 check --locked --offline -p arachne-sdk --examples` and the
  SDK package format check pass. All four examples run; protected receive
  completes between two direct peers.
- A disposable copy of the current feed worktree builds with the SDK after
  migrating its join call to `begin_join_with_peers` and removing the two
  obsolete `iroh-gossip` and `iroh-blobs` root patches. The live feed worktree
  was left untouched; it still needs those follow-on edits.

## Public-source release gate

- Publish the six core API commits, then pin the SDK to the reachable core
  commit and repeat the recursive-clone and feed checks.
- The local `public-release-candidate` branch is a parentless source snapshot
  and omits this internal status file.
- Keep the SDK crate unpublished until its separate package release plan is
  ready.
