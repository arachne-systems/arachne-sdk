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
