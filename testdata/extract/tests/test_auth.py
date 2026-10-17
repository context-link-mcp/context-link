from app.auth import verify_token


def test_verify_token_accepts_session_token():
    assert verify_token("abc123")
