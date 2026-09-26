use std::sync::Arc;

use arachne_sdk::{Client, MemoryProvider, Network, Result, StorageConfig, default_client_config};

/// Examples use temporary records in memory and fixed, local-only credentials.
/// A deployed host configures SQLite storage and its own private keys.
pub fn open(seed: u8) -> Result<Arc<Client>> {
    let mut config = default_client_config(Network::Direct);
    config.secret = Some(vec![seed; 32]);
    config.storage = Some(Arc::new(StorageConfig::memory(&MemoryProvider::default())));
    Client::open(config)
}
