package com.example.web

import org.springframework.web.bind.annotation.*

// A controller with a class prefix: every route starts with /api/users.
@RestController
@RequestMapping("/api/users")
class UserController(private val users: UserRepository) {

    @GetMapping
    fun list(): List<User> = users.findAll()

    @GetMapping("/{id:\\d+}")
    fun get(@PathVariable id: Long): User = users.find(id)

    // An overload of get: the route links to this one, not the first.
    @GetMapping(value = ["/{id}/avatar"], produces = ["image/png"])
    fun get(@PathVariable id: Long, @RequestParam thumb: Boolean): ByteArray = ByteArray(0)

    @PostMapping(path = ["/"], consumes = ["application/json"])
    fun create(@RequestBody user: User): User = users.save(user)

    @PutMapping(path = ["/{id}", "/{id}/replace"])
    fun update(@PathVariable id: Long, @RequestBody user: User): User = users.save(user)

    @DeleteMapping("/{id}", "/remove/{id}")
    fun delete(@PathVariable id: Long) {
        users.delete(id)
    }

    @org.springframework.web.bind.annotation.PatchMapping("/{id}")
    fun patch(@PathVariable id: Long): User = users.find(id)

    @RequestMapping(value = ["/search"], method = [RequestMethod.GET])
    fun search(@RequestParam q: String): List<User> = users.findAll()

    @RequestMapping(path = ["/bulk"], method = [RequestMethod.POST, RequestMethod.PUT])
    fun bulk(@RequestBody list: List<User>) {
    }

    // No method: every method.
    @RequestMapping("/legacy")
    fun legacy() {
    }

    // A path from a constant or a template can't be read: no route.
    @GetMapping(Paths.EXPORT)
    fun export() {
    }

    @GetMapping("/x/$PREFIX")
    fun templated() {
    }

    // Not a mapping: no route.
    fun helper() {
    }

    // A nested class without @RestController is not a controller.
    class Inner {
        @GetMapping("/inner")
        fun inner() {
        }
    }
}

// @Controller with no class prefix: a mapping without a path is "/".
@Controller
class PageController {

    @RequestMapping(method = [RequestMethod.GET])
    fun home(): String = "home"

    @GetMapping("/about")
    fun about(): String = "about"
}

// Two class-level paths: each method route exists under both.
@RestController
@RequestMapping(path = ["/v1/orders", "/v2/orders"])
class OrderController {

    @GetMapping("/{orderId}")
    fun get(@PathVariable orderId: String): Order = Order(orderId)

    @DeleteMapping
    fun clear() {
    }
}
