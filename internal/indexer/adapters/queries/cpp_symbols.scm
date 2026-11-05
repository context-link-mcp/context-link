; C++ Symbol Extraction Query
; Captures: @symbol.function, @symbol.class, @symbol.type, @symbol.variable

; === Function definitions ===
; int foo(int x) { ... }
(function_definition
  declarator: (function_declarator
    declarator: (identifier) @symbol.name
  )
  body: (compound_statement) @symbol.body
) @symbol.function

; === Qualified function definitions (member functions defined outside class) ===
; int Foo::bar() { ... }, ns::Foo::bar, Foo<T>::bar, Foo::~Foo, Foo::operator==,
; int* Foo::bar(), Foo& Foo::bar(). The whole qualified_identifier is the name:
; the extractor splits it into the last scope segment and the member name.
(function_definition
  declarator: [
    (function_declarator
      declarator: (qualified_identifier) @symbol.name)
    (pointer_declarator
      declarator: (function_declarator
        declarator: (qualified_identifier) @symbol.name))
    (reference_declarator
      (function_declarator
        declarator: (qualified_identifier) @symbol.name))
  ]
  body: (compound_statement) @symbol.body
) @symbol.method

; === Member functions defined inside the class body ===
; class Foo { int bar() { ... } ~Foo() { ... } bool operator==(...) { ... } int* p() { ... } };
; (Constructors are plain identifiers and match the function pattern above.)
(function_definition
  declarator: [
    (function_declarator
      declarator: [(field_identifier) (destructor_name) (operator_name)] @symbol.name)
    (pointer_declarator
      declarator: (function_declarator
        declarator: [(field_identifier) (destructor_name) (operator_name)] @symbol.name))
    (reference_declarator
      (function_declarator
        declarator: [(field_identifier) (destructor_name) (operator_name)] @symbol.name))
  ]
  body: (compound_statement) @symbol.body
) @symbol.method

; === Class definitions ===
; class Foo { ... };
(class_specifier
  name: (type_identifier) @symbol.name
  body: (field_declaration_list) @symbol.body
) @symbol.class

; === Struct definitions ===
; struct Foo { ... };
(struct_specifier
  name: (type_identifier) @symbol.name
  body: (field_declaration_list) @symbol.body
) @symbol.type

; === Enum definitions ===
; enum Foo { A, B, C };
; enum class Foo { A, B, C };
(enum_specifier
  name: (type_identifier) @symbol.name
  body: (enumerator_list) @symbol.body
) @symbol.type

; === Type aliases ===
; using Foo = Bar;
(alias_declaration
  name: (type_identifier) @symbol.name
  type: (_) @symbol.body
) @symbol.type

; === Template declarations with function ===
; template<typename T> T foo() { ... }
(template_declaration
  (function_definition
    declarator: (function_declarator
      declarator: (identifier) @symbol.name
    )
    body: (compound_statement) @symbol.body
  )
) @symbol.function

; === Template declarations with class ===
; template<typename T> class Foo { ... };
(template_declaration
  (class_specifier
    name: (type_identifier) @symbol.name
    body: (field_declaration_list) @symbol.body
  )
) @symbol.class
