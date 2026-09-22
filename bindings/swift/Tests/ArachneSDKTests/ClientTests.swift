import XCTest
import Foundation
@testable import ArachneSDK

final class ClientTests: XCTestCase {
    func testOpenStateAndClose() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let path = directory.appendingPathComponent("records.db")
        let root = Data(repeating: 10, count: 32)
        let secret = Data(repeating: 7, count: 32)
        let client = try Client.open(secret: secret)
        let state = try client.workspaceState()
        XCTAssertEqual(state.phase, .empty)
        let workspace = try client.createWorkspace(displayName: "Swift smoke", workspaceName: "Swift SDK")
        try client.useServiceProfile()
        XCTAssertEqual(try client.memberRoster().members[0].kind, .service)
        try client.enableRecordStorage(path: path, root: root)
        try client.installWorkspacePolicy(revision: workspace.epoch + 1)
        let staged = try client.stageProtectedPublication(
            workspace: workspace.workspace,
            revision: workspace.epoch + 1,
            topic: "sdk/swift/smoke",
            id: Data(repeating: 1, count: 16),
            payload: Data("swift binding".utf8)
        )
        XCTAssertFalse(staged.snapshot.isEmpty)
        try client.saveCandidate(staged.snapshot)
        _ = try client.adoptProtectedPublication(snapshot: staged.snapshot)
        try client.enableObjectDelivery()
        try client.enableObjectDelivery()
        let current = try client.stageProtectedPublication(
            workspace: workspace.workspace,
            revision: workspace.epoch + 1,
            topic: "sdk/swift/current",
            id: Data(repeating: 2, count: 16),
            payload: Data("current Swift value".utf8),
            current: PublicationCurrent(
                selector: Data(repeating: 7, count: 32),
                replacementKey: Data(repeating: 8, count: 32),
                expiresAt: UInt64.max
            )
        )
        try client.saveCandidate(current.snapshot)
        _ = try client.adoptProtectedPublication(snapshot: current.snapshot)
        try client.close()
        XCTAssertThrowsError(try client.workspaceState())

        let restored = try Client.open(secret: secret)
        _ = try restored.restoreRecordStorage(path: path, root: root, workspace: workspace.workspace)
        XCTAssertTrue(try restored.workspaceState().durable)
        try restored.close()
    }

    func testProtectedReceive() throws {
        let owner = try Client.open(secret: Data(repeating: 0x31, count: 32))
        let receiver = try Client.open(secret: Data(repeating: 0x42, count: 32))
        defer { try? owner.close(); try? receiver.close() }

        let workspace = try owner.createWorkspace(displayName: "Owner")
        let invitation = try owner.issueInvitation()
        _ = try receiver.inspectInvitation(invitation: invitation.invitation, checkpoint: invitation.checkpoint)
        try receiver.addAddressHint(peer: invitation.peer,
                                    address: invitation.address.replacingOccurrences(of: "0.0.0.0:", with: "127.0.0.1:"))
        let join = try receiver.beginJoin(invitation: invitation.invitation,
                                          checkpoint: invitation.checkpoint,
                                          displayName: "Receiver")
        let admissionCandidate = try owner.stageAdmission(authenticatedEndpoint: join.endpoint,
                                                         request: join.admissionRequest)
        let admittedOwner = try owner.adoptAdmission(snapshot: admissionCandidate.snapshot)
        let reply = try owner.retainedAdmission(authenticatedEndpoint: join.endpoint,
                                               request: join.admissionRequest)
        let joinCandidate = try receiver.stageJoin(welcome: reply.welcome, commits: [
            JoinAdmissionStep(commit: reply.commit, authorization: reply.authorization)
        ])
        let admittedReceiver = try receiver.adoptJoin(snapshot: joinCandidate.snapshot)
        XCTAssertEqual(admittedOwner.epoch, admittedReceiver.epoch)
        XCTAssertEqual(try owner.memberRoster().members.count, 2)
        XCTAssertEqual(try owner.metrics().workspace, workspace.workspace)
        XCTAssertEqual(try receiver.connectivity().workspace, workspace.workspace)

        let endpoint = try receiver.endpoint()
        try owner.addAddressHint(peer: endpoint.endpointKey,
                                 address: endpoint.boundAddress.replacingOccurrences(of: "0.0.0.0:", with: "127.0.0.1:"))
        let revision = admittedOwner.epoch + 1
        let topic = "sdk/swift/receive"
        try owner.installWorkspacePolicy(revision: revision)
        try receiver.installWorkspacePolicy(revision: revision)
        try receiver.setInterest(workspace: workspace.workspace, revision: revision, topic: topic, subscribed: true)

        let interestDeadline = Date(timeIntervalSinceNow: 10)
        var observation = try receiver.pollInterest()
        while observation == nil {
            guard Date() < interestDeadline else { XCTFail("subscription did not settle before the deadline"); return }
            Thread.sleep(forTimeInterval: 0.01)
            observation = try receiver.pollInterest()
        }
        XCTAssertTrue(observation?.admission.failed.isEmpty == true)

        let payload = Data("protected Swift receive".utf8)
        let publication = try owner.stageProtectedPublication(workspace: workspace.workspace,
                                                              revision: revision,
                                                              topic: topic,
                                                              id: Data(repeating: 1, count: 16),
                                                              payload: payload)
        let delivery = try owner.adoptProtectedPublication(snapshot: publication.snapshot)
        XCTAssertTrue(delivery.failed.isEmpty)

        let receiveDeadline = Date(timeIntervalSinceNow: 10)
        var reception = try receiver.pollProtected()
        while reception == nil {
            guard Date() < receiveDeadline else { XCTFail("protected publication did not arrive before the deadline"); return }
            Thread.sleep(forTimeInterval: 0.01)
            reception = try receiver.pollProtected()
        }
        let received = try receiver.adoptProtectedReception(snapshot: reception!.snapshot)
        XCTAssertEqual(received.workspace, workspace.workspace)
        XCTAssertEqual(received.topic, topic)
        XCTAssertEqual(received.payload, payload)
        let ownerEndpoint = try owner.endpoint()
        XCTAssertEqual(received.endpoint, ownerEndpoint.endpointKey)
        XCTAssertNil(try receiver.pollRecoveredPublication())

        XCTAssertThrowsError(try owner.publish(workspace: workspace.workspace,
                                               revision: revision,
                                               topic: topic,
                                               payload: Data("basic Swift pubsub".utf8))) { error in
            XCTAssertTrue(String(describing: error).contains("unprotected publication is disabled"))
        }
        XCTAssertThrowsError(try receiver.poll()) { error in
            XCTAssertTrue(String(describing: error).contains("use poll_protected"))
        }
    }
}
