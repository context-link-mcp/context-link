; Python HTTP Route Extraction Query (FastAPI, Flask)
; Captures: @route.call (the decorator call), @route.receiver, @route.verb,
;           @route.path, @route.handler
; Router prefixes: @prefix.ctor, @prefix.var, @prefix.key, @prefix.value

; === Decorated route handlers ===
; @router.get("/users/{id}")  /  @app.route("/users", methods=["POST"])
(decorated_definition
  (decorator
    (call
      function: (attribute
        object: (_) @route.receiver
        attribute: (identifier) @route.verb)
      arguments: (argument_list . (string) @route.path)
    ) @route.call
  )
  definition: (function_definition
    name: (identifier) @route.handler)
)

; === Router / Blueprint prefixes ===
; router = APIRouter(prefix="/api")  /  bp = Blueprint("x", __name__, url_prefix="/x")
(assignment
  left: (identifier) @prefix.var
  right: (call
    function: (identifier) @prefix.ctor
    arguments: (argument_list
      (keyword_argument
        name: (identifier) @prefix.key
        value: (string) @prefix.value)))
)
