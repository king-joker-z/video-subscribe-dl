package mediahttp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestValidateURLRejectsUnsafeAuthority(t *testing.T) {
	cases := []string{
		"http://v3-dy-o.douyinvod.com/video",
		"https://user@v3-dy-o.douyinvod.com/video",
		"https://v3-dy-o.douyinvod.com:443/video",
		"https://127.0.0.1/video",
		"https://localhost/video",
		"https://media.local/video",
		"https://douyinvod.com.evil.test/video",
	}
	for _, raw := range cases {
		if _, err := ValidateURL(raw, DouyinPolicy); err == nil {
			t.Fatalf("ValidateURL(%q) unexpectedly succeeded", raw)
		}
	}
}

func TestValidateURLAllowsLabelBoundedCDNHost(t *testing.T) {
	if _, err := ValidateURL("https://v3-dy-o.douyinvod.com/video", DouyinPolicy); err != nil {
		t.Fatalf("valid CDN host rejected: %v", err)
	}
}

func TestGuardedDialerRejectsUnsafeDNSBeforeDial(t *testing.T) {
	called := false
	dial := guardedDialer(DouyinPolicy,
		func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("100.64.0.1")}, nil },
		func(context.Context, string, string) (net.Conn, error) {
			called = true
			return nil, errors.New("must not dial")
		},
	)
	_, err := dial(context.Background(), "tcp", "v3-dy-o.douyinvod.com:443")
	if err == nil || !strings.Contains(err.Error(), "unsafe media address") {
		t.Fatalf("dial error = %v, want unsafe media address", err)
	}
	if called {
		t.Fatal("unsafe DNS answer reached dialer")
	}
}

func TestGuardedDialerPinsValidatedIP(t *testing.T) {
	var address string
	dial := guardedDialer(DouyinPolicy,
		func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("93.184.216.34")}, nil },
		func(_ context.Context, _, got string) (net.Conn, error) {
			address = got
			return nil, errors.New("dial stop")
		},
	)
	_, _ = dial(context.Background(), "tcp", "v3-dy-o.douyinvod.com:443")
	if address != "93.184.216.34:443" {
		t.Fatalf("dial address = %q, want pinned public IP", address)
	}
}

func TestClientRejectsUnsafeRedirect(t *testing.T) {
	client := NewClient(Options{
		Policy:  DouyinPolicy,
		Timeout: time.Second,
		TestRoundTripper: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://127.0.0.1/metadata"}}, Body: http.NoBody, Request: req}, nil
		}),
		AllowTestRoundTripper: true,
	})
	_, err := client.Get("https://v3-dy-o.douyinvod.com/video")
	if err == nil {
		t.Fatal("unsafe redirect unexpectedly succeeded")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestGuardedDialerRejectsReservedAndDocumentationRangesBeforeDial(t *testing.T) {
	cases := []string{
		"0.0.0.0", "192.0.0.0", "192.0.2.1", "198.18.0.1",
		"198.51.100.1", "203.0.113.1", "240.0.0.1", "2001:db8::1",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			called := false
			dial := guardedDialer(DouyinPolicy,
				func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP(raw)}, nil },
				func(context.Context, string, string) (net.Conn, error) {
					called = true
					return nil, errors.New("must not dial")
				},
			)
			_, err := dial(context.Background(), "tcp", "v3-dy-o.douyinvod.com:443")
			if err == nil || !strings.Contains(err.Error(), "unsafe media address") {
				t.Fatalf("dial error = %v, want unsafe media address", err)
			}
			if called {
				t.Fatal("reserved DNS answer reached dialer")
			}
		})
	}
}

func TestClientRedirectLimitAllowsFiveAndRejectsSixth(t *testing.T) {
	for _, tc := range []struct {
		name      string
		redirects int
		wantErr   bool
	}{
		{name: "five_allowed", redirects: 5},
		{name: "six_rejected", redirects: 6, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := NewClient(Options{
				Policy:                DouyinPolicy,
				Timeout:               time.Second,
				AllowTestRoundTripper: true,
				TestRoundTripper: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
					calls++
					if calls <= tc.redirects {
						return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://v3-dy-o.douyinvod.com/next"}}, Body: http.NoBody, Request: req}, nil
					}
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody, Request: req}, nil
				}),
			})
			resp, err := client.Get("https://v3-dy-o.douyinvod.com/start")
			if resp != nil {
				resp.Body.Close()
			}
			if tc.wantErr && err == nil {
				t.Fatal("sixth redirect unexpectedly succeeded")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("five redirects rejected: %v", err)
			}
		})
	}
}

func TestTestRoundTripperRequiresExplicitFlag(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("test RoundTripper without explicit flag did not panic")
		}
	}()
	_ = NewClient(Options{Policy: DouyinPolicy, TestRoundTripper: roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, nil })})
}
