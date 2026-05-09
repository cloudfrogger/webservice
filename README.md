# webservice

A production-ready web service scaffold for OpenAPI-generated REST APIs in Go.

```bash
go get github.com/cloudfrogger/webservice
```

## Features

- HTTP authentication schemas from OpenAPI specifications
- Swagger UI generation
- CORS support
- Request throttling and rate limiting
- Jaeger distributed tracing
- Proxy Protocol and Cloudflare source IP handling
- Role-based access control validation
- Correlation ID injection for Cloudflare compatibility

## Quick Start

### 1. Install openapi keygen
```
npm install @openapitools/openapi-generator-cli -g
```

### 2. Define Your API

Create an OpenAPI 3.0.0 specification (`openapi/v1-api.yaml`):

```yaml
openapi: 3.0.0
info:
	title: Hello World API
	version: 1.0.0
servers:
	- url: http://localhost:8000
paths:
	/hello:
		get:
			operationId: getHello
			responses:
				'200':
					description: Success
					content:
						application/json:
							schema:
								type: object
								properties:
									message:
										type: string
```
### 3. Configure the generator
```yaml
package: v1
output: api/v1/api.gen.go

generate:
  echo-server: true
  models: true
  embedded-spec: true

output-options:
  nullable-type: true
```

### 4. Generate API Code

```bash
oapi-codegen -config openapi/v1-config.yaml openapi/v1-api.yaml
```

### 3. Minimal main.go
Serve the hello world service on 8080:

```go
import "github.com/cloudfrogger/webservice"

type handler struct{}

func (h *handler) GetHello(ctx echo.Context) error {
		return ctx.JSON(200, map[string]string{"message": "Hello, World!"})
}

func main() {
		webservice.NewWebServer("My API").
				RegisterHandler(func(e *echo.Echo) {
						v1.RegisterHandlers(e, &handler{})
				}).
				Run(8080)
}
```
### 4. Test it it works
Run with: `go run cmd/main.go` and visit `http://localhost:8080/hello`

## Configuration Example

```go
builder := webservice.NewWebServer("My Service").
		WithLogger(logger).
		WithCache(redis).
		WithPrometheus(true).
		WithAuthentication(oidcURL, oidcURL).
		ThrottleRequests(100, 10*time.Minute).
		AllowOrigins("*").
		UseOpenAPISpecs("/v1", "openapi/v1-api.yaml", "API v1").
		WithSwagger(true).
		RegisterHandler(func(e *echo.Echo) {
				v1.RegisterHandlers(e, handlers.New())
		}).
		Run(8080)
```



## A more complete example

```golang
	builder := webservice.NewWebServer(APPLICATION_NAME).
		WithLogger(log.Logger).
		WithCache(redisClient).
		WithPrometheus(config.Prometheus.Enable).
		WithAuthentication(config.OIDC.BaseURL, config.OIDC.BaseURL).
		ErrorHandler(func(err error, c echo.Context) {
			log.Debug().Err(err).Msg("An error occurred while processing the request")
			//-- custom error handler to log errors
			c.Logger().Error(err)
		}).
		CustomAPITokenValidation(func(ctx context.Context, apiToken, sourceIP string) (email string, firstname string, lastname string, valid bool) {
			// use this to validate API tokens for service accounts
			return "", "", "", false
		}).
		ThrottleRequests(config.RequestThrottle, 10*time.Minute).
		AllowOrigins(config.PermittedOrigins...).
		AllowMethods(config.PermittedMethods...).
		AllowHeaders(config.PermittedHeaders...).
		ClearServers().
		//AddServerIf(config.LocalEnvironment, "http://localhost:5400/v1").
		//AddServerIf(!config.LocalEnvironment, config.APIBaseURL).
		UseOpenAPISpecs("/v1", "openapi/v1-api.yaml", "Some api defintion API v1").
		WithSwagger(true).
		RegisterHandler(func(e *echo.Echo) {
			v1.RegisterHandlers(e, handlers.NewHandlerV1(
				chatService,
				config.Bot.APIToken,
			))
		}).
		CustomGET("/", func(ctx echo.Context) error {
			// A small mainpage in case a developer  opens the API in a browser
			return ctx.String(200, "ToDo: Edit me!")
		}).
		OnEveryCall(func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(ctx echo.Context) error {
				if emailValue := ctx.Get(api.ContextKey_UserEmail); emailValue != nil {
					firstName := ""
					lastName := ""
					if ctx.Get(api.ContextKey_GivenName) != nil {
						firstName = ctx.Get(api.ContextKey_GivenName).(string)
					}
					if ctx.Get(api.ContextKey_FamilyName) != nil {
						lastName = ctx.Get(api.ContextKey_FamilyName).(string)
					}
					if _, err := chatService.EnsureUser(ctx.Request().Context(), emailValue.(string), firstName, lastName); err != nil {
						log.Warn().Err(err).Msg("Failed to synchronize authenticated user")
					}
				}
				return next(ctx)
			}
		})

	if config.OpenTelemetry.Enable {
		builder = builder.WithOpenTelemetry()
	}

	err = builder.Run(config.APIPort)

	if err != nil {
		log.Error().Err(err).Msg("Exit")
	}
```
