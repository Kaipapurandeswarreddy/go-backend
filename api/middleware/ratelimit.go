package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"ambigo-backend/api/response"
	"golang.org/x/time/rate"
)

// clientIP extracts the real client IP, respecting X-Forwarded-For / X-Real-IP
// set by Render / Cloud Run / nginx. Falls back to RemoteAddr host.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	host := r.RemoteAddr
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	host = strings.Trim(host, "[]")
	return host
}

type clientLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type ipLimiter struct {
	mu          sync.Mutex
	clients     map[string]*clientLimiter
	rate        rate.Limit
	burst       int
	cleanupDone chan struct{}
}

func NewIPLimiter(r rate.Limit, burst int) *ipLimiter {
	l := &ipLimiter{
		clients:     make(map[string]*clientLimiter),
		rate:        r,
		burst:       burst,
		cleanupDone: make(chan struct{}),
	}
	go l.cleanupLoop()
	return l
}

func (l *ipLimiter) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		l.mu.Lock()
		for ip, c := range l.clients {
			if time.Since(c.lastSeen) > 10*time.Minute {
				delete(l.clients, ip)
			}
		}
		l.mu.Unlock()
	}
}

func (l *ipLimiter) getLimiter(ip string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	c, exists := l.clients[ip]
	if !exists {
		c = &clientLimiter{
			limiter: rate.NewLimiter(l.rate, l.burst),
		}
		l.clients[ip] = c
	}
	c.lastSeen = time.Now()
	return c.limiter
}

func RateLimit(next http.HandlerFunc, limiter *ipLimiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !limiter.getLimiter(ip).Allow() {
			response.Error(w, "Too many requests. Please try again later.", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

// MobileRateLimiter limits per mobile number (V1, V7)
type MobileRateLimiter struct {
	mu          sync.Mutex
	clients     map[string]*clientLimiter
	rate        rate.Limit
	burst       int
	cleanupDone chan struct{}
}

func NewMobileRateLimiter(r rate.Limit, burst int) *MobileRateLimiter {
	l := &MobileRateLimiter{
		clients:     make(map[string]*clientLimiter),
		rate:        r,
		burst:       burst,
		cleanupDone: make(chan struct{}),
	}
	go l.cleanupLoop()
	return l
}

func (l *MobileRateLimiter) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		l.mu.Lock()
		for mobile, c := range l.clients {
			if time.Since(c.lastSeen) > 10*time.Minute {
				delete(l.clients, mobile)
			}
		}
		// Cap at 100k entries to prevent OOM under brute-force
		if len(l.clients) > 100000 {
			// Evict oldest 10% (approx) — simple random eviction
			n := len(l.clients) / 10
			for mobile := range l.clients {
				delete(l.clients, mobile)
				n--
				if n <= 0 {
					break
				}
			}
		}
		l.mu.Unlock()
	}
}

func (l *MobileRateLimiter) getLimiter(mobile string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	c, exists := l.clients[mobile]
	if !exists {
		c = &clientLimiter{
			limiter: rate.NewLimiter(l.rate, l.burst),
		}
		l.clients[mobile] = c
	}
	c.lastSeen = time.Now()
	return c.limiter
}

// RateLimitMiddleware wraps an http.Handler with per-IP rate limiting
func RateLimitMiddleware(limiter *ipLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !limiter.getLimiter(ip).Allow() {
			response.Error(w, "Too many requests. Please try again later.", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func RateLimitMobile(next http.HandlerFunc, limiter *MobileRateLimiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mobile := r.FormValue("mobile")
		if mobile == "" {
			bodyBytes, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			var body struct {
				Mobile string `json:"mobile"`
			}
			_ = json.Unmarshal(bodyBytes, &body)
			mobile = body.Mobile
		}
		if mobile != "" && !limiter.getLimiter(mobile).Allow() {
			response.Error(w, "Too many attempts. Please try again later.", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}
