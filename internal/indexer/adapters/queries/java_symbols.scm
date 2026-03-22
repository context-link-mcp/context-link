; Java Symbol Extraction Query
; Captures: @symbol.class, @symbol.interface, @symbol.method, @symbol.type, @symbol.variable

; === Class declarations ===
; public class Foo { ... }
(class_declaration
  name: (identifier) @symbol.name
  body: (class_body) @symbol.body
) @symbol.class

; === Interface declarations ===
; public interface Foo { ... }
(interface_declaration
  name: (identifier) @symbol.name
  body: (interface_body) @symbol.body
) @symbol.interface

; === Enum declarations ===
; public enum Foo { A, B, C }
(enum_declaration
  name: (identifier) @symbol.name
  body: (enum_body) @symbol.body
) @symbol.type

; === Method declarations ===
; public void foo() { ... }
(method_declaration
  name: (identifier) @symbol.name
  body: (block) @symbol.body
) @symbol.method

; === Constructor declarations ===
; public Foo() { ... }
(constructor_declaration
  name: (identifier) @symbol.name
  body: (constructor_body) @symbol.body
) @symbol.method

; === Field declarations (class-level variables) ===
; private int foo = 42;
(field_declaration
  declarator: (variable_declarator
    name: (identifier) @symbol.name
    value: (_) @symbol.body
  )
) @symbol.variable
