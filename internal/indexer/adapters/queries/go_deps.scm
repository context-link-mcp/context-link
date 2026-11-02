; Go Dependency Extraction Query
; Captures: @dependency.source, @dependency.call, @dependency.call_method

; === Import declarations ===
; import "fmt"
; import ( "fmt" ; "os" )
(import_spec
  path: (interpreted_string_literal) @dependency.source
) @dependency.import

; === Function calls (unqualified) ===
; foo()
(call_expression
  function: (identifier) @dependency.call
)

; === Qualified function and method calls ===
; fmt.Println()
; pkg.SomeFunc()
; s.cache.Load()  (any operand; the field is the callee)
(call_expression
  function: (selector_expression
    operand: (_) @dependency.call_object
    field: (field_identifier) @dependency.call_method
  )
)
