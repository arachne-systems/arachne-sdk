//! Typed `Client` operations of the exported surface, one group at a time.
//!
//! Not wrapped on purpose:
//! - TODO(A5): record storage (`enable_record_storage`, `restore_record_storage*`,
//!   `record_freshness`, `save_candidate`), sealing (`seal_workspace`,
//!   `restore_workspace`, `seal_pending_join`, `restore_pending_join`),
//!   `reset_workspace`, `discard_workspace_candidate`, and the storage-driven
//!   `drive_join` / `drive_workspace`. A5 replaces candidate persistence.
//! - TODO(A2): management and leave (`stage_management`, `leave_via_peer`,
//!   `stage_solo_leave`, `adopt_removal`, `stage_workspace_name`,
//!   invitation revocation). A2 changes commit ordering.
//! - TODO(core step 6): `request_admission` and `poll_membership_update`
//!   return an open `serde_json::Value` (a peer's wire reply); core types them.
//!   The direct-recovery ops (`next_direct_gap`, `fetch_direct_recovery`, ...)
//!   exist only in the JSON dispatcher, not on the typed `Client`.
//! - Fixtures: unprotected `publish` / `poll` and `install_policy` (ADR moves
//!   them behind `test-fixtures`).
//! - Nearby (`nearby_*`, `advertise_nearby_workspace`, ...) is a later slice.

use std::sync::Arc;
use std::time::Duration;

use arachne_api::{AttemptId, EndpointId, RecordId, WorkspaceId};

use super::candidates::{
    AdmissionCandidate, InvitationCandidate, JoinCandidate, PublicationCandidate,
    ReceptionCandidate, RecoveryCandidate, RecoveryStage, Staged,
};
use super::types::*;
use super::{ApiError, Client};

impl Client {
    fn admission_candidate(
        &self,
        staged: arachne_runtime::WorkspaceCandidate,
    ) -> Arc<AdmissionCandidate> {
        Arc::new(AdmissionCandidate {
            workspace: workspace(staged.workspace),
            staged: Staged::new(self.token, staged.snapshot),
        })
    }
}

// ---------------------------------------------------------------------------
// Workspace, state, metrics, lifecycle knobs.

#[uniffi::export]
impl Client {
    /// Create a workspace with this client as its first administrator.
    pub fn create_workspace(
        &self,
        display_name: String,
        workspace_name: Option<String>,
    ) -> Result<WorkspaceInfo, ApiError> {
        Ok(self
            .inner
            .create_workspace(&display_name, workspace_name.as_deref())?
            .into())
    }

    pub fn member_roster(&self) -> Result<MemberRoster, ApiError> {
        Ok(self.inner.member_roster()?.into())
    }

    /// Mark this client's workspace profile as a service (no extra rights).
    pub fn use_service_profile(&self) -> Result<(), ApiError> {
        Ok(self.inner.use_service_profile()?)
    }

    /// Local counters for diagnostics. Do not export them as telemetry.
    pub fn metrics(&self) -> Result<WorkspaceMetrics, ApiError> {
        Ok(self.inner.metrics()?.into())
    }

    /// Give each later blocking op this deadline (`None`: no deadline). At
    /// the deadline the op fails with `DeadlineExceeded`.
    pub fn set_deadline(&self, deadline_ms: Option<u64>) {
        self.inner
            .set_deadline(deadline_ms.map(Duration::from_millis));
    }

    /// Serve one queued peer-control exchange. `true`: one was served.
    pub fn poll_control(&self) -> Result<bool, ApiError> {
        Ok(self.inner.poll_control()?)
    }

    /// Rebind sockets after the device network changed.
    pub fn network_change(&self) -> Result<(), ApiError> {
        Ok(self.inner.network_change()?)
    }

    /// Tell the transport where `peer` can be reached (`host:port`).
    pub fn add_address_hint(&self, peer: EndpointId, address: String) -> Result<(), ApiError> {
        Ok(self.inner.add_address_hint(peer.to_bytes(), &address)?)
    }
}

// ---------------------------------------------------------------------------
// Invitations, join and admission.

