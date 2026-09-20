//! Adopter-facing Rust boundary for the portable Arachne fabric.
//!
//! This crate exposes the typed runtime seam explicitly. It does not copy core
//! implementation and it does not expose ATAK, Android, JNI, or transport
//! internals. The core dependency remains separately licensed under MPL-2.0.

pub use arachne_runtime::{
    Client,
    ClientConfig,
    ClientResult as Result,
    ConnectivityReport,
    DeliveryFailure,
    DeliveryReport,
    EndpointInfo,
    Error,
    ErrorKind,
    InterestObservation,
    InvitationDetails,
    InvitationInfo,
    MemberInfo,
    MemberKind,
    MemberRoster,
    Network,
    PeerPolicy,
    PeerRoute,
    Presence,
    Publication,
    RecoveredPublication,
    RouteHint,
    RouteKind,
    WorkspaceActivity,
    WorkspaceInfo,
    WorkspacePhase,
    WorkspaceState,
};
