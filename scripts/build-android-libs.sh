#!/usr/bin/env bash
# Build libarachne_sdk.so for Android (arm64-v8a, x86_64), release, stripped,
# 16 KB page aligned (.cargo/config.toml), into a jniLibs tree.
#
# Usage: scripts/build-android-libs.sh [OUT_DIR]   (default: target/android/jniLibs)
# Needs: cargo-ndk 4.1.2, the Android NDK (ANDROID_NDK_HOME, or ANDROID_HOME/ndk/27.1.12297006),
#        rustup targets aarch64-linux-android and x86_64-linux-android.
# Env: CARGO (default: cargo); set RUSTUP_TOOLCHAIN to pick the toolchain.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${1:-$ROOT/target/android/jniLibs}"
CARGO="${CARGO:-cargo}"
if [[ -z "${ANDROID_NDK_HOME:-}" && -n "${ANDROID_HOME:-}" ]]; then
    export ANDROID_NDK_HOME="$ANDROID_HOME/ndk/27.1.12297006"
fi

cd "$ROOT"
rm -rf "$OUT"
mkdir -p "$OUT"
CARGO_PROFILE_RELEASE_STRIP=symbols "$CARGO" ndk -t arm64-v8a -t x86_64 -P 26 -o "$OUT" \
    build --locked --release -p arachne-sdk

# cargo-ndk copies every cdylib in the build (iroh's too). The SDK library
# links them statically (NEEDED is only libc, libm, libdl), so ship one .so.
find "$OUT" -name '*.so' ! -name libarachne_sdk.so -delete

# Every LOAD segment must be 16 KB (0x4000) aligned for 16 KB page devices.
READELF="$(ls "$ANDROID_NDK_HOME"/toolchains/llvm/prebuilt/*/bin/llvm-readelf 2>/dev/null | head -1)"
READELF="${READELF:-readelf}"
for lib in "$OUT"/*/libarachne_sdk.so; do
    aligns="$("$READELF" -lW "$lib" | awk '$1 == "LOAD" { print $NF }' | sort -u)"
    if [[ "$aligns" != "0x4000" ]]; then
        echo "$lib: LOAD alignment $aligns, want 0x4000" >&2
        exit 1
    fi
    echo "$lib: LOAD alignment 0x4000"
done
