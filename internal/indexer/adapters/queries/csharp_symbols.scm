; C# Symbol Extraction Query
; Captures: @symbol.class, @symbol.interface, @symbol.method, @symbol.type, @symbol.variable

; === Class declarations ===
; public class Foo { ... }
(class_declaration
  name: (identifier) @symbol.name
  body: (declaration_list) @symbol.body
) @symbol.class

; === Interface declarations ===
; public interface IFoo { ... }
(interface_declaration
  name: (identifier) @symbol.name
  body: (declaration_list) @symbol.body
) @symbol.interface

; === Struct declarations ===
; public struct Foo { ... }
(struct_declaration
  name: (identifier) @symbol.name
  body: (declaration_list) @symbol.body
) @symbol.type

; === Enum declarations ===
; public enum Foo { A, B, C }
(enum_declaration
  name: (identifier) @symbol.name
  body: (enum_member_declaration_list) @symbol.body
) @symbol.type

; === Method declarations ===
; public void Foo() { ... }
(method_declaration
  name: (identifier) @symbol.name
  body: (block) @symbol.body
) @symbol.method

; === Constructor declarations ===
; public MyClass() { ... }
(constructor_declaration
  name: (identifier) @symbol.name
  body: (block) @symbol.body
) @symbol.method

; === Property declarations ===
; public int Foo { get; set; }
(property_declaration
  name: (identifier) @symbol.name
  accessors: (accessor_list) @symbol.body
) @symbol.variable

; === Field declarations ===
; private int _foo = 42;
(field_declaration
  (variable_declaration
    (variable_declarator
      (identifier) @symbol.name
    ) @symbol.body
  )
) @symbol.variable
