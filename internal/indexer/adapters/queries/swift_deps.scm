; Swift Dependency Extraction Query
; Captures: @dependency.source, @dependency.call, @dependency.extends

; === Import declarations ===
; import Foundation
; import UIKit
; import SwiftUI
(import_declaration
  (identifier) @dependency.source
) @dependency.import

; === Direct function / initializer calls ===
; fetchData()
; MyView()
(call_expression
  (simple_identifier) @dependency.call
)

; === Navigation / method calls ===
; URLSession.shared.dataTask(...)
; view.addSubview(button)
(call_expression
  (navigation_expression
    suffix: (navigation_suffix
      suffix: (simple_identifier) @dependency.call
    )
  )
)

; === Class/struct/enum inheritance and protocol conformance ===
; class ViewController: UIViewController { }
; struct Response: Codable { }
(class_declaration
  (inheritance_specifier
    inherits_from: (user_type
      (type_identifier) @dependency.extends
    )
  )
)
