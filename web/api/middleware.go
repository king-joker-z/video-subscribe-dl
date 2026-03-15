package api

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// JSONMiddleware 设置 JSON Content-Type
func JSONMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 跳过 SSE 端点
		if strings.HasSuffix(r.URL.Path, "/events") || strings.HasSuffix(r.URL.Path, "/stream") {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CORSMiddleware 处理跨域
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := os.Getenv("CORS_ORIGIN")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Max-Age", "86400")
		}

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// RecoveryMiddleware 错误恢复
func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("[PANIC] %s %s: %v", r.Method, r.URL.Path, err)
				apiError(w, CodeInternal, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// LogMiddleware 请求日志
func LogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// 包装 ResponseWriter 以获取状态码
		wrapped := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(wrapped, r)

		// 跳过静态资源和 SSE 的日志
		if strings.HasPrefix(r.URL.Path, "/static/") ||
			strings.HasSuffix(r.URL.Path, "/events") ||
			strings.HasSuffix(r.URL.Path, "/stream") {
			return
		}

		duration := time.Since(start)
		if duration > 500*time.Millisecond {
			log.Printf("[API] %s %s %d %s (slow)", r.Method, r.URL.Path, wrapped.status, duration)
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// MethodGuard 检查 HTTP 方法
func MethodGuard(method string, w http.ResponseWriter, r *http.Request) bool {
	if r.Method != method {
		apiError(w, CodeMethodNotAllow, "method not allowed")
		return false
	}
	return true
}
