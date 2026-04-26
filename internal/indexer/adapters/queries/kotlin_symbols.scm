; Kotlin Symbol Extraction Query
; Captures: @symbol.class, @symbol.type, @symbol.function, @symbol.variable
; Note: Kotlin's tree-sitter grammar does not expose named fields on these nodes,
; so all patterns use positional (anonymous) child matching.

; === Class and interface declarations ===
; class Foo { ... }
; interface Foo { ... }
(class_declaration
  (type_identifier) @symbol.name
  (class_body) @symbol.body
) @symbol.class

; === Enum class declarations ===
; enum class Direction { NORTH, SOUTH, EAST, WEST }
(class_declaration
  (type_identifier) @symbol.name
  (enum_class_body) @symbol.body
) @symbol.type

; === Data/sealed/abstract class declarations (primary constructor, no class body) ===
; data class User(val id: UUID, val name: String)
; sealed class Result
(class_declaration
  (type_identifier) @symbol.name
  (primary_constructor) @symbol.body
) @symbol.class

; === Top-level and member function declarations ===
; fun greet(name: String): String { ... }
; suspend fun fetchData(): Result { ... }
(function_declaration
  (simple_identifier) @symbol.name
  (function_body) @symbol.body
) @symbol.function

; === Object declarations (singletons) ===
; object Repository { ... }
(object_declaration
  (type_identifier) @symbol.name
  (class_body) @symbol.body
) @symbol.class

; === Property declarations ===
; val baseUrl = "https://..."
; var count: Int = 0
(property_declaration
  (variable_declaration
    (simple_identifier) @symbol.name
  )
) @symbol.variable
