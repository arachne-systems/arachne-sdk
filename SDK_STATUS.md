# SDK status

## Current

- The repository has one clean initial commit.
- `arachne-sdk` exposes the typed `arachne-runtime::Client` seam explicitly.
- The Rust smoke test and endpoint example run without Android or ATAK.
- The core is linked at `e410313ec750d4f6521053800361bc180bc94473` for staging.

## Not included

- ATAK, Android, JNI and CoT adapters.
- Feed-specific payload models.
- Relay or hosted-service code.
- Language bindings beyond the native Rust boundary.

## Release gate

Replace the core submodule with a released core package, complete the API and
provenance review, add approved contribution terms and publish the dependency
notice inventory before declaring an SDK release.
