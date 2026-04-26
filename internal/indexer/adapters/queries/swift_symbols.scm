; Swift Symbol Extraction Query
; Captures: @symbol.class, @symbol.type, @symbol.interface, @symbol.function, @symbol.variable

; === Class, struct, actor, and enum type declarations ===
; class NetworkManager { }
; struct Point { }
; actor DataStore { }
(class_declaration
  name: (type_identifier) @symbol.name
  body: (class_body) @symbol.body
) @symbol.class

; === Enum declarations (with enum body) ===
; enum Direction { case north, south }
(class_declaration
  name: (type_identifier) @symbol.name
  body: (enum_class_body) @symbol.body
) @symbol.type

; === Protocol declarations ===
; protocol Drawable { }
; protocol Hashable: Equatable { }
(protocol_declaration
  name: (type_identifier) @symbol.name
  body: (protocol_body) @symbol.body
) @symbol.interface

; === Function declarations ===
; func fetchUser(id: Int) -> User { }
; mutating func reset() { }
(function_declaration
  name: (simple_identifier) @symbol.name
  body: (function_body) @symbol.body
) @symbol.function

; === Stored and computed property declarations ===
; let name: String
; var isLoading = false
(property_declaration
  name: (pattern
    bound_identifier: (simple_identifier) @symbol.name
  )
) @symbol.variable

; === Typealias declarations ===
; typealias Completion = (Result<Data, Error>) -> Void
(typealias_declaration
  name: (type_identifier) @symbol.name
) @symbol.type
