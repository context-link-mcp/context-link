from django.contrib import admin
from django.http import HttpResponse
from django.urls import include, path


def healthz(request):
    return HttpResponse("ok")


urlpatterns = [
    path("admin/", admin.site.urls),
    # Included files keep their own paths: prefixes aren't joined across files.
    path("blog/", include("blog.urls")),
    path("api/", include("api.urls", namespace="api")),
    path("healthz/", healthz, name="healthz"),
    path("ping/", lambda request: HttpResponse("pong")),
]
