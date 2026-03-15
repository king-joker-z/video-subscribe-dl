package api

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"video-subscribe-dl/internal/db"
)

// StreamHandler 视频流播放 API
type StreamHandler struct {
	db          *db.DB
	downloadDir string
}

func NewStreamHandler(database *db.DB, downloadDir string) *StreamHandler {
	return &StreamHandler{db: database, downloadDir: downloadDir}
}

// HandleStream GET /api/stream/:id — 流式播放视频文件（支持 Range 请求）
func (h *StreamHandler) HandleStream(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("GET", w, r) {
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, "/api/stream/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		apiError(w, CodeInvalidParam, "无效的 ID")
		return
	}

	dl, err := h.db.GetDownload(id)
	if err != nil {
		apiError(w, CodeVideoNotFound, "视频不存在")
		return
	}

	if dl.FilePath == "" {
		apiError(w, CodeNotFound, "视频文件路径为空")
		return
	}

	// 检查文件是否存在
	info, err := os.Stat(dl.FilePath)
	if err != nil {
		apiError(w, CodeNotFound, "视频文件不存在: "+dl.FilePath)
		return
	}

	// 推断 MIME 类型
	contentType := detectVideoMIME(dl.FilePath)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Disposition", "inline")

	// 使用 http.ServeContent 自动处理 Range 请求（支持拖拽进度条）
	f, err := os.Open(dl.FilePath)
	if err != nil {
		apiError(w, CodeInternal, "无法打开视频文件")
		return
	}
	defer f.Close()

	log.Printf("[stream] Serving video %d: %s (%s, %d bytes)", id, dl.Title, contentType, info.Size())
	http.ServeContent(w, r, filepath.Base(dl.FilePath), info.ModTime(), f)
}

// detectVideoMIME 根据文件扩展名推断视频 MIME 类型
func detectVideoMIME(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".mkv":
		return "video/x-matroska"
	case ".webm":
		return "video/webm"
	case ".flv":
		return "video/x-flv"
	case ".avi":
		return "video/x-msvideo"
	case ".mov":
		return "video/quicktime"
	case ".ts":
		return "video/mp2t"
	default:
		return "video/mp4"
	}
}
