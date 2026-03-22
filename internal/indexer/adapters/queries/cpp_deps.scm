; C++ Dependency Extraction Query
; Captures: @dependency.source, @dependency.call, @dependency.extends

; === Include directives ===
; #include <iostream>
; #include "myheader.h"
(preproc_include
  path: (_) @dependency.source
) @dependency.import

; === Using declarations ===
; using namespace std;
; using std::vector;
(using_declaration
  (_) @dependency.source
) @dependency.import

; === Function calls ===
; foo()
(call_expression
  function: (identifier) @dependency.call
)

; === Method calls ===
; obj.foo()
(call_expression
  function: (field_expression
    field: (field_identifier) @dependency.call
  )
)

; === Qualified calls ===
; std::sort()
; Foo::bar()
(call_expression
  function: (qualified_identifier) @dependency.call
)

; === Class inheritance ===
; class Foo : public Bar { ... }
(class_specifier
  name: (type_identifier) @dependency.child_class
  (base_class_clause
    (type_identifier) @dependency.extends
  )
)
