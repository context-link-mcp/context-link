; Python Symbol Extraction Query
; Captures: @symbol.function, @symbol.class, @symbol.method, @symbol.variable

; === Function definitions ===
; def foo(): ...
(function_definition
  name: (identifier) @symbol.name
  body: (block) @symbol.body
) @symbol.function

; === Async function definitions ===
; async def foo(): ...

; === Class definitions ===
; class Foo: ...
(class_definition
  name: (identifier) @symbol.name
  body: (block) @symbol.body
) @symbol.class

; === Decorated definitions are captured by the inner function/class rules ===

; === Top-level assignments (module-level variables/constants) ===
; FOO = "bar"
(module
  (expression_statement
    (assignment
      left: (identifier) @symbol.name
      right: (_) @symbol.body
    )
  ) @symbol.variable
)
