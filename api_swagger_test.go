package webservice

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestSwaggerExactSpecRouteWinsOverWildcard(t *testing.T) {
	e := echo.New()

	e.GET("/swagger/*", func(c echo.Context) error {
		return c.String(http.StatusOK, "wildcard")
	})
	e.GET("/swagger/API v1", func(c echo.Context) error {
		return c.String(http.StatusOK, "spec")
	})

	req := httptest.NewRequest(http.MethodGet, "/swagger/API%20v1", nil)
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if rec.Body.String() != "spec" {
		t.Fatalf("expected exact route to match, got %q", rec.Body.String())
	}
}

func TestSwaggerRootServesUIWithoutRedirect(t *testing.T) {
	builder := NewWebServer("test")
	builder.registerSwaggerUIRoutes()

	req := httptest.NewRequest(http.MethodGet, "/swagger?urls.primaryName=API%20v1", nil)
	rec := httptest.NewRecorder()

	builder.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if location := rec.Header().Get("Location"); location != "" {
		t.Fatalf("expected no redirect, got %q", location)
	}
	if !strings.Contains(rec.Body.String(), "SwaggerUIBundle") {
		t.Fatal("expected swagger index html")
	}
}

func TestUseOpenAPISpecsFindsSpecFromParentDirectory(t *testing.T) {
	tempDir := t.TempDir()
	specPath := filepath.Join(tempDir, "openapi", "v1-api.yaml")
	if err := os.MkdirAll(filepath.Dir(specPath), 0o755); err != nil {
		t.Fatalf("failed to create spec directory: %v", err)
	}
	specContent := "openapi: 3.0.3\ninfo:\n  title: test\n  version: 1.0.0\npaths: {}\n"
	if err := os.WriteFile(specPath, []byte(specContent), 0o644); err != nil {
		t.Fatalf("failed to write spec: %v", err)
	}

	nestedDir := filepath.Join(tempDir, "cmd")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("failed to create nested directory: %v", err)
	}

	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	defer func() {
		if chdirErr := os.Chdir(originalWD); chdirErr != nil {
			t.Fatalf("failed to restore working directory: %v", chdirErr)
		}
	}()

	if err := os.Chdir(nestedDir); err != nil {
		t.Fatalf("failed to change working directory: %v", err)
	}

	builder := NewWebServer("test")
	builder.UseOpenAPISpecs("/v1", "openapi/v1-api.yaml", "API v1")

	spec, ok := builder.specifications["API v1"]
	if !ok {
		t.Fatal("expected openapi spec to be registered")
	}
	if spec.doc == nil {
		t.Fatal("expected openapi document to be loaded")
	}
}
