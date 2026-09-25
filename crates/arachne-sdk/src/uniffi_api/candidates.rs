//! Opaque, kind-bound, client-bound, single-use candidates (ADR decision 5).
//!
//! A stage call returns one of these objects. It holds the staged snapshot;
//! foreign code never sees the bytes. Only the adopt call of the same kind
//! on the same client accepts it, and only once. Another client gives
//! `WrongState`, a second adopt gives `WrongState`.
//!
//! SHIM: remove after core step 6 (with A5) - core keeps snapshot bytes in
//! the host today; A5 moves candidates into core as objects that do this
//! check themselves. Saving a candidate to record storage is not wrapped
//! here (A5 replaces it), so these adopt only on a non-durable client.

use std::sync::Mutex;

use arachne_api::WorkspaceId;

use super::ApiError;

/// The one-time snapshot and its owning client.
#[derive(Debug)]
pub(crate) struct Staged {
    client: u64,
    snapshot: Mutex<Option<Vec<u8>>>,
}

impl Staged {
    pub(crate) fn new(client: u64, snapshot: Vec<u8>) -> Self {
        Self {
            client,
            snapshot: Mutex::new(Some(snapshot)),
        }
    }

    /// The snapshot for one adopt on `client`. It is gone afterwards.
    pub(crate) fn take(&self, client: u64) -> Result<Vec<u8>, ApiError> {
        if self.client != client {
            return Err(wrong_state("the candidate belongs to another client"));
        }
        self.snapshot
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
            .take()
            .ok_or_else(|| wrong_state("the candidate was already used"))
    }

    fn used(&self) -> bool {
        self.snapshot
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
            .is_none()
    }
}

fn wrong_state(detail: &str) -> ApiError {
    ApiError::State {
        code: super::ErrorCode::WrongState,
        detail: detail.into(),
    }
}

macro_rules! candidate {
    ($(#[$doc:meta])* $name:ident) => {
        $(#[$doc])*
        #[derive(Debug, uniffi::Object)]
        pub struct $name {
            pub(crate) workspace: WorkspaceId,
            pub(crate) staged: Staged,
        }

        #[uniffi::export]
        impl $name {
            /// The workspace this candidate changes.
            pub fn workspace(&self) -> WorkspaceId {
                self.workspace
            }

            /// It was adopted (or an adopt was tried).
            pub fn is_used(&self) -> bool {
                self.staged.used()
            }
        }
    };
}

candidate!(
    /// A staged invitation link. Adopt it with `adopt_invitation`.
    InvitationCandidate
);
candidate!(
    /// A staged admission, approval or decline. Adopt it with `adopt_admission`.
    AdmissionCandidate
);
candidate!(
    /// A staged join. Adopt it with `adopt_join`.
    JoinCandidate
);
candidate!(
    /// A staged protected publication. Adopt it with `adopt_protected_publication`.
    PublicationCandidate
);
candidate!(
    /// A staged inbox change: a reception from `poll_protected`, or an
    /// acknowledgement or rejection. Adopt it with `adopt_protected_reception`.
    ReceptionCandidate
);

/// A staged recovery range. Adopt it with `adopt_recovery`.
#[derive(Debug, uniffi::Object)]
pub struct RecoveryCandidate {
    pub(crate) workspace: WorkspaceId,
    pub(crate) publication_count: u64,
    pub(crate) durable: bool,
    pub(crate) staged: Staged,
}

#[uniffi::export]
impl RecoveryCandidate {
    pub fn workspace(&self) -> WorkspaceId {
        self.workspace
    }

    pub fn publication_count(&self) -> u64 {
        self.publication_count
    }

    pub fn durable(&self) -> bool {
        self.durable
    }

    pub fn is_used(&self) -> bool {
        self.staged.used()
    }
}

/// The result of `stage_recovery_range`.
#[derive(Debug, uniffi::Enum)]
pub enum RecoveryStage {
    Candidate {
        candidate: std::sync::Arc<RecoveryCandidate>,
    },
    AlreadyCovered,
    NoNewObjects,
    /// Nothing fits the pending bounds until the application acknowledges
    /// or rejects pending objects. Drain the inbox, then ask again.
    AwaitingApplication,
}
