# SDK status

## Current

- `arachne-sdk` is a pre-release Rust source SDK; its crate remains
  `publish = false`.
- The GitHub repository description is now:
  `Typed Rust SDK for Arachne's secure peer-to-peer workspaces.`
- The README, workflow guide, and examples cover endpoint setup, invitation and
  admission, protected publication and reception, and local record storage.
- The local `core` submodule pin is `fa6ef24`, based on public core `main`
  `feaed56`. It adds the typed protected-receive and storage methods required
  by the SDK, but those three core commits are not on the public remote yet.

## Verified locally

- `cargo +1.98.0 test --locked --offline -p arachne-sdk` passes (2 integration
  tests).
- All SDK examples compile and run, including protected receive over a local
  direct peer connection.
- The feed binary builds against this SDK in a disposable checkout after
  dropping two stale `iroh-blobs` and `iroh-gossip` root patches. The feed
  worktree's uncommitted README and status edits were left untouched; its
  manifest still needs that follow-on adjustment.

## Public-source release gate

- Merge or publish the three local core API commits, then pin the SDK to the
  reachable public core commit and repeat the recursive-clone and feed checks.
- Prepare the SDK's public branch as a parentless source snapshot and omit this
  internal status file.
- Keep the SDK crate unpublished until the source and package release plans are
  aligned.
