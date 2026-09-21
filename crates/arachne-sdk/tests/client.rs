use arachne_sdk::{Client, ClientConfig, Network, WorkspacePhase};

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
fn owner_can_install_workspace_policy_through_the_sdk_boundary() {
    let mut client = Client::open(ClientConfig {
        network: Network::Direct,
        secret: Some([8; 32]),
    })
    .unwrap();
    let workspace = client.create_workspace("Feed owner", Some("Feed test")).unwrap();

    client
        .install_workspace_policy(workspace.epoch + 1)
        .unwrap();
    let candidate = client
        .stage_protected_publication(
            workspace.workspace,
            workspace.epoch + 1,
            "feeds/catalog/v1",
            [1; 16],
            br#"{"version":1}"#.to_vec(),
        )
        .unwrap();
    let report = client
        .adopt_protected_publication(&candidate.snapshot)
        .unwrap();
    assert!(report.failed.is_empty());
    client.close().unwrap();
}
