//! The UniFFI surface of the SDK (ADR A1/A4 steps 7-8, first vertical slice).
//!
//! `scripts/generate-bindings.sh` generates Kotlin, Swift, Python and Go from
//! this module. It wraps core's typed `arachne_runtime::Client`; it sends no
//! JSON and holds no lock around a blocking call.
//!
//! Core has no `uniffi` feature yet, so this module mirrors the public
//! `arachne-api` types that cross the boundary (`ErrorCode`, `ApiError`,
//! `Event`, `Network`). UniFFI's `remote` types cannot replace the mirrors:
//! the core types are `#[non_exhaustive]`, and the generated code must match
//! them exhaustively (rustc E0004) or build them with a struct expression
//! (E0639). The tests below check that the mirrors match core. When core adds
//! the derives behind a `uniffi` feature (ADR step 7), delete the mirrors and
//! re-export the core types.
//!
//! Names: an exported `close` clashes with Kotlin's `AutoCloseable.close`,
//! so `uniffi.toml` renames it to `shutdown` for Kotlin only. The error code
//! is a free function, `api_error_code`, because Go gets no methods on error
//! types and Python's `code` field hides a `code()` method.

use std::sync::Arc;
use std::time::Duration;

use arachne_api::{EndpointId, WorkspaceId};
use arachne_runtime::WorkspacePhase;

// ---------------------------------------------------------------------------
// IDs: lowercase hex strings on the foreign side. A bad string fails in the
// lift step with a typed `ApiError::InvalidId` (code 101).

uniffi::custom_type!(EndpointId, String, {
    remote,
    lower: |id| id.to_string(),
    try_lift: |text| Ok(EndpointId::from_hex(&text).map_err(ApiError::from)?),
});

uniffi::custom_type!(WorkspaceId, String, {
    remote,
    lower: |id| id.to_string(),
    try_lift: |text| Ok(WorkspaceId::from_hex(&text).map_err(ApiError::from)?),
});

// ---------------------------------------------------------------------------
// Error model (mirror of `arachne_api::{ErrorCode, ApiError}`).

/// A stable numeric error code. The discriminant is the number; see
/// `arachne_api::ErrorCode` for the ranges and rules.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, uniffi::Enum)]
#[repr(u32)]
pub enum ErrorCode {
    Closed = 1,
    Cancelled = 2,
    DeadlineExceeded = 3,
    InvalidInput = 100,
    InvalidId = 101,
    WrongState = 102,
    Unsupported = 103,
    CapacityExceeded = 200,
    LimitReached = 201,
    StorageFailed = 300,
    StorageCorrupt = 301,
    CandidateStale = 302,
    PeerUnreachable = 400,
    Timeout = 401,
    TransportFailed = 402,
    NotAuthorized = 500,
    InvitationInvalid = 501,
    InvitationExpired = 502,
    NotMember = 503,
    EpochMismatch = 600,
    PolicyMismatch = 601,
    Internal = 900,
}

impl ErrorCode {
    const ALL: &'static [ErrorCode] = &[
        Self::Closed,
        Self::Cancelled,
        Self::DeadlineExceeded,
        Self::InvalidInput,
        Self::InvalidId,
        Self::WrongState,
        Self::Unsupported,
        Self::CapacityExceeded,
        Self::LimitReached,
        Self::StorageFailed,
        Self::StorageCorrupt,
        Self::CandidateStale,
        Self::PeerUnreachable,
        Self::Timeout,
        Self::TransportFailed,
        Self::NotAuthorized,
        Self::InvitationInvalid,
        Self::InvitationExpired,
        Self::NotMember,
        Self::EpochMismatch,
        Self::PolicyMismatch,
        Self::Internal,
    ];

    fn from_number(number: u32) -> Option<Self> {
        Self::ALL
            .iter()
            .copied()
            .find(|code| *code as u32 == number)
    }
}

#[uniffi::export]
impl ErrorCode {
    /// The stable number of this code.
    pub fn number(&self) -> u32 {
        *self as u32
    }
}

