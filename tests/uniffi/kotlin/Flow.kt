// Two-client flow over localhost for the generated Kotlin binding: invitation
// -> join -> publish -> recovery -> ack; interest -> publish -> protected
// receive -> reject; presence, metrics, deadline, suspend/resume; candidate
// misuse. Called from SmokeTest.kt.
import org.arachne.sdk.generated.*

private const val TOPIC = "streams/uniffi"

private fun <T : Any> until(what: String, step: () -> T?): T {
    val deadline = System.nanoTime() + 15_000_000_000L
    while (true) {
        step()?.let { return it }
        if (System.nanoTime() >= deadline) check(false, "$what timed out")
        Thread.sleep(10)
    }
}

private fun local(address: String) = address.replace("0.0.0.0:", "127.0.0.1:")

private fun openClient(seed: Int) =
    Client.open(ClientConfig(network = Network.DIRECT, secret = ByteArray(32) { seed.toByte() }))

fun runFlow() {
    val owner = openClient(0x71)
    val reader = openClient(0x72)
    val created = owner.createWorkspace("Owner", "Kotlin flow")

    // Invitations (and candidate misuse).
    val staged = owner.stageInvitation(0uL, InvitationKind.REUSABLE)
    val other = openClient(0x73)
    try {
        other.adoptInvitation(staged)
        check(false, "adopt on another client must throw")
    } catch (e: ApiException) {
        check(apiErrorCode(e) == ErrorCode.WRONG_STATE, "candidate bound to its client: code ${apiErrorCode(e).number()}")
    }
    other.shutdown(); other.close()
    val invitation = owner.adoptInvitation(staged)
    try {
        owner.adoptInvitation(staged)
        check(false, "second adopt must throw")
    } catch (e: ApiException) {
        check(apiErrorCode(e) == ErrorCode.WRONG_STATE, "candidate single use: code ${apiErrorCode(e).number()}")
    }
    val details = reader.inspectInvitation(invitation.invitation, invitation.checkpoint)
    check(details.workspace == created.workspace, "inspect_invitation workspace = ${details.workspace.take(16)}...")

    // Join and admission.
    reader.addAddressHint(invitation.peer, local(invitation.address))
    val join = reader.beginJoin(invitation.invitation, invitation.checkpoint, "Reader", emptyList())
    val ownerView = owner.adoptAdmission(owner.stageAdmission(join.endpoint, join.admissionRequest))
    val reply = owner.retainedAdmission(join.endpoint, join.admissionRequest)
    val readerView = reader.adoptJoin(reader.stageJoin(reply.welcome, listOf(JoinAdmissionStep(reply.commit, reply.authorization))))
    check(ownerView.epoch == readerView.epoch && readerView.memberCount == 2uL,
        "joined at epoch ${readerView.epoch} with ${readerView.memberCount} members")
    val me = reader.describe()
    owner.addAddressHint(me.endpointId, local(me.boundAddress))

    // Publication the reader is not subscribed to, then recovery.
    val revision = ownerView.epoch + 1uL
    owner.installWorkspacePolicy(revision)
    reader.installWorkspacePolicy(revision)
    owner.adoptProtectedPublication(owner.stageProtectedPublication(
        created.workspace, revision, TOPIC, "01".repeat(16), "first".toByteArray(), null))
    val author = owner.memberRoster().members.first { it.selfMember }.id
    val ownerEndpoint = owner.describe().endpointId
    reader.fetchRecoveryRange(RecoveryRangeRequest(ownerEndpoint, author, revision, listOf(TOPIC), 0uL, 1uL), null)
    val ready = until("recovery range") { owner.pollControl(); reader.pollRecoveryRange() }
    check(ready is RecoveryRangeStatus.Ready, "recovery range ready: $ready")
    val stage = reader.stageRecoveryRange(0uL)
    check(stage is RecoveryStage.Candidate, "stage_recovery_range gave a candidate")
    val adoption = reader.adoptRecovery((stage as RecoveryStage.Candidate).candidate)
    check(adoption.recoveredPublications == 1uL, "recovered ${adoption.recoveredPublications} publication")
    val recovered = reader.pollPendingObject()
    check(recovered != null && recovered.payload.contentEquals("first".toByteArray()), "recovered object is pending")
    reader.adoptProtectedReception(reader.stageObjectAcknowledgement(recovered!!))
    check(reader.pollPendingObject() == null, "acknowledged object left the inbox")

    // Interest, live protected receive, rejection.
    reader.setInterest(created.workspace, revision, TOPIC, true)
    val observed = until("interest") { reader.pollInterest() }
    check(observed.subscribed, "interest settled")
    val report = owner.adoptProtectedPublication(owner.stageProtectedPublication(
        created.workspace, revision, TOPIC, "02".repeat(16), "second".toByteArray(), null))
    check(report.failed.isEmpty(), "publication sent")
    val reception = until("protected receive") { reader.pollProtected() }
    reader.adoptProtectedReception(reception)
    val received = reader.pollPendingObject()!!
    check(received.payload.contentEquals("second".toByteArray()) && received.endpoint == ownerEndpoint,
        "received the live publication")
    reader.adoptProtectedReception(reader.stageObjectRejection(received))
    check(reader.pollPendingObject() == null, "rejected object left the inbox")

    // Presence, metrics, deadline, suspend/resume.
    val round = owner.pollPresence(false)
    check(round.responseErrors <= 1u, "presence round: ${round.responseErrors} error(s)")
    check(reader.metrics().workspace == created.workspace, "metrics workspace")
    reader.setDeadline(5000uL)
    `suspend`()
    check(isSuspended(), "suspended")
    `resume`()
    check(!isSuspended(), "resumed")

    reader.shutdown(); reader.close()
    owner.shutdown(); owner.close()
    println("KOTLIN FLOW PASS")
}
