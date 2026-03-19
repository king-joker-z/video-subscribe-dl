package web

import (
	"io/fs"
	"net/http"

	newapi "video-subscribe-dl/web/api"
)

// registerRoutes 注册所有路由
func (s *Server) registerRoutes() {
	// ========== 新版 API（统一响应格式）==========
	s.apiRouter = newapi.NewRouter(s.db, s.downloader, s.downloadDir)
	s.apiRouter.Register(s.mux)

	// ========== 辅助路由（定义在 server.go）==========
	s.mux.HandleFunc("/health", s.handleHealth) // 健康检查（兼容旧监控探针）

	// ========== 静态资源 & 首页 ==========
	staticSub, _ := fs.Sub(staticFS, "static")
	s.mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))
	s.mux.HandleFunc("/", s.handleIndex)
}
