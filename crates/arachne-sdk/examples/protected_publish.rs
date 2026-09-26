mod support;

// Single-member send-path example with in-memory records; it has no receiving peer.
// The fixed credential is demo-only; applications should use a private random credential.
use arachne_sdk::Result;

fn main() -> Result<()> {
    let client = support::open(0x11)?;
    let workspace = client.create_workspace("Publisher", Some("SDK example".into()))?;
    let revision = workspace.epoch + 1;
    let topic = "feeds/example";

    client.install_workspace_policy(revision)?;

    let candidate = client.stage_protected_publication(
        workspace.workspace,
        revision,
        topic,
        [1; 16].into(),
        br#"{"status":"demo"}"#.to_vec(),
    )?;
    let delivery = client.adopt_protected_publication(&candidate)?;
    println!("admitted recipients: {}", delivery.admitted.len());
    client.close()
}