#[uniffi::export]
impl Client {
    /// Register an invitation link. `expires_at` is Unix seconds; 0 never expires.
    pub fn stage_invitation(
        &self,
        expires_at: u64,
        kind: InvitationKind,
    ) -> Result<Arc<InvitationCandidate>, ApiError> {
        let staged = self.inner.stage_invitation_of(expires_at, kind.into())?;
        Ok(Arc::new(InvitationCandidate {
            workspace: workspace(staged.workspace),
            staged: Staged::new(self.token, staged.snapshot),
        }))
    }

    /// Adopt a staged invitation and get its bearer link.
    pub fn adopt_invitation(
        &self,
        candidate: Arc<InvitationCandidate>,
    ) -> Result<InvitationInfo, ApiError> {
        let snapshot = candidate.staged.take(self.token)?;
        Ok(self.inner.adopt_invitation(&snapshot)?.into())
    }

    /// Check an invitation link against its checkpoint without joining.
    pub fn inspect_invitation(
        &self,
        invitation: Vec<u8>,
        checkpoint: Vec<u8>,
    ) -> Result<InvitationDetails, ApiError> {
        Ok(self
            .inner
            .inspect_invitation(&invitation, &checkpoint)?
            .into())
    }

    /// The registered invitation links (read only).
    pub fn invitation_controls(&self) -> Result<Vec<InvitationControl>, ApiError> {
        Ok(self
            .inner
            .invitation_controls()?
            .into_iter()
            .map(Into::into)
            .collect())
    }

    /// Fetch the current checkpoint of a compact invitation from up to three members.
    pub fn fetch_invitation_checkpoint(
        &self,
        invitation: Vec<u8>,
        peers: Vec<EndpointId>,
    ) -> Result<InvitationCheckpoint, ApiError> {
        let peers: Vec<[u8; 32]> = peers.iter().map(|p| p.to_bytes()).collect();
        let found = self
            .inner
            .fetch_invitation_checkpoint(&invitation, &peers)?;
        Ok(InvitationCheckpoint {
            workspace: workspace(found.workspace),
            checkpoint: found.checkpoint,
            peer: endpoint(found.peer),
        })
    }

    /// Start joining. Send `admission_request` of the result to a member.
    pub fn begin_join(
        &self,
        invitation: Vec<u8>,
        checkpoint: Vec<u8>,
        display_name: String,
        peers: Vec<EndpointId>,
    ) -> Result<JoinRequest, ApiError> {
        let peers: Vec<[u8; 32]> = peers.iter().map(|p| p.to_bytes()).collect();
        Ok(self
            .inner
            .begin_join_with_peers(&invitation, &checkpoint, &display_name, &peers)?
            .into())
    }

    /// Stage the join from the member's welcome and admission commits.
    pub fn stage_join(
        &self,
        welcome: Vec<u8>,
        commits: Vec<JoinAdmissionStep>,
    ) -> Result<Arc<JoinCandidate>, ApiError> {
        let commits: Vec<arachne_runtime::JoinAdmissionStep> =
            commits.into_iter().map(Into::into).collect();
        let staged = self.inner.stage_join(&welcome, &commits)?;
        Ok(Arc::new(JoinCandidate {
            workspace: workspace(staged.workspace),
            staged: Staged::new(self.token, staged.snapshot),
        }))
    }

    pub fn adopt_join(&self, candidate: Arc<JoinCandidate>) -> Result<WorkspaceInfo, ApiError> {
        let snapshot = candidate.staged.take(self.token)?;
        Ok(self.inner.adopt_join(&snapshot)?.into())
    }

    /// Stage the admission of a joiner. `authenticated_endpoint` is the
    /// endpoint the request came from.
    pub fn stage_admission(
        &self,
        authenticated_endpoint: EndpointId,
        request: Vec<u8>,
    ) -> Result<Arc<AdmissionCandidate>, ApiError> {
        let staged = self
            .inner
            .stage_admission(authenticated_endpoint.to_bytes(), &request)?;
        Ok(self.admission_candidate(staged))
    }

    /// Approve (bind) a personal invitation for one join request.
    pub fn stage_invitation_approval(
        &self,
        request: Vec<u8>,
        attempt_id: Option<AttemptId>,
    ) -> Result<Arc<AdmissionCandidate>, ApiError> {
        let staged = self
            .inner
            .stage_invitation_approval(&request, attempt_id.map(|a| a.to_bytes()))?;
        Ok(self.admission_candidate(staged))
    }

