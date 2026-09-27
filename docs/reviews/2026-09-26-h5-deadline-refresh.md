# H5: foreground deadlines and stream receive counters

## BLUF

Host qualification passes on Core `0d37ba9`. The SDK exposes the three new stream
receive counters through Core-owned bindings. The update also includes isolated
foreground deadlines and bounded independent MoQ group reads. The existing
Android artifacts remain on Core `05434f86`; they were not rebuilt or installed.
The current tablet stream stall remains a separate open defect.

## Source and contract

- SDK base: `0d857b709cae745110c42b215369a3df5feeca68`.
- Branch: `codex/night-h5-refresh`.
- Final Core pin: `0d37ba9d73385278aa7baf34a3d831d991a0686e`.
- RED baseline pin: `2b56892278385be25a8ed8768d2dcec5a4ff6c80`.
- Core mobile deadline fix: `ddb3f697df24c8ef2dc4612cd7c126e865329866`.
- The root Iroh path patch and exact MoQ patches remain unchanged.
- Iroh is `1.2.0`; iroh-moq is `bd5afc4`; moq-net and moq-tokio are `6ab7d1a`.
  UniFFI remains `0.31.2`. The lockfile has no change.

Core-owned `StreamMetrics` adds three `u64` fields: `groups_received`,
`frames_received` and `groups_completed`. A fresh opted-in node returns zero for
each field. The SDK adds no copied record or runtime logic. Four generated
runtime source files change. API component files and FFI headers stay identical.
The default native build returns Unsupported for stream metrics. The opt-in
Rust check proves feature selection and empty metrics; it does not exchange a
stream.

Core removes the detached operation timer that wrote to the node-wide cancel
watch. Foreground waits now own their timeout. Explicit cancel and close keep
the shared watch. Background requests keep their own limits. The
[deadline receipt](../../core/docs/evidence/foreground-deadline-isolation-2026-09-26.md)
contains the deterministic RED and GREEN results. An independent read-only review
found no blocking issue. The session mutex remains a limit: a lock wait can
exceed the operation deadline; expired work is rejected after lock acquisition.

The same pin includes bounded, independent MoQ group reads. A partial body or
missing EOF cannot hold all later groups. Core keeps at most eight readers, each
with the existing five-second live window. See the
[group read receipt](../../core/docs/evidence/moq-group-isolation-2026-09-26.md).
That host defect has a different trace from the current tablet stall. The
[restart qualification](../../core/docs/reviews/2026-09-26-stream-restart-host-qualification.md)
does not claim to fix that stall. No storage, wire format or dependency change
was made in this refresh.

## RED and GREEN

RED is confirmed against Core `2b568922`. The SDK opt-in metrics test failed with
exactly three `E0609` errors: the receive counter fields did not exist at that
pin. The cold compile took 77.03 seconds. After the pin update, the same test
compiled in 11.05 seconds and all three cases passed. Shared-lock wait is excluded
from that GREEN compiler time.

```text
cargo +1.98.0 test --locked -p arachne-sdk --features moq --test client --no-run --message-format=json
```

Receipts: `/tmp/h5-deadline-metrics-red.{jsonl,log}`,
`/tmp/h5-deadline-metrics-green.{jsonl,log}`, and
`/tmp/h5-deadline-metrics-green-tests.json`.

## Host qualification

All checks below use the final Core pin and the regenerated binding sources.

| Gate | Result |
| --- | --- |
| Default Rust tests | 4 passed, 0 failed, across 7 harnesses |
| Default Rust example programs | 4 passed: endpoint, join, protected publish and receive |
| Opt-in Rust MoQ tests | 3 passed, 0 failed; includes all three new zero counters |
| Kotlin | 30 checks passed, including the workspace flow |
| Swift | 29 checks passed, including the workspace flow |
| Python | 12 smoke and 35 flow checks passed; storage and management passed |
| Go | 2 tests passed: smoke and workspace flow |
| Fresh binding generation | All 15 tracked files match; no drift |

Rust formatting and handwritten-file whitespace checks pass. Full
`git diff --check` reports nine trailing-space lines in the new Kotlin and Swift
records. These are emitted by UniFFI 0.31.2. The generated files keep the exact
generator output; no hand edit masks that style warning.

The Python three-member flow proves addressed Critical and Bulk delivery with
bystander exclusion. It also checks the default workspace audience. The full Core
suite is a separate H7 lane; its final-pin run was still active at this checkpoint.

Build and generation commands:

```text
cargo +1.98.0 build --locked -p arachne-sdk -p uniffi-bindgen
cargo +1.98.0 test --locked -p arachne-sdk --all-targets --no-run --message-format=json
cargo +1.98.0 build --locked -p arachne-sdk --examples
NO_BUILD=1 BINDGEN_GO=<preserved-generator> scripts/generate-bindings.sh
```

Each foreign language uses the build and test commands in
[scripts/uniffi-smoke.sh](../../scripts/uniffi-smoke.sh). The commands were split
so compilation holds the shared lock and test execution does not. Each test has
a 120-second limit. The drift check generates into a separate directory and
compares all 15 tracked files byte for byte.

Detailed receipts are `/tmp/h5-deadline-default-build-results.json`,
`/tmp/h5-deadline-default-test-results.json`,
`/tmp/h5-deadline-example-results.json`,
`/tmp/h5-deadline-language-results.json`, and
`/tmp/h5-deadline-drift-result.json`.

## Android provenance and remaining gates

The preserved AAR, R8 smoke APK and test APK use SDK source
`853bacd3d9ae051c7b7341db1ec964b5d4958e79` and Core
`05434f86f4297b33620502dc3385f31953ff5889`. All three SHA-256 values still match the
[previous H5 receipt](2026-09-26-h5-publication-options-refresh.md#android-artifacts).
The new check is `/tmp/h5-deadline-preserved-android.json`. Those artifacts contain
their original bindings and native library, not this refresh. The default AAR
has no MoQ support. A streaming consumer needs an opted-in native build.

No Android build, install, tablet check, authenticated storage migration or ATAK
host proof is part of this refresh. The owner still selects the SDK line. The
PTT, generated and hand Kotlin lines remain separate. A mobile migration must
package the selected native library and matching bindings, then pass store,
restart, streaming, R8 and device checks together. The recording catalog and
Blobs archive remain unwired.

## Resource controls

Cargo compilation uses the shared lock, four jobs, CPUs 8–11, no incremental cache
and no debug symbols. Test binaries run outside the lock on CPUs 8–11. This
worktree owns its target. The exact patched Go generator was reused from
`~/.cache/arachne-sdk-tools/`; its SHA-256 is
`3d50ec020f691d49b0fc00eae4e4ef06efbbe347bc7a01bb317529ca608387e8`.
The native generator build passed in 35.71 seconds. Only the owned target is
removed after the checks and commit. Package artifacts, generated source and
receipts are kept.
