; JavaScript/TypeScript HTTP Route Extraction Query (Express and similar)
; Captures: @route.call (the whole call), @route.receiver, @route.verb, @route.path
; The handler is the last argument and is resolved in Go from @route.call.

; app.get("/users/:id", handler)  /  router.post("/users", auth, (req, res) => {})
(call_expression
  function: (member_expression
    object: (_) @route.receiver
    property: (property_identifier) @route.verb)
  arguments: (arguments . (string) @route.path)
) @route.call
