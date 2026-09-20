use arachne_sdk::{Client, ClientConfig, Network};

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let mut client = Client::open(ClientConfig {
        network: Network::Direct,
        secret: Some([7; 32]),
    })?;
    let endpoint = client.endpoint()?;

    println!("endpoint: {}", hex(&endpoint.endpoint_key));
    client.close()?;
    Ok(())
}

fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|byte| format!("{byte:02x}")).collect()
}
