package http

import "net/http"

// MaxRequestBodySize is the upper bound for HTTP request bodies across
// all routes. Prevents unbounded memory allocation from malicious or
// misconfigured clients.
const MaxRequestBodySize = 4 << 20 // 4 MiB

// MaxBody returns HTTP middleware that limits request body reads to
// MaxRequestBodySize. Reads past the limit fail with http.MaxBytesError
// and the connection is marked for close.
func MaxBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBodySize)
		next.ServeHTTP(w, r)
	})
}