impl From<arachne_api::ErrorCode> for ErrorCode {
    fn from(code: arachne_api::ErrorCode) -> Self {
        // A code that this SDK does not know yet reads as Internal. The
        // `error_codes_match_core` test fails first, in the SDK build.
        Self::from_number(code.as_u32()).unwrap_or(Self::Internal)
    }
}

/// The error of every SDK call. Programs read `api_error_code(error)`; the
/// text is for people and never holds secrets.
#[derive(Clone, Debug, PartialEq, Eq, thiserror::Error, uniffi::Error)]
pub enum ApiError {
    #[error("closed")]
    Closed,
    #[error("cancelled")]
    Cancelled,
    #[error("deadline exceeded")]
    DeadlineExceeded,
    #[error("invalid input `{field}`: {reason}")]
    InvalidInput { field: String, reason: String },
    #[error("invalid {kind} id: {reason}")]
    InvalidId { kind: String, reason: String },
    #[error("capacity exceeded for {resource} (limit {limit}): {detail}")]
    CapacityExceeded {
        resource: String,
        limit: u64,
        detail: String,
    },
    #[error("limit reached for {resource} (limit {limit}): {detail}")]
    LimitReached {
        resource: String,
        limit: u64,
        detail: String,
    },
    #[error("storage error ({code:?}): {detail}")]
    Storage { code: ErrorCode, detail: String },
    #[error("transport error ({code:?}): {detail}")]
    Transport {
        code: ErrorCode,
        peer: Option<EndpointId>,
        detail: String,
    },
    #[error("authorization error ({code:?}): {detail}")]
    Authorization { code: ErrorCode, detail: String },
    #[error("state error ({code:?}): {detail}")]
    State { code: ErrorCode, detail: String },
    #[error("internal error: {detail}")]
    Internal { detail: String },
}

impl ApiError {
    /// The stable code of this error.
    pub fn code(&self) -> ErrorCode {
        match self {
            Self::Closed => ErrorCode::Closed,
            Self::Cancelled => ErrorCode::Cancelled,
            Self::DeadlineExceeded => ErrorCode::DeadlineExceeded,
            Self::InvalidInput { .. } => ErrorCode::InvalidInput,
            Self::InvalidId { .. } => ErrorCode::InvalidId,
            Self::CapacityExceeded { .. } => ErrorCode::CapacityExceeded,
            Self::LimitReached { .. } => ErrorCode::LimitReached,
            Self::Storage { code, .. }
            | Self::Transport { code, .. }
            | Self::Authorization { code, .. }
            | Self::State { code, .. } => *code,
            Self::Internal { .. } => ErrorCode::Internal,
        }
    }

    /// The variant for `code`, by the pairing rule of `arachne_api::ApiError`.
    fn with_code(code: ErrorCode, detail: String) -> Self {
        use ErrorCode as C;
        match code {
            C::Closed => Self::Closed,
            C::Cancelled => Self::Cancelled,
            C::DeadlineExceeded => Self::DeadlineExceeded,
            C::InvalidInput => Self::InvalidInput {
                field: String::new(),
                reason: detail,
            },
            C::InvalidId => Self::InvalidId {
                kind: String::new(),
                reason: detail,
            },
            C::CapacityExceeded => Self::CapacityExceeded {
                resource: String::new(),
                limit: 0,
                detail,
            },
            C::LimitReached => Self::LimitReached {
                resource: String::new(),
                limit: 0,
                detail,
            },
            C::StorageFailed | C::StorageCorrupt | C::CandidateStale => {
                Self::Storage { code, detail }
            }
            C::PeerUnreachable | C::Timeout | C::TransportFailed => Self::Transport {
                code,
                peer: None,
                detail,
            },
            C::NotAuthorized | C::InvitationInvalid | C::InvitationExpired | C::NotMember => {
                Self::Authorization { code, detail }
            }
            C::WrongState | C::Unsupported | C::EpochMismatch | C::PolicyMismatch => {
                Self::State { code, detail }
            }
            C::Internal => Self::Internal { detail },
        }
    }
}

