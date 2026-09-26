//! Adopter-facing Rust boundary for the portable Arachne fabric.
//!
//! This crate exposes the typed runtime seam explicitly. It does not copy core
//! implementation and it does not expose ATAK, Android, JNI, or transport
//! internals. The core dependency remains separately licensed under MPL-2.0.
//!
//! The repository's `docs/workflows.md` covers invitations, protected
//! publication and reception, persistence ordering, and current integration limits.

pub use arachne_api::{
    API_VERSION, ApiError, AttemptId, Capabilities, EndpointId, ErrorCode, Event, Feature, Key32,
    Limits, MemberId, Network, PowerProfile, PublicationId, RecordId, TopicName, WorkspaceId,
    api_error_code, api_version, default_limits,
};
pub use arachne_runtime::{
    AdmissionApproval, AdmissionApprovalPage, AdmissionAuthorization, AdmissionGrant,
    AdmissionNotice, AdmissionReply, AdmissionResponse, AdmissionStatus, AdmissionStatusKind,
    AnchorSlots, AnchorStore, Client, ClientConfig, ClientResult as Result,
    ConnectionCapacityMetrics, ConnectivityReport, Context, ContextConfig, ControlTimingMetrics,
    CurrentViewAdoption, CurrentViewCandidate, CurrentViewRequest, CurrentViewStatus,
    DeliveryFailure, DeliveryReport, DirectRecoveryReady, DirectRecoveryRequest,
    DirectRecoveryStatus, DurationSummary, EndpointInfo, FreshnessAnchor, InterestObservation,
    InvitationCandidate, InvitationCheckpoint, InvitationControl, InvitationDetails,
    InvitationInfo, InvitationKind, JoinAdmissionStep, JoinCandidate, JoinProgress, JoinRequest,
    MemberAction, MemberInfo, MemberKind, MemberRoster, MemberUpdate, MemberUpdateState,
    MembershipCandidate, MembershipGossipMetrics, MemoryAnchors, MemoryProvider,
    NearbyAdvertisement, NearbyEndpoint, NearbyMode, NearbyScan, OperatorRelay, PeerPolicy,
    PeerRoute, Presence, PresenceRound, ProtectedReceptionCandidate, Publication,
    PublicationCandidate, PublicationCurrent, PublicationMode, PublicationOptions,
    ReceivedProtectedPublication, RecoveryAdoption, RecoveryCandidate, RecoveryCutoffRequest,
    RecoveryCutoffStatus, RecoveryRangeReady, RecoveryRangeRequest, RecoveryRangeStatus,
    RecoveryStage, RelayTrust, RemovalCandidate, RemovedMembership, ResourceRequest,
    ResourceStatus, ResourceTicket, RestoredJoin, RestoredWorkspace, RouteHint, RouteKind,
    RuntimeConfig, SqliteProvider, Storage, StorageConfig, StorageProvider, StreamMetrics,
    TransportInfo, TransportOptions, TransportTimeouts, WorkspaceActivity, WorkspaceCandidate,
    WorkspaceInfo, WorkspaceMetrics, WorkspacePhase, WorkspaceProgress, WorkspaceProgressState,
    WorkspaceState, default_client_config, default_publication_options, default_transport_options,
};

// Keep both Core components' foreign exports in this SDK library. The types,
// candidate checks and durable operations are defined only in Core.
arachne_api::uniffi_reexport_scaffolding!();
arachne_runtime::uniffi_reexport_scaffolding!();
