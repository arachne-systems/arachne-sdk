package org.arachne.sdk

import com.fasterxml.jackson.databind.DeserializationFeature
import com.fasterxml.jackson.databind.ObjectMapper
import com.fasterxml.jackson.databind.PropertyNamingStrategies
import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import com.fasterxml.jackson.module.kotlin.readValue
import com.fasterxml.jackson.databind.module.SimpleModule
import com.fasterxml.jackson.databind.JsonSerializer
import com.fasterxml.jackson.databind.SerializerProvider
import com.fasterxml.jackson.core.JsonGenerator
import com.fasterxml.jackson.annotation.JsonProperty
import java.io.File

enum class Network(val nativeValue: Int) { DIRECT(0), LAN(1), NEARBY(2), WAN(3), RELAY_ONLY(4), WAN_ONLY(5) }
typealias ID = ByteArray
typealias RecordID = ByteArray

data class ClientConfig(val network: Network = Network.DIRECT, val secret: ByteArray = byteArrayOf())
data class EndpointInfo(val endpointKey: ID, val boundAddress: String, val workspaceReady: Boolean)
data class Activity(@param:JsonProperty("state") val phase: WorkspacePhase,
                    val reason: String?)
data class WorkspaceInfo(val workspace: ID, val workspaceName: String?, val epoch: ULong,
                         val memberCount: Int, val durable: Boolean, val phase: WorkspacePhase, val reason: String?)
data class WorkspaceState(val endpointKey: ID, val workspace: ID?, val workspaceReady: Boolean,
                          val durable: Boolean, val phase: WorkspacePhase, val reason: String?)
data class StoredResult(val value: Any?, val snapshot: ByteArray)
data class DeliveryReport(val admitted: List<ID>, val queued: Boolean, val failed: List<DeliveryFailure>)

class ArachneException(val status: Int, message: String) : RuntimeException(message)

/** Blocking, serialized Kotlin access to the shared Rust SDK. */
class Client private constructor(private val handle: Long) : AutoCloseable {
    private val lock = Any()
    private var closed = false
    private val mapper = JSON

    fun rawCall(op: String, params: Map<String, Any?> = emptyMap()): Any? = synchronized(lock) {
        checkOpen()
        val request = request(op, params)
        val memory = Native.bytes(request)
        decode(checked(Native.sdk.arachne_sdk_execute(handle, memory, request.size.toLong())))
    }

    fun call(op: String, params: Map<String, Any?> = emptyMap()) = rawCall(op, params)

    fun rawCallStored(op: String, params: Map<String, Any?> = emptyMap(), snapshot: ByteArray = byteArrayOf()): StoredResult = synchronized(lock) {
        checkOpen()
        val request = request(op, params)
        val reqMemory = Native.bytes(request)
        val snapshotMemory = Native.bytes(snapshot)
        val result = Native.sdk.arachne_sdk_execute_stored(handle, reqMemory, request.size.toLong(),
            snapshotMemory, snapshot.size.toLong())
        result.read()
        val value = take(result.value)
        val staged = take(result.snapshot)
        if (result.status != 0) throw ArachneException(result.status, value.toString(Charsets.UTF_8))
        StoredResult(decode(value), staged)
    }

    fun callStored(op: String, params: Map<String, Any?> = emptyMap(), snapshot: ByteArray = byteArrayOf()) =
        rawCallStored(op, params, snapshot)

    fun endpoint(): EndpointInfo = synchronized(lock) {
        checkOpen()
        mapper.readValue(checked(Native.sdk.arachne_sdk_describe(handle)))
    }

    fun workspaceState(): WorkspaceState = synchronized(lock) {
        checkOpen()
        val raw = model(rawCall("workspace_state"), RawWorkspaceState::class.java)
        WorkspaceState(endpoint().endpointKey, raw.workspace, raw.workspaceReady,
            raw.durable, raw.activity.phase, raw.activity.reason)
    }

