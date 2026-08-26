package middleware

import "net/http"

var allowedOrigins = []string{
	"https://ambigo.in",
	"https://admin.ambigo.in",
	"https://ambigo-server-559193701066.asia-south1.run.app",
	"https://go-backend-viu3.onrender.com",
	"http://localhost:",
	"capacitor://",
}

func isAllowedOrigin(origin string) bool {
	if origin == "" {
		return true // non-browser (Flutter) — no Origin header
	}
	for _, ao := range allowedOrigins {
		if origin == ao || len(origin) >= len(ao) && origin[:len(ao)] == ao {
			return true
		}
	}
	return false
}

func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if isAllowedOrigin(origin) {
			if origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
			} else {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			}
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key, X-Request-ID")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
