import Foundation
import Combine

protocol Repository {
    associatedtype Entity
    func findById(_ id: UUID) async throws -> Entity?
    func findAll() async throws -> [Entity]
    func save(_ entity: Entity) async throws -> Entity
    func delete(_ id: UUID) async throws
}

struct User: Codable, Identifiable {
    let id: UUID
    var name: String
    var email: String
}

enum UserRole: String, CaseIterable {
    case admin = "admin"
    case editor = "editor"
    case viewer = "viewer"
}

typealias UserCompletion = (Result<User, Error>) -> Void

class UserRepository: Repository {
    typealias Entity = User

    private var store: [User] = []

    func findById(_ id: UUID) async throws -> User? {
        store.first { $0.id == id }
    }

    func findAll() async throws -> [User] {
        store
    }

    func save(_ entity: User) async throws -> User {
        store.removeAll { $0.id == entity.id }
        store.append(entity)
        return entity
    }

    func delete(_ id: UUID) async throws {
        store.removeAll { $0.id == id }
    }
}

func createUser(name: String, email: String) async throws -> User {
    let user = User(id: UUID(), name: name, email: email)
    return try await UserRepository().save(user)
}
