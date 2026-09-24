# Android consumer integration plan

This is the readiness gate for a future Arachne-ATAK SDK integration. The
current Kotlin SDK PR does not migrate the ATAK plugin.

## Current boundary

- ATAK builds `fabric-android`, a JNI bridge with direct path dependencies on
  `arachne-runtime` and `arachne-security`. The plugin calls it through its
  `FabricNative`/`NativeAccess` layer and owns ATAK lifecycle, identity, storage,
  UI, and CoT translation.
- Arachne-SDK builds a separate `libarachne_sdk` C ABI. Kotlin uses JNA and the
  Android AAR packages that library. Its typed API covers the common endpoint,
  workspace, invitation, publication, storage, and recovery flows; other
  runtime operations are currently available through raw JSON calls.
- The two repositories currently pin different Core commits (`b124c90` in
  ATAK and `7123166` in the SDK), so they are not a version-matched drop-in.
- Packaging both native libraries would create two runtime entrypoints and
  separate client handles. An integration must choose one native boundary.

## Target

The SDK should own endpoint and workspace operations, with typed inputs,
results, errors, persistence, and lifecycle. The ATAK adapter should own only
ATAK-facing concerns such as contacts, CoT, UI, and app lifecycle. Android
storage and classloader behavior should be explicit parts of the Android SDK
adapter, not hidden inside ATAK.

## Gaps to close

1. **Version and native ownership.** Select a reviewed Core revision or release
   used by both repositories. Choose one native library and one session owner;
   keep transport profile mappings explicit.
2. **Typed operation coverage.** The Kotlin `Client` has no typed methods for
   several operations the ATAK plugin needs, including workspace snapshot
   import/export beyond record-storage restore, pending-join resumption,
   membership and invitation management,
   workspace-name changes, presence, nearby invitations, and resource transfer.
   The Rust runtime dispatcher may already support some of these even where its
   public `Client` does not. Map each plugin workflow to its current Core
   operation, then classify it as reusable Core behavior or ATAK-only behavior
   before adding a typed SDK method. Keep `rawCall` as an advanced escape hatch,
   out of normal examples; keep CoT and ATAK object mapping in the plugin.
3. **Safe staged transitions.** Core's Rust client returns typed candidate
   structs, but some kinds share `WorkspaceCandidate`; candidate snapshots are
   public bytes, adoption accepts arbitrary bytes, and durable callers must call
   `save_candidate` in the right order. Kotlin now returns operation-specific,
   client-bound candidates and saves the exact snapshot in the matching adopt
   method. Carry that safety to the Rust and C boundaries so candidate kind,
   client ownership, and persistence order are enforced consistently. Kotlin's
   low-level `rawCallStored` remains an escape hatch and leaves those obligations
   to its caller.
4. **Typed values and errors.** Kotlin aliases endpoint, member, and workspace
   IDs to `ByteArray`; typed methods now check fixed lengths at key inputs, but
   valid IDs from different domains can still be confused and validation is
   spread across methods. Introduce distinct ID value types with validation at
   construction. Core's Rust client preserves `ErrorKind`, while the C ABI
   reduces errors to status plus text; preserve stable error categories through
   the ABI and Kotlin exceptions.
5. **Android consumption.** The standalone AAR smoke test now consumes the
   generated AAR and declares its dependencies like an app. This does not prove
   ATAK classloader, minified release, host lifecycle, or physical arm64
   behavior. Decide on a versioned artifact with transitive dependency metadata
   or a pinned source-build path before making the plugin depend on it. Keep
   endpoint and record-storage keys in Android-protected storage.

## Migration sequence and acceptance

1. Align Core revisions and select one native handle owner. Close the typed API,
   candidate-safety, ID, and error gaps that block the selected workflow; freeze
   the Android dependency shape.
2. Port one vertical slice through the SDK: create and restore a workspace,
   invite and admit a second client, then publish and receive protected data.
   Keep ATAK UI and CoT handling outside this slice.
3. Move the plugin adapter to that SDK surface and remove its direct Core path
   dependencies. Retain only Android/ATAK lifecycle and presentation code.
4. Verify one native runtime is packaged, no plugin call site constructs raw
   Core JSON, release shrinking preserves the binding, the ATAK package
   classloader loads the native library, and the target ATAK host completes the
   vertical slice. Keep the standalone 16 KB emulator and arm64 build checks.

Recheck repository pins and host build inputs before starting the migration;
the commit values above record this inventory, not a permanent compatibility
claim.
