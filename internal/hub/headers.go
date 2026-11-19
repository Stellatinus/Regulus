package hub

import "net/http"

// contentSecurityPolicy is what the dashboard needs and nothing more: its own
// script, style and files, images from itself or inline (the QR code of the
// two-factor setup is a data: URI), and its own origin for the API and the
// live stream. It embeds nothing and cannot be framed.
const contentSecurityPolicy = "default-src 'self'; " +
	"img-src 'self' data:; " +
	"object-src 'none'; " +
	"base-uri 'none'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

// withSecurityHeaders sets the headers that tell a browser how to treat what
// the hub sends, on every response (the dashboard, the API and the errors):
// no framing (clickjacking on the revoke, token and reset buttons), no
// content sniffing, no Referer, and a content security policy that limits what
// a page can load or run if a cross-site scripting hole ever appears.
//
// Strict-Transport-Security is added when the request came over TLS, directly
// or as a proxy reports it in X-Forwarded-Proto. Believing that header from
// anyone is harmless here: a browser ignores the header over plain HTTP, and
// over real HTTPS the proxy's answer is the truth.
func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Frame-Options", "DENY") // old browsers; frame-ancestors covers the rest
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}
