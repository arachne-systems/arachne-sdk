mod support;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let client = support::open(0x01)?;
    let endpoint = client.endpoint()?;
    let workspace = client.create_workspace("SDK example", None)?;

    println!("endpoint: {}", endpoint.endpoint_key);
    println!("workspace: {}", workspace.workspace);
    client.close()?;
    Ok(())
}
