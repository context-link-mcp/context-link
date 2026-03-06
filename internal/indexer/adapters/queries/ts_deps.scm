; TypeScript Dependency Extraction Query
; Captures: @dependency.import, @dependency.call, @dependency.extends, @dependency.implements

; === Import statements ===
; import { foo } from './bar'
; import foo from './bar'
; import * as foo from './bar'
(import_statement
  source: (string) @dependency.source
) @dependency.import

; === Dynamic imports ===
; import('./foo')
(call_expression
  function: (import)
  arguments: (arguments
    (string) @dependency.source
  )
) @dependency.dynamic_import

; === Call expressions ===
; foo()
(call_expression
  function: (identifier) @dependency.call
)

; === Method calls ===
; foo.bar()
(call_expression
  function: (member_expression
    object: (identifier) @dependency.call_object
    property: (property_identifier) @dependency.call_method
  )
)

; === Class extends ===
; class Foo extends Bar { ... }
(class_declaration
  name: (type_identifier) @dependency.child_class
  (class_heritage
    (extends_clause
      value: (identifier) @dependency.extends
    )
  )
)

; === Class implements ===
; class Foo implements Bar { ... }
(class_declaration
  name: (type_identifier) @dependency.implementor
  (class_heritage
    (implements_clause
      (type_identifier) @dependency.implements
    )
  )
)
