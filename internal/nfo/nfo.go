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
	Platform      string    // "bilibili" 或 "douyin"，用于区分平台相关字段
	BvID          string
	Title         string
	Description   string
	UploaderName  string
	UploaderFace  string
	UploadDate    time.Time
	FavDate       time.Time // 收藏时间（可选，nfo_time_type=favtime 时使用）
	Duration      int       // 秒
	Tags          []string
	ViewCount     int64
	LikeCount     int64
	CoinCount     int64
	DanmakuCount  int64
	ReplyCount    int64
	FavoriteCount int64
	ShareCount    int64
	Thumbnail     string
	WebpageURL    string
	TName         string // B站分区名称
}

type TVShowMeta struct {
	Platform     string // 可选，默认 "bilibili"
	Title        string
	Plot         string
	UploaderName string
	UploaderFace string
	Premiered    string
	Poster       string
	Tags         []string
}

type SeasonMeta struct {
	Title        string
	Plot         string
	SeasonNumber int
	Poster       string
	Year         int
}

type EpisodeMeta struct {
	Title        string
	Plot         string
	Season       int
	Episode      int
	BvID         string
	UploaderName string
	UploadDate   time.Time
	Duration     int // 秒
}

type PersonMeta struct {
	Name  string
	Thumb string
	MID   int64
	Sign  string
	Level int
	Sex   string
}

// === XML 结构（极空间极影视 / Emby / Jellyfin 兼容） ===

type movieNFO struct {
	XMLName   xml.Name `xml:"movie"`
	Title     string   `xml:"title"`
	Plot      string   `xml:"plot"`
	Outline   string   `xml:"outline,omitempty"`
	Year      int      `xml:"year,omitempty"`
	Premiered string   `xml:"premiered,omitempty"`
	Studio    string   `xml:"studio,omitempty"`
	Runtime   int      `xml:"runtime,omitempty"`
	UniqueID  uniqueID `xml:"uniqueid"`
	Ratings   *ratings `xml:"ratings,omitempty"`
	Genres    []string `xml:"genre"`
	Tags      []string `xml:"tag"`
	Actors    []actor  `xml:"actor"`
	Website   string   `xml:"website,omitempty"`
}

type tvshowNFO struct {
	XMLName   xml.Name `xml:"tvshow"`
	Title     string   `xml:"title"`
	Plot      string   `xml:"plot,omitempty"`
	Premiered string   `xml:"premiered,omitempty"`
	Studio    string   `xml:"studio,omitempty"`
	Ratings   *ratings `xml:"ratings,omitempty"`
	Genres    []string `xml:"genre"`
	Tags      []string `xml:"tag"`
	Thumb     string   `xml:"thumb,omitempty"`
	Actors    []actor  `xml:"actor"`
}

type seasonNFO struct {
	XMLName      xml.Name `xml:"season"`
	Title        string   `xml:"title"`
	Plot         string   `xml:"plot,omitempty"`
	SeasonNumber int      `xml:"seasonnumber"`
	Year         int      `xml:"year,omitempty"`
	Thumb        string   `xml:"thumb,omitempty"`
}

type personNFO struct {
	XMLName   xml.Name `xml:"person"`
	Name      string   `xml:"name"`
	Thumb     string   `xml:"thumb,omitempty"`
	Biography string   `xml:"biography,omitempty"`
	UniqueID  string   `xml:"uniqueid,omitempty"`
	Website   string   `xml:"website,omitempty"`
}

