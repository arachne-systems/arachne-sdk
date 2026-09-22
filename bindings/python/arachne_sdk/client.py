from __future__ import annotations

import ctypes
import json
import os
import sys
import threading
from dataclasses import asdict, dataclass, is_dataclass
from enum import Enum, IntEnum
from pathlib import Path
from typing import Any, NewType


class Network(IntEnum):
    DIRECT = 0
    LAN = 1
    NEARBY = 2
    WAN = 3
    RELAY_ONLY = 4
    WAN_ONLY = 5


class ArachneError(RuntimeError):
    """A runtime or native binding error."""


ID = NewType("ID", bytes)
RecordID = NewType("RecordID", bytes)


class WorkspacePhase(str, Enum):
    EMPTY = "empty"
    CREATING = "creating"
    JOINING = "joining"
    SYNCHRONIZING = "synchronizing"
    ACTIVE = "active"
    RECOVERING = "recovering"
    LEAVING = "leaving"
    RESETTING = "resetting"
    REMOVED = "removed"
    FAILED = "failed"


class MemberKind(str, Enum):
    PERSON = "person"
    SERVICE = "service"


class Presence(str, Enum):
    SELF = "self"
    UNKNOWN = "unknown"
    REACHABLE = "reachable"
    STALE = "stale"


@dataclass(frozen=True)
class ClientConfig:
    network: Network = Network.DIRECT
    secret: bytes | None = None


@dataclass(frozen=True)
class Activity:
    phase: WorkspacePhase
    reason: str | None = None


@dataclass(frozen=True)
class EndpointInfo:
    endpoint_key: ID
    bound_address: str
    workspace_ready: bool


@dataclass(frozen=True)
class WorkspaceState:
    endpoint_key: ID
    workspace: ID | None
    workspace_ready: bool
    durable: bool
    phase: WorkspacePhase
    reason: str | None


@dataclass(frozen=True)
class WorkspaceInfo:
    workspace: ID
    workspace_name: str | None
    epoch: int
    member_count: int
    durable: bool
    phase: WorkspacePhase
    reason: str | None


@dataclass(frozen=True)
class WorkspaceCandidate:
    workspace: ID
    snapshot: bytes


@dataclass(frozen=True)
class RestoredMember:
    id: ID
    display_name: str | None


@dataclass(frozen=True)
class RestoreResult:
    workspace: ID
    workspace_name: str | None = None
    endpoint: ID | None = None
    epoch: int | None = None
    member_count: int | None = None
    durable: bool = False
    workspace_ready: bool | None = None
    state: str | None = None
    activity: Activity | None = None
    member: RestoredMember | None = None
    key_package: bytes = b""
    admission_request: bytes = b""
    personal_invitation: bool | None = None
    commit_digest: bytes = b""


@dataclass(frozen=True)
class AdmissionAuthorization:
    invitation_key: ID
    grant_signature: bytes
    redemption_signature: bytes


@dataclass(frozen=True)
class JoinAdmissionStep:
    commit: bytes
    authorization: AdmissionAuthorization


@dataclass(frozen=True)
class JoinRequest:
    workspace: ID
    member: ID
    endpoint: ID
    admission_request: bytes


@dataclass(frozen=True)
class AdmissionReply:
    workspace: ID
    epoch: int
    commit: bytes
    welcome: bytes
    authorization: AdmissionAuthorization


@dataclass(frozen=True)
class MemberInfo:
    id: ID
    endpoint: ID
    administrator: bool
    self_member: bool
    display_name: str | None
    kind: MemberKind
    presence: Presence
    last_contact_age_ms: int | None
    presence_fresh_for_ms: int | None


@dataclass(frozen=True)
class MemberRoster:
    workspace: ID
    workspace_name: str | None
    workspace_name_revision: int
    workspace_name_head: ID
    epoch: int
    members: tuple[MemberInfo, ...]
    profile_count: int
    profiles_retained: bool


@dataclass(frozen=True)
class RouteHint:
    peer: ID
    address: str


@dataclass(frozen=True)
class InvitationInfo:
    workspace: ID
    workspace_name: str | None
    invitation: bytes
    invitation_key: ID
    checkpoint: bytes
    peer: ID
    bootstrap_peers: tuple[ID, ...]
    address: str
    routes: tuple[RouteHint, ...]


@dataclass(frozen=True)
class InvitationDetails:
    workspace: ID
    invitation_key: ID
    workspace_name: str | None
    epoch: int
    personal: bool
    automatic: bool
    expires_at: int


@dataclass(frozen=True)
class PeerPolicy:
    peer: ID
    publish: tuple[str, ...] = ()
    subscribe: tuple[str, ...] = ()


@dataclass(frozen=True)
class PeerRoute:
    member: ID
    route: str
    rtt_ms: int


@dataclass(frozen=True)
class ConnectivityReport:
    workspace: ID
    paths: tuple[PeerRoute, ...]
    paths_limited: bool
    receive_queue: int
    repair_jobs: int