    fun createWorkspace(displayName: String, workspaceName: String? = null): WorkspaceInfo {
        return workspaceInfo(rawCall("create_workspace", mapOf("display_name" to displayName,
            "workspace_name" to workspaceName)))
    }

    fun beginJoin(invitation: ByteArray, checkpoint: ByteArray, displayName: String,
                  peers: List<ID> = emptyList()): JoinRequest {
        val raw = model(rawCall("begin_join", mapOf("invitation" to invitation, "checkpoint" to checkpoint,
            "display_name" to displayName, "peers" to peers)), RawJoinRequest::class.java)
        val admission = raw.admissionRequest ?: throw ArachneException(1, "invitation has no admission request")
        return JoinRequest(checkId(raw.workspace), checkId(raw.member.id), checkId(raw.endpoint), admission)
    }

    fun driveJoin(): Any? = rawCall("drive_join")

    fun stageAdmission(authenticatedEndpoint: ID, request: ByteArray): WorkspaceCandidate {
        val result = rawCallStored("stage_admission", mapOf("authenticated_endpoint" to checkId(authenticatedEndpoint),
            "request" to request))
        return WorkspaceCandidate(checkId(model(result.value, RawWorkspaceCandidate::class.java).workspace), result.snapshot)
    }

    fun adoptAdmission(snapshot: ByteArray): WorkspaceInfo =
        workspaceInfo(rawCallStored("adopt_admission", snapshot = snapshot).value)

    fun retainedAdmission(authenticatedEndpoint: ID, request: ByteArray): AdmissionReply =
        model(rawCall("retained_admission", mapOf("authenticated_endpoint" to checkId(authenticatedEndpoint),
            "request" to request)), AdmissionReply::class.java)

    fun stageJoin(welcome: ByteArray, commits: List<JoinAdmissionStep>): WorkspaceCandidate {
        val result = rawCallStored("stage_join", mapOf("commits" to commits), welcome)
        return WorkspaceCandidate(checkId(model(result.value, RawWorkspaceCandidate::class.java).workspace), result.snapshot)
    }

    fun adoptJoin(snapshot: ByteArray): WorkspaceInfo =
        workspaceInfo(rawCallStored("adopt_join", snapshot = snapshot).value)

    fun installWorkspacePolicy(revision: ULong) { rawCall("install_workspace_policy", mapOf("revision" to revision)) }

    fun enableObjectDelivery() = synchronized(lock) {
        checkOpen()
        val durable = workspaceState().durable
        val staged = rawCallStored("enable_object_delivery")
        val state = (staged.value as? Map<*, *>)?.get("state") as? String
        if (staged.snapshot.isEmpty()) {
            if (state == "object_delivery_enabled") return@synchronized
            throw ArachneException(2, "object delivery returned no adoptable snapshot")
        }
        if (durable) saveCandidate(staged.snapshot)
        rawCallStored("adopt_reception", snapshot = staged.snapshot)
    }

    fun stageProtectedPublication(workspace: ID, revision: ULong, topic: String, id: RecordID,
                                 payload: ByteArray, current: PublicationCurrent? = null): PublicationCandidate {
        require(workspace.size == 32) { "workspace ID must be exactly 32 bytes" }
        require(id.size == 16) { "record ID must be exactly 16 bytes" }
        val activeWorkspace = workspaceState().workspace
            ?: throw ArachneException(1, "no active workspace for protected publication")
        if (!activeWorkspace.contentEquals(workspace)) throw ArachneException(1, "publication candidate workspace mismatch")
        val result = rawCallStored("stage_network_publication", mapOf(
            "revision" to revision, "topic" to topic, "id" to id, "payload" to payload,
            "current" to current))
        val value = model(result.value, RawWorkspaceCandidate::class.java)
        if (!value.workspace.contentEquals(workspace)) throw ArachneException(2, "publication candidate workspace mismatch")
        return PublicationCandidate(value.workspace, result.snapshot)
    }

    fun adoptProtectedPublication(snapshot: ByteArray): DeliveryReport {
        val result = model(rawCallStored("adopt_publication", snapshot = snapshot).value, RawAdoptPublication::class.java)
        if (result.networkError != null) throw ArachneException(1, result.networkError)
        return result.admission
    }

