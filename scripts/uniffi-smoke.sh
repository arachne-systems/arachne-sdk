#!/usr/bin/env bash
# Run the smoke tests of the generated bindings (tests/uniffi/<language>).
# Run scripts/generate-bindings.sh first. Linux only for now.
#
# Usage: scripts/uniffi-smoke.sh [kotlin] [swift] [python] [go]   (default: all)
# Env:
#   JNA_JAR   jna-5.17.0.jar (Kotlin). kotlinc and java must be on PATH.
#   SMOKE_OUT scratch output (default: target/uniffi-smoke).
# Each test has a 120 s cap. The exit status is nonzero if any test fails.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
GEN="$ROOT/generated"
TESTS="$ROOT/tests/uniffi"
LIBDIR="${CARGO_TARGET_DIR:-$ROOT/target}/debug"
LIB="$LIBDIR/libarachne_sdk.so"
OUT="${SMOKE_OUT:-$ROOT/target/uniffi-smoke}"
[[ -f "$LIB" ]] || { echo "missing $LIB (cargo build -p arachne-sdk)" >&2; exit 1; }

languages=("$@")
(( ${#languages[@]} )) || languages=(kotlin swift python go)
mkdir -p "$OUT"
status=0
run() {
    local name=$1; shift
    echo "== $name"
    if timeout 120 "$@"; then echo "$name: PASS"; else echo "$name: FAILED"; status=1; fi
}

kotlin() {
    local jna="${JNA_JAR:?set JNA_JAR to jna-5.17.0.jar}"
    kotlinc "$GEN"/kotlin/org/arachne/sdk/generated/*.kt "$TESTS/kotlin/SmokeTest.kt" "$TESTS/kotlin/Flow.kt" \
        -cp "$jna" -include-runtime -nowarn -d "$OUT/smoke-kt.jar"
    run kotlin java -Djna.library.path="$LIBDIR" -cp "$OUT/smoke-kt.jar:$jna" SmokeTestKt
}

swift() {
    # One module: the generated file plus the test.
    swiftc -module-name ArachneSmoke -swift-version 6 \
        -Xcc -fmodule-map-file="$GEN/swift/ArachneGeneratedFFI.modulemap" -I "$GEN/swift" \
        "$GEN/swift/ArachneGenerated.swift" "$TESTS/swift/Flow.swift" "$TESTS/swift/main.swift" \
        -L "$LIBDIR" -larachne_sdk -o "$OUT/smoke-swift"
    run swift env LD_LIBRARY_PATH="$LIBDIR" "$OUT/smoke-swift"
}

python() {
    # The generated loader looks for the library next to the module.
    rm -rf "$OUT/python" && mkdir -p "$OUT/python"
    cp -r "$GEN/python/arachne_generated" "$OUT/python/"
    cp "$LIB" "$OUT/python/arachne_generated/"
    run python env PYTHONPATH="$OUT/python" uv run --no-project python "$TESTS/python/test_smoke.py"
    run python-flow env PYTHONPATH="$OUT/python" uv run --no-project python "$TESTS/python/test_flow.py"
}

go() {
    (
        cd "$ROOT"
        export CGO_ENABLED=1 CGO_LDFLAGS="-L$LIBDIR -larachne_sdk" LD_LIBRARY_PATH="$LIBDIR"
        run go go test -count=1 -v ./tests/uniffi/go/
        exit $status
    ) || status=1
}

for language in "${languages[@]}"; do
    case "$language" in
        kotlin | swift | python | go) "$language" || { echo "$language: FAILED (build)"; status=1; } ;;
        *) echo "unknown language: $language" >&2; exit 2 ;;
    esac
done
exit $status
