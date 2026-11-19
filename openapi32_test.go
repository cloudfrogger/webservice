package webservice

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/labstack/echo/v4"
)

func TestOpenAPI32PublicOperations(t *testing.T) {
	b := NewWebServer("catalog").PublicAPI("/api", "testdata/openapi32.yaml", "catalog.yaml").WithRoleValidation(true)
	// Stand in for an authenticated caller with an auditor role.
	b.echo.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if role := c.Request().Header.Get("X-Test-Role"); role != "" {
				c.Set(ContextKey_Roles, []string{role})
			}
			return next(c)
		}
	})
	b.RegisterHandler(func(e *echo.Echo) {
		for _, method := range []string{"QUERY", "REPORT", "ARCHIVE"} {
			e.Add(method, "/api/items", func(c echo.Context) error { return c.NoContent(http.StatusNoContent) })
		}
	})
	if err := b.prepareServer(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, body, role string
		status             int
	}{
		{"QUERY", `{"name":"book"}`, "", 204},
		{"QUERY", `{}`, "", 400},
		{"REPORT", "", "", 403},
		{"REPORT", "", "auditor", 204},
		{"ARCHIVE", "", "", 405},
	} {
		req := httptest.NewRequest(tc.method, "/api/items", strings.NewReader(tc.body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		req.Header.Set("X-Test-Role", tc.role)
		rec := httptest.NewRecorder()
		b.echo.ServeHTTP(rec, req)
		if rec.Code != tc.status {
			t.Errorf("%s body=%q role=%q: got %d, want %d: %s", tc.method, tc.body, tc.role, rec.Code, tc.status, rec.Body)
		}
	}
	for _, ext := range []string{"yaml", "json"} {
		rec := httptest.NewRecorder()
		b.echo.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/swagger/catalog."+ext, nil))
		if rec.Code != 200 {
			t.Fatal(rec.Code)
		}
		doc, err := openapi3.NewLoader().LoadFromData(rec.Body.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		if err := doc.Validate(t.Context()); err != nil {
			t.Fatal(err)
		}
		item := doc.Paths.Value("/api/items")
		if doc.OpenAPI != "3.2.1" || doc.Info.Title != "catalog.yaml" || doc.Paths.Len() != 1 {
			t.Errorf("unexpected document metadata or paths in %s: %s", ext, rec.Body)
		}
		if item == nil || item.Query == nil || item.AdditionalOperations["REPORT"] == nil || len(item.AdditionalOperations) != 1 {
			t.Errorf("unexpected published %s: %s", ext, rec.Body)
		}
	}
}

func TestInvalidOpenAPISpecPreventsStartup(t *testing.T) {
	b := NewWebServer("catalog").PublicAPI("/api", t.TempDir()+"/missing.yaml", "catalog.yaml")
	if err := b.prepareServer(t.Context(), 0); err == nil {
		t.Fatal("expected a startup error for the missing spec")
	}
	b.PublicAPI("/api", "testdata/openapi32.yaml", "catalog.yaml")
	if err := b.prepareServer(t.Context(), 0); err != nil {
		t.Fatalf("could not replace the missing spec: %v", err)
	}
}

func TestRoleValidationUsesMatchingSpecAndOperation(t *testing.T) {
	b := NewWebServer("roles").WithRoleValidation(true)
	for base, role := range map[string]string{"/v1": "reader", "/v10": "auditor"} {
		path := filepath.Join(t.TempDir(), "spec.yaml")
		spec := fmt.Sprintf(`openapi: 3.2.1
info: {title: roles, version: '1'}
servers: [{url: %s}]
paths:
  /items/{id}:
    parameters:
      - {in: path, name: id, required: true, schema: {type: string}}
    query:
      x-roles: [%s]
      responses:
        '204': {description: OK}
`, base, role)
		if err := os.WriteFile(path, []byte(spec), 0600); err != nil {
			t.Fatal(err)
		}
		b.UseOpenAPISpecs(base, path, role+".yaml")
		b.echo.Add("QUERY", base+"/items/:itemID", func(c echo.Context) error { return c.NoContent(204) })
	}
	b.echo.Add("QUERY", "/unrelated/items/:id", func(c echo.Context) error { return c.NoContent(204) })
	b.echo.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(ContextKey_Roles, []string{c.Request().Header.Get("X-Test-Role")})
			return next(c)
		}
	})
	if err := b.prepareServer(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(b.echo)
	defer server.Close()
	for _, tc := range []struct {
		path, role string
		status     int
	}{
		{"/v1/items/1", "reader", 204},
		{"/v10/items/1", "auditor", 204},
		{"/v1/items/1", "auditor", 403},
		{"/v10/items/1", "reader", 403},
		{"/unrelated/items/1", "", 204},
	} {
		req, err := http.NewRequestWithContext(t.Context(), "QUERY", server.URL+tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Test-Role", tc.role)
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != tc.status {
			t.Errorf("%s with role %q: got %d, want %d", tc.path, tc.role, res.StatusCode, tc.status)
		}
	}
}

func TestInvalidOpenAPIDocumentReturnsStartupError(t *testing.T) {
	for _, tc := range []struct{ name, document, want string }{
		{"missing version", "openapi: 3.2.1\ninfo: {title: test}\npaths: {}", "version"},
		{"invalid expiration setting", `openapi: 3.0.3
info: {title: test, version: '1'}
paths: {}
components:
  securitySchemes:
    bearer: {type: http, scheme: bearer, x-jwt-require-exp: "true"}
`, "x-jwt-require-exp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid.yaml")
			if err := os.WriteFile(path, []byte(tc.document), 0600); err != nil {
				t.Fatal(err)
			}
			// A publish-only specification must also be valid.
			b := NewWebServer("invalid").UseOpenAPISpecs("", path, "doc.yaml")
			if err := b.Run(-1); err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unexpected startup error: %v", err)
			}
		})
	}
}
