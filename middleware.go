package chaosbackend

import "net/http"

// AddHeaders is middleware that sets the fixed X-Backends header.
func AddHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backends", "snuskepus")

		next.ServeHTTP(w, r)
	})
}