type episodedetailsNFO struct {
	XMLName  xml.Name `xml:"episodedetails"`
	Title    string   `xml:"title"`
	Plot     string   `xml:"plot,omitempty"`
	Season   int      `xml:"season"`
	Episode  int      `xml:"episode"`
	Aired    string   `xml:"aired,omitempty"`
	Runtime  int      `xml:"runtime,omitempty"`
	UniqueID uniqueID `xml:"uniqueid"`
	Studio   string   `xml:"studio,omitempty"`
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

type ratings struct {
	Rating []ratingEntry `xml:"rating"`
}

type ratingEntry struct {
	Name    string  `xml:"name,attr"`
	Max     int     `xml:"max,attr"`
	Default string  `xml:"default,attr,omitempty"`
	Value   float64 `xml:"value"`
	Votes   int64   `xml:"votes"`
}

// === B站分区 → genre 映射 ===

var tidToGenre = map[string]string{
	// 动画
	"MAD·AMV": "动画",
	"MMD·3D":  "动画",
	"短片·手书":   "动画",
	"综合":      "动画",
	// 番剧
	"连载动画": "番剧",
	"完结动画": "番剧",
	"资讯":   "番剧",
	"官方延伸": "番剧",
	// 音乐
	"原创音乐": "音乐",
	"翻唱":   "音乐",
	"演奏":   "音乐",
	"MV":   "音乐",
	"音乐现场": "音乐",
	// 舞蹈
	"宅舞": "舞蹈",
	"街舞": "舞蹈",
	// 游戏
	"单机游戏": "游戏",
	"网络游戏": "游戏",
	"手机游戏": "游戏",
	"电子竞技": "游戏",
	// 知识
	"科学科普":     "知识",
	"社科·法律·心理": "知识",
	"人文历史":     "知识",
	"财经商业":     "知识",
	"校园学习":     "知识",
	"职业职场":     "知识",
	// 科技
	"数码":       "科技",
	"软件应用":     "科技",
	"计算机技术":    "科技",
	"工业·工程·机械": "科技",
	// 生活
	"搞笑":   "生活",
	"家居房产": "生活",
	"手工":   "生活",
	"绘画":   "生活",
	"日常":   "生活",
	"出行":   "生活",
	// 美食
	"美食制作": "美食",
	"美食侦探": "美食",
	"美食测评": "美食",
	// 汽车
	"赛车":   "汽车",
	"改装玩车": "汽车",
	"新能源车": "汽车",
	"购车攻略": "汽车",
	// 时尚
	"美妆护肤":  "时尚",
	"仿妆cos": "时尚",
	"穿搭":    "时尚",
	// 运动
	"篮球": "运动",
	"足球": "运动",
	"健身": "运动",
	// 影视
	"影视杂谈":  "影视",
	"影视剪辑":  "影视",
	"预告·资讯": "影视",
	// 娱乐
	"综艺":   "娱乐",
	"明星八卦": "娱乐",
	// 鬼畜
	"鬼畜调教":       "鬼畜",
	"音MAD":       "鬼畜",
	"人力VOCALOID": "鬼畜",
}

// mapTNameToGenre 将 B站分区名映射到通用 genre
func mapTNameToGenre(tname string) string {
	if genre, ok := tidToGenre[tname]; ok {
		return genre
	}
	// 直接用分区名作为 genre
	if tname != "" {
		return tname
	}
	return ""
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
	if len([]rune(outline)) > 200 {
		outline = string([]rune(outline)[:200]) + "..."
	}

	// 根据 FavDate 是否设置来决定 NFO 中的日期
	dateForNFO := meta.UploadDate
	if !meta.FavDate.IsZero() {
		dateForNFO = meta.FavDate
	}
	year := dateForNFO.Year()
	premiered := dateForNFO.Format("2006-01-02")

	runtime := 0
	if meta.Duration > 0 {
		runtime = meta.Duration / 60
		if runtime == 0 {
			runtime = 1
		}
	}

	// Platform fallback: 默认 bilibili 以兼容旧代码
	platform := meta.Platform
	if platform == "" {
		platform = "bilibili"
	}

	var actors []actor
	if meta.UploaderName != "" {
		role := "UP主"
		switch platform {
		case "douyin":
			role = "作者"
		case "pornhub":
			role = "P主"
		}
		actors = append(actors, actor{
			Name:  meta.UploaderName,
			Role:  role,
			Thumb: meta.UploaderFace,
		})
	}

	// 标签 → genre 映射
	tags := meta.Tags
	genreSet := make(map[string]bool)
	var genres []string

	// 先加 B站分区 genre
	if genre := mapTNameToGenre(meta.TName); genre != "" {
		genreSet[genre] = true
		genres = append(genres, genre)
	}
	// 从 tags 补充（最多 5 个 genre）
	for _, tag := range tags {
		if len(genres) >= 5 {
			break
		}
		g := mapTNameToGenre(tag)
		if g == "" {
			g = tag
		}
		if !genreSet[g] {
			genreSet[g] = true
			genres = append(genres, g)
		}
	}

	// 评分：基于互动数据计算（满分 10）
	var ratingEntries *ratings
	if meta.ViewCount > 0 {
		// B站评分算法：综合 点赞率 + 投币率 + 收藏率
		// 点赞率权重最大（最常见互动）
		likeRate := float64(meta.LikeCount) / float64(meta.ViewCount)
		coinRate := float64(meta.CoinCount) / float64(meta.ViewCount)
		favRate := float64(meta.FavoriteCount) / float64(meta.ViewCount)

		// 加权打分（likeRate 通常 2-5%, coinRate 0.5-2%, favRate 0.5-3%）
		score := likeRate*100 + coinRate*200 + favRate*150
		// 归一化到 1-10 分（sigmoid-like）
		rating := 10.0 * score / (score + 10.0)
		if rating < 1 {
			rating = 1
		}
		if rating > 10 {
			rating = 10
		}

		ratingEntries = &ratings{
			Rating: []ratingEntry{
				{
					Name:    platform,
					Max:     10,
					Default: "true",
					Value:   float64(int(rating*10)) / 10, // 保留 1 位小数
					Votes:   meta.ViewCount,
				},
			},
		}
	}

	nfo := movieNFO{
		Title:     meta.Title,
		Plot:      plot,
		Outline:   outline,
		Year:      year,
		Premiered: premiered,
		Studio:    meta.UploaderName,
		Runtime:   runtime,
		UniqueID:  uniqueID{Type: platform, Default: "true", Value: meta.BvID},
		Ratings:   ratingEntries,
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

	platform := meta.Platform
	if platform == "" {
		platform = "bilibili"
	}

	var actors []actor
	if meta.UploaderName != "" {
		role := "UP主"
		switch platform {
		case "douyin":
			role = "作者"
		case "pornhub":
			role = "P主"
		}
		actors = append(actors, actor{
			Name:  meta.UploaderName,
			Role:  role,
			Thumb: meta.UploaderFace,
		})
	}

	// 从 tags 生成 genre
	var genres []string
	genreSet := make(map[string]bool)
	for _, tag := range meta.Tags {
		g := mapTNameToGenre(tag)
		if g == "" {
			g = tag
		}
		if !genreSet[g] && len(genres) < 5 {
			genreSet[g] = true
			genres = append(genres, g)
		}
	}

	nfo := tvshowNFO{
		Thumb:     meta.Poster,
		Title:     meta.Title,
		Plot:      meta.Plot,
		Premiered: meta.Premiered,
		Studio:    meta.UploaderName,
		Genres:    genres,
		Tags:      meta.Tags,
		Actors:    actors,
	}
	return writeXML(filepath.Join(dir, "tvshow.nfo"), nfo)
}

// GenerateSeasonNFO 在 season 目录下生成 season.nfo
func GenerateSeasonNFO(meta *SeasonMeta, dir string) error {
	os.MkdirAll(dir, 0755)
	nfo := seasonNFO{
		Title:        meta.Title,
		Plot:         meta.Plot,
		SeasonNumber: meta.SeasonNumber,
		Year:         meta.Year,
		Thumb:        meta.Poster,
	}
	return writeXML(filepath.Join(dir, "season.nfo"), nfo)
}

// GeneratePersonNFO 生成 person.nfo
func GeneratePersonNFO(meta *PersonMeta, dir string) error {
	os.MkdirAll(dir, 0755)
	biography := ""
	if meta.Sign != "" {
		biography = meta.Sign
	}
	if meta.Level > 0 {
		biography += fmt.Sprintf("\nLv.%d", meta.Level)
	}
	if meta.MID > 0 {
		biography += fmt.Sprintf(" | UID: %d", meta.MID)
	}
	if meta.Sex != "" && meta.Sex != "保密" {
		biography += fmt.Sprintf(" | %s", meta.Sex)
	}
	nfo := personNFO{
		Name:      meta.Name,
		Thumb:     meta.Thumb,
		Biography: strings.TrimSpace(biography),
		UniqueID:  fmt.Sprintf("%d", meta.MID),
		Website:   fmt.Sprintf("https://space.bilibili.com/%d", meta.MID),
	}
	return writeXML(filepath.Join(dir, "person.nfo"), nfo)
}

// GenerateEpisodeNFO 生成分P视频的 episodedetails NFO
func GenerateEpisodeNFO(meta *EpisodeMeta, nfoPath string) error {
	aired := ""
	if !meta.UploadDate.IsZero() {
		aired = meta.UploadDate.Format("2006-01-02")
	}

	runtime := 0
	if meta.Duration > 0 {
		runtime = meta.Duration / 60
		if runtime == 0 {
			runtime = 1
		}
	}

	nfo := episodedetailsNFO{
		Title:    meta.Title,
		Plot:     meta.Plot,
		Season:   meta.Season,
		Episode:  meta.Episode,
		Aired:    aired,
		Runtime:  runtime,
		UniqueID: uniqueID{Type: "bilibili", Default: "true", Value: meta.BvID}, // Episode NFO 目前只有 B站用
		Studio:   meta.UploaderName,
	}
	return writeXML(nfoPath, nfo)
}

// GenerateEpisodeNFOFromPath 从视频文件路径推导 NFO 路径并生成 episodedetails
func GenerateEpisodeNFOFromPath(meta *EpisodeMeta, videoFilePath string) error {
	ext := filepath.Ext(videoFilePath)
	nfoPath := strings.TrimSuffix(videoFilePath, ext) + ".nfo"
	return GenerateEpisodeNFO(meta, nfoPath)
}

// === 内部 ===

func writeXML(path string, data interface{}) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()

	// [FIXED: P2-6] Check WriteString error so a partial-write is detected early.
	if _, err := f.WriteString(xml.Header); err != nil {
		return fmt.Errorf("write xml header: %w", err)
	}
	enc := xml.NewEncoder(f)
	enc.Indent("", "  ")
	if err := enc.Encode(data); err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	log.Printf("NFO: %s", path)
	return nil
}
