; JavaScript Dependency Extraction Query
; Captures: @dependency.source, @dependency.call, @dependency.extends

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

; === Require calls ===
; const foo = require('./bar')
(call_expression
  function: (identifier) @dependency.call
  (#eq? @dependency.call "require")
  arguments: (arguments
    (string) @dependency.source
  )
) @dependency.import

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
  name: (identifier) @dependency.child_class
  (class_heritage
    (identifier) @dependency.extends
  )
)
