package webservice

import (
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/rs/zerolog"
)

func RequestLogger(baseLogger *zerolog.Logger) echo.MiddlewareFunc {
	return middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogStatus:   true,
		LogURI:      true,
		LogLatency:  true,
		LogMethod:   true,
		LogError:    true,
		HandleError: true,
		LogValuesFunc: func(c echo.Context, v middleware.RequestLoggerValues) error {
			lgr, _ := c.Get("logger").(*zerolog.Logger)
			if lgr == nil {
				lgr = baseLogger
			}
			event := lgr.Info()
			if v.Error != nil {
				event = lgr.Error().Err(v.Error)
			}
			event.Str("method", v.Method).
				Str("uri", v.URI).
				Str("origin", c.Request().Header.Get(echo.HeaderOrigin)).
				Str("access_control_request_method", c.Request().Header.Get(echo.HeaderAccessControlRequestMethod)).
				Str("access_control_request_headers", c.Request().Header.Get(echo.HeaderAccessControlRequestHeaders)).
				Str("access_control_allow_origin", c.Response().Header().Get(echo.HeaderAccessControlAllowOrigin)).
				Int("status", v.Status).
				Dur("latency", v.Latency).
				Msg("http request")
			return nil
		},
	})
}
