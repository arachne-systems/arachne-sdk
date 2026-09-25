package org.arachne.sdk.smoke

import androidx.test.ext.junit.runners.AndroidJUnit4
import org.arachne.sdk.generated.ApiException
import org.arachne.sdk.generated.Client
import org.arachne.sdk.generated.ClientConfig
import org.arachne.sdk.generated.ErrorCode
import org.arachne.sdk.generated.Network
import org.arachne.sdk.generated.WorkspacePhase
import org.arachne.sdk.generated.apiErrorCode
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
        val client = Client.open(ClientConfig(network = Network.DIRECT))
        try {
            assertEquals(64, client.describe().endpointId.length)
            val workspace = client.createWorkspace("Android smoke", null)
            assertEquals(WorkspacePhase.ACTIVE, workspace.phase)
            assertNull(client.nextEvent(100uL))

            var parked: Any? = Unit
            val waiter = thread { parked = client.nextEvent(30_000uL) }
            Thread.sleep(200)
            client.shutdown()
            waiter.join(2_000)
            assertTrue("close() released next_event", !waiter.isAlive && parked == null)
        } finally {
            client.close()
        }
    }

    @Test
    fun errorsCrossWithTheirCode() {
        try {
            Client.open(ClientConfig(network = Network.TOR, secret = ByteArray(32)))
            fail("Tor must throw")
        } catch (e: ApiException) {
            assertEquals(ErrorCode.UNSUPPORTED, apiErrorCode(e))
            assertEquals(103u, apiErrorCode(e).number())
        }
    }
}
