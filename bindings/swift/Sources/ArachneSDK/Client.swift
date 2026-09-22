import CArachneSDK
import Foundation

public enum Network: UInt32, Sendable {
    case direct = 0
    case lan = 1
    case nearby = 2
    case wan = 3
    case relayOnly = 4
    case wanOnly = 5
}

public struct ArachneError: Error, CustomStringConvertible, Sendable {
    public let status: Int32
    public let message: String

    public var description: String { message }
}

public typealias ID = Data
public typealias RecordID = Data

public enum WorkspacePhase: String, Codable, Sendable {
    case empty, creating, joining, synchronizing, active, recovering, leaving, resetting, removed, failed
}

public struct Activity: Decodable, Sendable {
    public let phase: WorkspacePhase
    public let reason: String?

    enum CodingKeys: String, CodingKey { case phase = "state", reason }
}

public struct ClientConfig: Sendable {
    public var network: Network
    public var secret: Data

    public init(network: Network = .direct, secret: Data = Data()) {
        self.network = network
        self.secret = secret
    }
}

public struct EndpointInfo: Decodable, Sendable {
    public let endpointKey: ID
    public let boundAddress: String
    public let workspaceReady: Bool

    enum CodingKeys: String, CodingKey {
        case endpointKey = "endpoint_key", boundAddress = "bound_address", workspaceReady = "workspace_ready"
    }
}

public struct WorkspaceState: Sendable {
    public let endpointKey: ID
    public let workspace: ID?
    public let workspaceReady: Bool
    public let durable: Bool
    public let phase: WorkspacePhase
    public let reason: String?
}

public struct WorkspaceInfo: Sendable {
    public let workspace: ID
    public let workspaceName: String?
    public let epoch: UInt64
    public let memberCount: Int
    public let durable: Bool
    public let phase: WorkspacePhase
    public let reason: String?
}

public struct WorkspaceCandidate: Sendable {
    public let workspace: ID
    public let snapshot: Data
}

public struct RestoredMember: Decodable, Sendable {
    public let id: ID
    public let displayName: String?
    enum CodingKeys: String, CodingKey { case id, displayName = "display_name" }
}

public struct RestoreResult: Decodable, Sendable {
    public let workspace: ID
    public let workspaceName: String?
    public let endpoint: ID?
    public let epoch: UInt64?
    public let members: Int?
    public let durable: Bool
    public let workspaceReady: Bool?
    public let state: String?
    public let activity: Activity?
    public let member: RestoredMember?
    public let keyPackage: Data?
    public let admissionRequest: Data?
    public let personalInvitation: Bool?
    public let commitDigest: Data?

    enum CodingKeys: String, CodingKey {
        case workspace, endpoint, epoch, members, durable, state, activity, member
        case workspaceName = "workspace_name", workspaceReady = "workspace_ready"
        case keyPackage = "key_package", admissionRequest = "admission_request"
        case personalInvitation = "personal_invitation", commitDigest = "commit_digest"
    }
}

public struct AdmissionAuthorization: Codable, Sendable {
    public let invitationKey: ID
    public let grantSignature: Data
    public let redemptionSignature: Data
    enum CodingKeys: String, CodingKey {
        case invitationKey = "invitation_key", grantSignature = "grant_signature", redemptionSignature = "redemption_signature"
    }
    public init(invitationKey: ID, grantSignature: Data, redemptionSignature: Data) {
        self.invitationKey = invitationKey
        self.grantSignature = grantSignature
        self.redemptionSignature = redemptionSignature
    }
}

public struct JoinAdmissionStep: Codable, Sendable {
    public let commit: Data
    public let authorization: AdmissionAuthorization
    enum CodingKeys: String, CodingKey { case commit, authorization }
    public init(commit: Data, authorization: AdmissionAuthorization) {
        self.commit = commit
        self.authorization = authorization
    }
}

public struct JoinRequest: Decodable, Sendable {
    public let workspace: ID
    public let member: ID
    public let endpoint: ID
    public let admissionRequest: Data
    enum CodingKeys: String, CodingKey { case workspace, endpoint, member, admissionRequest = "admission_request" }
}

public struct AdmissionReply: Decodable, Sendable {
    public let workspace: ID
    public let epoch: UInt64
    public let commit: Data
    public let welcome: Data
    public let authorization: AdmissionAuthorization
}

public enum MemberKind: String, Decodable, Sendable { case person, service }
public enum Presence: String, Decodable, Sendable { case `self`, unknown, reachable, stale }

public struct MemberInfo: Decodable, Sendable {
    public let id: ID
    public let endpoint: ID
    public let administrator: Bool
    public let selfMember: Bool
    public let displayName: String?
    public let kind: MemberKind
    public let presence: Presence
    public let lastContactAgeMS: UInt64?
    public let presenceFreshForMS: UInt64?
    enum CodingKeys: String, CodingKey {
        case id, endpoint, administrator, displayName = "display_name", kind, presence
        case selfMember = "self", lastContactAgeMS = "last_contact_age_ms"
        case presenceFreshForMS = "presence_fresh_for_ms"
    }
}

public struct MemberRoster: Sendable {
    public let workspace: ID
    public let workspaceName: String?
    public let workspaceNameRevision: UInt64
    public let workspaceNameHead: ID
    public let epoch: UInt64
    public let members: [MemberInfo]
    public let profileCount: Int
    public let profilesRetained: Bool
}

