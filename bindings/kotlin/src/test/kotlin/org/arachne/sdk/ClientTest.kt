package org.arachne.sdk

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class ClientTest {
    @Test fun opensAndUsesTheNativeRuntime() {
        Client.open().use { client ->
            assertEquals(32, client.endpoint().endpointKey.size)
            val workspace = client.createWorkspace("Kotlin smoke test")
            assertEquals(32, workspace.workspace.size)
            assertTrue(workspace.phase.isNotBlank())
            assertTrue(client.rawCallStored("workspace_state").value is Map<*, *>)
            assertFailsWith<ArachneException> { client.rawCall("not_an_operation") }
        }
    }

    @Test fun validatesSecretsBeforeEnteringNativeCode() {
        val error = assertFailsWith<IllegalArgumentException> {
            Client.open(ClientConfig(secret = byteArrayOf(1)))
        }
        assertTrue(error.message!!.contains("32 bytes"))
    }
}
