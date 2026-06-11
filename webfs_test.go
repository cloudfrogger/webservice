package webservice

import (
	"embed"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

//go:embed testdata/webfs/hello.txt
var testWebFS embed.FS

//go:embed testdata/adminfs/dashboard.txt
var testAdminWebFS embed.FS

func TestEmbedFSServesMultipleStaticFileSystems(t *testing.T) {
	localDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(localDir, "local.txt"), []byte("hello from local filesystem"), 0o644); err != nil {
		t.Fatalf("failed to write local file fixture: %v", err)
	}

	builder := NewWebServer("test").
		EnablePrometheus(false).
		EnableSwagger(false)
	builder.EmbedFS("/web", testWebFS)
	builder.EmbedFS("/admin", testAdminWebFS)
	builder.AddFilesystemPath("/local", localDir)

	if err := builder.prepareServer(t.Context(), 0); err != nil {
		t.Fatalf("prepareServer failed: %v", err)
	}

	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "web fs",
			path: "/web/testdata/webfs/hello.txt",
			want: "hello from embedded webfs",
		},
		{
			name: "admin fs",
			path: "/admin/testdata/adminfs/dashboard.txt",
			want: "hello from admin webfs",
		},
		{
			name: "local fs",
			path: "/local/local.txt",
			want: "hello from local filesystem",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()

			builder.echo.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusOK)
			}

			if got := strings.TrimSpace(rec.Body.String()); got != tt.want {
				t.Fatalf("unexpected body: got %q", got)
			}
		})
	}
}
