package hub

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSecurityHeaders(t *testing.T) {
	ui := fstest.MapFS{"index.html": {Data: []byte("<h1>Regulus</h1>")}}
	h := NewHandler(Deps{UI: ui})
	for _, path := range []string{"/", "/healthz", "/api/database", "/api/agents", "/api/auth/me", "/no/such/file"} {
		rec := get(t, h, path)
		for name, want := range map[string]string{
			"X-Frame-Options":        "DENY",
			"X-Content-Type-Options": "nosniff",
			"Referrer-Policy":        "no-referrer",
		} {
			if got := rec.Header().Get(name); got != want {
				t.Errorf("%s: %s = %q, want %q", path, name, got, want)
			}
		}
		csp := rec.Header().Get("Content-Security-Policy")
		for _, want := range []string{"default-src 'self'", "frame-ancestors 'none'", "object-src 'none'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("%s: Content-Security-Policy %q lacks %q", path, csp, want)
			}
		}
		if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
			t.Errorf("%s: the policy must not allow unsafe code: %q", path, csp)
		}
		if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
			t.Errorf("%s: HSTS over plain HTTP: %q", path, got)
		}
	}
}

func TestStrictTransportSecurityOnlyOverHTTPS(t *testing.T) {
	h := NewHandler(Deps{UI: fstest.MapFS{"index.html": {Data: []byte("x")}}})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Strict-Transport-Security"); !strings.HasPrefix(got, "max-age=") {
		t.Fatalf("behind an HTTPS proxy: Strict-Transport-Security = %q", got)
	}
}
