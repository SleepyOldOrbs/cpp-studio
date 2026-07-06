package demo

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesIndex(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, "text/html") {
		t.Fatalf("expected HTML content type, got %q", contentType)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "cpp-studio local studio") {
		t.Fatalf("expected index HTML marker, got %q", body)
	}
	if !strings.Contains(body, "/v1/images/generations") {
		t.Fatalf("expected image route marker, got %q", body)
	}
	if !strings.Contains(body, "imageErrorBox") {
		t.Fatalf("expected image error marker, got %q", body)
	}
}

func TestHandlerServesAssets(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		contentType string
		needle      string
	}{
		{
			name:        "javascript",
			path:        "/app.js",
			contentType: "javascript",
			needle:      "generateImage",
		},
		{
			name:        "css",
			path:        "/styles.css",
			contentType: "text/css",
			needle:      ".image-panel",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)

			Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
			}
			if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, tt.contentType) {
				t.Fatalf("expected content type containing %q, got %q", tt.contentType, contentType)
			}
			if body := rec.Body.String(); !strings.Contains(body, tt.needle) {
				t.Fatalf("expected asset marker %q, got %q", tt.needle, body)
			}
		})
	}
}
