; Python Dependency Extraction Query
; Captures: @dependency.source, @dependency.call

; === Import statements ===
; import os
(import_statement
  name: (dotted_name) @dependency.source
) @dependency.import

; === From-import statements ===
; from os.path import join
(import_from_statement
  module_name: (dotted_name) @dependency.source
) @dependency.import

; === Function calls (unqualified) ===
; foo()
(call
  function: (identifier) @dependency.call
)

; === Method calls / qualified calls ===
; os.path.join()
(call
  function: (attribute
    object: (identifier) @dependency.call_object
    attribute: (identifier) @dependency.call_method
  )
)