impl From<arachne_api::ApiError> for ApiError {
    fn from(error: arachne_api::ApiError) -> Self {
        use arachne_api::ApiError as E;
        match error {
            E::Closed => Self::Closed,
            E::Cancelled => Self::Cancelled,
            E::DeadlineExceeded => Self::DeadlineExceeded,
            E::InvalidInput { field, reason } => Self::InvalidInput { field, reason },
            E::InvalidId { kind, reason } => Self::InvalidId { kind, reason },
            E::CapacityExceeded {
                resource,
                limit,
                detail,
            } => Self::CapacityExceeded {
                resource,
                limit,
                detail,
            },
            E::LimitReached {
                resource,
                limit,
                detail,
            } => Self::LimitReached {
                resource,
                limit,
                detail,
            },
            E::Storage { code, detail } => Self::Storage {
                code: code.into(),
                detail,
            },
            E::Transport { code, peer, detail } => Self::Transport {
                code: code.into(),
                peer,
                detail,
            },
            E::Authorization { code, detail } => Self::Authorization {
                code: code.into(),
                detail,
            },
            E::State { code, detail } => Self::State {
                code: code.into(),
                detail,
            },
            E::Internal { detail } => Self::Internal { detail },
            // `ApiError` is `#[non_exhaustive]`: a variant added in core
            // lands here with its code intact.
            other => Self::with_code(other.code().into(), other.to_string()),
        }
    }
}

impl From<arachne_runtime::Error> for ApiError {
    fn from(error: arachne_runtime::Error) -> Self {
        // Core reports an op that races `close` as an unknown session handle
        // (code 101). Its `kind()` table turns that into `Closed`; keep it.
        if error.kind() == arachne_runtime::ErrorKind::Closed {
            return Self::Closed;
        }
        error.api_error().clone().into()
    }
}

/// The stable code of `error`. A free function, so every language has it.
#[uniffi::export]
pub fn api_error_code(error: &ApiError) -> ErrorCode {
    error.code()
}

/// The contract version (`arachne_api::API_VERSION`) this library was built with.
#[uniffi::export]
pub fn api_version() -> u32 {
    arachne_api::API_VERSION
}

// ---------------------------------------------------------------------------
// Values.

/// A network mode. `Tor` always exists, so the bindings are the same for
/// every build; without Tor in the build, `Client.open` gives `Unsupported`.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, uniffi::Enum)]
pub enum Network {
    Direct,
    Lan,
    Nearby,
    Wan,
    RelayOnly,
    WanOnly,
    Tor,
}

impl Network {
    fn runtime(self) -> Result<arachne_runtime::Network, ApiError> {
        use arachne_runtime::Network as N;
        Ok(match self {
            Self::Direct => N::Direct,
            Self::Lan => N::Lan,
            Self::Nearby => N::Nearby,
            Self::Wan => N::Wan,
            Self::RelayOnly => N::RelayOnly,
            Self::WanOnly => N::WanOnly,
            Self::Tor => {
                return Err(ApiError::State {
                    code: ErrorCode::Unsupported,
                    detail: "this build has no Tor transport".into(),
                });
            }
        })
    }
}

/// One event from `Client.next_event`. It names the queue or job that has
/// work; the host then drains that queue. Keep a default branch: new
/// variants can come in a later `api_version`.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, uniffi::Enum)]
pub enum Event {
    AdmissionRequest,
    MembershipChanged,
    ProtectedReceived,
    RecoveryReady,
    CurrentViewReady,
    InterestChanged,
    Presence,
    NearbyInvitation,
    Closed,
    Control,
    PublicationReceived,
}

impl TryFrom<arachne_api::Event> for Event {
    type Error = ApiError;

    fn try_from(event: arachne_api::Event) -> Result<Self, ApiError> {
        use arachne_api::Event as E;
        Ok(match event {
            E::AdmissionRequest => Self::AdmissionRequest,
            E::MembershipChanged => Self::MembershipChanged,
            E::ProtectedReceived => Self::ProtectedReceived,
            E::RecoveryReady => Self::RecoveryReady,
            E::CurrentViewReady => Self::CurrentViewReady,
            E::InterestChanged => Self::InterestChanged,
            E::Presence => Self::Presence,
            E::NearbyInvitation => Self::NearbyInvitation,
            E::Closed => Self::Closed,
            E::Control => Self::Control,
            E::PublicationReceived => Self::PublicationReceived,
            // `Event` is `#[non_exhaustive]` and has no `ALL` list, so the
            // compiler cannot catch a new core variant here.
            other => {
                return Err(ApiError::Internal {
                    detail: format!("event {other:?} is not in this SDK"),
                });
            }
        })
    }
}

