package notify

import (
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type lifecycleSettings struct {
	values map[string]string
}

func (s lifecycleSettings) GetSetting(key string) (string, error) {
	return s.values[key], nil
}

func enabledWebhookSettings() lifecycleSettings {
	return lifecycleSettings{values: map[string]string{
		"notify_type":     string(NotifyWebhook),
		"webhook_url":     "http://127.0.0.1:1",
		"notify_on_error": "true",
	}}
}

func waitForSignal(t *testing.T, ch <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func TestNotifierStopIsIdempotentAndWorkerExits(t *testing.T) {
	n := New(enabledWebhookSettings())
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n.Stop()
		}()
	}
	wg.Wait()
	select {
	case <-n.workerDone:
	default:
		t.Fatal("Stop returned before notifier worker exited")
	}
}

func TestNotifierStopInterruptsRetryWait(t *testing.T) {
	n := New(enabledWebhookSettings())
	attempted := make(chan struct{}, 1)
	n.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		select {
		case attempted <- struct{}{}:
		default:
		}
		return nil, errors.New("test send failure")
	})

	n.Send(EventDownloadFailed, "test", "retry")
	waitForSignal(t, attempted, "first notification attempt")
	n.Stop()
	select {
	case <-n.workerDone:
	default:
		t.Fatal("Stop returned before notifier worker exited during retry wait")
	}
}

func TestNotifierSendAndStopConcurrent(t *testing.T) {
	n := New(enabledWebhookSettings())
	var calls atomic.Int32
	n.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("test send failure")
	})

	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range 16 {
				n.Send(EventDownloadFailed, "test", "concurrent")
			}
		}()
	}
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			n.Stop()
		}()
	}
	close(start)
	wg.Wait()

	select {
	case <-n.workerDone:
	default:
		t.Fatal("Stop returned before notifier worker exited")
	}
	before := calls.Load()
	for range 32 {
		n.Send(EventDownloadFailed, "test", "after stop")
	}
	if got := calls.Load(); got != before {
		t.Fatalf("transport calls grew after Stop: before=%d after=%d", before, got)
	}
}

func TestNotifierSendAfterStopDoesNotDispatch(t *testing.T) {
	n := New(enabledWebhookSettings())
	var calls atomic.Int32
	n.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("unexpected dispatch")
	})

	n.Stop()
	waitForSignal(t, n.workerDone, "notifier worker exit")
	n.Send(EventDownloadFailed, "test", "after stop")
	if got := calls.Load(); got != 0 {
		t.Fatalf("Send after Stop dispatched %d network calls", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
