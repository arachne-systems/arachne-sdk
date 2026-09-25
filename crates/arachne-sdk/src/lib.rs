//! Adopter-facing Rust boundary for the portable Arachne fabric.
//!
//! This crate exposes the typed runtime seam explicitly. It does not copy core
//! implementation and it does not expose ATAK, Android, JNI, or transport
//! internals. The core dependency remains separately licensed under MPL-2.0.
//!
//! The repository's `docs/workflows.md` covers invitations, protected
//! publication and reception, persistence ordering, and current integration limits.

pub use arachne_runtime::{
    AdmissionApproval, AdmissionApprovalPage, AdmissionAuthorization, AdmissionReply, ApiError,
    Client, ClientConfig, ClientResult as Result, ConnectionCapacityMetrics, ConnectivityReport,
    Context, ContextConfig, ControlTimingMetrics, DeliveryFailure, DeliveryReport, DurationSummary,
    EndpointInfo, Error, ErrorCode, ErrorKind, Event, FreshnessAnchor, InterestObservation,
    InvitationCheckpoint, InvitationControl, InvitationDetails, InvitationInfo, InvitationKind,
    JoinAdmissionStep, JoinRequest, Limits, MemberAction, MemberInfo, MemberKind, MemberRoster,
    MembershipGossipMetrics, NearbyAdvertisement, NearbyEndpoint, NearbyMode, NearbyScan, Network,
    OperatorRelay, PeerPolicy, PeerRoute, PowerProfile, Presence, PresenceRound,
    ProtectedReceptionCandidate, Publication, PublicationCandidate, PublicationCurrent,
    ReceivedProtectedPublication, RecoveryAdoption, RecoveryCandidate, RecoveryRangeReady,
    RecoveryRangeRequest, RecoveryRangeStatus, RecoveryStage, RelayTrust, RemovedMembership,
    RouteHint, RouteKind, RuntimeConfig, TransportInfo, TransportOptions, TransportTimeouts,
    WorkspaceActivity, WorkspaceCandidate, WorkspaceInfo, WorkspaceMetrics, WorkspacePhase,
    WorkspaceState,
};

mod ffi;