/// Core's workspace phase. It is exhaustive in core, so UniFFI exports it
/// as a remote type with no mirror.
#[uniffi::remote(Enum)]
pub enum WorkspacePhase {
    Empty,
    Creating,
    Joining,
    Synchronizing,
    Active,
    Recovering,
    Leaving,
    Resetting,
    Removed,
    Failed,
}

/// How to open a client.
#[derive(Clone, Debug, PartialEq, Eq, uniffi::Record)]
pub struct ClientConfig {
    pub network: Network,
    /// A 32-byte endpoint secret. Only `Direct` accepts none (an ephemeral
    /// endpoint).
    #[uniffi(default = None)]
    pub secret: Option<Vec<u8>>,
    /// Per-op deadline in milliseconds for blocking ops and for the bind.
    #[uniffi(default = None)]
    pub deadline_ms: Option<u64>,
}

/// The bound endpoint of a client.
#[derive(Clone, Debug, PartialEq, Eq, uniffi::Record)]
pub struct EndpointInfo {
    pub endpoint_id: EndpointId,
    pub bound_address: String,
    pub workspace_ready: bool,
}

/// The workspace state of a client.
#[derive(Clone, Debug, PartialEq, Eq, uniffi::Record)]
pub struct WorkspaceState {
    pub endpoint_id: EndpointId,
    pub workspace: Option<WorkspaceId>,
    pub workspace_ready: bool,
    pub durable: bool,
    pub phase: WorkspacePhase,
    pub reason: Option<String>,
}

// ---------------------------------------------------------------------------
// The client object.

/// One Arachne session. All methods block; call them from a worker thread.
/// `close`, `wake`, `wait_for_work` and `next_event` may run on any thread
/// while another thread waits: no lock is held while a call waits.
#[derive(uniffi::Object)]
pub struct Client {
    inner: arachne_runtime::Client,
}

#[uniffi::export]
impl Client {
    /// Open a client in the process default context.
    #[uniffi::constructor]
    pub fn open(config: ClientConfig) -> Result<Arc<Self>, ApiError> {
        let network = config.network.runtime()?;
        let secret = match config.secret {
            None => None,
            Some(bytes) => Some(<[u8; 32]>::try_from(bytes.as_slice()).map_err(|_| {
                ApiError::InvalidInput {
                    field: "secret".into(),
                    reason: format!("must be 32 bytes, got {}", bytes.len()),
                }
            })?),
        };
        let transport = arachne_runtime::TransportOptions {
            deadline: config.deadline_ms.map(Duration::from_millis),
            ..Default::default()
        };
        let inner = arachne_runtime::Client::open(arachne_runtime::ClientConfig {
            network,
            secret,
            transport,
        })?;
        Ok(Arc::new(Self { inner }))
    }

    /// The bound endpoint.
    pub fn describe(&self) -> Result<EndpointInfo, ApiError> {
        let info = self.inner.endpoint()?;
        Ok(EndpointInfo {
            endpoint_id: EndpointId::from_bytes(info.endpoint_key),
            bound_address: info.bound_address,
            workspace_ready: info.workspace_ready,
        })
    }

    /// The workspace state.
    pub fn state(&self) -> Result<WorkspaceState, ApiError> {
        let state = self.inner.workspace_state()?;
        Ok(WorkspaceState {
            endpoint_id: EndpointId::from_bytes(state.endpoint_key),
            workspace: state.workspace.map(WorkspaceId::from_bytes),
            workspace_ready: state.workspace_ready,
            durable: state.durable,
            phase: state.phase,
            reason: state.reason,
        })
    }

    /// The next event, waiting up to `timeout_ms`. `None`: the timeout
    /// passed, `wake` was called, or the client closed while it waited.
    /// After `close` it fails with `Closed`.
    pub fn next_event(&self, timeout_ms: u64) -> Result<Option<Event>, ApiError> {
        self.inner
            .next_event(Some(Duration::from_millis(timeout_ms)))?
            .map(Event::try_from)
            .transpose()
    }

