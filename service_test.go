package webservice_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"webservice"
)

func TestDefaultHandlerServesHealthcheck(t *testing.T) {
	t.Parallel()

	svc := webservice.New()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	svc.Handler().ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}

	if got := strings.TrimSpace(string(body)); got != "ok" {
		t.Fatalf("body = %q, want %q", got, "ok")
	}
}
