; Go HTTP Route Extraction Query (Gin, Echo, chi, net/http)
; Captures: @route.call (the whole call), @route.receiver, @route.verb
; Router bindings: @prefix.var, @prefix.value (a variable and the expression
;                  assigned to it: a router constructor or a .Group(...) call)
; Router-typed struct fields: @router.field, @router.type
;
; The query only finds candidates. internal/indexer/routes_go.go keeps a call
; only when its receiver is a router of an imported framework (constructed,
; grouped, or a parameter/field of a router type), reads the path, method and
; handler from the arguments, and applies group and chi r.Route prefixes.

; r.GET("/users/:id", h)  /  mux.HandleFunc("GET /x", h)  /  http.Handle("/x", h)
(call_expression
  function: (selector_expression
    operand: (_) @route.receiver
    field: (field_identifier) @route.verb)
  arguments: (argument_list)
) @route.call

; r := gin.Default()  /  api := r.Group("/api")
(short_var_declaration
  left: (expression_list . (identifier) @prefix.var)
  right: (expression_list . (_) @prefix.value))

; r = chi.NewRouter()  /  s.router = echo.New()
(assignment_statement
  left: (expression_list . (_) @prefix.var)
  right: (expression_list . (_) @prefix.value))

; var mux = http.NewServeMux()
(var_spec
  name: (identifier) @prefix.var
  value: (expression_list . (_) @prefix.value))

; type Server struct { router *gin.Engine }
(field_declaration
  name: (field_identifier) @router.field
  type: (_) @router.type)
