; Rust Dependency Extraction Query
; Captures: @dependency.source, @dependency.call

; === Use declarations ===
; use std::collections::HashMap;
; use crate::module::Item;
(use_declaration
  argument: (_) @dependency.source
) @dependency.import

; === Function calls ===
; foo()
(call_expression
  function: (identifier) @dependency.call
)

; === Method calls ===
; foo.bar()
(call_expression
  function: (field_expression
    field: (field_identifier) @dependency.call
  )
)

; === Qualified path calls ===
; Foo::new()
; std::io::read()
(call_expression
  function: (scoped_identifier) @dependency.call
)
