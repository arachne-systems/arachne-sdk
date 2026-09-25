#!/usr/bin/env bash
# Build the Go generator for the UniFFI bindings (ADR A1/A4 step 8).
#
# uniffi-bindgen-go v0.7.1+v0.31.0 is the only release for UniFFI 0.31. It has
# one bug that breaks our ErrorCode (enums with explicit discriminants go on
# the wire by value, not by variant index). patches/uniffi-bindgen-go-enum-discr.patch
# fixes it; see patches/README.md for provenance.
#
# Usage: scripts/build-uniffi-bindgen-go.sh [--stock] [TOOLS_DIR]
#   TOOLS_DIR  install root (default: target/tools). The binary lands in
#              TOOLS_DIR/bin/uniffi-bindgen-go (TOOLS_DIR/stock/bin with --stock).
#   --stock    build the unpatched generator (only to show the bug).
# Env: CARGO (default cargo). Set RUSTUP_TOOLCHAIN to pick the toolchain.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
REPO="https://github.com/NordSecurity/uniffi-bindgen-go"
TAG="v0.7.1+v0.31.0"
COMMIT="0b7fb4ceef12021bd7f790cc516fa9133e001813"
UNIFFI_VERSION="0.31.2"
PATCH="$ROOT/patches/uniffi-bindgen-go-enum-discr.patch"
CARGO="${CARGO:-cargo}"

stock=0
if [[ "${1:-}" == "--stock" ]]; then stock=1; shift; fi
TOOLS="${1:-$ROOT/target/tools}"
if (( stock )); then TOOLS="$TOOLS/stock"; fi
SRC="$TOOLS/src/uniffi-bindgen-go"

rm -rf "$SRC"
mkdir -p "$TOOLS/src"
git clone -q --depth 1 --branch "$TAG" "$REPO" "$SRC"
actual="$(git -C "$SRC" rev-parse HEAD)"
if [[ "$actual" != "$COMMIT" ]]; then
    echo "tag $TAG is $actual, expected $COMMIT" >&2
    exit 1
fi

cd "$SRC"
# The tag pins Rust 1.87; its current dependencies need 1.88 or later.
rm -f rust-toolchain rust-toolchain.toml
if (( ! stock )); then
    patch -p1 --dry-run --quiet < "$PATCH"
    patch -p1 --quiet < "$PATCH"
fi
# The tag's lock file has the uniffi 0.31.0 family twice: from crates.io (the
# generator and its fixtures) and from a uniffi-rs git tag (test fixtures).
# Move the crates.io family to the scaffolding version (=0.31.2); updating
# `uniffi` moves uniffi_bindgen, uniffi_meta and the rest with it.
"$CARGO" update -q -p "registry+https://github.com/rust-lang/crates.io-index#uniffi@0.31.0" \
    --precise "$UNIFFI_VERSION"
if ! grep -A1 '^name = "uniffi_bindgen"$' Cargo.lock | grep -q "version = \"$UNIFFI_VERSION\""; then
    echo "uniffi_bindgen is not at $UNIFFI_VERSION in Cargo.lock" >&2
    exit 1
fi
"$CARGO" install -q --locked --path bindgen --root "$TOOLS" --target-dir "$TOOLS/target"
echo "built $TOOLS/bin/uniffi-bindgen-go ($TAG, $( ((stock)) && echo stock || echo patched ))"
