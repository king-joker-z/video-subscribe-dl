package scheduler

import (
	"errors"
	"fmt"
	"log"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
)

// checkWatchLater 检查稍后再看列表
func (s *Scheduler) checkWatchLater(src db.Source) {
	client := s.clientForSource(src)

	videos, err := client.GetWatchLater()
	if err != nil {
		if errors.Is(err, bilibili.ErrRateLimited) {
			s.triggerCooldown()
			s.dl.Pause()
			return
		}
		log.Printf("Get watch later list failed: %v", err)
		return
	}

	log.Printf("Watch later: %d videos", len(videos))

	for _, v := range videos {
		if v.BvID == "" {
			continue
		}
		uploaderName := v.Owner.Name
		uploaderDir := bilibili.SanitizePath(uploaderName)
		ownerInfo := &bilibili.UPInfo{MID: v.Owner.MID, Name: v.Owner.Name, Face: v.Owner.Face}
		if ownerInfo.Name != "" {
			s.db.UpsertPerson(fmt.Sprintf("%d", ownerInfo.MID), ownerInfo.Name, ownerInfo.Face)
			s.ensurePeopleDir(ownerInfo)
		}
		s.processOneVideo(src, client, v.BvID, v.Title, v.Pic, uploaderName, uploaderDir, "", ownerInfo)
	}
}
