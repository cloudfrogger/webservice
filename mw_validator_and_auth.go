package webservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	echomiddleware "github.com/oapi-codegen/echo-middleware"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// This middleware validates the request against the OpenAPI spec
// and also checks the Authorization header for a valid JWT token
func AuthWithValidator(_ *redis.Client, basePath string, spec *openapi3.T, oidcBaseURL,
	loginOIDCBaseURL *string,
	customAPITokenChecker CustomApiAuthentication) echo.MiddlewareFunc {

	var jwks *keyfunc.JWKS
	if oidcBaseURL != nil {
		jwksURL, err := resolveJWKSURL(*oidcBaseURL)
		if err != nil {
			log.Fatal().Err(err).Msgf("Failed to resolve JWKS endpoint from %s", *oidcBaseURL)
		}
		jwks, err = keyfunc.Get(jwksURL, keyfunc.Options{
			RefreshInterval:   time.Hour,
			RefreshUnknownKID: true,
		})
		if err != nil || jwks == nil {
			log.Fatal().Err(err).Msgf("Failed to initialize JWKS from %s", jwksURL)
		}
	}

	var loginJWKS *keyfunc.JWKS
	if loginOIDCBaseURL != nil {
		jwksURL, err := resolveJWKSURL(*loginOIDCBaseURL)
		if err != nil {
			log.Fatal().Err(err).Msgf("Failed to resolve JWKS endpoint from %s", *loginOIDCBaseURL)
		}
		loginJWKS, err = keyfunc.Get(jwksURL, keyfunc.Options{
			RefreshInterval:   time.Hour,
			RefreshUnknownKID: true,
		})
		if err != nil {
			log.Fatal().Err(err).Msgf("Failed to initialize JWKS from %s", jwksURL)
		}
	}

	return echomiddleware.OapiRequestValidatorWithOptions(spec, &echomiddleware.Options{
		Options: openapi3filter.Options{
			AuthenticationFunc: func(ctx context.Context, input *openapi3filter.AuthenticationInput) error {

				// Skip authentication if no security requirements are defined
				if len(input.SecurityScheme.Type) == 0 {
					log.Debug().Msg("No security requirements defined, skipping authentication")
					return nil
				}

				if oidcBaseURL == nil {
					log.Debug().Msg("No OIDC configuration found, rejecting requests. No further info provided in the response for security reasons.")
					return echo.NewHTTPError(http.StatusUnauthorized, "")
				}

				echoCtx := echomiddleware.GetEchoContext(ctx)
				if echoCtx == nil {
					return echo.NewHTTPError(http.StatusInternalServerError, "missing request context")
				}

				auth := echoCtx.Request().Header.Get("Authorization")

				// If no Bearer is present, it might be an API token
				if !strings.HasPrefix(auth, "Bearer ") {

					apiToken := echoCtx.Request().Header.Get("X-API-Key")

					//-- Try to get API token from query parameter
					if apiToken != "" && customAPITokenChecker != nil {

						//-- for API token validation, also provide the source IP address
						sourceIP := ""
						if echoCtx.Get(ContextKey_SourceIP) != nil {
							sourceIP = echoCtx.Get(ContextKey_SourceIP).(string)
						}
						email, firstname, lastname, valid := customAPITokenChecker(echoCtx.Request().Context(), apiToken, sourceIP)

						//-- login via auth token permitted
						if valid {
							log.Debug().Msgf("API token authentication successful for email: %s", email)
							echoCtx.Set(ContextKey_UserEmail, email)
							echoCtx.Set(ContextKey_GivenName, firstname)
							echoCtx.Set(ContextKey_FamilyName, lastname)
							return nil
						}
					} else {
						//TODO: Invalid requests of this sort should be monitored more closely
						log.Debug().Str("authorization", auth).Msg("Request did not provide any authentication token")
						return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
					}
				}

				tokenStr := strings.TrimPrefix(auth, "Bearer ")
				token, err := jwt.Parse(tokenStr, jwks.Keyfunc)
				if err != nil || token == nil || !token.Valid {
					logJWTValidationFailure("oidc", err, tokenStr)
					if loginJWKS == nil {
						log.Error().Msg("JWT validation failed and no login OIDC configuration found, rejecting request. No further info provided in the response for security reasons.")
						return echo.NewHTTPError(http.StatusUnauthorized, "invalid token")
					}
					token, err = jwt.Parse(tokenStr, loginJWKS.Keyfunc)
					if err != nil || token == nil || !token.Valid {
						log.Error().Msg("JWT validation failed for both OIDC and login OIDC configurations, rejecting request. No further info provided in the response for security reasons.")
						logJWTValidationFailure("login_oidc", err, tokenStr)
						return echo.NewHTTPError(http.StatusUnauthorized, "invalid token")
					}
				}

				claims, ok := token.Claims.(jwt.MapClaims)
				if !ok {
					return echo.NewHTTPError(http.StatusUnauthorized, "invalid token claims")
				}

				setUserContextFromClaims(echoCtx, claims)

				return nil
			},
		},
		Skipper: func(c echo.Context) bool {
			// Let Echo's CORS middleware answer browser preflight requests without
			// forcing auth or OpenAPI validation first.
			if c.Request().Method == http.MethodOptions {
				return true
			}

			// skip if the configured basepath doesnt match
			// this is needed to validate multiple specs
			return !strings.HasPrefix(c.Path(), basePath)
		},
	})
}

