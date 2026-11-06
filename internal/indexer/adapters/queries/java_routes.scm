; Java HTTP Route Extraction Query (Spring MVC / WebFlux annotations)
; Captures: @route.call (the annotated method), @route.annotation (one
;           annotation of it), @route.verb (the annotation's name),
;           @route.handler (the method's name)
;
; The query only finds candidates: one match per annotation of a method.
; internal/indexer/routes_spring.go keeps @GetMapping, @PostMapping,
; @PutMapping, @DeleteMapping, @PatchMapping and @RequestMapping, reads their
; paths and methods, and keeps a method only when its class is annotated
; @RestController or @Controller (the class-level @RequestMapping gives the
; prefix). The class is found by walking up the AST from the method.

; @GetMapping("/users/{id}") public User get(...) { ... }
(method_declaration
  (modifiers
    (annotation
      name: (_) @route.verb) @route.annotation)
  name: (identifier) @route.handler
) @route.call

; @GetMapping public List<User> list() { ... }
(method_declaration
  (modifiers
    (marker_annotation
      name: (_) @route.verb) @route.annotation)
  name: (identifier) @route.handler
) @route.call
