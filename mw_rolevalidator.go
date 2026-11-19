package webservice

// Middleware to enforce role-based access control based on OpenAPI spec extensions
// Usage:
// Add  x-roles to the specification

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/labstack/echo/v4"
)

func RoleValidator(spec *openapi3.T) echo.MiddlewareFunc {
	if spec == nil {
		return func(next echo.HandlerFunc) echo.HandlerFunc { return next }
	}
	router, initErr := gorillamux.NewRouter(spec)
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if initErr != nil {
				return echo.NewHTTPError(http.StatusInternalServerError).SetInternal(initErr)
			}
			route, _, err := router.FindRoute(c.Request())
			if errors.Is(err, routers.ErrPathNotFound) || errors.Is(err, routers.ErrMethodNotAllowed) {
				return next(c)
			}
			if err != nil {
				return err
			}
			if err := validateOperationRoles(c, route.Operation); err != nil {
				return err
			}
			return next(c)
		}
	}
}

func validateOperationRoles(c echo.Context, op *openapi3.Operation) error {
	required := rolesFromExtension(op.Extensions["x-roles"])
	if !hasRequiredRole(rolesFromContext(c), required) {
		return echo.NewHTTPError(http.StatusForbidden, "insufficient role")
	}
	return nil
}

func rolesFromContext(c echo.Context) []string {
	val := c.Get("roles")
	switch roles := val.(type) {
	case nil:
		return nil
	case []string:
		return roles
	case []interface{}:
		out := make([]string, 0, len(roles))
		for _, r := range roles {
			if rs, ok := r.(string); ok {
				out = append(out, rs)
			}
		}
		return out
	case string:
		return []string{roles}
	default:
		return nil
	}
}

func rolesFromExtension(raw interface{}) []string {
	switch v := raw.(type) {
	case []string:
		return v
	case []interface{}:
		return convertInterfaceSliceToStrings(v)
	case json.RawMessage:
		var roles []string
		if err := json.Unmarshal(v, &roles); err == nil {
			return roles
		}
		return nil
	default:
		if s, ok := v.(string); ok {
			return []string{s}
		}
		return nil
	}
}

func convertInterfaceSliceToStrings(values []interface{}) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if str, ok := value.(string); ok {
			result = append(result, str)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func hasRequiredRole(userRoles, requiredRoles []string) bool {
	if len(requiredRoles) == 0 {
		return true
	}
	if len(userRoles) == 0 {
		return false
	}

	roleSet := make(map[string]struct{}, len(userRoles))
	for _, role := range userRoles {
		role = strings.TrimSpace(role)
		if role == "" {
			continue
		}
		roleSet[role] = struct{}{}
	}

	for _, required := range requiredRoles {
		required = strings.TrimSpace(required)
		if required == "" {
			continue
		}
		if _, ok := roleSet[required]; !ok {
			return false
		}
	}

	return true
}
