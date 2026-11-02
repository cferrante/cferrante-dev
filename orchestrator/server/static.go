package server

import "net/http"

// ContentSecurityPolicy allows only the site's own scripts, styles and
// connections, and no inline script (design §14): if model text ever got
// into the page as HTML, an injected script still wouldn't run.
const ContentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; " +
	"img-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// Static serves the page from dir with the security headers. http.Dir
// refuses paths that leave dir. In production, S3 + CloudFront serve the
// page and must send the same headers (Phase 7).
func Static(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", ContentSecurityPolicy)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		files.ServeHTTP(w, r)
	})
}
