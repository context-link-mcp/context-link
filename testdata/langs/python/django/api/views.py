from django.http import JsonResponse
from rest_framework import viewsets


class UserViewSet(viewsets.ModelViewSet):
    def list(self, request):
        return JsonResponse([], safe=False)


class GroupViewSet(viewsets.ViewSet):
    def list(self, request):
        return JsonResponse([], safe=False)


def status(request):
    return JsonResponse({"ok": True})
