mod support;

// In-process API choreography only; admission material is not sent over a
// peer transport in this example.
// Fixed credentials are demo-only; applications should use private random values.
use arachne_sdk::{JoinAdmissionStep, Result};

fn main() -> Result<()> {
    let owner = support::open(0x33)?;
    let joiner = support::open(0x44)?;

    let workspace = owner.create_workspace("Owner", Some("SDK example".into()))?;
    // Register a reusable invitation link (no expiry), then adopt it to get
    // the bearer link. Core commits the candidate before adoption succeeds.
    let staged = owner.stage_invitation(0)?;
    let invitation = owner.adopt_invitation(&staged)?;
    let request = joiner.begin_join(
        &invitation.invitation,
        &invitation.checkpoint,
        "Joining member",
    )?;

    let candidate = owner.stage_admission(request.endpoint, &request.admission_request)?;
    let admitted_owner = owner.adopt_admission(&candidate)?;
    let reply = owner.retained_admission(request.endpoint, &request.admission_request)?;

    let candidate = joiner.stage_join(
        &reply.welcome,
        &[JoinAdmissionStep {
            commit: reply.commit,
            authorization: reply.authorization,
        }],
    )?;
    let admitted_joiner = joiner.adopt_join(&candidate)?;

    println!("workspace: {:?}", workspace.workspace);
    println!("owner members: {}", admitted_owner.member_count);
    println!("joiner epoch: {}", admitted_joiner.epoch);

    owner.close()?;
    joiner.close()?;
    Ok(())
}
