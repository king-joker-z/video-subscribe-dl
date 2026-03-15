package bilibili

import (
	"net/url"
	"sort"
	"strings"
)

// CDN 排序优先级（数字越小越优先）
// upos-(服务商CDN) > cn-(自建CDN) > mcdn > pcdn/其他
func cdnPriority(rawURL string) int {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 99
	}
	host := strings.ToLower(u.Hostname())

	switch {
	case strings.HasPrefix(host, "upos-"):
		return 1 // 服务商 CDN，最优
	case strings.HasPrefix(host, "cn-"):
		return 2 // 自建 CDN
	case strings.Contains(host, "mcdn"):
		return 3 // mcdn
	case strings.Contains(host, "pcdn"):
		return 4 // pcdn
	default:
		return 5
	}
}

// SortCDNURLs 对 URL 列表按 CDN 优先级排序
// baseURL 放在 backupURLs 前面，然后整体按 CDN 优先级排序
func SortCDNURLs(baseURL string, backupURLs []string) []string {
	all := make([]string, 0, 1+len(backupURLs))
	all = append(all, baseURL)
	all = append(all, backupURLs...)

	sort.SliceStable(all, func(i, j int) bool {
		return cdnPriority(all[i]) < cdnPriority(all[j])
	})

	return all
}

// StreamURLs 获取流的所有 URL（按 CDN 优先级排序）
// enableSorting=true 时排序，false 时原序（baseURL 在前）
func StreamURLs(s *DashStream, enableSorting bool) []string {
	if !enableSorting {
		urls := make([]string, 0, 1+len(s.BackupURL))
		urls = append(urls, s.BaseURL)
		urls = append(urls, s.BackupURL...)
		return urls
	}
	return SortCDNURLs(s.BaseURL, s.BackupURL)
}
