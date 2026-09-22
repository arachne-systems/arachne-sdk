use std::time::{SystemTime, UNIX_EPOCH};

use arachne_sdk::{Client, ClientConfig, MemberKind, Network, WorkspacePhase};

#[test]
fn opens_a_native_client_through_the_sdk_boundary() {
    let mut client = Client::open(ClientConfig {
        network: Network::Direct,
        secret: Some([7; 32]),
    })
    .unwrap();

    let state = client.workspace_state().unwrap();
    assert_eq!(state.phase, WorkspacePhase::Empty);
    assert!(!state.workspace_ready);

    client.close().unwrap();
}

#[test]
fn protected_publication_can_be_saved_adopted_and_restored() {
    let directory = std::env::temp_dir().join(format!(
        "arachne-sdk-{}-{}",
        std::process::id(),
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos()
    ));
    std::fs::create_dir(&directory).unwrap();
    let path = directory.join("records.db");

    let config = ClientConfig {
        network: Network::Direct,
        secret: Some([9; 32]),
    };
    let root = [10; 32];
    let mut client = Client::open(config.clone()).unwrap();
    let workspace = client
        .create_workspace("Feed owner", Some("Feed test"))
        .unwrap();
    client.use_service_profile().unwrap();
    assert_eq!(
        client.member_roster().unwrap().members[0].kind,
        MemberKind::Service
    );
    client.enable_record_storage(&path, &root).unwrap();
    client
        .install_workspace_policy(workspace.epoch + 1)
        .unwrap();
    let candidate = client
        .stage_protected_publication(
            workspace.workspace,
            workspace.epoch + 1,
            "feeds/catalog/v1",
            [2; 16],
            br#"{"version":1}"#.to_vec(),
        )
        .unwrap();

    let mut altered = candidate.snapshot.clone();
    altered[0] ^= 1;
    assert!(client.save_candidate(&altered).is_err());
    assert!(
        client
            .adopt_protected_publication(&candidate.snapshot)
            .is_err()
    );
    client.save_candidate(&candidate.snapshot).unwrap();
    assert!(
        client
            .adopt_protected_publication(&candidate.snapshot)
            .unwrap()
            .failed
            .is_empty()
    );
    client.close().unwrap();

    let mut restored = Client::open(config).unwrap();
    restored
        .restore_record_storage(&path, &root, workspace.workspace)
        .unwrap();
    restored.use_service_profile().unwrap();
    let state = restored.workspace_state().unwrap();
    assert_eq!(state.workspace, Some(workspace.workspace));
    assert!(state.durable);
    assert_eq!(
        restored.member_roster().unwrap().members[0].kind,
        MemberKind::Service
    );
    restored.close().unwrap();
    std::fs::remove_dir_all(directory).unwrap();
}
