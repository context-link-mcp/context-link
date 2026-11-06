package com.example.web;

import org.springframework.cloud.openfeign.FeignClient;
import org.springframework.stereotype.Service;
import org.springframework.web.bind.annotation.*;

// Mapping-like annotations outside a controller: none of these are routes.

// A service is not a controller, even with @RequestMapping.
@Service
@RequestMapping("/svc")
class UserService {

    @GetMapping("/users")
    public void users() {
    }

    @RequestMapping(value = "/all", method = RequestMethod.POST)
    public void all() {
    }
}

// A Feign client declares calls to another service, not routes.
@FeignClient(name = "users")
interface UsersClient {

    @GetMapping("/users/{id}")
    User get(@PathVariable("id") long id);
}

// No annotation on the class at all.
class Plain {

    @PostMapping("/plain")
    public void plain() {
    }
}
