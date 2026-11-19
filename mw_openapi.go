package webservice

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/labstack/echo/v4"
	echomiddleware "github.com/oapi-codegen/echo-middleware"
)

func newRequestValidators(ctx context.Context, specs map[string]*apiSpecification, oidcURL, loginURL *string, checker CustomApiAuthentication, checkRoles bool) ([]echo.MiddlewareFunc, error) {
	type scope struct {
		base   string
		router routers.Router
	}
	var scopes []scope
	for _, name := range slices.Sorted(maps.Keys(specs)) {
		spec := specs[name]
		if spec.loadErr != nil {
			return nil, spec.loadErr
		}
		if spec.basePath == "" {
			continue
		}
		if spec.doc == nil {
			return nil, fmt.Errorf("missing OpenAPI document %q", name)
		}
		router, err := gorillamux.NewRouter(spec.doc)
		if err != nil {
			return nil, fmt.Errorf("create OpenAPI router for %q: %w", name, err)
		}
		scopes = append(scopes, scope{spec.basePath, router})
	}
	if len(scopes) == 0 {
		return nil, nil
	}
	auth, err := newAuthenticator(ctx, oidcURL, loginURL, checker)
	if err != nil {
		return nil, err
	}
	var validators []echo.MiddlewareFunc
	for _, s := range scopes {
		validators = append(validators, scopedValidation(s.base, requestValidator(s.router, auth, checkRoles)))
	}
	return validators, nil
}

func scopedValidation(basePath string, validate echo.HandlerFunc) echo.MiddlewareFunc {
	base := strings.TrimRight(basePath, "/")
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			req := c.Request()
			if req.Method != http.MethodOptions && (req.URL.Path == base || strings.HasPrefix(req.URL.Path, base+"/")) {
				if err := validate(c); err != nil {
					return err
				}
			}
			return next(c)
		}
	}
}

// matchedOperation captures the router's result for this request only.
type matchedOperation struct {
	routers.Router
	operation *openapi3.Operation
}

func (m *matchedOperation) FindRoute(req *http.Request) (*routers.Route, map[string]string, error) {
	route, params, err := m.Router.FindRoute(req)
	if err == nil {
		m.operation = route.Operation
	}
	return route, params, err
}

func requestValidator(router routers.Router, auth openapi3filter.AuthenticationFunc, checkRoles bool) echo.HandlerFunc {
	opts := &echomiddleware.Options{Options: openapi3filter.Options{AuthenticationFunc: auth}}
	return func(c echo.Context) error {
		match := matchedOperation{Router: router}
		if err := echomiddleware.ValidateRequestFromContext(c, &match, opts); err != nil {
			return err
		}
		if checkRoles {
			return validateOperationRoles(c, match.operation)
		}
		return nil
	}
}