    /// Decline a personal invitation request.
    pub fn stage_invitation_decline(
        &self,
        request: Vec<u8>,
        attempt_id: Option<AttemptId>,
    ) -> Result<Arc<AdmissionCandidate>, ApiError> {
        let staged = self
            .inner
            .stage_invitation_decline(&request, attempt_id.map(|a| a.to_bytes()))?;
        Ok(self.admission_candidate(staged))
    }

    pub fn adopt_admission(
        &self,
        candidate: Arc<AdmissionCandidate>,
    ) -> Result<WorkspaceInfo, ApiError> {
        let snapshot = candidate.staged.take(self.token)?;
        Ok(self.inner.adopt_admission(&snapshot)?.into())
    }

    /// The retained answer for an adopted admission, to send to the joiner.
    pub fn retained_admission(
        &self,
        authenticated_endpoint: EndpointId,
        request: Vec<u8>,
    ) -> Result<AdmissionReply, ApiError> {
        Ok(self
            .inner
            .retained_admission(authenticated_endpoint.to_bytes(), &request)?
            .into())
    }

    /// One page of requests that wait for an administrator (`limit` 1-64).
    pub fn admission_approvals(
        &self,
        after: Option<AttemptId>,
        limit: Option<u32>,
    ) -> Result<AdmissionApprovalPage, ApiError> {
        Ok(self
            .inner
            .admission_approvals(after.map(|a| a.to_bytes()), limit.map(|l| l as usize))?
            .into())
    }

    /// Mark a pending approval as seen.
    pub fn acknowledge_admission_approval(&self, attempt_id: AttemptId) -> Result<(), ApiError> {
        Ok(self
            .inner
            .acknowledge_admission_approval(attempt_id.to_bytes())?)
    }

    /// Answer the held exchange after its transition is durable.
    /// `false`: the requester expired.
    pub fn send_admission_reply(&self) -> Result<bool, ApiError> {
        Ok(self.inner.send_admission_reply()?)
    }
}

// ---------------------------------------------------------------------------
// Policy, interest and protected publication.

#[uniffi::export]
impl Client {
    /// Route every topic between all members at `revision` (epoch + 1).
    pub fn install_workspace_policy(&self, revision: u64) -> Result<(), ApiError> {
        Ok(self.inner.install_workspace_policy(revision)?)
    }

    /// Route only `topics` between all members at `revision`.
    pub fn install_member_policy(
        &self,
        revision: u64,
        topics: Vec<String>,
    ) -> Result<(), ApiError> {
        let topics: Vec<&str> = topics.iter().map(String::as_str).collect();
        Ok(self.inner.install_member_policy(revision, &topics)?)
    }

    pub fn set_interest(
        &self,
        workspace: WorkspaceId,
        revision: u64,
        topic: String,
        subscribed: bool,
    ) -> Result<(), ApiError> {
        Ok(self
            .inner
            .set_interest(workspace.to_bytes(), revision, &topic, subscribed)?)
    }

    /// The settled result of `set_interest`, or `None` while it is pending.
    pub fn poll_interest(&self) -> Result<Option<InterestObservation>, ApiError> {
        Ok(self.inner.poll_interest()?.map(Into::into))
    }

    /// Stage an encrypted publication for the workspace members.
    pub fn stage_protected_publication(
        &self,
        workspace: WorkspaceId,
        revision: u64,
        topic: String,
        id: RecordId,
        payload: Vec<u8>,
        current: Option<PublicationCurrent>,
    ) -> Result<Arc<PublicationCandidate>, ApiError> {
        let staged = self.inner.stage_protected_publication_with_current(
            workspace.to_bytes(),
            revision,
            &topic,
            id.to_bytes(),
            payload,
            current.map(Into::into),
        )?;
        Ok(Arc::new(PublicationCandidate {
            workspace: super::types::workspace(staged.workspace),
            staged: Staged::new(self.token, staged.snapshot),
        }))
    }