@dataclass(frozen=True)
class DurationSummary:
    count: int
    total_us: int
    max_us: int


@dataclass(frozen=True)
class ControlTimingMetrics:
    inquiry: DurationSummary
    host_wait: DurationSummary
    host_service: DurationSummary


@dataclass(frozen=True)
class MembershipGossipMetrics:
    sent: int
    no_overlay: int
    failed: int
    received: int
    staged: int
    rejected: int
    range_pulled: int
    range_failed: int


@dataclass(frozen=True)
class ConnectionCapacityMetrics:
    evicted: int
    refused: int


@dataclass(frozen=True)
class WorkspaceMetrics:
    workspace: ID
    phase: WorkspacePhase
    reason: str | None
    received_bytes: int
    sent_bytes: int
    receive_queue: int
    admission_queue: int
    admission_queue_bytes: int
    admission_waiters: int
    admission_in_flight: int
    approval_pending: int
    pending_objects: int
    repair_jobs: int
    gossip_neighbors: int
    control_timing: ControlTimingMetrics
    membership_gossip: MembershipGossipMetrics
    connection_capacity: ConnectionCapacityMetrics
    paths: tuple[PeerRoute, ...]
    paths_limited: bool


@dataclass(frozen=True)
class DeliveryFailure:
    peer: ID
    error: str


@dataclass(frozen=True)
class DeliveryReport:
    admitted: tuple[ID, ...]
    queued: bool
    failed: tuple[DeliveryFailure, ...]


@dataclass(frozen=True)
class PublicationCurrent:
    selector: ID
    replacement_key: ID
    expires_at: int
    tombstone: bool = False


@dataclass(frozen=True)
class PublicationCandidate:
    workspace: ID
    snapshot: bytes


@dataclass(frozen=True)
class ProtectedReceptionCandidate:
    workspace: ID
    snapshot: bytes


@dataclass(frozen=True)
class ReceivedProtectedPublication:
    workspace: ID
    revision: int
    member: ID
    endpoint: ID
    topic: str
    id: RecordID
    sequence: int | None
    payload: bytes
    recipients: tuple[ID, ...]


@dataclass(frozen=True)
class InterestObservation:
    workspace: ID
    revision: int
    topic: str
    subscribed: bool
    admission: DeliveryReport


@dataclass(frozen=True)
class Publication:
    workspace: ID
    revision: int
    sender: ID
    topic: str
    payload: bytes


@dataclass(frozen=True)
class RecoveredPublication:
    workspace: ID
    revision: int
    member: ID
    endpoint: ID
    topic: str
    id: RecordID
    sequence: int | None
    payload: bytes


@dataclass(frozen=True)
class RecoveryRangeRequest:
    revision: int
    topics: tuple[str, ...]
    peer: ID | None = None
    author: ID | None = None
    after: int | None = None
    through: int | None = None


@dataclass(frozen=True)
class RecoveryRangeReady:
    workspace: ID
    author: ID
    peer: ID
    epoch: int
    revision: int
    after: int
    through: int
    packet_count: int
    retained_bytes: int
    automatic_source: bool
    attempted: int | None


@dataclass(frozen=True)
class RecoveryRangeStatus:
    state: str
    ready: RecoveryRangeReady | None = None
    candidate_count: int = 0
    automatic_source: bool = False
    attempted: int | None = None
    reason: str | None = None


@dataclass(frozen=True)
class RecoveryCandidate:
    workspace: ID
    snapshot: bytes
    publication_count: int
    already_received: int
    durable: bool


@dataclass(frozen=True)
class RecoveryStage:
    state: str
    candidate: RecoveryCandidate | None = None


@dataclass(frozen=True)
class RecoveryAdoption:
    workspace: ID
    epoch: int
    member_count: int
    durable: bool
    recovered_publications: int
    missing_publications: int


class _Buffer(ctypes.Structure):
    _fields_ = [("data", ctypes.POINTER(ctypes.c_uint8)), ("len", ctypes.c_size_t)]


class _Result(ctypes.Structure):
    _fields_ = [("status", ctypes.c_int32), ("value", _Buffer)]


class _StoredResult(ctypes.Structure):
    _fields_ = [
        ("status", ctypes.c_int32),
        ("value", _Buffer),
        ("snapshot", _Buffer),
    ]


def _default_library() -> Path:
    explicit = os.environ.get("ARACHNE_SDK_LIBRARY")
    if explicit:
        return Path(explicit)
    names = {
        "linux": "libarachne_sdk.so",
        "darwin": "libarachne_sdk.dylib",
        "win32": "arachne_sdk.dll",
    }
    try:
        name = names[sys.platform]
    except KeyError as error:
        raise ArachneError(f"unsupported platform: {sys.platform}") from error
    repository = Path(__file__).resolve().parents[3]
    return repository / "target" / "debug" / name


