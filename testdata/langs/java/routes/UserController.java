package com.example.web;

import java.util.List;

import org.springframework.web.bind.annotation.*;

// A controller with a class prefix: every route starts with /api/users.
@RestController
@RequestMapping("/api/users")
public class UserController {

    @GetMapping
    public List<User> list() {
        return null;
    }

    @GetMapping("/{id:\\d+}")
    public User get(@PathVariable long id) {
        return null;
    }

    // An overload of get: the route links to this one, not the first.
    @GetMapping(value = "/{id}/avatar", produces = "image/png")
    public byte[] get(@PathVariable long id, @RequestParam boolean thumb) {
        return null;
    }

    @PostMapping(path = "/", consumes = "application/json")
    public User create(@RequestBody User user) {
        return null;
    }

    @PutMapping(path = {"/{id}", "/{id}/replace"})
    public User update(@PathVariable long id, @RequestBody User user) {
        return null;
    }

    @DeleteMapping({"/{id}", "/remove/{id}"})
    public void delete(@PathVariable long id) {
    }

    @org.springframework.web.bind.annotation.PatchMapping("/{id}")
    public User patch(@PathVariable long id) {
        return null;
    }

    @RequestMapping(value = "/search", method = RequestMethod.GET)
    public List<User> search(@RequestParam String q) {
        return null;
    }

    @RequestMapping(path = "/bulk", method = {RequestMethod.POST, RequestMethod.PUT})
    public void bulk(@RequestBody List<User> users) {
    }

    // No method: every method.
    @RequestMapping("/legacy")
    public void legacy() {
    }

    // A path from a constant can't be read: no route.
    @GetMapping(Paths.EXPORT)
    public void export() {
    }

    // Not a mapping: no route.
    public void helper() {
    }

    // A nested class without @RestController is not a controller.
    static class Inner {
        @GetMapping("/inner")
        public void inner() {
        }
    }
}

// @Controller with no class prefix: a mapping without a path is "/".
@Controller
class PageController {

    @RequestMapping(method = RequestMethod.GET)
    public String home() {
        return "home";
    }

    @GetMapping("/about")
    public String about() {
        return "about";
    }
}

// Two class-level paths: each method route exists under both.
@RestController
@RequestMapping(path = {"/v1/orders", "/v2/orders"})
class OrderController {

    @GetMapping("/{orderId}")
    public Order get(@PathVariable String orderId) {
        return null;
    }

    @DeleteMapping
    public void clear() {
    }
}