    /// Adopt and send a staged publication.
    pub fn adopt_protected_publication(
        &self,
        candidate: Arc<PublicationCandidate>,
    ) -> Result<DeliveryReport, ApiError> {
        let snapshot = candidate.staged.take(self.token)?;
        Ok(self.inner.adopt_protected_publication(&snapshot)?.into())
    }
}

// ---------------------------------------------------------------------------
// Receive and the durable inbox.

#[uniffi::export]
impl Client {
    /// Stage one incoming protected publication. The plaintext stays hidden
    /// until the candidate is adopted; then read it with `poll_pending_object`.
    pub fn poll_protected(&self) -> Result<Option<Arc<ReceptionCandidate>>, ApiError> {
        Ok(self.inner.poll_protected()?.map(|staged| {
            Arc::new(ReceptionCandidate {
                workspace: workspace(staged.workspace),
                staged: Staged::new(self.token, staged.snapshot),
            })
        }))
    }

    /// Adopt a reception, acknowledgement or rejection.
    pub fn adopt_protected_reception(
        &self,
        candidate: Arc<ReceptionCandidate>,
    ) -> Result<(), ApiError> {
        let snapshot = candidate.staged.take(self.token)?;
        Ok(self.inner.adopt_protected_reception(&snapshot)?)
    }

    /// The next object that the application has not acknowledged or rejected.
    pub fn poll_pending_object(&self) -> Result<Option<ReceivedPublication>, ApiError> {
        Ok(self.inner.poll_pending_object()?.map(Into::into))
    }

    /// Stage the application's acceptance of a pending object.
    pub fn stage_object_acknowledgement(
        &self,
        object: ReceivedPublication,
    ) -> Result<Arc<ReceptionCandidate>, ApiError> {
        let staged = self.inner.stage_object_acknowledgement(&(&object).into())?;
        Ok(Arc::new(ReceptionCandidate {
            workspace: workspace(staged.workspace),
            staged: Staged::new(self.token, staged.snapshot),
        }))
    }

    /// Stage a permanent rejection of a pending object; it never comes back.
    pub fn stage_object_rejection(
        &self,
        object: ReceivedPublication,
    ) -> Result<Arc<ReceptionCandidate>, ApiError> {
        let staged = self.inner.stage_object_rejection(&(&object).into())?;
        Ok(Arc::new(ReceptionCandidate {
            workspace: workspace(staged.workspace),
            staged: Staged::new(self.token, staged.snapshot),
        }))
    }
}

// ---------------------------------------------------------------------------
// Recovery.

#[uniffi::export]
impl Client {
    /// Ask a peer for a range of an author's objects. `epoch`: an earlier
    /// author epoch still in the receive window (`None`: current).
    pub fn fetch_recovery_range(
        &self,
        request: RecoveryRangeRequest,
        epoch: Option<u64>,
    ) -> Result<RecoveryRangeStatus, ApiError> {
        Ok(self
            .inner
            .fetch_recovery_range_at(request.into(), epoch)?
            .into())
    }

    /// The fetch result once it changed, or `None`.
    pub fn poll_recovery_range(&self) -> Result<Option<RecoveryRangeStatus>, ApiError> {
        Ok(self.inner.poll_recovery_range()?.map(Into::into))
    }

    pub fn cancel_recovery_range(&self) -> Result<(), ApiError> {
        Ok(self.inner.cancel_recovery_range()?)
    }

    /// Stage a ready range. `retain_until` is Unix seconds; 0 keeps no copy
    /// for third-party recovery.
    pub fn stage_recovery_range(&self, retain_until: u64) -> Result<RecoveryStage, ApiError> {
        use arachne_runtime::RecoveryStage as S;
        Ok(match self.inner.stage_recovery_range(retain_until)? {
            S::Candidate(candidate) => RecoveryStage::Candidate {
                candidate: Arc::new(RecoveryCandidate {
                    workspace: workspace(candidate.workspace),
                    publication_count: count(candidate.publication_count),
                    durable: candidate.durable,
                    staged: Staged::new(self.token, candidate.snapshot),
                }),
            },
            S::AlreadyCovered => RecoveryStage::AlreadyCovered,
            S::NoNewObjects => RecoveryStage::NoNewObjects,
            S::AwaitingApplication => RecoveryStage::AwaitingApplication,
        })
    }

