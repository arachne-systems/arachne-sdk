package arachne

/*
#cgo linux LDFLAGS: -L${SRCDIR}/../../target/debug -larachne_sdk -Wl,-rpath,${SRCDIR}/../../target/debug
#cgo darwin LDFLAGS: -L${SRCDIR}/../../target/debug -larachne_sdk -Wl,-rpath,${SRCDIR}/../../target/debug
#include "../../include/arachne_sdk.h"
*/
import "C"

import (
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"sync"
	"unsafe"
)

// Network selects the endpoint discovery profile.
type Network uint32

const (
	Direct Network = iota
	LAN
	Nearby
	WAN
	RelayOnly
	WANOnly
)

type ID [32]byte
type RecordID [16]byte

// Bytes carries Rust serde byte vectors as JSON integer arrays.
type Bytes []byte

func (b *Bytes) UnmarshalJSON(data []byte) error {
	var values []uint8
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	*b = Bytes(values)
	return nil
}

func (b Bytes) MarshalJSON() ([]byte, error) {
	values := make([]uint16, len(b))
	for i, value := range b {
		values[i] = uint16(value)
	}
	return json.Marshal(values)
}

type WorkspacePhase string

const (
	PhaseEmpty         WorkspacePhase = "empty"
	PhaseCreating      WorkspacePhase = "creating"
	PhaseJoining       WorkspacePhase = "joining"
	PhaseSynchronizing WorkspacePhase = "synchronizing"
	PhaseActive        WorkspacePhase = "active"
	PhaseRecovering    WorkspacePhase = "recovering"
	PhaseLeaving       WorkspacePhase = "leaving"
	PhaseResetting     WorkspacePhase = "resetting"
	PhaseRemoved       WorkspacePhase = "removed"
	PhaseFailed        WorkspacePhase = "failed"
)

type Activity struct {
	Phase  WorkspacePhase `json:"state"`
	Reason *string        `json:"reason"`
}
type EndpointInfo struct {
	EndpointKey    ID     `json:"endpoint_key"`
	BoundAddress   string `json:"bound_address"`
	WorkspaceReady bool   `json:"workspace_ready"`
}
type WorkspaceState struct {
	EndpointKey    ID
	Workspace      *ID
	WorkspaceReady bool
	Durable        bool
	Phase          WorkspacePhase
	Reason         *string
}
type WorkspaceInfo struct {
	Workspace     ID
	WorkspaceName *string
	Epoch         uint64
	MemberCount   int
	Durable       bool
	Phase         WorkspacePhase
	Reason        *string
}
type WorkspaceCandidate struct {
	Workspace ID
	Snapshot  []byte
}
type RestoredMember struct {
	ID          ID      `json:"id"`
	DisplayName *string `json:"display_name"`
}
type RestoreResult struct {
	Workspace          ID              `json:"workspace"`
	WorkspaceName      *string         `json:"workspace_name"`
	Endpoint           *ID             `json:"endpoint"`
	Epoch              *uint64         `json:"epoch"`
	MemberCount        *int            `json:"members"`
	Durable            bool            `json:"durable"`
	WorkspaceReady     *bool           `json:"workspace_ready"`
	State              string          `json:"state"`
	Activity           *Activity       `json:"activity"`
	Member             *RestoredMember `json:"member"`
	KeyPackage         Bytes           `json:"key_package"`
	AdmissionRequest   Bytes           `json:"admission_request"`
	PersonalInvitation *bool           `json:"personal_invitation"`
	CommitDigest       Bytes           `json:"commit_digest"`
}
type JoinRequest struct {
	Workspace        ID
	Member           ID
	Endpoint         ID
	AdmissionRequest Bytes
}
type AdmissionAuthorization struct {
	InvitationKey       ID    `json:"invitation_key"`
	GrantSignature      Bytes `json:"grant_signature"`
	RedemptionSignature Bytes `json:"redemption_signature"`
}
type JoinAdmissionStep struct {
	Commit        Bytes                  `json:"commit"`
	Authorization AdmissionAuthorization `json:"authorization"`
}
type AdmissionReply struct {
	Workspace     ID                     `json:"workspace"`
	Epoch         uint64                 `json:"epoch"`
	Commit        Bytes                  `json:"commit"`
	Welcome       Bytes                  `json:"welcome"`
	Authorization AdmissionAuthorization `json:"authorization"`
}
type MemberKind string

const (
	MemberPerson  MemberKind = "person"
	MemberService MemberKind = "service"
)

type Presence string

const (
	PresenceSelf      Presence = "self"
	PresenceUnknown   Presence = "unknown"
	PresenceReachable Presence = "reachable"
	PresenceStale     Presence = "stale"
)

