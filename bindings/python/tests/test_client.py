import tempfile
import time
import unittest
from pathlib import Path

from arachne_sdk import (
    Client,
    JoinAdmissionStep,
    MemberKind,
    Network,
    PublicationCurrent,
    WorkspacePhase,
)


class ClientSmokeTests(unittest.TestCase):
    def test_open_state_and_close(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "records.db"
            root = bytes([10]) * 32
            with Client.open(Network.DIRECT, secret=bytes([7]) * 32) as client:
                state = client.workspace_state()
                self.assertEqual(state.phase, WorkspacePhase.EMPTY)
                workspace = client.create_workspace("Python smoke", "Python SDK")
                client.use_service_profile()
                self.assertEqual(client.member_roster().members[0].kind, MemberKind.SERVICE)
                client.enable_record_storage(path, root)
                client.install_workspace_policy(workspace.epoch + 1)
                candidate = client.stage_protected_publication(
                    workspace.workspace,
                    workspace.epoch + 1,
                    "sdk/python/smoke",
                    bytes(range(16)),
                    b"python binding",
                )
                self.assertTrue(candidate.snapshot)
                client.save_candidate(candidate.snapshot)
                client.adopt_protected_publication(candidate.snapshot)
                client.enable_object_delivery()
                client.enable_object_delivery()
                current = client.stage_protected_publication(
                    workspace.workspace,
                    workspace.epoch + 1,
                    "sdk/python/current",
                    bytes([2]) * 16,
                    b"current Python value",
                    current=PublicationCurrent(
                        selector=bytes([7]) * 32,
                        replacement_key=bytes([8]) * 32,
                        expires_at=2**64 - 1,
                    ),
                )
                client.save_candidate(current.snapshot)
                client.adopt_protected_publication(current.snapshot)

            with self.assertRaisesRegex(RuntimeError, "closed"):
                client.workspace_state()

            with Client.open(Network.DIRECT, secret=bytes([7]) * 32) as restored:
                result = restored.restore_record_storage(path, root, workspace.workspace)
                self.assertEqual(result.workspace, workspace.workspace)
                self.assertTrue(restored.workspace_state().durable)

    def test_protected_receive(self):
        with Client.open(Network.DIRECT, secret=bytes([0x31]) * 32) as owner, Client.open(
            Network.DIRECT, secret=bytes([0x42]) * 32
        ) as receiver:
            workspace = owner.create_workspace("Owner")
            invitation = owner.issue_invitation()
            receiver.inspect_invitation(invitation.invitation, invitation.checkpoint)
            receiver.add_address_hint(
                invitation.peer, invitation.address.replace("0.0.0.0:", "127.0.0.1:", 1)
            )
            join = receiver.begin_join(
                invitation.invitation, invitation.checkpoint, "Receiver"
            )
            candidate = owner.stage_admission(join.endpoint, join.admission_request)
            admitted_owner = owner.adopt_admission(candidate.snapshot)
            reply = owner.retained_admission(join.endpoint, join.admission_request)
            candidate = receiver.stage_join(
                reply.welcome,
                (JoinAdmissionStep(reply.commit, reply.authorization),),
            )
            admitted_receiver = receiver.adopt_join(candidate.snapshot)
            self.assertEqual(admitted_owner.epoch, admitted_receiver.epoch)
            self.assertEqual(len(owner.member_roster().members), 2)
            self.assertEqual(owner.metrics().workspace, workspace.workspace)
            self.assertEqual(receiver.connectivity().workspace, workspace.workspace)

            endpoint = receiver.endpoint()
            owner.add_address_hint(
                endpoint.endpoint_key,
                endpoint.bound_address.replace("0.0.0.0:", "127.0.0.1:", 1),
            )
            revision = admitted_owner.epoch + 1
            topic = "sdk/python/receive"
            owner.install_workspace_policy(revision)
            receiver.install_workspace_policy(revision)
            receiver.set_interest(workspace.workspace, revision, topic, True)
            deadline = time.monotonic() + 10
            observation = receiver.poll_interest()
            while observation is None:
                if time.monotonic() >= deadline:
                    self.fail("subscription did not settle before the deadline")
                time.sleep(0.01)
                observation = receiver.poll_interest()
            self.assertFalse(observation.admission.failed)

            payload = b"protected Python receive"
            candidate = owner.stage_protected_publication(
                workspace.workspace, revision, topic, bytes([1]) * 16, payload
            )
            self.assertFalse(owner.adopt_protected_publication(candidate.snapshot).failed)
            deadline = time.monotonic() + 10
            candidate = receiver.poll_protected()
            while candidate is None:
                if time.monotonic() >= deadline:
                    self.fail("protected publication did not arrive before the deadline")
                time.sleep(0.01)
                candidate = receiver.poll_protected()

            received = receiver.adopt_protected_reception(candidate.snapshot)
            self.assertEqual(received.workspace, workspace.workspace)
            self.assertEqual(received.topic, topic)
            self.assertEqual(received.payload, payload)
            self.assertEqual(received.endpoint, owner.endpoint().endpoint_key)
            self.assertIsNone(receiver.poll_recovered_publication())

            with self.assertRaisesRegex(RuntimeError, "unprotected publication is disabled"):
                owner.publish(workspace.workspace, revision, topic, b"basic Python pubsub")
            with self.assertRaisesRegex(RuntimeError, "use poll_protected"):
                receiver.poll()


if __name__ == "__main__":
    unittest.main()
