package api

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

// sharedAPITransport 是 web/api 包中共享的 HTTP Transport，带连接池配置。
// 所有对外部 API（B站验证、抖音诊断等）的短请求均复用此 Transport，
// 避免每次 handler 调用都新建 Client + Transport 导致 TCP 连接无法复用。
var sharedAPITransport = &http.Transport{
	DialContext: (&net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	TLSClientConfig:      &tls.Config{MinVersion: tls.VersionTLS12},
	MaxIdleConns:          50,
	MaxIdleConnsPerHost:   5,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:  10 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
	ResponseHeaderTimeout: 15 * time.Second,
}

// sharedAPIClient 是 web/api 包中共享的 HTTP Client（15s 超时），
// 用于替代各 handler 中零散创建的 &http.Client{Timeout: 15 * time.Second}。
var sharedAPIClient = &http.Client{
	Timeout:   15 * time.Second,
	Transport: sharedAPITransport,
}

// sharedAPIClient10s 是 web/api 包中共享的 HTTP Client（10s 超时），
// 用于 dashboard 等需要更短超时的场景。
var sharedAPIClient10s = &http.Client{
	Timeout:   10 * time.Second,
	Transport: sharedAPITransport,
}