type MemberInfo struct {
	ID                 ID         `json:"id"`
	Endpoint           ID         `json:"endpoint"`
	Administrator      bool       `json:"administrator"`
	Self               bool       `json:"self"`
	DisplayName        *string    `json:"display_name"`
	Kind               MemberKind `json:"kind"`
	Presence           Presence   `json:"presence"`
	LastContactAgeMS   *uint64    `json:"last_contact_age_ms"`
	PresenceFreshForMS *uint64    `json:"presence_fresh_for_ms"`
}
type MemberRoster struct {
	Workspace             ID
	WorkspaceName         *string
	WorkspaceNameRevision uint64
	WorkspaceNameHead     ID
	Epoch                 uint64
	Members               []MemberInfo
	ProfileCount          int
	ProfilesRetained      bool
}
type RouteHint struct {
	Peer    ID     `json:"peer"`
	Address string `json:"address"`
}
type InvitationInfo struct {
	Workspace      ID          `json:"workspace"`
	WorkspaceName  *string     `json:"workspace_name"`
	Invitation     Bytes       `json:"invitation"`
	InvitationKey  ID          `json:"invitation_key"`
	Checkpoint     Bytes       `json:"checkpoint"`
	Peer           ID          `json:"peer"`
	BootstrapPeers []ID        `json:"bootstrap_peers"`
	Address        string      `json:"address"`
	Routes         []RouteHint `json:"routes"`
}
type InvitationDetails struct {
	Workspace     ID
	InvitationKey ID
	WorkspaceName *string
	Epoch         uint64
	Personal      bool
	Automatic     bool
	ExpiresAt     uint64
}
type PeerPolicy struct {
	Peer      ID       `json:"peer"`
	Publish   []string `json:"publish"`
	Subscribe []string `json:"subscribe"`
}
type PeerRoute struct {
	Member ID     `json:"member"`
	Route  string `json:"route"`
	RTTMS  uint64 `json:"rtt_ms"`
}
type ConnectivityReport struct {
	Workspace    ID
	Paths        []PeerRoute
	PathsLimited bool
	ReceiveQueue int
	RepairJobs   int
}
type DurationSummary struct {
	Count   uint64 `json:"count"`
	TotalUS uint64 `json:"total_us"`
	MaxUS   uint64 `json:"max_us"`
}
type ControlTimingMetrics struct {
	Inquiry     DurationSummary `json:"inquiry"`
	HostWait    DurationSummary `json:"host_wait"`
	HostService DurationSummary `json:"host_service"`
}
type MembershipGossipMetrics struct {
	Sent        uint64 `json:"sent"`
	NoOverlay   uint64 `json:"no_overlay"`
	Failed      uint64 `json:"failed"`
	Received    uint64 `json:"received"`
	Staged      uint64 `json:"staged"`
	Rejected    uint64 `json:"rejected"`
	RangePulled uint64 `json:"range_pulled"`
	RangeFailed uint64 `json:"range_failed"`
}
type ConnectionCapacityMetrics struct {
	Evicted uint64 `json:"evicted"`
	Refused uint64 `json:"refused"`
}
type WorkspaceMetrics struct {
	Workspace           ID
	Phase               WorkspacePhase
	Reason              *string
	ReceivedBytes       uint64
	SentBytes           uint64
	ReceiveQueue        int
	AdmissionQueue      int
	AdmissionQueueBytes int
	AdmissionWaiters    int
	AdmissionInFlight   int
	ApprovalPending     int
	PendingObjects      int
	RepairJobs          int
	GossipNeighbors     int
	ControlTiming       ControlTimingMetrics
	MembershipGossip    MembershipGossipMetrics
	ConnectionCapacity  ConnectionCapacityMetrics
	Paths               []PeerRoute
	PathsLimited        bool
}
type DeliveryFailure struct {
	Peer  ID     `json:"peer"`
	Error string `json:"error"`
}
type DeliveryReport struct {
	Admitted []ID              `json:"admitted"`
	Queued   bool              `json:"queued"`
	Failed   []DeliveryFailure `json:"failed"`
}
type PublicationCandidate struct {
	Workspace ID
	Snapshot  []byte
}
type PublicationCurrent struct {
	Selector       ID     `json:"selector"`
	ReplacementKey ID     `json:"replacement_key"`
	ExpiresAt      uint64 `json:"expires_at"`
	Tombstone      bool   `json:"tombstone"`
}
type ProtectedReceptionCandidate struct {
	Workspace ID
	Snapshot  []byte
}
type ReceivedProtectedPublication struct {
	Workspace  ID       `json:"workspace"`
	Revision   uint64   `json:"revision"`
	Member     ID       `json:"member"`
	Endpoint   ID       `json:"endpoint"`
	Topic      string   `json:"topic"`
	ID         RecordID `json:"id"`
	Sequence   *uint64  `json:"sequence"`
	Payload    Bytes    `json:"payload"`
	Recipients []ID     `json:"recipients"`
}
type InterestObservation struct {
	Workspace  ID             `json:"workspace"`
	Revision   uint64         `json:"revision"`
	Topic      string         `json:"topic"`
	Subscribed bool           `json:"subscribed"`
	Admission  DeliveryReport `json:"admission"`
}
type Publication struct {
	Workspace ID     `json:"workspace"`
	Revision  uint64 `json:"revision"`
	Sender    ID     `json:"sender"`
	Topic     string `json:"topic"`
	Payload   Bytes  `json:"payload"`
}
type RecoveredPublication struct {
	Workspace ID       `json:"workspace"`
	Revision  uint64   `json:"revision"`
	Member    ID       `json:"member"`
	Endpoint  ID       `json:"endpoint"`
	Topic     string   `json:"topic"`
	ID        RecordID `json:"id"`
	Sequence  *uint64  `json:"sequence"`
	Payload   Bytes    `json:"payload"`
}
type RecoveryRangeRequest struct {
	Peer     *ID      `json:"peer"`
	Author   *ID      `json:"author"`
	Revision uint64   `json:"revision"`
	Topics   []string `json:"topics"`
	After    *uint64  `json:"after"`
	Through  *uint64  `json:"through"`
}
type RecoveryRangeReady struct {
	Workspace       ID     `json:"workspace"`
	Author          ID     `json:"author"`
	Peer            ID     `json:"peer"`
	Epoch           uint64 `json:"epoch"`
	Revision        uint64 `json:"revision"`
	After           uint64 `json:"after"`
	Through         uint64 `json:"through"`
	PacketCount     int    `json:"packet_count"`
	RetainedBytes   int    `json:"retained_bytes"`
	AutomaticSource bool   `json:"automatic_source"`
	Attempted       *int   `json:"attempted"`
}
type RecoveryRangeStatus struct {
	State           string              `json:"state"`
	CandidateCount  int                 `json:"candidate_count,omitempty"`
	AutomaticSource bool                `json:"automatic_source,omitempty"`
	Ready           *RecoveryRangeReady `json:"-"`
	Attempted       int                 `json:"attempted,omitempty"`
	Reason          string              `json:"reason,omitempty"`
}
type RecoveryCandidate struct {
	Workspace        ID
	Snapshot         []byte
	PublicationCount int
	AlreadyReceived  int
	Durable          bool
}
type RecoveryStage struct {
	State     string
	Candidate *RecoveryCandidate
}
type RecoveryAdoption struct {
	Workspace             ID
	Epoch                 uint64
	MemberCount           int
	Durable               bool
	RecoveredPublications int
	MissingPublications   int
}

