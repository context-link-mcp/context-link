; Java Dependency Extraction Query
; Captures: @dependency.source, @dependency.call, @dependency.extends, @dependency.implements

; === Import declarations ===
; import java.util.List;
; import static java.lang.Math.PI;
(import_declaration
  (scoped_identifier) @dependency.source
) @dependency.import

; === Method invocations ===
; foo(), obj.foo(), this.foo(), a.b().foo()
; The name is the callee whatever the object, so a separate object pattern
; would only duplicate these deps.
(method_invocation
  name: (identifier) @dependency.call
)

; === Object creation ===
; new Foo()
(object_creation_expression
  type: (type_identifier) @dependency.call
)

; === Class extends ===
; class Foo extends Bar { ... }
(class_declaration
  name: (identifier) @dependency.child_class
  (superclass
    (type_identifier) @dependency.extends
  )
)

; === Class implements ===
; class Foo implements Bar, Baz { ... }
(class_declaration
  name: (identifier) @dependency.implementor
  (super_interfaces
    (type_list
      (type_identifier) @dependency.implements
    )
  )
)
