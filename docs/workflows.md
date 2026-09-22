# SDK workflow guide

This guide maps the pre-release typed `Client` API to common workflows. The
`endpoint` example opens a client and creates a workspace. The `join_flow`
example walks through invitation and admission with two clients in one
process; it passes the exchange in memory and is not a deployed transport.

## Start a client

Open a client with a network profile, inspect its endpoint, and create a
workspace:

```rust
use arachne_sdk::{Client, ClientConfig, Network, Result};

fn main() -> Result<()> {
    let mut client = Client::open(ClientConfig {
        network: Network::Direct,
        secret: None,
    })?;
    let endpoint = client.endpoint()?;
    let workspace = client.create_workspace("Owner", Some("Field team"))?;
    println!("endpoint: {:?}", endpoint.endpoint_key);
    println!("workspace: {:?}", workspace.workspace);
    client.close()
}
```

`secret: None` is allowed for an ephemeral identity with `Network::Direct`.
Supply a securely stored endpoint credential when the endpoint key must stay
stable across restarts. Protected publication and reception require
`secret: Some(...)`; core derives the protected local-state key from that
credential. The separate record-storage root key protects the local record
database. Admission, join, and other staged workspace transitions also need a
configured endpoint credential. Use a different credential for each endpoint.

These client calls are synchronous. Run them on a blocking worker rather than
an application's UI thread or async executor.

## Invite and admit a member

The API exposes the steps so the application can apply its admission and
storage policy:

1. The workspace owner calls `issue_invitation` and gives the joining client
the invitation, checkpoint, and route information it needs to reach the owner.
2. The joining client calls `begin_join`, producing a join request and its
endpoint ID. With `Network::Direct`, it may need to add the invitation's peer
and address with `add_address_hint` first.
3. The owner calls `stage_admission` with the peer-authenticated endpoint ID
and request. After deciding to admit it, the owner calls `adopt_admission`; it
can then obtain the matching reply with `retained_admission`.
4. The joining client passes the reply's welcome and a
`JoinAdmissionStep { commit, authorization }` to `stage_join`. It then accepts
the join by calling `adopt_join`.

For a direct network profile, use the peer and address returned in the
invitation with `add_address_hint` when discovery has not supplied a route.
The `join_flow` example passes admission data directly between clients; it
does not show the production control channel or retry handling. Its distinct
fixed credentials are for the local demo only.

These are separate stage and adopt operations. When using record storage, save
the exact candidate snapshot with `save_candidate` before the matching adopt
call. Keep the invitation and admission exchange tied to the intended
workspace and endpoint.

## Publish and receive protected data

Install policy derived from the accepted workspace state. `install_policy`
accepts explicit endpoint permissions; `install_workspace_policy` is a broad
convenience policy that includes all current members and topics.

For the protected send path, call `stage_protected_publication` with the
workspace, policy revision, topic, record ID, and opaque payload. If the
workspace is durable, persist the exact returned snapshot before calling
`adopt_protected_publication`.

`publish` and `poll` are a separate basic transport path; they do not provide
MLS-protected group messaging. For protected live reception, call
`poll_protected`. It stages the incoming message and returns a
`ProtectedReceptionCandidate` containing the workspace and exact snapshot;
the payload remains unavailable until `adopt_protected_reception`. For a
durable workspace, save that exact snapshot with `save_candidate` before
adoption. The resulting `ReceivedProtectedPublication` contains the
authenticated sender, topic, record ID, sequence, and payload.

Run `cargo run --example protected_publish` for a non-durable, single-member
example of workspace-derived policy and protected staging/adoption. It has no
receiving peer, so its delivery report is not evidence of network delivery.
Its fixed endpoint credential is for the local demo only.

Run `cargo run --example protected_receive` for a two-client direct-network
example. It passes invitation/admission material in memory, sends one
protected publication over the peer connection, and adopts it on the receiver.
Its fixed credentials are for this local demo only. It uses non-durable
workspaces and does not demonstrate persisted recovery or the separate
object-delivery workflow.

## Restore durable state

Call `enable_record_storage` with a caller-managed 32-byte root key and store
path. Keep that key separate from the endpoint credential. After restart, open
the client, call `restore_record_storage` with the same root key and workspace
ID, then call `use_service_profile` again for service endpoints. A service
profile marks the signed profile as a service; it does not grant membership or
publication rights.

## Other client operations

- Session control: `workspace_state`, `cancel`, `wait_for_work`,
  `poll_control`, `network_change`, and `close`.
- Membership and diagnostics: `member_roster`, `connectivity`, and `metrics`.
- Topic routing: `add_address_hint`, `set_interest`, and `poll_interest`.
- Protected messaging: `stage_protected_publication`,
  `adopt_protected_publication`, `poll_protected`, and
  `adopt_protected_reception`.
- Basic transport: `publish` and `poll`; these do not provide MLS protection.
- Recovery: `fetch_recovery_range`, `poll_recovery_range`,
  `cancel_recovery_range`, `stage_recovery_range`, `adopt_recovery`, and
  `poll_recovered_publication`.
