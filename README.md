# webservice

`webservice` is a small production-oriented scaffold for Go HTTP APIs built on top of [Echo](https://github.com/labstack/echo), OpenAPI, and a pragmatic middleware stack.

It is designed for services that already use generated OpenAPI handlers and want the repetitive platform wiring handled consistently:

- OpenAPI request validation
- JWT and API key authentication hooks
- Swagger UI and spec publishing
- CORS handling
- Prometheus metrics
- request correlation and structured logging
- source IP extraction behind Cloudflare or other proxies
- optional rate limiting
- optional OpenTelemetry tracing
- optional role validation from OpenAPI extensions

## Requirements

- Go `1.27+`
- an OpenAPI 3.0, 3.1, or 3.2 document for request validation

Install the package with:

```bash
go get github.com/cloudfrogger/webservice
```

## What The Package Does

`NewWebServer()` returns an `APIBuilder` that wires a standard Echo server with the middleware used by this package.

By default the server includes:

- correlation ID handling
- source IP extraction
- structured request logging
- CORS middleware
- Swagger UI at `/swagger`
- Prometheus metrics at `/metrics`

You opt into additional behavior by chaining builder methods such as `UseOpenAPISpecs`, `WithAuthentication`, `ThrottleRequests`, or `WithOpenTelemetry`.

## Quick Start

### 1. Generate Echo handlers from OpenAPI

This package is intended to work well with `oapi-codegen`.

Install it with:

```bash
go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest
```

Create an OpenAPI spec, for example `openapi/v1.yaml`:

```yaml
openapi: 3.2.1
info:
  title: Hello API
  version: 1.0.0
paths:
  /v1/hello:
    get:
      operationId: getHello
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
                properties:
                  message:
                    type: string
```

Create an `oapi-codegen` config such as `openapi/v1.codegen.yaml`:

```yaml
package: apiv1
output: internal/apiv1/api.gen.go
generate:
  echo-server: true
  models: true
  embedded-spec: true
```

Generate the code:

```bash
mkdir -p internal/apiv1
oapi-codegen -config openapi/v1.codegen.yaml openapi/v1.yaml
```

### 2. Start a service

```go
package main

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/cloudfrogger/webservice"
	"your/module/internal/apiv1"
)

type handler struct{}

func (h *handler) GetHello(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{"message": "Hello, World!"})
}

func main() {
	err := webservice.NewWebServer("hello-api").
		UseOpenAPISpecs("/v1", "openapi/v1.yaml", "v1.yaml").
		RegisterHandler(func(e *echo.Echo) {
			apiv1.RegisterHandlers(e, &handler{})
		}).
		Run(8080)

	if err != nil {
		panic(err)
	}
}
```

### 3. Verify the runtime endpoints

With the example above running:

- `GET /v1/hello` serves your handler
- `GET /swagger` serves Swagger UI
- `GET /swagger/v1.yaml` serves the YAML spec
- `GET /swagger/v1.json` serves the JSON version of the spec
- `GET /metrics` serves Prometheus metrics

## Common Configuration

```go
builder := webservice.NewWebServer("my-service").
	WithLogger(logger).
	UseRedisCache(redisClient).
	EnablePrometheus(true).
	EnableSwagger(true).
	WithAuthentication(oidcBaseURL, loginOIDCBaseURL).
	ThrottleRequests(100, 10*time.Minute).
	AllowOrigins("https://app.example.com").
	AllowMethods(http.MethodGet, http.MethodPost, http.MethodOptions).
	AllowHeaders("Authorization", "Content-Type", "ClientId").
	UseOpenAPISpecs("/v1", "openapi/v1.yaml", "v1.yaml").
	RegisterHandler(func(e *echo.Echo) {
		apiv1.RegisterHandlers(e, handlers.New())
	})

if enableTracing {
	builder = builder.WithOpenTelemetry()
}

if err := builder.Run(8080); err != nil {
	logger.Fatal().Err(err).Msg("server stopped")
}
```

## Builder Overview

The main builder methods are:

- `UseOpenAPISpecs(basePath, specPath, publicName)` registers an OpenAPI document, publishes it in Swagger, and enables request validation for matching routes.
- `PublicAPI(basePath, specPath, publicName)` is like `UseOpenAPISpecs`, but keeps only operations marked with `x-public: true`.
- `RegisterHandler(func(*echo.Echo))` registers your generated handlers or custom routes.
- `WithAuthentication(oidcBaseURL, loginOIDCBaseURL)` enables JWT validation using OIDC discovery.
- `CustomAPITokenValidation(func)` adds support for custom API key validation.
- `WithRoleValidation(true)` enforces `x-roles` OpenAPI extensions after authentication.
- `ThrottleRequests(limit, window)` enables per-source-IP rate limiting.
- `AllowOrigins`, `AllowMethods`, `AllowHeaders` configure CORS.
- `EnableSwagger(bool)` enables or disables Swagger UI and published spec endpoints.
- `EnablePrometheus(bool)` enables or disables `/metrics`.
- `WithOpenTelemetry()` enables Echo OpenTelemetry middleware. Your application is still responsible for configuring the global OTel provider.
- `OnEveryCall(middleware)` appends your own Echo middleware.
- `CustomGET(path, handler)` is a convenience for adding simple routes such as `/`.
- `ErrorHandler(func)` lets you run custom logic before Echo's default HTTP error handler.

## Behavioral Notes

### CORS

- Default allowed origins: `*`
- Default allowed methods: `GET`, `POST`, `PUT`, `DELETE`, `PATCH`, `OPTIONS`, `QUERY`
- If the effective CORS origin is `*`, credentials are disabled to keep browser behavior valid
- Preflight `OPTIONS` requests are handled before OpenAPI auth/validation middleware

### Source IP Handling

The source IP middleware uses this precedence:

1. `CF-Connecting-IP`
2. `X-Forwarded-For`
3. `RemoteAddr`

The final value is stored in the Echo context under `source_ip`.

### Correlation IDs

The correlation middleware uses this precedence:

1. `CF-RAY`
2. `X-CORRELATION-ID`
3. generated ID

The selected value is exposed in the response header `X-CORRELATION-ID` and in the Echo context as `correlation_id`.

### Rate Limiting

- Rate limiting is disabled until `ThrottleRequests()` is configured
- Limiting is keyed by the resolved source IP
- If a Redis client is configured, Redis is used
- Otherwise, an in-memory limiter is used

### OpenAPI 3.2

Register a `query` operation with `e.Add("QUERY", path, handler)`. Register a method from `additionalOperations` with the exact method name from the document. Both participate in request validation, role checks, and public API filtering. Include custom methods in `AllowMethods` for cross-origin browser requests.

The request validator uses `kin-openapi`, which currently implements a subset of OAS 3.2. Its model supports `query`, `additionalOperations`, and the media type field `itemSchema`. Request validation of `in: querystring` parameters is still unsupported. [Upstream support status](https://github.com/getkin/kin-openapi/tree/v0.153.0#readme)

### Authentication And Validation

- Requests under a spec's base path are validated against that document. Base paths match full URL path segments.
- With role validation enabled, the service checks `x-roles` on the operation selected by the request validator after authentication.
- Operations without security requirements skip authentication. Browser preflight requests skip request validation.
- Custom API key validation receives the request context, API token, and resolved source IP.
- During startup, the builder validates each OpenAPI document before applying runtime overrides or filtering public operations. `Run` and `RunHttps` return configuration, OIDC discovery, and JWKS initialization errors. Each OIDC HTTP request has a 10-second timeout.

A JWT with an expired `exp` claim is rejected. A JWT without `exp` is accepted by default. To require expiration for a security scheme, set the boolean extension `x-jwt-require-exp` on that scheme. Omitting the extension or setting it to `false` keeps expiration optional.

```yaml
components:
  securitySchemes:
    bearer:
      type: http
      scheme: bearer
      x-jwt-require-exp: true
security:
  - bearer: []
```

## Public API Filtering

`PublicAPI()` is useful when you want to publish only a subset of a larger spec. Operations without `x-public: true` are removed from the served document.

Example:

```yaml
paths:
  /status:
    get:
      x-public: true
      operationId: getStatus
      responses:
        "200":
          description: OK
  /internal/jobs:
    get:
      operationId: listJobs
      responses:
        "200":
          description: OK
```

With `PublicAPI(...)`, only `/status` remains in the published spec.

## Testing

Run the package test suite with:

```bash
go test ./...
```

## Scope

This package is intentionally opinionated. It is a good fit when you want a consistent service bootstrap around Echo and OpenAPI without rebuilding the same middleware chain in every project.