public struct RouteHint: Decodable, Sendable {
    public let peer: ID
    public let address: String
}

public struct InvitationInfo: Decodable, Sendable {
    public let workspace: ID
    public let workspaceName: String?
    public let invitation: Data
    public let invitationKey: ID
    public let checkpoint: Data
    public let peer: ID
    public let bootstrapPeers: [ID]
    public let address: String
    public let routes: [RouteHint]
    enum CodingKeys: String, CodingKey {
        case workspace, invitation, peer, address, routes
        case workspaceName = "workspace_name", invitationKey = "invitation_key"
        case checkpoint, bootstrapPeers = "bootstrap_peers"
    }
}

public struct InvitationDetails: Sendable {
    public let workspace: ID
    public let invitationKey: ID
    public let workspaceName: String?
    public let epoch: UInt64
    public let personal: Bool
    public let automatic: Bool
    public let expiresAt: UInt64
}

public struct PeerPolicy: Codable, Sendable {
    public let peer: ID
    public let publish: [String]
    public let subscribe: [String]
    public init(peer: ID, publish: [String], subscribe: [String]) {
        self.peer = peer; self.publish = publish; self.subscribe = subscribe
    }
}

public struct PeerRoute: Decodable, Sendable {
    public let member: ID
    public let route: String
    public let rttMS: UInt64
    enum CodingKeys: String, CodingKey { case member, route, rttMS = "rtt_ms" }
}

public struct ConnectivityReport: Sendable {
    public let workspace: ID
    public let paths: [PeerRoute]
    public let pathsLimited: Bool
    public let receiveQueue: Int
    public let repairJobs: Int
}

public struct DurationSummary: Decodable, Sendable {
    public let count: UInt64
    public let totalUS: UInt64
    public let maxUS: UInt64
    enum CodingKeys: String, CodingKey { case count, totalUS = "total_us", maxUS = "max_us" }
}

public struct ControlTimingMetrics: Decodable, Sendable {
    public let inquiry: DurationSummary
    public let hostWait: DurationSummary
    public let hostService: DurationSummary
    enum CodingKeys: String, CodingKey { case inquiry, hostWait = "host_wait", hostService = "host_service" }
}

public struct MembershipGossipMetrics: Decodable, Sendable {
    public let sent, noOverlay, failed, received, staged, rejected, rangePulled, rangeFailed: UInt64
    enum CodingKeys: String, CodingKey {
        case sent, failed, received, staged, rejected
        case noOverlay = "no_overlay", rangePulled = "range_pulled", rangeFailed = "range_failed"
    }
}

public struct ConnectionCapacityMetrics: Decodable, Sendable {
    public let evicted: UInt64
    public let refused: UInt64
}

public struct WorkspaceMetrics: Sendable {
    public let workspace: ID
    public let phase: WorkspacePhase
    public let reason: String?
    public let receivedBytes, sentBytes: UInt64
    public let receiveQueue, admissionQueue, admissionQueueBytes, admissionWaiters: Int
    public let admissionInFlight, approvalPending, pendingObjects, repairJobs, gossipNeighbors: Int
    public let controlTiming: ControlTimingMetrics
    public let membershipGossip: MembershipGossipMetrics
    public let connectionCapacity: ConnectionCapacityMetrics
    public let paths: [PeerRoute]
    public let pathsLimited: Bool
}

public struct DeliveryFailure: Decodable, Sendable {
    public let peer: ID
    public let error: String
}

public struct DeliveryReport: Decodable, Sendable {
    public let admitted: [ID]
    public let queued: Bool
    public let failed: [DeliveryFailure]
}

public struct PublicationCurrent: Codable, Sendable {
    public let selector: ID
    public let replacementKey: ID
    public let expiresAt: UInt64
    public let tombstone: Bool
    enum CodingKeys: String, CodingKey {
        case selector, tombstone, replacementKey = "replacement_key", expiresAt = "expires_at"
    }
    public init(selector: ID, replacementKey: ID, expiresAt: UInt64, tombstone: Bool = false) {
        self.selector = selector; self.replacementKey = replacementKey; self.expiresAt = expiresAt; self.tombstone = tombstone
    }
}

public struct PublicationCandidate: Sendable {
    public let workspace: ID
    public let snapshot: Data
}

public struct ProtectedReceptionCandidate: Sendable {
    public let workspace: ID
    public let snapshot: Data
}

public struct ReceivedProtectedPublication: Decodable, Sendable {
    public let workspace: ID
    public let revision: UInt64
    public let member: ID
    public let endpoint: ID
    public let topic: String
    public let id: RecordID
    public let sequence: UInt64?
    public let payload: Data
    public let recipients: [ID]

    private enum CodingKeys: String, CodingKey {
        case workspace, revision, member, endpoint, topic, id, sequence, payload, recipients
    }

    public init(from decoder: Decoder) throws {
        let values = try decoder.container(keyedBy: CodingKeys.self)
        workspace = try values.decode(ID.self, forKey: .workspace)
        revision = try values.decode(UInt64.self, forKey: .revision)
        member = try values.decode(ID.self, forKey: .member)
        endpoint = try values.decode(ID.self, forKey: .endpoint)
        topic = try values.decode(String.self, forKey: .topic)
        id = try values.decode(RecordID.self, forKey: .id)
        sequence = try values.decodeIfPresent(UInt64.self, forKey: .sequence)
        payload = try values.decode(Data.self, forKey: .payload)
        recipients = try values.decodeIfPresent([ID].self, forKey: .recipients) ?? []
    }
}

