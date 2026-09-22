//! Adopter-facing Rust boundary for the portable Arachne fabric.
//!
//! This crate exposes the typed runtime seam explicitly. It does not copy core
//! implementation and it does not expose ATAK, Android, JNI, or transport
//! internals. The core dependency remains separately licensed under MPL-2.0.
//!
//! The repository's `docs/workflows.md` covers invitations, protected
//! publication and reception, persistence ordering, and current integration limits.

pub use arachne_runtime::{
    AdmissionAuthorization, AdmissionReply, Client, ClientConfig, ClientResult as Result,
    ConnectionCapacityMetrics, ConnectivityReport, ControlTimingMetrics, DeliveryFailure,
    DeliveryReport, DurationSummary, EndpointInfo, Error, ErrorKind, InterestObservation,
    InvitationDetails, InvitationInfo, JoinAdmissionStep, JoinRequest, MemberInfo, MemberKind,
    MemberRoster, MembershipGossipMetrics, Network, PeerPolicy, PeerRoute, Presence,
    ProtectedReceptionCandidate, Publication, PublicationCandidate, PublicationCurrent,
    ReceivedProtectedPublication, RecoveredPublication, RecoveryAdoption, RecoveryCandidate,
    RecoveryRangeReady, RecoveryRangeRequest, RecoveryRangeStatus, RecoveryStage, RouteHint,
    RouteKind, WorkspaceActivity, WorkspaceCandidate, WorkspaceInfo, WorkspaceMetrics,
    WorkspacePhase, WorkspaceState,
};
