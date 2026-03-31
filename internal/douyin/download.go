package douyin

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// 共享的 http.Transport，带连接池配置，供所有抖音下载复用
var sharedDownloadTransport = &http.Transport{
	DialContext: (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	MaxIdleConns:           100,
	MaxIdleConnsPerHost:    10,
	IdleConnTimeout:        90 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ExpectContinueTimeout:  1 * time.Second,
	ResponseHeaderTimeout:  30 * time.Second,
}

// sharedVideoClient 用于视频/图片等大文件下载的复用 HTTP Client（长超时）
var sharedVideoClient = &http.Client{
	Timeout:   10 * time.Minute,
	Transport: sharedDownloadTransport,
}

// sharedThumbClient 用于封面等小文件下载的复用 HTTP Client（短超时）
var sharedThumbClient = &http.Client{
	Timeout:   30 * time.Second,
	Transport: sharedDownloadTransport,
}

// CloseDownloadClients 关闭共享下载 Transport 的空闲连接，释放资源。
// 应在程序退出或 DouyinClient.Close() 时调用。
func CloseDownloadClients() {
	sharedDownloadTransport.CloseIdleConnections()
}

// DownloadFile 下载抖音视频/图片文件到 destPath（原子写入: 先写 .tmp 再 rename）。
// 若 destPath 已存在且非空则直接跳过，返回已有文件大小。
func DownloadFile(fileURL, destPath string) (int64, error) {
	// [FIXED: P2-5] 检查 MkdirAll 错误，避免目录创建失败时返回不直观的错误信息
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return 0, fmt.Errorf("mkdir: %w", err)
	}

	// 已存在且非空则跳过
	if info, err := os.Stat(destPath); err == nil && info.Size() > 0 {
		return info.Size(), nil
	}

	req, err := http.NewRequest("GET", fileURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1")
	req.Header.Set("Referer", "https://www.douyin.com/")
	req.Header.Set("Accept", "*/*")

	resp, err := sharedVideoClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("http get video: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("video download returned %d", resp.StatusCode)
	}

	tmpPath := destPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return 0, fmt.Errorf("create tmp file: %w", err)
	}

	written, err := io.Copy(f, resp.Body)
	f.Close()
	if err != nil {
		os.Remove(tmpPath)
		return 0, fmt.Errorf("write video: %w", err)
	}

	if written == 0 {
		os.Remove(tmpPath)
		return 0, fmt.Errorf("downloaded 0 bytes")
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		return 0, fmt.Errorf("rename tmp: %w", err)
	}

	return written, nil
}

// DownloadThumb 下载封面图到 destPath。若已存在且非空非目录则跳过。
func DownloadThumb(thumbURL, destPath string) error {
	// 检查是否为非空普通文件，避免 destPath 为空目录时跳过导致封面缺失
	if info, err := os.Stat(destPath); err == nil && !info.IsDir() && info.Size() > 0 {
		return nil
	}

	req, err := http.NewRequest("GET", thumbURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15")

	resp, err := sharedThumbClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("thumb download returned %d", resp.StatusCode)
	}

	// [FIXED: P2-4] Write to a temp file first, rename on success to avoid
	// leaving a corrupted file behind on download failure.
	tmpPath := destPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(f, resp.Body)
	f.Close()
	if copyErr != nil {
		os.Remove(tmpPath)
		return copyErr
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}
