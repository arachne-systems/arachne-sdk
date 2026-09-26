use std::time::{SystemTime, UNIX_EPOCH};

use arachne_sdk::{
    Client, Context, ErrorCode, Limits, MemberKind, Network, PowerProfile, RestoredWorkspace,
    StorageConfig, WorkspacePhase, api_version, default_client_config,
};

#[test]
fn opens_a_native_client_through_the_sdk_boundary() {
    let limits = Limits::default().with_max_sessions(2);
    let context = Context::owned(limits, PowerProfile::Normal, 1).unwrap();
    let client = Client::open_in(context, default_client_config(Network::Direct)).unwrap();

    assert!(api_version() >= 6);
    assert_eq!(client.capabilities().unwrap().limits, limits);
    let state = client.workspace_state().unwrap();
    assert_eq!(state.phase, WorkspacePhase::Empty);
    assert!(!state.workspace_ready);
    client.close().unwrap();
}

#[test]
fn protected_publication_is_committed_by_core_and_restored() {
    let directory = std::env::temp_dir().join(format!(
        "arachne-sdk-{}-{}",
        std::process::id(),
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos()
    ));
    std::fs::create_dir(&directory).unwrap();
    let mut config = default_client_config(Network::Direct);
    config.secret = Some(vec![9; 32]);
    config.storage = Some(
        StorageConfig::open_sqlite(directory.to_str().unwrap().to_owned(), vec![10; 32]).unwrap(),
    );

    let client = Client::open(config.clone()).unwrap();
    let workspace = client
        .create_workspace("Feed owner", Some("Feed test".into()))
        .unwrap();
    client.use_service_profile().unwrap();
    assert_eq!(
        client.member_roster().unwrap().members[0].kind,
        MemberKind::Service
    );
    client
        .install_workspace_policy(workspace.epoch + 1)
        .unwrap();
    let candidate = client
        .stage_protected_publication(
            workspace.workspace,
            workspace.epoch + 1,
            "feeds/catalog/v1",
            [2; 16].into(),
            br#"{"version":1}"#.to_vec(),
        )
        .unwrap();
    assert_eq!(candidate.workspace(), workspace.workspace);

    let other = Client::open(default_client_config(Network::Direct)).unwrap();
    assert_eq!(
        other
            .adopt_protected_publication(&candidate)
            .unwrap_err()
            .code(),
        ErrorCode::WrongState
    );
    other.close().unwrap();
    assert!(
        client
            .adopt_protected_publication(&candidate)
            .unwrap()
            .failed
            .is_empty()
    );
    assert_eq!(
        client
            .adopt_protected_publication(&candidate)
            .unwrap_err()
            .code(),
        ErrorCode::CandidateStale
    );
    assert!(!candidate.discard().unwrap());
    let anchor = client.record_freshness().unwrap();
    client.close().unwrap();

    let restored = Client::open(config).unwrap();
    assert!(matches!(
        restored
            .restore_workspace(workspace.workspace, Some(anchor))
            .unwrap(),
        RestoredWorkspace::Active(_)
    ));
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

#[test]
fn moq_build_feature_controls_general_stream_metrics() {
    let client = Client::open(default_client_config(Network::Direct)).unwrap();
    #[cfg(not(feature = "moq"))]
    assert_eq!(
        client.moq_metrics().unwrap_err().code(),
        ErrorCode::Unsupported
    );
    #[cfg(feature = "moq")]
    {
        let metrics = client
            .moq_metrics()
            .expect("SDK moq feature enables Core streaming");
        assert_eq!(metrics.sessions_total, 0);
        assert_eq!(metrics.sessions_active, 0);
        assert_eq!(metrics.packets_sent, 0);
        assert_eq!(metrics.packets_received, 0);
        assert_eq!(metrics.rejected_sessions, 0);
    }
    client.close().unwrap();
}
