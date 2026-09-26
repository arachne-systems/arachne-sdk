# H5 final transport pin — 2026-09-26

## BLUF

Pin the generated SDK to Core `6c70a6d465ee0e8746ee407600c845ad3aa08893`.
This brings in the admitted-peer retry reset and Android UDP send policy.
Core's exported API and binding metadata are unchanged. Keep all 15 generated
files. The narrow host checks pass; Android was not rebuilt.

## Source and binding boundary

SDK base: `2183cb36fead56380aa53eb762d7d7798429efa6`. Prior Core:
`0d37ba9d73385278aa7baf34a3d831d991a0686e`. Final Core:
`6c70a6d465ee0e8746ee407600c845ad3aa08893`.

The complete diff changes only Node `connections.rs`, Node `streams.rs`, tests
and documents. The production change removes a peer's stale failed-dial delay
after a fresh authenticated and authorized incoming MoQ session. It retains
other peers' delays and all authorization checks. Android now uses individual
UDP sends on every architecture; this extends the prior emulator setting.

The `arachne-api` and `arachne-runtime` crate trees, UniFFI namespace configs,
Core Cargo inputs and vendor files match the prior pin exactly. The SDK's Cargo
inputs and source also stay unchanged. The 15 tracked generated files have the
same SHA-256 values. They were not regenerated. Receipt:
`/tmp/h5-final-source.json`, with `/tmp/h5-final-generated-before.json`.

## Fresh checks

All checks below use final Core `6c70a6d`. Receipt:
`/tmp/h5-final-host-results.json`; totals: `/tmp/h5-final-summary.json`.

| Check | Result |
| --- | --- |
| Default Rust | 4 passed, 0 failed, 0 ignored across 7 harnesses |
| Optional MoQ Rust | 3 passed, 0 failed, 0 ignored |
| Executable examples | Endpoint, join, protected publish and protected receive pass |
| Python | 12 smoke checks, 35 flow checks and storage/management pass |
| Generated source | All 15 tracked file hashes unchanged; no regeneration |
| Historical Android artifacts | All three sizes and SHA-256 values still match |

Cargo compile times were 54.07 s for default tests, 1.49 s for the default
library/examples, and 56.76 s for MoQ tests. Wall receipts also include lock waits.
Build jobs use the shared lock, CPUs 8–11, four jobs, no incremental state and no
debug information. Test binaries run outside the build lock. The target belongs
only to this worktree. No tests or implementation were added.

The fresh default shared library is 115,324,832 bytes, SHA-256
`5111a1ad68f65ca217d7d0f87518b46cc02c3c8d9d61174e095d674f4291a5a0`.
Receipts: `/tmp/h5-final-native-library.json` and
`/tmp/h5-final-preserved-android.json`.

The Python package uses the unchanged generated modules and a newly built
default SDK shared library. Import checks the exported function checksums.
Smoke, flow and storage checks test this rebuilt library. No language generator
or Kotlin, Swift or Go compiler rebuild is needed for this unchanged boundary.

The Core regression is already RED then GREEN in its own receipt. The SDK pin
change does not duplicate that test or introduce a new implementation. Core's
full qualification at this final pin is tracked separately by H7.

## Evidence limits and preserved artifacts

The three optional MoQ SDK tests check feature forwarding, default behavior and
zero initial metrics. They do not exchange a stream. The default SDK and AAR
still have no MoQ support. Android-specific GSO behavior is not compiled or
proved by these host checks.

The [prior four-language flow proof](2026-09-26-h5-deadline-refresh.md) remains
tied to Core `0d37ba9`: Kotlin 30 checks, Swift 29, Python 12 smoke and 35 flow
checks plus storage/management, and Go 2 tests. Only Python is rerun for this pin.

The retained AAR, R8 APK and test APK still use SDK `853bacd` / Core `05434f86`.
Their hashes are in the [Android receipt](2026-09-26-h5-publication-options-refresh.md).
They do not contain this Core update. No Android rebuild, device install, SDK
line merge or mobile store migration took place in this task. The owner still
chooses the SDK line. Root owns current tablet results.
