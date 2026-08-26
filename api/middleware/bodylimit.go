package middleware

import (
	"net/http"
	"strings"
)

const MaxBodyBytes = 1 << 20 // 1 MB

const verificationBodyLimit = 10 << 20 // 10 MB for 6 base64 images

func BodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := MaxBodyBytes
		// Verification upload carries 6 base64 images (~6-8 MB); allow 10 MB for that one route
		if strings.Contains(r.URL.Path, "/driver/verification/update") {
			limit = verificationBodyLimit
		}
		r.Body = http.MaxBytesReader(w, r.Body, int64(limit))
		next.ServeHTTP(w, r)
	})
}
