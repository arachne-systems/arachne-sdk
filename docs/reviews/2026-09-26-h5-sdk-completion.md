# H5 SDK completion

## BLUF

The SDK uses Core's typed API and UniFFI metadata. Rust, Kotlin, Swift, installed
Python and Go checks pass on integrated Core `e420a52`. The generated files have
no drift. The Android AAR, R8 smoke APK and Android test APK build, and the
packaging checks pass. Device and release gates remain with the owner.

## Source and ownership

- SDK branch: `codex/night-h5-bindings`, from `8a546d4720c6ec1f13b9fc97a70551b527f8e316`.
- Core pin: `e420a5279487da6bb09d7c0ab9add8fd36034afa`. This includes H1, H2, H3,
  H4 and the restart transport fix. The SDK root patch table selects
  `core/vendor/iroh`; Cargo does not inherit Core's patch table.
- Worktree: `/home/user/development/worktrees/arachne-sdk-night-h5`.
- No SDK lines are merged. The live `feat/kotlin-sdk` tree is unchanged. The lead
  agent owns integration. No code is pushed or published.

Core owns the Client contract, records, errors, candidate checks and durable
adoption. The SDK links Core scaffolding and packages the language outputs. It
has no copied Client implementation, candidate map or storage commit logic.

## Changes

The generated API has 95 public Client methods, plus Context, StorageConfig,
opaque candidates, admission grants, member updates and resource objects. It
includes storage, management, leave, nearby, typed drivers, current/direct/range
recovery and protected resources. H1's pending object fields include authenticated
`epoch`, `from_losing_branch` and `MemberId` recipients.

The old SDK JSON C ABI, C header, hand Go/Python/Swift clients, SDK UniFFI mirrors,
SHIM markers and disabled hand-binding CI steps are removed. Core's dispatcher
remains a separate migration item for existing native app callers.

| Language | Package choice |
| --- | --- |
| Kotlin | Core packages `org.arachne.core.api` and `org.arachne.core.runtime` |
| Swift | One `ArachneSDK` SwiftPM module, with both generated Core components and separate C FFI modules |
| Python | Root `pyproject.toml`; installable `arachne_generated` package; native library beside its generated modules |
| Go | `generated/go/arachne_api` and `generated/go/arachne_runtime`, with the SDK module prefix |

The native library stays `arachne_sdk`. Core's configuration names the Kotlin
native close operation `shutdown`. Kotlin `close()` releases the foreign handle.
The SDK config sets the Go package prefix. Generated files are not edited by hand.

The AAR ships both Core packages and their R8 rules. JNA remains a POM dependency.
`scripts/gradle.sh` provides a separate Gradle lane. All four language flows now
use owned contexts and Core storage. Rust examples use in-memory storage.

## RED and final evidence

The baseline was SDK `8a546d4` / Core `2205887`: the all-target build passed in
67 s, with 7 SDK unit tests and 2 client tests passing. The new Python check then
failed with `ImportError: cannot import name 'Context'`. It passes with the Core
metadata. A second check failed on `8ce6498` because a recovered publication had
no `epoch`. It now passes for recovery and live receive on `e420a52`.

| Final check | Result on Core `e420a52` |
| --- | --- |
| SDK library, generator and all-target build | PASS, 20.46 s |
| `cargo test --all-targets --no-run`, then test binaries | PASS, build 12.17 s; 6 binaries, 2 tests |
| Four Rust example mains | PASS, including direct peer receive |
| Kotlin generated smoke and protocol flow | PASS |
| SwiftPM package, generated smoke and protocol flow | PASS |
| Installed Python package, protocol flow and storage/management checks | PASS |
| Go generated smoke and protocol flow | PASS, 2 tests, 5.054 s |
| Regeneration | PASS, all 15 generated files byte-identical |
| SDK rustfmt, shell syntax, source whitespace | PASS |
| AAR, R8 smoke APK and Android test APK | PASS, 2m 19s |
| R8 class names and both native ABIs | PASS, 664 Core classes and 128 JNA classes |
| Native 16 KB LOAD alignment | PASS, `0x4000` for both SDK libraries |

The flows cover join, publication, recovery, inbox acknowledgement/rejection,
candidate misuse, waits and close. Python also checks context isolation, durable
reopen, typed workspace progress, rename/discard, nearby Direct results, restored
removal, and H1's authenticated inbox metadata. Nearby results on Direct do not
prove LAN discovery.

Receipts are `/tmp/h5-storage-red.log`, `/tmp/h5-inbox-metadata-red.log`,
`/tmp/h5-final-rust-build.log`, `/tmp/h5-final-all-targets-results.json`,
`/tmp/h5-final-rust-results.json`, `/tmp/h5-final-languages.log`,
`/tmp/h5-final-drift-result.json`, `/tmp/h5-final-android-build.log`,
`/tmp/h5-final-r8.log` and `/tmp/h5-final-android-artifacts.json`.
The initial Swift package used separate modules and failed to import Core types;
the single-module package above passes. A private Core clone received incidental
rustfmt changes; they were removed before the final pin and checks. That clone is
clean.

Tests used CPUs 8–11. Cargo builds used the shared lock, four jobs, no incremental
state and no dev/test debug information. Android release debug information was
also disabled. The finished Go generator build cache was removed (382.5 MiB).
The final Cargo target cleanup runs after this commit; the AAR and APK files stay
in `android/` for review. The AAR is `android/sdk/build/outputs/aar/sdk-release.aar`
(22,819,990 bytes; SHA-256
`e9796645ad0aa4174c8dceee717fd57edcf5ade70ad9fb8a6826e4dde0964501`).

## Owner decisions and limits

1. Select the SDK line and the merge order with the live Kotlin branch. Generated
   namespaces and candidate APIs change existing consumers; no compatibility
   client is added.
2. Retain the PTT SDK line's `moq` feature forwarding when integrating the SDK
   lines. This UniFFI base has no forwarding feature, so generated streaming calls
   return `Unsupported` in its default build. No second media implementation is
   added here.
3. Prove the native storage upgrade before deployment to existing app data. The
   new Core format differs from the deployed mobile format. The authenticated
   conversion must preserve identity, authority, roots, anchors, counters and
   retained/inbox records, including recovery after an interrupted upgrade.
4. Run the ATAK JNA/classloader and 16 KB emulator checks. This work built the
   Android test APK but used no tablet or emulator and cleared no app data.
5. Publish the local Core pin before remote CI can fetch it. CI and release
   publication are not proved by local checks. Native Python wheels and prebuilt
   host libraries also remain release packaging work.
