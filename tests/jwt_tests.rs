use hbbs::jwt;
use std::sync::Once;

fn configure() {
    static INIT: Once = Once::new();
    INIT.call_once(|| std::env::set_var("RUSTDESK_API_JWT_KEY", "test-only-key-of-at-least-32-bytes"));
}

#[test]
fn valid_token_roundtrip() {
    configure();
    let token = jwt::generate_token(1, 3600).unwrap();
    assert!(jwt::verify_token(&token).is_ok());
}

#[test]
fn reject_expired_missing_identity_and_malformed_tokens() {
    configure();
    for token in [jwt::generate_token(1, -1).unwrap(), jwt::generate_token(0, 60).unwrap(), String::new(), "a.b.c".into()] {
        assert!(jwt::verify_token(&token).is_err());
    }
}