public struct InterestObservation: Decodable, Sendable {
    public let workspace: ID
    public let revision: UInt64
    public let topic: String
    public let subscribed: Bool
    public let admission: DeliveryReport
}

public struct Publication: Decodable, Sendable {
    public let workspace: ID
    public let revision: UInt64
    public let sender: ID
    public let topic: String
    public let payload: Data
}

public struct RecoveredPublication: Decodable, Sendable {
    public let workspace: ID
    public let revision: UInt64
    public let member: ID
    public let endpoint: ID
    public let topic: String
    public let id: RecordID
    public let sequence: UInt64?
    public let payload: Data
}

public struct RecoveryRangeRequest: Codable, Sendable {
    public let peer: ID?
    public let author: ID?
    public let revision: UInt64
    public let topics: [String]
    public let after: UInt64?
    public let through: UInt64?
    public init(peer: ID? = nil, author: ID? = nil, revision: UInt64, topics: [String], after: UInt64? = nil, through: UInt64? = nil) {
        self.peer = peer; self.author = author; self.revision = revision; self.topics = topics; self.after = after; self.through = through
    }
}

public struct RecoveryRangeReady: Decodable, Sendable {
    public let workspace, author, peer: ID
    public let epoch, revision, after, through: UInt64
    public let packetCount, retainedBytes: Int
    public let automaticSource: Bool
    public let attempted: Int?
    enum CodingKeys: String, CodingKey {
        case workspace, author, peer, epoch, revision, after, through, attempted
        case packetCount = "packet_count", retainedBytes = "retained_bytes", automaticSource = "automatic_source"
    }
}

public enum RecoveryRangeStatus: Sendable {
    case pending(candidateCount: Int, automaticSource: Bool)
    case ready(RecoveryRangeReady)
    case sourceWaiting(automaticSource: Bool)
    case sourceUnavailable(attempted: Int, reason: String, automaticSource: Bool)
    case rejected(reason: String)
    case cancelled
}

public enum RecoveryStage: Sendable {
    case candidate(RecoveryCandidate)
    case alreadyCovered
    case noNewObjects
}

public struct RecoveryCandidate: Sendable {
    public let workspace: ID
    public let snapshot: Data
    public let publicationCount: Int
    public let alreadyReceived: Int
    public let durable: Bool
}

public struct RecoveryAdoption: Sendable {
    public let workspace: ID
    public let epoch: UInt64
    public let memberCount: Int
    public let durable: Bool
    public let recoveredPublications: Int
    public let missingPublications: Int
}

/// One blocking endpoint session. Calls on an instance are serialized.
public final class Client: @unchecked Sendable {
    private let handle: Int64
    private let lock = NSLock()
    private var closed = false

    private init(handle: Int64) {
        self.handle = handle
    }

    public static func open(
        network: Network = .direct,
        secret: Data = Data()
    ) throws -> Client {
        let result = secret.withUnsafeBytes { bytes in
            arachne_sdk_open(
                network.rawValue,
                bytes.bindMemory(to: UInt8.self).baseAddress,
                secret.count
            )
        }
        let value = take(result.value)
        guard result.status == 0 else {
            throw ArachneError(status: result.status, message: String(decoding: value, as: UTF8.self))
        }
        guard let text = String(data: value, encoding: .ascii), let handle = Int64(text) else {
            throw ArachneError(status: 2, message: "native SDK returned an invalid client handle")
        }
        return Client(handle: handle)
    }

    public static func open(config: ClientConfig) throws -> Client {
        try open(network: config.network, secret: config.secret)
    }

    /// Invoke an advanced core JSON operation. Prefer the typed Client methods.
    public func rawCall(_ op: String, params: [String: Any] = [:]) throws -> Any {
        try withOpen {
            let request = try Self.request(op, params: params)
            let result = request.withUnsafeBytes { bytes in
                arachne_sdk_execute(
                    handle,
                    bytes.bindMemory(to: UInt8.self).baseAddress,
                    request.count
                )
            }
            let value = take(result.value)
            guard result.status == 0 else {
                throw ArachneError(status: result.status, message: String(decoding: value, as: UTF8.self))
            }
            return try JSONSerialization.jsonObject(with: value, options: [.fragmentsAllowed])
        }
    }

    /// Compatibility alias for `rawCall`.
    public func call(_ op: String, params: [String: Any] = [:]) throws -> Any {
        try rawCall(op, params: params)
    }

    /// Invoke a staged operation with opaque snapshot bytes kept out of JSON.
    public func rawCallStored(
        _ op: String,
        params: [String: Any] = [:],
        snapshot: Data = Data()
    ) throws -> (value: Any, snapshot: Data) {
        try withOpen {
            let request = try Self.request(op, params: params)
            let result = request.withUnsafeBytes { requestBytes in
                snapshot.withUnsafeBytes { snapshotBytes in
                    arachne_sdk_execute_stored(
                        handle,
                        requestBytes.bindMemory(to: UInt8.self).baseAddress,
                        request.count,
                        snapshotBytes.bindMemory(to: UInt8.self).baseAddress,
                        snapshot.count
                    )
                }
            }
            let value = take(result.value)
            let stagedSnapshot = take(result.snapshot)
            guard result.status == 0 else {
                throw ArachneError(status: result.status, message: String(decoding: value, as: UTF8.self))
            }
            return (
                try JSONSerialization.jsonObject(with: value, options: [.fragmentsAllowed]),
                stagedSnapshot
            )
        }
    }

