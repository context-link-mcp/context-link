from django.http import HttpResponse
from django.views import View


def index(request):
    return HttpResponse("index")


def year_archive(request, year):
    return HttpResponse(str(year))


def feed(request, feed):
    return HttpResponse(feed)


def serve_file(request, name=None, rest=None):
    return HttpResponse(name or rest)


def legacy(request, number):
    return HttpResponse(number)


def drafts(request):
    return HttpResponse("drafts")


def draft_detail(request, pk):
    return HttpResponse(str(pk))


class ArticleDetail(View):
    def get(self, request, year, slug):
        return HttpResponse(slug)


class ArchiveView(View):
    def get(self, request):
        return HttpResponse("archive")
