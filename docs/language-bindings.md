# Language bindings

The SDK is moving from hand-written bindings over a JSON C ABI to bindings that
UniFFI generates from Rust (ADR A1/A4 in core, `docs/reviews/adr-a1-a4-sdk-contract.md`,
steps 7-9). Both paths are in the repository during the move.

## Generated bindings (UniFFI)

### What is generated

One Rust module, `crates/arachne-sdk/src/uniffi_api/`, is the source of truth. The UniFFI
export layer stays in the SDK crate; core stays free of FFI code. It wraps core's typed
`arachne_runtime::Client`. It sends no JSON and holds no lock while a call waits.

| Group | Exported (`Client` methods unless noted) |
| --- | --- |
| Lifecycle | `open(ClientConfig)`, `describe`, `state`, `next_event(timeout_ms)`, `wait_for_work(timeout_ms)`, `wake`, `close`, `set_deadline(ms)`, `poll_control`, `network_change`, `add_address_hint`; free `suspend`, `resume`, `is_suspended` (default context) |
| Workspace | `create_workspace`, `member_roster`, `use_service_profile`, `metrics` |
| Invitations, join, admission | `stage_invitation(expires_at, kind)` / `adopt_invitation`, `inspect_invitation`, `invitation_controls` (read only), `fetch_invitation_checkpoint`, `begin_join`, `stage_join` / `adopt_join`, `stage_admission`, `stage_invitation_approval`, `stage_invitation_decline` / `adopt_admission`, `retained_admission`, `admission_approvals`, `acknowledge_admission_approval`, `send_admission_reply` |
| Policy, interest, publication | `install_workspace_policy`, `install_member_policy`, `set_interest`, `poll_interest`, `stage_protected_publication` / `adopt_protected_publication` |
| Receive (inbox) | `poll_protected` / `adopt_protected_reception`, `poll_pending_object`, `stage_object_acknowledgement`, `stage_object_rejection` |
| Recovery | `fetch_recovery_range(request, epoch)`, `poll_recovery_range`, `cancel_recovery_range`, `stage_recovery_range` (`Candidate`, `AlreadyCovered`, `NoNewObjects`, `AwaitingApplication`) / `adopt_recovery` (recovered and missing counts) |
| Presence | `poll_presence(announce)` |
| Errors | `ApiError`, `ErrorCode` (stable numbers), free `api_error_code(error)`, `api_version()` |

Candidates are opaque objects (`InvitationCandidate`, `AdmissionCandidate`, `JoinCandidate`,
`PublicationCandidate`, `ReceptionCandidate`, `RecoveryCandidate`). Foreign code never sees the
snapshot bytes. Only the adopt call of the same kind on the same client accepts a candidate,
and only once; otherwise it gives `WrongState` (102). IDs are lowercase hex strings
(`EndpointId`, `MemberId`, `WorkspaceId`, `AttemptId`, `RecordId`, and `Key32` for other
32-byte keys). Counts are `u64`.

Not wrapped yet, on purpose (TODOs in `uniffi_api/ops.rs`):

- Storage and candidate persistence (A5 replaces it): record storage, `save_candidate`,
  seal/restore, `reset_workspace`, `discard_workspace_candidate`, `drive_join`,
  `drive_workspace`. So a generated client is never durable today.
- Management, leave and revocation (A2 changes commit ordering).
- `request_admission` and `poll_membership_update` (they return untyped JSON), the
  direct-recovery ops (not on the typed core `Client`), nearby, and the fixtures
  `publish` / `poll` / `install_policy`.

### Shims

Core cannot yet be exported as it is (see the core step 6 list). The SDK carries shims,
each marked `// SHIM: remove after core step 6 <reason>`: mirrors of the
`#[non_exhaustive]` `ErrorCode`, `ApiError`, `Event`, `InvitationKind`; one `Network` with
`Tor` always present (`Unsupported` in this build); hex custom types for the core IDs and
`Key32`; `u64` for `usize`; record copies of the runtime structs; `RouteKind::Custom { name }`;
and the candidate objects.

Rules for foreign code:

