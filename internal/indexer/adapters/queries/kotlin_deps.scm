; Kotlin Dependency Extraction Query
; Captures: @dependency.source, @dependency.call, @dependency.extends

; === Import declarations ===
; import java.util.List
; import kotlin.collections.*
(import_header
  (identifier) @dependency.source
) @dependency.import

; === Direct function calls ===
; println("hello")
; launch { ... }
(call_expression
  (simple_identifier) @dependency.call
)

; === Navigation / method calls ===
; viewModel.loadData()
; listOf(1, 2, 3).map { ... }
(call_expression
  (navigation_expression
    (navigation_suffix
      (simple_identifier) @dependency.call
    )
  )
)

; === Class inheritance via constructor invocation ===
; class Foo : Bar()
(delegation_specifier
  (constructor_invocation
    (user_type
      (type_identifier) @dependency.extends
    )
  )
)

; === Interface implementation (no constructor call) ===
; class Foo : Baz
(delegation_specifier
  (user_type
    (type_identifier) @dependency.extends
  )
)
