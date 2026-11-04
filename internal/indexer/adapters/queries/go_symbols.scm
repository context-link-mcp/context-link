; Go Symbol Extraction Query
; Captures: @symbol.function, @symbol.method, @symbol.interface, @symbol.type, @symbol.variable

; === Function declarations ===
; func Foo() { ... }
(function_declaration
  name: (identifier) @symbol.name
  body: (block) @symbol.body
) @symbol.function

; === Method declarations ===
; func (r *Receiver) Foo() { ... }
(method_declaration
  name: (field_identifier) @symbol.name
  body: (block) @symbol.body
) @symbol.method

; === Struct type declarations ===
; type Foo struct { ... }
(type_declaration
  (type_spec
    name: (type_identifier) @symbol.name
    type: (struct_type) @symbol.body
  )
) @symbol.type

; === Interface type declarations ===
; type Reader interface { Read(...) error }
(type_declaration
  (type_spec
    name: (type_identifier) @symbol.name
    type: (interface_type) @symbol.body
  )
) @symbol.interface

; === Function type declarations ===
; type Handler func(key string) (interface{}, error)
(type_declaration
  (type_spec
    name: (type_identifier) @symbol.name
    type: (function_type) @symbol.body
  )
) @symbol.type

; === Other named type declarations (type aliases, etc.) ===
; type MyString string
; type ID int64
(type_declaration
  (type_spec
    name: (type_identifier) @symbol.name
    type: (type_identifier) @symbol.body
  )
) @symbol.type

; === Const declarations (single) ===
; const Foo = "bar"
; Anchored to source_file: a const or var inside a function body is a local,
; not a symbol (it would otherwise be the innermost symbol around the calls
; next to it and take their edges).
(source_file
  (const_declaration
    (const_spec
      name: (identifier) @symbol.name
      value: (_) @symbol.body
    )
  ) @symbol.variable
)

; === Var declarations (single) ===
; var Foo string
(source_file
  (var_declaration
    (var_spec
      name: (identifier) @symbol.name
    )
  ) @symbol.variable
)
