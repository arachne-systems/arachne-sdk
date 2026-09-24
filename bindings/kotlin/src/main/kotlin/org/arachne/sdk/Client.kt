package org.arachne.sdk

import com.fasterxml.jackson.databind.ObjectMapper
import com.fasterxml.jackson.databind.DeserializationFeature
import com.fasterxml.jackson.databind.PropertyNamingStrategies
import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import com.fasterxml.jackson.module.kotlin.readValue

enum class Network(val nativeValue: Int) { DIRECT(0), LAN(1), NEARBY(2), WAN(3), RELAY_ONLY(4), WAN_ONLY(5) }
typealias ID = ByteArray
typealias RecordID = ByteArray

data class ClientConfig(val network: Network = Network.DIRECT, val secret: ByteArray = byteArrayOf())
data class EndpointInfo(val endpointKey: ID, val boundAddress: String, val workspaceReady: Boolean)
data class Activity(val phase: String, val reason: String?)
data class WorkspaceInfo(val workspace: ID, val workspaceName: String?, val epoch: Long,
                         val memberCount: Int, val durable: Boolean, val phase: String, val reason: String?)
data class WorkspaceState(val endpointKey: ID, val workspace: ID?, val workspaceReady: Boolean,
                          val durable: Boolean, val phase: String, val reason: String?)
data class Candidate(val workspace: ID, val snapshot: ByteArray)
data class StoredResult(val value: Any?, val snapshot: ByteArray)
data class DeliveryReport(val admitted: List<ID>, val queued: Boolean, val failed: List<Any>)

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

    fun endpoint(): EndpointInfo = synchronized(lock) {
        checkOpen()
        mapper.readValue(checked(Native.sdk.arachne_sdk_describe(handle)))
    }

    fun workspaceState(): WorkspaceState {
        val raw = rawCall("workspace_state") as Map<*, *>
        val activity = raw["activity"] as Map<*, *>
        return WorkspaceState(endpoint().endpointKey, raw["workspace"]?.let(::byteVector),
            raw["workspace_ready"] as Boolean, raw["durable"] as Boolean,
            activity["state"] as String, activity["reason"] as? String)
    }

    fun createWorkspace(displayName: String, workspaceName: String? = null): WorkspaceInfo {
        val raw = rawCall("create_workspace", mapOf("display_name" to displayName,
            "workspace_name" to workspaceName)) as Map<*, *>
        val activity = raw["activity"] as Map<*, *>
        return WorkspaceInfo(byteVector(raw["workspace"]), raw["workspace_name"] as? String,
            (raw["epoch"] as Number).toLong(), (raw["members"] as Number).toInt(),
            raw["durable"] as Boolean, activity["state"] as String, activity["reason"] as? String)
    }

    fun installWorkspacePolicy(revision: Long) { rawCall("install_workspace_policy", mapOf("revision" to revision)) }

    fun stageProtectedPublication(workspace: ID, revision: Long, topic: String, id: RecordID,
                                 payload: ByteArray): Candidate {
        require(workspace.size == 32) { "workspace ID must be exactly 32 bytes" }
        require(id.size == 16) { "record ID must be exactly 16 bytes" }
        val result = rawCallStored("stage_network_publication", mapOf("workspace" to workspace,
            "revision" to revision, "topic" to topic, "id" to id, "payload" to payload))
        val value = result.value as Map<*, *>
        return Candidate(byteVector(value["workspace"]), result.snapshot)
    }

    fun adoptProtectedPublication(snapshot: ByteArray): DeliveryReport {
        val result = rawCallStored("adopt_publication", snapshot = snapshot).value as Map<*, *>
        if (result["network_error"] != null) throw ArachneException(1, result["network_error"].toString())
        return mapper.convertValue(result["admission"], DeliveryReport::class.java)
    }

    fun saveCandidate(snapshot: ByteArray) = synchronized(lock) {
        checkOpen()
        val memory = Native.bytes(snapshot)
        checked(Native.sdk.arachne_sdk_save_candidate(handle, memory, snapshot.size.toLong()))
    }

    fun enableRecordStorage(path: String, root: ID) = synchronized(lock) {
        checkOpen(); require(root.size == 32) { "storage root must be exactly 32 bytes" }
        val p = path.toByteArray(Charsets.UTF_8); val pm = Native.bytes(p); val rm = Native.bytes(root)
        checked(Native.sdk.arachne_sdk_enable_record_storage(handle, pm, p.size.toLong(), rm, root.size.toLong()))
    }

    fun restoreRecordStorage(path: String, root: ID, workspace: ID): Any? = synchronized(lock) {
        checkOpen(); require(root.size == 32 && workspace.size == 32) { "root and workspace IDs must be exactly 32 bytes" }
        val p = path.toByteArray(Charsets.UTF_8); val pm = Native.bytes(p); val rm = Native.bytes(root); val wm = Native.bytes(workspace)
        decode(checked(Native.sdk.arachne_sdk_restore_record_storage(handle, pm, p.size.toLong(),
            rm, root.size.toLong(), wm, workspace.size.toLong())))
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
        ?.map { (it as Number).toInt().toByte() }?.toByteArray()
        ?: throw ArachneException(2, "native SDK returned an invalid byte vector")
    private fun decode(bytes: ByteArray): Any? = mapper.readValue(bytes, Any::class.java)

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