    fun pollProtected(): ProtectedReceptionCandidate? {
        val result = rawCallStored("poll_protected")
        if (result.value == null) {
            if (result.snapshot.isNotEmpty()) throw ArachneException(2, "empty protected reception returned a snapshot")
            return null
        }
        val raw = model(result.value, RawProtectedCandidate::class.java)
        if (raw.state != "awaiting_reception_save" || result.snapshot.isEmpty()) {
            throw ArachneException(2, "protected reception has no adoptable snapshot")
        }
        return ProtectedReceptionCandidate(checkId(raw.workspace), result.snapshot)
    }

    fun adoptProtectedReception(snapshot: ByteArray): ReceivedProtectedPublication =
        model(rawCallStored("adopt_reception", snapshot = snapshot).value, ReceivedProtectedPublication::class.java)

    fun setInterest(workspace: ID, revision: ULong, topic: String, subscribed: Boolean) {
        rawCall("set_interest", mapOf("workspace" to checkId(workspace), "revision" to revision,
            "topic" to topic, "subscribed" to subscribed))
    }

    fun pollInterest(): InterestObservation? {
        val value = rawCall("poll_interest") ?: return null
        val raw = model(value, RawInterest::class.java)
        return when (raw.state) {
            "interest_pending" -> null
            "interest_failed" -> throw ArachneException(1, raw.error ?: "interest update failed")
            else -> model(value, InterestObservation::class.java)
        }
    }

    fun publish(workspace: ID, revision: ULong, topic: String, payload: ByteArray): DeliveryReport =
        model(rawCall("publish", mapOf("workspace" to checkId(workspace), "revision" to revision,
            "topic" to topic, "payload" to payload)), DeliveryReport::class.java)

    fun poll(): Publication? = rawCall("poll")?.let { model(it, Publication::class.java) }
    fun pollRecoveredPublication(): RecoveredPublication? =
        rawCall("poll_recovered_publication")?.let { model(it, RecoveredPublication::class.java) }

    fun fetchRecoveryRange(request: RecoveryRangeRequest): RecoveryRangeStatus =
        recoveryRange(rawCall("fetch_recovery_range", mapOf("peer" to request.peer, "author" to request.author,
            "revision" to request.revision, "topics" to request.topics,
            "after" to request.after, "through" to request.through)))

    fun pollRecoveryRange(): RecoveryRangeStatus? = rawCall("poll_recovery_range")?.let(::recoveryRange)

    fun cancelRecoveryRange() {
        val state = model(rawCall("cancel_recovery_range"), RawState::class.java).state
        if (state != "recovery_range_cancelled") throw ArachneException(2, "invalid recovery cancellation response")
    }

    fun stageRecoveryRange(retainUntil: ULong): RecoveryStage {
        val staged = rawCallStored("stage_recovery_range", mapOf("retain_until" to retainUntil))
        val raw = model(staged.value, RawRecoveryStage::class.java)
        return when (raw.state) {
            "awaiting_recovery_save" -> RecoveryStage.Candidate(RecoveryCandidate(
                checkId(raw.workspace ?: throw ArachneException(2, "recovery candidate has no workspace")),
                staged.snapshot, raw.publicationCount ?: 0, raw.alreadyReceived ?: 0, raw.durable ?: false))
            "recovery_already_covered" -> RecoveryStage.AlreadyCovered
            "recovery_no_new_objects" -> RecoveryStage.NoNewObjects
            else -> throw ArachneException(2, "unknown recovery stage: ${raw.state}")
        }
    }

    fun adoptRecovery(snapshot: ByteArray): RecoveryAdoption {
        val raw = model(rawCallStored("adopt_recovery", snapshot = snapshot).value, RawRecoveryAdoption::class.java)
        val (recovered, missing) = when (raw.state) {
            "recovery_adopted" -> (raw.publicationCount ?: 0) to 0
            "direct_miss_adopted" -> 0 to (raw.missingCount ?: 0)
            else -> throw ArachneException(2, "unknown recovery adoption state: ${raw.state}")
        }
        return RecoveryAdoption(checkId(raw.workspace), raw.epoch, raw.members, raw.durable, recovered, missing)
    }

