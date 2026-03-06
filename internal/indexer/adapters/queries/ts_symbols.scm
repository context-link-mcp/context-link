; TypeScript Symbol Extraction Query
; Captures: @symbol.function, @symbol.class, @symbol.interface, @symbol.type, @symbol.variable
; Each capture group includes @symbol.name and @symbol.body where applicable.

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

; === Async function declarations ===
; async function foo() { ... }
; export async function foo() { ... }

; === Arrow functions assigned to variables ===
; const foo = () => { ... }
; const foo = async () => { ... }
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
  name: (type_identifier) @symbol.name
  body: (class_body) @symbol.body
) @symbol.class

; === Exported class declarations ===
; export class Foo { ... }
(export_statement
  declaration: (class_declaration
    name: (type_identifier) @symbol.name
    body: (class_body) @symbol.body
  ) @symbol.class
)

; === Class methods ===
; method() { ... }
(method_definition
  name: (property_identifier) @symbol.name
  body: (statement_block) @symbol.body
) @symbol.method

; === Interface declarations ===
; interface Foo { ... }
(interface_declaration
  name: (type_identifier) @symbol.name
  body: (interface_body) @symbol.body
) @symbol.interface

; === Exported interface declarations ===
; export interface Foo { ... }
(export_statement
  declaration: (interface_declaration
    name: (type_identifier) @symbol.name
    body: (interface_body) @symbol.body
  ) @symbol.interface
)

; === Type alias declarations ===
; type Foo = ...
(type_alias_declaration
  name: (type_identifier) @symbol.name
  value: (_) @symbol.body
) @symbol.type

; === Exported type alias declarations ===
; export type Foo = ...
(export_statement
  declaration: (type_alias_declaration
    name: (type_identifier) @symbol.name
    value: (_) @symbol.body
  ) @symbol.type
)

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
