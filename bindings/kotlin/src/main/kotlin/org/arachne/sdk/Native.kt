package org.arachne.sdk

import com.sun.jna.Library
import com.sun.jna.Memory
import com.sun.jna.Native as JnaNative
import com.sun.jna.Pointer
import com.sun.jna.Structure

internal interface NativeSdk : Library {
    fun arachne_sdk_open(network: Int, secret: Pointer?, secretLen: Long): Result.ByValue
    fun arachne_sdk_execute(handle: Long, request: Pointer, requestLen: Long): Result.ByValue
    fun arachne_sdk_execute_stored(handle: Long, request: Pointer, requestLen: Long,
                                   snapshot: Pointer?, snapshotLen: Long): StoredResult.ByValue
    fun arachne_sdk_describe(handle: Long): Result.ByValue
    fun arachne_sdk_enable_record_storage(handle: Long, path: Pointer, pathLen: Long,
                                          root: Pointer, rootLen: Long): Result.ByValue
    fun arachne_sdk_restore_record_storage(handle: Long, path: Pointer, pathLen: Long,
                                           root: Pointer, rootLen: Long, workspace: Pointer,
                                           workspaceLen: Long): Result.ByValue
    fun arachne_sdk_save_candidate(handle: Long, snapshot: Pointer?, snapshotLen: Long): Result.ByValue
    fun arachne_sdk_cancel(handle: Long): Result.ByValue
    fun arachne_sdk_wait_for_work(handle: Long): Result.ByValue
    fun arachne_sdk_close(handle: Long): Result.ByValue
    fun arachne_sdk_buffer_free(data: Pointer?, len: Long)

    @Structure.FieldOrder("status", "value")
    open class Result : Structure() {
        @JvmField var status: Int = 0
        @JvmField var value: Buffer = Buffer()
        class ByValue : Result(), Structure.ByValue
    }

    @Structure.FieldOrder("data", "len")
    open class Buffer : Structure() {
        @JvmField var data: Pointer? = null
        @JvmField var len: Long = 0
    }

    @Structure.FieldOrder("status", "value", "snapshot")
    open class StoredResult : Structure() {
        @JvmField var status: Int = 0
        @JvmField var value: Buffer = Buffer()
        @JvmField var snapshot: Buffer = Buffer()
        class ByValue : StoredResult(), Structure.ByValue
    }
}

internal object Native {
    val sdk: NativeSdk by lazy {
        val path = System.getenv("ARACHNE_SDK_LIBRARY")
        if (path.isNullOrBlank()) JnaNative.load("arachne_sdk", NativeSdk::class.java)
        else JnaNative.load(path, NativeSdk::class.java)
    }

    fun bytes(bytes: ByteArray): Memory = Memory((bytes.size.coerceAtLeast(1)).toLong()).also {
        if (bytes.isNotEmpty()) it.write(0, bytes, 0, bytes.size)
    }
}