    fun saveCandidate(snapshot: ByteArray) = synchronized(lock) {
        checkOpen()
        val memory = Native.bytes(snapshot)
        checked(Native.sdk.arachne_sdk_save_candidate(handle, memory, snapshot.size.toLong()))
    }

    fun enableRecordStorage(path: File, root: ID) = synchronized(lock) {
        checkOpen(); require(root.size == 32) { "storage root must be exactly 32 bytes" }
        val p = path.absolutePath.toByteArray(Charsets.UTF_8); val pm = Native.bytes(p); val rm = Native.bytes(root)
        checked(Native.sdk.arachne_sdk_enable_record_storage(handle, pm, p.size.toLong(), rm, root.size.toLong()))
    }

    fun restoreRecordStorage(path: File, root: ID, workspace: ID): RestoreResult = synchronized(lock) {
        checkOpen(); require(root.size == 32 && workspace.size == 32) { "root and workspace IDs must be exactly 32 bytes" }
        val p = path.absolutePath.toByteArray(Charsets.UTF_8); val pm = Native.bytes(p); val rm = Native.bytes(root); val wm = Native.bytes(workspace)
        model(decode(checked(Native.sdk.arachne_sdk_restore_record_storage(handle, pm, p.size.toLong(),
            rm, root.size.toLong(), wm, workspace.size.toLong()))), RestoreResult::class.java)
    }

    fun memberRoster(): MemberRoster {
        val raw = model(rawCall("member_roster"), RawMemberRoster::class.java)
        return MemberRoster(checkId(raw.workspace), raw.workspaceName, raw.workspaceNameRevision,
            checkId(raw.workspaceNameHead), raw.epoch, raw.members, raw.profiles.size, raw.profilesRetained ?: true)
    }

    fun useServiceProfile() { rawCall("use_service_profile") }

    fun issueInvitation(): InvitationInfo = model(rawCall("issue_invitation"), InvitationInfo::class.java)

    fun inspectInvitation(invitation: ByteArray, checkpoint: ByteArray): InvitationDetails {
        val raw = model(rawCall("inspect_invitation", mapOf("invitation" to invitation,
            "checkpoint" to checkpoint)), RawInvitationDetails::class.java)
        return InvitationDetails(checkId(raw.workspace), checkId(raw.invitationKey), raw.workspaceName,
            raw.epoch, raw.personalInvitation, raw.automaticApproval, raw.expiresAt)
    }

    fun metrics(): WorkspaceMetrics {
        val raw = model(rawCall("workspace_metrics"), RawWorkspaceMetrics::class.java)
        return WorkspaceMetrics(checkId(raw.workspace), raw.activity.phase, raw.activity.reason,
            raw.receivedBytes, raw.sentBytes, raw.receiveQueue, raw.admissionQueue, raw.admissionQueueBytes,
            raw.admissionWaiters, raw.admissionInFlight, raw.approvalPending, raw.pendingObjects,
            raw.repairJobs, raw.gossipNeighbors, raw.controlTiming, raw.membershipGossip,
            raw.connectionCapacity, raw.paths, raw.pathsLimited)
    }

    fun connectivity(): ConnectivityReport = metrics().let {
        ConnectivityReport(it.workspace, it.paths, it.pathsLimited, it.receiveQueue, it.repairJobs)
    }

    fun networkChange() { rawCall("network_change") }
    fun pollControl(): Boolean = rawCall("poll_admission") != null
    fun addAddressHint(peer: ID, address: String) {
        rawCall("add_address_hint", mapOf("peer" to checkId(peer), "address" to address))
    }
    fun installPolicy(workspace: ID, revision: ULong, endpoints: List<PeerPolicy>) {
        rawCall("install_verified_policy", mapOf("workspace" to checkId(workspace),
            "revision" to revision, "endpoints" to endpoints))
    }

