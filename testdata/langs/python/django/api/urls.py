from django.urls import include, path
from rest_framework import routers

from . import views

router = routers.DefaultRouter()
router.register(r"users", views.UserViewSet)
router.register("groups", views.GroupViewSet, basename="group")

urlpatterns = [
    path("v1/", include(router.urls)),
    path("status/", views.status),
]
