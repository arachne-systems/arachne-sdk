#!/usr/bin/env bash
# Check that R8 kept the generated UniFFI binding and JNA in the minified
# smoke app, under their own names (JNA binds them by name at run time).
# Run after `scripts/gradle.sh :smoke:assembleRelease`.
# Env: ANDROID_HOME (for build-tools dexdump).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/android/smoke/build/outputs"
APK="$OUT/apk/release/smoke-release-unsigned.apk"
MAPPING="$OUT/mapping/release/mapping.txt"
DEXDUMP="$(ls "${ANDROID_HOME:?set ANDROID_HOME}"/build-tools/*/dexdump | sort -V | tail -1)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

unzip -q "$APK" -d "$WORK"
for dex in "$WORK"/classes*.dex; do "$DEXDUMP" "$dex" 2>/dev/null; done |
    sed -n "s/^  Class descriptor  : 'L\(.*\);'/\1/p" | tr / . > "$WORK/classes.txt"

status=0
for class in org.arachne.core.runtime.Client org.arachne.core.runtime.Context \
    org.arachne.core.runtime.StorageConfig org.arachne.core.runtime.UniffiLib \
    org.arachne.core.api.UniffiLib org.arachne.core.api.ApiException \
    com.sun.jna.Native com.sun.jna.Structure com.sun.jna.Pointer; do
    if ! grep -qx "$class" "$WORK/classes.txt"; then
        echo "missing after R8: $class"; status=1
    elif grep -q "^$class -> " "$MAPPING" && ! grep -q "^$class -> $class:" "$MAPPING"; then
        echo "renamed by R8: $class"; status=1
    else
        echo "kept: $class"
    fi
done
renamed="$(grep -E '^(org\.arachne\.core\.(api|runtime)|com\.sun\.jna)\.' "$MAPPING" |
    grep -v '$$ExternalSynthetic' | awk '{ if ($1 ":" != $3) print $1 }' | head -5)"
if [[ -n "$renamed" ]]; then echo "renamed by R8: $renamed"; status=1; fi
for abi in arm64-v8a x86_64; do
    for lib in libarachne_sdk.so libjnidispatch.so; do
        [[ -f "$WORK/lib/$abi/$lib" ]] && echo "packaged: lib/$abi/$lib" || { echo "missing: lib/$abi/$lib"; status=1; }
    done
done
echo "generated classes: $(grep -c '^org\.arachne\.core\.' "$WORK/classes.txt"), JNA classes: $(grep -c '^com\.sun\.jna\.' "$WORK/classes.txt")"
exit $status