    fun cancel() = synchronized(lock) { checkOpen(); checked(Native.sdk.arachne_sdk_cancel(handle)) }
    fun waitForWork(): Boolean = synchronized(lock) {
        checkOpen(); checked(Native.sdk.arachne_sdk_wait_for_work(handle)).toString(Charsets.US_ASCII) == "1"
    }

    override fun close() = synchronized(lock) {
        if (!closed) { checked(Native.sdk.arachne_sdk_close(handle)); closed = true }
    }

    private fun checkOpen() { if (closed) throw ArachneException(1, "Arachne client is closed") }
    private fun request(op: String, params: Map<String, Any?>): ByteArray {
        require(op.isNotBlank()) { "operation name is required" }
        require("op" !in params) { "params must not contain op" }
        val normalized = params.mapValues { normalize(it.value) } + ("op" to op)
        return mapper.writeValueAsBytes(normalized)
    }
    private fun normalize(value: Any?): Any? = when (value) {
        is ByteArray -> value.map { it.toInt() and 0xff }
        is Map<*, *> -> value.entries.associate { it.key.toString() to normalize(it.value) }
        is Iterable<*> -> value.map(::normalize)
        else -> value
    }
    private fun byteVector(value: Any?): ByteArray = (value as? List<*>)
        ?.map {
            val byte = (it as? Number)?.toInt() ?: throw ArachneException(2, "invalid byte vector value")
            if (byte !in 0..255) throw ArachneException(2, "invalid byte vector value")
            byte.toByte()
        }?.toByteArray()
        ?: throw ArachneException(2, "native SDK returned an invalid byte vector")
    private fun decode(bytes: ByteArray): Any? = mapper.readValue(bytes, Any::class.java)
    private fun <T> model(value: Any?, type: Class<T>): T = mapper.convertValue(value, type)

    private fun workspaceInfo(value: Any?): WorkspaceInfo {
        val raw = model(value, RawWorkspaceInfo::class.java)
        return WorkspaceInfo(checkId(raw.workspace), raw.workspaceName, raw.epoch, raw.members,
            raw.durable, raw.activity.phase, raw.activity.reason)
    }

    private fun recoveryRange(value: Any?): RecoveryRangeStatus {
        val raw = model(value, RawRecoveryRangeStatus::class.java)
        return when (raw.state) {
            "recovery_range_pending" -> RecoveryRangeStatus.Pending(raw.candidateCount ?: 0, raw.automaticSource ?: false)
            "recovery_range_ready" -> RecoveryRangeStatus.Ready(model(value, RecoveryRangeReady::class.java))
            "recovery_source_waiting" -> RecoveryRangeStatus.SourceWaiting(raw.automaticSource ?: true)
            "recovery_source_unavailable" -> RecoveryRangeStatus.SourceUnavailable(raw.attempted ?: 0,
                raw.reason ?: "source unavailable", raw.automaticSource ?: false)
            "recovery_range_rejected" -> RecoveryRangeStatus.Rejected(raw.reason ?: "recovery range rejected")
            "recovery_range_cancelled" -> RecoveryRangeStatus.Cancelled
            else -> throw ArachneException(2, "unknown recovery range state: ${raw.state}")
        }
    }

    private fun checkId(value: ID): ID = value.also {
        if (it.size != 32) throw ArachneException(2, "native SDK returned an invalid ID")
    }

    private fun checked(result: NativeSdk.Result): ByteArray {
        result.read()
        val value = take(result.value)
        if (result.status != 0) throw ArachneException(result.status, value.toString(Charsets.UTF_8))
        return value
    }

    private fun take(buffer: NativeSdk.Buffer): ByteArray {
        buffer.read()
        val bytes = buffer.data?.getByteArray(0, buffer.len.toInt()) ?: byteArrayOf()
        Native.sdk.arachne_sdk_buffer_free(buffer.data, buffer.len)
        return bytes
    }

