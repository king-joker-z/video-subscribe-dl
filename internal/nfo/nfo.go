package nfo

import (
	"encoding/xml"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// === 输入结构 ===

type VideoMeta struct {
	BvID          string
	Title         string
	Description   string
	UploaderName  string
	UploaderFace  string
	UploadDate    time.Time
	Duration      int // 秒
	Tags          []string
	ViewCount     int64
	LikeCount     int64
	Thumbnail     string
	WebpageURL    string
}

type TVShowMeta struct {
	Title        string
	Plot         string
	UploaderName string
	UploaderFace string
	Premiered    string
	Poster       string
}

type PersonMeta struct {
	Name  string
	Thumb string
}

// === XML 结构（极空间极影视兼容） ===

type movieNFO struct {
	XMLName   xml.Name  `xml:"movie"`
	Title     string    `xml:"title"`
	Plot      string    `xml:"plot"`
	Outline   string    `xml:"outline,omitempty"`
	Year      int       `xml:"year,omitempty"`
	Premiered string    `xml:"premiered,omitempty"`
	Studio    string    `xml:"studio,omitempty"`
	Runtime   int       `xml:"runtime,omitempty"`
	UniqueID  uniqueID  `xml:"uniqueid"`
	Genres    []string  `xml:"genre"`
	Tags      []string  `xml:"tag"`
	Actors    []actor   `xml:"actor"`
	Website   string    `xml:"website,omitempty"`
}

type tvshowNFO struct {
	XMLName   xml.Name `xml:"tvshow"`
	Title     string   `xml:"title"`
	Plot      string   `xml:"plot,omitempty"`
	Premiered string   `xml:"premiered,omitempty"`
	Studio    string   `xml:"studio,omitempty"`
	Actors    []actor  `xml:"actor"`
}

type personNFO struct {
	XMLName xml.Name `xml:"person"`
	Name    string   `xml:"name"`
	Thumb   string   `xml:"thumb,omitempty"`
}

type actor struct {
	Name  string `xml:"name"`
	Role  string `xml:"role,omitempty"`
	Thumb string `xml:"thumb,omitempty"`
}

type uniqueID struct {
	Type    string `xml:"type,attr"`
	Default string `xml:"default,attr"`
	Value   string `xml:",chardata"`
}

// === 生成函数 ===

// GenerateMovieNFO 生成视频 NFO（极空间极影视格式）
// nfoPath 是 .nfo 文件的完整路径
func GenerateMovieNFO(meta *VideoMeta, nfoPath string) error {
	plot := meta.Description
	if plot == "" {
		plot = meta.Title
	}
	outline := plot
	if len(outline) > 200 {
		outline = outline[:200] + "..."
	}

	year := meta.UploadDate.Year()
	premiered := meta.UploadDate.Format("2006-01-02")

	runtime := 0
	if meta.Duration > 0 {
		runtime = meta.Duration / 60
		if runtime == 0 {
			runtime = 1
		}
	}

	var actors []actor
	if meta.UploaderName != "" {
		actors = append(actors, actor{
			Name:  meta.UploaderName,
			Role:  "UP主",
			Thumb: meta.UploaderFace,
		})
	}

	tags := meta.Tags
	var genres []string
	for i := 0; i < len(tags) && i < 3; i++ {
		genres = append(genres, tags[i])
	}

	nfo := movieNFO{
		Title:     meta.Title,
		Plot:      plot,
		Outline:   outline,
		Year:      year,
		Premiered: premiered,
		Studio:    meta.UploaderName,
		Runtime:   runtime,
		UniqueID:  uniqueID{Type: "bilibili", Default: "true", Value: meta.BvID},
		Genres:    genres,
		Tags:      tags,
		Actors:    actors,
		Website:   meta.WebpageURL,
	}

	return writeXML(nfoPath, nfo)
}

// GenerateVideoNFO 从视频文件路径推导 NFO 路径并生成
func GenerateVideoNFO(meta *VideoMeta, videoFilePath string) error {
	ext := filepath.Ext(videoFilePath)
	nfoPath := strings.TrimSuffix(videoFilePath, ext) + ".nfo"
	return GenerateMovieNFO(meta, nfoPath)
}

// GenerateTVShowNFO 在目录下生成 tvshow.nfo
func GenerateTVShowNFO(meta *TVShowMeta, dir string) error {
	os.MkdirAll(dir, 0755)
	var actors []actor
	if meta.UploaderName != "" {
		actors = append(actors, actor{
			Name:  meta.UploaderName,
			Role:  "UP主",
			Thumb: meta.UploaderFace,
		})
	}
	nfo := tvshowNFO{
		Title:     meta.Title,
		Plot:      meta.Plot,
		Premiered: meta.Premiered,
		Studio:    meta.UploaderName,
		Actors:    actors,
	}
	return writeXML(filepath.Join(dir, "tvshow.nfo"), nfo)
}

// GeneratePersonNFO 生成 person.nfo
func GeneratePersonNFO(meta *PersonMeta, dir string) error {
	os.MkdirAll(dir, 0755)
	nfo := personNFO{Name: meta.Name, Thumb: meta.Thumb}
	return writeXML(filepath.Join(dir, "person.nfo"), nfo)
}

// === 内部 ===

func writeXML(path string, data interface{}) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()

	f.WriteString(xml.Header)
	enc := xml.NewEncoder(f)
	enc.Indent("", "  ")
	if err := enc.Encode(data); err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	log.Printf("NFO: %s", path)
	return nil
}
