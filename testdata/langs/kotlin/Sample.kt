package com.example.sample

import java.util.UUID
import kotlin.collections.List

interface Repository<T> {
    fun findById(id: UUID): T?
    fun findAll(): List<T>
    fun save(entity: T): T
    fun delete(id: UUID)
}

data class User(
    val id: UUID,
    val name: String,
    val email: String
)

enum class UserRole {
    ADMIN,
    EDITOR,
    VIEWER
}

object UserCache {
    private val cache = mutableMapOf<UUID, User>()

    fun get(id: UUID): User? = cache[id]
    fun put(user: User) {
        cache[user.id] = user
    }
}

class UserRepository : Repository<User> {
    private val store = mutableListOf<User>()

    override fun findById(id: UUID): User? =
        store.find { it.id == id }

    override fun findAll(): List<User> = store.toList()

    override fun save(entity: User): User {
        store.removeIf { it.id == entity.id }
        store.add(entity)
        UserCache.put(entity)
        return entity
    }

    override fun delete(id: UUID) {
        store.removeIf { it.id == id }
    }
}

fun createUser(name: String, email: String): User {
    val user = User(UUID.randomUUID(), name, email)
    return UserRepository().save(user)
}
