package com.example.web

import org.springframework.cloud.openfeign.FeignClient
import org.springframework.stereotype.Service
import org.springframework.web.bind.annotation.*

// Mapping-like annotations outside a controller: none of these are routes.

// A service is not a controller, even with @RequestMapping.
@Service
@RequestMapping("/svc")
class UserService {

    @GetMapping("/users")
    fun users() {
    }

    @RequestMapping(value = ["/all"], method = [RequestMethod.POST])
    fun all() {
    }
}

// A Feign client declares calls to another service, not routes.
@FeignClient(name = "users")
interface UsersClient {

    @GetMapping("/users/{id}")
    fun get(@PathVariable("id") id: Long): User
}

// No annotation on the class at all.
class Plain {

    @PostMapping("/plain")
    fun plain() {
    }
}

// A top-level function is never a handler.
@GetMapping("/top")
fun topLevel() {
}
