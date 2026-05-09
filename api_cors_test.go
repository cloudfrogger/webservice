package webservice

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	echo4mw "github.com/labstack/echo/v4/middleware"
)

func TestCORSPreflightAllowsClientIDHeader(t *testing.T) {
	builder := WebServer("test").AllowOrigins("https://app.example.com")

	e := echo.New()
	e.Use(echo4mw.CORSWithConfig(builder.corsConfig()))
	e.GET("/", func(c echo.Context) error {
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set(echo.HeaderOrigin, "https://app.example.com")
	req.Header.Set(echo.HeaderAccessControlRequestMethod, http.MethodGet)
	req.Header.Set(echo.HeaderAccessControlRequestHeaders, "authorization,clientid")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if got := rec.Header().Get(echo.HeaderAccessControlAllowOrigin); got != "https://app.example.com" {
		t.Fatalf("unexpected allow-origin header: %q", got)
	}

	allowHeaders := strings.ToLower(rec.Header().Get(echo.HeaderAccessControlAllowHeaders))
	if !strings.Contains(allowHeaders, "clientid") {
		t.Fatalf("expected Access-Control-Allow-Headers to include clientid, got %q", allowHeaders)
	}
}

func TestCORSDisablesCredentialsForWildcardOrigins(t *testing.T) {
	builder := WebServer("test")

	cfg := builder.corsConfig()
	if cfg.AllowCredentials {
		t.Fatal("expected wildcard CORS config to disable credentials")
	}
}

func TestCORSWildcardAllowsAnyOrigin(t *testing.T) {
	builder := WebServer("test").AllowOrigins("*")

	e := echo.New()
	e.Use(echo4mw.CORSWithConfig(builder.corsConfig()))
	e.GET("/", func(c echo.Context) error {
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set(echo.HeaderOrigin, "https://random.example.com")
	req.Header.Set(echo.HeaderAccessControlRequestMethod, http.MethodGet)
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get(echo.HeaderAccessControlAllowOrigin); got != "https://random.example.com" {
		t.Fatalf("unexpected allow-origin header: %q", got)
	}
}

func TestCORSPreflightMatchesNormalizedConfiguredOrigin(t *testing.T) {
	builder := WebServer("test").AllowOrigins("https://app.example.com/")

	e := echo.New()
	e.Use(echo4mw.CORSWithConfig(builder.corsConfig()))
	e.GET("/", func(c echo.Context) error {
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set(echo.HeaderOrigin, "https://app.example.com")
	req.Header.Set(echo.HeaderAccessControlRequestMethod, http.MethodGet)
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if got := rec.Header().Get(echo.HeaderAccessControlAllowOrigin); got != "https://app.example.com" {
		t.Fatalf("unexpected allow-origin header: %q", got)
	}
}