    /// Compatibility alias for `rawCallStored`.
    public func callStored(
        _ op: String,
        params: [String: Any] = [:],
        snapshot: Data = Data()
    ) throws -> (value: Any, snapshot: Data) {
        try rawCallStored(op, params: params, snapshot: snapshot)
    }

    public func endpoint() throws -> EndpointInfo {
        try withOpen { try Self.decode(EndpointInfo.self, from: Self.checked(arachne_sdk_describe(handle))) }
    }

    public func workspaceState() throws -> WorkspaceState {
        let raw = try Self.decode(RawWorkspaceState.self, from: rawCall("workspace_state"))
        let endpoint = try self.endpoint()
        return WorkspaceState(endpointKey: endpoint.endpointKey, workspace: raw.workspace,
                              workspaceReady: raw.workspaceReady, durable: raw.durable,
                              phase: raw.activity.phase, reason: raw.activity.reason)
    }

    public func createWorkspace(displayName: String, workspaceName: String? = nil) throws -> WorkspaceInfo {
        let raw = try rawCall("create_workspace", params: [
            "display_name": displayName, "workspace_name": workspaceName as Any? ?? NSNull()
        ])
        return try Self.workspaceInfo(raw)
    }

    public func beginJoin(invitation: Data, checkpoint: Data, displayName: String, peers: [ID] = []) throws -> JoinRequest {
        let raw = try Self.decode(RawJoinRequest.self, from: rawCall("begin_join", params: [
            "invitation": invitation, "checkpoint": checkpoint, "display_name": displayName, "peers": peers
        ]))
        guard let admission = raw.admissionRequest else {
            throw ArachneError(status: 1, message: "invitation has no admission request")
        }
        return JoinRequest(workspace: try Self.checkedID(raw.workspace), member: try Self.checkedID(raw.member.id),
                           endpoint: try Self.checkedID(raw.endpoint), admissionRequest: admission)
    }

    public func driveJoin() throws -> Any { try rawCall("drive_join") }

    public func stageAdmission(authenticatedEndpoint: ID, request: Data) throws -> WorkspaceCandidate {
        let (raw, snapshot) = try rawCallStored("stage_admission", params: [
            "authenticated_endpoint": try Self.checkedID(authenticatedEndpoint), "request": request
        ])
        let candidate = try Self.decode(RawWorkspaceCandidate.self, from: raw)
        return WorkspaceCandidate(workspace: try Self.checkedID(candidate.workspace), snapshot: snapshot)
    }

    public func adoptAdmission(snapshot: Data) throws -> WorkspaceInfo {
        let (raw, _) = try rawCallStored("adopt_admission", snapshot: snapshot)
        return try Self.workspaceInfo(raw)
    }

    public func retainedAdmission(authenticatedEndpoint: ID, request: Data) throws -> AdmissionReply {
        let raw = try rawCall("retained_admission", params: [
            "authenticated_endpoint": try Self.checkedID(authenticatedEndpoint), "request": request
        ])
        return try Self.decode(AdmissionReply.self, from: raw)
    }

    public func stageJoin(welcome: Data, commits: [JoinAdmissionStep]) throws -> WorkspaceCandidate {
        let (raw, snapshot) = try rawCallStored("stage_join", params: ["commits": try Self.jsonObject(commits)], snapshot: welcome)
        let candidate = try Self.decode(RawWorkspaceCandidate.self, from: raw)
        return WorkspaceCandidate(workspace: try Self.checkedID(candidate.workspace), snapshot: snapshot)
    }

    public func adoptJoin(snapshot: Data) throws -> WorkspaceInfo {
        let (raw, _) = try rawCallStored("adopt_join", snapshot: snapshot)
        return try Self.workspaceInfo(raw)
    }

    public func enableRecordStorage(path: URL, root: ID) throws {
        let pathData = Data(path.path.utf8)
        let root = try Self.checkedID(root)
        try withOpen {
            let result = pathData.withUnsafeBytes { pathBytes in
                root.withUnsafeBytes { rootBytes in
                    arachne_sdk_enable_record_storage(handle,
                        pathBytes.bindMemory(to: UInt8.self).baseAddress, pathData.count,
                        rootBytes.bindMemory(to: UInt8.self).baseAddress, root.count)
                }
            }
            _ = try Self.checked(result)
        }
    }

    public func restoreRecordStorage(path: URL, root: ID, workspace: ID) throws -> RestoreResult {
        let pathData = Data(path.path.utf8)
        let root = try Self.checkedID(root)
        let workspace = try Self.checkedID(workspace)
        return try withOpen {
            let bytes = try pathData.withUnsafeBytes { pathBytes in
                try root.withUnsafeBytes { rootBytes in
                    try workspace.withUnsafeBytes { workspaceBytes in
                        try Self.checked(arachne_sdk_restore_record_storage(handle,
                            pathBytes.bindMemory(to: UInt8.self).baseAddress, pathData.count,
                            rootBytes.bindMemory(to: UInt8.self).baseAddress, root.count,
                            workspaceBytes.bindMemory(to: UInt8.self).baseAddress, workspace.count))
                    }
                }
            }
            return try Self.decode(RestoreResult.self, from: bytes)
        }
    }

