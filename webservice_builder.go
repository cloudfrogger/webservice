package webservice

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/labstack/echo/v4"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func (b *APIBuilder) ErrorHandler(f CustomErrorHandler) *APIBuilder {
	b.customErrorHandler = f
	return b
}

func (b *APIBuilder) CustomAPITokenValidation(f CustomApiAuthentication) *APIBuilder {
	b.customAPITokenChecker = f
	return b
}

func (b *APIBuilder) ClearServers() *APIBuilder {
	b.servers = &[]string{}
	return b
}

func (b *APIBuilder) CustomGET(path string, handler echo.HandlerFunc) *APIBuilder {
	b.echo.GET(path, handler)
	return b
}

func (b *APIBuilder) WithCache(redisClient *redis.Client) *APIBuilder {
	b.redisCache = redisClient
	return b
}

func (b *APIBuilder) WithLogger(logger zerolog.Logger) *APIBuilder {
	b.logger = logger
	return b
}

// -- if set a middleware will keep pushing metrics trhu otoel collector
// -- in order for this to work the collector needs to be initialized in main
func (b *APIBuilder) WithOpenTelemetry() *APIBuilder {
	b.enableOpenTelemetry = true
	return b
}

// WithRoleValidation enables or disables role validation middleware
// Requires x-role annotations in OpenAPI spec
func (b *APIBuilder) WithRoleValidation(enable bool) *APIBuilder {
	b.enableRolevalidation = enable
	return b
}

// WithAuthentication enables OIDC authentication with the given OIDC base URL
func (b *APIBuilder) WithAuthentication(oidcBaseURL string, loginOIDCBaseURL string) *APIBuilder {
	b.oidcBaseURL = &oidcBaseURL
	if oidcBaseURL != loginOIDCBaseURL {
		b.loginOIDCBaseURL = &loginOIDCBaseURL
	}
	return b
}

// ThrottleRequests enables request throttling with the given threshold and window
func (b *APIBuilder) ThrottleRequests(threshold int, window time.Duration) *APIBuilder {
	b.request_threshold = threshold
	b.request_window = window
	return b
}

// AllowOrigins sets the allowed origins for CORS
func (b *APIBuilder) AllowOrigins(origins ...string) *APIBuilder {
	normalized := make([]string, 0, len(origins))
	seen := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		candidate := normalizeCORSOrigin(origin)
		if candidate == "" {
			continue
		}
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		normalized = append(normalized, candidate)
	}
	if len(normalized) == 0 {
		b.allow_origins = []string{"*"}
		return b
	}
	b.allow_origins = normalized
	return b
}

func (b *APIBuilder) AllowMethods(methods ...string) *APIBuilder {
	b.allow_methods = methods
	return b
}

func (b *APIBuilder) AllowHeaders(headers ...string) *APIBuilder {
	b.allow_headers = headers
	return b
}

func (b *APIBuilder) UseOpenAPISpecs(basePath string, oapiSpecificationPath string, publicName string) *APIBuilder {

	if _, exists := b.specifications[publicName]; exists {
		b.logger.Warn().Msgf("OpenAPI spec with name %s already exists, overwriting", publicName)
	}

	doc, err := openapi3.NewLoader().LoadFromFile(resolveOpenAPISpecPath(oapiSpecificationPath))
	if err != nil {
		b.logger.Error().Err(err).Msg("Failed to load OpenAPI spec")
		return b
	}

	// Update the spec with runtime servers if configured
	if b.servers != nil {
		doc.Servers = []*openapi3.Server{}
		for _, server := range *b.servers {
			doc.Servers = append(doc.Servers, &openapi3.Server{
				URL: server,
			})
		}
	}

	// Update the document title/info with the public name
	if doc.Info != nil {
		doc.Info.Title = publicName
	}

	b.specifications[publicName] = &apiSpecification{
		basePath: basePath,
		doc:      doc, // will be loaded later
	}
	return b
}

func (b *APIBuilder) PublicAPI(basePath string, oapiSpecificationPath string, publicName string) *APIBuilder {

	b.UseOpenAPISpecs(basePath, oapiSpecificationPath, publicName)

	doc := b.specifications[publicName].doc

	if doc == nil {
		log.Fatal().Msgf("Spec %s not loaded", publicName)
	}

	// Remove paths that don't have x-public: true
	for path, pathItem := range doc.Paths.Map() {
		hasPublic := false

		// Check all operations in the path
		for _, op := range []*openapi3.Operation{
			pathItem.Get, pathItem.Post, pathItem.Put,
			pathItem.Delete, pathItem.Patch, pathItem.Head,
			pathItem.Options, pathItem.Trace,
		} {
			if op != nil {
				if xPublic, ok := op.Extensions["x-public"]; ok {
					if public, ok := xPublic.(bool); ok && public {
						hasPublic = true
						break
					}
				}
			}
		}

		// Remove path if no operation has x-public: true
		if !hasPublic {
			delete(doc.Paths.Map(), path)
		}
	}

	b.specifications[publicName] = &apiSpecification{
		basePath: basePath,
		doc:      doc,
	}

	return b
}

// Add a server URL if the condition is true
func (b *APIBuilder) AddServerIf(condition bool, server string) *APIBuilder {
	if condition {
		if b.servers == nil {
			b.servers = &[]string{}
		}
		*b.servers = append(*b.servers, server)
	}
	return b
}

func (b *APIBuilder) WithPrometheus(enable bool) *APIBuilder {
	b.enablePrometheus = enable
	return b
}

func (b *APIBuilder) WithApplicationName(appName string) *APIBuilder {
	b.applicationName = appName
	return b
}

func (b *APIBuilder) WithSwagger(enable bool) *APIBuilder {
	b.enableSwagger = enable
	return b
}

func (b *APIBuilder) RegisterHandler(handler func(*echo.Echo)) *APIBuilder {
	handler(b.echo)
	return b
}

// Add some Echo Middleware to be executed on every call
func (b *APIBuilder) OnEveryCall(handler echo.MiddlewareFunc) *APIBuilder {
	b.additionalMiddlewares = append(b.additionalMiddlewares, handler)
	return b
}

func normalizeCORSOrigin(origin string) string {
	trimmed := strings.TrimSpace(origin)
	if trimmed == "" {
		return ""
	}
	if trimmed == "*" {
		return trimmed
	}

	parsed, err := url.Parse(trimmed)
	if err == nil && parsed.Scheme != "" && parsed.Host != "" {
		return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host)
	}

	return strings.TrimRight(trimmed, "/")
}

func resolveOpenAPISpecPath(specPath string) string {
	if specPath == "" || filepath.IsAbs(specPath) {
		return specPath
	}

	cleaned := filepath.Clean(specPath)
	if fileExists(cleaned) {
		return cleaned
	}

	if wd, err := os.Getwd(); err == nil {
		if candidate, ok := findPathInParentDirs(wd, cleaned); ok {
			return candidate
		}
	}

	if executable, err := os.Executable(); err == nil {
		if candidate, ok := findPathInParentDirs(filepath.Dir(executable), cleaned); ok {
			return candidate
		}
	}

	return cleaned
}

func findPathInParentDirs(startDir string, relPath string) (string, bool) {
	currentDir := filepath.Clean(startDir)
	for {
		candidate := filepath.Join(currentDir, relPath)
		if fileExists(candidate) {
			return candidate, true
		}

		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			return "", false
		}
		currentDir = parentDir
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
