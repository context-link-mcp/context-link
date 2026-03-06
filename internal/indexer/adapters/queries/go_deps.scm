; Go Dependency Extraction Query
; Captures: @dependency.source, @dependency.call

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

; === Qualified function calls ===
; fmt.Println()
; pkg.SomeFunc()
(call_expression
  function: (selector_expression
    operand: (identifier) @dependency.call_object
    field: (field_identifier) @dependency.call_method
  )
)
