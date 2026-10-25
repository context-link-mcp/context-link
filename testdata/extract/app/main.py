from fastapi import APIRouter, Depends

from app.auth import verify_token

router = APIRouter(prefix="/api")


@router.post("/login")
def login(body: dict):
    """Authenticate a user and return a session token."""
    return {"token": "abc"}


@router.get("/users/{user_id}")
def get_user(user_id: int, token: str = Depends(verify_token)):
    """Return a user record; requires a valid token."""
    return {"id": user_id}


@router.get("")
def list_routes():
    return []
