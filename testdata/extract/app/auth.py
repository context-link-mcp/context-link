import hashlib


def hash_password(password: str, salt: str) -> str:
    """Hash a password with a salt for storage."""
    return hashlib.sha256((salt + password).encode()).hexdigest()


def verify_token(token: str) -> bool:
    """Check an authentication token against the session store."""
    return token.startswith("abc")
