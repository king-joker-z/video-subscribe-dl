// Package urlguard validates user-supplied platform page URLs before the
// application makes outbound requests. It intentionally protects page and
// short-link entry points; media CDN validation is a separate concern.
package urlguard

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

type Platform string

const (
	Bilibili Platform = "bilibili"
	Douyin   Platform = "douyin"
	Pornhub  Platform = "pornhub"
	XChina   Platform = "xchina"
)

var hosts = map[Platform]map[string]struct{}{
	Bilibili: {"www.bilibili.com": {}, "space.bilibili.com": {}, "b23.tv": {}},
	Douyin:   {"v.douyin.com": {}, "www.douyin.com": {}, "www.iesdouyin.com": {}},
	Pornhub:  {"pornhub.com": {}, "www.pornhub.com": {}},
	XChina:   {"xchina.co": {}, "www.xchina.co": {}, "en.xchina.co": {}},
}

func ParsePlatformURL(raw string, platform Platform) (*url.URL, error) {
	if len(raw) == 0 || len(raw) > 4096 {
		return nil, fmt.Errorf("invalid URL length")
	}
	u, err := url.ParseRequestURI(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid URL")
	}
	if u.Scheme != "https" || u.User != nil || u.Host == "" || u.Port() != "" {
		return nil, fmt.Errorf("unsafe platform URL")
	}
	host := strings.ToLower(u.Hostname())
	if net.ParseIP(host) != nil || host == "localhost" || strings.HasSuffix(host, ".local") {
		return nil, fmt.Errorf("unsafe platform host")
	}
	if _, ok := hosts[platform][host]; !ok {
		return nil, fmt.Errorf("unsupported platform host")
	}
	return u, nil
}

func IsPlatformURL(raw string, platform Platform) bool {
	_, err := ParsePlatformURL(raw, platform)
	return err == nil
}

// ValidateRedirect validates an absolute redirect target for the same platform.
func ValidateRedirect(raw string, platform Platform) (*url.URL, error) {
	return ParsePlatformURL(raw, platform)
}
