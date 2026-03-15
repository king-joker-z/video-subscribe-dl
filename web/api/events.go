package api

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
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
	appLogger := logger.Default()
	if appLogger != nil {
		appLogger.Clear()
	}
	apiOK(w, map[string]string{"message": "日志已清空"})
}

// === WebSocket 日志（标准库实现，无第三方依赖）===

// WebSocket opcodes
const (
	wsOpContinuation = 0x0
	wsOpText         = 0x1
	wsOpBinary       = 0x2
	wsOpClose        = 0x8
	wsOpPing         = 0x9
	wsOpPong         = 0xA
)

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
	frame := buildWSFrame(wsOpText, data)
	_, err := ws.bufrw.Write(frame)
	if err != nil {
		return err
	}
	return ws.bufrw.Flush()
}

// writeFrame 写入指定 opcode 的 frame（内部使用，调用方需已持锁或保证安全）
func (ws *wsConn) writeFrame(opcode byte, payload []byte) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if ws.closed {
		return fmt.Errorf("connection closed")
	}
	frame := buildWSFrame(opcode, payload)
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

// readFrame 从连接读取一个完整的 WebSocket frame
// 返回: opcode, payload, error
// 正确处理 mask（客户端发来的 frame 都有 mask）
func (ws *wsConn) readFrame() (byte, []byte, error) {
	reader := ws.bufrw.Reader

	// 读取前 2 字节头
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		return 0, nil, err
	}

	// FIN + opcode
	// fin := (header[0] & 0x80) != 0  // 暂不处理分片重组，只读单帧
	opcode := header[0] & 0x0F

	// Mask + payload length
	masked := (header[1] & 0x80) != 0
	payloadLen := uint64(header[1] & 0x7F)

	// Extended payload length
	if payloadLen == 126 {
		ext := make([]byte, 2)
		if _, err := io.ReadFull(reader, ext); err != nil {
			return 0, nil, err
		}
		payloadLen = uint64(binary.BigEndian.Uint16(ext))
	} else if payloadLen == 127 {
		ext := make([]byte, 8)
		if _, err := io.ReadFull(reader, ext); err != nil {
			return 0, nil, err
		}
		payloadLen = binary.BigEndian.Uint64(ext)
	}

	// 安全限制：单帧最大 1MB（控制帧正常很小）
	if payloadLen > 1<<20 {
		return 0, nil, fmt.Errorf("frame too large: %d bytes", payloadLen)
	}

	// Mask key（4 bytes）
	var maskKey [4]byte
	if masked {
		if _, err := io.ReadFull(reader, maskKey[:]); err != nil {
			return 0, nil, err
		}
	}

	// Payload
	payload := make([]byte, payloadLen)
	if payloadLen > 0 {
		if _, err := io.ReadFull(reader, payload); err != nil {
			return 0, nil, err
		}
	}

	// Unmask
	if masked {
		for i := range payload {
			payload[i] ^= maskKey[i%4]
		}
	}

	return opcode, payload, nil
}

// readLoop 读取客户端发来的 frame，处理 ping/pong/close
// 当 readLoop 退出时，通过 closeCh 通知发送循环
func (ws *wsConn) readLoop(closeCh chan struct{}) {
	defer close(closeCh)
	for {
		ws.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		opcode, payload, err := ws.readFrame()
		if err != nil {
			return // 读出错，退出
		}

		switch opcode {
		case wsOpClose:
			// 回复 close frame
			ws.writeFrame(wsOpClose, payload)
			return

		case wsOpPing:
			// 回复 pong，payload 原样返回
			ws.writeFrame(wsOpPong, payload)

		case wsOpPong:
			// 收到 pong，忽略（我们没有主动发 ping）

		case wsOpText, wsOpBinary, wsOpContinuation:
			// 日志是单向推送，忽略客户端发来的消息

		default:
			// 未知 opcode，忽略
		}
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

	// 推送历史日志（除非客户端请求跳过）
	appLogger := logger.Default()
	noHistory := r.URL.Query().Get("no_history") == "1"
	if appLogger != nil && !noHistory {
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
	go ws.readLoop(closeCh)

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
// 正确处理 3 种 payload length 编码：
//   - payload <= 125: 1 字节 length
//   - 126 <= payload <= 65535: 1 字节 126 + 2 字节 length（big-endian）
//   - payload > 65535: 1 字节 127 + 8 字节 length（big-endian）
func buildWSFrame(opcode byte, payload []byte) []byte {
	length := len(payload)
	// 预分配：2字节头 + 最多8字节扩展长度 + payload
	frame := make([]byte, 0, 2+8+length)

	// FIN + opcode
	frame = append(frame, 0x80|opcode)

	if length <= 125 {
		frame = append(frame, byte(length))
	} else if length <= 65535 {
		frame = append(frame, 126)
		frame = append(frame, byte(length>>8), byte(length&0xFF))
	} else {
		frame = append(frame, 127)
		for i := 7; i >= 0; i-- {
			frame = append(frame, byte(length>>(i*8)&0xFF))
		}
	}

	frame = append(frame, payload...)
	return frame
}
