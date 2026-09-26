#!/usr/bin/env bash
# Generate the Kotlin, Swift, Python and Go bindings from the SDK cdylib
# (UniFFI library mode, ADR A1/A4 steps 7-8). Output: generated/<language>/.
# The layout is described in docs/language-bindings.md.
#
# Needs the patched Go generator: scripts/build-uniffi-bindgen-go.sh.
# Env:
#   CARGO       cargo command (default: cargo). Set RUSTUP_TOOLCHAIN to pick one.
#   BINDGEN_GO  Go generator (default: target/tools/bin/uniffi-bindgen-go).
#   OUT         output root (default: generated). CI compares it with git.
#   NO_BUILD=1  use the existing target/debug library.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CARGO="${CARGO:-cargo}"
BINDGEN_GO="${BINDGEN_GO:-$ROOT/target/tools/bin/uniffi-bindgen-go}"
OUT="${OUT:-$ROOT/generated}"
TARGET_DIR="${CARGO_TARGET_DIR:-$ROOT/target}"
case "$(uname -s)" in
    Darwin) LIB="$TARGET_DIR/debug/libarachne_sdk.dylib" ;;
    *) LIB="$TARGET_DIR/debug/libarachne_sdk.so" ;;
esac
BINDGEN="$TARGET_DIR/debug/uniffi-bindgen"
CONFIG="$ROOT/crates/arachne-sdk/uniffi.toml"

cd "$ROOT"
if [[ "${NO_BUILD:-0}" != 1 ]]; then
    "$CARGO" build --locked -p arachne-sdk -p uniffi-bindgen
fi
[[ -f "$LIB" ]] || { echo "missing $LIB" >&2; exit 1; }
[[ -x "$BINDGEN_GO" ]] || { echo "missing $BINDGEN_GO (run scripts/build-uniffi-bindgen-go.sh)" >&2; exit 1; }

rm -rf "$OUT/kotlin" "$OUT/swift" "$OUT/python" "$OUT/go"
gen() {
    "$BINDGEN" generate --library "$LIB" --no-format --language "$1" --out-dir "$2"
}

# Kotlin: Core's API and runtime packages, in one SDK library.
gen kotlin "$OUT/kotlin"

# Swift: one SwiftPM module contains both components, with separate C modules.
gen swift "$OUT/swift"
for component in ArachneApi ArachneRuntime; do
    mkdir -p "$OUT/swift/$component" "$OUT/swift/${component}FFI"
    mv "$OUT/swift/$component.swift" "$OUT/swift/$component/"
    mv "$OUT/swift/${component}FFI.h" "$OUT/swift/${component}FFI/"
    mv "$OUT/swift/${component}FFI.modulemap" "$OUT/swift/${component}FFI/module.modulemap"
done

# Python: both generated components share one package for relative imports.
gen python "$OUT/python/arachne_generated"
cat > "$OUT/python/arachne_generated/__init__.py" <<'EOF'
"""Generated UniFFI bindings for the Arachne SDK. Do not edit.

Regenerate with scripts/generate-bindings.sh. The native library
(libarachne_sdk.so / .dylib) must sit next to these generated modules.
"""

from .arachne_api import *  # noqa: F401,F403
from .arachne_runtime import *  # noqa: F401,F403
EOF

# Go: separate API and runtime packages under the SDK module.
"$BINDGEN_GO" "$LIB" --config "$CONFIG" --out-dir "$OUT/go"
gofmt -w "$OUT/go"

echo "generated into $OUT:"
find "$OUT" -type f \( -name '*.kt' -o -name '*.swift' -o -name '*.h' -o -name '*.modulemap' -o -name '*.py' -o -name '*.go' \) \
    -print0 | sort -z | xargs -0 wc -l
