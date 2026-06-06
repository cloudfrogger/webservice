package webservice

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	echo4mw "github.com/labstack/echo/v4/middleware"
)

func TestCORSPreflightUsesConfiguredAllowOriginAndAllowMethods(t *testing.T) {
	builder := NewWebServer("test").
		AllowOrigins("https://app.example.com").
		AllowMethods(http.MethodGet, http.MethodPost)

	e := echo.New()
	e.Use(echo4mw.CORSWithConfig(builder.corsConfig()))
	e.GET("/", func(c echo.Context) error {
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set(echo.HeaderOrigin, "https://app.example.com")
	req.Header.Set(echo.HeaderAccessControlRequestMethod, http.MethodPost)
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if got := rec.Header().Get(echo.HeaderAccessControlAllowOrigin); got != "https://app.example.com" {
		t.Fatalf("unexpected allow-origin header: %q", got)
	}

	allowMethods := strings.ReplaceAll(rec.Header().Get(echo.HeaderAccessControlAllowMethods), " ", "")
	if allowMethods != http.MethodGet+","+http.MethodPost {
		t.Fatalf("unexpected allow-methods header: %q", rec.Header().Get(echo.HeaderAccessControlAllowMethods))
	}
}

func TestSourceIPUsesCloudflareHeaderAndSetsContext(t *testing.T) {
	const cloudflareIP = "203.0.113.10"

	e := echo.New()
	e.Use(SourceIP())
	e.GET("/", func(c echo.Context) error {
		got, ok := c.Get(ContextKey_SourceIP).(string)
		if !ok {
			t.Fatalf("expected %q to be set in context", ContextKey_SourceIP)
		}
		if got != cloudflareIP {
			t.Fatalf("unexpected source IP in context: got %q want %q", got, cloudflareIP)
		}
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("CF-Connecting-IP", cloudflareIP)
	req.Header.Set("X-Forwarded-For", "198.51.100.7")
	req.RemoteAddr = "192.0.2.25:12345"
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusOK)
	}
}

func TestSourceIPFallsBackToRemoteAddrHost(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		want       string
	}{
		{
			name:       "ipv4 host and port",
			remoteAddr: "127.0.0.1:54321",
			want:       "127.0.0.1",
		},
		{
			name:       "ipv6 host and port",
			remoteAddr: "[::1]:54321",
			want:       "::1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			e.Use(SourceIP())
			e.GET("/", func(c echo.Context) error {
				got, ok := c.Get(ContextKey_SourceIP).(string)
				if !ok {
					t.Fatalf("expected %q to be set in context", ContextKey_SourceIP)
				}
				if got != tt.want {
					t.Fatalf("unexpected source IP in context: got %q want %q", got, tt.want)
				}
				return c.NoContent(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			rec := httptest.NewRecorder()

			e.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusOK)
			}
		})
	}
}

func TestRateLimiterBlocksRequestsAboveThresholdForSameSourceIP(t *testing.T) {
	const sourceIP = "203.0.113.10"

	e := echo.New()
	e.Use(SourceIP())
	e.Use(RateLimiter(nil, 2, time.Minute))
	e.GET("/", func(c echo.Context) error {
		return c.NoContent(http.StatusOK)
	})

	makeRequest := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("CF-Connecting-IP", sourceIP)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	if rec := makeRequest(); rec.Code != http.StatusOK {
		t.Fatalf("first request: got status %d want %d", rec.Code, http.StatusOK)
	}

	if rec := makeRequest(); rec.Code != http.StatusOK {
		t.Fatalf("second request: got status %d want %d", rec.Code, http.StatusOK)
	}

	if rec := makeRequest(); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third request: got status %d want %d", rec.Code, http.StatusTooManyRequests)
	}
}

func TestSetCorrelationIDPassesThroughRequestHeaderAndSetsContext(t *testing.T) {
	const correlationID = "cid-12345"

	e := echo.New()
	e.Use(SetCorrelationID())
	e.GET("/", func(c echo.Context) error {
		got, ok := c.Get(ContextKey_Correlation).(string)
		if !ok {
			t.Fatalf("expected %q to be set in context", ContextKey_Correlation)
		}
		if got != correlationID {
			t.Fatalf("unexpected correlation ID in context: got %q want %q", got, correlationID)
		}
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-CORRELATION-ID", correlationID)
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusOK)
	}

	if got := rec.Header().Get("X-CORRELATION-ID"); got != correlationID {
		t.Fatalf("unexpected correlation ID response header: got %q want %q", got, correlationID)
	}
}
