# Go, Python, Swift, and Kotlin SDKs

Go, Python, Swift, and Kotlin expose clients over the Rust runtime. Each package
uses the same small C ABI and requires a native library built for the host
operating system and architecture. Linux x86_64 is the qualified target today;
the repository does not distribute prebuilt libraries. The SDK pins the public
Core source revision it builds against. The package commands below use the
repository root as the Go module and Swift package, with Python installed from
its local package directory.

## Build the native library

Clone with the Core submodule, then build from the repository root:

```sh
cargo +1.98.0 build --locked -p arachne-sdk
```

The library is in `target/debug`. Use a build for the same platform and
architecture as the application. The Go package uses cgo; Python uses ctypes;
Swift uses SwiftPM and a C system module.

## Go

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

## Python

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

## Swift

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

## Kotlin/JVM

The Kotlin package is a Gradle project under `bindings/kotlin`. It uses JNA to
call the same C ABI. Build the native library first, then run:

```sh
export ARACHNE_SDK_LIBRARY="$PWD/target/debug/libarachne_sdk.so"
export LD_LIBRARY_PATH="$PWD/target/debug${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
cd bindings/kotlin
./gradlew test
```

Use `libarachne_sdk.dylib` and `DYLD_LIBRARY_PATH` on macOS. `Client.open()`
returns a blocking client with typed endpoint, workspace, policy, protected
publication, and persistence methods. `rawCall` and `rawCallStored` expose the
remaining runtime operations; snapshot bytes remain separate from JSON. Calls
are serialized per client, so use a worker thread instead of a UI thread.

```kotlin
import org.arachne.sdk.Client

Client.open().use { client ->
    val workspace = client.createWorkspace("Feed owner", "Field feeds")
    client.installWorkspacePolicy(workspace.epoch + 1)
    println(workspace.workspace.contentToString())
}
```

## Typed client coverage

Go, Python, and Swift provide typed methods and models for endpoint and workspace
state; workspace creation and invitations; join and admission staging/adoption;
encrypted record storage; roster, policy, and service profiles; protected
publication and reception; topic interest and basic unprotected pub/sub;
connectivity and metrics; and recovery-range workflows. Kotlin provides typed
methods and models for those workflows, with `rawCall` / `rawCallStored` for
the complete runtime API. See the
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