func (r *RecoveryRangeStatus) UnmarshalJSON(data []byte) error {
	var raw struct {
		State           string `json:"state"`
		CandidateCount  int    `json:"candidate_count"`
		AutomaticSource *bool  `json:"automatic_source"`
		Workspace       ID     `json:"workspace"`
		Author          ID     `json:"author"`
		Peer            ID     `json:"peer"`
		Epoch           uint64 `json:"epoch"`
		Revision        uint64 `json:"revision"`
		After           uint64 `json:"after"`
		Through         uint64 `json:"through"`
		PacketCount     int    `json:"packet_count"`
		RetainedBytes   int    `json:"retained_bytes"`
		Attempted       *int   `json:"attempted"`
		Reason          string `json:"reason"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	automaticSource := raw.State == "recovery_source_waiting"
	if raw.AutomaticSource != nil {
		automaticSource = *raw.AutomaticSource
	}
	*r = RecoveryRangeStatus{State: raw.State, CandidateCount: raw.CandidateCount, AutomaticSource: automaticSource, Attempted: 0, Reason: raw.Reason}
	if raw.Attempted != nil {
		r.Attempted = *raw.Attempted
	}
	if raw.State == "recovery_range_ready" {
		r.Ready = &RecoveryRangeReady{Workspace: raw.Workspace, Author: raw.Author, Peer: raw.Peer, Epoch: raw.Epoch,
			Revision: raw.Revision, After: raw.After, Through: raw.Through, PacketCount: raw.PacketCount,
			RetainedBytes: raw.RetainedBytes, AutomaticSource: automaticSource, Attempted: raw.Attempted}
	}
	switch raw.State {
	case "recovery_range_pending", "recovery_range_ready", "recovery_source_waiting",
		"recovery_source_unavailable", "recovery_range_rejected", "recovery_range_cancelled":
	default:
		return errors.New("unknown recovery range state: " + raw.State)
	}
	return nil
}

// Client is one blocking endpoint session. Calls on a Client are serialized.
type Client struct {
	mu     sync.Mutex
	handle int64
	closed bool
}

// Open starts a client. A nonempty secret must contain exactly 32 bytes.
// Direct permits a nil secret for an ephemeral endpoint; other profiles require one.
func Open(network Network, secret []byte) (*Client, error) {
	var secretPtr *C.uint8_t
	if len(secret) != 0 {
		secretPtr = (*C.uint8_t)(unsafe.Pointer(&secret[0]))
	}
	result := C.arachne_sdk_open(C.uint32_t(network), secretPtr, C.size_t(len(secret)))
	runtime.KeepAlive(secret)
	value := takeBuffer(result.value)
	if result.status != 0 {
		return nil, errors.New(string(value))
	}
	handle, err := strconv.ParseInt(string(value), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid native client handle: %w", err)
	}
	return &Client{handle: handle}, nil
}

// ClientConfig selects the endpoint profile and stable endpoint credential.
type ClientConfig struct {
	Network Network
	Secret  []byte
}

// OpenConfig starts a client using a named configuration.
func OpenConfig(config ClientConfig) (*Client, error) {
	return Open(config.Network, config.Secret)
}

// RawCall invokes an advanced core JSON operation. Prefer the typed Client methods.
// Byte slices in params are encoded as JSON byte arrays.
func (c *Client) RawCall(op string, params map[string]any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("Arachne client is closed")
	}
	request, err := encodeRequest(op, params)
	if err != nil {
		return nil, err
	}
	result := C.arachne_sdk_execute(C.int64_t(c.handle), bytePointer(request), C.size_t(len(request)))
	runtime.KeepAlive(request)
	value := takeBuffer(result.value)
	if result.status != 0 {
		return nil, errors.New(string(value))
	}
	return json.RawMessage(value), nil
}

// Call is retained as a compatibility alias for RawCall.
func (c *Client) Call(op string, params map[string]any) (json.RawMessage, error) {
	return c.RawCall(op, params)
}

// CallStored invokes an operation that stages or adopts an opaque snapshot.
// Keep the returned snapshot bytes exact and durably save staged bytes before adoption.
func (c *Client) CallStored(op string, params map[string]any, snapshot []byte) (json.RawMessage, []byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, nil, errors.New("Arachne client is closed")
	}
	request, err := encodeRequest(op, params)
	if err != nil {
		return nil, nil, err
	}
	result := C.arachne_sdk_execute_stored(
		C.int64_t(c.handle),
		bytePointer(request),
		C.size_t(len(request)),
		bytePointer(snapshot),
		C.size_t(len(snapshot)),
	)
	runtime.KeepAlive(request)
	runtime.KeepAlive(snapshot)
	value := takeBuffer(result.value)
	stored := takeBuffer(result.snapshot)
	if result.status != 0 {
		return nil, nil, errors.New(string(value))
	}
	return json.RawMessage(value), stored, nil
}

// RawCallStored invokes an advanced operation that stages or adopts a snapshot.
func (c *Client) RawCallStored(op string, params map[string]any, snapshot []byte) (json.RawMessage, []byte, error) {
	return c.CallStored(op, params, snapshot)
}

// Close releases the endpoint and is safe to call more than once.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	result := C.arachne_sdk_close(C.int64_t(c.handle))
	value := takeBuffer(result.value)
	if result.status != 0 {
		return errors.New(string(value))
	}
	c.closed = true
	return nil
}

func (c *Client) Endpoint() (EndpointInfo, error) {
	value, err := c.nativeCall(func() C.ArachneResult { return C.arachne_sdk_describe(C.int64_t(c.handle)) })
	if err != nil {
		return EndpointInfo{}, err
	}
	return decode[EndpointInfo](json.RawMessage(value), nil)
}

func (c *Client) WorkspaceState() (WorkspaceState, error) {
	var raw struct {
		Workspace      *ID      `json:"workspace"`
		WorkspaceReady bool     `json:"workspace_ready"`
		Durable        bool     `json:"durable"`
		Activity       Activity `json:"activity"`
	}
	value, err := c.RawCall("workspace_state", nil)
	if err != nil {
		return WorkspaceState{}, err
	}
	if err := json.Unmarshal(value, &raw); err != nil {
		return WorkspaceState{}, err
	}
	endpoint, err := c.Endpoint()
	if err != nil {
		return WorkspaceState{}, err
	}
	return WorkspaceState{
		EndpointKey: endpoint.EndpointKey, Workspace: raw.Workspace,
		WorkspaceReady: raw.WorkspaceReady, Durable: raw.Durable,
		Phase: raw.Activity.Phase, Reason: raw.Activity.Reason,
	}, nil
}

func (c *Client) CreateWorkspace(displayName string, workspaceName *string) (WorkspaceInfo, error) {
	value, err := c.RawCall("create_workspace", map[string]any{
		"display_name": displayName, "workspace_name": workspaceName,
	})
	return workspaceInfo(value, err)
}

func (c *Client) BeginJoin(invitation, checkpoint []byte, displayName string, peers []ID) (JoinRequest, error) {
	if peers == nil {
		peers = []ID{}
	}
	value, err := c.RawCall("begin_join", map[string]any{
		"invitation": invitation, "checkpoint": checkpoint,
		"display_name": displayName, "peers": peers,
	})
	if err != nil {
		return JoinRequest{}, err
	}
	var raw struct {
		Workspace ID `json:"workspace"`
		Endpoint  ID `json:"endpoint"`
		Member    struct {
			ID ID `json:"id"`
		} `json:"member"`
		AdmissionRequest Bytes `json:"admission_request"`
	}
	if err := json.Unmarshal(value, &raw); err != nil {
		return JoinRequest{}, err
	}
	return JoinRequest{Workspace: raw.Workspace, Member: raw.Member.ID, Endpoint: raw.Endpoint, AdmissionRequest: raw.AdmissionRequest}, nil
}

func (c *Client) DriveJoin() (json.RawMessage, error) { return c.RawCall("drive_join", nil) }

func (c *Client) StageAdmission(endpoint ID, request []byte) (WorkspaceCandidate, error) {
	value, snapshot, err := c.CallStored("stage_admission", map[string]any{
		"authenticated_endpoint": endpoint, "request": request,
	}, nil)
	return workspaceCandidate(value, snapshot, err)
}

func (c *Client) AdoptAdmission(snapshot []byte) (WorkspaceInfo, error) {
	value, _, err := c.CallStored("adopt_admission", nil, snapshot)
	return workspaceInfo(value, err)
}

func (c *Client) RetainedAdmission(endpoint ID, request []byte) (AdmissionReply, error) {
	value, err := c.RawCall("retained_admission", map[string]any{
		"authenticated_endpoint": endpoint, "request": request,
	})
	return decode[AdmissionReply](value, err)
}

func (c *Client) StageJoin(welcome []byte, steps []JoinAdmissionStep) (WorkspaceCandidate, error) {
	if steps == nil {
		steps = []JoinAdmissionStep{}
	}
	value, snapshot, err := c.CallStored("stage_join", map[string]any{"commits": steps}, welcome)
	return workspaceCandidate(value, snapshot, err)
}

func (c *Client) AdoptJoin(snapshot []byte) (WorkspaceInfo, error) {
	value, _, err := c.CallStored("adopt_join", nil, snapshot)
	return workspaceInfo(value, err)
}

func (c *Client) EnableRecordStorage(path string, root [32]byte) error {
	pathBytes := []byte(path)
	_, err := c.nativeCall(func() C.ArachneResult {
		return C.arachne_sdk_enable_record_storage(C.int64_t(c.handle), bytePointer(pathBytes), C.size_t(len(pathBytes)), bytePointer(root[:]), C.size_t(len(root)))
	})
	runtime.KeepAlive(pathBytes)
	runtime.KeepAlive(root)
	return err
}

func (c *Client) RestoreRecordStorage(path string, root [32]byte, workspace ID) (RestoreResult, error) {
	pathBytes := []byte(path)
	value, err := c.nativeCall(func() C.ArachneResult {
		return C.arachne_sdk_restore_record_storage(C.int64_t(c.handle), bytePointer(pathBytes), C.size_t(len(pathBytes)), bytePointer(root[:]), C.size_t(len(root)), bytePointer(workspace[:]), C.size_t(len(workspace)))
	})
	runtime.KeepAlive(pathBytes)
	runtime.KeepAlive(root)
	runtime.KeepAlive(workspace)
	if err != nil {
		return RestoreResult{}, err
	}
	return decode[RestoreResult](json.RawMessage(value), nil)
}

func (c *Client) SaveCandidate(snapshot []byte) error {
	_, err := c.nativeCall(func() C.ArachneResult {
		return C.arachne_sdk_save_candidate(C.int64_t(c.handle), bytePointer(snapshot), C.size_t(len(snapshot)))
	})
	runtime.KeepAlive(snapshot)
	return err
}

func (c *Client) MemberRoster() (MemberRoster, error) {
	value, err := c.RawCall("member_roster", nil)
	if err != nil {
		return MemberRoster{}, err
	}
	var raw struct {
		Workspace             ID                `json:"workspace"`
		WorkspaceName         *string           `json:"workspace_name"`
		WorkspaceNameRevision uint64            `json:"workspace_name_revision"`
		WorkspaceNameHead     ID                `json:"workspace_name_head"`
		Epoch                 uint64            `json:"epoch"`
		Members               []MemberInfo      `json:"members"`
		Profiles              []json.RawMessage `json:"profiles"`
		ProfilesRetained      *bool             `json:"profiles_retained"`
	}
	if err := json.Unmarshal(value, &raw); err != nil {
		return MemberRoster{}, err
	}
	retained := true
	if raw.ProfilesRetained != nil {
		retained = *raw.ProfilesRetained
	}
	return MemberRoster{Workspace: raw.Workspace, WorkspaceName: raw.WorkspaceName,
		WorkspaceNameRevision: raw.WorkspaceNameRevision, WorkspaceNameHead: raw.WorkspaceNameHead,
		Epoch: raw.Epoch, Members: raw.Members, ProfileCount: len(raw.Profiles), ProfilesRetained: retained}, nil
}

func (c *Client) UseServiceProfile() error { return c.callOK("use_service_profile", nil) }

func (c *Client) IssueInvitation() (InvitationInfo, error) {
	value, err := c.RawCall("issue_invitation", nil)
	return decode[InvitationInfo](value, err)
}

func (c *Client) InspectInvitation(invitation, checkpoint []byte) (InvitationDetails, error) {
	value, err := c.RawCall("inspect_invitation", map[string]any{"invitation": invitation, "checkpoint": checkpoint})
	if err != nil {
		return InvitationDetails{}, err
	}
	var raw struct {
		Workspace     ID      `json:"workspace"`
		InvitationKey ID      `json:"invitation_key"`
		WorkspaceName *string `json:"workspace_name"`
		Epoch         uint64  `json:"epoch"`
		Personal      bool    `json:"personal_invitation"`
		Automatic     bool    `json:"automatic_approval"`
		ExpiresAt     uint64  `json:"expires_at"`
	}
	if err := json.Unmarshal(value, &raw); err != nil {
		return InvitationDetails{}, err
	}
	return InvitationDetails{Workspace: raw.Workspace, InvitationKey: raw.InvitationKey,
		WorkspaceName: raw.WorkspaceName, Epoch: raw.Epoch, Personal: raw.Personal,
		Automatic: raw.Automatic, ExpiresAt: raw.ExpiresAt}, nil
}

func (c *Client) Connectivity() (ConnectivityReport, error) {
	metrics, err := c.Metrics()
	if err != nil {
		return ConnectivityReport{}, err
	}
	return ConnectivityReport{Workspace: metrics.Workspace, Paths: metrics.Paths,
		PathsLimited: metrics.PathsLimited, ReceiveQueue: metrics.ReceiveQueue,
		RepairJobs: metrics.RepairJobs}, nil
}

func (c *Client) Metrics() (WorkspaceMetrics, error) {
	value, err := c.RawCall("workspace_metrics", nil)
	if err != nil {
		return WorkspaceMetrics{}, err
	}
	var raw struct {
		Workspace           ID                        `json:"workspace"`
		Activity            Activity                  `json:"activity"`
		ReceivedBytes       uint64                    `json:"received_bytes"`
		SentBytes           uint64                    `json:"sent_bytes"`
		ReceiveQueue        int                       `json:"receive_queue"`
		AdmissionQueue      int                       `json:"admission_queue"`
		AdmissionQueueBytes int                       `json:"admission_queue_bytes"`
		AdmissionWaiters    int                       `json:"admission_waiters"`
		AdmissionInFlight   int                       `json:"admission_in_flight"`
		ApprovalPending     int                       `json:"approval_pending"`
		PendingObjects      int                       `json:"pending_objects"`
		RepairJobs          int                       `json:"repair_jobs"`
		GossipNeighbors     int                       `json:"gossip_neighbors"`
		ControlTiming       ControlTimingMetrics      `json:"control_timing"`
		MembershipGossip    MembershipGossipMetrics   `json:"membership_gossip"`
		ConnectionCapacity  ConnectionCapacityMetrics `json:"connection_capacity"`
		Paths               []PeerRoute               `json:"paths"`
		PathsLimited        bool                      `json:"paths_limited"`
	}
	if err := json.Unmarshal(value, &raw); err != nil {
		return WorkspaceMetrics{}, err
	}
	return WorkspaceMetrics{Workspace: raw.Workspace, Phase: raw.Activity.Phase, Reason: raw.Activity.Reason,
		ReceivedBytes: raw.ReceivedBytes, SentBytes: raw.SentBytes, ReceiveQueue: raw.ReceiveQueue,
		AdmissionQueue: raw.AdmissionQueue, AdmissionQueueBytes: raw.AdmissionQueueBytes,
		AdmissionWaiters: raw.AdmissionWaiters, AdmissionInFlight: raw.AdmissionInFlight,
		ApprovalPending: raw.ApprovalPending, PendingObjects: raw.PendingObjects, RepairJobs: raw.RepairJobs,
		GossipNeighbors: raw.GossipNeighbors, ControlTiming: raw.ControlTiming,
		MembershipGossip: raw.MembershipGossip, ConnectionCapacity: raw.ConnectionCapacity,
		Paths: raw.Paths, PathsLimited: raw.PathsLimited}, nil
}

func (c *Client) NetworkChange() error { return c.callOK("network_change", nil) }
func (c *Client) Cancel() error {
	_, err := c.nativeCall(func() C.ArachneResult { return C.arachne_sdk_cancel(C.int64_t(c.handle)) })
	return err
}
func (c *Client) WaitForWork() (bool, error) {
	value, err := c.nativeCall(func() C.ArachneResult { return C.arachne_sdk_wait_for_work(C.int64_t(c.handle)) })
	if err != nil {
		return false, err
	}
	return string(value) == "1", nil
}
func (c *Client) PollControl() (bool, error) {
	value, err := c.RawCall("poll_admission", nil)
	if err != nil {
		return false, err
	}
	return string(value) != "null", nil
}
func (c *Client) AddAddressHint(peer ID, address string) error {
	return c.callOK("add_address_hint", map[string]any{"peer": peer, "address": address})
}
func (c *Client) InstallPolicy(workspace ID, revision uint64, endpoints []PeerPolicy) error {
	if endpoints == nil {
		endpoints = []PeerPolicy{}
	}
	return c.callOK("install_verified_policy", map[string]any{"workspace": workspace, "revision": revision, "endpoints": endpoints})
}
func (c *Client) InstallWorkspacePolicy(revision uint64) error {
	return c.callOK("install_workspace_policy", map[string]any{"revision": revision})
}
func (c *Client) EnableObjectDelivery() error {
	state, err := c.WorkspaceState()
	if err != nil {
		return err
	}
	value, snapshot, err := c.CallStored("enable_object_delivery", nil, nil)
	if err != nil {
		return err
	}
	if len(snapshot) == 0 {
		var raw struct {
			State string `json:"state"`
		}
		if err := json.Unmarshal(value, &raw); err != nil {
			return err
		}
		if raw.State == "object_delivery_enabled" {
			return nil
		}
		return errors.New("object delivery returned no adoptable snapshot")
	}
	if state.Durable {
		if err := c.SaveCandidate(snapshot); err != nil {
			return err
		}
	}
	_, _, err = c.CallStored("adopt_reception", nil, snapshot)
	return err
}

func (c *Client) StageProtectedPublication(workspace ID, revision uint64, topic string, id RecordID, payload []byte) (PublicationCandidate, error) {
	return c.StageProtectedPublicationWithCurrent(workspace, revision, topic, id, payload, nil)
}
func (c *Client) StageProtectedPublicationWithCurrent(workspace ID, revision uint64, topic string, id RecordID, payload []byte, current *PublicationCurrent) (PublicationCandidate, error) {
	value, snapshot, err := c.CallStored("stage_network_publication", map[string]any{
		"revision": revision, "topic": topic, "id": id, "payload": payload, "current": current,
	}, nil)
	if err != nil {
		return PublicationCandidate{}, err
	}
	var raw struct {
		Workspace ID `json:"workspace"`
	}
	if err := json.Unmarshal(value, &raw); err != nil {
		return PublicationCandidate{}, err
	}
	if raw.Workspace != workspace {
		return PublicationCandidate{}, errors.New("publication candidate workspace mismatch")
	}
	return PublicationCandidate{Workspace: raw.Workspace, Snapshot: snapshot}, nil
}
func (c *Client) AdoptProtectedPublication(snapshot []byte) (DeliveryReport, error) {
	value, _, err := c.CallStored("adopt_publication", nil, snapshot)
	if err != nil {
		return DeliveryReport{}, err
	}
	var raw struct {
		Admission    DeliveryReport `json:"admission"`
		NetworkError string         `json:"network_error"`
	}
	if err := json.Unmarshal(value, &raw); err != nil {
		return DeliveryReport{}, err
	}
	if raw.NetworkError != "" {
		return DeliveryReport{}, errors.New(raw.NetworkError)
	}
	return raw.Admission, nil
}
func (c *Client) PollProtected() (*ProtectedReceptionCandidate, error) {
	value, snapshot, err := c.CallStored("poll_protected", nil, nil)
	if err != nil {
		return nil, err
	}
	if string(value) == "null" {
		return nil, nil
	}
	var raw struct {
		Workspace ID     `json:"workspace"`
		State     string `json:"state"`
	}
	if err := json.Unmarshal(value, &raw); err != nil {
		return nil, err
	}
	if raw.State != "awaiting_reception_save" || len(snapshot) == 0 {
		return nil, errors.New("protected reception has no adoptable snapshot")
	}
	return &ProtectedReceptionCandidate{Workspace: raw.Workspace, Snapshot: snapshot}, nil
}
func (c *Client) AdoptProtectedReception(snapshot []byte) (ReceivedProtectedPublication, error) {
	value, _, err := c.CallStored("adopt_reception", nil, snapshot)
	return decode[ReceivedProtectedPublication](value, err)
}

func (c *Client) SetInterest(workspace ID, revision uint64, topic string, subscribed bool) error {
	return c.callOK("set_interest", map[string]any{"workspace": workspace, "revision": revision, "topic": topic, "subscribed": subscribed})
}
func (c *Client) PollInterest() (*InterestObservation, error) {
	value, err := c.RawCall("poll_interest", nil)
	if err != nil {
		return nil, err
	}
	if string(value) == "null" {
		return nil, nil
	}
	var raw struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(value, &raw); err != nil {
		return nil, err
	}
	if raw.State == "interest_pending" {
		return nil, nil
	}
	if raw.State == "interest_failed" {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(value, &failure)
		return nil, errors.New(failure.Error)
	}
	return decodePointer[InterestObservation](value)
}
func (c *Client) Publish(workspace ID, revision uint64, topic string, payload []byte) (DeliveryReport, error) {
	value, err := c.RawCall("publish", map[string]any{"workspace": workspace, "revision": revision, "topic": topic, "payload": payload})
	return decode[DeliveryReport](value, err)
}
func (c *Client) Poll() (*Publication, error) {
	value, err := c.RawCall("poll", nil)
	if err != nil || string(value) == "null" {
		return nil, err
	}
	return decodePointer[Publication](value)
}
func (c *Client) PollRecoveredPublication() (*RecoveredPublication, error) {
	value, err := c.RawCall("poll_recovered_publication", nil)
	if err != nil || string(value) == "null" {
		return nil, err
	}
	return decodePointer[RecoveredPublication](value)
}
func (c *Client) FetchRecoveryRange(request RecoveryRangeRequest) (RecoveryRangeStatus, error) {
	if request.Topics == nil {
		request.Topics = []string{}
	}
	value, err := c.RawCall("fetch_recovery_range", map[string]any{
		"peer": request.Peer, "author": request.Author, "revision": request.Revision,
		"topics": request.Topics, "after": request.After, "through": request.Through,
	})
	return decode[RecoveryRangeStatus](value, err)
}
func (c *Client) PollRecoveryRange() (*RecoveryRangeStatus, error) {
	value, err := c.RawCall("poll_recovery_range", nil)
	if err != nil || string(value) == "null" {
		return nil, err
	}
	return decodePointer[RecoveryRangeStatus](value)
}
func (c *Client) CancelRecoveryRange() error {
	value, err := c.RawCall("cancel_recovery_range", nil)
	if err != nil {
		return err
	}
	var raw struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(value, &raw); err != nil {
		return err
	}
	if raw.State != "recovery_range_cancelled" {
		return errors.New("invalid recovery cancellation response")
	}
	return nil
}
func (c *Client) StageRecoveryRange(retainUntil uint64) (RecoveryStage, error) {
	value, snapshot, err := c.CallStored("stage_recovery_range", map[string]any{"retain_until": retainUntil}, nil)
	if err != nil {
		return RecoveryStage{}, err
	}
	var raw struct {
		State            string `json:"state"`
		Workspace        ID     `json:"workspace"`
		PublicationCount int    `json:"publication_count"`
		AlreadyReceived  int    `json:"already_received"`
		Durable          bool   `json:"durable"`
	}
	if err := json.Unmarshal(value, &raw); err != nil {
		return RecoveryStage{}, err
	}
	result := RecoveryStage{State: raw.State}
	if raw.State == "awaiting_recovery_save" {
		if len(snapshot) == 0 {
			return RecoveryStage{}, errors.New("recovery candidate has no snapshot")
		}
		result.Candidate = &RecoveryCandidate{Workspace: raw.Workspace, Snapshot: snapshot, PublicationCount: raw.PublicationCount, AlreadyReceived: raw.AlreadyReceived, Durable: raw.Durable}
	} else if raw.State != "recovery_already_covered" && raw.State != "recovery_no_new_objects" {
		return RecoveryStage{}, errors.New("unknown recovery stage state: " + raw.State)
	}
	return result, nil
}
func (c *Client) AdoptRecovery(snapshot []byte) (RecoveryAdoption, error) {
	value, _, err := c.CallStored("adopt_recovery", nil, snapshot)
	if err != nil {
		return RecoveryAdoption{}, err
	}
	var raw struct {
		Workspace        ID     `json:"workspace"`
		Epoch            uint64 `json:"epoch"`
		Members          int    `json:"members"`
		Durable          bool   `json:"durable"`
		State            string `json:"state"`
		PublicationCount int    `json:"publication_count"`
		MissingCount     int    `json:"missing_count"`
	}
	if err := json.Unmarshal(value, &raw); err != nil {
		return RecoveryAdoption{}, err
	}
	recovered, missing := 0, 0
	if raw.State == "recovery_adopted" {
		recovered = raw.PublicationCount
	} else if raw.State == "direct_miss_adopted" {
		missing = raw.MissingCount
	} else {
		return RecoveryAdoption{}, errors.New("unknown recovery adoption state")
	}
	return RecoveryAdoption{Workspace: raw.Workspace, Epoch: raw.Epoch, MemberCount: raw.Members, Durable: raw.Durable, RecoveredPublications: recovered, MissingPublications: missing}, nil
}

func (c *Client) callOK(op string, params map[string]any) error {
	_, err := c.RawCall(op, params)
	return err
}

func workspaceInfo(value json.RawMessage, err error) (WorkspaceInfo, error) {
	if err != nil {
		return WorkspaceInfo{}, err
	}
	var raw struct {
		Workspace     ID       `json:"workspace"`
		WorkspaceName *string  `json:"workspace_name"`
		Epoch         uint64   `json:"epoch"`
		Members       int      `json:"members"`
		Durable       bool     `json:"durable"`
		Activity      Activity `json:"activity"`
	}
	if err := json.Unmarshal(value, &raw); err != nil {
		return WorkspaceInfo{}, err
	}
	return WorkspaceInfo{Workspace: raw.Workspace, WorkspaceName: raw.WorkspaceName, Epoch: raw.Epoch, MemberCount: raw.Members, Durable: raw.Durable, Phase: raw.Activity.Phase, Reason: raw.Activity.Reason}, nil
}
func workspaceCandidate(value json.RawMessage, snapshot []byte, err error) (WorkspaceCandidate, error) {
	if err != nil {
		return WorkspaceCandidate{}, err
	}
	var raw struct {
		Workspace ID `json:"workspace"`
	}
	if err := json.Unmarshal(value, &raw); err != nil {
		return WorkspaceCandidate{}, err
	}
	return WorkspaceCandidate{Workspace: raw.Workspace, Snapshot: snapshot}, nil
}
func decode[T any](value json.RawMessage, err error) (T, error) {
	var result T
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(value, &result)
	return result, err
}
func decodePointer[T any](value json.RawMessage) (*T, error) {
	var result T
	if err := json.Unmarshal(value, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
func (c *Client) nativeCall(call func() C.ArachneResult) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("Arachne client is closed")
	}
	result := call()
	value := takeBuffer(result.value)
	if result.status != 0 {
		return nil, errors.New(string(value))
	}
	return value, nil
}

func encodeRequest(op string, params map[string]any) ([]byte, error) {
	if op == "" {
		return nil, errors.New("operation name is required")
	}
	request := make(map[string]any, len(params)+1)
	request["op"] = op
	for key, value := range params {
		if key == "op" {
			return nil, errors.New("params must not contain op")
		}
		request[key] = normalizeJSON(value)
	}
	return json.Marshal(request)
}

func normalizeJSON(value any) any {
	switch value := value.(type) {
	case []byte:
		bytes := make([]any, len(value))
		for i, b := range value {
			bytes[i] = b
		}
		return bytes
	case []any:
		items := make([]any, len(value))
		for i, item := range value {
			items[i] = normalizeJSON(item)
		}
		return items
	case map[string]any:
		items := make(map[string]any, len(value))
		for key, item := range value {
			items[key] = normalizeJSON(item)
		}
		return items
	default:
		return value
	}
}

func bytePointer(value []byte) *C.uint8_t {
	if len(value) == 0 {
		return nil
	}
	return (*C.uint8_t)(unsafe.Pointer(&value[0]))
}

func takeBuffer(value C.ArachneBuffer) []byte {
	defer C.arachne_sdk_buffer_free(value.data, value.len)
	if value.len == 0 {
		return []byte{}
	}
	return C.GoBytes(unsafe.Pointer(value.data), C.int(value.len))
}