    /// Adopt a staged range. Recovered objects wait in the inbox.
    pub fn adopt_recovery(
        &self,
        candidate: Arc<RecoveryCandidate>,
    ) -> Result<RecoveryAdoption, ApiError> {
        let snapshot = candidate.staged.take(self.token)?;
        Ok(self.inner.adopt_recovery(&snapshot)?.into())
    }
}

// ---------------------------------------------------------------------------
// Presence.

#[uniffi::export]
impl Client {
    /// One presence round with the members. `announce` marks a restart.
    pub fn poll_presence(&self, announce: bool) -> Result<PresenceRound, ApiError> {
        let round = self.inner.poll_presence(announce)?;
        Ok(PresenceRound {
            sync_peer: round.sync_peer.map(endpoint),
            response_errors: round.response_errors,
            response_error: round.response_error,
        })
    }
}

// ---------------------------------------------------------------------------
// The process default context.

/// Stop background work of every client in the default context (for an app
/// in the background). Clients stay open and ops still run.
#[uniffi::export]
pub fn suspend() -> Result<(), ApiError> {
    Ok(arachne_runtime::Context::default_shared()?.suspend()?)
}

/// Restart what `suspend` stopped and rebind sockets.
#[uniffi::export]
pub fn resume() -> Result<(), ApiError> {
    Ok(arachne_runtime::Context::default_shared()?.resume()?)
}

/// The default context is suspended.
#[uniffi::export]
pub fn is_suspended() -> Result<bool, ApiError> {
    Ok(arachne_runtime::Context::default_shared()?.is_suspended())
}

#[cfg(test)]
mod tests {
    use std::thread;
    use std::time::Instant;

    use super::super::{ClientConfig, ErrorCode, Network};
    use super::*;

    fn open(seed: u8) -> Arc<Client> {
        Client::open(ClientConfig {
            network: Network::Direct,
            secret: Some(vec![seed; 32]),
            deadline_ms: None,
        })
        .unwrap()
    }

    fn local(address: &str) -> String {
        address.replace("0.0.0.0:", "127.0.0.1:")
    }

    fn until<T>(what: &str, mut step: impl FnMut() -> Option<T>) -> T {
        let deadline = Instant::now() + Duration::from_secs(15);
        loop {
            if let Some(value) = step() {
                return value;
            }
            assert!(Instant::now() < deadline, "{what} timed out");
            thread::sleep(Duration::from_millis(10));
        }
    }

