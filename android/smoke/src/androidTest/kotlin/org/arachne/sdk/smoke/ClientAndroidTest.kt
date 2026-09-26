package org.arachne.sdk.smoke

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.arachne.core.api.ApiException
import org.arachne.core.api.ErrorCode
import org.arachne.core.api.Network
import org.arachne.core.api.PowerProfile
import org.arachne.core.api.apiErrorCode
import org.arachne.core.api.defaultLimits
import org.arachne.core.runtime.Client
import org.arachne.core.runtime.Context
import org.arachne.core.runtime.Phase
import org.arachne.core.runtime.StorageConfig
import org.arachne.core.runtime.defaultClientConfig
import java.io.File
import java.time.Duration
import java.util.UUID
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import org.junit.runner.RunWith
import kotlin.concurrent.thread

/** Loads the generated binding and libarachne_sdk.so from the minified app. */
@RunWith(AndroidJUnit4::class)
class ClientAndroidTest {
    @Test
    fun loadsRuntimeCreatesWorkspaceAndCloses() {
        val directory = File(
            InstrumentationRegistry.getInstrumentation().targetContext.cacheDir,
            "sdk-storage-${UUID.randomUUID()}",
        ).apply { check(mkdirs()) }
        val context = Context.owned(defaultLimits(), PowerProfile.NORMAL, 2u)
        val config = defaultClientConfig(Network.DIRECT).copy(
            secret = ByteArray(32) { 0x51 },
            storage = StorageConfig.openSqlite(directory.path, ByteArray(32) { 0x52 }),
        )
        val client = Client.openIn(context, config)
        try {
            assertEquals(64, client.endpoint().endpointKey.length)
            val workspace = client.createWorkspace("Android smoke", null)
            assertEquals(Phase.ACTIVE, workspace.phase)
            assertNull(client.nextEvent(Duration.ofMillis(100)))

            var parked: Any? = Unit
            val waiter = thread { parked = client.nextEvent(Duration.ofSeconds(30)) }
            Thread.sleep(200)
            client.shutdown()
            waiter.join(2_000)
            assertTrue("close() released next_event", !waiter.isAlive && parked == null)
        } finally {
            client.shutdown()
            client.close()
            context.close()
            directory.deleteRecursively()
        }
    }

    @Test
    fun errorsCrossWithTheirCode() {
        try {
            Client.open(defaultClientConfig(Network.TOR).copy(secret = ByteArray(32)))
            fail("Tor must throw")
        } catch (e: ApiException) {
            assertEquals(ErrorCode.UNSUPPORTED, apiErrorCode(e))
            assertEquals(103u, apiErrorCode(e).value)
        }
    }
}
