class UserService:
    def __init__(self, db):
        self.db = db

    def get_user(self, user_id):
        return self.db.query(user_id)

    def delete_user(self, user_id):
        self.db.delete(user_id)


def standalone_function():
    return 42


class AdminService(UserService):
    def promote(self, user_id):
        self.db.promote(user_id)


MAX_RETRIES = 3
