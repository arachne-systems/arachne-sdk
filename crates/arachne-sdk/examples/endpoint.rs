use arachne_sdk::{Client, ClientConfig, Network};

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let mut client = Client::open(ClientConfig {
        network: Network::Direct,
        secret: None,
    })?;
    let endpoint = client.endpoint()?;
    let workspace = client.create_workspace("SDK example", None)?;

    println!("endpoint: {}", hex(&endpoint.endpoint_key));
    println!("workspace: {}", hex(&workspace.workspace));
    client.close()?;
    Ok(())
}

fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|byte| format!("{byte:02x}")).collect()
}