def _load_library(path: str | os.PathLike[str] | None) -> ctypes.CDLL:
    library_path = Path(path) if path is not None else _default_library()
    try:
        library = ctypes.CDLL(str(library_path))
    except OSError as error:
        raise ArachneError(
            f"could not load Arachne native library at {library_path}; "
            "build it with `cargo build -p arachne-sdk` or set ARACHNE_SDK_LIBRARY"
        ) from error

    library.arachne_sdk_open.argtypes = [
        ctypes.c_uint32,
        ctypes.POINTER(ctypes.c_uint8),
        ctypes.c_size_t,
    ]
    library.arachne_sdk_open.restype = _Result
    library.arachne_sdk_execute.argtypes = [
        ctypes.c_int64,
        ctypes.POINTER(ctypes.c_uint8),
        ctypes.c_size_t,
    ]
    library.arachne_sdk_execute.restype = _Result
    library.arachne_sdk_execute_stored.argtypes = [
        ctypes.c_int64,
        ctypes.POINTER(ctypes.c_uint8),
        ctypes.c_size_t,
        ctypes.POINTER(ctypes.c_uint8),
        ctypes.c_size_t,
    ]
    library.arachne_sdk_execute_stored.restype = _StoredResult
    library.arachne_sdk_describe.argtypes = [ctypes.c_int64]
    library.arachne_sdk_describe.restype = _Result
    library.arachne_sdk_enable_record_storage.argtypes = [
        ctypes.c_int64,
        ctypes.POINTER(ctypes.c_uint8),
        ctypes.c_size_t,
        ctypes.POINTER(ctypes.c_uint8),
        ctypes.c_size_t,
    ]
    library.arachne_sdk_enable_record_storage.restype = _Result
    library.arachne_sdk_restore_record_storage.argtypes = [
        ctypes.c_int64,
        ctypes.POINTER(ctypes.c_uint8),
        ctypes.c_size_t,
        ctypes.POINTER(ctypes.c_uint8),
        ctypes.c_size_t,
        ctypes.POINTER(ctypes.c_uint8),
        ctypes.c_size_t,
    ]
    library.arachne_sdk_restore_record_storage.restype = _Result
    library.arachne_sdk_save_candidate.argtypes = [
        ctypes.c_int64,
        ctypes.POINTER(ctypes.c_uint8),
        ctypes.c_size_t,
    ]
    library.arachne_sdk_save_candidate.restype = _Result
    library.arachne_sdk_cancel.argtypes = [ctypes.c_int64]
    library.arachne_sdk_cancel.restype = _Result
    library.arachne_sdk_wait_for_work.argtypes = [ctypes.c_int64]
    library.arachne_sdk_wait_for_work.restype = _Result
    library.arachne_sdk_close.argtypes = [ctypes.c_int64]
    library.arachne_sdk_close.restype = _Result
    library.arachne_sdk_buffer_free.argtypes = [
        ctypes.POINTER(ctypes.c_uint8),
        ctypes.c_size_t,
    ]
    library.arachne_sdk_buffer_free.restype = None
    return library


def _input_buffer(value: bytes) -> tuple[Any, ctypes.POINTER(ctypes.c_uint8)]:
    if not value:
        return None, ctypes.POINTER(ctypes.c_uint8)()
    storage = (ctypes.c_uint8 * len(value)).from_buffer_copy(value)
    return storage, ctypes.cast(storage, ctypes.POINTER(ctypes.c_uint8))


def _take_buffer(library: ctypes.CDLL, value: _Buffer) -> bytes:
    try:
        return ctypes.string_at(value.data, value.len) if value.len else b""
    finally:
        library.arachne_sdk_buffer_free(value.data, value.len)


