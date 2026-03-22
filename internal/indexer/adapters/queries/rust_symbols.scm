; Rust Symbol Extraction Query
; Captures: @symbol.function, @symbol.type, @symbol.interface, @symbol.method

; === Function definitions ===
; fn foo() { ... }
; pub fn foo() { ... }
(function_item
  name: (identifier) @symbol.name
  body: (block) @symbol.body
) @symbol.function

; === Struct definitions ===
; struct Foo { ... }
(struct_item
  name: (type_identifier) @symbol.name
  body: (field_declaration_list) @symbol.body
) @symbol.type

; === Struct (tuple) definitions ===
; struct Foo(i32, i32);
(struct_item
  name: (type_identifier) @symbol.name
) @symbol.type

; === Enum definitions ===
; enum Foo { A, B, C }
(enum_item
  name: (type_identifier) @symbol.name
  body: (enum_variant_list) @symbol.body
) @symbol.type

; === Trait definitions ===
; trait Foo { ... }
(trait_item
  name: (type_identifier) @symbol.name
  body: (declaration_list) @symbol.body
) @symbol.interface

; === Impl blocks ===
; impl Foo { ... }
; impl Trait for Foo { ... }
(impl_item
  type: (type_identifier) @symbol.name
  body: (declaration_list) @symbol.body
) @symbol.class

; === Type aliases ===
; type Foo = Bar;
(type_item
  name: (type_identifier) @symbol.name
  type: (_) @symbol.body
) @symbol.type

; === Const items ===
; const FOO: i32 = 42;
(const_item
  name: (identifier) @symbol.name
  value: (_) @symbol.body
) @symbol.variable

; === Static items ===
; static FOO: i32 = 42;
(static_item
  name: (identifier) @symbol.name
  value: (_) @symbol.body
) @symbol.variable