    public func saveCandidate(_ snapshot: Data) throws {
        try withOpen {
            _ = try snapshot.withUnsafeBytes { bytes in
                try Self.checked(arachne_sdk_save_candidate(handle,
                    bytes.bindMemory(to: UInt8.self).baseAddress, snapshot.count))
            }
        }
    }

    public func memberRoster() throws -> MemberRoster {
        let raw = try Self.decode(RawMemberRoster.self, from: rawCall("member_roster"))
        return MemberRoster(workspace: try Self.checkedID(raw.workspace), workspaceName: raw.workspaceName,
                            workspaceNameRevision: raw.workspaceNameRevision,
                            workspaceNameHead: try Self.checkedID(raw.workspaceNameHead), epoch: raw.epoch,
                            members: raw.members, profileCount: raw.profiles.count,
                            profilesRetained: raw.profilesRetained ?? true)
    }

    public func useServiceProfile() throws { _ = try rawCall("use_service_profile") }

    public func issueInvitation() throws -> InvitationInfo {
        try Self.decode(InvitationInfo.self, from: rawCall("issue_invitation"))
    }

    public func inspectInvitation(invitation: Data, checkpoint: Data) throws -> InvitationDetails {
        let raw = try Self.decode(RawInvitationDetails.self, from: rawCall("inspect_invitation", params: [
            "invitation": invitation, "checkpoint": checkpoint
        ]))
        return InvitationDetails(workspace: try Self.checkedID(raw.workspace),
                                 invitationKey: try Self.checkedID(raw.invitationKey),
                                 workspaceName: raw.workspaceName, epoch: raw.epoch,
                                 personal: raw.personalInvitation, automatic: raw.automaticApproval,
                                 expiresAt: raw.expiresAt)
    }

    public func metrics() throws -> WorkspaceMetrics {
        let raw = try Self.decode(RawWorkspaceMetrics.self, from: rawCall("workspace_metrics"))
        return WorkspaceMetrics(workspace: try Self.checkedID(raw.workspace), phase: raw.activity.phase,
                                reason: raw.activity.reason, receivedBytes: raw.receivedBytes, sentBytes: raw.sentBytes,
                                receiveQueue: raw.receiveQueue, admissionQueue: raw.admissionQueue,
                                admissionQueueBytes: raw.admissionQueueBytes, admissionWaiters: raw.admissionWaiters,
                                admissionInFlight: raw.admissionInFlight, approvalPending: raw.approvalPending,
                                pendingObjects: raw.pendingObjects, repairJobs: raw.repairJobs,
                                gossipNeighbors: raw.gossipNeighbors, controlTiming: raw.controlTiming,
                                membershipGossip: raw.membershipGossip, connectionCapacity: raw.connectionCapacity,
                                paths: raw.paths, pathsLimited: raw.pathsLimited)
    }

    public func connectivity() throws -> ConnectivityReport {
        let value = try metrics()
        return ConnectivityReport(workspace: value.workspace, paths: value.paths,
                                  pathsLimited: value.pathsLimited, receiveQueue: value.receiveQueue,
                                  repairJobs: value.repairJobs)
    }

    public func networkChange() throws { _ = try rawCall("network_change") }
    public func cancel() throws { try withOpen { _ = try Self.checked(arachne_sdk_cancel(handle)) } }
    public func waitForWork() throws -> Bool {
        try withOpen {
            let bytes = try Self.checked(arachne_sdk_wait_for_work(handle))
            guard bytes == Data("0".utf8) || bytes == Data("1".utf8) else {
                throw ArachneError(status: 2, message: "native SDK returned an invalid wait result")
            }
            return bytes == Data("1".utf8)
        }
    }
    public func pollControl() throws -> Bool { try rawCall("poll_admission") is NSNull == false }
    public func addAddressHint(peer: ID, address: String) throws {
        _ = try rawCall("add_address_hint", params: ["peer": try Self.checkedID(peer), "address": address])
    }
    public func installPolicy(workspace: ID, revision: UInt64, endpoints: [PeerPolicy]) throws {
        _ = try rawCall("install_verified_policy", params: [
            "workspace": try Self.checkedID(workspace), "revision": revision, "endpoints": try Self.jsonObject(endpoints)
        ])
    }
    public func installWorkspacePolicy(revision: UInt64) throws {
        _ = try rawCall("install_workspace_policy", params: ["revision": revision])
    }
    public func enableObjectDelivery() throws {
        let durable = try workspaceState().durable
        let (value, snapshot) = try rawCallStored("enable_object_delivery")
        if snapshot.isEmpty {
            if (value as? [String: Any])?["state"] as? String == "object_delivery_enabled" { return }
            throw ArachneError(status: 2, message: "object delivery returned no adoptable snapshot")
        }
        if durable { try saveCandidate(snapshot) }
        _ = try rawCallStored("adopt_reception", snapshot: snapshot)
    }

