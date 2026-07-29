// Package mediahttp provides a guarded HTTP client for media CDN downloads.
// It validates every URL and redirect, and pins each outbound connection to a
// public IP resolved immediately before dialing to prevent DNS rebinding.
package mediahttp

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const MaxRedirects = 5

type Policy struct {
	Name            string
	AllowedSuffixes []string
}

// DouyinPolicy permits the public media namespaces returned by Douyin APIs.
// Matching is label-boundary based; "byteimg.com" does not match
// "byteimg.com.example".
var DouyinPolicy = Policy{
	Name: "douyin media",
	AllowedSuffixes: []string{
		"douyinvod.com", "douyinpic.com", "byteimg.com", "bytedance.com",
		"bytecdn.cn", "toutiaoimg.com", "toutiaovod.com", "ibytedtos.com", "snssdk.com",
	},
}

type Resolver func(ctx context.Context, host string) ([]net.IP, error)
type DialContextFunc func(ctx context.Context, network, address string) (net.Conn, error)

type Options struct {
	Policy      Policy
	Timeout     time.Duration
	Resolver    Resolver
	DialContext DialContextFunc
	Transport   *http.Transport

	// TestRoundTripper is a test-only injection point for deterministic HTTP
	// responses. It is rejected unless AllowTestRoundTripper is explicitly true.
	// Production callers must use the guarded *http.Transport path below, which
	// performs DNS/IP validation and pinned-IP dialing.
	TestRoundTripper      http.RoundTripper
	AllowTestRoundTripper bool
}

func DefaultResolver(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

// ValidateURL rejects schemes and authority forms that must never reach media
// download clients. Host policy remains deliberately separate from page URL
// validation because CDN hostnames are dynamic.
func ValidateURL(raw string, policy Policy) (*url.URL, error) {
	u, err := url.ParseRequestURI(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid media URL")
	}
	if u.Scheme != "https" || u.User != nil || u.Host == "" || u.Port() != "" {
		return nil, fmt.Errorf("unsafe media URL")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" || net.ParseIP(host) != nil || host == "localhost" || strings.HasSuffix(host, ".local") {
		return nil, fmt.Errorf("unsafe media host")
	}
	if !policy.allowsHost(host) {
		return nil, fmt.Errorf("untrusted %s host", policy.Name)
	}
	return u, nil
}

func (p Policy) allowsHost(host string) bool {
	for _, suffix := range p.AllowedSuffixes {
		suffix = strings.TrimSuffix(strings.ToLower(suffix), ".")
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

// NewClient builds a production-safe client by default. Resolver and dialer
// are explicit dependencies so tests can exercise all paths without relaxing
// production URL, DNS or IP policy.
func NewClient(opt Options) *http.Client {
	resolver := opt.Resolver
	if resolver == nil {
		resolver = DefaultResolver
	}
	dial := opt.DialContext
	if dial == nil {
		dial = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}

	if opt.TestRoundTripper != nil {
		if !opt.AllowTestRoundTripper {
			panic("mediahttp: TestRoundTripper requires AllowTestRoundTripper")
		}
		// Test-only path: URL and redirect validation remain enabled, but it does
		// not own a network transport and therefore cannot exercise DNS dialing.
		return newValidatedClient(opt.Policy, opt.Timeout, opt.TestRoundTripper)
	}

	base := opt.Transport
	if base == nil {
		base = http.DefaultTransport.(*http.Transport).Clone()
	} else {
		base = base.Clone()
	}
	base.TLSClientConfig = cloneTLSConfig(base.TLSClientConfig)
	if base.TLSClientConfig.MinVersion < tls.VersionTLS12 {
		base.TLSClientConfig.MinVersion = tls.VersionTLS12
	}
	base.DialContext = guardedDialer(opt.Policy, resolver, dial)

	transport := guardedTransport{policy: opt.Policy, base: base}
	return newValidatedClient(opt.Policy, opt.Timeout, transport)
}

func newValidatedClient(policy Policy, timeout time.Duration, transport http.RoundTripper) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// via includes the request that produced the redirect. Therefore len=5
			// is the fifth allowed redirect and len=6 is the forbidden sixth.
			if len(via) > MaxRedirects {
				return fmt.Errorf("media redirect limit exceeded")
			}
			_, err := ValidateURL(req.URL.String(), policy)
			return err
		},
	}
}

func NewNoRedirectClient(opt Options) *http.Client {
	client := NewClient(opt)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > MaxRedirects {
			return fmt.Errorf("media redirect limit exceeded")
		}
		_, err := ValidateURL(req.URL.String(), opt.Policy)
		if err != nil {
			return err
		}
		return http.ErrUseLastResponse
	}
	return client
}
func cloneTLSConfig(in *tls.Config) *tls.Config {
	if in == nil {
		return &tls.Config{}
	}
	return in.Clone()
}

type guardedTransport struct {
	policy Policy
	base   http.RoundTripper
}

func (t guardedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if _, err := ValidateURL(req.URL.String(), t.policy); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(req)
}

func guardedDialer(policy Policy, resolver Resolver, dial DialContextFunc) DialContextFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("invalid dial address: %w", err)
		}
		host = strings.TrimSuffix(strings.ToLower(host), ".")
		if !policy.allowsHost(host) {
			return nil, fmt.Errorf("untrusted %s host", policy.Name)
		}
		ips, err := resolver(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("resolve media host: %w", err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("resolve media host: no addresses")
		}
		for _, ip := range ips {
			if isUnsafeIP(ip) {
				return nil, fmt.Errorf("unsafe media address")
			}
		}
		// Dial a validated IP, never the original hostname, so a resolver change
		// between validation and connection cannot redirect this request.
		var lastErr error
		for _, ip := range ips {
			conn, err := dial(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, fmt.Errorf("dial media host: %w", lastErr)
	}
}

var blockedSpecialPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "this" network
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved/future use
	netip.MustParsePrefix("2001:db8::/32"),   // IPv6 documentation
}

func isUnsafeIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok || !addr.IsValid() {
		return true
	}
	addr = addr.Unmap()
	for _, prefix := range blockedSpecialPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() {
		return true
	}
	// RFC 6598 shared address space (100.64.0.0/10) is not covered by IsPrivate.
	if addr.Is4() {
		v := addr.As4()
		if v[0] == 100 && v[1] >= 64 && v[1] <= 127 {
			return true
		}
	}
	return false
}
