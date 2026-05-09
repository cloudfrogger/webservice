package webservice

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/labstack/echo/v4"
	echo4mw "github.com/labstack/echo/v4/middleware"
	"github.com/oasdiff/yaml"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	echoSwagger "github.com/swaggo/echo-swagger"
	"go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

type OapiRegisterFunc func(*echo.Echo)

type CustomApiAuthentication func(ctx context.Context, apitoken, sourceIP string) (email string, firstname string, lastname string, valid bool)
type CustomErrorHandler func(err error, c echo.Context)

type apiSpecification struct {
	basePath string
	doc      *openapi3.T
}

type APIBuilder struct {
	echo                  *echo.Echo
	redisCache            *redis.Client
	enablePrometheus      bool
	enableSwagger         bool
	enableOpenTelemetry   bool
	enableRolevalidation  bool
	logger                zerolog.Logger
	servers               *[]string
	oidcBaseURL           *string
	loginOIDCBaseURL      *string
	request_threshold     int
	request_window        time.Duration
	allow_origins         []string
	allow_methods         []string
	allow_headers         []string
	additionalMiddlewares []echo.MiddlewareFunc
	applicationName       string
	specifications        map[string]*apiSpecification
	customAPITokenChecker CustomApiAuthentication
	customErrorHandler    CustomErrorHandler
}

var defaultCORSAllowHeaders = []string{
	echo.HeaderAccept,
	echo.HeaderAuthorization,
	echo.HeaderContentType,
	echo.HeaderOrigin,
	"X-API-Key",
	"X-Correlation-Id",
	"ClientId",
	"X-Client-Id",
	"Last-Event-ID",
}

func WebServer(appName string) *APIBuilder {
	builder := &APIBuilder{
		echo:                 echo.New(),
		enablePrometheus:     true,
		enableSwagger:        true,
		enableOpenTelemetry:  false,
		request_threshold:    -1,
		request_window:       0,
		allow_origins:        []string{"*"},
		allow_methods:        []string{echo.GET, echo.POST, echo.PUT, echo.DELETE, echo.PATCH, echo.OPTIONS},
		allow_headers:        []string{},
		enableRolevalidation: false,
		applicationName:      appName,
		specifications:       make(map[string]*apiSpecification),
	}
	builder.echo.HideBanner = true
	builder.echo.Binder = &MergePatchBinder{Binder: builder.echo.Binder}
	builder.logger = zerolog.Nop() // dont log by default
	return builder
}

func (b *APIBuilder) registerSwaggerUIRoutes() {
	swaggerHandler := echoSwagger.EchoWrapHandler(func(cfg *echoSwagger.Config) {
		urls := []string{}
		for key := range b.specifications {
			urls = append(urls, key)
		}
		cfg.URLs = urls
	})

	serveSwaggerUI := func(c echo.Context, targetPath string) error {
		request := c.Request()
		originalPath := request.URL.Path
		originalURI := request.RequestURI
		request.URL.Path = targetPath
		request.RequestURI = request.URL.EscapedPath()
		defer func() {
			request.URL.Path = originalPath
			request.RequestURI = originalURI
		}()

		return swaggerHandler(c)
	}

	//-- serve swagger ui directly on /swagger
	b.echo.GET("/swagger", func(c echo.Context) error {
		return serveSwaggerUI(c, "/swagger/index.html")
	})

	//-- make /swagger path catch everything below it
	//-- and provide multiple specs in swagger-ui
	b.echo.GET("/swagger/*", func(c echo.Context) error {
		return serveSwaggerUI(c, c.Request().URL.Path)
	})
}

