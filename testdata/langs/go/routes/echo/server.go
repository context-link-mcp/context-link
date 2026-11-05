// Package echo registers Echo routes: verbs, Any/Add/Match, a group and a
// router parameter.
package echo

import (
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"example.com/shop/handlers"
)

func listOrders(c echo.Context) error { return nil }

func getOrder(c echo.Context) error { return nil }

func main() {
	e := echo.New()
	e.GET("/orders", listOrders)
	e.POST("/orders", listOrders, middleware.Logger())
	e.Any("/any", getOrder)
	e.Add("PATCH", "/orders/:id", getOrder)
	e.Match([]string{"GET", "HEAD"}, "/files/*", getOrder)

	admin := e.Group("/admin", middleware.BasicAuth(nil))
	admin.GET("/orders/:id", getOrder)
	admin.DELETE("/users/:id", echoWrap(handlers.Create))

	mount(e)
}

func mount(e *echo.Echo) {
	e.PUT("/orders/:id", func(c echo.Context) error { return nil })
}

// Not a route: an echo.Context's Get reads a context value.
func fromContext(c echo.Context) {
	c.Get("/user")
}

func echoWrap(h any) echo.HandlerFunc { return nil }
