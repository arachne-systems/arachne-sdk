# H5: publication options and optional streaming

## BLUF

The generated SDK now consumes integrated Core `05434f8`. Rust, Kotlin, Swift,
Python and Go checks pass. Addressed Critical and Bulk publication preserve the
recipient set. The opt-in streaming feature passes its SDK check. Generated
sources have no drift. Android AAR, R8 smoke APK and test APK packaging checks
pass. The finished Rust target is removed. SDK lines stay separate.

## Source and changes

- SDK base: `3c750fb33409c217d4581985682979388063df5d` (`feat/uniffi-sdk`).
- Branch: `codex/night-h5-refresh`.
- Worktree: `/home/user/development/worktrees/arachne-sdk-night-h5-refresh`.
- Final Core pin: `05434f86f4297b33620502dc3385f31953ff5889`. It includes
  production H7, its final fixture/receipt changes, and typed options from
  `db571852c98dc62d79d499031ccada1b2fd83867`.
- The root Iroh path patch remains in place. The SDK root also forwards Core's
  exact `moq-net` and `moq-tokio` Git patches at `6ab7d1a223e1949a5c2e10b68e29db2e8ab9bdd1`.
  Cargo does not inherit a dependency workspace's patch table. The lock resolves
  the H6/H7 dependencies, including SFrame 2 with the ring backend.

Core owns `PublicationOptions`, `PublicationMode`, `default_publication_options`
and `Client::stage_protected_publication_with_options`. The SDK re-exports these
items and regenerates Kotlin, Swift, Python and Go. It has no copied option model
or publication logic.

The default is an empty audience with Critical delivery. A named audience uses
current MemberIds, sorted by bytes, with at most 64 entries and no duplicates or
the sender. Bulk selects the existing Bulk queue. Current carries its current-value
metadata and requires an empty audience. Existing methods retain their behavior.

The opt-in SDK Cargo feature `moq` forwards to `arachne-runtime/moq`. Default
builds still return `Unsupported` from stream methods. The Android build script
still builds the default library; a streaming consumer must package a native
library built with the feature. No microphone, codec, floor or channel types are
added. CI checks both the default Rust build and the opt-in feature.

## Evidence

Three RED checks preceded their fixes:

1. The new Rust default test failed with unresolved SDK imports for Core's
   `PublicationMode`, `PublicationOptions` and `default_publication_options`.
   Receipt: `/tmp/h5-refresh-options-red.log`.
2. With an empty SDK `moq` feature, the metrics test failed with Core's
   `Unsupported` error. Forwarding the feature makes it pass.
   Receipts: `/tmp/h5-refresh-moq-red.log` and `/tmp/h5-refresh-moq-green.log`.
3. Android packaging rejected duplicate `--max-workers` arguments. The wrapper
   hardcoded 4 while the caller, including CI, supplied its own limit. The wrapper
   now adds the default only when the caller has no worker option. Both the CI
   `--max-workers=2 --help` check and the full `--max-workers=4` build pass.
   Receipts: `/tmp/h5-refresh-gradle-workers-{red,green}.log`.

| Check on final Core `05434f8` | Result |
| --- | --- |
| SDK library, generator and all-target build | PASS, 44.05 s |
| `cargo test --locked -p arachne-sdk --all-targets --no-run`, then binaries outside lock | PASS, 12.13 s build; 7 harnesses, 4 tests |
| Four Rust example mains | PASS |
| `cargo test --locked -p arachne-sdk --features moq --test client --no-run`, then binary outside lock | PASS, 13.94 s build; 3 tests, 0.22 s |
| Kotlin generated smoke and protocol flow | PASS, 30 checks |
| Swift generated smoke and protocol flow | PASS, 29 checks |
| Installed Python package | PASS, 12 smoke checks, 35 flow checks, storage/management checks |
| Go generated smoke and protocol flow | PASS, 2 tests, 5.13 s |
| Fresh generation compared with tracked sources | PASS, all 15 files byte-identical |
| Transport lock entries compared with Core | PASS, Iroh, Iroh-MoQ, MoQ net and MoQ Tokio |
| Rustfmt, shell syntax, source whitespace | PASS |
| Default Android AAR, R8 smoke APK and Android test APK | PASS, 4m 51s, 115 tasks |
| R8 names and packaged native libraries | PASS, 674 Core classes, 128 JNA classes, both ABIs |
| Native LOAD alignment | PASS, `0x4000` on arm64-v8a and x86_64 |

The language build and run commands are the commands in
`scripts/uniffi-smoke.sh`. They ran in separate steps so compilation used the
shared lock and test processes ran outside it. Generated files retain the UniFFI
template whitespace. The source whitespace check excludes `generated/`; the
byte-for-byte generation check covers those files. Python installation writes
ignored `egg-info` metadata, which is not generated binding source.

