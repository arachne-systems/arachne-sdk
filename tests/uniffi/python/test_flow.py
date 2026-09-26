"""Two-client flow over localhost for the generated Python binding.

invitation -> join -> publish -> recovery -> ack; interest -> publish ->
protected receive -> reject; presence, metrics, deadline, suspend/resume;
candidate misuse. Run by scripts/uniffi-smoke.sh.
"""

from datetime import timedelta
from pathlib import Path
import tempfile
import sys
import time

from arachne_generated import (
    ApiError,
    Client,
    Context,
    PowerProfile,
    StorageConfig,
    default_client_config,
    default_limits,
    ErrorCode,
    InvitationKind,
    JoinAdmissionStep,
    Network,
    RecoveryRangeRequest,
    RecoveryRangeStatus,
    RecoveryStage,
    api_error_code,
)

TOPIC = "streams/uniffi"
stores = tempfile.TemporaryDirectory(prefix="arachne-python-flow-")
context = Context.owned(default_limits(), PowerProfile.NORMAL, 2)


def check(ok, what):
    if not ok:
        print(f"FAIL: {what}")
        sys.exit(1)
    print(f"ok: {what}")


def until(what, step):
    deadline = time.monotonic() + 15
    while True:
        value = step()
        if value is not None:
            return value
        if time.monotonic() > deadline:
            check(False, f"{what} timed out")
        time.sleep(0.01)


def local(address):
    return address.replace("0.0.0.0:", "127.0.0.1:")


def open_client(seed):
    config = default_client_config(Network.DIRECT)
    config.secret = bytes([seed]) * 32
    directory = Path(stores.name) / str(seed)
    directory.mkdir()
    config.storage = StorageConfig.open_sqlite(str(directory), bytes([seed + 1]) * 32)
    return Client.open_in(context, config)


owner = open_client(0x61)
reader = open_client(0x62)
created = owner.create_workspace("Owner", "Python flow")

# Invitations (and candidate misuse).
staged = owner.stage_invitation_of(0, InvitationKind.REUSABLE)
other = open_client(0x63)
try:
    other.adopt_invitation(staged)
    check(False, "adopt on another client must raise")
except ApiError as e:
    check(api_error_code(e) == ErrorCode.WRONG_STATE, f"candidate bound to its client: code {api_error_code(e).value}")
other.close()
invitation = owner.adopt_invitation(staged)
try:
    owner.adopt_invitation(staged)
    check(False, "second adopt must raise")
except ApiError as e:
    check(api_error_code(e) == ErrorCode.CANDIDATE_STALE, f"candidate single use: code {api_error_code(e).value}")
details = reader.inspect_invitation(invitation.invitation, invitation.checkpoint)
check(details.workspace == created.workspace, f"inspect_invitation workspace = {details.workspace[:16]}...")

# Join and admission.
reader.add_address_hint(invitation.peer, local(invitation.address))
join = reader.begin_join(invitation.invitation, invitation.checkpoint, "Reader")
owner_view = owner.adopt_admission(owner.stage_admission(join.endpoint, join.admission_request))
reply = owner.retained_admission(join.endpoint, join.admission_request)
reader_view = reader.adopt_join(
    reader.stage_join(reply.welcome, [JoinAdmissionStep(commit=reply.commit, authorization=reply.authorization)])
)
check(owner_view.epoch == reader_view.epoch and reader_view.member_count == 2,
      f"joined at epoch {reader_view.epoch} with {reader_view.member_count} members")
me = reader.endpoint()
owner.add_address_hint(me.endpoint_key, local(me.bound_address))

# Publication the reader is not subscribed to, then recovery.
revision = owner_view.epoch + 1
owner.install_workspace_policy(revision)
reader.install_workspace_policy(revision)
owner.adopt_protected_publication(
    owner.stage_protected_publication(created.workspace, revision, TOPIC, "01" * 16, b"first")
)
author = next(m.id for m in owner.member_roster().members if m.self_member)
owner_endpoint = owner.endpoint().endpoint_key
reader.fetch_recovery_range(
    RecoveryRangeRequest(peer=owner_endpoint, author=author, revision=revision, topics=[TOPIC], after=0, through=1),
)


def poll_range():
    owner.poll_control()
    return reader.poll_recovery_range()


ready = until("recovery range", poll_range)
check(isinstance(ready, RecoveryRangeStatus.READY), f"recovery range ready: {ready[0].packet_count} packet(s)")
stage = reader.stage_recovery_range(0)
check(isinstance(stage, RecoveryStage.CANDIDATE), "stage_recovery_range gave a candidate")
adoption = reader.adopt_recovery(stage[0])
check(adoption.recovered_publications == 1, f"recovered {adoption.recovered_publications} publication")
recovered = reader.poll_pending_object()
check(recovered is not None and recovered.payload == b"first", "recovered object is pending")
check(recovered.epoch == owner_view.epoch and not recovered.from_losing_branch,
      "recovery keeps the authenticated author epoch and branch status")
reader.adopt_protected_reception(reader.stage_object_acknowledgement(recovered))
check(reader.poll_pending_object() is None, "acknowledged object left the inbox")

# Interest, live protected receive, rejection.
reader.set_interest(created.workspace, revision, TOPIC, True)
observed = until("interest", reader.poll_interest)
check(observed.subscribed, "interest settled")
report = owner.adopt_protected_publication(
    owner.stage_protected_publication(created.workspace, revision, TOPIC, "02" * 16, b"second")
)
check(not report.failed, f"publication sent, admitted {len(report.admitted)}")
reception = until("protected receive", reader.poll_protected)
reader.adopt_protected_reception(reception)
received = reader.poll_pending_object()
check(received.payload == b"second" and received.endpoint == owner_endpoint, "received the live publication")
check(received.epoch == owner_view.epoch and not received.from_losing_branch,
      "live receive exposes the authenticated author epoch and branch status")
reader.adopt_protected_reception(reader.stage_object_rejection(received))
check(reader.poll_pending_object() is None, "rejected object left the inbox")

# Presence, metrics, deadline, suspend/resume.
round_ = owner.poll_presence(False)
check(round_.response_errors <= 1, f"presence round: {round_.response_errors} error(s)")
check(reader.metrics().workspace == created.workspace, "metrics workspace")
reader.set_deadline(timedelta(seconds=5))
context.suspend()
check(context.is_suspended(), "suspended")
context.resume()
check(not context.is_suspended(), "resumed")

reader.close()
owner.close()
stores.cleanup()
print("PYTHON FLOW PASS")
