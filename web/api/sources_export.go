package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"video-subscribe-dl/internal/db"
)

// ExportSource 导出用的 source 结构（不含 ID、时间戳等内部字段）
type ExportSource struct {
	Type               string `json:"type"`
	URL                string `json:"url"`
	Name               string `json:"name"`
	CheckInterval      int    `json:"check_interval,omitempty"`
	DownloadQuality    string `json:"download_quality,omitempty"`
	DownloadCodec      string `json:"download_codec,omitempty"`
	DownloadDanmaku    bool   `json:"download_danmaku,omitempty"`
	DownloadSubtitle   bool   `json:"download_subtitle,omitempty"`
	DownloadFilter     string `json:"download_filter,omitempty"`
	DownloadQualityMin string `json:"download_quality_min,omitempty"`
	SkipNFO            bool   `json:"skip_nfo,omitempty"`
	SkipPoster         bool   `json:"skip_poster,omitempty"`
	FilterRules        string `json:"filter_rules,omitempty"`
	UseDynamicAPI      bool   `json:"use_dynamic_api,omitempty"`
	Enabled            bool   `json:"enabled"`
}

// ExportPayload 导出文件的顶层结构
type ExportPayload struct {
	Version    string         `json:"version"`
	ExportedAt string         `json:"exported_at"`
	Count      int            `json:"count"`
	Sources    []ExportSource `json:"sources"`
}

// GET /api/sources/export — 导出所有订阅源为 JSON 文件
func (h *SourcesHandler) HandleExport(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("GET", w, r) {
		return
	}

	sources, err := h.db.GetSources()
	if err != nil {
		apiError(w, CodeInternal, "获取订阅源失败: "+err.Error())
		return
	}

	exported := make([]ExportSource, 0, len(sources))
	for _, s := range sources {
		exported = append(exported, ExportSource{
			Type:               s.Type,
			URL:                s.URL,
			Name:               s.Name,
			CheckInterval:      s.CheckInterval,
			DownloadQuality:    s.DownloadQuality,
			DownloadCodec:      s.DownloadCodec,
			DownloadDanmaku:    s.DownloadDanmaku,
			DownloadSubtitle:   s.DownloadSubtitle,
			DownloadFilter:     s.DownloadFilter,
			DownloadQualityMin: s.DownloadQualityMin,
			SkipNFO:            s.SkipNFO,
			SkipPoster:         s.SkipPoster,
			FilterRules:        s.FilterRules,
			UseDynamicAPI:      s.UseDynamicAPI,
			Enabled:            s.Enabled,
		})
	}

	payload := ExportPayload{
		Version:    "1",
		ExportedAt: time.Now().Format(time.RFC3339),
		Count:      len(exported),
		Sources:    exported,
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		apiError(w, CodeInternal, "JSON 序列化失败: "+err.Error())
		return
	}

	filename := fmt.Sprintf("vsd-sources-%s.json", time.Now().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Write(data)
}

// ImportResult 导入结果
type ImportResult struct {
	Created int      `json:"created"`
	Skipped int      `json:"skipped"`
	Errors  int      `json:"errors"`
	Details []string `json:"details"`
}

// POST /api/sources/import — 从 JSON 文件导入订阅源
func (h *SourcesHandler) HandleImport(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("POST", w, r) {
		return
	}

	var payload ExportPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		apiError(w, CodeInvalidParam, "JSON 解析失败: "+err.Error())
		return
	}

	if len(payload.Sources) == 0 {
		apiError(w, CodeInvalidParam, "导入文件中没有订阅源数据")
		return
	}

	// 获取当前所有源的 URL 用于去重
	existing, err := h.db.GetSources()
	if err != nil {
		apiError(w, CodeInternal, "读取现有订阅源失败: "+err.Error())
		return
	}
	urlSet := make(map[string]bool, len(existing))
	for _, s := range existing {
		urlSet[s.URL] = true
	}

	result := ImportResult{
		Details: make([]string, 0),
	}

	for _, es := range payload.Sources {
		if es.URL == "" {
			result.Errors++
			result.Details = append(result.Details, "跳过: 缺少 URL")
			continue
		}

		// 去重：相同 URL 不重复创建
		if urlSet[es.URL] {
			result.Skipped++
			name := es.Name
			if name == "" {
				name = es.URL
			}
			result.Details = append(result.Details, fmt.Sprintf("跳过(已存在): %s", name))
			continue
		}

		src := &db.Source{
			Type:               es.Type,
			URL:                es.URL,
			Name:               es.Name,
			CheckInterval:      es.CheckInterval,
			DownloadQuality:    es.DownloadQuality,
			DownloadCodec:      es.DownloadCodec,
			DownloadDanmaku:    es.DownloadDanmaku,
			DownloadSubtitle:   es.DownloadSubtitle,
			DownloadFilter:     es.DownloadFilter,
			DownloadQualityMin: es.DownloadQualityMin,
			SkipNFO:            es.SkipNFO,
			SkipPoster:         es.SkipPoster,
			FilterRules:        es.FilterRules,
			UseDynamicAPI:      es.UseDynamicAPI,
			Enabled:            es.Enabled,
		}

		if src.Type == "" {
			src.Type = "up"
		}
		if src.CheckInterval <= 0 {
			src.CheckInterval = 7200
		}
		if src.DownloadQuality == "" {
			src.DownloadQuality = "best"
		}
		if src.DownloadCodec == "" {
			src.DownloadCodec = "all"
		}

		id, err := h.db.CreateSource(src)
		if err != nil {
			result.Errors++
			result.Details = append(result.Details, fmt.Sprintf("失败(%s): %v", es.Name, err))
			continue
		}

		result.Created++
		urlSet[es.URL] = true
		result.Details = append(result.Details, fmt.Sprintf("创建: %s (id=%d)", es.Name, id))
		log.Printf("[source/import] Created: id=%d, name=%s, type=%s", id, es.Name, es.Type)
	}

	log.Printf("[source/import] Import complete: created=%d, skipped=%d, errors=%d",
		result.Created, result.Skipped, result.Errors)
	apiOK(w, result)
}