- All calls block. Call them from a worker thread, not a UI thread or an async executor.
  `close`, `wake`, `next_event` and `wait_for_work` can run on different threads at
  the same time. `close` releases every waiter.
- Branch on `api_error_code(error)`, not on the message text.
- Keep a default branch in every `when`/`switch`/`match` on `Event`, `ErrorCode` and
  `ApiError`. The foreign enums are exhaustive, but new variants come with a new
  `api_version`.
- Kotlin: `Client.close` is named `shutdown` (it ends the session). The generated
  `close()` of `AutoCloseable` frees the native handle. Call `shutdown()`, then `close()`
  (or use `use { }`).

### Layout

| Path | What |
| --- | --- |
| `crates/arachne-sdk/src/uniffi_api/` | Exported Rust surface: `mod.rs` (errors, lifecycle), `types.rs` (records), `candidates.rs`, `ops.rs` |
| `crates/arachne-sdk/uniffi.toml` | Generator settings: package and module names, the Kotlin `close` rename |
| `crates/uniffi-bindgen` | `uniffi-bindgen` at the exact scaffolding version (`=0.31.2`) for Kotlin, Swift, Python |
| `patches/uniffi-bindgen-go-enum-discr.patch` | Fix for `uniffi-bindgen-go` v0.7.1 (provenance in `patches/README.md`) |
| `scripts/build-uniffi-bindgen-go.sh` | Builds the patched Go generator into `target/tools/bin` |
| `scripts/generate-bindings.sh` | Builds the cdylib and writes `generated/` |
| `scripts/uniffi-smoke.sh` | Runs `tests/uniffi/<language>` against `generated/` |
| `generated/kotlin/org/arachne/sdk/generated/arachne_sdk.kt` | Kotlin (JNA), package `org.arachne.sdk.generated` |
| `generated/swift/ArachneGenerated.swift`, `ArachneGeneratedFFI.{h,modulemap}` | Swift module `ArachneGenerated` and its C module |
| `generated/python/arachne_generated/` | Python package `arachne_generated` (the library goes next to `arachne_sdk.py`) |
| `generated/go/arachne_sdk/` | Go package `github.com/arachne-systems/arachne-sdk/generated/go/arachne_sdk` (cgo) |

**The generated files are committed.** Go modules and SwiftPM remote packages fetch
source and run no build step, so Go and Swift users can only get generated code that
is in the repository. Committed output also makes every change to the foreign API
show in review. CI regenerates the tree and fails if `git diff` is not empty, so the
files cannot drift from the Rust source. Do not edit them by hand. The names
(`org.arachne.sdk.generated`, `ArachneGenerated`, `arachne_generated`) are different
from the hand packages so that both can load in one process during the move.

### Generate and test

```sh
scripts/build-uniffi-bindgen-go.sh          # one time; patched uniffi-bindgen-go
scripts/generate-bindings.sh                # cargo build + generate into generated/
JNA_JAR=/path/to/jna-5.17.0.jar scripts/uniffi-smoke.sh   # kotlin swift python go
```

The smoke tests need `kotlinc` and `java` 21, Swift 6.1.3, `uv` and Go with cgo.
Each smoke test opens a client, reads `describe` and `state`, waits in `next_event` with
a timeout, checks that `wake` and a `close` from another thread release a parked
`next_event`, and checks the error codes 100, 103 and 1 across the boundary. Each flow
test (`Flow.kt`, `Flow.swift`, `test_flow.py`, `flow_test.go`) runs two clients on
localhost: invitation, join, publish, recovery, acknowledge; then interest, live protected
receive, reject; then presence, metrics, deadline, suspend and resume; and candidate misuse
(another client, second adopt). The
`generated-bindings` CI job runs the same steps on Linux.

### Android AAR (generated Kotlin)

`android/` is a Gradle build (wrapper 8.14.3, AGP 8.11.1, Kotlin 2.2.20, the same setup as the
Kotlin PR) with two modules:

- `:sdk` is the AAR. It holds `generated/kotlin` and `libarachne_sdk.so` for `arm64-v8a`
  and `x86_64` (API 26+), and it ships `consumer-rules.pro` for R8. `scripts/build-android-libs.sh`
  builds the libraries (release, stripped, `cargo ndk`, NDK 27.1.12297006). `.cargo/config.toml`
  links them with 16 KB pages. The script fails if a LOAD segment is not aligned to `0x4000`.
  It ships only `libarachne_sdk.so`; iroh's own cdylibs are linked in statically.
- `:smoke` is a consumer app. It takes the AAR from the local Maven repo
  (`android/build/repo`), so JNA comes in through the POM as it would for a real app.
  R8 minifies it in both build types, so the instrumented test runs the shrunk binding.
  The app adds no rules for the SDK or JNA.

JNA (`net.java.dev.jna:jna:5.17.0@aar`) is an `api` dependency in the POM. It is not
inside the AAR. A second copy of `libjnidispatch.so` in an ATAK process is the open risk
of ADR step 9, so the host app decides where JNA comes from.

```sh
export ANDROID_HOME=~/Android/Sdk JAVA_HOME=/path/to/jdk-17-or-21   # a JDK with javac
cd android
./gradlew --no-daemon :sdk:publishReleasePublicationToLocalRepository :smoke:assembleRelease
../scripts/check-android-r8.sh                 # UniFFI and JNA classes kept, not renamed
ANDROID_SERIAL=emulator-5554 ./gradlew --no-daemon :smoke:connectedDebugAndroidTest
```

Set `ANDROID_SERIAL` when other devices are attached, because otherwise the test installs on
every device. The `generated-android` CI job runs the same steps on a 16 KB page
(`google_apis_ps16k`) API 35 emulator.

### Versions

All four generators use UniFFI `=0.31.2`. `uniffi-bindgen-go` v0.7.1+v0.31.0 is the
only Go generator for 0.31, so we do not move to UniFFI 0.32 until a Go release for it
exists. The Go build moves the generator's own `uniffi` dependencies from 0.31.0 to
0.31.2, so the scaffolding and all generators use one version.

### Mirrors of the core contract

Core stays free of FFI code; the export layer lives in the SDK. So `uniffi_api/`
declares copies of `ErrorCode`, `ApiError`, `Event`, `Network` and `InvitationKind` with
`From` conversions. UniFFI `remote`
types cannot be used for them, because the core types are `#[non_exhaustive]`.
Unit tests check the copies against core (`ErrorCode::ALL`, `Network::ALL`, and one
`ApiError::new` for each code). `Event` has no `ALL` list, so a new core event gives
`Internal` at run time until the mirror has it. The core ID newtypes, `WorkspacePhase`, `MemberKind`, `Presence` and the metrics
counters are exported directly as remote types. When core step 6 fixes the blockers
(exhaustive-matchable or `ALL`-listed enums, ID newtypes and `u64` in the runtime
structs, one `Network`), delete the matching shims.

### Migration plan from the hand bindings

1. **Now (this slice).** Pipeline, generated tree, smoke tests and CI job exist. The hand
   bindings and `ffi.rs` stay unchanged. At the local core pin `2205887`, the hand
   Go, Python and Swift bindings call ops that core removed (A3, B1), so their CI steps
   are off (`if: false`, with a TODO). The Rust SDK API (`arachne_sdk::Client`,
   examples, `tests/client.rs`) is ported and tested.
2. **Grow the surface with core (ADR steps 2-6).** Add each typed op to `uniffi_api/ops.rs`
   as one exported method. Candidates are already objects bound to their client and kind
   (the Kotlin PR's "candidate owner" check is now a Rust-side check, so no language
   keeps a candidate map); A5 moves that check into core. After core step 6, delete the
   shims.
3. **Kotlin (ADR step 7).** The generated AAR (`android/`) exists and passes its
   minified emulator test. Next, retire the Kotlin PR's hand `Client.kt`, `Models.kt` and
   `Native.kt` and its `bindings/kotlin-android`. Then rename the package to
   `org.arachne.sdk` (update `consumer-rules.pro` with it).
