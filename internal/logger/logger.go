package logger

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// LogEntry represents a single log entry
type LogEntry struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

// RingLogger is a thread-safe ring buffer logger that writes to both stdout and buffer
type RingLogger struct {
	mu        sync.RWMutex
	entries   []LogEntry
	maxSize   int
	writeIdx  int
	count     int
	listeners []chan LogEntry
	listMu    sync.Mutex
}

var defaultLogger *RingLogger

// Init initializes the global logger with given buffer size
func Init(size int) *RingLogger {
	l := &RingLogger{
		entries:   make([]LogEntry, size),
		maxSize:   size,
		listeners: make([]chan LogEntry, 0),
	}
	defaultLogger = l
	return l
}

// Default returns the global logger instance
func Default() *RingLogger {
	return defaultLogger
}

// Writer returns an io.Writer that parses log lines and adds them to the buffer
func (l *RingLogger) Writer() io.Writer {
	return &logWriter{logger: l}
}

type logWriter struct {
	logger *RingLogger
	buf    []byte
}

func (w *logWriter) Write(p []byte) (n int, err error) {
	// Write to stdout first
	os.Stdout.Write(p)

	// Buffer incomplete lines
	w.buf = append(w.buf, p...)

	for {
		idx := indexOf(w.buf, '\n')
		if idx < 0 {
			break
		}
		line := string(w.buf[:idx])
		w.buf = w.buf[idx+1:]

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		entry := parseLine(line)
		w.logger.add(entry)
	}

	return len(p), nil
}

func indexOf(b []byte, c byte) int {
	for i, v := range b {
		if v == c {
			return i
		}
	}
	return -1
}

func parseLine(line string) LogEntry {
	level := "info"
	msg := line

	// Try to detect level from common patterns
	lower := strings.ToLower(line)
	if strings.Contains(lower, "error") || strings.Contains(lower, "fatal") {
		level = "error"
	} else if strings.Contains(lower, "warn") {
		level = "warn"
	}

	return LogEntry{
		Time:    time.Now().Format("2006-01-02 15:04:05"),
		Level:   level,
		Message: msg,
	}
}

func (l *RingLogger) add(entry LogEntry) {
	l.mu.Lock()
	l.entries[l.writeIdx] = entry
	l.writeIdx = (l.writeIdx + 1) % l.maxSize
	if l.count < l.maxSize {
		l.count++
	}
	l.mu.Unlock()

	// Notify SSE listeners
	l.listMu.Lock()
	for _, ch := range l.listeners {
		select {
		case ch <- entry:
		default:
			// drop if channel full
		}
	}
	l.listMu.Unlock()
}

// Info logs an info level message
func (l *RingLogger) Info(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	entry := LogEntry{
		Time:    time.Now().Format("2006-01-02 15:04:05"),
		Level:   "info",
		Message: msg,
	}
	// Write to stdout
	fmt.Fprintf(os.Stdout, "%s [INFO] %s\n", entry.Time, msg)
	l.add(entry)
}

// Warn logs a warn level message
func (l *RingLogger) Warn(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	entry := LogEntry{
		Time:    time.Now().Format("2006-01-02 15:04:05"),
		Level:   "warn",
		Message: msg,
	}
	fmt.Fprintf(os.Stdout, "%s [WARN] %s\n", entry.Time, msg)
	l.add(entry)
}

// Error logs an error level message
func (l *RingLogger) Error(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	entry := LogEntry{
		Time:    time.Now().Format("2006-01-02 15:04:05"),
		Level:   "error",
		Message: msg,
	}
	fmt.Fprintf(os.Stderr, "%s [ERROR] %s\n", entry.Time, msg)
	l.add(entry)
}

// GetLogs returns log entries with pagination (newest first)
func (l *RingLogger) GetLogs(limit, offset int) []LogEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if l.count == 0 {
		return []LogEntry{}
	}

	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	// Build result slice from ring buffer (oldest to newest)
	total := l.count
	result := make([]LogEntry, 0, total)

	startIdx := 0
	if total == l.maxSize {
		startIdx = l.writeIdx
	}

	for i := 0; i < total; i++ {
		idx := (startIdx + i) % l.maxSize
		result = append(result, l.entries[idx])
	}

	// Apply offset from the end (newest first for offset)
	if offset >= len(result) {
		return []LogEntry{}
	}

	// Return oldest-to-newest order, but skip 'offset' from the newest end
	end := len(result) - offset
	start := end - limit
	if start < 0 {
		start = 0
	}

	return result[start:end]
}

// Subscribe returns a channel that receives new log entries
func (l *RingLogger) Subscribe() chan LogEntry {
	ch := make(chan LogEntry, 64)
	l.listMu.Lock()
	l.listeners = append(l.listeners, ch)
	l.listMu.Unlock()
	return ch
}

// Unsubscribe removes a listener channel
func (l *RingLogger) Unsubscribe(ch chan LogEntry) {
	l.listMu.Lock()
	defer l.listMu.Unlock()
	for i, c := range l.listeners {
		if c == ch {
			l.listeners = append(l.listeners[:i], l.listeners[i+1:]...)
			close(ch)
			return
		}
	}
}

// MarshalEntry converts a log entry to JSON bytes
func MarshalEntry(entry LogEntry) []byte {
	data, _ := json.Marshal(entry)
	return data
}

// Clear 清空日志 ring buffer
func (l *RingLogger) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = make([]LogEntry, l.maxSize)
	l.writeIdx = 0
	l.count = 0
}
