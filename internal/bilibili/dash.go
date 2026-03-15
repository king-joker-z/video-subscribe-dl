package bilibili

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DASH 流信息
type DashResult struct {
	Video []DashStream `json:"video"`
	Audio []DashStream `json:"audio"`
}

type DashStream struct {
	ID        int      `json:"id"`
	BaseURL   string   `json:"baseUrl"`
	BackupURL []string `json:"backupUrl"`
	Bandwidth int      `json:"bandwidth"`
	Codecs    string   `json:"codecs"`
	CodecID   int      `json:"codecid"`
	Width     int      `json:"width"`
	Height    int      `json:"height"`
	MimeType  string   `json:"mimeType"`
}

// 视频质量 ID
const (
	QN360P     = 16
	QN480P     = 32
	QN720P     = 64
	QN1080P    = 80
	QN1080PHigh = 112
	QN4K       = 120
)

// 音频质量 ID
const (
	Audio64K    = 30216
	Audio132K   = 30232
	Audio192K   = 30280
	AudioDolby  = 30250
	AudioHiRes  = 30251
)

// 视频编码 ID
const (
	CodecAVC  = 7
	CodecHEVC = 12
	CodecAV1  = 13
)

// GetDashStreams 获取视频的 DASH 流信息
func (c *Client) GetDashStreams(bvid string, cid int64) (*DashResult, error) {
	params := url.Values{}
	params.Set("bvid", bvid)
	params.Set("cid", fmt.Sprintf("%d", cid))
	params.Set("fnval", "4048")  // 请求全部格式（DASH+杜比+Hi-Res+AV1等）
	params.Set("qn", "127")     // 请求最高画质
	params.Set("fourk", "1")    // 允许4K

	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"message"`
		Data struct {
			Dash DashResult `json:"dash"`
		} `json:"data"`
	}

	err := c.getWbi("https://api.bilibili.com/x/player/wbi/playurl", params, &resp)
	if err != nil {
		return nil, fmt.Errorf("playurl API: %w", err)
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("playurl: %d %s", resp.Code, resp.Msg)
	}

	return &resp.Data.Dash, nil
}

// SelectBestVideo 选择最优视频流
// preferCodec: "avc", "hevc", "av1", "" (auto)
// maxHeight: 0 表示不限制
func SelectBestVideo(streams []DashStream, preferCodec string, maxHeight int) *DashStream {
	if len(streams) == 0 {
		return nil
	}

	// 过滤高度限制
	var filtered []DashStream
	for _, s := range streams {
		if maxHeight > 0 && s.Height > maxHeight {
			continue
		}
		filtered = append(filtered, s)
	}
	if len(filtered) == 0 {
		filtered = streams // 没有符合的就不限制
	}

	// 排序：先按高度降序，同高度按 bandwidth 降序
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Height != filtered[j].Height {
			return filtered[i].Height > filtered[j].Height
		}
		return filtered[i].Bandwidth > filtered[j].Bandwidth
	})

	// 如果有编码偏好
	if preferCodec != "" {
		codecID := 0
		switch preferCodec {
		case "avc", "h264":
			codecID = CodecAVC
		case "hevc", "h265":
			codecID = CodecHEVC
		case "av1":
			codecID = CodecAV1
		}
		if codecID > 0 {
			// 在最高分辨率中找偏好编码
			bestHeight := filtered[0].Height
			for _, s := range filtered {
				if s.Height == bestHeight && s.CodecID == codecID {
					return &s
				}
			}
		}
	}

	return &filtered[0]
}

// SelectBestAudio 选择最优音频流
// 优先级: Hi-Res > Dolby > 192k > 132k > 64k
// 未知 ID 按 bandwidth 降序排在已知ID之后
func SelectBestAudio(streams []DashStream) *DashStream {
	if len(streams) == 0 {
		return nil
	}

	priority := map[int]int{
		AudioHiRes: 5,
		AudioDolby: 4,
		Audio192K:  3,
		Audio132K:  2,
		Audio64K:   1,
	}

	sort.Slice(streams, func(i, j int) bool {
		pi, oki := priority[streams[i].ID]
		pj, okj := priority[streams[j].ID]
		// 已知 ID 优先于未知 ID
		if oki && !okj {
			return true
		}
		if !oki && okj {
			return false
		}
		// 同类按优先级，相同优先级按 bandwidth
		if pi != pj {
			return pi > pj
		}
		return streams[i].Bandwidth > streams[j].Bandwidth
	})

	return &streams[0]
}

// DownloadDash 下载 DASH 流并合并为视频文件
// 返回最终视频文件路径
func DownloadDash(video, audio *DashStream, outputDir, filename string) (string, error) {
	os.MkdirAll(outputDir, 0755)

	videoTmp := filepath.Join(outputDir, ".video.m4s")
	audioTmp := filepath.Join(outputDir, ".audio.m4s")
	outputPath := filepath.Join(outputDir, filename+".mkv")

	// 下载视频流
	log.Printf("Downloading video: %dx%d %s (%d kbps)", video.Width, video.Height, video.Codecs, video.Bandwidth/1000)
	if err := downloadStream(video, videoTmp); err != nil {
		return "", fmt.Errorf("download video: %w", err)
	}

	// 下载音频流
	log.Printf("Downloading audio: %s (%d kbps)", audio.Codecs, audio.Bandwidth/1000)
	if err := downloadStream(audio, audioTmp); err != nil {
		os.Remove(videoTmp)
		return "", fmt.Errorf("download audio: %w", err)
	}

	// ffmpeg 合并
	log.Printf("Merging: %s", outputPath)
	err := mergeStreams(videoTmp, audioTmp, outputPath)

	// 清理临时文件
	os.Remove(videoTmp)
	os.Remove(audioTmp)

	if err != nil {
		return "", fmt.Errorf("merge: %w", err)
	}

	return outputPath, nil
}

func downloadStream(stream *DashStream, dest string) error {
	// 使用 CDN 优先级排序: upos > cn > mcdn > pcdn
	urls := StreamURLs(stream, true)

	for _, u := range urls {
		err := downloadWithResume(u, dest)
		if err == nil {
			return nil
		}
		log.Printf("Download failed from %s: %v, trying backup...", u[:80], err)
	}
	return fmt.Errorf("all URLs failed")
}

func downloadWithResume(rawURL, dest string) error {
	client := sharedLargeDownloadClient // 复用 client.go 中的共享大文件下载 Client

	// 检查已下载的大小（支持断点续传）
	var startByte int64
	if fi, err := os.Stat(dest); err == nil {
		startByte = fi.Size()
	}

	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", randUA())
	req.Header.Set("Referer", "https://www.bilibili.com")
	req.Header.Set("Origin", "https://www.bilibili.com")
	if startByte > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", startByte))
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 206 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	flags := os.O_CREATE | os.O_WRONLY
	if startByte > 0 && resp.StatusCode == 206 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}

	f, err := os.OpenFile(dest, flags, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	// 带进度的下载
	total := resp.ContentLength
	written := startByte
	buf := make([]byte, 256*1024) // 256KB buffer
	lastLog := time.Now()

	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := f.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
			written += int64(n)
			if time.Since(lastLog) > 3*time.Second {
				if total > 0 {
					pct := float64(written) / float64(total+startByte) * 100
					log.Printf("  %.1f%% (%s / %s)", pct, fmtBytes(written), fmtBytes(total+startByte))
				} else {
					log.Printf("  %s downloaded", fmtBytes(written))
				}
				lastLog = time.Now()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}

	return nil
}

func mergeStreams(videoPath, audioPath, outputPath string) error {
	args := []string{
		"-y",
		"-i", videoPath,
		"-i", audioPath,
		"-c", "copy",      // 不重新编码
		"-movflags", "+faststart",
		outputPath,
	}

	cmd := exec.Command("ffmpeg", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, string(output))
	}
	return nil
}

func fmtBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %s", float64(b)/float64(div), []string{"KB", "MB", "GB"}[exp])
}

// PageInfo 分P信息
type PageInfo struct {
	CID      int64
	Page     int
	PartName string
}

// GetVideoCID 获取视频的 CID（从 VideoDetail 中获取第一个 page 的 cid）
func GetVideoCID(detail *VideoDetail) int64 {
	if len(detail.Pages) > 0 {
		return detail.Pages[0].CID
	}
	return 0
}

// GetAllPages 返回视频所有分P的信息列表
func GetAllPages(detail *VideoDetail) []PageInfo {
	pages := make([]PageInfo, 0, len(detail.Pages))
	for _, p := range detail.Pages {
		pages = append(pages, PageInfo{
			CID:      p.CID,
			Page:     p.Page,
			PartName: p.Part,
		})
	}
	return pages
}

// FormatVideoInfo 格式化视频流信息（用于日志）
func FormatVideoInfo(s *DashStream) string {
	codec := "unknown"
	switch s.CodecID {
	case CodecAVC:
		codec = "AVC/H.264"
	case CodecHEVC:
		codec = "HEVC/H.265"
	case CodecAV1:
		codec = "AV1"
	}
	return fmt.Sprintf("%dx%d %s %dkbps", s.Width, s.Height, codec, s.Bandwidth/1000)
}

// FormatAudioInfo 格式化音频流信息
func FormatAudioInfo(s *DashStream) string {
	name := "unknown"
	switch s.ID {
	case AudioHiRes:
		name = "Hi-Res"
	case AudioDolby:
		name = "杜比全景声"
	case Audio192K:
		name = "192kbps"
	case Audio132K:
		name = "132kbps"
	case Audio64K:
		name = "64kbps"
	}
	return fmt.Sprintf("%s %s %dkbps", name, s.Codecs, s.Bandwidth/1000)
}

// SanitizeFilename 清理文件名（过滤非法字符 + 不可见Unicode字符）
func SanitizeFilename(name string) string {
	// 替换文件系统非法字符
	for _, c := range []string{"<", ">", ":", "\"", "/", "\\", "|", "?", "*"} {
		name = strings.ReplaceAll(name, c, "_")
	}

	// 过滤 Unicode 不可见/控制字符（与 SanitizePath 保持一致）
	name = strings.Map(func(r rune) rune {
		switch {
		case r <= 0x001F: // C0 控制字符
			return -1
		case r == 0x007F: // DEL
			return -1
		case r >= 0x0080 && r <= 0x009F: // C1 控制字符
			return -1
		case r == 0xFFFD: // 替换字符 \ufffd (�)
			return -1
		case r >= 0x200B && r <= 0x200F: // 零宽字符
			return -1
		case r >= 0x2028 && r <= 0x202F: // 行/段分隔符等
			return -1
		case r >= 0x2060 && r <= 0x206F: // 不可见格式字符
			return -1
		case r == 0xFEFF: // BOM/零宽不断空格
			return -1
		case r >= 0xFE00 && r <= 0xFE0F: // 变体选择器
			return -1
		case r >= 0xE0000 && r <= 0xE007F: // 标签字符
			return -1
		default:
			return r
		}
	}, name)

	// 合并连续空格并 trim
	name = spaceCollapser.ReplaceAllString(name, " ")
	name = strings.TrimSpace(name)
	if name == "" {
		return "unknown"
	}
	// 按 rune 截断到 80 字符
	runes := []rune(name)
	if len(runes) > 80 {
		name = string(runes[:80])
	}
	return name
}
