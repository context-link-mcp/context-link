; Python Dependency Extraction Query
; Captures: @dependency.source, @dependency.call, @dependency.call_method

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

; from . import x / from ..pkg.mod import x
(import_from_statement
  module_name: (relative_import) @dependency.source
) @dependency.import

; === Function calls (unqualified) ===
; foo()
(call
  function: (identifier) @dependency.call
)

; === Method calls / qualified calls ===
; os.path.join()
; self.client.fetch()  (any object; the attribute is the callee)
(call
  function: (attribute
    object: (_) @dependency.call_object
    attribute: (identifier) @dependency.call_method
  )
)
