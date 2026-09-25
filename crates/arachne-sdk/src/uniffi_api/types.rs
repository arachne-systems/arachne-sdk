//! Records and enums of the exported surface.
//!
//! Most of these copy a runtime struct with two changes that UniFFI needs:
//! IDs are hex newtypes (not `[u8; 32]`) and counts are `u64` (not `usize`).
//! Each copy is a shim. Core step 6 makes the runtime types exportable, and
//! then these go away.

use arachne_api::{AttemptId, EndpointId, MemberId, RecordId, WorkspaceId};
use arachne_runtime as rt;
use arachne_runtime::{
    ConnectionCapacityMetrics, ControlTimingMetrics, DurationSummary, MemberKind,
    MembershipGossipMetrics, Presence,
};

use super::ApiError;

// SHIM: remove after core step 6 - opaque 32-byte keys (invitation key,
// current-value selector and replacement key, workspace name head) have no
// core ID newtype; this one crosses as lowercase hex like the core IDs.
/// An opaque 32-byte key, as 64 lowercase hex characters.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub struct Key32(pub [u8; 32]);

impl Key32 {
    fn to_hex(self) -> String {
        self.0.iter().map(|byte| format!("{byte:02x}")).collect()
    }

    fn from_hex(text: &str) -> Result<Self, ApiError> {
        let invalid = |reason: &str| ApiError::InvalidId {
            kind: "key".into(),
            reason: reason.into(),
        };
        if text.len() != 64 {
            return Err(invalid("must be 64 hex characters"));
        }
        let mut bytes = [0u8; 32];
        for (index, byte) in bytes.iter_mut().enumerate() {
            let pair = text
                .get(index * 2..index * 2 + 2)
                .ok_or_else(|| invalid("must be ASCII hex"))?;
            if pair.bytes().any(|c| c.is_ascii_uppercase()) {
                return Err(invalid("must be lowercase hex"));
            }
            *byte = u8::from_str_radix(pair, 16).map_err(|_| invalid("must be hex"))?;
        }
        Ok(Self(bytes))
    }
}

uniffi::custom_type!(Key32, String, {
    lower: |key| key.to_hex(),
    try_lift: |text| Ok(Key32::from_hex(&text)?),
});

/// `usize` counts cross as `u64`.
// SHIM: remove after core step 6 - UniFFI has no `usize`.
pub(crate) fn count(value: usize) -> u64 {
    value as u64
}

pub(crate) fn endpoint(bytes: [u8; 32]) -> EndpointId {
    EndpointId::from_bytes(bytes)
}

pub(crate) fn member(bytes: [u8; 32]) -> MemberId {
    MemberId::from_bytes(bytes)
}

pub(crate) fn workspace(bytes: [u8; 32]) -> WorkspaceId {
    WorkspaceId::from_bytes(bytes)
}

// ---------------------------------------------------------------------------
// Workspace and members.

/// A workspace this client created or joined.
#[derive(Clone, Debug, PartialEq, Eq, uniffi::Record)]
pub struct WorkspaceInfo {
    pub workspace: WorkspaceId,
    pub workspace_name: Option<String>,
    pub epoch: u64,
    pub member_count: u64,
    pub durable: bool,
    pub phase: rt::WorkspacePhase,
    pub reason: Option<String>,
}

impl From<rt::WorkspaceInfo> for WorkspaceInfo {
    fn from(info: rt::WorkspaceInfo) -> Self {
        Self {
            workspace: workspace(info.workspace),
            workspace_name: info.workspace_name,
            epoch: info.epoch,
            member_count: count(info.member_count),
            durable: info.durable,
            phase: info.phase,
            reason: info.reason,
        }
    }
}

#[uniffi::remote(Enum)]
pub enum MemberKind {
    Person,
    Service,
}

#[uniffi::remote(Enum)]
pub enum Presence {
    SelfMember,
    Unknown,
    Reachable,
    Stale,
}

/// One member of the workspace.
#[derive(Clone, Debug, PartialEq, Eq, uniffi::Record)]
pub struct MemberInfo {
    pub id: MemberId,
    pub endpoint: EndpointId,
    pub administrator: bool,
    pub self_member: bool,
    pub display_name: Option<String>,
    pub kind: rt::MemberKind,
    pub presence: rt::Presence,
    pub last_contact_age_ms: Option<u64>,
    pub presence_fresh_for_ms: Option<u64>,
}

