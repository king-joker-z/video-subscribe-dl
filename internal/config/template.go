package config

import (
	"bytes"
	"strings"
	"text/template"
)

const DefaultFilenameTemplate = "{{.Title}} [{{.BvID}}]"

// FilenameVars 文件名模板可用变量
type FilenameVars struct {
	Title        string // 视频标题
	BvID         string // BV 号
	UploaderName string // UP 主名称
	Quality      string // 画质描述
	Codec        string // 编码格式
	Duration     int    // 时长（秒）
	PartIndex    int    // 分P 索引（从 1 开始）
	PartTitle    string // 分P 标题
	PubDate      string // 发布日期 (YYYY-MM-DD)
}

// RenderFilename 使用模板渲染文件名
func RenderFilename(tmpl string, vars FilenameVars) string {
	if tmpl == "" {
		tmpl = DefaultFilenameTemplate
	}

	t, err := template.New("filename").Parse(tmpl)
	if err != nil {
		// 模板解析失败，回退到默认
		t, _ = template.New("filename").Parse(DefaultFilenameTemplate)
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, vars); err != nil {
		// 执行失败，回退
		return vars.Title + " [" + vars.BvID + "]"
	}

	result := buf.String()
	// 清理非法文件名字符
	result = sanitizeTemplateName(result)
	return result
}

// sanitizeTemplateName 清理文件名中的非法字符
func sanitizeTemplateName(name string) string {
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
		"\n", " ",
		"\r", "",
	)
	result := replacer.Replace(name)
	// 去除首尾空白
	result = strings.TrimSpace(result)
	if result == "" {
		result = "untitled"
	}
	return result
}
