; C Symbol Extraction Query
; Captures: @symbol.function, @symbol.type, @symbol.variable

; === Function definitions ===
; int foo(int x) { ... }
(function_definition
  declarator: (function_declarator
    declarator: (identifier) @symbol.name
  )
  body: (compound_statement) @symbol.body
) @symbol.function

; === Pointer function definitions ===
; int *foo(int x) { ... }
(function_definition
  declarator: (pointer_declarator
    declarator: (function_declarator
      declarator: (identifier) @symbol.name
    )
  )
  body: (compound_statement) @symbol.body
) @symbol.function

; === Struct definitions ===
; struct Foo { ... };
(type_definition
  type: (struct_specifier
    name: (type_identifier) @symbol.name
    body: (field_declaration_list) @symbol.body
  )
) @symbol.type

; === Tagged struct declarations (without typedef) ===
(struct_specifier
  name: (type_identifier) @symbol.name
  body: (field_declaration_list) @symbol.body
) @symbol.type

; === Enum definitions ===
; enum Foo { A, B, C };
(type_definition
  type: (enum_specifier
    name: (type_identifier) @symbol.name
    body: (enumerator_list) @symbol.body
  )
) @symbol.type

; === Tagged enum declarations (without typedef) ===
(enum_specifier
  name: (type_identifier) @symbol.name
  body: (enumerator_list) @symbol.body
) @symbol.type

; === Type definitions (aliases) ===
; typedef int MyInt;
(type_definition
  declarator: (type_identifier) @symbol.name
) @symbol.type
