package org.arachne.sdk

import com.fasterxml.jackson.annotation.JsonProperty

enum class WorkspacePhase {
    @JsonProperty("empty") EMPTY,
    @JsonProperty("creating") CREATING,
    @JsonProperty("joining") JOINING,
    @JsonProperty("synchronizing") SYNCHRONIZING,
    @JsonProperty("active") ACTIVE,
    @JsonProperty("recovering") RECOVERING,
    @JsonProperty("leaving") LEAVING,
    @JsonProperty("resetting") RESETTING,
    @JsonProperty("removed") REMOVED,
    @JsonProperty("failed") FAILED
}

enum class MemberKind {
    @JsonProperty("person") PERSON,
    @JsonProperty("service") SERVICE
}
enum class Presence {
    @JsonProperty("self") SELF,
    @JsonProperty("unknown") UNKNOWN,
    @JsonProperty("reachable") REACHABLE,
    @JsonProperty("stale") STALE
}

data class RestoredMember(val id: ID, val displayName: String?)
data class WorkspaceCandidate(val workspace: ID, val snapshot: ByteArray)
data class RestoreResult(
    val workspace: ID,
    val workspaceName: String? = null,
    val endpoint: ID? = null,
    val epoch: ULong? = null,
    val members: Int? = null,
    val durable: Boolean = false,
    val workspaceReady: Boolean? = null,
    val state: String? = null,
    val activity: Activity? = null,
    val member: RestoredMember? = null,
    val keyPackage: ByteArray? = null,
    val admissionRequest: ByteArray? = null,
    val personalInvitation: Boolean? = null,
    val commitDigest: ByteArray? = null
)

data class AdmissionAuthorization(val invitationKey: ID, val grantSignature: ByteArray,
                                  val redemptionSignature: ByteArray)
data class JoinAdmissionStep(val commit: ByteArray, val authorization: AdmissionAuthorization)
data class JoinRequest(val workspace: ID, val member: ID, val endpoint: ID, val admissionRequest: ByteArray)
data class AdmissionReply(val workspace: ID, val epoch: ULong, val commit: ByteArray,
                          val welcome: ByteArray, val authorization: AdmissionAuthorization)
data class MemberInfo(
    val id: ID,
    val endpoint: ID,
    val administrator: Boolean,
    @param:JsonProperty("self") val selfMember: Boolean,
    val displayName: String?,
    val kind: MemberKind,
    val presence: Presence,
    val lastContactAgeMs: ULong?,
    val presenceFreshForMs: ULong?
)
data class MemberRoster(val workspace: ID, val workspaceName: String?, val workspaceNameRevision: ULong,
                        val workspaceNameHead: ID, val epoch: ULong, val members: List<MemberInfo>,
                        val profileCount: Int, val profilesRetained: Boolean)
data class RouteHint(val peer: ID, val address: String)
data class InvitationInfo(val workspace: ID, val workspaceName: String?, val invitation: ByteArray,
                          val invitationKey: ID, val checkpoint: ByteArray, val peer: ID,
                          val bootstrapPeers: List<ID>, val address: String, val routes: List<RouteHint>)
data class InvitationDetails(val workspace: ID, val invitationKey: ID, val workspaceName: String?,
                             val epoch: ULong, val personal: Boolean, val automatic: Boolean, val expiresAt: ULong)
data class PeerPolicy(val peer: ID, val publish: List<String>, val subscribe: List<String>)
data class PeerRoute(val member: ID, val route: String, val rttMs: ULong)
data class ConnectivityReport(val workspace: ID, val paths: List<PeerRoute>, val pathsLimited: Boolean,
                              val receiveQueue: Int, val repairJobs: Int)
data class DurationSummary(val count: ULong, val totalUs: ULong, val maxUs: ULong)
data class ControlTimingMetrics(val inquiry: DurationSummary, val hostWait: DurationSummary,
                                val hostService: DurationSummary)