type oidcDiscovery struct {
	JWKSURI string `json:"jwks_uri"`
}

func resolveJWKSURL(base string) (string, error) {
	if base == "" {
		return "", errors.New("missing OIDC base URL")
	}

	discovery := strings.TrimSuffix(base, "/")
	if !strings.Contains(discovery, "/.well-known/") {
		discovery = fmt.Sprintf("%s/.well-known/openid-configuration", discovery)
	}

	req, err := http.NewRequest(http.MethodGet, discovery, nil)
	if err != nil {
		return "", fmt.Errorf("build discovery request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch discovery document: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discovery endpoint returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read discovery document: %w", err)
	}

	var doc oidcDiscovery
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("decode discovery document: %w", err)
	}
	if doc.JWKSURI == "" {
		return "", errors.New("discovery document missing jwks_uri")
	}
	return doc.JWKSURI, nil
}

func logJWTValidationFailure(source string, validationErr error, tokenStr string) {
	event := log.Debug().Str("jwt_source", source)
	if validationErr != nil {
		event = event.Err(validationErr)
	}

	claims := jwt.MapClaims{}
	token, _, parseErr := new(jwt.Parser).ParseUnverified(tokenStr, claims)
	if parseErr != nil {
		event.Str("jwt_parse_unverified_error", parseErr.Error()).Msg("JWT validation failed")
		return
	}

	if token != nil {
		if alg, ok := token.Header["alg"].(string); ok {
			event = event.Str("jwt_alg", alg)
		}
		if kid, ok := token.Header["kid"].(string); ok {
			event = event.Str("jwt_kid", kid)
		}
	}

	event = addJWTClaimString(event, claims, "iss", "jwt_iss")
	event = addJWTClaimAudience(event, claims)
	event = addJWTClaimTime(event, claims, "exp", "jwt_exp")
	event = addJWTClaimTime(event, claims, "nbf", "jwt_nbf")
	event = addJWTClaimTime(event, claims, "iat", "jwt_iat")
	event.Msg("JWT validation failed")
}

func addJWTClaimString(event *zerolog.Event, claims jwt.MapClaims, claimName, fieldName string) *zerolog.Event {
	if value, ok := claims[claimName].(string); ok {
		return event.Str(fieldName, value)
	}
	return event
}

func addJWTClaimAudience(event *zerolog.Event, claims jwt.MapClaims) *zerolog.Event {
	switch aud := claims["aud"].(type) {
	case string:
		return event.Str("jwt_aud", aud)
	case []any:
		values := make([]string, 0, len(aud))
		for _, value := range aud {
			if s, ok := value.(string); ok {
				values = append(values, s)
			}
		}
		if len(values) > 0 {
			return event.Strs("jwt_aud", values)
		}
	}
	return event
}

func addJWTClaimTime(event *zerolog.Event, claims jwt.MapClaims, claimName, fieldName string) *zerolog.Event {
	value, ok := claims[claimName].(float64)
	if !ok {
		return event
	}
	return event.Time(fieldName, time.Unix(int64(value), 0).UTC())
}

func setUserContextFromClaims(c echo.Context, claims jwt.MapClaims) {

	if sub, ok := claims["sub"].(string); ok {
		uid, _ := uuid.Parse(sub)
		c.Set(ContextKey_UserID, uid)
	}
	if email, ok := claims["email"].(string); ok {
		c.Set(ContextKey_UserEmail, email)
	}
	if given, ok := claims["given_name"].(string); ok {
		c.Set(ContextKey_GivenName, given)
	}
	if family, ok := claims["family_name"].(string); ok {
		c.Set(ContextKey_FamilyName, family)
	}
	if verified, ok := claims["email_verified"].(bool); ok {
		c.Set(ContextKey_EmailVerified, verified)
	}

	c.Set(ContextKey_Roles, extractRoles(claims))
}

func extractRoles(claims jwt.MapClaims) []string {
	realmAccess, ok := claims["realm_access"].(map[string]any)
	if !ok {
		return nil
	}

	rawRoles, ok := realmAccess["roles"].([]any)
	if !ok {
		return nil
	}

	roles := make([]string, 0, len(rawRoles))
	for _, role := range rawRoles {
		if s, ok := role.(string); ok {
			roles = append(roles, s)
		}
	}
	return roles
}
