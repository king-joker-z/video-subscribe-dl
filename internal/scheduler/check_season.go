package scheduler

import (
	"fmt"
	"log"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
)

// checkSeason 检查合集源，获取合集所有视频。
// 委托 fetchAndProcessSeason 统一处理翻页、NFO、封面和下载。
func (s *Scheduler) checkSeason(src db.Source) {
	client := s.clientForSource(src)

	mid, seasonID, err := bilibili.ExtractSeasonInfo(src.URL)
	if err != nil {
		log.Printf("Extract season info failed: %v", err)
		return
	}

	upInfo, err := client.GetUPInfo(mid)
	if err != nil {
		if bilibili.IsRiskControl(err) {
			s.triggerCooldown()
			s.dl.Pause()
		} else {
			log.Printf("Get UP info failed (mid=%d): %v", mid, err)
		}
		return
	}

	if upInfo.Name != "" {
		s.db.UpsertPerson(fmt.Sprintf("%d", upInfo.MID), upInfo.Name, upInfo.Face)
		s.ensurePeopleDir(upInfo)
	}

	uploaderName := upInfo.Name
	uploaderDir := bilibili.SanitizePath(uploaderName)

	s.fetchAndProcessSeason(src, client, mid, seasonID, uploaderName, uploaderDir, upInfo)
}