4. **Swift, Python, Go (ADR step 8).** Point `Package.swift`, `bindings/python/pyproject.toml`
   and the Go import path at `generated/`. Delete `bindings/go`, `bindings/python/arachne_sdk`,
   `bindings/swift/Sources/ArachneSDK`, `include/arachne_sdk.h` and `ffi.rs`. Delete the
   off CI steps.
5. **Dispatcher (ADR step 9).** Core deletes `execute`/`execute_stored`. The SDK has no
   `rawCall` left to remove.

## Hand-written bindings (to be removed in step 8)

Go, Python, and Swift expose typed clients over the Rust runtime. Each package
uses the same small C ABI and requires a native library built for the host
operating system and architecture. Linux x86_64 is the qualified target today;
the repository does not distribute prebuilt libraries. The SDK pins the public
Core source revision it builds against. The package commands below use the
repository root as the Go module and Swift package, with Python installed from
its local package directory.

### Build the native library

Clone with the Core submodule, then build from the repository root:

```sh
cargo +1.98.0 build --locked -p arachne-sdk
```

The library is in `target/debug`. Use a build for the same platform and
architecture as the application. The Go package uses cgo; Python uses ctypes;
Swift uses SwiftPM and a C system module.

### Go

```go
package main

import (
	"crypto/rand"
	"fmt"

	arachne "github.com/arachne-systems/arachne-sdk/bindings/go"
)

func main() {
	if err := run(); err != nil { panic(err) }
}

func run() error {
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil { return err }
	client, err := arachne.Open(arachne.Direct, secret[:])
	if err != nil { return err }
	defer client.Close()

	workspace, err := client.CreateWorkspace("Feed owner", nil)
	if err != nil { return err }
	if err := client.InstallWorkspacePolicy(workspace.Epoch + 1); err != nil { return err }
	var id arachne.RecordID
	if _, err := rand.Read(id[:]); err != nil { return err }
	candidate, err := client.StageProtectedPublication(
		workspace.Workspace, workspace.Epoch+1, "feeds/catalog/v1", id, []byte(`{"version":1}`),
	)
	if err != nil { return err }
	report, err := client.AdoptProtectedPublication(candidate.Snapshot)
	if err != nil { return err }
	fmt.Println("queued:", report.Queued)
	return nil
}
```

The root Go module exposes
`github.com/arachne-systems/arachne-sdk/bindings/go`. Add it to an application
with:

```sh
go get github.com/arachne-systems/arachne-sdk/bindings/go@main
```

The module requires cgo. Build the native library from a recursive SDK checkout,
then point cgo and the dynamic loader at it. On Linux:

```sh
export CGO_LDFLAGS="-L$ARACHNE_SDK_DIR/target/debug"
export LD_LIBRARY_PATH="$ARACHNE_SDK_DIR/target/debug${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
```

Set `ARACHNE_SDK_DIR` to the root of the SDK checkout first.

Use `DYLD_LIBRARY_PATH` on macOS. To run the binding tests inside the checkout:

```sh
cd bindings/go
go build ./...
```

### Python

Install the package from the checkout and point it at the native library:

```sh
python3 -m pip install ./bindings/python
export ARACHNE_SDK_LIBRARY="$PWD/target/debug/libarachne_sdk.so"
export LD_LIBRARY_PATH="$PWD/target/debug${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
```

Use `libarachne_sdk.dylib` and `DYLD_LIBRARY_PATH` on macOS. `ARACHNE_SDK_LIBRARY`
selects the library explicitly on all supported platforms.

```python
import secrets

from arachne_sdk import Client, ClientConfig, Network, RecordID

with Client.open(ClientConfig(Network.DIRECT, secrets.token_bytes(32))) as client:
    workspace = client.create_workspace("Feed owner", "Field feeds")
    client.install_workspace_policy(workspace.epoch + 1)
    candidate = client.stage_protected_publication(
        workspace.workspace,
        workspace.epoch + 1,
        "feeds/catalog/v1",
        RecordID(secrets.token_bytes(16)),
        b'{"version":1}',
    )
    report = client.adopt_protected_publication(candidate.snapshot)
    print("queued:", report.queued)
```

