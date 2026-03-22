; C# Dependency Extraction Query
; Captures: @dependency.source, @dependency.call, @dependency.extends, @dependency.implements

; === Using directives ===
; using System;
; using System.Collections.Generic;
(using_directive
  (identifier) @dependency.source
) @dependency.import

(using_directive
  (qualified_name) @dependency.source
) @dependency.import

; === Method invocations ===
; Foo()
(invocation_expression
  function: (identifier) @dependency.call
)

; === Member method invocations ===
; obj.Foo()
(invocation_expression
  function: (member_access_expression
    name: (identifier) @dependency.call
  )
)

; === Object creation ===
; new Foo()
(object_creation_expression
  type: (identifier) @dependency.call
)

; === Class inheritance ===
; class Foo : Bar { ... }
(class_declaration
  name: (identifier) @dependency.child_class
  (base_list
    (identifier) @dependency.extends
  )
)

; === Interface implementation ===
; class Foo : IBar { ... }
(class_declaration
  name: (identifier) @dependency.implementor
  (base_list
    (identifier) @dependency.implements
  )
)
