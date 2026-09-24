package org.arachne.sdk.smoke

import androidx.test.ext.junit.runners.AndroidJUnit4
import org.arachne.sdk.Client
import org.arachne.sdk.WorkspacePhase
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class ClientAndroidTest {
    @Test fun loadsRustRuntimeAndCreatesWorkspace() {
        Client.open().use { client ->
            assertEquals(32, client.endpoint().endpointKey.size)
            assertEquals(WorkspacePhase.ACTIVE, client.createWorkspace("Android smoke").phase)
        }
    }
}
