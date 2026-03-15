package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
)

// GET/POST /api/sources
func (s *Server) handleSources(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		sources, err := s.db.GetSources()
		if err != nil {
			jsonError(w, err.Error(), 500)
			return
		}
		if sources == nil {
			sources = []db.Source{}
		}
		jsonResponse(w, sources)

	case "POST":
		var source db.Source
		if err := json.NewDecoder(r.Body).Decode(&source); err != nil {
			jsonError(w, err.Error(), 400)
			return
		}
		// 默认 type 为 "up"
		if source.Type == "" {
			source.Type = "up"
		}
		// 自动识别 URL 类型
		if source.Type == "up" && source.URL != "" {
			if strings.Contains(source.URL, "favlist") {
				source.Type = "favorite"
			} else if strings.Contains(source.URL, "/lists/") && strings.Contains(source.URL, "type=season") {
				source.Type = "season"
			} else if strings.Contains(source.URL, "collectiondetail") {
				source.Type = "season"
			}
		}

		// 构建 client 用于 API 调用
		cookie := bilibili.ReadCookieFile(source.CookiesFile)
		if cookie == "" {
			cp, err := s.db.GetSetting("cookie_path")
			if err != nil {
				log.Printf("[WARN] Failed to get cookie_path from DB: %v", err)
			} else if cp != "" {
				cookie = bilibili.ReadCookieFile(cp)
			}
		}
		client := bilibili.NewClient(cookie)

		// 根据类型自动获取名称
		switch source.Type {
		case "season":
			mid, seasonID, _ := bilibili.ExtractSeasonInfo(source.URL)
			if mid > 0 && seasonID > 0 && source.Name == "" {
				if info, err := client.GetUPInfo(mid); err == nil && info.Name != "" {
					// 获取合集标题
					archives, meta, err := client.GetSeasonVideos(mid, seasonID, 1, 1)
					_ = archives
					if err == nil && meta != nil && meta.Title != "" {
						source.Name = meta.Title
					} else {
						source.Name = info.Name + " - 合集"
					}
				}
			}
		case "favorite":
			mid, mediaID, _ := bilibili.ExtractFavoriteInfo(source.URL)
			if mid > 0 && source.Name == "" {
				if info, err := client.GetUPInfo(mid); err == nil && info.Name != "" {
					if mediaID > 0 {
						// 获取收藏夹标题
						folders, err := client.GetFavoriteList(mid)
						if err == nil {
							for _, f := range folders {
								if f.ID == mediaID {
									source.Name = info.Name + " - " + f.Title
									break
								}
							}
						}
					}
					if source.Name == "" {
						source.Name = info.Name + " - 收藏夹"
					}
				}
			}
		case "watchlater":
			// 稍后再看：自动使用当前 cookie 用户
			if source.URL == "" {
				source.URL = "watchlater://0"
			}
			if source.Name == "" {
				// 通过 cookie 验证获取用户名
				result, err := client.VerifyCookie()
				if err == nil && result.LoggedIn {
					source.Name = result.Username + " - 稍后再看"
					// 更新 URL 中的 mid
					source.URL = fmt.Sprintf("watchlater://%d", result.MID)
				} else {
					source.Name = "稍后再看"
				}
			}
		default: // "up"
			if source.Name == "" && source.URL != "" {
				if mid, err := bilibili.ExtractMID(source.URL); err == nil {
					if info, err := client.GetUPInfo(mid); err == nil && info.Name != "" {
						source.Name = info.Name
					}
				}
			}
		}
		// 自动关联全局 Cookie
		if source.CookiesFile == "" {
			cookiePath, err := s.db.GetSetting("cookie_path")
			if err != nil {
				log.Printf("[WARN] Failed to get cookie_path from DB: %v", err)
			} else if cookiePath != "" {
				source.CookiesFile = cookiePath
			}
		}
		id, err := s.db.CreateSource(&source)
		if err != nil {
			jsonError(w, err.Error(), 500)
			return
		}
		jsonResponse(w, map[string]interface{}{"id": id, "name": source.Name})

	default:
		jsonError(w, "method not allowed", 405)
	}
}

// GET/PUT/DELETE /api/sources/{id} or POST /api/sources/{id}/sync
func (s *Server) handleSourceByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/sources/")
	
	// 处理 /api/sources/{id}/sync
	if strings.HasSuffix(path, "/sync") && r.Method == "POST" {
		idStr := strings.TrimSuffix(path, "/sync")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			jsonError(w, "invalid id", 400)
			return
		}
		if s.onSyncSource != nil {
			s.onSyncSource(id)
		}
		jsonResponse(w, map[string]bool{"ok": true})
		return
	}
	
	idStr := path
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		jsonError(w, "invalid id", 400)
		return
	}

	switch r.Method {
	case "GET":
		source, err := s.db.GetSource(id)
		if err != nil {
			jsonError(w, err.Error(), 500)
			return
		}
		if source == nil {
			jsonError(w, "not found", 404)
			return
		}
		jsonResponse(w, source)

	case "PUT":
		var source db.Source
		if err := json.NewDecoder(r.Body).Decode(&source); err != nil {
			jsonError(w, err.Error(), 400)
			return
		}
		source.ID = id
		if err := s.db.UpdateSource(&source); err != nil {
			jsonError(w, err.Error(), 500)
			return
		}
		jsonResponse(w, map[string]bool{"ok": true})

	case "DELETE":
		if err := s.db.DeleteSource(id); err != nil {
			jsonError(w, err.Error(), 500)
			return
		}
		jsonResponse(w, map[string]bool{"ok": true})

	default:
		jsonError(w, "method not allowed", 405)
	}
}
