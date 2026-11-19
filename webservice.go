package webservice

import (
	"context"
	"embed"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/labstack/echo-otel/v4"
	"github.com/labstack/echo/v4"
	echo4mw "github.com/labstack/echo/v4/middleware"
	"github.com/oasdiff/yaml"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/trace"
)

type OapiRegisterFunc func(*echo.Echo)

type CustomApiAuthentication func(ctx context.Context, apitoken, sourceIP string) (email string, firstname string, lastname string, valid bool)
type CustomErrorHandler func(err error, c echo.Context)

type apiSpecification struct {
	basePath string
	doc      *openapi3.T
	loadErr  error
}

type embeddedWebFS struct {
	path           string
	fs             embed.FS
	filesystemPath string
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
	webFS                 []embeddedWebFS
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

func NewWebServer(appName string) *APIBuilder {
	builder := &APIBuilder{
		echo:                 echo.New(),
		enablePrometheus:     true,
		enableSwagger:        true,
		enableOpenTelemetry:  false,
		request_threshold:    -1,
		request_window:       0,
		allow_origins:        []string{"*"},
		allow_methods:        []string{echo.GET, echo.POST, echo.PUT, echo.DELETE, echo.PATCH, echo.OPTIONS, openapi3.MethodQuery},
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

// Run with TLS using local certificate and key files
func (b *APIBuilder) RunHttps(listenPort int, certFile interface{}, keyFile interface{}) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := b.prepareServer(ctx, listenPort)
	if err != nil {
		return err
	}
	return b.echo.StartTLS(fmt.Sprintf(":%d", listenPort), certFile, keyFile)
}

func (b *APIBuilder) Run(listenPort int) error {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := b.prepareServer(ctx, listenPort)
	if err != nil {
		return err
	}
	return b.echo.Start(fmt.Sprintf(":%d", listenPort))
}

func (b *APIBuilder) prepareServer(ctx context.Context, listenPort int) error {
	validators, err := newRequestValidators(ctx, b.specifications, b.oidcBaseURL, b.loginOIDCBaseURL, b.customAPITokenChecker, b.enableRolevalidation)
	if err != nil {
		return err
	}

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
		telemetry, err := (echootel.Config{ServerName: b.applicationName}).ToMiddleware()
		if err != nil {
			return err
		}
		b.echo.Use(telemetry)

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

	}
	b.echo.Use(validators...)

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

	for _, webFS := range b.webFS {
		if webFS.filesystemPath != "" {
			b.echo.Static(webFS.path, webFS.filesystemPath)
		}
		if webFS.fs != (embed.FS{}) {
			b.echo.StaticFS(webFS.path, webFS.fs)
		}
	}

	return nil
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