data class MembershipGossipMetrics(val sent: ULong, val noOverlay: ULong, val failed: ULong,
                                   val received: ULong, val staged: ULong, val rejected: ULong,
                                   val rangePulled: ULong, val rangeFailed: ULong)
data class ConnectionCapacityMetrics(val evicted: ULong, val refused: ULong)
data class WorkspaceMetrics(
    val workspace: ID,
    val phase: WorkspacePhase,
    val reason: String?,
    val receivedBytes: ULong,
    val sentBytes: ULong,
    val receiveQueue: Int,
    val admissionQueue: Int,
    val admissionQueueBytes: Int,
    val admissionWaiters: Int,
    val admissionInFlight: Int,
    val approvalPending: Int,
    val pendingObjects: Int,
    val repairJobs: Int,
    val gossipNeighbors: Int,
    val controlTiming: ControlTimingMetrics,
    val membershipGossip: MembershipGossipMetrics,
    val connectionCapacity: ConnectionCapacityMetrics,
    val paths: List<PeerRoute>,
    val pathsLimited: Boolean
)
data class DeliveryFailure(val peer: ID, val error: String)
data class PublicationCurrent(val selector: ID, val replacementKey: ID, val expiresAt: ULong,
                              val tombstone: Boolean = false)
data class PublicationCandidate(val workspace: ID, val snapshot: ByteArray)
data class ProtectedReceptionCandidate(val workspace: ID, val snapshot: ByteArray)
data class ReceivedProtectedPublication(
    val workspace: ID,
    val revision: ULong,
    val member: ID,
    val endpoint: ID,
    val topic: String,
    val id: RecordID,
    val sequence: ULong? = null,
    val payload: ByteArray,
    val recipients: List<ID> = emptyList()
)
data class InterestObservation(val workspace: ID, val revision: ULong, val topic: String,
                               val subscribed: Boolean, val admission: DeliveryReport)
data class Publication(val workspace: ID, val revision: ULong, val sender: ID,
                       val topic: String, val payload: ByteArray)
data class RecoveredPublication(val workspace: ID, val revision: ULong, val member: ID,
                                val endpoint: ID, val topic: String, val id: RecordID,
                                val sequence: ULong?, val payload: ByteArray)
data class RecoveryRangeRequest(val peer: ID? = null, val author: ID? = null, val revision: ULong,
                                val topics: List<String>, val after: ULong? = null, val through: ULong? = null)
data class RecoveryRangeReady(val workspace: ID, val author: ID, val peer: ID, val epoch: ULong,
                              val revision: ULong, val after: ULong, val through: ULong, val packetCount: Int,
                              val retainedBytes: Int, val automaticSource: Boolean, val attempted: Int? = null)
sealed interface RecoveryRangeStatus {
    data class Pending(val candidateCount: Int, val automaticSource: Boolean) : RecoveryRangeStatus
    data class Ready(val value: RecoveryRangeReady) : RecoveryRangeStatus
    data class SourceWaiting(val automaticSource: Boolean) : RecoveryRangeStatus
    data class SourceUnavailable(val attempted: Int, val reason: String,
                                 val automaticSource: Boolean) : RecoveryRangeStatus
    data class Rejected(val reason: String) : RecoveryRangeStatus
    data object Cancelled : RecoveryRangeStatus
}
sealed interface RecoveryStage {
    data class Candidate(val value: RecoveryCandidate) : RecoveryStage
    data object AlreadyCovered : RecoveryStage
    data object NoNewObjects : RecoveryStage
}
data class RecoveryCandidate(val workspace: ID, val snapshot: ByteArray, val publicationCount: Int,
                             val alreadyReceived: Int, val durable: Boolean)
data class RecoveryAdoption(val workspace: ID, val epoch: ULong, val memberCount: Int, val durable: Boolean,
                            val recoveredPublications: Int, val missingPublications: Int)
