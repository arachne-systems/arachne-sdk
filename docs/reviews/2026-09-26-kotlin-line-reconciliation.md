# Kotlin SDK line reconciliation

## Decision

The generated UniFFI SDK at `1a9f6cf` is the selected Kotlin and Android line.
The historical `feat/kotlin-sdk` tip `996806f` is recorded as reconciled ancestry
without importing its files.

## Comparison

| Boundary | Selected generated line | Historical handwritten line |
| --- | --- | --- |
| Kotlin API | Generated from Core metadata in `generated/kotlin` | Copied `Client`, models and native bridge |
| Native entry point | One `libarachne_sdk` with Core-owned typed objects | Separate handwritten Rust FFI surface |
| Android AAR | Generated sources, Maven publication and JNA POM dependency | Handwritten sources and file-based AAR consumption |
| Shrinking | R8 keep check inspects generated UniFFI and JNA classes | Basic keep rules only |
| Native packaging | arm64 and x86_64, stripped, 16 KB LOAD alignment checked | arm64 and x86_64 build without the alignment gate |
| CI dependencies | GitHub Actions pinned to commit SHAs | Mutable major-version tags |
| Serialization | Typed UniFFI values | Jackson and raw JSON escape paths |

The current Android implementation already provides the useful capability from
the older branch through `android/`, `scripts/build-android-libs.sh`,
`scripts/check-android-r8.sh` and the `generated-android` CI job. Importing the
older implementation would restore a second contract and duplicate security
checks that Core now owns.

## Remaining evidence

The generated JVM and Android packaging have existing host build, smoke and R8
receipts. A physical arm64 device and the ATAK host still require qualification
at the exact release commit. Publication and remote branch changes remain
separate owner actions.
