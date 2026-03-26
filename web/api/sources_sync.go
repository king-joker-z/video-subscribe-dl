package api

import "net/http"

// POST /api/sources/:id/sync
func (h *SourcesHandler) HandleSync(w http.ResponseWriter, r *http.Request, id int64) {
	if h.onSyncSource != nil {
		h.onSyncSource(id)
	}
	apiOK(w, map[string]interface{}{"id": id, "message": "同步已触发"})
}

// POST /api/sources/:id/fullscan — 全量补漏扫描
func (h *SourcesHandler) HandleFullScan(w http.ResponseWriter, r *http.Request, id int64) {
	if h.onFullScanSource != nil {
		h.onFullScanSource(id)
	}
	apiOK(w, map[string]interface{}{"id": id, "message": "全量补漏扫描已触发"})
}