/// The workspace members.
#[derive(Clone, Debug, PartialEq, Eq, uniffi::Record)]
pub struct MemberRoster {
    pub workspace: WorkspaceId,
    pub workspace_name: Option<String>,
    pub workspace_name_revision: u64,
    pub workspace_name_head: Key32,
    pub epoch: u64,
    pub members: Vec<MemberInfo>,
    pub profile_count: u64,
    pub profiles_retained: bool,
}

impl From<rt::MemberRoster> for MemberRoster {
    fn from(roster: rt::MemberRoster) -> Self {
        Self {
            workspace: workspace(roster.workspace),
            workspace_name: roster.workspace_name,
            workspace_name_revision: roster.workspace_name_revision,
            workspace_name_head: Key32(roster.workspace_name_head),
            epoch: roster.epoch,
            members: roster
                .members
                .into_iter()
                .map(|m| MemberInfo {
                    id: member(m.id),
                    endpoint: endpoint(m.endpoint),
                    administrator: m.administrator,
                    self_member: m.self_member,
                    display_name: m.display_name,
                    kind: m.kind,
                    presence: m.presence,
                    last_contact_age_ms: m.last_contact_age_ms,
                    presence_fresh_for_ms: m.presence_fresh_for_ms,
                })
                .collect(),
            profile_count: count(roster.profile_count),
            profiles_retained: roster.profiles_retained,
        }
    }
}

// ---------------------------------------------------------------------------
// Invitations, join and admission.

// SHIM: remove after core step 6 - `rt::InvitationKind` is non_exhaustive.
/// The kind of invitation link to register.
#[derive(Clone, Copy, Debug, PartialEq, Eq, uniffi::Enum)]
pub enum InvitationKind {
    /// Anyone with the link may join until it expires or is disabled.
    Reusable,
    /// One person; an administrator approves the first join request.
    Personal,
    /// One person; the first join request is approved automatically.
    PersonalAutomatic,
    /// One person asks for access; an administrator approves or declines.
    RequestAccess,
}

