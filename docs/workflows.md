# SDK workflow guide

## BLUF

Open a Client with a stable endpoint key and Core storage. Use typed candidates to
change workspace state. Core commits durable state before an adopt call succeeds.
Application payloads and UI behavior stay in the application.

## Start a client

1. Obtain Core defaults with `default_client_config(network)`.
2. Set a private endpoint key. Use a distinct key for each endpoint.
3. Set `storage` to `StorageConfig::open_sqlite(directory, storage_root)`.
   The root is a separate 32-byte key. Create the host-private directory first.
4. Open an owned `Context`, then call `Client::open_in(context, config)`.
5. Call `endpoint` and either `create_workspace` or `restore_workspace`.

`Network::Direct` permits an ephemeral endpoint without a secret for endpoint
inspection. Creating or joining a durable workspace needs the configured secret
and storage. The network profile selects Iroh discovery and relay behavior.
Address hints carry peer routes, not peer identity.

Client calls block. Use a blocking worker outside the UI thread or async executor.
An owned Context gives the host independent limits, power mode and suspend/resume.

## Invite and admit a member

1. The administrator stages and adopts an invitation. Share its invitation,
   checkpoint and route information through the application's approved channel.
2. The joining client calls `begin_join` or `begin_join_with_peers`. Core retains
   the pending join in storage.
3. The administrator stages the authenticated request and adopts the admission.
   `retained_admission` returns the matching reply.
4. The joining client stages and adopts the reply. Both clients install policy
   derived from their accepted workspace state.

For a transport-driven flow, use `drive_join` and `drive_workspace` to advance the
persisted protocol. Handle administrator-required, approval, self-update and
recovery states as application states. After a restart, restore the workspace
and continue its driver.

The `join_flow` example passes protocol material between two Clients in one
process. It checks the typed operations; it does not prove deployed discovery or
operator approval flows. The fixed keys in examples are for that local run.

## Publish and receive protected data

Install workspace or member policy before publication. A protected publication
has a workspace, policy revision, topic, record ID and opaque application payload.
Call `stage_protected_publication`, then `adopt_protected_publication` with the
returned object. For a replaceable current value, use
`stage_protected_publication_with_current` and its selector, replacement key,
expiry and tombstone fields.

Use `stage_protected_publication_with_options` to select the audience and delivery
mode. `default_publication_options()` returns Core's empty audience and `Critical`
mode. Empty means the workspace audience. A directed audience contains at most
64 current member IDs, sorted by bytes, with no duplicates or the sender's ID.
It retains the same workspace authorization checks.

`PublicationMode::Bulk` selects the Bulk queue. `PublicationMode::Current` carries
the current-value metadata and requires an empty audience. These modes are one
choice, so a Current publication cannot also be Bulk. The existing publication
methods keep their default behavior.

`poll_protected` stages incoming protected data. Adopt the candidate to commit
it to the inbox. `poll_pending_object` returns the authenticated publication.
After the application handles it, stage and adopt an acknowledgement or rejection.
Pending data remains available until that application decision commits.

Core performs the durable write and readback. There is no SDK `save_candidate`
step. Candidates cannot move to another client or operation, and cannot be used
twice. A discard operation abandons a staged candidate.

`protected_publish` is a single-member local example. Its empty recipient report
is not proof of delivery. `protected_receive` uses two direct Clients and one
protected peer exchange. The language flow tests also exercise retained recovery
and the inbox decision.

## Restore and recover

Reopen the Client with the same endpoint key, storage directory and storage root.
Call `restore_workspace(workspace, expected_anchor)`. The result identifies active,
joining or removed state. Restoring a removal returns its tombstone and closes
the session. SQLite restore requires the freshness anchor returned by
`record_freshness`; store it outside the database after every committing call.
Reapply the service profile for service endpoints.
A service profile changes the member profile; it does not grant membership.

Use range recovery for retained publications, direct recovery for an authenticated
peer range, and current-view recovery for replaceable current data. Stage and adopt
the returned recovery candidate. Deliver each recovered object through the same
pending inbox and application decision as a live object. Handle awaiting-application
and missing-count results explicitly.

Use resource publication and fetch operations for retained bytes. Authorization,
verified content hashes and publication identity stay in Core. The application
chooses its payload format, retention policy and presentation.

## Close

`wake` releases a parked wait. `close` drains within the configured close deadline,
ends the session and releases waiters. Generated Kotlin names this method
`shutdown`; its separate `close()` releases the foreign object handle.