    /// Wait up to `timeout_ms` for work. `true`: drain the queues, then call
    /// again. `false`: the timeout passed, `wake` was called, or the client
    /// closed.
    pub fn wait_for_work(&self, timeout_ms: u64) -> Result<bool, ApiError> {
        Ok(self
            .inner
            .wait_for_work(Some(Duration::from_millis(timeout_ms)))?)
    }

    /// Release one waiter (`next_event` or `wait_for_work`) without work.
    pub fn wake(&self) -> Result<(), ApiError> {
        Ok(self.inner.wake()?)
    }

    /// Close the session. Idempotent, from any thread. It releases every
    /// waiter; later calls fail with `Closed`. Kotlin names it `shutdown`.
    pub fn close(&self) -> Result<(), ApiError> {
        Ok(self.inner.close()?)
    }
}

#[cfg(test)]
mod tests {
    use std::thread;
    use std::time::Instant;

    use super::*;

    #[test]
    fn error_codes_match_core() {
        assert_eq!(ErrorCode::ALL.len(), arachne_api::ErrorCode::ALL.len());
        for core in arachne_api::ErrorCode::ALL {
            let mirror = ErrorCode::from_number(core.as_u32())
                .unwrap_or_else(|| panic!("{core:?} ({}) has no mirror", core.as_u32()));
            assert_eq!(mirror.number(), core.as_u32());
            // Same names (Debug is the variant name on both sides).
            assert_eq!(format!("{mirror:?}"), format!("{core:?}"));
        }
    }

    #[test]
    fn every_core_error_keeps_its_code() {
        for core in arachne_api::ErrorCode::ALL {
            let error = arachne_api::ApiError::new(*core, "x");
            let mirror = ApiError::from(error.clone());
            assert_eq!(api_error_code(&mirror).number(), core.as_u32(), "{error:?}");
            // The fallback path agrees with the direct mapping.
            let fallback = ApiError::with_code((*core).into(), "x".into());
            assert_eq!(fallback.code(), mirror.code(), "{error:?}");
        }
    }

    #[test]
    fn networks_match_core() {
        let names: Vec<String> = arachne_api::Network::ALL
            .iter()
            .map(|n| format!("{n:?}"))
            .collect();
        let mirror = [
            Network::Direct,
            Network::Lan,
            Network::Nearby,
            Network::Wan,
            Network::RelayOnly,
            Network::WanOnly,
            Network::Tor,
        ];
        let mirror: Vec<String> = mirror.iter().map(|n| format!("{n:?}")).collect();
        assert_eq!(mirror, names);
    }

    #[test]
    fn bad_secret_and_tor_give_typed_codes() {
        let short = Client::open(ClientConfig {
            network: Network::Direct,
            secret: Some(vec![1; 5]),
            deadline_ms: None,
        })
        .err()
        .unwrap();
        assert_eq!(short.code(), ErrorCode::InvalidInput);
        let tor = Client::open(ClientConfig {
            network: Network::Tor,
            secret: Some(vec![1; 32]),
            deadline_ms: None,
        })
        .err()
        .unwrap();
        assert_eq!(tor.code(), ErrorCode::Unsupported);
    }

    #[test]
    fn close_from_another_thread_releases_next_event() {
        let client = Client::open(ClientConfig {
            network: Network::Direct,
            secret: None,
            deadline_ms: None,
        })
        .unwrap();
        assert_eq!(client.describe().unwrap().endpoint_id.to_string().len(), 64);
        let waiter = {
            let client = client.clone();
            thread::spawn(move || {
                let start = Instant::now();
                (client.next_event(30_000), start.elapsed())
            })
        };
        thread::sleep(Duration::from_millis(200));
        client.close().unwrap();
        let (event, elapsed) = waiter.join().unwrap();
        assert_eq!(event.unwrap(), None);
        assert!(elapsed < Duration::from_secs(2), "{elapsed:?}");
        assert_eq!(client.describe().unwrap_err().code(), ErrorCode::Closed);
    }
}
