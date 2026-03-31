package scanner

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/nfo"
)

// 预编译正则
var reBVID = regexp.MustCompile(`\[(BV[a-zA-Z0-9]+)\]`)

type Scanner struct {
	db          *db.DB
	downloadDir string
}

func New(database *db.DB, downloadDir string) *Scanner {
	return &Scanner{db: database, downloadDir: downloadDir}
}

// ScanAndSync 扫描本地文件，补录数据库 + 生成缺失 NFO
func (s *Scanner) ScanAndSync() (int, int, error) {
	scanned, nfoGen := 0, 0

	filepath.Walk(s.downloadDir, func(path string, info os.FileInfo, err error) error {
		// [FIXED: P1-4] Log walk errors instead of silently swallowing them
		if err != nil {
			log.Printf("[scanner] walk error %s: %v", path, err)
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".info.json") {
			return nil
		}

		scanned++
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		var vi map[string]interface{}
		if json.Unmarshal(data, &vi) != nil {
			return nil
		}

		id := getString(vi, "id")
		title := getString(vi, "title")
		uploader := getString(vi, "channel")
		if uploader == "" {
			uploader = getString(vi, "uploader")
		}

		// 找视频文件
		videoPath := findVideoNear(path)
		var fileSize int64
		if videoPath != "" {
			if fi, err := os.Stat(videoPath); err == nil {
				fileSize = fi.Size()
			}
		}

		// 从路径推断 uploader
		if uploader == "" {
			rel, _ := filepath.Rel(s.downloadDir, path)
			parts := strings.Split(rel, string(os.PathSeparator))
			if len(parts) >= 2 {
				uploader = parts[0]
			}
		}

		// [FIXED: P1-5] Check and log DB write failures
		if err := s.db.UpsertDownloadFromScan(id, title, uploader, videoPath, fileSize); err != nil {
			log.Printf("[scanner] upsert error: %v", err)
		}

		// 生成缺失的 NFO
		if videoPath != "" {
			ext := filepath.Ext(videoPath)
			nfoPath := strings.TrimSuffix(videoPath, ext) + ".nfo"
			if _, err := os.Stat(nfoPath); os.IsNotExist(err) {
				desc := getString(vi, "description")
				uploadDate := getString(vi, "upload_date")
				thumbnail := getString(vi, "thumbnail")

				pubTime := time.Now()
				if len(uploadDate) == 8 {
					if t, err := time.Parse("20060102", uploadDate); err == nil {
						pubTime = t
					}
				}

				var tags []string
				if t, ok := vi["tags"].([]interface{}); ok {
					for _, tag := range t {
						if s, ok := tag.(string); ok {
							tags = append(tags, s)
						}
					}
				}

				meta := &nfo.VideoMeta{
					BvID:         id,
					Title:        title,
					Description:  desc,
					UploaderName: uploader,
					UploadDate:   pubTime,
					Thumbnail:    thumbnail,
					Tags:         tags,
					WebpageURL:   getString(vi, "webpage_url"),
				}
				if nfo.GenerateVideoNFO(meta, videoPath) == nil {
					nfoGen++
				}
			}
		}

		return nil
	})

	return scanned, nfoGen, nil
}

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func findVideoNear(infoPath string) string {
	base := strings.TrimSuffix(infoPath, ".info.json")
	for _, ext := range []string{".mp4", ".mkv", ".webm", ".flv"} {
		p := base + ext
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	// 同目录找
	dir := filepath.Dir(infoPath)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext == ".mp4" || ext == ".mkv" || ext == ".webm" {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

func ExtractBvID(filename string) string {
	re := reBVID
	m := re.FindStringSubmatch(filename)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}
