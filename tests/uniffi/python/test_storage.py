"""Core storage and owned-context behavior through the generated SDK."""

import tempfile

from arachne_generated import (
    ApiError,
    Client,
    ClientConfig,
    Context,
    ErrorCode,
    Limits,
    Network,
    PowerProfile,
    Phase,
    RestoredWorkspace,
    StorageConfig,
    TransportOptions,
    api_error_code,
)


def config(directory):
    return ClientConfig(
        network=Network.DIRECT,
        secret=bytes([0x41]) * 32,
        transport=TransportOptions(relay=None, public_lookup=None, timeouts=None, deadline=None),
        storage=StorageConfig.open_sqlite(directory, bytes([0x42]) * 32),
    )


with tempfile.TemporaryDirectory(prefix="arachne-sdk-python-storage-") as directory:
    context = Context.owned(Limits(max_sessions=2, max_overlay_paths=10), PowerProfile.NORMAL, 1)
    other_context = Context.owned(Limits(max_sessions=1, max_overlay_paths=5), PowerProfile.NORMAL, 1)
    context.suspend()
    assert context.is_suspended() and not other_context.is_suspended()
    context.resume()
    assert not context.is_suspended()

    try:
        StorageConfig.open_sqlite(directory, bytes(31))
        raise AssertionError("short storage root was accepted")
    except ApiError as error:
        assert api_error_code(error) == ErrorCode.INVALID_INPUT

    client = Client.open_in(context, config(directory))
    workspace = client.create_workspace("Python owner", "Storage flow")
    assert client.drive_workspace().activity.phase == Phase.ACTIVE
    assert client.poll_membership_update() is None
    assert client.nearby_endpoints() == []
    nearby = client.nearby_workspaces()
    assert nearby.workspaces == [] and nearby.endpoints_checked == 0

    discarded = client.stage_workspace_name("Discard this name")
    assert discarded.workspace() == workspace.workspace
    assert discarded.discard() and not discarded.discard()
    renamed = client.adopt_admission(client.stage_workspace_name("Saved name"))
    assert renamed.workspace == workspace.workspace
    revision = renamed.epoch + 1
    client.install_workspace_policy(revision)
    candidate = client.stage_protected_publication(
        workspace.workspace, revision, "sdk/storage", "23" * 16, b"saved by Core"
    )
    assert candidate.workspace() == workspace.workspace
    report = client.adopt_protected_publication(candidate)
    assert not report.failed
    assert client.workspace_state().durable
    client.close()

    restored = Client.open_in(context, config(directory))
    restored.restore_workspace(workspace.workspace, None)
    state = restored.workspace_state()
    assert state.durable and state.workspace == workspace.workspace and state.workspace_ready
    removed = restored.adopt_removal(restored.stage_solo_leave())
    assert removed.workspace == workspace.workspace
    restored.close()

    departed = Client.open_in(context, config(directory))
    tombstone = departed.restore_workspace(workspace.workspace, None)
    assert isinstance(tombstone, RestoredWorkspace.REMOVED)
    assert tombstone[0].workspace == workspace.workspace
    try:
        departed.workspace_state()
        raise AssertionError("a restored removal must close the session")
    except ApiError as error:
        assert api_error_code(error) == ErrorCode.CLOSED
    departed.close()

print("PYTHON STORAGE PASS")
