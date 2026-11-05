"""Not a urls module: path() calls here are not routes."""
from os import path

from django.urls import path as route


def build(name):
    return path.join("static", name)


patterns = [route("unused/", build), path("unused/", build)]


def make_patterns():
    urlpatterns = [path("nested/", build)]
    return urlpatterns