func (b *APIBuilder) Run(listenPort int) error {

	//-- fundamental middlewares
	b.echo.Use(SetCorrelationID())
	b.echo.Use(SourceIP())
	b.echo.Use(CorrelationContextEnricher(&b.logger))
	b.echo.Use(RequestLogger(&b.logger))
	if b.request_threshold > 0 {
		b.echo.Use(RateLimiter(b.redisCache, b.request_threshold, b.request_window))
	}

	// CORS must run before auth/validation middleware so error responses and
	// preflight requests still receive the configured Access-Control-* headers.
	b.echo.Use(echo4mw.CORSWithConfig(b.corsConfig()))

	//-- use opentelemetry
	if b.enableOpenTelemetry {
		b.echo.Use(otelecho.Middleware(b.applicationName,
			otelecho.WithMeterProvider(otel.GetMeterProvider()),
			otelecho.WithTracerProvider(otel.GetTracerProvider()),
		))

		b.echo.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {

				// -- prefer upstream Correlation-ID
				if cid := c.Request().Header.Get("X-Correlation-Id"); cid != "" {
					c.Set("correlation_id", cid)
					c.Response().Header().Set("X-Correlation-Id", cid)
					return next(c)
				}

				// -- Fallback: TraceID from OTel context
				span := trace.SpanFromContext(c.Request().Context())
				if sc := span.SpanContext(); sc.IsValid() {
					cid := sc.TraceID().String()
					c.Set("correlation_id", cid)
					c.Response().Header().Set("X-Correlation-Id", cid)
				}

				return next(c)
			}
		})
	}

	//-- load OpenAPI spec and convert to JSON
	if b.enableSwagger {
		b.registerSwaggerUIRoutes()
	}

	//-- register specifications
	for key, spec := range b.specifications {

		//-- if swagger is enabled, serve specification as .yaml and .json
		if b.enableSwagger {
			//-- encode as json
			jsonData, err := spec.doc.MarshalJSON()
			if err != nil {
				b.logger.Error().Err(err).Msg("Failed to marshal OpenAPI spec to JSON")
				return err
			}
			//-- encode as yaml
			yamlInterface, err := spec.doc.MarshalYAML()
			if err != nil {
				b.logger.Error().Err(err).Msg("Failed to marshal OpenAPI spec to YAML")
				return err
			}
			yamlData, err := yaml.Marshal(yamlInterface)
			if err != nil {
				b.logger.Error().Err(err).Msg("Failed to convert YAML interface to bytes")
				return err
			}
			b.echo.GET("/swagger/"+key, func(c echo.Context) error {
				return c.Blob(http.StatusOK, "application/yaml", yamlData)
			})
			b.echo.GET("/swagger/"+strings.TrimSuffix(key, ".yaml")+".json", func(c echo.Context) error {
				return c.Blob(http.StatusOK, "application/json", jsonData)
			})
		}

		//-- Add validation schema
		if spec.basePath != "" {
			b.echo.Use(AuthWithValidator(b.redisCache, spec.basePath, spec.doc, b.oidcBaseURL, b.loginOIDCBaseURL, b.customAPITokenChecker))
			// Check x-role annotations if enabled
			if b.enableRolevalidation {
				b.echo.Use(RoleValidator(spec.doc))
			}
		}

	}

	//-- Prometheus endpoint
	if b.enablePrometheus {
		b.echo.GET("/metrics", echo.WrapHandler(promhttp.Handler()))
	}

	for _, mw := range b.additionalMiddlewares {
		b.echo.Use(mw)
	}

	if b.customErrorHandler != nil {
		defaultHTTPErrorHandler := b.echo.DefaultHTTPErrorHandler
		b.echo.HTTPErrorHandler = func(err error, c echo.Context) {
			b.customErrorHandler(err, c)
			if !c.Response().Committed {
				defaultHTTPErrorHandler(err, c)
			}
		}
	}

	return b.echo.Start(fmt.Sprintf(":%d", listenPort))
}

func (b *APIBuilder) corsConfig() echo4mw.CORSConfig {
	allowCredentials := true
	if len(b.allow_origins) == 1 && b.allow_origins[0] == "*" {
		// Browsers reject credentialed CORS responses that use a wildcard origin.
		allowCredentials = false
	}

	log.Debug().Strs("allow_origins", b.allow_origins).
		Bool("allow_credentials", allowCredentials).Msg("CORS configuration")

	return echo4mw.CORSConfig{
		AllowOrigins:     b.allow_origins,
		AllowOriginFunc:  b.allowOrigin,
		AllowMethods:     b.allow_methods,
		AllowHeaders:     b.allow_headers,
		AllowCredentials: allowCredentials,
	}
}

func (b *APIBuilder) allowOrigin(origin string) (bool, error) {
	normalizedOrigin := normalizeCORSOrigin(origin)
	if normalizedOrigin == "" {
		b.logger.Debug().
			Str("origin", origin).
			Msg("CORS origin rejected because it is empty after normalization")
		return false, nil
	}

	for _, allowedOrigin := range b.allow_origins {
		if allowedOrigin == "*" {
			return true, nil
		}
		if allowedOrigin == normalizedOrigin || corsOriginPatternMatches(normalizedOrigin, allowedOrigin) {
			return true, nil
		}
	}

	b.logger.Debug().
		Str("origin", origin).
		Str("normalized_origin", normalizedOrigin).
		Strs("allow_origins", b.allow_origins).
		Msg("CORS origin rejected")

	return false, nil
}

func corsOriginPatternMatches(origin, pattern string) bool {
	if !strings.ContainsAny(pattern, "*?") {
		return false
	}

	quoted := regexp.QuoteMeta(pattern)
	quoted = strings.ReplaceAll(quoted, "\\*", ".*")
	quoted = strings.ReplaceAll(quoted, "\\?", ".")

	matcher, err := regexp.Compile("^" + quoted + "$")
	if err != nil {
		return false
	}

	return matcher.MatchString(origin)
}
