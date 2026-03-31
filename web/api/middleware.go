package api

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// JSONMiddleware 设置 JSON Content-Type（跳过 SSE/stream 路径）
// [FIXED: P2-9] Actually set the Content-Type header for non-streaming API paths.
func JSONMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 跳过 SSE / WebSocket / stream 端点，它们自行管理 Content-Type
		if strings.HasSuffix(r.URL.Path, "/events") || strings.HasSuffix(r.URL.Path, "/stream") ||
			strings.HasPrefix(r.URL.Path, "/api/stream/") ||
			strings.HasSuffix(r.URL.Path, "/ws/logs") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
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
			strings.HasSuffix(r.URL.Path, "/stream") ||
			strings.HasPrefix(r.URL.Path, "/api/stream/") {
			return
		}

		duration := time.Since(start)
		// P2-6: always log 4xx/5xx responses so errors are visible in the log
		if wrapped.status >= 400 {
			log.Printf("[api] %s %s %d %s", r.Method, r.URL.Path, wrapped.status, duration)
		} else if duration > 500*time.Millisecond {
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

// AuthMiddleware Token 认证中间件
// 检查 Authorization: Bearer {token} 或 cookie auth_token
// 白名单路径不需要认证
func AuthMiddleware(getToken func() string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path

			// 白名单：不需要认证
			if isAuthWhitelist(path) {
				next.ServeHTTP(w, r)
				return
			}

			token := getToken()
			// 如果未设置 token（空字符串），则不启用认证
			if token == "" {
				next.ServeHTTP(w, r)
				return
			}

			// 从 Authorization header 获取
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				if strings.TrimPrefix(authHeader, "Bearer ") == token {
					next.ServeHTTP(w, r)
					return
				}
			}

			// 从 cookie 获取
			cookie, err := r.Cookie("auth_token")
			if err == nil && cookie.Value == token {
				next.ServeHTTP(w, r)
				return
			}

			// 从 query param 获取（WebSocket 连接用）
			if qToken := r.URL.Query().Get("token"); qToken == token {
				next.ServeHTTP(w, r)
				return
			}

			// 未认证
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"code":401,"message":"未认证，请先登录"}`))
		})
	}
}

// isAuthWhitelist 判断路径是否在白名单中
func isAuthWhitelist(path string) bool {
	whitelist := []string{
		"/health",
		"/api/login/token",
		"/api/login/qrcode/generate",
		"/api/login/qrcode/poll",
	}
	for _, w := range whitelist {
		if path == w {
			return true
		}
	}

	// 静态文件不需要认证
	if strings.HasPrefix(path, "/static/") {
		return true
	}

	// 根路径（SPA 入口）
	if path == "/" || path == "/index.html" {
		return true
	}

	return false
}