    companion object {
        private val JSON: ObjectMapper = jacksonObjectMapper()
            .registerModule(SimpleModule().addSerializer(ByteArray::class.java,
                object : JsonSerializer<ByteArray>() {
                    override fun serialize(value: ByteArray, gen: JsonGenerator, serializers: SerializerProvider) {
                        gen.writeStartArray()
                        value.forEach { gen.writeNumber(it.toInt() and 0xff) }
                        gen.writeEndArray()
                    }
                }))
            .setPropertyNamingStrategy(PropertyNamingStrategies.SNAKE_CASE)
            .disable(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES)

        @JvmStatic fun open(config: ClientConfig = ClientConfig()): Client {
            require(config.secret.isEmpty() || config.secret.size == 32) { "endpoint secret must be exactly 32 bytes" }
            val memory = Native.bytes(config.secret)
            val result = Native.sdk.arachne_sdk_open(config.network.nativeValue, memory, config.secret.size.toLong())
            result.read()
            val value = result.value
            value.read()
            val bytes = value.data?.getByteArray(0, value.len.toInt()) ?: byteArrayOf()
            Native.sdk.arachne_sdk_buffer_free(value.data, value.len)
            if (result.status != 0) throw ArachneException(result.status, bytes.toString(Charsets.UTF_8))
            val handle = bytes.toString(Charsets.US_ASCII).toLongOrNull()
                ?: throw ArachneException(2, "native SDK returned an invalid client handle")
            return Client(handle)
        }
    }
}

private data class RawWorkspaceState(val workspace: ID?, val workspaceReady: Boolean,
                                    val durable: Boolean, val activity: Activity)
private data class RawWorkspaceInfo(val workspace: ID, val workspaceName: String?, val epoch: ULong,
                                    val members: Int, val durable: Boolean, val activity: Activity)
private data class RawJoinRequest(val workspace: ID, val endpoint: ID, val member: Member,
                                  val admissionRequest: ByteArray?) {
    data class Member(val id: ID)
}
private data class RawWorkspaceCandidate(val workspace: ID)
private data class RawMemberRoster(val workspace: ID, val workspaceName: String?,
                                   val workspaceNameRevision: ULong, val workspaceNameHead: ID,
                                   val epoch: ULong, val members: List<MemberInfo>,
                                   val profiles: List<ByteArray>, val profilesRetained: Boolean?)
private data class RawInvitationDetails(val workspace: ID, val invitationKey: ID, val workspaceName: String?,
                                        val epoch: ULong, val personalInvitation: Boolean,
                                        val automaticApproval: Boolean, val expiresAt: ULong)
private data class RawWorkspaceMetrics(
    val workspace: ID, val activity: Activity, val receivedBytes: ULong, val sentBytes: ULong,
    val receiveQueue: Int, val admissionQueue: Int, val admissionQueueBytes: Int, val admissionWaiters: Int,
    val admissionInFlight: Int, val approvalPending: Int, val pendingObjects: Int, val repairJobs: Int,
    val gossipNeighbors: Int, val controlTiming: ControlTimingMetrics,
    val membershipGossip: MembershipGossipMetrics, val connectionCapacity: ConnectionCapacityMetrics,
    val paths: List<PeerRoute>, val pathsLimited: Boolean
)
private data class RawAdoptPublication(val admission: DeliveryReport, val networkError: String?)
private data class RawProtectedCandidate(val workspace: ID, val state: String)
private data class RawInterest(val state: String?, val error: String?)
private data class RawState(val state: String)
private data class RawRecoveryRangeStatus(val state: String, val candidateCount: Int? = null,
    val automaticSource: Boolean? = null, val attempted: Int? = null, val reason: String? = null)
private data class RawRecoveryStage(val state: String, val workspace: ID? = null, val publicationCount: Int? = null,
    val alreadyReceived: Int? = null, val durable: Boolean? = null)
private data class RawRecoveryAdoption(val workspace: ID, val epoch: ULong, val members: Int, val durable: Boolean,
    val state: String, val publicationCount: Int? = null, val missingCount: Int? = null)