    /// invitation -> join -> publish -> recovery -> ack; then interest ->
    /// publish -> protected receive -> reject. Also candidate misuse.
    #[test]
    fn two_clients_invite_join_publish_recover_receive() {
        let owner = open(0x51);
        let reader = open(0x52);
        let created = owner
            .create_workspace("Owner".into(), Some("UniFFI flow".into()))
            .unwrap();

        // Invitations.
        let staged = owner.stage_invitation(0, InvitationKind::Reusable).unwrap();
        let other = open(0x53);
        assert_eq!(
            other.adopt_invitation(staged.clone()).unwrap_err().code(),
            ErrorCode::WrongState,
            "a candidate is bound to its client"
        );
        let invitation = owner.adopt_invitation(staged.clone()).unwrap();
        assert!(staged.is_used());
        assert_eq!(
            owner.adopt_invitation(staged).unwrap_err().code(),
            ErrorCode::WrongState,
            "a candidate is single use"
        );
        other.close().unwrap();
        let details = reader
            .inspect_invitation(invitation.invitation.clone(), invitation.checkpoint.clone())
            .unwrap();
        assert_eq!(details.workspace, created.workspace);
        assert_eq!(owner.invitation_controls().unwrap().len(), 1);

        // Join and admission.
        reader
            .add_address_hint(invitation.peer, local(&invitation.address))
            .unwrap();
        let join = reader
            .begin_join(
                invitation.invitation,
                invitation.checkpoint,
                "Reader".into(),
                vec![],
            )
            .unwrap();
        let admission = owner
            .stage_admission(join.endpoint, join.admission_request.clone())
            .unwrap();
        let owner_view = owner.adopt_admission(admission).unwrap();
        let reply = owner
            .retained_admission(join.endpoint, join.admission_request)
            .unwrap();
        let joined = reader
            .stage_join(
                reply.welcome,
                vec![JoinAdmissionStep {
                    commit: reply.commit,
                    authorization: reply.authorization,
                }],
            )
            .unwrap();
        let reader_view = reader.adopt_join(joined).unwrap();
        assert_eq!(owner_view.epoch, reader_view.epoch);
        assert_eq!(reader_view.member_count, 2);
        let reader_endpoint = reader.describe().unwrap();
        owner
            .add_address_hint(
                reader_endpoint.endpoint_id,
                local(&reader_endpoint.bound_address),
            )
            .unwrap();

        // Policy and a publication the reader is not subscribed to.
        let revision = owner_view.epoch + 1;
        owner.install_workspace_policy(revision).unwrap();
        reader.install_workspace_policy(revision).unwrap();
        let first = owner
            .stage_protected_publication(
                created.workspace,
                revision,
                "streams/uniffi".into(),
                RecordId::from_bytes([1; 16]),
                b"first".to_vec(),
                None,
            )
            .unwrap();
        owner.adopt_protected_publication(first).unwrap();

        // Recovery pulls it.
        let author = owner
            .member_roster()
            .unwrap()
            .members
            .into_iter()
            .find(|m| m.self_member)
            .unwrap()
            .id;
        let owner_endpoint = owner.describe().unwrap().endpoint_id;
        reader
            .fetch_recovery_range(
                RecoveryRangeRequest {
                    peer: Some(owner_endpoint),
                    author: Some(author),
                    revision,
                    topics: vec!["streams/uniffi".into()],
                    after: Some(0),
                    through: Some(1),
                },
                None,
            )
            .unwrap();
        let ready = until("recovery range", || {
            owner.poll_control().unwrap();
            reader.poll_recovery_range().unwrap()
        });
        assert!(
            matches!(ready, RecoveryRangeStatus::Ready { .. }),
            "{ready:?}"
        );
        let RecoveryStage::Candidate { candidate } = reader.stage_recovery_range(0).unwrap() else {
            panic!("no recovery candidate");
        };
        assert_eq!(candidate.publication_count(), 1);
        let adoption = reader.adopt_recovery(candidate).unwrap();
        assert_eq!(adoption.recovered_publications, 1);
        let recovered = reader.poll_pending_object().unwrap().unwrap();
        assert_eq!(recovered.payload, b"first");
        let ack = reader.stage_object_acknowledgement(recovered).unwrap();
        reader.adopt_protected_reception(ack).unwrap();
        assert_eq!(reader.poll_pending_object().unwrap(), None);

        // Interest, then a live protected receive, rejected.
        reader
            .set_interest(created.workspace, revision, "streams/uniffi".into(), true)
            .unwrap();
        let observed = until("interest", || reader.poll_interest().unwrap());
        assert!(observed.subscribed);
        let second = owner
            .stage_protected_publication(
                created.workspace,
                revision,
                "streams/uniffi".into(),
                RecordId::from_bytes([2; 16]),
                b"second".to_vec(),
                None,
            )
            .unwrap();
        let report = owner.adopt_protected_publication(second).unwrap();
        assert!(report.failed.is_empty(), "{report:?}");
        let reception = until("protected receive", || reader.poll_protected().unwrap());
        reader.adopt_protected_reception(reception).unwrap();
        let received = reader.poll_pending_object().unwrap().unwrap();
        assert_eq!(received.payload, b"second");
        assert_eq!(received.endpoint, owner_endpoint);
        let reject = reader.stage_object_rejection(received).unwrap();
        reader.adopt_protected_reception(reject).unwrap();
        assert_eq!(reader.poll_pending_object().unwrap(), None);

        // Presence, metrics, deadline, suspend/resume.
        let round = owner.poll_presence(false).unwrap();
        assert!(round.response_errors <= 1, "{round:?}");
        assert_eq!(reader.metrics().unwrap().workspace, created.workspace);
        reader.set_deadline(Some(5_000));
        suspend().unwrap();
        assert!(is_suspended().unwrap());
        resume().unwrap();
        assert!(!is_suspended().unwrap());

        reader.close().unwrap();
        owner.close().unwrap();
    }
}
