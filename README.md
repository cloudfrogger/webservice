# webservice

Webservice scaffold for use with openapi generated apis.

```bash
go get github.com/cloudfrogger/webservice
```

Features:
  - HTTP Authentication schemas from declaration
  - Swagger UI generation
  - CORS
  - Request Throttle / Ratelimiter can be configured 
  - Jaeger Tracing for endpoints
  - Proxy-Protocol and Cloudflare Source IP are provided in context
  - Rolevalidation from specification 
  - Correlation Injector compatible with cloudflare

## How to use
Cloudfrogger.Webserver wraps the echo webserver, redis, authentication etc. into a builder pattern easy
to use:

```golang
import github.com/cloudfrogger/webservice

func main() {
    err := webservice.NewWebServer("Example Server").
        RegisterHandler(func(e *echo.Echo) {
            // this is your openapi-generated handler
			v1.RegisterHandlers(e, handlers.NewHandlerV1())
		}).Run()
}
```

## Quick start from zero

Install openapi codegenerator cli
```
npm install @openapitools/openapi-generator-cli -g
```

Have your v1-api.yaml with OAS2 and a config in the projects openapi folder next to v1-config.yaml like so :  
```bash
package: v1
output: api/v1/api.gen.go

generate:
  echo-server: true
  models: true
  embedded-spec: true

output-options:
  nullable-type: true
```

Generate the api 
```
oapi-codegen -config openapi/v1-config.yaml openapi/v1-api.yaml
```

## Example use 

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