### Swift

```swift
import ArachneSDK
import Foundation

var random = SystemRandomNumberGenerator()
let secret = Data((0..<32).map { _ in UInt8.random(in: .min ... .max, using: &random) })
let client = try Client.open(config: ClientConfig(network: .direct, secret: secret))
defer { try? client.close() }

let workspace = try client.createWorkspace(displayName: "Feed owner", workspaceName: "Field feeds")
try client.installWorkspacePolicy(revision: workspace.epoch + 1)
let recordID = Data((0..<16).map { _ in UInt8.random(in: .min ... .max, using: &random) })
let candidate = try client.stageProtectedPublication(
    workspace: workspace.workspace,
    revision: workspace.epoch + 1,
    topic: "feeds/catalog/v1",
    id: recordID,
    payload: Data(#"{"version":1}"#.utf8)
)
let report = try client.adoptProtectedPublication(snapshot: candidate.snapshot)
print("queued:", report.queued)
```

The repository root is the SwiftPM package, so it can be used as a remote
dependency. Add it to an application's `Package.swift` with
`.package(url: "https://github.com/arachne-systems/arachne-sdk.git", branch: "main")`
and depend on the `ArachneSDK` product:

```swift
.product(name: "ArachneSDK", package: "arachne-sdk")
```

Build the native library from a recursive SDK checkout, then set `LIBRARY_PATH`
and the runtime library path to that checkout's `target/debug` directory. To run
the package tests:

```sh
LIBRARY_PATH="$PWD/target/debug" \
LD_LIBRARY_PATH="$PWD/target/debug" swift test
```

On macOS use `DYLD_LIBRARY_PATH` for runtime lookup.

### Typed client coverage

Each language provides typed methods and models for endpoint and workspace
state; workspace creation and invitations; join and admission staging/adoption;
encrypted record storage; roster, policy, and service profiles; protected
publication and reception; topic interest and basic unprotected pub/sub;
connectivity and metrics; and recovery-range workflows. See the
[workflow guide](workflows.md) for ordering and security details.

Protected reception follows the same save-before-adopt rule as protected
publication. `poll_protected` returns an opaque candidate, and the authenticated
plaintext is released only by `adopt_protected_reception`. If record storage is
enabled, save the exact returned snapshot with `save_candidate` before
adoption. Do not edit, serialize, or reconstruct candidate snapshot bytes.

The receive methods have matching typed names in all three languages:

```go
candidate, err := client.PollProtected()
if err != nil { return err }
if candidate != nil {
	state, err := client.WorkspaceState()
	if err != nil { return err }
	if state.Durable {
		if err := client.SaveCandidate(candidate.Snapshot); err != nil { return err }
	}
	message, err := client.AdoptProtectedReception(candidate.Snapshot)
	if err != nil { return err }
	_ = message // authenticated payload, topic, sender, and record ID
}
```

Python uses `poll_protected()`, `save_candidate(...)`, and
`adopt_protected_reception(...)`; Swift uses `pollProtected()`,
`saveCandidate(...)`, and `adoptProtectedReception(snapshot:)`.

Go, Python, and Swift `enable_object_delivery` complete the inbox-enable
transition and save its exact snapshot first when record storage is enabled.
These bindings can publish current values, but do not expose pending-object
polling or acknowledgement/rejection methods yet.

The generic dispatcher remains available for less common runtime operations:
`RawCall` / `RawCallStored` in Go, `raw_call` / `raw_call_stored` in Python, and
`rawCall` / `rawCallStored` in Swift. Byte-vector fields use arrays of unsigned
integers at the C boundary; the language adapters map these to Go byte slices,
Python `bytes`, and Swift `Data`.

Client calls block and are serialized per client. Call them from a blocking
worker instead of an async executor or UI thread. Use a unique 32-byte endpoint
secret for a persistent identity. Only the Direct profile accepts an empty
secret for an ephemeral endpoint. The record-storage root is separate from the
endpoint secret and remains under application control.
