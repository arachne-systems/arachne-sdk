// Single-member, non-durable send-path example; it has no receiving peer.
// The fixed credential is demo-only; applications should use a private random credential.
use arachne_sdk::{Client, ClientConfig, Network, Result};

fn main() -> Result<()> {
    let mut client = Client::open(ClientConfig {
        network: Network::Direct,
        secret: Some([0x11; 32]),
    })?;
    let workspace = client.create_workspace("Publisher", Some("SDK example"))?;
    let revision = workspace.epoch + 1;
    let topic = "feeds/example";

    client.install_workspace_policy(revision)?;

    let candidate = client.stage_protected_publication(
        workspace.workspace,
        revision,
        topic,
        [1; 16],
        br#"{"status":"demo"}"#.to_vec(),
    )?;
    let delivery = client.adopt_protected_publication(&candidate.snapshot)?;
    println!("admitted recipients: {}", delivery.admitted.len());
    client.close()
}
