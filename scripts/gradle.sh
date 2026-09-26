#!/usr/bin/env bash
# Give each local agent one Gradle lane. Rust builds still use the shared
# Cargo lock supplied by the caller.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
LANE="${GRADLE_LANE:-default}"
[[ "$LANE" =~ ^[a-zA-Z0-9_-]+$ ]] || { echo "invalid GRADLE_LANE: $LANE" >&2; exit 2; }
export ANDROID_HOME="${ANDROID_HOME:-$HOME/Android/Sdk}"
if [[ -z "${JAVA_HOME:-}" && -x /usr/lib/jvm/java-17-openjdk-amd64/bin/javac ]]; then
    export JAVA_HOME=/usr/lib/jvm/java-17-openjdk-amd64
fi

exec flock "/tmp/arachne-sdk-gradle-lane-$LANE.lock" nice -n 10 \
    "$ROOT/android/gradlew" --project-dir "$ROOT/android" --console=plain \
    --project-cache-dir "$ROOT/android/.gradle-$LANE" --max-workers=4 "$@"
