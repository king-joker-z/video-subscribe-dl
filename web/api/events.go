package api

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"video-subscribe-dl/internal/downloader"
	"video-subscribe-dl/internal/logger"
)

// EventsHandler SSE/WebSocket 实时推送
type EventsHandler struct {
	downloader *downloader.Downloader
	// WebSocket 连接管理
	wsMu    sync.Mutex
	wsConns map[*wsConn]struct{}
}

func NewEventsHandler(dl *downloader.Downloader) *EventsHandler {
	return &EventsHandler{
		downloader: dl,
		wsConns:    make(map[*wsConn]struct{}),
	}
}

// GET /api/events — 统一 SSE 端点（下载进度 + 日志）
func (h *EventsHandler) HandleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		apiError(w, CodeMethodNotAllow, "method not allowed")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		apiError(w, CodeInternal, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	if origin := os.Getenv("CORS_ORIGIN"); origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	}

	// 连接建立事件
	fmt.Fprintf(w, "event: connected\ndata: {}\n\n")
	flusher.Flush()

	// 订阅日志
	appLogger := logger.Default()
	var logCh chan logger.LogEntry
	if appLogger != nil {
		logCh = appLogger.Subscribe()
		defer appLogger.Unsubscribe(logCh)
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			// 发送下载进度
			if h.downloader != nil {
				progress := h.downloader.GetProgress()
				data, _ := json.Marshal(progress)
				fmt.Fprintf(w, "event: progress\ndata: %s\n\n", data)
				flusher.Flush()
			}

		case entry, ok := <-logCh:
			if !ok {
				return
			}
			data := logger.MarshalEntry(entry)
			fmt.Fprintf(w, "event: log\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// GET /api/logs — 历史日志
func (h *EventsHandler) HandleLogs(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("GET", w, r) {
		return
	}

	appLogger := logger.Default()
	if appLogger == nil {
		apiOK(w, []logger.LogEntry{})
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	entries := appLogger.GetLogs(limit, offset)
	apiOK(w, entries)
}

// POST /api/logs — 清空日志 buffer
func (h *EventsHandler) HandleLogsClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		apiError(w, CodeMethodNotAllow, "method not allowed")
		return
	}
	appLogger := logger.Default()
	if appLogger != nil {
		appLogger.Clear()
	}
	apiOK(w, map[string]string{"message": "日志已清空"})
}

// === WebSocket 日志（标准库实现，无第三方依赖）===

// wsConn 表示一个 WebSocket 连接
type wsConn struct {
	conn   net.Conn
	bufrw  *bufio.ReadWriter
	mu     sync.Mutex
	closed bool
}

func (ws *wsConn) writeMessage(data []byte) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if ws.closed {
		return fmt.Errorf("connection closed")
	}
	// 构造 WebSocket text frame
	frame := buildWSFrame(1, data) // opcode 1 = text
	_, err := ws.bufrw.Write(frame)
	if err != nil {
		return err
	}
	return ws.bufrw.Flush()
}

func (ws *wsConn) close() {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if !ws.closed {
		ws.closed = true
		ws.conn.Close()
	}
}

// HandleWSLogs WebSocket 日志端点
// GET /api/ws/logs
func (h *EventsHandler) HandleWSLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		apiError(w, CodeMethodNotAllow, "method not allowed")
		return
	}

	// WebSocket 握手
	if r.Header.Get("Upgrade") != "websocket" {
		apiError(w, CodeBadRequest, "需要 WebSocket 升级")
		return
	}

	conn, bufrw, err := hijackConnection(w)
	if err != nil {
		apiError(w, CodeInternal, "WebSocket 升级失败")
		return
	}

	// 完成握手
	key := r.Header.Get("Sec-WebSocket-Key")
	accept := computeWSAccept(key)
	response := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	bufrw.WriteString(response)
	bufrw.Flush()

	ws := &wsConn{conn: conn, bufrw: bufrw}

	// 注册连接
	h.wsMu.Lock()
	h.wsConns[ws] = struct{}{}
	h.wsMu.Unlock()

	defer func() {
		h.wsMu.Lock()
		delete(h.wsConns, ws)
		h.wsMu.Unlock()
		ws.close()
	}()

	// 推送历史日志
	appLogger := logger.Default()
	if appLogger != nil {
		history := appLogger.GetLogs(200, 0)
		for _, entry := range history {
			data := logger.MarshalEntry(entry)
			if err := ws.writeMessage(data); err != nil {
				return
			}
		}
	}

	// 订阅新日志
	var logCh chan logger.LogEntry
	if appLogger != nil {
		logCh = appLogger.Subscribe()
		defer appLogger.Unsubscribe(logCh)
	}

	// 读取协程：处理 ping/pong/close
	closeCh := make(chan struct{})
	go func() {
		defer close(closeCh)
		buf := make([]byte, 4096)
		for {
			conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			_, err := conn.Read(buf)
			if err != nil {
				return
			}
			// 解析 frame 检测 close
			if len(buf) > 0 && (buf[0]&0x0F) == 8 {
				return // close frame
			}
		}
	}()

	// 发送循环
	for {
		select {
		case entry, ok := <-logCh:
			if !ok {
				return
			}
			data := logger.MarshalEntry(entry)
			if err := ws.writeMessage(data); err != nil {
				return
			}
		case <-closeCh:
			return
		}
	}
}

// BroadcastWSLog 广播日志给所有 WebSocket 连接
func (h *EventsHandler) BroadcastWSLog(data []byte) {
	h.wsMu.Lock()
	conns := make([]*wsConn, 0, len(h.wsConns))
	for ws := range h.wsConns {
		conns = append(conns, ws)
	}
	h.wsMu.Unlock()

	for _, ws := range conns {
		if err := ws.writeMessage(data); err != nil {
			h.wsMu.Lock()
			delete(h.wsConns, ws)
			h.wsMu.Unlock()
			ws.close()
		}
	}
}

// === WebSocket 工具函数 ===

func hijackConnection(w http.ResponseWriter) (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("server doesn't support hijacking")
	}
	conn, bufrw, err := hj.Hijack()
	return conn, bufrw, err
}

func computeWSAccept(key string) string {
	const magic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	h := sha1.New()
	h.Write([]byte(key + magic))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// buildWSFrame 构建 WebSocket frame（仅服务端发送，不需要 mask）
func buildWSFrame(opcode byte, payload []byte) []byte {
	length := len(payload)
	var frame []byte

	// FIN + opcode
	frame = append(frame, 0x80|opcode)

	if length <= 125 {
		frame = append(frame, byte(length))
	} else if length <= 65535 {
		frame = append(frame, 126, byte(length>>8), byte(length&0xFF))
	} else {
		frame = append(frame, 127)
		for i := 7; i >= 0; i-- {
			frame = append(frame, byte(length>>(i*8)&0xFF))
		}
	}

	frame = append(frame, payload...)
	return frame
}