impl From<InvitationKind> for rt::InvitationKind {
    fn from(kind: InvitationKind) -> Self {
        match kind {
            InvitationKind::Reusable => Self::Reusable,
            InvitationKind::Personal => Self::Personal,
            InvitationKind::PersonalAutomatic => Self::PersonalAutomatic,
            InvitationKind::RequestAccess => Self::RequestAccess,
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq, uniffi::Record)]
pub struct RouteHint {
    pub peer: EndpointId,
    pub address: String,
}

/// An adopted invitation link. `invitation` and `checkpoint` are secret
/// bearer material: give them only to the invited person.
#[derive(Clone, Debug, PartialEq, Eq, uniffi::Record)]
pub struct InvitationInfo {
    pub workspace: WorkspaceId,
    pub workspace_name: Option<String>,
    pub invitation: Vec<u8>,
    pub invitation_key: Key32,
    pub checkpoint: Vec<u8>,
    pub peer: EndpointId,
    pub bootstrap_peers: Vec<EndpointId>,
    pub address: String,
    pub routes: Vec<RouteHint>,
}

impl From<rt::InvitationInfo> for InvitationInfo {
    fn from(info: rt::InvitationInfo) -> Self {
        Self {
            workspace: workspace(info.workspace),
            workspace_name: info.workspace_name,
            invitation: info.invitation,
            invitation_key: Key32(info.invitation_key),
            checkpoint: info.checkpoint,
            peer: endpoint(info.peer),
            bootstrap_peers: info.bootstrap_peers.into_iter().map(endpoint).collect(),
            address: info.address,
            routes: info
                .routes
                .into_iter()
                .map(|route| RouteHint {
                    peer: endpoint(route.peer),
                    address: route.address,
                })
                .collect(),
        }
    }
}

/// What an invitation link grants, checked against its checkpoint.
#[derive(Clone, Debug, PartialEq, Eq, uniffi::Record)]
pub struct InvitationDetails {
    pub workspace: WorkspaceId,
    pub invitation_key: Key32,
    pub workspace_name: Option<String>,
    pub epoch: u64,
    pub personal: bool,
    pub automatic: bool,
    pub expires_at: u64,
}

impl From<rt::InvitationDetails> for InvitationDetails {
    fn from(details: rt::InvitationDetails) -> Self {
        Self {
            workspace: workspace(details.workspace),
            invitation_key: Key32(details.invitation_key),
            workspace_name: details.workspace_name,
            epoch: details.epoch,
            personal: details.personal,
            automatic: details.automatic,
            expires_at: details.expires_at,
        }
    }
}

/// One registered invitation link (read only; changing it is management).
#[derive(Clone, Debug, PartialEq, Eq, uniffi::Record)]
pub struct InvitationControl {
    pub number: u64,
    pub key: Key32,
    pub expires_at: u64,
    pub enabled: bool,
    pub personal: bool,
    pub automatic: bool,
    pub request_access: bool,
    pub approved: bool,
}

impl From<rt::InvitationControl> for InvitationControl {
    fn from(row: rt::InvitationControl) -> Self {
        Self {
            number: count(row.number),
            key: Key32(row.key),
            expires_at: row.expires_at,
            enabled: row.enabled,
            personal: row.personal,
            automatic: row.automatic,
            request_access: row.request_access,
            approved: row.approved,
        }
    }
}

/// A verified invitation checkpoint and the member that served it.
#[derive(Clone, Debug, PartialEq, Eq, uniffi::Record)]
pub struct InvitationCheckpoint {
    pub workspace: WorkspaceId,
    pub checkpoint: Vec<u8>,
    pub peer: EndpointId,
}

/// A started join. Send `admission_request` to a member.
#[derive(Clone, Debug, PartialEq, Eq, uniffi::Record)]
pub struct JoinRequest {
    pub workspace: WorkspaceId,
    pub member: MemberId,
    pub endpoint: EndpointId,
    pub admission_request: Vec<u8>,
}

impl From<rt::JoinRequest> for JoinRequest {
    fn from(request: rt::JoinRequest) -> Self {
        Self {
            workspace: workspace(request.workspace),
            member: member(request.member),
            endpoint: endpoint(request.endpoint),
            admission_request: request.admission_request,
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq, uniffi::Record)]
pub struct AdmissionAuthorization {
    pub invitation_key: Key32,
    pub grant_signature: Vec<u8>,
    pub redemption_signature: Vec<u8>,
}

/// One admission commit for `stage_join`.
#[derive(Clone, Debug, PartialEq, Eq, uniffi::Record)]
pub struct JoinAdmissionStep {
    pub commit: Vec<u8>,
    pub authorization: AdmissionAuthorization,
}

impl From<JoinAdmissionStep> for rt::JoinAdmissionStep {
    fn from(step: JoinAdmissionStep) -> Self {
        Self {
            commit: step.commit,
            authorization: rt::AdmissionAuthorization {
                invitation_key: step.authorization.invitation_key.0,
                grant_signature: step.authorization.grant_signature,
                redemption_signature: step.authorization.redemption_signature,
            },
        }
    }
}

/// The member's answer to a join request: pass `welcome` and a step made of
/// `commit` and `authorization` to `stage_join`.
#[derive(Clone, Debug, PartialEq, Eq, uniffi::Record)]
pub struct AdmissionReply {
    pub workspace: WorkspaceId,
    pub epoch: u64,
    pub commit: Vec<u8>,
    pub welcome: Vec<u8>,
    pub authorization: AdmissionAuthorization,
}

impl From<rt::AdmissionReply> for AdmissionReply {
    fn from(reply: rt::AdmissionReply) -> Self {
        Self {
            workspace: workspace(reply.workspace),
            epoch: reply.epoch,
            commit: reply.commit,
            welcome: reply.welcome,
            authorization: AdmissionAuthorization {
                invitation_key: Key32(reply.authorization.invitation_key),
                grant_signature: reply.authorization.grant_signature,
                redemption_signature: reply.authorization.redemption_signature,
            },
        }
    }
}

/// One admission request that waits for an administrator.
#[derive(Clone, Debug, PartialEq, Eq, uniffi::Record)]
pub struct AdmissionApproval {
    pub attempt_id: AttemptId,
    pub endpoint: EndpointId,
    pub request: Vec<u8>,
    pub display_name: Option<String>,
    pub automatic: bool,
    pub delivered: bool,
    pub acknowledged: bool,
}

/// One page of pending approvals. Pass `next_after` for the next page.
#[derive(Clone, Debug, PartialEq, Eq, uniffi::Record)]
pub struct AdmissionApprovalPage {
    pub approvals: Vec<AdmissionApproval>,
    pub complete: bool,
    pub next_after: Option<AttemptId>,
}

impl From<rt::AdmissionApprovalPage> for AdmissionApprovalPage {
    fn from(page: rt::AdmissionApprovalPage) -> Self {
        Self {
            approvals: page
                .approvals
                .into_iter()
                .map(|row| AdmissionApproval {
                    attempt_id: AttemptId::from_bytes(row.attempt_id),
                    endpoint: endpoint(row.endpoint),
                    request: row.request,
                    display_name: row.display_name,
                    automatic: row.automatic,
                    delivered: row.delivered,
                    acknowledged: row.acknowledged,
                })
                .collect(),
            complete: page.complete,
            next_after: page.next_after.map(AttemptId::from_bytes),
        }
    }
}

// ---------------------------------------------------------------------------
// Publication and receive.

#[derive(Clone, Debug, PartialEq, Eq, uniffi::Record)]
pub struct DeliveryFailure {
    pub peer: EndpointId,
    pub error: String,
}

/// Where a publication or interest change went.
#[derive(Clone, Debug, PartialEq, Eq, uniffi::Record)]
pub struct DeliveryReport {
    pub admitted: Vec<EndpointId>,
    pub queued: bool,
    pub failed: Vec<DeliveryFailure>,
}

impl From<rt::DeliveryReport> for DeliveryReport {
    fn from(report: rt::DeliveryReport) -> Self {
        Self {
            admitted: report.admitted.into_iter().map(endpoint).collect(),
            queued: report.queued,
            failed: report
                .failed
                .into_iter()
                .map(|failure| DeliveryFailure {
                    peer: endpoint(failure.peer),
                    error: failure.error,
                })
                .collect(),
        }
    }
}

/// Current-value (latest-value) metadata of a protected publication.
#[derive(Clone, Copy, Debug, PartialEq, Eq, uniffi::Record)]
pub struct PublicationCurrent {
    pub selector: Key32,
    pub replacement_key: Key32,
    /// Unix seconds (UTC), by the author's clock.
    pub expires_at: u64,
    pub tombstone: bool,
}

impl From<PublicationCurrent> for rt::PublicationCurrent {
    fn from(current: PublicationCurrent) -> Self {
        Self {
            selector: current.selector.0,
            replacement_key: current.replacement_key.0,
            expires_at: current.expires_at,
            tombstone: current.tombstone,
        }
    }
}

impl From<rt::PublicationCurrent> for PublicationCurrent {
    fn from(current: rt::PublicationCurrent) -> Self {
        Self {
            selector: Key32(current.selector),
            replacement_key: Key32(current.replacement_key),
            expires_at: current.expires_at,
            tombstone: current.tombstone,
        }
    }
}

/// An authenticated object in the durable inbox. It stays pending until an
/// acknowledgement or rejection is adopted (at-least-once delivery).
#[derive(Clone, Debug, PartialEq, Eq, uniffi::Record)]
pub struct ReceivedPublication {
    pub workspace: WorkspaceId,
    pub revision: u64,
    pub member: MemberId,
    pub endpoint: EndpointId,
    pub topic: String,
    pub id: RecordId,
    pub sequence: Option<u64>,
    pub payload: Vec<u8>,
    pub recipients: Vec<MemberId>,
    /// Author sender counter; identifies the object for acknowledgement.
    pub counter: u64,
    pub current: Option<PublicationCurrent>,
}

impl From<rt::ReceivedProtectedPublication> for ReceivedPublication {
    fn from(object: rt::ReceivedProtectedPublication) -> Self {
        Self {
            workspace: workspace(object.workspace),
            revision: object.revision,
            member: member(object.member),
            endpoint: endpoint(object.endpoint),
            topic: object.topic,
            id: RecordId::from_bytes(object.id),
            sequence: object.sequence,
            payload: object.payload,
            recipients: object.recipients.into_iter().map(member).collect(),
            counter: object.counter,
            current: object.current.map(Into::into),
        }
    }
}

impl From<&ReceivedPublication> for rt::ReceivedProtectedPublication {
    fn from(object: &ReceivedPublication) -> Self {
        Self {
            workspace: object.workspace.to_bytes(),
            revision: object.revision,
            member: object.member.to_bytes(),
            endpoint: object.endpoint.to_bytes(),
            topic: object.topic.clone(),
            id: object.id.to_bytes(),
            sequence: object.sequence,
            payload: object.payload.clone(),
            recipients: object.recipients.iter().map(|m| m.to_bytes()).collect(),
            counter: object.counter,
            current: object.current.map(Into::into),
        }
    }
}

/// The settled result of `set_interest`.
#[derive(Clone, Debug, PartialEq, Eq, uniffi::Record)]
pub struct InterestObservation {
    pub workspace: WorkspaceId,
    pub revision: u64,
    pub topic: String,
    pub subscribed: bool,
    pub admission: DeliveryReport,
}

impl From<rt::InterestObservation> for InterestObservation {
    fn from(observation: rt::InterestObservation) -> Self {
        Self {
            workspace: workspace(observation.workspace),
            revision: observation.revision,
            topic: observation.topic,
            subscribed: observation.subscribed,
            admission: observation.admission.into(),
        }
    }
}

// ---------------------------------------------------------------------------
// Recovery.

/// Which range of an author's objects to fetch from a peer.
#[derive(Clone, Debug, PartialEq, Eq, uniffi::Record)]
pub struct RecoveryRangeRequest {
    #[uniffi(default = None)]
    pub peer: Option<EndpointId>,
    #[uniffi(default = None)]
    pub author: Option<MemberId>,
    pub revision: u64,
    pub topics: Vec<String>,
    #[uniffi(default = None)]
    pub after: Option<u64>,
    #[uniffi(default = None)]
    pub through: Option<u64>,
}

impl From<RecoveryRangeRequest> for rt::RecoveryRangeRequest {
    fn from(request: RecoveryRangeRequest) -> Self {
        Self {
            peer: request.peer.map(|p| p.to_bytes()),
            author: request.author.map(|a| a.to_bytes()),
            revision: request.revision,
            topics: request.topics,
            after: request.after,
            through: request.through,
        }
    }
}

/// A fetched range, ready to stage.
#[derive(Clone, Debug, PartialEq, Eq, uniffi::Record)]
pub struct RecoveryRangeReady {
    pub workspace: WorkspaceId,
    pub author: MemberId,
    pub peer: EndpointId,
    pub epoch: u64,
    pub revision: u64,
    pub after: u64,
    pub through: u64,
    pub packet_count: u64,
    pub retained_bytes: u64,
    pub automatic_source: bool,
    pub attempted: Option<u64>,
}

// SHIM: remove after core step 6 - the runtime enum carries `usize` fields.
/// The state of a recovery range fetch.
#[derive(Clone, Debug, PartialEq, Eq, uniffi::Enum)]
pub enum RecoveryRangeStatus {
    Pending {
        candidate_count: u64,
        automatic_source: bool,
    },
    Ready {
        range: RecoveryRangeReady,
    },
    SourceWaiting {
        automatic_source: bool,
    },
    SourceUnavailable {
        attempted: u64,
        reason: String,
        automatic_source: bool,
    },
    Rejected {
        reason: String,
    },
    Cancelled,
}

impl From<rt::RecoveryRangeStatus> for RecoveryRangeStatus {
    fn from(status: rt::RecoveryRangeStatus) -> Self {
        use rt::RecoveryRangeStatus as S;
        match status {
            S::Pending {
                candidate_count,
                automatic_source,
            } => Self::Pending {
                candidate_count: count(candidate_count),
                automatic_source,
            },
            S::Ready(ready) => Self::Ready {
                range: RecoveryRangeReady {
                    workspace: workspace(ready.workspace),
                    author: member(ready.author),
                    peer: endpoint(ready.peer),
                    epoch: ready.epoch,
                    revision: ready.revision,
                    after: ready.after,
                    through: ready.through,
                    packet_count: count(ready.packet_count),
                    retained_bytes: count(ready.retained_bytes),
                    automatic_source: ready.automatic_source,
                    attempted: ready.attempted.map(count),
                },
            },
            S::SourceWaiting { automatic_source } => Self::SourceWaiting { automatic_source },
            S::SourceUnavailable {
                attempted,
                reason,
                automatic_source,
            } => Self::SourceUnavailable {
                attempted: count(attempted),
                reason,
                automatic_source,
            },
            S::Rejected { reason } => Self::Rejected { reason },
            S::Cancelled => Self::Cancelled,
        }
    }
}

/// An adopted recovery. `missing_publications` counts objects the source
/// no longer had (a direct miss).
#[derive(Clone, Debug, PartialEq, Eq, uniffi::Record)]
pub struct RecoveryAdoption {
    pub workspace: WorkspaceId,
    pub epoch: u64,
    pub member_count: u64,
    pub durable: bool,
    pub recovered_publications: u64,
    pub missing_publications: u64,
}

impl From<rt::RecoveryAdoption> for RecoveryAdoption {
    fn from(adoption: rt::RecoveryAdoption) -> Self {
        Self {
            workspace: workspace(adoption.workspace),
            epoch: adoption.epoch,
            member_count: count(adoption.member_count),
            durable: adoption.durable,
            recovered_publications: count(adoption.recovered_publications),
            missing_publications: count(adoption.missing_publications),
        }
    }
}

// ---------------------------------------------------------------------------
// Presence and metrics.

/// The outcome of one presence round.
#[derive(Clone, Debug, PartialEq, Eq, uniffi::Record)]
pub struct PresenceRound {
    pub sync_peer: Option<EndpointId>,
    pub response_errors: u32,
    pub response_error: Option<String>,
}

// SHIM: remove after core step 6 - `RouteKind::Custom(String)` becomes a named field.
#[derive(Clone, Debug, PartialEq, Eq, uniffi::Enum)]
pub enum RouteKind {
    Direct,
    Relay,
    Tor,
    Custom { name: String },
}

#[derive(Clone, Debug, PartialEq, Eq, uniffi::Record)]
pub struct PeerRoute {
    pub member: MemberId,
    pub route: RouteKind,
    pub rtt_ms: u64,
}

#[uniffi::remote(Record)]
pub struct DurationSummary {
    pub count: u64,
    pub total_us: u64,
    pub max_us: u64,
}

#[uniffi::remote(Record)]
pub struct ControlTimingMetrics {
    pub inquiry: rt::DurationSummary,
    pub host_wait: rt::DurationSummary,
    pub host_service: rt::DurationSummary,
}

#[uniffi::remote(Record)]
pub struct MembershipGossipMetrics {
    pub sent: u64,
    pub no_overlay: u64,
    pub failed: u64,
    pub received: u64,
    pub staged: u64,
    pub rejected: u64,
    pub range_pulled: u64,
    pub range_failed: u64,
}

#[uniffi::remote(Record)]
pub struct ConnectionCapacityMetrics {
    pub evicted: u64,
    pub refused: u64,
}

/// A local, read-only snapshot of workspace counters (diagnostics only).
#[derive(Clone, Debug, PartialEq, Eq, uniffi::Record)]
pub struct WorkspaceMetrics {
    pub workspace: WorkspaceId,
    pub phase: rt::WorkspacePhase,
    pub reason: Option<String>,
    pub received_bytes: u64,
    pub sent_bytes: u64,
    pub receive_queue: u64,
    pub admission_queue: u64,
    pub admission_queue_bytes: u64,
    pub admission_waiters: u64,
    pub admission_in_flight: u64,
    pub approval_pending: u64,
    pub pending_objects: u64,
    pub repair_jobs: u64,
    pub gossip_neighbors: u64,
    pub control_timing: rt::ControlTimingMetrics,
    pub membership_gossip: rt::MembershipGossipMetrics,
    pub connection_capacity: rt::ConnectionCapacityMetrics,
    pub paths: Vec<PeerRoute>,
    pub paths_limited: bool,
}

impl From<rt::WorkspaceMetrics> for WorkspaceMetrics {
    fn from(m: rt::WorkspaceMetrics) -> Self {
        Self {
            workspace: workspace(m.workspace),
            phase: m.phase,
            reason: m.reason,
            received_bytes: m.received_bytes,
            sent_bytes: m.sent_bytes,
            receive_queue: count(m.receive_queue),
            admission_queue: count(m.admission_queue),
            admission_queue_bytes: count(m.admission_queue_bytes),
            admission_waiters: count(m.admission_waiters),
            admission_in_flight: count(m.admission_in_flight),
            approval_pending: count(m.approval_pending),
            pending_objects: count(m.pending_objects),
            repair_jobs: count(m.repair_jobs),
            gossip_neighbors: count(m.gossip_neighbors),
            control_timing: m.control_timing,
            membership_gossip: m.membership_gossip,
            connection_capacity: m.connection_capacity,
            paths: m
                .paths
                .into_iter()
                .map(|path| PeerRoute {
                    member: member(path.member),
                    route: match path.route {
                        rt::RouteKind::Direct => RouteKind::Direct,
                        rt::RouteKind::Relay => RouteKind::Relay,
                        rt::RouteKind::Tor => RouteKind::Tor,
                        rt::RouteKind::Custom(name) => RouteKind::Custom { name },
                    },
                    rtt_ms: path.rtt_ms,
                })
                .collect(),
            paths_limited: m.paths_limited,
        }
    }
}