    public func stageProtectedPublication(workspace: ID, revision: UInt64, topic: String,
                                           id: RecordID, payload: Data,
                                           current: PublicationCurrent? = nil) throws -> PublicationCandidate {
        let params: [String: Any] = [
            "revision": revision, "topic": topic, "id": try Self.checkedRecordID(id),
            "payload": payload, "current": try current.map { try Self.jsonObject($0) } ?? NSNull()
        ]
        let (raw, snapshot) = try rawCallStored("stage_network_publication", params: params)
        let candidate = try Self.decode(RawWorkspaceCandidate.self, from: raw)
        let candidateWorkspace = try Self.checkedID(candidate.workspace)
        guard candidateWorkspace == (try Self.checkedID(workspace)) else {
            throw ArachneError(status: 2, message: "publication candidate workspace mismatch")
        }
        return PublicationCandidate(workspace: candidateWorkspace, snapshot: snapshot)
    }

    public func adoptProtectedPublication(snapshot: Data) throws -> DeliveryReport {
        let (value, _) = try rawCallStored("adopt_publication", snapshot: snapshot)
        let raw = try Self.decode(RawAdoptPublication.self, from: value)
        if let message = raw.networkError { throw ArachneError(status: 1, message: message) }
        return raw.admission
    }

    public func pollProtected() throws -> ProtectedReceptionCandidate? {
        let (value, snapshot) = try rawCallStored("poll_protected")
        if value is NSNull {
            guard snapshot.isEmpty else { throw ArachneError(status: 2, message: "empty protected reception returned a snapshot") }
            return nil
        }
        let raw = try Self.decode(RawProtectedCandidate.self, from: value)
        guard raw.state == "awaiting_reception_save", !snapshot.isEmpty else {
            throw ArachneError(status: 2, message: "protected reception has no adoptable snapshot")
        }
        return ProtectedReceptionCandidate(workspace: try Self.checkedID(raw.workspace), snapshot: snapshot)
    }

    public func adoptProtectedReception(snapshot: Data) throws -> ReceivedProtectedPublication {
        let (raw, _) = try rawCallStored("adopt_reception", snapshot: snapshot)
        return try Self.decode(ReceivedProtectedPublication.self, from: raw)
    }

    public func setInterest(workspace: ID, revision: UInt64, topic: String, subscribed: Bool) throws {
        _ = try rawCall("set_interest", params: ["workspace": try Self.checkedID(workspace),
            "revision": revision, "topic": topic, "subscribed": subscribed])
    }

    public func pollInterest() throws -> InterestObservation? {
        let value = try rawCall("poll_interest")
        guard !(value is NSNull) else { return nil }
        let raw = try Self.decode(RawInterest.self, from: value)
        if raw.state == "interest_pending" { return nil }
        if raw.state == "interest_failed" { throw ArachneError(status: 1, message: raw.error ?? "interest update failed") }
        return try Self.decode(InterestObservation.self, from: value)
    }

    public func publish(workspace: ID, revision: UInt64, topic: String, payload: Data) throws -> DeliveryReport {
        let value = try rawCall("publish", params: ["workspace": try Self.checkedID(workspace),
            "revision": revision, "topic": topic, "payload": payload])
        return try Self.decode(DeliveryReport.self, from: value)
    }

    public func poll() throws -> Publication? {
        let value = try rawCall("poll")
        return value is NSNull ? nil : try Self.decode(Publication.self, from: value)
    }

    public func pollRecoveredPublication() throws -> RecoveredPublication? {
        let value = try rawCall("poll_recovered_publication")
        return value is NSNull ? nil : try Self.decode(RecoveredPublication.self, from: value)
    }

    public func fetchRecoveryRange(_ request: RecoveryRangeRequest) throws -> RecoveryRangeStatus {
        guard let object = try Self.jsonObject(request) as? [String: Any] else {
            throw ArachneError(status: 2, message: "invalid recovery request")
        }
        return try Self.recoveryRange(rawCall("fetch_recovery_range", params: object))
    }

    public func pollRecoveryRange() throws -> RecoveryRangeStatus? {
        let value = try rawCall("poll_recovery_range")
        return value is NSNull ? nil : try Self.recoveryRange(value)
    }

    public func cancelRecoveryRange() throws {
        guard try Self.decode(RawState.self, from: rawCall("cancel_recovery_range")).state == "recovery_range_cancelled" else {
            throw ArachneError(status: 2, message: "invalid recovery cancellation response")
        }
    }

    public func stageRecoveryRange(retainUntil: UInt64) throws -> RecoveryStage {
        let (value, snapshot) = try rawCallStored("stage_recovery_range", params: ["retain_until": retainUntil])
        let raw = try Self.decode(RawRecoveryStage.self, from: value)
        switch raw.state {
        case "awaiting_recovery_save":
            guard let workspace = raw.workspace else {
                throw ArachneError(status: 2, message: "recovery candidate has no workspace")
            }
            return .candidate(RecoveryCandidate(workspace: try Self.checkedID(workspace), snapshot: snapshot,
                publicationCount: raw.publicationCount ?? 0, alreadyReceived: raw.alreadyReceived ?? 0,
                durable: raw.durable ?? false))
        case "recovery_already_covered": return .alreadyCovered
        case "recovery_no_new_objects": return .noNewObjects
        default: throw ArachneError(status: 2, message: "unknown recovery stage: \(raw.state)")
        }
    }

