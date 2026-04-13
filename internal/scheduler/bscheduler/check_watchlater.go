package bscheduler

import (
	"fmt"
	"log"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
)

// CheckWatchLater 检查稍后再看列表
func (s *BiliScheduler) CheckWatchLater(src db.Source) {
	client := s.clientForSource(src)

	videos, err := client.GetWatchLater()
	if err != nil {
		log.Printf("[bscheduler] Get watch later list failed: %v", err)
		return
	}

	log.Printf("[bscheduler] Watch later: %d videos", len(videos))

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