def _normalize(value: Any) -> Any:
    if isinstance(value, (bytes, bytearray, memoryview)):
        return list(bytes(value))
    if isinstance(value, Enum):
        return value.value
    if is_dataclass(value):
        return _normalize(asdict(value))
    if isinstance(value, dict):
        return {key: _normalize(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [_normalize(item) for item in value]
    return value


class Client:
    """One blocking endpoint session. Calls on a client are serialized."""

    def __init__(self, library: ctypes.CDLL, handle: int):
        self._library = library
        self._handle = handle
        self._closed = False
        self._lock = threading.Lock()

    @classmethod
    def open(
        cls,
        network: Network | ClientConfig = Network.DIRECT,
        secret: bytes | None = None,
        *,
        library_path: str | os.PathLike[str] | None = None,
    ) -> Client:
        if isinstance(network, ClientConfig):
            if secret is not None:
                raise ValueError("secret must be supplied in ClientConfig or separately")
            secret = network.secret
            network = network.network
        library = _load_library(library_path)
        secret_bytes = bytes(secret) if secret is not None else b""
        storage, pointer = _input_buffer(secret_bytes)
        result = library.arachne_sdk_open(
            int(network), pointer, len(secret_bytes)
        )
        value = _take_buffer(library, result.value)
        if result.status:
            raise ArachneError(value.decode("utf-8", errors="replace"))
        try:
            handle = int(value.decode("ascii"))
        except (UnicodeDecodeError, ValueError) as error:
            raise ArachneError("native SDK returned an invalid client handle") from error
        finally:
            del storage
        return cls(library, handle)

    def raw_call(self, op: str, **params: Any) -> Any:
        """Invoke an advanced core JSON operation; prefer typed methods."""
        with self._lock:
            self._ensure_open()
            request = self._request(op, params)
            storage, pointer = _input_buffer(request)
            result = self._library.arachne_sdk_execute(
                self._handle, pointer, len(request)
            )
            value = _take_buffer(self._library, result.value)
            del storage
            if result.status:
                raise ArachneError(value.decode("utf-8", errors="replace"))
            return json.loads(value)

    def call(self, op: str, **params: Any) -> Any:
        """Compatibility alias for :meth:`raw_call`."""
        return self.raw_call(op, **params)

    def raw_call_stored(
        self, op: str, snapshot: bytes = b"", **params: Any
    ) -> tuple[Any, bytes]:
        """Run a snapshot operation and return its JSON result and opaque bytes."""
        with self._lock:
            self._ensure_open()
            request = self._request(op, params)
            request_storage, request_pointer = _input_buffer(request)
            snapshot_bytes = bytes(snapshot)
            snapshot_storage, snapshot_pointer = _input_buffer(snapshot_bytes)
            result = self._library.arachne_sdk_execute_stored(
                self._handle,
                request_pointer,
                len(request),
                snapshot_pointer,
                len(snapshot_bytes),
            )
            value = _take_buffer(self._library, result.value)
            returned_snapshot = _take_buffer(self._library, result.snapshot)
            del request_storage, snapshot_storage
            if result.status:
                raise ArachneError(value.decode("utf-8", errors="replace"))
            return json.loads(value), returned_snapshot

    def call_stored(
        self, op: str, snapshot: bytes = b"", **params: Any
    ) -> tuple[Any, bytes]:
        """Compatibility alias for :meth:`raw_call_stored`."""
        return self.raw_call_stored(op, snapshot, **params)

    def endpoint(self) -> EndpointInfo:
        raw = json.loads(self._native_call(self._library.arachne_sdk_describe, self._handle))
        return EndpointInfo(
            endpoint_key=_id(raw["endpoint_key"], "endpoint_key"),
            bound_address=raw["bound_address"],
            workspace_ready=raw["workspace_ready"],
        )

    def workspace_state(self) -> WorkspaceState:
        raw = self.raw_call("workspace_state")
        activity = _activity(raw["activity"])
        return WorkspaceState(
            endpoint_key=self.endpoint().endpoint_key,
            workspace=_optional_id(raw.get("workspace"), "workspace"),
            workspace_ready=raw["workspace_ready"],
            durable=raw["durable"],
            phase=activity.phase,
            reason=activity.reason,
        )

    def create_workspace(self, display_name: str, workspace_name: str | None = None) -> WorkspaceInfo:
        return _workspace_info(self.raw_call(
            "create_workspace", display_name=display_name, workspace_name=workspace_name
        ))

    def begin_join(
        self,
        invitation: bytes,
        checkpoint: bytes,
        display_name: str,
        peers: tuple[ID, ...] = (),
    ) -> JoinRequest:
        raw = self.raw_call(
            "begin_join", invitation=invitation, checkpoint=checkpoint,
            display_name=display_name, peers=[_fixed_bytes(peer, 32, "peer ID") for peer in peers],
        )
        admission = raw.get("admission_request")
        if admission is None:
            raise ArachneError("invitation has no admission request")
        return JoinRequest(
            workspace=_id(raw["workspace"], "workspace"),
            member=_id(raw["member"]["id"], "member ID"),
            endpoint=_id(raw["endpoint"], "endpoint"),
            admission_request=bytes(admission),
        )

    def drive_join(self) -> Any:
        return self.raw_call("drive_join")

    def stage_admission(self, authenticated_endpoint: ID, request: bytes) -> WorkspaceCandidate:
        raw, snapshot = self.raw_call_stored(
            "stage_admission", authenticated_endpoint=_fixed_bytes(authenticated_endpoint, 32, "endpoint ID"),
            request=request,
        )
        return WorkspaceCandidate(_id(raw["workspace"], "workspace"), snapshot)

    def adopt_admission(self, snapshot: bytes) -> WorkspaceInfo:
        raw, _ = self.raw_call_stored("adopt_admission", snapshot=snapshot)
        return _workspace_info(raw)

    def retained_admission(self, authenticated_endpoint: ID, request: bytes) -> AdmissionReply:
        raw = self.raw_call(
            "retained_admission", authenticated_endpoint=_fixed_bytes(authenticated_endpoint, 32, "endpoint ID"),
            request=request,
        )
        return _admission_reply(raw)

    def stage_join(self, welcome: bytes, commits: tuple[JoinAdmissionStep, ...]) -> WorkspaceCandidate:
        raw, snapshot = self.raw_call_stored("stage_join", welcome, commits=commits)
        return WorkspaceCandidate(_id(raw["workspace"], "workspace"), snapshot)

    def adopt_join(self, snapshot: bytes) -> WorkspaceInfo:
        raw, _ = self.raw_call_stored("adopt_join", snapshot=snapshot)
        return _workspace_info(raw)

    def enable_record_storage(self, path: str | os.PathLike[str], root: bytes) -> None:
        path_bytes = os.fspath(path).encode("utf-8")
        root_bytes = _fixed_bytes(root, 32, "storage root")
        path_storage, path_pointer = _input_buffer(path_bytes)
        root_storage, root_pointer = _input_buffer(root_bytes)
        self._native_call(
            self._library.arachne_sdk_enable_record_storage,
            self._handle, path_pointer, len(path_bytes), root_pointer, len(root_bytes),
        )
        del path_storage, root_storage

    def restore_record_storage(
        self, path: str | os.PathLike[str], root: bytes, workspace: ID
    ) -> RestoreResult:
        path_bytes = os.fspath(path).encode("utf-8")
        root_bytes = _fixed_bytes(root, 32, "storage root")
        workspace_bytes = _fixed_bytes(workspace, 32, "workspace ID")
        path_storage, path_pointer = _input_buffer(path_bytes)
        root_storage, root_pointer = _input_buffer(root_bytes)
        workspace_storage, workspace_pointer = _input_buffer(workspace_bytes)
        raw = json.loads(self._native_call(
            self._library.arachne_sdk_restore_record_storage,
            self._handle, path_pointer, len(path_bytes), root_pointer, len(root_bytes),
            workspace_pointer, len(workspace_bytes),
        ))
        del path_storage, root_storage, workspace_storage
        return _restore_result(raw)

    def save_candidate(self, snapshot: bytes) -> None:
        storage, pointer = _input_buffer(bytes(snapshot))
        self._native_call(
            self._library.arachne_sdk_save_candidate,
            self._handle, pointer, len(snapshot),
        )
        del storage

    def member_roster(self) -> MemberRoster:
        raw = self.raw_call("member_roster")
        members = tuple(_member_info(item) for item in raw["members"])
        return MemberRoster(
            workspace=_id(raw["workspace"], "workspace"), workspace_name=raw.get("workspace_name"),
            workspace_name_revision=raw["workspace_name_revision"],
            workspace_name_head=_id(raw["workspace_name_head"], "workspace name head"),
            epoch=raw["epoch"], members=members, profile_count=len(raw.get("profiles", [])),
            profiles_retained=raw.get("profiles_retained", True),
        )

    def use_service_profile(self) -> None:
        self.raw_call("use_service_profile")

    def issue_invitation(self) -> InvitationInfo:
        raw = self.raw_call("issue_invitation")
        return InvitationInfo(
            workspace=_id(raw["workspace"], "workspace"), workspace_name=raw.get("workspace_name"),
            invitation=bytes(raw["invitation"]), invitation_key=_id(raw["invitation_key"], "invitation key"),
            checkpoint=bytes(raw["checkpoint"]), peer=_id(raw["peer"], "peer"),
            bootstrap_peers=tuple(_id(item, "bootstrap peer") for item in raw.get("bootstrap_peers", [])),
            address=raw["address"], routes=tuple(RouteHint(_id(item["peer"], "peer"), item["address"]) for item in raw.get("routes", [])),
        )

    def inspect_invitation(self, invitation: bytes, checkpoint: bytes) -> InvitationDetails:
        raw = self.raw_call("inspect_invitation", invitation=invitation, checkpoint=checkpoint)
        return InvitationDetails(
            workspace=_id(raw["workspace"], "workspace"),
            invitation_key=_id(raw["invitation_key"], "invitation key"),
            workspace_name=raw.get("workspace_name"), epoch=raw["epoch"],
            personal=raw["personal_invitation"], automatic=raw["automatic_approval"],
            expires_at=raw["expires_at"],
        )

    def connectivity(self) -> ConnectivityReport:
        metrics = self.metrics()
        return ConnectivityReport(
            workspace=metrics.workspace, paths=metrics.paths, paths_limited=metrics.paths_limited,
            receive_queue=metrics.receive_queue, repair_jobs=metrics.repair_jobs,
        )

    def metrics(self) -> WorkspaceMetrics:
        raw = self.raw_call("workspace_metrics")
        activity = _activity(raw["activity"])
        timing = raw["control_timing"]
        gossip = raw["membership_gossip"]
        capacity = raw["connection_capacity"]
        return WorkspaceMetrics(
            workspace=_id(raw["workspace"], "workspace"), phase=activity.phase, reason=activity.reason,
            received_bytes=raw["received_bytes"], sent_bytes=raw["sent_bytes"],
            receive_queue=raw["receive_queue"], admission_queue=raw["admission_queue"],
            admission_queue_bytes=raw["admission_queue_bytes"], admission_waiters=raw["admission_waiters"],
            admission_in_flight=raw["admission_in_flight"], approval_pending=raw["approval_pending"],
            pending_objects=raw["pending_objects"], repair_jobs=raw["repair_jobs"],
            gossip_neighbors=raw["gossip_neighbors"],
            control_timing=ControlTimingMetrics(
                inquiry=_duration(timing["inquiry"]), host_wait=_duration(timing["host_wait"]),
                host_service=_duration(timing["host_service"]),
            ),
            membership_gossip=MembershipGossipMetrics(**gossip),
            connection_capacity=ConnectionCapacityMetrics(**capacity),
            paths=tuple(_peer_route(item) for item in raw["paths"]), paths_limited=raw["paths_limited"],
        )

    def network_change(self) -> None:
        self.raw_call("network_change")

    def cancel(self) -> None:
        self._native_call(self._library.arachne_sdk_cancel, self._handle)

    def wait_for_work(self) -> bool:
        value = self._native_call(self._library.arachne_sdk_wait_for_work, self._handle)
        if value not in (b"0", b"1"):
            raise ArachneError("native SDK returned an invalid wait result")
        return value == b"1"

    def poll_control(self) -> bool:
        return self.raw_call("poll_admission") is not None

    def add_address_hint(self, peer: ID, address: str) -> None:
        self.raw_call("add_address_hint", peer=_fixed_bytes(peer, 32, "peer ID"), address=address)

    def install_policy(self, workspace: ID, revision: int, endpoints: tuple[PeerPolicy, ...]) -> None:
        self.raw_call("install_verified_policy", workspace=_fixed_bytes(workspace, 32, "workspace ID"),
                      revision=revision, endpoints=endpoints)

    def install_workspace_policy(self, revision: int) -> None:
        self.raw_call("install_workspace_policy", revision=revision)

    def enable_object_delivery(self) -> None:
        durable = self.workspace_state().durable
        value, snapshot = self.raw_call_stored("enable_object_delivery")
        if not snapshot:
            if isinstance(value, dict) and value.get("state") == "object_delivery_enabled":
                return
            raise ArachneError("object delivery returned no adoptable snapshot")
        if durable:
            self.save_candidate(snapshot)
        self.raw_call_stored("adopt_reception", snapshot=snapshot)

    def stage_protected_publication(
        self, workspace: ID, revision: int, topic: str, record_id: RecordID, payload: bytes,
        current: PublicationCurrent | None = None,
    ) -> PublicationCandidate:
        raw, snapshot = self.raw_call_stored(
            "stage_network_publication", revision=revision, topic=topic,
            id=_fixed_bytes(record_id, 16, "record ID"), payload=payload, current=current,
        )
        candidate_workspace = _id(raw["workspace"], "workspace")
        if candidate_workspace != _fixed_bytes(workspace, 32, "workspace ID"):
            raise ArachneError("publication candidate workspace mismatch")
        return PublicationCandidate(candidate_workspace, snapshot)

    def adopt_protected_publication(self, snapshot: bytes) -> DeliveryReport:
        raw, _ = self.raw_call_stored("adopt_publication", snapshot=snapshot)
        if raw.get("network_error"):
            raise ArachneError(raw["network_error"])
        return _delivery_report(raw["admission"])

    def poll_protected(self) -> ProtectedReceptionCandidate | None:
        raw, snapshot = self.raw_call_stored("poll_protected")
        if raw is None:
            if snapshot:
                raise ArachneError("empty protected reception returned a snapshot")
            return None
        if raw.get("state") != "awaiting_reception_save" or not snapshot:
            raise ArachneError("protected reception has no adoptable snapshot")
        return ProtectedReceptionCandidate(_id(raw["workspace"], "workspace"), snapshot)

    def adopt_protected_reception(self, snapshot: bytes) -> ReceivedProtectedPublication:
        raw, _ = self.raw_call_stored("adopt_reception", snapshot=snapshot)
        return _protected_publication(raw)

    def set_interest(self, workspace: ID, revision: int, topic: str, subscribed: bool) -> None:
        self.raw_call("set_interest", workspace=_fixed_bytes(workspace, 32, "workspace ID"),
                      revision=revision, topic=topic, subscribed=subscribed)

    def poll_interest(self) -> InterestObservation | None:
        raw = self.raw_call("poll_interest")
        if raw is None or raw.get("state") == "interest_pending":
            return None
        if raw.get("state") == "interest_failed":
            raise ArachneError(raw.get("error", "interest update failed"))
        admission = _delivery_report(raw["admission"])
        return InterestObservation(_id(raw["workspace"], "workspace"), raw["revision"],
                                   raw["topic"], raw["subscribed"], admission)

    def publish(self, workspace: ID, revision: int, topic: str, payload: bytes) -> DeliveryReport:
        raw = self.raw_call("publish", workspace=_fixed_bytes(workspace, 32, "workspace ID"),
                            revision=revision, topic=topic, payload=payload)
        return _delivery_report(raw)

    def poll(self) -> Publication | None:
        raw = self.raw_call("poll")
        return None if raw is None else Publication(
            _id(raw["workspace"], "workspace"), raw["revision"], _id(raw["sender"], "sender"),
            raw["topic"], bytes(raw["payload"]),
        )

    def poll_recovered_publication(self) -> RecoveredPublication | None:
        raw = self.raw_call("poll_recovered_publication")
        return None if raw is None else _recovered_publication(raw)

    def fetch_recovery_range(self, request: RecoveryRangeRequest) -> RecoveryRangeStatus:
        raw = self.raw_call("fetch_recovery_range", **asdict(request))
        return _recovery_range_status(raw)

    def poll_recovery_range(self) -> RecoveryRangeStatus | None:
        raw = self.raw_call("poll_recovery_range")
        return None if raw is None else _recovery_range_status(raw)

    def cancel_recovery_range(self) -> None:
        raw = self.raw_call("cancel_recovery_range")
        if raw.get("state") != "recovery_range_cancelled":
            raise ArachneError("invalid recovery cancellation response")

    def stage_recovery_range(self, retain_until: int) -> RecoveryStage:
        raw, snapshot = self.raw_call_stored("stage_recovery_range", retain_until=retain_until)
        state = raw["state"]
        candidate = None
        if state == "awaiting_recovery_save":
            if not snapshot:
                raise ArachneError("recovery candidate has no snapshot")
            candidate = RecoveryCandidate(_id(raw["workspace"], "workspace"), snapshot,
                                          raw.get("publication_count", 0),
                                          raw.get("already_received", 0), raw["durable"])
        elif state not in ("recovery_already_covered", "recovery_no_new_objects"):
            raise ArachneError(f"unknown recovery stage state: {state}")
        return RecoveryStage(state, candidate)

    def adopt_recovery(self, snapshot: bytes) -> RecoveryAdoption:
        raw, _ = self.raw_call_stored("adopt_recovery", snapshot=snapshot)
        if raw["state"] == "recovery_adopted":
            recovered, missing = raw["publication_count"], 0
        elif raw["state"] == "direct_miss_adopted":
            recovered, missing = 0, raw["missing_count"]
        else:
            raise ArachneError(f"unknown recovery adoption state: {raw['state']}")
        return RecoveryAdoption(_id(raw["workspace"], "workspace"), raw["epoch"], raw["members"],
                                raw["durable"], recovered, missing)

    def close(self) -> None:
        with self._lock:
            if self._closed:
                return
            result = self._library.arachne_sdk_close(self._handle)
            value = _take_buffer(self._library, result.value)
            if result.status:
                raise ArachneError(value.decode("utf-8", errors="replace"))
            self._closed = True

    def __enter__(self) -> Client:
        self._ensure_open()
        return self

    def __exit__(self, exc_type: Any, exc: Any, traceback: Any) -> None:
        self.close()

    def _ensure_open(self) -> None:
        if self._closed:
            raise ArachneError("Arachne client is closed")

    def _native_call(self, function: Any, *args: Any) -> bytes:
        with self._lock:
            self._ensure_open()
            result = function(*args)
            value = _take_buffer(self._library, result.value)
            if result.status:
                raise ArachneError(value.decode("utf-8", errors="replace"))
            return value

    @staticmethod
    def _request(op: str, params: dict[str, Any]) -> bytes:
        if not op:
            raise ValueError("operation name is required")
        if "op" in params:
            raise ValueError("params must not contain op")
        request = {"op": op, **_normalize(params)}
        return json.dumps(
            request, separators=(",", ":"), allow_nan=False
        ).encode("utf-8")


def _fixed_bytes(value: bytes, size: int, name: str) -> bytes:
    result = bytes(value)
    if len(result) != size:
        raise ValueError(f"{name} must be exactly {size} bytes")
    return result


def _id(value: Any, name: str) -> ID:
    return ID(_fixed_bytes(bytes(value), 32, name))


def _optional_id(value: Any, name: str) -> ID | None:
    return None if value is None else _id(value, name)


def _activity(raw: dict[str, Any]) -> Activity:
    return Activity(WorkspacePhase(raw["state"]), raw.get("reason"))


def _workspace_info(raw: dict[str, Any]) -> WorkspaceInfo:
    activity = _activity(raw["activity"])
    return WorkspaceInfo(
        workspace=_id(raw["workspace"], "workspace"),
        workspace_name=raw.get("workspace_name"), epoch=raw["epoch"],
        member_count=raw["members"], durable=raw["durable"],
        phase=activity.phase, reason=activity.reason,
    )


def _restore_result(raw: dict[str, Any]) -> RestoreResult:
    member_raw = raw.get("member")
    member = None if member_raw is None else RestoredMember(
        _id(member_raw["id"], "member"), member_raw.get("display_name")
    )
    activity_raw = raw.get("activity")
    return RestoreResult(
        workspace=_id(raw["workspace"], "workspace"),
        workspace_name=raw.get("workspace_name"),
        endpoint=_optional_id(raw.get("endpoint"), "endpoint"),
        epoch=raw.get("epoch"), member_count=raw.get("members"),
        durable=raw.get("durable", False), workspace_ready=raw.get("workspace_ready"),
        state=raw.get("state"), activity=None if activity_raw is None else _activity(activity_raw),
        member=member, key_package=bytes(raw.get("key_package") or []),
        admission_request=bytes(raw.get("admission_request") or []),
        personal_invitation=raw.get("personal_invitation"),
        commit_digest=bytes(raw.get("commit_digest") or []),
    )


def _authorization(raw: dict[str, Any]) -> AdmissionAuthorization:
    return AdmissionAuthorization(
        invitation_key=_id(raw["invitation_key"], "invitation key"),
        grant_signature=bytes(raw["grant_signature"]),
        redemption_signature=bytes(raw["redemption_signature"]),
    )


def _admission_reply(raw: dict[str, Any]) -> AdmissionReply:
    return AdmissionReply(
        workspace=_id(raw["workspace"], "workspace"), epoch=raw["epoch"],
        commit=bytes(raw["commit"]), welcome=bytes(raw["welcome"]),
        authorization=_authorization(raw["authorization"]),
    )


def _member_info(raw: dict[str, Any]) -> MemberInfo:
    return MemberInfo(
        id=_id(raw["id"], "member ID"), endpoint=_id(raw["endpoint"], "endpoint"),
        administrator=raw["administrator"], self_member=raw["self"],
        display_name=raw.get("display_name"), kind=MemberKind(raw["kind"]),
        presence=Presence(raw["presence"]),
        last_contact_age_ms=raw.get("last_contact_age_ms"),
        presence_fresh_for_ms=raw.get("presence_fresh_for_ms"),
    )


def _peer_route(raw: dict[str, Any]) -> PeerRoute:
    return PeerRoute(_id(raw["member"], "member ID"), raw["route"], raw["rtt_ms"])


def _duration(raw: dict[str, Any]) -> DurationSummary:
    return DurationSummary(raw["count"], raw["total_us"], raw["max_us"])


def _delivery_report(raw: dict[str, Any]) -> DeliveryReport:
    return DeliveryReport(
        admitted=tuple(_id(item, "member ID") for item in raw["admitted"]),
        queued=raw["queued"],
        failed=tuple(DeliveryFailure(_id(item["peer"], "peer ID"), item["error"])
                     for item in raw["failed"]),
    )


def _protected_publication(raw: dict[str, Any]) -> ReceivedProtectedPublication:
    return ReceivedProtectedPublication(
        workspace=_id(raw["workspace"], "workspace"), revision=raw["revision"],
        member=_id(raw["member"], "member ID"), endpoint=_id(raw["endpoint"], "endpoint"),
        topic=raw["topic"], id=RecordID(_fixed_bytes(bytes(raw["id"]), 16, "record ID")),
        sequence=raw.get("sequence"), payload=bytes(raw["payload"]),
        recipients=tuple(_id(item, "recipient ID") for item in raw.get("recipients", [])),
    )


def _recovered_publication(raw: dict[str, Any]) -> RecoveredPublication:
    return RecoveredPublication(
        workspace=_id(raw["workspace"], "workspace"), revision=raw["revision"],
        member=_id(raw["member"], "member ID"), endpoint=_id(raw["endpoint"], "endpoint"),
        topic=raw["topic"], id=RecordID(_fixed_bytes(bytes(raw["id"]), 16, "record ID")),
        sequence=raw.get("sequence"), payload=bytes(raw["payload"]),
    )


def _recovery_range_status(raw: dict[str, Any]) -> RecoveryRangeStatus:
    if raw.get("state") not in {
        "recovery_range_pending", "recovery_range_ready", "recovery_source_waiting",
        "recovery_source_unavailable", "recovery_range_rejected", "recovery_range_cancelled",
    }:
        raise ArachneError(f"unknown recovery range state: {raw.get('state')}")
    ready = None
    if raw["state"] == "recovery_range_ready":
        ready = RecoveryRangeReady(
            workspace=_id(raw["workspace"], "workspace"), author=_id(raw["author"], "author"),
            peer=_id(raw["peer"], "peer"), epoch=raw["epoch"], revision=raw["revision"],
            after=raw["after"], through=raw["through"], packet_count=raw["packet_count"],
            retained_bytes=raw["retained_bytes"], automatic_source=raw["automatic_source"],
            attempted=raw.get("attempted"),
        )
    return RecoveryRangeStatus(
        state=raw["state"], ready=ready, candidate_count=raw.get("candidate_count", 0),
        automatic_source=raw.get("automatic_source", False), attempted=raw.get("attempted"),
        reason=raw.get("reason"),
    )