    public func adoptRecovery(snapshot: Data) throws -> RecoveryAdoption {
        let (value, _) = try rawCallStored("adopt_recovery", snapshot: snapshot)
        let raw = try Self.decode(RawRecoveryAdoption.self, from: value)
        let recovered: Int
        let missing: Int
        switch raw.state {
        case "recovery_adopted": recovered = raw.publicationCount ?? 0; missing = 0
        case "direct_miss_adopted": recovered = 0; missing = raw.missingCount ?? 0
        default: throw ArachneError(status: 2, message: "unknown recovery adoption state: \(raw.state)")
        }
        return RecoveryAdoption(workspace: try Self.checkedID(raw.workspace), epoch: raw.epoch,
                                memberCount: raw.members, durable: raw.durable,
                                recoveredPublications: recovered, missingPublications: missing)
    }

    public func close() throws {
        lock.lock()
        defer { lock.unlock() }
        guard !closed else { return }
        let result = arachne_sdk_close(handle)
        let value = take(result.value)
        guard result.status == 0 else {
            throw ArachneError(status: result.status, message: String(decoding: value, as: UTF8.self))
        }
        closed = true
    }

    deinit {
        try? close()
    }

    private func withOpen<T>(_ operation: () throws -> T) throws -> T {
        lock.lock()
        defer { lock.unlock() }
        guard !closed else {
            throw ArachneError(status: 1, message: "Arachne client is closed")
        }
        return try operation()
    }

    private static func request(_ op: String, params: [String: Any]) throws -> Data {
        guard !op.isEmpty else {
            throw ArachneError(status: 1, message: "operation name is required")
        }
        guard params["op"] == nil else {
            throw ArachneError(status: 1, message: "params must not contain op")
        }
        var value = [String: Any](minimumCapacity: params.count + 1)
        value["op"] = op
        for (key, item) in params {
            value[key] = normalize(item)
        }
        return try JSONSerialization.data(withJSONObject: value, options: [.sortedKeys])
    }

    private static func normalize(_ value: Any) -> Any {
        if let data = value as? Data {
            return Array(data)
        }
        if let values = value as? [String: Any] {
            return values.mapValues(normalize)
        }
        if let values = value as? [Any] {
            return values.map(normalize)
        }
        return value
    }

    private static func checked(_ result: ArachneResult) throws -> Data {
        let value = take(result.value)
        guard result.status == 0 else {
            throw ArachneError(status: result.status, message: String(decoding: value, as: UTF8.self))
        }
        return value
    }

    private static func decode<T: Decodable>(_ type: T.Type, from data: Data) throws -> T {
        let decoder = JSONDecoder()
        decoder.dataDecodingStrategy = .custom { decoder in
            var bytes = try decoder.unkeyedContainer()
            var value = Data()
            while !bytes.isAtEnd { value.append(try bytes.decode(UInt8.self)) }
            return value
        }
        return try decoder.decode(type, from: data)
    }

    private static func decode<T: Decodable>(_ type: T.Type, from object: Any) throws -> T {
        let data = try JSONSerialization.data(withJSONObject: object, options: [.fragmentsAllowed])
        return try decode(type, from: data)
    }

    private static func jsonObject<T: Encodable>(_ value: T) throws -> Any {
        let encoder = JSONEncoder()
        encoder.dataEncodingStrategy = .custom { data, encoder in
            var bytes = encoder.unkeyedContainer()
            for byte in data { try bytes.encode(byte) }
        }
        let data = try encoder.encode(value)
        return try JSONSerialization.jsonObject(with: data, options: [.fragmentsAllowed])
    }

    private static func checkedID(_ value: ID) throws -> ID {
        guard value.count == 32 else { throw ArachneError(status: 1, message: "ID must be exactly 32 bytes") }
        return value
    }

    private static func checkedRecordID(_ value: RecordID) throws -> RecordID {
        guard value.count == 16 else { throw ArachneError(status: 1, message: "record ID must be exactly 16 bytes") }
        return value
    }

    private static func workspaceInfo(_ object: Any) throws -> WorkspaceInfo {
        let raw = try decode(RawWorkspaceInfo.self, from: object)
        return WorkspaceInfo(workspace: try checkedID(raw.workspace), workspaceName: raw.workspaceName,
                             epoch: raw.epoch, memberCount: raw.members, durable: raw.durable,
                             phase: raw.activity.phase, reason: raw.activity.reason)
    }

    private static func recoveryRange(_ object: Any) throws -> RecoveryRangeStatus {
        let raw = try decode(RawRecoveryRangeStatus.self, from: object)
        switch raw.state {
        case "recovery_range_pending":
            return .pending(candidateCount: raw.candidateCount ?? 0, automaticSource: raw.automaticSource ?? false)
        case "recovery_range_ready":
            return .ready(try decode(RecoveryRangeReady.self, from: object))
        case "recovery_source_waiting":
            return .sourceWaiting(automaticSource: raw.automaticSource ?? true)
        case "recovery_source_unavailable":
            return .sourceUnavailable(attempted: raw.attempted ?? 0, reason: raw.reason ?? "source unavailable",
                                      automaticSource: raw.automaticSource ?? false)
        case "recovery_range_rejected":
            return .rejected(reason: raw.reason ?? "recovery range rejected")
        case "recovery_range_cancelled": return .cancelled
        default: throw ArachneError(status: 2, message: "unknown recovery range state: \(raw.state)")
        }
    }
}

