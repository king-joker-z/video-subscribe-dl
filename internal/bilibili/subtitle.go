package bilibili

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Subtitle 描述一条字幕轨道信息
type Subtitle struct {
	ID        int64  `json:"id"`
	Lan       string `json:"lan"`        // 语言代码: zh-CN, en, ja 等
	LanDoc    string `json:"lan_doc"`    // 语言描述: 中文（自动生成）, English 等
	SubtitleURL string `json:"subtitle_url"` // 字幕 JSON URL
	IsAI      bool   `json:"ai_type"`    // 是否为 AI 自动生成（ai_type > 0）
}

// subtitleJSON B 站字幕 JSON 格式
type subtitleJSON struct {
	Body []subtitleLine `json:"body"`
}

type subtitleLine struct {
	From    float64 `json:"from"`
	To      float64 `json:"to"`
	Content string  `json:"content"`
}

// GetSubtitles 获取视频字幕列表
func (c *Client) GetSubtitles(bvid string, cid int64) ([]Subtitle, error) {
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Subtitle struct {
				List []struct {
					ID          int64  `json:"id"`
					Lan         string `json:"lan"`
					LanDoc      string `json:"lan_doc"`
					SubtitleURL string `json:"subtitle_url"`
					AIType      int    `json:"ai_type"` // 0=人工, 1=AI生成
				} `json:"list"`
			} `json:"subtitle"`
		} `json:"data"`
	}

	url := fmt.Sprintf("https://api.bilibili.com/x/player/v2?bvid=%s&cid=%d", bvid, cid)
	if err := c.get(url, &resp); err != nil {
		return nil, err
	}

	var subs []Subtitle
	for _, s := range resp.Data.Subtitle.List {
		sub := Subtitle{
			ID:          s.ID,
			Lan:         s.Lan,
			LanDoc:      s.LanDoc,
			SubtitleURL: s.SubtitleURL,
			IsAI:        s.AIType > 0,
		}
		subs = append(subs, sub)
	}
	return subs, nil
}

// DownloadSubtitleAsSRT 下载字幕并转换为 SRT 格式
// videoPath 为视频文件完整路径，用于推导字幕文件名
// 返回已下载的字幕文件路径列表
func DownloadSubtitleAsSRT(subs []Subtitle, videoDir, baseName string) []string {
	var downloaded []string

	for _, sub := range subs {
		subURL := sub.SubtitleURL
		if subURL == "" {
			continue
		}
		// B 站返回的 URL 可能缺少协议头
		if strings.HasPrefix(subURL, "//") {
			subURL = "https:" + subURL
		}

		// 构造文件名: baseName.zh-CN.srt 或 baseName.zh-CN.ai.srt
		var srtName string
		if sub.IsAI {
			srtName = fmt.Sprintf("%s.%s.ai.srt", baseName, sub.Lan)
		} else {
			srtName = fmt.Sprintf("%s.%s.srt", baseName, sub.Lan)
		}
		srtPath := filepath.Join(videoDir, srtName)

		// 已存在则跳过
		if _, err := os.Stat(srtPath); err == nil {
			continue
		}

		// 下载 JSON 字幕
		jsonData, err := fetchSubtitleJSON(subURL)
		if err != nil {
			log.Printf("  Subtitle download failed (%s): %v", sub.Lan, err)
			continue
		}

		// 转换为 SRT
		srtContent := convertToSRT(jsonData)
		if err := os.WriteFile(srtPath, []byte(srtContent), 0644); err != nil {
			log.Printf("  Subtitle write failed (%s): %v", sub.Lan, err)
			continue
		}

		aiTag := ""
		if sub.IsAI {
			aiTag = " [AI]"
		}
		log.Printf("  Subtitle saved: %s (%s%s)", srtPath, sub.LanDoc, aiTag)
		downloaded = append(downloaded, srtPath)
	}

	return downloaded
}

func fetchSubtitleJSON(url string) (*subtitleJSON, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", randUA())
	req.Header.Set("Referer", "https://www.bilibili.com")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var data subtitleJSON
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("parse subtitle JSON: %w", err)
	}
	return &data, nil
}

// convertToSRT 将 B 站 JSON 字幕转换为 SRT 格式
func convertToSRT(data *subtitleJSON) string {
	var sb strings.Builder
	for i, line := range data.Body {
		fmt.Fprintf(&sb, "%d\n", i+1)
		fmt.Fprintf(&sb, "%s --> %s\n", formatSRTTime(line.From), formatSRTTime(line.To))
		fmt.Fprintf(&sb, "%s\n\n", line.Content)
	}
	return sb.String()
}

// formatSRTTime 将秒数转换为 SRT 时间格式 (HH:MM:SS,mmm)
func formatSRTTime(seconds float64) string {
	h := int(seconds) / 3600
	m := (int(seconds) % 3600) / 60
	s := int(seconds) % 60
	ms := int((seconds - float64(int(seconds))) * 1000)
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}
