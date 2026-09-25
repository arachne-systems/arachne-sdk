// In-process API choreography only; admission material is not sent over a
// peer transport in this example.
// Fixed credentials are demo-only; applications should use private random values.
use arachne_sdk::{Client, ClientConfig, JoinAdmissionStep, Network, Result, TransportOptions};

fn main() -> Result<()> {
    let owner = Client::open(ClientConfig {
        network: Network::Direct,
        secret: Some([0x33; 32]),
        transport: TransportOptions::default(),
    })?;
    let joiner = Client::open(ClientConfig {
        network: Network::Direct,
        secret: Some([0x44; 32]),
        transport: TransportOptions::default(),
    })?;

    let workspace = owner.create_workspace("Owner", Some("SDK example"))?;
    // Register a reusable invitation link (no expiry), then adopt it to get
    // the bearer link. A durable owner saves the candidate before adopting.
    let staged = owner.stage_invitation(0)?;
    let invitation = owner.adopt_invitation(&staged.snapshot)?;
    let request = joiner.begin_join(
        &invitation.invitation,
        &invitation.checkpoint,
        "Joining member",
    )?;

    let candidate = owner.stage_admission(request.endpoint, &request.admission_request)?;
    let admitted_owner = owner.adopt_admission(&candidate.snapshot)?;
    let reply = owner.retained_admission(request.endpoint, &request.admission_request)?;

    let candidate = joiner.stage_join(
        &reply.welcome,
        &[JoinAdmissionStep {
            commit: reply.commit,
            authorization: reply.authorization,
        }],
    )?;
    let admitted_joiner = joiner.adopt_join(&candidate.snapshot)?;

    println!("workspace: {:?}", workspace.workspace);
    println!("owner members: {}", admitted_owner.member_count);
    println!("joiner epoch: {}", admitted_joiner.epoch);

    owner.close()?;
    joiner.close()?;
    Ok(())
}