private struct RawWorkspaceState: Decodable {
    let workspace: ID?
    let workspaceReady: Bool
    let durable: Bool
    let activity: Activity
    enum CodingKeys: String, CodingKey { case workspace, durable, activity, workspaceReady = "workspace_ready" }
}

private struct RawWorkspaceInfo: Decodable {
    let workspace: ID
    let workspaceName: String?
    let epoch: UInt64
    let members: Int
    let durable: Bool
    let activity: Activity
    enum CodingKeys: String, CodingKey {
        case workspace, epoch, members, durable, activity, workspaceName = "workspace_name"
    }
}

private struct RawJoinRequest: Decodable {
    struct Member: Decodable { let id: ID }
    let workspace: ID
    let endpoint: ID
    let member: Member
    let admissionRequest: Data?
    enum CodingKeys: String, CodingKey { case workspace, endpoint, member, admissionRequest = "admission_request" }
}

private struct RawWorkspaceCandidate: Decodable { let workspace: ID }

private struct RawMemberRoster: Decodable {
    let workspace: ID
    let workspaceName: String?
    let workspaceNameRevision: UInt64
    let workspaceNameHead: ID
    let epoch: UInt64
    let members: [MemberInfo]
    let profiles: [Data]
    let profilesRetained: Bool?
    enum CodingKeys: String, CodingKey {
        case workspace, epoch, members, profiles
        case workspaceName = "workspace_name", workspaceNameRevision = "workspace_name_revision"
        case workspaceNameHead = "workspace_name_head", profilesRetained = "profiles_retained"
    }
}

private struct RawInvitationDetails: Decodable {
    let workspace: ID
    let invitationKey: ID
    let workspaceName: String?
    let epoch: UInt64
    let personalInvitation: Bool
    let automaticApproval: Bool
    let expiresAt: UInt64
    enum CodingKeys: String, CodingKey {
        case workspace, epoch, invitationKey = "invitation_key", workspaceName = "workspace_name"
        case personalInvitation = "personal_invitation", automaticApproval = "automatic_approval", expiresAt = "expires_at"
    }
}

private struct RawWorkspaceMetrics: Decodable {
    let workspace: ID
    let activity: Activity
    let receivedBytes, sentBytes: UInt64
    let receiveQueue, admissionQueue, admissionQueueBytes, admissionWaiters: Int
    let admissionInFlight, approvalPending, pendingObjects, repairJobs, gossipNeighbors: Int
    let controlTiming: ControlTimingMetrics
    let membershipGossip: MembershipGossipMetrics
    let connectionCapacity: ConnectionCapacityMetrics
    let paths: [PeerRoute]
    let pathsLimited: Bool
    enum CodingKeys: String, CodingKey {
        case workspace, activity, paths
        case receivedBytes = "received_bytes", sentBytes = "sent_bytes"
        case receiveQueue = "receive_queue", admissionQueue = "admission_queue"
        case admissionQueueBytes = "admission_queue_bytes", admissionWaiters = "admission_waiters"
        case admissionInFlight = "admission_in_flight", approvalPending = "approval_pending"
        case pendingObjects = "pending_objects", repairJobs = "repair_jobs"
        case gossipNeighbors = "gossip_neighbors", controlTiming = "control_timing"
        case membershipGossip = "membership_gossip", connectionCapacity = "connection_capacity"
        case pathsLimited = "paths_limited"
    }
}

private struct RawAdoptPublication: Decodable {
    let admission: DeliveryReport
    let networkError: String?
    enum CodingKeys: String, CodingKey { case admission, networkError = "network_error" }
}
private struct RawProtectedCandidate: Decodable { let workspace: ID; let state: String }
private struct RawInterest: Decodable { let state: String?; let error: String? }
private struct RawState: Decodable { let state: String }
private struct RawRecoveryRangeStatus: Decodable {
    let state: String
    let candidateCount: Int?
    let automaticSource: Bool?
    let attempted: Int?
    let reason: String?
    enum CodingKeys: String, CodingKey {
        case state, reason, attempted
        case candidateCount = "candidate_count", automaticSource = "automatic_source"
    }
}
private struct RawRecoveryStage: Decodable {
    let state: String
    let workspace: ID?
    let publicationCount: Int?
    let alreadyReceived: Int?
    let durable: Bool?
    enum CodingKeys: String, CodingKey {
        case state, workspace, durable
        case publicationCount = "publication_count", alreadyReceived = "already_received"
    }
}
private struct RawRecoveryAdoption: Decodable {
    let workspace: ID
    let epoch: UInt64
    let members: Int
    let durable: Bool
    let state: String
    let publicationCount: Int?
    let missingCount: Int?
    enum CodingKeys: String, CodingKey {
        case workspace, epoch, members, durable, state
        case publicationCount = "publication_count", missingCount = "missing_count"
    }
}

private func take(_ buffer: ArachneBuffer) -> Data {
    defer { arachne_sdk_buffer_free(buffer.data, buffer.len) }
    guard let pointer = buffer.data, buffer.len > 0 else { return Data() }
    return Data(bytes: pointer, count: Int(buffer.len))
}
