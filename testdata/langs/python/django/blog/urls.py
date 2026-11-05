from django.conf.urls import url
from django.contrib.auth.decorators import login_required
from django.urls import include, path, re_path
from django.views.generic import TemplateView

from . import views
from .views import ArticleDetail

app_name = "blog"

urlpatterns = [
    path("", views.index, name="index"),
    path("articles/<int:year>/", views.year_archive),
    path("articles/<int:year>/<slug:slug>/", ArticleDetail.as_view(), name="detail"),
    path("archive/", views.ArchiveView.as_view(paginate_by=20)),
    path("files/<path:name>", view=views.serve_file),
    re_path(r"^feeds/(?P<feed>[\w-]+)/$", views.feed),
    re_path(r"^raw/(?P<rest>.*)$", views.serve_file),
    re_path(r"^legacy/(\d+)/$", views.legacy),
    path("about/", TemplateView.as_view(template_name="about.html")),
    path("private/", login_required(views.index)),
    path(
        "drafts/",
        include([
            path("", views.drafts),
            path("<int:pk>/", views.draft_detail),
        ]),
    ),
]

urlpatterns += [
    url(r"^old-feed/$", views.feed),
]
