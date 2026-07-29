package douyin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"video-subscribe-dl/internal/mediahttp"
)

func TestResolveVideoURLRejectsUnsafeRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://127.0.0.1/metadata")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	client := newTestClient(server.URL)
	defer client.Close()

	if _, err := client.ResolveVideoURL("https://v3-dy-o.douyinvod.com/video.mp4"); err == nil {
		t.Fatal("unsafe redirect unexpectedly succeeded")
	}
}

func TestResolveVideoURLRelativeRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start.mp4":
			w.Header().Set("Location", "/final.mp4")
			w.WriteHeader(http.StatusFound)
		case "/final.mp4":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := newTestClient(server.URL)
	defer client.Close()

	resolved, err := client.ResolveVideoURL("https://v3-dy-o.douyinvod.com/start.mp4")
	if err != nil {
		t.Fatalf("relative redirect error: %v", err)
	}
	if resolved != "https://v3-dy-o.douyinvod.com/final.mp4" {
		t.Fatalf("resolved = %q", resolved)
	}
}

func TestResolveVideoURLRejectsUnsafeRedirectAfterRelativeResolution(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://127.0.0.1/metadata")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	client := newTestClient(server.URL)
	defer client.Close()

	if _, err := client.ResolveVideoURL("https://v3-dy-o.douyinvod.com/start.mp4"); err == nil {
		t.Fatal("unsafe redirect after resolution unexpectedly succeeded")
	}
}

func TestResolveVideoURLRejectsSixthRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://v3-dy-o.douyinvod.com/again.mp4")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	client := newTestClient(server.URL)
	defer client.Close()

	if _, err := client.ResolveVideoURL("https://v3-dy-o.douyinvod.com/start.mp4"); err == nil || !strings.Contains(err.Error(), "redirect limit") {
		t.Fatalf("error = %v, want redirect limit", err)
	}
}

func TestResolveVideoURLUsesGuardedPolicy(t *testing.T) {
	if _, err := mediahttp.ValidateURL("https://127.0.0.1/metadata", mediahttp.DouyinPolicy); err == nil {
		t.Fatal("test precondition: unsafe media URL accepted")
	}
}
