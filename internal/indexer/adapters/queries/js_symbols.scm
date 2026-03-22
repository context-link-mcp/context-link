; JavaScript Symbol Extraction Query
; Captures: @symbol.function, @symbol.class, @symbol.method, @symbol.variable
; Based on ts_symbols.scm but without TypeScript-specific constructs
; (interface_declaration, type_alias_declaration).

; === Regular function declarations ===
; function foo() { ... }
(function_declaration
  name: (identifier) @symbol.name
  body: (statement_block) @symbol.body
) @symbol.function

; === Exported function declarations ===
; export function foo() { ... }
(export_statement
  declaration: (function_declaration
    name: (identifier) @symbol.name
    body: (statement_block) @symbol.body
  ) @symbol.function
)

; === Arrow functions assigned to variables ===
; const foo = () => { ... }
(lexical_declaration
  (variable_declarator
    name: (identifier) @symbol.name
    value: (arrow_function
      body: (_) @symbol.body
    )
  )
) @symbol.function

; === Exported arrow functions ===
; export const foo = () => { ... }
(export_statement
  declaration: (lexical_declaration
    (variable_declarator
      name: (identifier) @symbol.name
      value: (arrow_function
        body: (_) @symbol.body
      )
    )
  ) @symbol.function
)

; === Class declarations ===
; class Foo { ... }
(class_declaration
  name: (identifier) @symbol.name
  body: (class_body) @symbol.body
) @symbol.class

; === Exported class declarations ===
; export class Foo { ... }
(export_statement
  declaration: (class_declaration
    name: (identifier) @symbol.name
    body: (class_body) @symbol.body
  ) @symbol.class
)

; === Class methods ===
; method() { ... }
(method_definition
  name: (property_identifier) @symbol.name
  body: (statement_block) @symbol.body
) @symbol.method

; === Exported variables/constants (non-arrow) ===
; export const FOO = "bar"
(export_statement
  declaration: (lexical_declaration
    (variable_declarator
      name: (identifier) @symbol.name
      value: (_) @symbol.body
    )
  ) @symbol.variable
  (#not-match? @symbol.body "^\\(arrow_function")
)
