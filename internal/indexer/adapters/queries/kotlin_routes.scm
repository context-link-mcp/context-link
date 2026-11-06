; Kotlin HTTP Route Extraction Query (Spring MVC / WebFlux annotations)
; Captures: @route.call (the annotated function), @route.annotation (one
;           annotation of it), @route.handler (the function's name)
;
; The Kotlin grammar exposes no fields, and an annotation's name sits in a
; user_type, either directly (@GetMapping) or in a constructor_invocation
; (@GetMapping("/x")), so internal/indexer/routes_spring.go reads the name and
; arguments from @route.annotation. It keeps the Spring mapping annotations
; and only functions of a class annotated @RestController or @Controller,
; found by walking up the AST from the function.

; @GetMapping("/users/{id}") fun get(@PathVariable id: Long): User = ...
(function_declaration
  (modifiers
    (annotation) @route.annotation)
  (simple_identifier) @route.handler
) @route.call
