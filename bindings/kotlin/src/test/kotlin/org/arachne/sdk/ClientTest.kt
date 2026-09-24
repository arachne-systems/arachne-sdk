package org.arachne.sdk

import java.nio.file.Files
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class ClientTest {
    @Test fun opensAndUsesTheNativeRuntime() {
        Client.open().use { client ->
            assertEquals(32, client.endpoint().endpointKey.size)
            val workspace = client.createWorkspace("Kotlin smoke test")
            assertEquals(32, workspace.workspace.size)
            assertEquals(WorkspacePhase.ACTIVE, workspace.phase)
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

    @Test fun typedPersistenceAndProtectedPublication() {
        val directory = Files.createTempDirectory("arachne-kotlin-")
        val path = directory.resolve("records.db")
        val secret = ByteArray(32) { 7 }
        val root = ByteArray(32) { 10 }
        val client = Client.open(ClientConfig(secret = secret))
        var workspaceId: ID? = null
        try {
            val workspace = client.createWorkspace("Kotlin smoke", "Kotlin SDK")
            workspaceId = workspace.workspace
            client.useServiceProfile()
            assertEquals(MemberKind.SERVICE, client.memberRoster().members.single().kind)
            client.enableRecordStorage(path.toFile(), root)
            client.installWorkspacePolicy(workspace.epoch + 1u)
            assertFailsWith<ArachneException> {
                client.stageProtectedPublication(ByteArray(32) { 9 }, workspace.epoch + 1u,
                    "sdk/kotlin/wrong-workspace", ByteArray(16) { 3 }, byteArrayOf(1))
            }
            val staged = client.stageProtectedPublication(workspace.workspace, workspace.epoch + 1u,
                "sdk/kotlin/smoke", ByteArray(16) { 1 }, "kotlin binding".toByteArray())
            assertTrue(staged.snapshot.isNotEmpty())
            client.saveCandidate(staged.snapshot)
            client.adoptProtectedPublication(staged.snapshot)
            client.enableObjectDelivery()
            client.enableObjectDelivery()
            val current = client.stageProtectedPublication(workspace.workspace, workspace.epoch + 1u,
                "sdk/kotlin/current", ByteArray(16) { 2 }, "current value".toByteArray(),
                PublicationCurrent(ByteArray(32) { 7 }, ByteArray(32) { 8 }, ULong.MAX_VALUE))
            client.saveCandidate(current.snapshot)
            client.adoptProtectedPublication(current.snapshot)
        } finally {
            client.close()
        }

        try {
            Client.open(ClientConfig(secret = secret)).use { restored ->
                assertTrue(restored.restoreRecordStorage(path.toFile(), root, workspaceId!!).durable)
                assertTrue(restored.workspaceState().durable)
            }
        } finally {
            Files.deleteIfExists(path)
            Files.deleteIfExists(directory)
        }
    }

    @Test fun typedInvitationAdmissionAndProtectedReception() {
        val owner = Client.open(ClientConfig(secret = ByteArray(32) { 0x31 }))
        val receiver = Client.open(ClientConfig(secret = ByteArray(32) { 0x42 }))
        try {
            val workspace = owner.createWorkspace("Owner")
            val invitation = owner.issueInvitation()
            assertEquals(workspace.workspace.toList(),
                receiver.inspectInvitation(invitation.invitation, invitation.checkpoint).workspace.toList())
            receiver.addAddressHint(invitation.peer, loopback(invitation.address))
            val join = receiver.beginJoin(invitation.invitation, invitation.checkpoint, "Receiver")
            val admission = owner.stageAdmission(join.endpoint, join.admissionRequest)
            val admittedOwner = owner.adoptAdmission(admission.snapshot)
            val reply = owner.retainedAdmission(join.endpoint, join.admissionRequest)
            val joined = receiver.stageJoin(reply.welcome,
                listOf(JoinAdmissionStep(reply.commit, reply.authorization)))
            val admittedReceiver = receiver.adoptJoin(joined.snapshot)
            assertEquals(admittedOwner.epoch, admittedReceiver.epoch)
            assertEquals(2, owner.memberRoster().members.size)
            assertEquals(workspace.workspace.toList(), owner.metrics().workspace.toList())
            assertEquals(workspace.workspace.toList(), receiver.connectivity().workspace.toList())

            val receiverEndpoint = receiver.endpoint()
            owner.addAddressHint(receiverEndpoint.endpointKey, loopback(receiverEndpoint.boundAddress))
            val revision = admittedOwner.epoch + 1u
            val topic = "sdk/kotlin/receive"
            owner.installWorkspacePolicy(revision)
            receiver.installWorkspacePolicy(revision)
            receiver.setInterest(workspace.workspace, revision, topic, true)
            val subscription = awaitValue { receiver.pollInterest() }
            assertTrue(subscription.admission.failed.isEmpty())

            val payload = "protected Kotlin receive".toByteArray()
            val publication = owner.stageProtectedPublication(workspace.workspace, revision, topic,
                ByteArray(16) { 1 }, payload)
            assertTrue(owner.adoptProtectedPublication(publication.snapshot).failed.isEmpty())
            val reception = awaitValue { receiver.pollProtected() }
            val message = receiver.adoptProtectedReception(reception.snapshot)
            assertEquals(workspace.workspace.toList(), message.workspace.toList())
            assertEquals(topic, message.topic)
            assertContentEquals(payload, message.payload)
            assertEquals(owner.endpoint().endpointKey.toList(), message.endpoint.toList())
            assertEquals(null, receiver.pollRecoveredPublication())
            assertFailsWith<ArachneException> {
                owner.publish(workspace.workspace, revision, topic, "unprotected".toByteArray())
            }
            assertFailsWith<ArachneException> { receiver.poll() }
        } finally {
            owner.close()
            receiver.close()
        }
    }

    private fun loopback(address: String) = address.replace("0.0.0.0:", "127.0.0.1:")

    private fun <T : Any> awaitValue(read: () -> T?): T {
        val deadline = System.nanoTime() + 10_000_000_000L
        while (System.nanoTime() < deadline) {
            read()?.let { return it }
            Thread.sleep(10)
        }
        error("SDK operation did not produce a value before the deadline")
    }
}
