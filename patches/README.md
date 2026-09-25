# Vendored patches

## `uniffi-bindgen-go-enum-discr.patch`

| Item | Value |
| --- | --- |
| Applies to | `uniffi-bindgen-go`, <https://github.com/NordSecurity/uniffi-bindgen-go> |
| Tag | `v0.7.1+v0.31.0` (2026-04-16), the only release for UniFFI 0.31 |
| Tag commit | `0b7fb4ceef12021bd7f790cc516fa9133e001813` |
| File changed | `bindgen/templates/EnumTemplate.go` |
| Patch sha256 | `d38e66218d3fbbaad9586c2a4e88d336bb386d8cf66535f130b9af5ef229eb9f` |
| Origin | arachne-core branch `spike/a1-uniffi`, `crates/arachne-uniffi-spike/bindgen-go-enum-discr.patch` (see core `docs/reviews/spike-a1-uniffi.md`, finding 2) |
| Upstream status | Not sent. An upstream PR needs owner approval (public GitHub). |

Why: for a flat enum with explicit discriminants (our `ErrorCode`, `#[repr(u32)]`
with values 1, 2, 3, 100, ...) the stock generator makes the Go constants from the
discriminants but reads and writes the value itself on the wire. The wire format
carries the 1-based variant index. So Rust `InvalidId` (101) arrives in Go as
`ErrorCode(5)`, and a Go constant sent to Rust fails. The patch maps index to
constant and back for enums with discriminants. Enums without discriminants do not
change.

`scripts/build-uniffi-bindgen-go.sh` clones the tag, checks the commit, applies the
patch (dry run first), moves the crates.io `uniffi` family to 0.31.2 (the
scaffolding version), and installs with `--locked`. Pass `--stock` to build the
unpatched generator and see the bug (`tests/uniffi/go` then fails).

Drop the patch when an upstream release fixes the bug, or when we move to a UniFFI
version with a new Go generator.
