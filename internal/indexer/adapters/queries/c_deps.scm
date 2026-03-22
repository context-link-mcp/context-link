; C Dependency Extraction Query
; Captures: @dependency.source, @dependency.call

; === Include directives ===
; #include <stdio.h>
; #include "myheader.h"
(preproc_include
  path: (_) @dependency.source
) @dependency.import

; === Function calls ===
; foo()
(call_expression
  function: (identifier) @dependency.call
)

; === Qualified function calls (via pointer/member) ===
; ptr->method()
(call_expression
  function: (field_expression
    field: (field_identifier) @dependency.call
  )
)
