; Python HTTP Route Extraction Query (FastAPI, Flask, Django, DRF routers)
; Captures: @route.call (the decorator call), @route.receiver, @route.verb,
;           @route.path, @route.handler
; Router prefixes: @prefix.ctor, @prefix.var, @prefix.key, @prefix.value
; Django: @django.var, @django.value (built in routes_django.go)
; DRF: @drf.var, @drf.ctor, @drf.call, @drf.receiver, @drf.verb

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

; === Django urlpatterns ===
; urlpatterns = [path("users/<int:id>/", views.user_detail), ...]
; urlpatterns += [...]   /   urlpatterns = [...] + static(...)
; Module level only; the builder checks the name and walks the list.
(module
  (expression_statement
    (assignment
      left: (identifier) @django.var
      right: (_) @django.value)))

(module
  (expression_statement
    (augmented_assignment
      left: (identifier) @django.var
      right: (_) @django.value)))

; === Django REST framework routers ===
; router = routers.DefaultRouter()  /  router = SimpleRouter()
(module
  (expression_statement
    (assignment
      left: (identifier) @drf.var
      right: (call function: (_) @drf.ctor))))

; router.register(r"users", UserViewSet)
(module
  (expression_statement
    (call
      function: (attribute
        object: (identifier) @drf.receiver
        attribute: (identifier) @drf.verb)) @drf.call))
