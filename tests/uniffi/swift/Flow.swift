// Two-client flow over localhost for the generated Swift binding. Called
// from main.swift.
import Foundation
import ArachneSDK

private let topic = "streams/uniffi"

private func until<T>(_ what: String, _ step: () throws -> T?) rethrows -> T {
    let deadline = Date().addingTimeInterval(15)
    while true {
        if let value = try step() { return value }
        if Date() > deadline { check(false, "\(what) timed out") }
        Thread.sleep(forTimeInterval: 0.01)
    }
}

private func local(_ address: String) -> String {
    address.replacingOccurrences(of: "0.0.0.0:", with: "127.0.0.1:")
}

private func openClient(_ context: Context, _ stores: URL, _ seed: UInt8) throws -> Client {
    let directory = stores.appendingPathComponent(String(seed))
    try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
    var config = defaultClientConfig(network: .direct)
    config.secret = Data(repeating: seed, count: 32)
    config.storage = try StorageConfig.openSqlite(directory: directory.path, root: Data(repeating: seed + 1, count: 32))
    return try Client.openIn(context: context, config: config)
}

func runFlow() throws {
    let stores = FileManager.default.temporaryDirectory.appendingPathComponent("arachne-swift-" + UUID().uuidString)
    defer { try? FileManager.default.removeItem(at: stores) }
    let context = try Context.owned(limits: defaultLimits(), power: .normal, workers: 2)
    let owner = try openClient(context, stores, 0x81)
    let reader = try openClient(context, stores, 0x82)
    let created = try owner.createWorkspace(displayName: "Owner", workspaceName: "Swift flow")

    // Invitations (and candidate misuse).
    let staged = try owner.stageInvitationOf(expiresAt: 0, kind: .reusable)
    let other = try openClient(context, stores, 0x83)
    do {
        _ = try other.adoptInvitation(candidate: staged)
        check(false, "adopt on another client must throw")
    } catch let e as ApiError {
        check(apiErrorCode(error: e) == .wrongState, "candidate bound to its client: code \(apiErrorCode(error: e).rawValue)")
    }
    try other.close()
    let invitation = try owner.adoptInvitation(candidate: staged)
    do {
        _ = try owner.adoptInvitation(candidate: staged)
        check(false, "second adopt must throw")
    } catch let e as ApiError {
        check(apiErrorCode(error: e) == .candidateStale, "candidate single use: code \(apiErrorCode(error: e).rawValue)")
    }
    let details = try reader.inspectInvitation(invitation: invitation.invitation, checkpoint: invitation.checkpoint)
    check(details.workspace == created.workspace, "inspect_invitation workspace = \(details.workspace.prefix(16))...")

    // Join and admission.
    try reader.addAddressHint(peer: invitation.peer, address: local(invitation.address))
    let join = try reader.beginJoin(invitation: invitation.invitation, checkpoint: invitation.checkpoint,
                                    displayName: "Reader")
    let ownerView = try owner.adoptAdmission(
        candidate: try owner.stageAdmission(authenticatedEndpoint: join.endpoint, request: join.admissionRequest))
    let reply = try owner.retainedAdmission(authenticatedEndpoint: join.endpoint, request: join.admissionRequest)
    let readerView = try reader.adoptJoin(candidate: try reader.stageJoin(
        welcome: reply.welcome, commits: [JoinAdmissionStep(commit: reply.commit, authorization: reply.authorization)]))
    check(ownerView.epoch == readerView.epoch && readerView.memberCount == 2,
          "joined at epoch \(readerView.epoch) with \(readerView.memberCount) members")
    let me = try reader.endpoint()
    try owner.addAddressHint(peer: me.endpointKey, address: local(me.boundAddress))

    // Publication the reader is not subscribed to, then recovery.
    let revision = ownerView.epoch + 1
    try owner.installWorkspacePolicy(revision: revision)
    try reader.installWorkspacePolicy(revision: revision)
    _ = try owner.adoptProtectedPublication(candidate: try owner.stageProtectedPublication(
        workspace: created.workspace, revision: revision, topic: topic,
        id: String(repeating: "01", count: 16), payload: Data("first".utf8)))
    let author = try owner.memberRoster().members.first { $0.selfMember }!.id
    let ownerEndpoint = try owner.endpoint().endpointKey
    _ = try reader.fetchRecoveryRange(
        request: RecoveryRangeRequest(peer: ownerEndpoint, author: author, revision: revision,
                                      topics: [topic], after: 0, through: 1))
    let ready = try until("recovery range") { () throws -> RecoveryRangeStatus? in
        _ = try owner.pollControl()
        return try reader.pollRecoveryRange()
    }
    guard case .ready(let range) = ready else { check(false, "range not ready: \(ready)"); return }
    check(range.packetCount == 1, "recovery range ready: \(range.packetCount) packet(s)")
    guard case .candidate(let candidate) = try reader.stageRecoveryRange(retainUntil: 0) else {
        check(false, "no recovery candidate"); return
    }
    let adoption = try reader.adoptRecovery(candidate: candidate)
    check(adoption.recoveredPublications == 1, "recovered \(adoption.recoveredPublications) publication")
    let recovered = try reader.pollPendingObject()
    check(recovered?.payload == Data("first".utf8), "recovered object is pending")
    try reader.adoptProtectedReception(candidate: try reader.stageObjectAcknowledgement(object: recovered!))
    check(try reader.pollPendingObject() == nil, "acknowledged object left the inbox")

    // Interest, live protected receive, rejection.
    try reader.setInterest(workspace: created.workspace, revision: revision, topic: topic, subscribed: true)
    let observed = try until("interest") { try reader.pollInterest() }
    check(observed.subscribed, "interest settled")
    let report = try owner.adoptProtectedPublication(candidate: try owner.stageProtectedPublication(
        workspace: created.workspace, revision: revision, topic: topic,
        id: String(repeating: "02", count: 16), payload: Data("second".utf8)))
    check(report.failed.isEmpty, "publication sent")
    let reception = try until("protected receive") { try reader.pollProtected() }
    try reader.adoptProtectedReception(candidate: reception)
    let received = try reader.pollPendingObject()!
    check(received.payload == Data("second".utf8) && received.endpoint == ownerEndpoint, "received the live publication")
    try reader.adoptProtectedReception(candidate: try reader.stageObjectRejection(object: received))
    check(try reader.pollPendingObject() == nil, "rejected object left the inbox")

    // Presence, metrics, deadline, suspend/resume.
    let round = try owner.pollPresence(announce: false)
    check(round.responseErrors <= 1, "presence round: \(round.responseErrors) error(s)")
    check(try reader.metrics().workspace == created.workspace, "metrics workspace")
    reader.setDeadline(deadline: 5)
    try context.suspend()
    check(context.isSuspended(), "suspended")
    try context.resume()
    check(!context.isSuspended(), "resumed")

    try reader.close()
    try owner.close()
    print("SWIFT FLOW PASS")
}
