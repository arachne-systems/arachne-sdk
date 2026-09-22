use arachne_sdk::{Client, ClientConfig, JoinAdmissionStep, Network};
use std::{
    thread,
    time::{Duration, Instant},
};

// Fixed credentials keep the local example reproducible; applications should use
// private, unique random credentials and store them securely.
fn main() -> std::result::Result<(), Box<dyn std::error::Error>> {
    let mut owner = Client::open(ClientConfig {
        network: Network::Direct,
        secret: Some([0x11; 32]),
    })?;
    let mut receiver = Client::open(ClientConfig {
        network: Network::Direct,
        secret: Some([0x22; 32]),
    })?;

    let workspace = owner.create_workspace("Owner", Some("SDK protected receive"))?;
    let invitation = owner.issue_invitation()?;
    receiver.add_address_hint(
        invitation.peer,
        &invitation.address.replace("0.0.0.0:", "127.0.0.1:"),
    )?;

    let join = receiver.begin_join(&invitation.invitation, &invitation.checkpoint, "Receiver")?;
    let candidate = owner.stage_admission(join.endpoint, &join.admission_request)?;
    let admitted_owner = owner.adopt_admission(&candidate.snapshot)?;
    let reply = owner.retained_admission(join.endpoint, &join.admission_request)?;
    let candidate = receiver.stage_join(
        &reply.welcome,
        &[JoinAdmissionStep {
            commit: reply.commit,
            authorization: reply.authorization,
        }],
    )?;
    let admitted_receiver = receiver.adopt_join(&candidate.snapshot)?;
    if admitted_owner.epoch != admitted_receiver.epoch {
        return Err("members adopted different workspace epochs".into());
    }

    let receiver_endpoint = receiver.endpoint()?;
    owner.add_address_hint(
        receiver_endpoint.endpoint_key,
        &receiver_endpoint
            .bound_address
            .replace("0.0.0.0:", "127.0.0.1:"),
    )?;

    let revision = admitted_owner.epoch + 1;
    let topic = "streams/example";
    owner.install_workspace_policy(revision)?;
    receiver.install_workspace_policy(revision)?;
    receiver.set_interest(workspace.workspace, revision, topic, true)?;

    let interest_deadline = Instant::now() + Duration::from_secs(10);
    loop {
        if let Some(observation) = receiver.poll_interest()? {
            if !observation.admission.failed.is_empty() {
                return Err(format!(
                    "topic subscription failed: {:?}",
                    observation.admission.failed
                )
                .into());
            }
            break;
        }
        if Instant::now() >= interest_deadline {
            return Err("topic subscription did not settle before the deadline".into());
        }
        thread::sleep(Duration::from_millis(10));
    }

    let payload = br#"{"status":"protected demo"}"#.to_vec();
    let candidate = owner.stage_protected_publication(
        workspace.workspace,
        revision,
        topic,
        [1; 16],
        payload.clone(),
    )?;
    let delivery = owner.adopt_protected_publication(&candidate.snapshot)?;
    if !delivery.failed.is_empty() {
        return Err(format!("protected send failed: {:?}", delivery.failed).into());
    }

    let receive_deadline = Instant::now() + Duration::from_secs(10);
    let candidate = loop {
        if let Some(candidate) = receiver.poll_protected()? {
            break candidate;
        }
        if Instant::now() >= receive_deadline {
            return Err("protected publication did not arrive before the deadline".into());
        }
        thread::sleep(Duration::from_millis(10));
    };
    if receiver.workspace_state()?.durable {
        receiver.save_candidate(&candidate.snapshot)?;
    }
    let received = receiver.adopt_protected_reception(&candidate.snapshot)?;

    assert_eq!(received.workspace, workspace.workspace);
    assert_eq!(received.revision, revision);
    assert_eq!(received.topic, topic);
    assert_eq!(received.payload, payload);
    assert_eq!(received.endpoint, owner.endpoint()?.endpoint_key);

    owner.close()?;
    receiver.close()?;
    println!("received one protected publication over direct peer transport");
    Ok(())
}
