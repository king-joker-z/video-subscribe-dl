package notify

import (
	"bytes"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"testing"
)

const sensitiveResponseBody = "super-secret-third-party-response"

type guardedResponseBody struct {
	payload    string
	readCalls  int
	closeCalls int
}

func (b *guardedResponseBody) Read([]byte) (int, error) {
	b.readCalls++
	return 0, errors.New("response body must not be read for non-2xx status")
}

func (b *guardedResponseBody) Close() error {
	b.closeCalls++
	return nil
}

type notifyLogCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *notifyLogCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *notifyLogCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

var notifyLogOutputMu sync.Mutex

func captureNotifyLogs(t *testing.T) *notifyLogCapture {
	t.Helper()
	notifyLogOutputMu.Lock()
	capture := &notifyLogCapture{}
	original := log.Writer()
	log.SetOutput(capture)
	t.Cleanup(func() {
		log.SetOutput(original)
		notifyLogOutputMu.Unlock()
	})
	return capture
}

func TestTelegramAndBarkNonOKResponseDoesNotReadOrLogBody(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings lifecycleSettings
		send     func(*Notifier) error
		channel  string
	}{
		{
			name: "telegram",
			settings: lifecycleSettings{values: map[string]string{
				"notify_type":        string(NotifyTelegram),
				"telegram_bot_token": "token",
				"telegram_chat_id":   "chat",
			}},
			send:    func(n *Notifier) error { return n.sendTelegram(EventTest, "title", "message") },
			channel: "telegram",
		},
		{
			name: "bark",
			settings: lifecycleSettings{values: map[string]string{
				"notify_type": "bark",
				"bark_server": "https://bark.example",
				"bark_key":    "key",
			}},
			send:    func(n *Notifier) error { return n.sendBark(EventTest, "title", "message") },
			channel: "bark",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := New(tc.settings)
			t.Cleanup(n.Stop)
			body := &guardedResponseBody{payload: strings.Repeat(sensitiveResponseBody, 1024)}
			n.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusBadGateway, Body: body, Header: make(http.Header)}, nil
			})
			logs := captureNotifyLogs(t)

			err := tc.send(n)
			if err == nil || !strings.Contains(err.Error(), "status 502") {
				t.Fatalf("expected status-only error, got %v", err)
			}
			if body.readCalls != 0 {
				t.Fatalf("non-2xx %s response body was read %d times", tc.channel, body.readCalls)
			}
			if body.closeCalls != 1 {
				t.Fatalf("non-2xx %s response body close calls = %d, want 1", tc.channel, body.closeCalls)
			}
			if got := logs.String(); strings.Contains(got, sensitiveResponseBody) {
				t.Fatalf("sensitive response body leaked to logs: %q", got)
			}
		})
	}
}

func TestTelegramAndBarkSuccessResponseRegression(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings lifecycleSettings
		send     func(*Notifier) error
	}{
		{
			name: "telegram",
			settings: lifecycleSettings{values: map[string]string{
				"telegram_bot_token": "token",
				"telegram_chat_id":   "chat",
			}},
			send: func(n *Notifier) error { return n.sendTelegram(EventTest, "title", "message") },
		},
		{
			name: "bark",
			settings: lifecycleSettings{values: map[string]string{
				"bark_server": "https://bark.example",
				"bark_key":    "key",
			}},
			send: func(n *Notifier) error { return n.sendBark(EventTest, "title", "message") },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := New(tc.settings)
			t.Cleanup(n.Stop)
			body := io.NopCloser(strings.NewReader(sensitiveResponseBody))
			n.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: body, Header: make(http.Header)}, nil
			})

			if err := tc.send(n); err != nil {
				t.Fatalf("success response returned error: %v", err)
			}
		})
	}
}