Receipts: `/tmp/h5-refresh-{default-build.log,all-targets-build.log,all-targets-results.json}`,
`/tmp/h5-refresh-rust-results.json`, `/tmp/h5-refresh-language-results.json`,
`/tmp/h5-refresh-drift-result.json` and `/tmp/h5-refresh-transport-pins.json`.
The first streaming resolve retained a registry MoQ Tokio version. Selecting the
Core patch with `cargo update --offline -p moq-tokio --precise 0.19.11` removed that
version and its unused newer MoQ graph. The final locked build has no unused-patch
warning and matches the Core lock entries.

The new generated Python flow has three authorized members. Critical and Bulk
publications use one recipient. The receiver checks the bytes and authenticated
recipient list. The interested bystander must receive neither. A final default
group publication reaches both members and proves the bystander is connected.
Kotlin, Swift, Python, Go and Rust also check Core's defaults.

Core's separate [options receipt](../../core/docs/reviews/2026-09-26-typed-publication-options.md)
records 10 passes of all three new three-member tests, the exact scheduler class
check, 15 existing typed tests, and all-feature Clippy. The scheduler check proves
Bulk queue selection. Receipt of bytes through a foreign binding alone cannot
prove that internal queue choice.

## Android artifacts

These artifacts use SDK source `853bacd3d9ae051c7b7341db1ec964b5d4958e79`
and Core `05434f86f4297b33620502dc3385f31953ff5889`. Later edits in this lane
only complete this receipt. Build command:

```sh
GRADLE_LANE=codex-h5-refresh scripts/gradle.sh --no-daemon --max-workers=4 \
  :sdk:publishReleasePublicationToLocalRepository :smoke:assembleRelease \
  :smoke:assembleDebugAndroidTest
```

The shared build lock, CPU affinity and Cargo environment described below wrapped
this command. `scripts/check-android-r8.sh` passed afterward. No package was installed.

| Artifact in this worktree | Bytes | SHA-256 |
| --- | ---: | --- |
| `android/sdk/build/outputs/aar/sdk-release.aar` | 22,920,698 | `729b5d97ae310cc8dee51aea7e47e9d5ba2be7342dd79855199382657fcb5713` |
| `android/smoke/build/outputs/apk/release/smoke-release-unsigned.apk` | 54,224,916 | `3e8c6f412991d4fc495ae009f33be430f23b47ad5e5fec75795bf4c25c4e8de1` |
| `android/smoke/build/outputs/apk/androidTest/debug/smoke-debug-androidTest.apk` | 792,421 | `efbe849d8ff8669f49f1fbd83df9651c0df1cf30a29de9b6f6353f42f3122f47` |

Receipts: `/tmp/h5-refresh-android-build.log`, `/tmp/h5-refresh-r8.log` and
`/tmp/h5-refresh-android-artifacts.json`. The JSON includes both native library
hashes. The AAR uses the default feature set; its streaming calls remain unavailable.

## Limits and owner decisions

- The owner still selects the SDK line. This change does not merge the live
  Kotlin SDK, the PTT SDK or the generated SDK lines.
- ATAK's current bridge and Core pin are unchanged. This API closes its typed
  audience/Bulk gap; it does not prove the ATAK JNA classloader or plugin migration.
- No devices, emulator, WAN or application data upgrade are tested in this lane.
- Native storage migration, remote CI and publication remain separate gates.
- The MoQ check proves feature selection and empty metrics. It is not a stream
  exchange, latency, audio or restart test. Core and mobile receipts remain the
  sources for those claims.

## Resource controls

Cargo uses the shared lock, four jobs, CPUs 8–11, no incremental state and no
dev/test/release debug information. Test binaries run outside the Cargo lock.
No Cargo target is shared with another worktree. After all compilers exited,
`target/` was removed: 5,609,181,184 bytes (5.224 GiB). Packages and generated
sources remain for review. Receipt: `/tmp/h5-refresh-cache-cleanup.json`.

The exact patched Go generator is retained at
`/home/user/.cache/arachne-sdk-tools/uniffi-0.31.2-go-0b7fb4c-patched/uniffi-bindgen-go`.
Set `BINDGEN_GO` to this file for a later local regeneration. Its adjacent
`provenance.json` records upstream commit, UniFFI version, patch hash and binary
hash. Its SHA-256 is
`3d50ec020f691d49b0fc00eae4e4ef06efbbe347bc7a01bb317529ca608387e8`.
The 401,072,972-byte compile cache and temporary source clone were removed; the
6,394,336-byte executable remains outside Cargo targets.
