use arachne_sdk::{PublicationMode, PublicationOptions, default_publication_options};

#[test]
fn publication_defaults_come_from_core() {
    let options = default_publication_options();
    assert_eq!(options, PublicationOptions::default());
    assert!(options.recipients.is_empty());
    assert_eq!(options.mode, PublicationMode::Critical);
}
